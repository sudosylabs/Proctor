//go:build integration

// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/app/apperror"
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
	require.NotEmpty(t, dataSource, "PROCTOR_TEST_DATABASE_URL is not set")
	primaryStore := openAuthenticationStore(t, dataSource)
	seedInitialAuthenticationAccessPolicy(t, primaryStore)
	secondaryStore := openAdditionalUserSettingsStore(t, dataSource)
	primaryCluster := &userSettingsRecordingCluster{nodeID: "settings-node-a"}
	secondaryCluster := &userSettingsRecordingCluster{nodeID: "settings-node-b"}
	primary := testlib.Setup(t, testlib.WithStore(primaryStore), testlib.WithCluster(primaryCluster))
	secondary := testlib.Setup(t, testlib.WithStore(secondaryStore), testlib.WithCluster(secondaryCluster))
	ctx := context.Background()
	_, err := primaryStore.Institution().Save(ctx, &model.Institution{
		Name: "settings-two-node", DisplayName: "Settings Two Node",
	})
	require.NoError(t, err)
	const password = "correct horse battery staple"
	user, err := primary.App.CreateLocalUser(ctx, app.CreateLocalUserCommand{User: &model.User{
		Username: "settings-two-node-user", Email: "settings-two-node-user@example.edu",
	}, Password: password})
	require.NoError(t, err)
	login, err := primary.App.Login(ctx, app.Invocation{}, app.LoginCommand{
		LoginID: user.Username, Password: password, ClientType: model.SessionClientWeb, Source: "127.0.0.1:1",
	})
	require.NoError(t, err)
	principal, err := primary.App.AuthenticateAccess(ctx, login.Tokens.AccessToken)
	require.NoError(t, err)
	invocation := app.NewInvocation(*principal, model.RequestMetadata{RequestID: "settings-two-node"})
	before, err := primary.App.ReadOwnUserSettings(ctx, invocation)
	require.NoError(t, err)

	type outcome struct {
		result app.UserSettingsReplacementResult
		err    error
	}
	outcomes := make(chan outcome, 2)
	for _, candidate := range []struct {
		app    *app.App
		source string
		key    string
	}{
		{app: primary.App, source: "{\"workbench.colorTheme\":\"private-a\"}\n", key: "settings-node-a"},
		{app: secondary.App, source: "{\"workbench.colorTheme\":\"private-b\"}\n", key: "settings-node-b"},
	} {
		go func(candidate struct {
			app    *app.App
			source string
			key    string
		}) {
			result, replaceErr := candidate.app.ReplaceOwnUserSettings(ctx, invocation, app.ReplaceOwnUserSettingsCommand{
				Source: candidate.source, FormatVersion: 1,
				ExpectedRevision: before.Revision, IdempotencyKey: candidate.key,
			})
			outcomes <- outcome{result: result, err: replaceErr}
		}(candidate)
	}
	var winner app.UserSettingsReplacementResult
	successes, conflicts := 0, 0
	for range 2 {
		completed := <-outcomes
		switch {
		case completed.err == nil:
			successes++
			winner = completed.result
		case apperror.Is(completed.err, "user_settings.revision_conflict"):
			conflicts++
		default:
			require.FailNow(t, fmt.Sprintf("concurrent replacement error = %v", completed.err))
		}
	}
	require.Equal(t, 1, successes, "concurrent outcomes success/conflict/winner = %d/%d/%#v", successes, conflicts, winner)
	require.Equal(t, 1, conflicts, "concurrent outcomes success/conflict/winner = %d/%d/%#v", successes, conflicts, winner)
	require.True(t, winner.Changed, "concurrent outcomes success/conflict/winner = %d/%d/%#v", successes, conflicts, winner)
	primaryView, err := primary.App.ReadOwnUserSettings(ctx, invocation)
	require.NoError(t, err)
	secondaryView, err := secondary.App.ReadOwnUserSettings(ctx, invocation)
	require.NoError(t, err)
	require.Equal(t, winner.Revision, primaryView.Revision, "authoritative views diverged: primary=%#v secondary=%#v winner=%#v", primaryView, secondaryView, winner)
	require.Equal(t, winner.Revision, secondaryView.Revision, "authoritative views diverged: primary=%#v secondary=%#v winner=%#v", primaryView, secondaryView, winner)
	require.Equal(t, secondaryView.Source, primaryView.Source, "authoritative views diverged: primary=%#v secondary=%#v winner=%#v", primaryView, secondaryView, winner)
	events := append(primaryCluster.settingsEvents(), secondaryCluster.settingsEvents()...)
	require.Len(t, events, 1, "settings cluster events")
	payload := events[0].Data
	require.True(t, bytes.Contains(payload, []byte(winner.Revision.String())))
	require.True(t, bytes.Contains(payload, []byte(user.ID.String())))
	for _, forbidden := range []string{
		"private-a", "private-b", "colorTheme", `"source":`, `"idempotency_key":`, `"session_id":`,
	} {
		require.False(t, bytes.Contains(payload, []byte(forbidden)), "settings event exposed %q: %s", forbidden, payload)
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
	require.NotEmpty(t, dataSource, "PROCTOR_TEST_DATABASE_URL is not set")
	persistence := openAuthenticationStore(t, dataSource)
	seedInitialAuthenticationAccessPolicy(t, persistence)
	uncertain := &userSettingsUnknownOutcomeStore{Store: persistence}
	helper := testlib.Setup(t, testlib.WithStore(uncertain))
	ctx := context.Background()
	_, err := persistence.Institution().Save(ctx, &model.Institution{
		Name: "settings-unknown-outcome", DisplayName: "Settings Unknown Outcome",
	})
	require.NoError(t, err)
	const password = "correct horse battery staple"
	user, err := helper.App.CreateLocalUser(ctx, app.CreateLocalUserCommand{User: &model.User{
		Username: "settings-unknown-user", Email: "settings-unknown-user@example.edu",
	}, Password: password})
	require.NoError(t, err)
	login, err := helper.App.Login(ctx, app.Invocation{}, app.LoginCommand{
		LoginID: user.Username, Password: password, ClientType: model.SessionClientWeb, Source: "127.0.0.1:1",
	})
	require.NoError(t, err)
	principal, err := helper.App.AuthenticateAccess(ctx, login.Tokens.AccessToken)
	require.NoError(t, err)
	invocation := app.NewInvocation(*principal, model.RequestMetadata{RequestID: "settings-unknown-outcome"})
	before, err := helper.App.ReadOwnUserSettings(ctx, invocation)
	require.NoError(t, err)
	const source = "{\n  // private-comment-marker\n  \"private.visual.key\": \"private-value-marker\",\n}\n"
	command := app.ReplaceOwnUserSettingsCommand{
		Source: source, FormatVersion: 1, ExpectedRevision: before.Revision,
		IdempotencyKey: "private-idempotency-marker",
	}
	uncertain.failNext.Store(true)
	_, err = helper.App.ReplaceOwnUserSettings(ctx, invocation, command)
	require.True(t, apperror.Is(err, "user_settings.unavailable"), "unknown replacement outcome error = %v", err)
	authoritative, err := helper.App.ReadOwnUserSettings(ctx, invocation)
	require.NoError(t, err)
	require.Equal(t, source, authoritative.Source)
	require.NotEqual(t, before.Revision, authoritative.Revision)
	replayed, err := helper.App.ReplaceOwnUserSettings(ctx, invocation, command)
	require.NoError(t, err, "retained replay = %#v", replayed)
	require.True(t, replayed.Replayed, "retained replay = %#v", replayed)
	require.Equal(t, authoritative.Revision, replayed.Revision, "retained replay = %#v", replayed)
	var encodedOutcome string
	require.NoError(t, persistence.GetMaster().Get(ctx, &encodedOutcome, `
		SELECT outcome::text FROM command_outcomes
		WHERE user_id = $1 AND operation = 'user_settings.replace'`, user.ID.String()))
	for _, surface := range []struct {
		name  string
		value string
	}{
		{name: "command outcome", value: encodedOutcome},
		{name: "ordinary logs", value: helper.Logs.String()},
	} {
		t.Run(surface.name, func(t *testing.T) {
			for _, forbidden := range []string{"private-comment-marker", "private.visual.key", "private-value-marker", "private-idempotency-marker"} {
				require.False(t, bytes.Contains([]byte(surface.value), []byte(forbidden)), "%s exposed %q: %s", surface.name, forbidden, surface.value)
			}
		})
	}
}

func openAdditionalUserSettingsStore(t *testing.T, dataSource string) *sqlstore.SQLStore {
	t.Helper()
	database := config.Default().Database
	database.DataSource = dataSource
	persistence, err := sqlstore.New(context.Background(), sqlstore.SettingsFromConfig(database))
	require.NoError(t, err)
	return persistence
}
