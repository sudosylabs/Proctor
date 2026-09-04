// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package storetest

import (
	"context"
	"crypto/sha256"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestUserSettingsStore(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	document, err := ss.UserSettings().Get(ctx, user.ID)
	requireNoError(t, err)
	if document.UserID != user.ID || document.Source != model.UserSettingsInitialSource ||
		document.FormatVersion != model.UserSettingsFormatVersion1 || !document.Revision.IsValid() ||
		!document.CreatedAt.Equal(user.CreatedAt) || !document.UpdatedAt.Equal(user.CreatedAt) {
		t.Fatalf("Get() = %#v", document)
	}
	if _, err := ss.UserSettings().Get(ctx, model.UserID("invalid")); err == nil {
		t.Fatal("Get() accepted invalid User identity")
	}
	if _, err := ss.UserSettings().Get(ctx, model.NewUserID()); !store.IsNotFound(err) {
		t.Fatalf("Get(missing) error = %v", err)
	}

	institution := saveInstitution(t, ctx, ss)
	exact := "{\n  // exact source\n  \"workbench.colorTheme\": \"Proctor Dark\",\n}\n"
	replacedAt := document.UpdatedAt.Add(time.Second)
	nextRevision := model.NewUserSettingsRevision()
	command := userSettingsCommand(user.ID, "replace-1", exact)
	replaced, err := ss.UserSettings().Replace(ctx, &store.UserSettingsReplacement{
		UserID: user.ID, Source: exact, FormatVersion: 1,
		// Clock input may carry nanoseconds. The immediate result, stored document,
		// and retained replay must agree at durable microsecond precision.
		ExpectedRevision: document.Revision, NextRevision: nextRevision, UpdatedAt: replacedAt.Add(613 * time.Nanosecond),
		AuditEvent: userSettingsAudit(user.ID, institution.ID, document.Revision, nextRevision, len(exact)),
	}, command)
	requireNoError(t, err)
	if replaced.Replayed || !replaced.Changed || replaced.Revision != nextRevision ||
		replaced.FormatVersion != 1 || !replaced.UpdatedAt.Equal(replacedAt) {
		t.Fatalf("Replace(changed) = %#v", replaced)
	}
	stored, err := ss.UserSettings().Get(ctx, user.ID)
	requireNoError(t, err)
	if stored.Source != exact || stored.Revision != nextRevision || !stored.UpdatedAt.Equal(replacedAt) {
		t.Fatalf("Get(after replace) = %#v", stored)
	}
	replayed, err := ss.UserSettings().Replace(ctx, &store.UserSettingsReplacement{
		UserID: user.ID, Source: exact, FormatVersion: 1,
		ExpectedRevision: document.Revision, NextRevision: model.NewUserSettingsRevision(), UpdatedAt: replacedAt.Add(time.Second),
	}, command)
	requireNoError(t, err)
	if !replayed.Replayed || replayed.Revision != replaced.Revision || !replayed.UpdatedAt.Equal(replaced.UpdatedAt) {
		t.Fatalf("Replace(replay) = %#v, want %#v", replayed, replaced)
	}
	audits, err := ss.Audit().List(ctx, store.AuditListOptions{
		Action: "user.settings.replace", Limit: 10,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	requireNoError(t, err)
	if len(audits) != 1 || audits[0].Status != model.AuditStatusSuccess {
		t.Fatalf("settings replacement audits = %#v", audits)
	}

	noOpCommand := userSettingsCommand(user.ID, "replace-noop", exact)
	noOp, err := ss.UserSettings().Replace(ctx, &store.UserSettingsReplacement{
		UserID: user.ID, Source: exact, FormatVersion: 1,
		ExpectedRevision: stored.Revision, NextRevision: model.NewUserSettingsRevision(), UpdatedAt: replacedAt.Add(2 * time.Second),
	}, noOpCommand)
	requireNoError(t, err)
	if noOp.Changed || noOp.Replayed || noOp.Revision != stored.Revision || !noOp.UpdatedAt.Equal(stored.UpdatedAt) {
		t.Fatalf("Replace(no-op) = %#v", noOp)
	}
	noOpReplay, err := ss.UserSettings().Replace(ctx, &store.UserSettingsReplacement{
		UserID: user.ID, Source: exact, FormatVersion: 1,
		ExpectedRevision: stored.Revision, NextRevision: model.NewUserSettingsRevision(), UpdatedAt: replacedAt.Add(3 * time.Second),
	}, noOpCommand)
	requireNoError(t, err)
	if !noOpReplay.Replayed || noOpReplay.Changed || noOpReplay.Revision != stored.Revision {
		t.Fatalf("Replace(no-op replay) = %#v", noOpReplay)
	}
	audits, err = ss.Audit().List(ctx, store.AuditListOptions{
		Action: "user.settings.replace", Limit: 10,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	requireNoError(t, err)
	if len(audits) != 1 {
		t.Fatalf("no-op/replay wrote audit: %#v", audits)
	}

	_, err = ss.UserSettings().Replace(ctx, &store.UserSettingsReplacement{
		UserID: user.ID, Source: "{}\n", FormatVersion: 1,
		ExpectedRevision: stored.Revision, NextRevision: model.NewUserSettingsRevision(), UpdatedAt: replacedAt.Add(4 * time.Second),
		AuditEvent: userSettingsAudit(user.ID, institution.ID, stored.Revision, model.NewUserSettingsRevision(), 3),
	}, userSettingsCommand(user.ID, "replace-noop", "{}\n"))
	var idempotencyConflict *store.ErrIdempotencyConflict
	if !errors.As(err, &idempotencyConflict) {
		t.Fatalf("Replace(changed semantics) error = %v", err)
	}

	stale := model.NewUserSettingsRevision()
	_, err = ss.UserSettings().Replace(ctx, &store.UserSettingsReplacement{
		UserID: user.ID, Source: "{}\n", FormatVersion: 1,
		ExpectedRevision: stale, NextRevision: model.NewUserSettingsRevision(), UpdatedAt: replacedAt.Add(5 * time.Second),
		AuditEvent: userSettingsAudit(user.ID, institution.ID, stale, model.NewUserSettingsRevision(), 3),
	}, userSettingsCommand(user.ID, "replace-stale", "{}\n"))
	var revisionConflict *store.ErrUserSettingsRevisionConflict
	if !errors.As(err, &revisionConflict) || revisionConflict.CurrentRevision != stored.Revision {
		t.Fatalf("Replace(stale) error = %v", err)
	}

	rollbackUser := saveUser(t, ctx, ss)
	rollbackCurrent, err := ss.UserSettings().Get(ctx, rollbackUser.ID)
	requireNoError(t, err)
	rollbackSource := "{\"editor.fontSize\": 17}\n"
	rollbackNext := model.NewUserSettingsRevision()
	invalidAudit := userSettingsAudit(
		rollbackUser.ID, institution.ID, rollbackCurrent.Revision, rollbackNext, len(rollbackSource),
	)
	invalidAudit.SessionID = model.NewSessionID()
	rollbackCommand := userSettingsCommand(rollbackUser.ID, "replace-audit-rollback", rollbackSource)
	_, err = ss.UserSettings().Replace(ctx, &store.UserSettingsReplacement{
		UserID: rollbackUser.ID, Source: rollbackSource, FormatVersion: 1,
		ExpectedRevision: rollbackCurrent.Revision, NextRevision: rollbackNext,
		UpdatedAt: rollbackCurrent.UpdatedAt.Add(time.Second), AuditEvent: invalidAudit,
	}, rollbackCommand)
	if err == nil {
		t.Fatal("Replace() succeeded with an audit event that could not commit")
	}
	rollbackStored, err := ss.UserSettings().Get(ctx, rollbackUser.ID)
	requireNoError(t, err)
	if rollbackStored.Source != rollbackCurrent.Source || rollbackStored.Revision != rollbackCurrent.Revision {
		t.Fatalf("failed audit retained settings mutation: %#v", rollbackStored)
	}
	validAudit := userSettingsAudit(
		rollbackUser.ID, institution.ID, rollbackCurrent.Revision, rollbackNext, len(rollbackSource),
	)
	if _, err := ss.UserSettings().Replace(ctx, &store.UserSettingsReplacement{
		UserID: rollbackUser.ID, Source: rollbackSource, FormatVersion: 1,
		ExpectedRevision: rollbackCurrent.Revision, NextRevision: rollbackNext,
		UpdatedAt: rollbackCurrent.UpdatedAt.Add(time.Second), AuditEvent: validAudit,
	}, rollbackCommand); err != nil {
		t.Fatalf("failed audit retained command outcome: %v", err)
	}

	concurrentCurrent, err := ss.UserSettings().Get(ctx, user.ID)
	requireNoError(t, err)
	type concurrentOutcome struct{ err error }
	outcomes := make(chan concurrentOutcome, 2)
	var wait sync.WaitGroup
	for index := 0; index < 2; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			source := exact + string(rune('a'+index))
			next := model.NewUserSettingsRevision()
			_, replaceErr := ss.UserSettings().Replace(ctx, &store.UserSettingsReplacement{
				UserID: user.ID, Source: source, FormatVersion: 1,
				ExpectedRevision: concurrentCurrent.Revision, NextRevision: next,
				UpdatedAt:  concurrentCurrent.UpdatedAt.Add(time.Duration(index+10) * time.Second),
				AuditEvent: userSettingsAudit(user.ID, institution.ID, concurrentCurrent.Revision, next, len(source)),
			}, userSettingsCommand(user.ID, "replace-concurrent-"+string(rune('a'+index)), source))
			outcomes <- concurrentOutcome{err: replaceErr}
		}(index)
	}
	wait.Wait()
	close(outcomes)
	successes, conflicts := 0, 0
	for outcome := range outcomes {
		var conflict *store.ErrUserSettingsRevisionConflict
		switch {
		case outcome.err == nil:
			successes++
		case errors.As(outcome.err, &conflict):
			conflicts++
		default:
			t.Fatalf("concurrent Replace() error = %v", outcome.err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent outcomes success/conflict = %d/%d", successes, conflicts)
	}
}

func userSettingsCommand(userID model.UserID, key, semantic string) *store.CommandIdempotency {
	keyDigest := sha256.Sum256([]byte(key))
	fingerprint := sha256.Sum256([]byte(semantic))
	return &store.CommandIdempotency{
		UserID: userID, Operation: "user_settings.replace",
		KeyDigest: keyDigest, FingerprintVersion: 1, Fingerprint: fingerprint,
		OutcomeVersion: 1, Retention: 24 * time.Hour, Wait: 2 * time.Second,
	}
}

func userSettingsAudit(
	userID model.UserID,
	institutionID model.InstitutionID,
	previous model.UserSettingsRevision,
	next model.UserSettingsRevision,
	sourceBytes int,
) *model.AuditEvent {
	parameters, _ := model.EncodeAuditData(map[string]any{
		"previous_revision": previous.String(), "resulting_revision": next.String(),
		"format_version": 1, "source_bytes": sourceBytes,
	})
	return &model.AuditEvent{
		ActorID: userID, Action: "user.settings.replace",
		Resource:  model.Resource{Type: model.ResourceUser, ID: userID.String()},
		ScopeType: model.RoleScopeInstitution, ScopeID: institutionID.String(),
		Status: model.AuditStatusSuccess, NodeID: "store-test", ClientType: string(model.SessionClientWeb),
		AuthMethod: "password", Parameters: parameters,
	}
}
