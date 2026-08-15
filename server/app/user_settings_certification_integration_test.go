//go:build integration

// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/cluster"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/sqlstore"
	"github.com/sudosylabs/proctor/server/testlib"
)

type userSettingsRecordingCluster struct {
	nodeID string
	mu     sync.Mutex
	events []*cluster.Message
}

func (c *userSettingsRecordingCluster) NodeID() string            { return c.nodeID }
func (*userSettingsRecordingCluster) Start(context.Context) error { return nil }
func (*userSettingsRecordingCluster) Stop(context.Context) error  { return nil }
func (*userSettingsRecordingCluster) Ping(context.Context) error  { return nil }
func (*userSettingsRecordingCluster) SendToNode(context.Context, string, *cluster.Message) error {
	return nil
}
func (*userSettingsRecordingCluster) RegisterHandler(cluster.Event, cluster.Handler) error {
	return nil
}
func (c *userSettingsRecordingCluster) Broadcast(_ context.Context, message *cluster.Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, message.Clone())
	return nil
}

func (c *userSettingsRecordingCluster) settingsEvents() []*cluster.Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	var result []*cluster.Message
	for _, event := range c.events {
		if event.Event == cluster.Event("websocket.publish") &&
			bytes.Contains(event.Data, []byte(`"event":"user_settings_changed"`)) {
			result = append(result, event.Clone())
		}
	}
	return result
}

func TestUserSettingsTwoNodePostgreSQLConvergencePublishesOnePrivateRefetchHint(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	if dataSource == "" {
		t.Fatal("PROCTOR_TEST_DATABASE_URL is not set")
	}
	primaryStore := openAuthenticationStore(t, dataSource)
	secondaryStore := openAdditionalUserSettingsStore(t, dataSource)
	primaryCluster := &userSettingsRecordingCluster{nodeID: "settings-node-a"}
	secondaryCluster := &userSettingsRecordingCluster{nodeID: "settings-node-b"}
	primary := testlib.Setup(t, testlib.WithStore(primaryStore), testlib.WithCluster(primaryCluster))
	secondary := testlib.Setup(t, testlib.WithStore(secondaryStore), testlib.WithCluster(secondaryCluster))
	ctx := context.Background()
	if _, err := primaryStore.Institution().Save(ctx, &model.Institution{
		Name: "settings-two-node", DisplayName: "Settings Two Node",
	}); err != nil {
		t.Fatal(err)
	}
	const password = "correct horse battery staple"
	user, err := primary.App.CreateLocalUser(ctx, &model.User{
		Username: "settings-two-node-user", Email: "settings-two-node-user@example.edu",
	}, password)
	if err != nil {
		t.Fatal(err)
	}
	login, err := primary.App.Login(ctx, application.Invocation{}, application.LoginCommand{
		LoginID: user.Username, Password: password, ClientType: model.SessionClientDesktop, Source: "127.0.0.1:1",
	})
	if err != nil {
		t.Fatal(err)
	}
	principal, err := primary.App.AuthenticateAccess(ctx, login.Tokens.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	invocation := application.NewInvocation(*principal, model.RequestMetadata{RequestID: "settings-two-node"})
	before, err := primary.App.ReadOwnUserSettings(ctx, invocation)
	if err != nil {
		t.Fatal(err)
	}

	type outcome struct {
		result application.UserSettingsReplacementResult
		err    error
	}
	outcomes := make(chan outcome, 2)
	for _, candidate := range []struct {
		app    *application.App
		source string
		key    string
	}{
		{app: primary.App, source: "{\"workbench.colorTheme\":\"private-a\"}\n", key: "settings-node-a"},
		{app: secondary.App, source: "{\"workbench.colorTheme\":\"private-b\"}\n", key: "settings-node-b"},
	} {
		go func(candidate struct {
			app    *application.App
			source string
			key    string
		}) {
			result, replaceErr := candidate.app.ReplaceOwnUserSettings(ctx, invocation, application.ReplaceOwnUserSettingsCommand{
				Source: candidate.source, FormatVersion: 1,
				ExpectedRevision: before.Revision, IdempotencyKey: candidate.key,
			})
			outcomes <- outcome{result: result, err: replaceErr}
		}(candidate)
	}
	var winner application.UserSettingsReplacementResult
	successes, conflicts := 0, 0
	for range 2 {
		completed := <-outcomes
		switch {
		case completed.err == nil:
			successes++
			winner = completed.result
		case application.Is(completed.err, "user_settings.revision_conflict"):
			conflicts++
		default:
			t.Fatalf("concurrent replacement error = %v", completed.err)
		}
	}
	if successes != 1 || conflicts != 1 || !winner.Changed {
		t.Fatalf("concurrent outcomes success/conflict/winner = %d/%d/%#v", successes, conflicts, winner)
	}
	primaryView, err := primary.App.ReadOwnUserSettings(ctx, invocation)
	if err != nil {
		t.Fatal(err)
	}
	secondaryView, err := secondary.App.ReadOwnUserSettings(ctx, invocation)
	if err != nil {
		t.Fatal(err)
	}
	if primaryView.Revision != winner.Revision || secondaryView.Revision != winner.Revision ||
		primaryView.Source != secondaryView.Source {
		t.Fatalf("authoritative views diverged: primary=%#v secondary=%#v winner=%#v", primaryView, secondaryView, winner)
	}
	events := append(primaryCluster.settingsEvents(), secondaryCluster.settingsEvents()...)
	if len(events) != 1 {
		t.Fatalf("settings cluster events = %d, want one winning refetch hint", len(events))
	}
	payload := events[0].Data
	if !bytes.Contains(payload, []byte(winner.Revision.String())) ||
		!bytes.Contains(payload, []byte(user.ID.String())) {
		t.Fatalf("settings event lacks winning metadata: %s", payload)
	}
	for _, forbidden := range []string{
		"private-a", "private-b", "colorTheme", `"source":`, `"idempotency_key":`, `"session_id":`,
	} {
		if bytes.Contains(payload, []byte(forbidden)) {
			t.Fatalf("settings event exposed %q: %s", forbidden, payload)
		}
	}
}

type userSettingsUnknownOutcomeStore struct {
	store.Store
	failNext atomic.Bool
}

func (s *userSettingsUnknownOutcomeStore) UserSettings() store.UserSettingsStore {
	return userSettingsUnknownOutcomeAdapter{delegate: s.Store.UserSettings(), failNext: &s.failNext}
}

type userSettingsUnknownOutcomeAdapter struct {
	delegate store.UserSettingsStore
	failNext *atomic.Bool
}

func (s userSettingsUnknownOutcomeAdapter) Get(ctx context.Context, userID model.UserID) (*model.UserSettingsDocument, error) {
	return s.delegate.Get(ctx, userID)
}

func (s userSettingsUnknownOutcomeAdapter) Replace(
	ctx context.Context,
	replacement *store.UserSettingsReplacement,
	command *store.CommandIdempotency,
) (*store.UserSettingsReplacementResult, error) {
	result, err := s.delegate.Replace(ctx, replacement, command)
	if err == nil && s.failNext.CompareAndSwap(true, false) {
		return nil, errors.New("simulated response loss after durable commit")
	}
	return result, err
}

func TestUserSettingsUnknownOutcomeReconcilesByReadWithoutContentLeak(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	if dataSource == "" {
		t.Fatal("PROCTOR_TEST_DATABASE_URL is not set")
	}
	persistence := openAuthenticationStore(t, dataSource)
	uncertain := &userSettingsUnknownOutcomeStore{Store: persistence}
	helper := testlib.Setup(t, testlib.WithStore(uncertain))
	ctx := context.Background()
	if _, err := persistence.Institution().Save(ctx, &model.Institution{
		Name: "settings-unknown-outcome", DisplayName: "Settings Unknown Outcome",
	}); err != nil {
		t.Fatal(err)
	}
	const password = "correct horse battery staple"
	user, err := helper.App.CreateLocalUser(ctx, &model.User{
		Username: "settings-unknown-user", Email: "settings-unknown-user@example.edu",
	}, password)
	if err != nil {
		t.Fatal(err)
	}
	login, err := helper.App.Login(ctx, application.Invocation{}, application.LoginCommand{
		LoginID: user.Username, Password: password, ClientType: model.SessionClientDesktop, Source: "127.0.0.1:1",
	})
	if err != nil {
		t.Fatal(err)
	}
	principal, err := helper.App.AuthenticateAccess(ctx, login.Tokens.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	invocation := application.NewInvocation(*principal, model.RequestMetadata{RequestID: "settings-unknown-outcome"})
	before, err := helper.App.ReadOwnUserSettings(ctx, invocation)
	if err != nil {
		t.Fatal(err)
	}
	const source = "{\n  // private-comment-marker\n  \"private.visual.key\": \"private-value-marker\",\n}\n"
	command := application.ReplaceOwnUserSettingsCommand{
		Source: source, FormatVersion: 1, ExpectedRevision: before.Revision,
		IdempotencyKey: "private-idempotency-marker",
	}
	uncertain.failNext.Store(true)
	if _, err := helper.App.ReplaceOwnUserSettings(ctx, invocation, command); !application.Is(err, "user_settings.unavailable") {
		t.Fatalf("unknown replacement outcome error = %v", err)
	}
	authoritative, err := helper.App.ReadOwnUserSettings(ctx, invocation)
	if err != nil {
		t.Fatal(err)
	}
	if authoritative.Source != source || authoritative.Revision == before.Revision {
		t.Fatalf("authoritative reconciliation = %#v", authoritative)
	}
	replayed, err := helper.App.ReplaceOwnUserSettings(ctx, invocation, command)
	if err != nil || !replayed.Replayed || replayed.Revision != authoritative.Revision {
		t.Fatalf("retained replay = %#v, %v", replayed, err)
	}
	var encodedOutcome string
	if err := persistence.GetMaster().Get(ctx, &encodedOutcome, `
		SELECT outcome::text FROM command_outcomes
		WHERE user_id = $1 AND operation = 'user_settings.replace'`, user.ID.String()); err != nil {
		t.Fatal(err)
	}
	for _, surface := range []struct {
		name  string
		value string
	}{
		{name: "command outcome", value: encodedOutcome},
		{name: "ordinary logs", value: helper.Logs.String()},
	} {
		for _, forbidden := range []string{"private-comment-marker", "private.visual.key", "private-value-marker", "private-idempotency-marker"} {
			if bytes.Contains([]byte(surface.value), []byte(forbidden)) {
				t.Fatalf("%s exposed %q: %s", surface.name, forbidden, surface.value)
			}
		}
	}
}

func openAdditionalUserSettingsStore(t *testing.T, dataSource string) *sqlstore.SQLStore {
	t.Helper()
	database := config.Default().Database
	database.DataSource = dataSource
	persistence, err := sqlstore.New(context.Background(), sqlstore.SettingsFromConfig(database))
	if err != nil {
		t.Fatal(err)
	}
	return persistence
}
