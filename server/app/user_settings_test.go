// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type userSettingsStoreFake struct {
	document       *model.UserSettingsDocument
	err            error
	got            model.UserID
	reads          int
	replaceInput   *store.UserSettingsReplacement
	replaceCommand *store.CommandIdempotency
	replaceResult  *store.UserSettingsReplacementResult
	replaceErr     error
	events         *[]string
}

func (s *userSettingsStoreFake) Get(_ context.Context, userID model.UserID) (*model.UserSettingsDocument, error) {
	s.got = userID
	s.reads++
	if s.err != nil {
		return nil, s.err
	}
	return s.document.Clone(), nil
}

func (s *userSettingsStoreFake) Replace(
	_ context.Context,
	input *store.UserSettingsReplacement,
	command *store.CommandIdempotency,
) (*store.UserSettingsReplacementResult, error) {
	if s.events != nil {
		*s.events = append(*s.events, "store")
	}
	s.replaceInput = input
	s.replaceCommand = command
	if s.replaceErr != nil {
		return nil, s.replaceErr
	}
	return s.replaceResult, nil
}

type userSettingsAuditFake struct {
	event *model.AuditEvent
	err   error
	input userSettingsAuditInput
	calls int
}

type userSettingsEffectsFake struct {
	changes []userSettingsChanged
	err     error
	events  *[]string
}

func (e *userSettingsEffectsFake) Changed(_ context.Context, change userSettingsChanged) error {
	if e.events != nil {
		*e.events = append(*e.events, "effect")
	}
	e.changes = append(e.changes, change)
	return e.err
}

type userSettingsEffectFailuresFake struct {
	operations []string
	errors     []error
}

func (r *userSettingsEffectFailuresFake) Report(_ context.Context, operation string, err error) {
	r.operations = append(r.operations, operation)
	r.errors = append(r.errors, err)
}

func newUserSettingsServiceForTest(
	t *testing.T,
	settings userSettingsStore,
	audit userSettingsAuditPreparer,
	now func() time.Time,
) *userSettingsService {
	t.Helper()
	service, err := newUserSettingsService(
		settings, audit, &userSettingsEffectsFake{}, &userSettingsEffectFailuresFake{}, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func (a *userSettingsAuditFake) PrepareReplacement(
	_ context.Context,
	input userSettingsAuditInput,
) (*model.AuditEvent, error) {
	a.calls++
	a.input = input
	return a.event, a.err
}

func TestUserSettingsReadOwnPreservesExactSource(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	principal := userSettingsSessionPrincipal(at)
	source := "{\n  // keep this comment\n  \"editor.fontFamily\": \"A\\\\B\",\n  \"future.unknown\": true,\n}\n"
	persistence := &userSettingsStoreFake{document: &model.UserSettingsDocument{
		UserID: principal.UserID, Source: source,
		FormatVersion: model.UserSettingsFormatVersion1,
		Revision:      model.NewUserSettingsRevision(),
		CreatedAt:     at.Add(-time.Hour), UpdatedAt: at,
	}}
	service := newUserSettingsServiceForTest(t, persistence, &userSettingsAuditFake{}, time.Now)

	result, err := service.ReadOwn(context.Background(), NewInvocation(principal, model.RequestMetadata{}))
	if err != nil {
		t.Fatal(err)
	}
	if persistence.got != principal.UserID || persistence.reads != 1 {
		t.Fatalf("Get() user=%s reads=%d", persistence.got, persistence.reads)
	}
	if result.Source != source || result.FormatVersion != model.UserSettingsFormatVersion1 ||
		result.Revision != persistence.document.Revision || !result.Writable ||
		!result.UpdatedAt.Equal(at) {
		t.Fatalf("ReadOwn() = %#v", result)
	}
}

func TestUserSettingsReadOwnPreservesUnsupportedFormatAsOpaqueReadOnlySource(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	principal := userSettingsSessionPrincipal(at)
	// This deliberately is not version-1 JSONC. An older server must preserve
	// newer grammar as bounded opaque source instead of attempting to parse it.
	const source = "future-format(source => must.remain.exact);\n"
	persistence := &userSettingsStoreFake{document: &model.UserSettingsDocument{
		UserID: principal.UserID, Source: source, FormatVersion: 2,
		Revision: model.NewUserSettingsRevision(), CreatedAt: at.Add(-time.Hour), UpdatedAt: at,
	}}
	service := newUserSettingsServiceForTest(t, persistence, &userSettingsAuditFake{}, time.Now)

	result, err := service.ReadOwn(context.Background(), NewInvocation(principal, model.RequestMetadata{}))
	if err != nil {
		t.Fatal(err)
	}
	if result.Source != source || result.FormatVersion != 2 || result.Revision != persistence.document.Revision ||
		result.Writable || !result.UpdatedAt.Equal(at) {
		t.Fatalf("ReadOwn() = %#v", result)
	}
	if persistence.reads != 1 {
		t.Fatalf("Get() reads = %d, want 1", persistence.reads)
	}
}

func TestUserSettingsReadOwnRequiresInteractiveSession(t *testing.T) {
	t.Parallel()
	persistence := &userSettingsStoreFake{}
	service := newUserSettingsServiceForTest(t, persistence, &userSettingsAuditFake{}, time.Now)
	pat := model.Principal{
		UserID: model.NewUserID(), CredentialID: model.PrincipalCredentialID(model.NewPersonalAccessTokenID()),
		CredentialType: model.CredentialPersonalAccessToken, AuthenticationMethod: "personal_access_token",
		ClientType: model.SessionClientCLI, CredentialScopes: []string{string(model.ActionUserView)},
	}
	for name, principal := range map[string]model.Principal{
		"personal access token": pat,
		"system invocation":     {},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := service.ReadOwn(context.Background(), NewInvocation(principal, model.RequestMetadata{})); !Is(err, "authentication.invalid_token") {
				t.Fatalf("ReadOwn() error = %v", err)
			}
		})
	}
	if persistence.reads != 0 {
		t.Fatalf("unauthorized calls performed %d reads", persistence.reads)
	}
}

func TestUserSettingsReadOwnFailsClosedWithoutExposingSource(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	principal := userSettingsSessionPrincipal(at)
	secret := "private.setting.name"
	tests := []struct {
		name  string
		store *userSettingsStoreFake
	}{
		{name: "store failure", store: &userSettingsStoreFake{err: errors.New("database unavailable")}},
		{name: "missing invariant", store: &userSettingsStoreFake{err: store.NewErrNotFound("user_settings_document", principal.UserID.String())}},
		{name: "invalid persisted source", store: &userSettingsStoreFake{document: &model.UserSettingsDocument{
			UserID: principal.UserID, Source: "{\"" + secret + "\": }", FormatVersion: 1,
			Revision: model.NewUserSettingsRevision(), CreatedAt: at, UpdatedAt: at,
		}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := newUserSettingsServiceForTest(t, test.store, &userSettingsAuditFake{}, time.Now)
			_, err := service.ReadOwn(context.Background(), NewInvocation(principal, model.RequestMetadata{}))
			if !Is(err, "user_settings.unavailable") {
				t.Fatalf("ReadOwn() error = %v", err)
			}
			if err != nil && containsSensitiveText(err.Error(), secret) {
				t.Fatalf("error exposed source: %v", err)
			}
		})
	}
}

func TestUserSettingsReplaceOwnValidatesAndPreparesSafeAtomicMutation(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	principal := userSettingsSessionPrincipal(at)
	current := &model.UserSettingsDocument{
		UserID: principal.UserID, Source: "{}\n", FormatVersion: 1,
		Revision: model.NewUserSettingsRevision(), CreatedAt: at.Add(-time.Hour), UpdatedAt: at.Add(-time.Minute),
	}
	source := "{\n  // visual preference\n  \"workbench.colorTheme\": \"Proctor Dark\",\n}\n"
	persistence := &userSettingsStoreFake{document: current}
	audit := &userSettingsAuditFake{event: &model.AuditEvent{Action: "user.settings.replace"}}
	service := newUserSettingsServiceForTest(t, persistence, audit, func() time.Time { return at })
	nextRevision := model.NewUserSettingsRevision()
	persistence.replaceResult = &store.UserSettingsReplacementResult{
		Revision: nextRevision, FormatVersion: 1, UpdatedAt: at, Changed: true,
	}
	result, err := service.ReplaceOwn(context.Background(), NewInvocation(principal, model.RequestMetadata{}), ReplaceOwnUserSettingsCommand{
		Source: source, FormatVersion: 1, ExpectedRevision: current.Revision,
		IdempotencyKey: "settings-save-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Revision != nextRevision || !result.Changed || result.Replayed {
		t.Fatalf("ReplaceOwn() = %#v", result)
	}
	if persistence.replaceInput == nil || persistence.replaceInput.UserID != principal.UserID ||
		persistence.replaceInput.Source != source || persistence.replaceInput.FormatVersion != 1 ||
		persistence.replaceInput.ExpectedRevision != current.Revision ||
		!persistence.replaceInput.NextRevision.IsValid() || !persistence.replaceInput.UpdatedAt.Equal(at) {
		t.Fatalf("replacement input = %#v", persistence.replaceInput)
	}
	if persistence.replaceCommand == nil || persistence.replaceCommand.Operation != "user_settings.replace" ||
		persistence.replaceCommand.UserID != principal.UserID {
		t.Fatalf("idempotency = %#v", persistence.replaceCommand)
	}
	if audit.calls != 1 || audit.input.SourceBytes != len(source) ||
		audit.input.PreviousRevision != current.Revision ||
		audit.input.ResultingRevision != persistence.replaceInput.NextRevision {
		t.Fatalf("audit input = %#v calls=%d", audit.input, audit.calls)
	}
}

func TestUserSettingsReplaceOwnRejectsInvalidSourceWithoutLeakOrMutation(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	principal := userSettingsSessionPrincipal(at)
	secret := "secret.visual.key"
	persistence := &userSettingsStoreFake{}
	audit := &userSettingsAuditFake{}
	service := newUserSettingsServiceForTest(t, persistence, audit, time.Now)
	_, err := service.ReplaceOwn(context.Background(), NewInvocation(principal, model.RequestMetadata{}), ReplaceOwnUserSettingsCommand{
		Source: "{\"" + secret + "\": }", FormatVersion: 1,
		ExpectedRevision: model.NewUserSettingsRevision(), IdempotencyKey: "invalid-save",
	})
	if !Is(err, "user_settings.invalid") {
		t.Fatalf("ReplaceOwn() error = %v", err)
	}
	failure, ok := As(err)
	if !ok || failure.Fields()["diagnostics"] == "" || containsSensitiveText(err.Error(), secret) {
		t.Fatalf("invalid-source error = %#v", err)
	}
	if persistence.reads != 0 || persistence.replaceInput != nil || audit.calls != 0 {
		t.Fatalf("invalid source effects reads=%d replace=%#v audits=%d", persistence.reads, persistence.replaceInput, audit.calls)
	}
}

func TestUserSettingsReplaceOwnRejectsUnsupportedFormatsWithoutEffects(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	principal := userSettingsSessionPrincipal(at)
	unsupported := &model.UserSettingsDocument{
		UserID: principal.UserID, Source: "future-format(private.value);\n", FormatVersion: 2,
		Revision: model.NewUserSettingsRevision(), CreatedAt: at.Add(-time.Hour), UpdatedAt: at,
	}
	tests := []struct {
		name          string
		persisted     *model.UserSettingsDocument
		targetVersion int
		wantReads     int
	}{
		{name: "unsupported persisted format", persisted: unsupported, targetVersion: 1, wantReads: 1},
		{name: "unsupported target format", persisted: unsupported, targetVersion: 2, wantReads: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			persistence := &userSettingsStoreFake{document: test.persisted}
			audit := &userSettingsAuditFake{}
			service := newUserSettingsServiceForTest(t, persistence, audit, time.Now)
			_, err := service.ReplaceOwn(context.Background(), NewInvocation(principal, model.RequestMetadata{}), ReplaceOwnUserSettingsCommand{
				Source: "{}\n", FormatVersion: test.targetVersion,
				ExpectedRevision: test.persisted.Revision, IdempotencyKey: "unsupported-format-save",
			})
			if !Is(err, "user_settings.format_unsupported") {
				t.Fatalf("ReplaceOwn() error = %v", err)
			}
			if persistence.reads != test.wantReads || persistence.replaceInput != nil ||
				persistence.replaceCommand != nil || audit.calls != 0 {
				t.Fatalf("effects reads=%d replace=%#v command=%#v audits=%d",
					persistence.reads, persistence.replaceInput, persistence.replaceCommand, audit.calls)
			}
			if persistence.document.Source != test.persisted.Source ||
				persistence.document.Revision != test.persisted.Revision ||
				!persistence.document.UpdatedAt.Equal(test.persisted.UpdatedAt) {
				t.Fatalf("persisted document changed: %#v", persistence.document)
			}
		})
	}
}

func TestUserSettingsReplaceOwnMapsRevisionAndIdempotencyConflictsSafely(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	principal := userSettingsSessionPrincipal(at)
	current := &model.UserSettingsDocument{
		UserID: principal.UserID, Source: "{}\n", FormatVersion: 1,
		Revision: model.NewUserSettingsRevision(), CreatedAt: at.Add(-time.Hour), UpdatedAt: at.Add(-time.Minute),
	}
	for name, storeErr := range map[string]error{
		"revision":    &store.ErrUserSettingsRevisionConflict{CurrentRevision: current.Revision},
		"idempotency": &store.ErrIdempotencyConflict{},
	} {
		t.Run(name, func(t *testing.T) {
			persistence := &userSettingsStoreFake{document: current, replaceErr: storeErr}
			service := newUserSettingsServiceForTest(t,
				persistence,
				&userSettingsAuditFake{event: &model.AuditEvent{Action: "user.settings.replace"}},
				func() time.Time { return at },
			)
			_, err := service.ReplaceOwn(context.Background(), NewInvocation(principal, model.RequestMetadata{}), ReplaceOwnUserSettingsCommand{
				Source: "{\"editor.fontSize\": 16}\n", FormatVersion: 1,
				ExpectedRevision: current.Revision, IdempotencyKey: "conflict-key",
			})
			want := "user_settings.revision_conflict"
			if name == "idempotency" {
				want = "idempotency.conflict"
			}
			if !Is(err, want) {
				t.Fatalf("ReplaceOwn() error = %v, want %s", err, want)
			}
			if name == "revision" {
				failure, _ := As(err)
				if failure.Fields()["current_revision"] != current.Revision.String() {
					t.Fatalf("revision hint = %#v", failure.Fields())
				}
			}
		})
	}
}

func TestUserSettingsReplaceOwnUnknownOutcomeIsResolvedByAuthoritativeRead(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	principal := userSettingsSessionPrincipal(at)
	previous := &model.UserSettingsDocument{
		UserID: principal.UserID, Source: "{}\n", FormatVersion: 1,
		Revision: model.NewUserSettingsRevision(), CreatedAt: at.Add(-time.Hour), UpdatedAt: at.Add(-time.Minute),
	}
	persistence := &userSettingsStoreFake{
		document: previous, replaceErr: errors.New("commit outcome unavailable"),
	}
	service := newUserSettingsServiceForTest(t,
		persistence,
		&userSettingsAuditFake{event: &model.AuditEvent{Action: "user.settings.replace"}},
		func() time.Time { return at },
	)
	const source = "{\"editor.fontSize\": 18}\n"
	_, err := service.ReplaceOwn(context.Background(), NewInvocation(principal, model.RequestMetadata{}), ReplaceOwnUserSettingsCommand{
		Source: source, FormatVersion: 1, ExpectedRevision: previous.Revision,
		IdempotencyKey: "unknown-outcome",
	})
	if !Is(err, "user_settings.unavailable") {
		t.Fatalf("ReplaceOwn() error = %v", err)
	}
	committed := *previous
	committed.Source = source
	committed.Revision = model.NewUserSettingsRevision()
	committed.UpdatedAt = at
	persistence.document = &committed
	view, err := service.ReadOwn(context.Background(), NewInvocation(principal, model.RequestMetadata{}))
	if err != nil {
		t.Fatal(err)
	}
	if view.Source != source || view.Revision != committed.Revision {
		t.Fatalf("authoritative read = %#v", view)
	}
}

func TestUserSettingsReplaceOwnPublishesOnePostCommitRefetchHint(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 15, 15, 0, 0, 0, time.UTC)
	principal := userSettingsSessionPrincipal(at)
	current := &model.UserSettingsDocument{
		UserID: principal.UserID, Source: "{}\n", FormatVersion: 1,
		Revision: model.NewUserSettingsRevision(), CreatedAt: at.Add(-time.Hour), UpdatedAt: at.Add(-time.Minute),
	}
	next := model.NewUserSettingsRevision()
	events := []string{}
	persistence := &userSettingsStoreFake{document: current, events: &events, replaceResult: &store.UserSettingsReplacementResult{
		Revision: next, FormatVersion: 1, UpdatedAt: at, Changed: true,
	}}
	effects := &userSettingsEffectsFake{events: &events}
	failures := &userSettingsEffectFailuresFake{}
	service, err := newUserSettingsService(
		persistence, &userSettingsAuditFake{event: &model.AuditEvent{Action: "user.settings.replace"}},
		effects, failures, func() time.Time { return at },
	)
	if err != nil {
		t.Fatal(err)
	}

	result, err := service.ReplaceOwn(context.Background(), NewInvocation(principal, model.RequestMetadata{}), ReplaceOwnUserSettingsCommand{
		Source: "{\"editor.fontSize\": 16}\n", FormatVersion: 1,
		ExpectedRevision: current.Revision, IdempotencyKey: "notify-settings",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || result.Replayed || len(effects.changes) != 1 {
		t.Fatalf("result=%#v changes=%#v", result, effects.changes)
	}
	change := effects.changes[0]
	if change.UserID != principal.UserID || change.Revision != next || change.FormatVersion != 1 || !change.ChangedAt.Equal(at) {
		t.Fatalf("change = %#v", change)
	}
	if len(events) != 2 || events[0] != "store" || events[1] != "effect" {
		t.Fatalf("effect order = %#v", events)
	}
	if len(failures.operations) != 0 {
		t.Fatalf("failure reports = %#v", failures.operations)
	}
}

func TestUserSettingsReplaceOwnSuppressesNoOpReplayAndRejectedCommandEvents(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 15, 15, 0, 0, 0, time.UTC)
	principal := userSettingsSessionPrincipal(at)
	current := &model.UserSettingsDocument{
		UserID: principal.UserID, Source: "{}\n", FormatVersion: 1,
		Revision: model.NewUserSettingsRevision(), CreatedAt: at.Add(-time.Hour), UpdatedAt: at,
	}
	tests := []struct {
		name     string
		result   *store.UserSettingsReplacementResult
		storeErr error
		source   string
	}{
		{name: "no-op", result: &store.UserSettingsReplacementResult{Revision: current.Revision, FormatVersion: 1, UpdatedAt: at}},
		{name: "replay", result: &store.UserSettingsReplacementResult{Revision: current.Revision, FormatVersion: 1, UpdatedAt: at, Changed: true, Replayed: true}},
		{name: "revision conflict", storeErr: &store.ErrUserSettingsRevisionConflict{CurrentRevision: current.Revision}},
		{name: "validation failure", source: "{private.key: }"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			effects := &userSettingsEffectsFake{}
			persistence := &userSettingsStoreFake{document: current, replaceResult: test.result, replaceErr: test.storeErr}
			audit := &userSettingsAuditFake{err: errors.New("audit unavailable")}
			service, err := newUserSettingsService(
				persistence, audit,
				effects, &userSettingsEffectFailuresFake{}, func() time.Time { return at.Add(time.Second) },
			)
			if err != nil {
				t.Fatal(err)
			}
			source := "{}\n"
			if test.source != "" {
				source = test.source
			}
			_, replaceErr := service.ReplaceOwn(context.Background(), NewInvocation(principal, model.RequestMetadata{}), ReplaceOwnUserSettingsCommand{
				Source: source, FormatVersion: 1, ExpectedRevision: current.Revision, IdempotencyKey: "suppressed-event",
			})
			if (test.name == "no-op" || test.name == "replay") && replaceErr != nil {
				t.Fatalf("ReplaceOwn() error = %v", replaceErr)
			}
			if audit.calls != 0 {
				t.Fatalf("suppressed command prepared %d audit events", audit.calls)
			}
			if len(effects.changes) != 0 {
				t.Fatalf("published changes = %#v", effects.changes)
			}
		})
	}
}

func TestUserSettingsReplaceOwnRejectedPrincipalPublishesNothing(t *testing.T) {
	t.Parallel()
	effects := &userSettingsEffectsFake{}
	persistence := &userSettingsStoreFake{}
	service, err := newUserSettingsService(
		persistence, &userSettingsAuditFake{}, effects, &userSettingsEffectFailuresFake{}, time.Now,
	)
	if err != nil {
		t.Fatal(err)
	}
	principal := model.Principal{
		UserID: model.NewUserID(), CredentialID: model.PrincipalCredentialID(model.NewPersonalAccessTokenID()),
		CredentialType: model.CredentialPersonalAccessToken, AuthenticationMethod: "personal_access_token",
		ClientType: model.SessionClientCLI, CredentialScopes: []string{string(model.ActionUserView)},
	}
	_, err = service.ReplaceOwn(context.Background(), NewInvocation(principal, model.RequestMetadata{}), ReplaceOwnUserSettingsCommand{
		Source: "{}\n", FormatVersion: 1, ExpectedRevision: model.NewUserSettingsRevision(), IdempotencyKey: "rejected",
	})
	if !Is(err, "authentication.invalid_token") || persistence.reads != 0 || persistence.replaceInput != nil || len(effects.changes) != 0 {
		t.Fatalf("error=%v reads=%d replace=%#v changes=%#v", err, persistence.reads, persistence.replaceInput, effects.changes)
	}
}

func TestUserSettingsRealtimeFailureDoesNotChangeCommittedResultAndReadReconciles(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 15, 15, 0, 0, 0, time.UTC)
	principal := userSettingsSessionPrincipal(at)
	current := &model.UserSettingsDocument{UserID: principal.UserID, Source: "{}\n", FormatVersion: 1,
		Revision: model.NewUserSettingsRevision(), CreatedAt: at.Add(-time.Hour), UpdatedAt: at.Add(-time.Minute)}
	next := model.NewUserSettingsRevision()
	persistence := &userSettingsStoreFake{document: current, replaceResult: &store.UserSettingsReplacementResult{
		Revision: next, FormatVersion: 1, UpdatedAt: at, Changed: true,
	}}
	failures := &userSettingsEffectFailuresFake{}
	service, err := newUserSettingsService(
		persistence, &userSettingsAuditFake{event: &model.AuditEvent{Action: "user.settings.replace"}},
		&userSettingsEffectsFake{err: errors.New("peer delivery unavailable")}, failures, func() time.Time { return at },
	)
	if err != nil {
		t.Fatal(err)
	}
	const source = "{\"workbench.colorTheme\": \"Dark\"}\n"
	result, err := service.ReplaceOwn(context.Background(), NewInvocation(principal, model.RequestMetadata{}), ReplaceOwnUserSettingsCommand{
		Source: source, FormatVersion: 1, ExpectedRevision: current.Revision, IdempotencyKey: "delivery-fails",
	})
	if err != nil || result.Revision != next || !result.Changed {
		t.Fatalf("ReplaceOwn() = %#v, %v", result, err)
	}
	if len(failures.operations) != 1 || failures.operations[0] != "user_settings_changed" {
		t.Fatalf("failure reports = %#v", failures.operations)
	}
	committed := *current
	committed.Source, committed.Revision, committed.UpdatedAt = source, next, at
	persistence.document = &committed
	view, err := service.ReadOwn(context.Background(), NewInvocation(principal, model.RequestMetadata{}))
	if err != nil || view.Source != source || view.Revision != next {
		t.Fatalf("authoritative read = %#v, %v", view, err)
	}
}

type userSettingsRaceStore struct {
	document *model.UserSettingsDocument
	result   *store.UserSettingsReplacementResult
	calls    atomic.Int32
}

func (s *userSettingsRaceStore) Get(context.Context, model.UserID) (*model.UserSettingsDocument, error) {
	return s.document.Clone(), nil
}

func (s *userSettingsRaceStore) Replace(context.Context, *store.UserSettingsReplacement, *store.CommandIdempotency) (*store.UserSettingsReplacementResult, error) {
	if s.calls.Add(1) == 1 {
		return s.result, nil
	}
	return nil, &store.ErrUserSettingsRevisionConflict{CurrentRevision: s.result.Revision}
}

type userSettingsAtomicEffects struct{ calls atomic.Int32 }

func (e *userSettingsAtomicEffects) Changed(context.Context, userSettingsChanged) error {
	e.calls.Add(1)
	return nil
}

type userSettingsConcurrentAudit struct{}

func (userSettingsConcurrentAudit) PrepareReplacement(context.Context, userSettingsAuditInput) (*model.AuditEvent, error) {
	return &model.AuditEvent{Action: "user.settings.replace"}, nil
}

func TestUserSettingsConcurrentWritersPublishOnlyTheCommittedWinner(t *testing.T) {
	at := time.Date(2026, 8, 15, 15, 0, 0, 0, time.UTC)
	principal := userSettingsSessionPrincipal(at)
	current := &model.UserSettingsDocument{UserID: principal.UserID, Source: "{}\n", FormatVersion: 1,
		Revision: model.NewUserSettingsRevision(), CreatedAt: at.Add(-time.Hour), UpdatedAt: at.Add(-time.Minute)}
	persistence := &userSettingsRaceStore{document: current, result: &store.UserSettingsReplacementResult{
		Revision: model.NewUserSettingsRevision(), FormatVersion: 1, UpdatedAt: at, Changed: true,
	}}
	effects := &userSettingsAtomicEffects{}
	service, err := newUserSettingsService(
		persistence, userSettingsConcurrentAudit{}, effects, &userSettingsEffectFailuresFake{}, func() time.Time { return at },
	)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	errorsByWriter := make(chan error, 2)
	for index := 0; index < 2; index++ {
		go func(index int) {
			<-start
			_, replaceErr := service.ReplaceOwn(context.Background(), NewInvocation(principal, model.RequestMetadata{}), ReplaceOwnUserSettingsCommand{
				Source: "{\"editor.fontSize\": " + string(rune('1'+index)) + "}\n", FormatVersion: 1,
				ExpectedRevision: current.Revision, IdempotencyKey: "racing-writer-" + string(rune('a'+index)),
			})
			errorsByWriter <- replaceErr
		}(index)
	}
	close(start)
	successes := 0
	for index := 0; index < 2; index++ {
		if err := <-errorsByWriter; err == nil {
			successes++
		}
	}
	if successes != 1 || effects.calls.Load() != 1 {
		t.Fatalf("successes=%d published=%d", successes, effects.calls.Load())
	}
}

func userSettingsSessionPrincipal(at time.Time) model.Principal {
	return model.Principal{
		UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID:         model.PrincipalCredentialID(model.NewSessionCredentialID()),
		CredentialType:       model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType: model.SessionClientWeb, AuthenticatedAt: at,
	}
}

func containsSensitiveText(value, sensitive string) bool {
	for index := 0; index+len(sensitive) <= len(value); index++ {
		if value[index:index+len(sensitive)] == sensitive {
			return true
		}
	}
	return false
}
