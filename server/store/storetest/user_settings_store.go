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
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestUserSettingsStore(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	document, err := ss.UserSettings().Get(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, user.ID, document.UserID)
	require.Equal(t, model.UserSettingsInitialSource, document.Source)
	require.Equal(t, model.UserSettingsFormatVersion1, document.FormatVersion)
	require.True(t, document.Revision.IsValid())
	require.True(t, document.CreatedAt.Equal(user.CreatedAt))
	require.True(t, document.UpdatedAt.Equal(user.CreatedAt))
	_, err = ss.UserSettings().Get(ctx, model.UserID("invalid"))
	require.Error(t, err, "Get() accepted invalid User identity")
	_, err = ss.UserSettings().Get(ctx, model.NewUserID())
	require.True(t, store.IsNotFound(err), "Get(missing) error")

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
	require.NoError(t, err)
	require.False(t, replaced.Replayed)
	require.True(t, replaced.Changed)
	require.Equal(t, nextRevision, replaced.Revision)
	require.Equal(t, 1, replaced.FormatVersion)
	require.True(t, replaced.UpdatedAt.Equal(replacedAt))
	stored, err := ss.UserSettings().Get(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, exact, stored.Source)
	require.Equal(t, nextRevision, stored.Revision)
	require.True(t, stored.UpdatedAt.Equal(replacedAt))
	replayed, err := ss.UserSettings().Replace(ctx, &store.UserSettingsReplacement{
		UserID: user.ID, Source: exact, FormatVersion: 1,
		ExpectedRevision: document.Revision, NextRevision: model.NewUserSettingsRevision(), UpdatedAt: replacedAt.Add(time.Second),
	}, command)
	require.NoError(t, err)
	require.True(t, replayed.Replayed, "Replace(replay) = %#v, want %#v", replayed, replaced)
	require.Equal(t, replaced.Revision, replayed.Revision, "Replace(replay) = %#v, want %#v", replayed, replaced)
	require.True(t, replayed.UpdatedAt.Equal(replaced.UpdatedAt), "Replace(replay) = %#v, want %#v", replayed, replaced)
	audits, err := ss.Audit().List(ctx, store.AuditListOptions{
		Action: "user.settings.replace", Limit: 10,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	require.NoError(t, err)
	require.Len(t, audits, 1)
	require.Equal(t, model.AuditStatusSuccess, audits[0].Status)

	noOpCommand := userSettingsCommand(user.ID, "replace-noop", exact)
	noOp, err := ss.UserSettings().Replace(ctx, &store.UserSettingsReplacement{
		UserID: user.ID, Source: exact, FormatVersion: 1,
		ExpectedRevision: stored.Revision, NextRevision: model.NewUserSettingsRevision(), UpdatedAt: replacedAt.Add(2 * time.Second),
	}, noOpCommand)
	require.NoError(t, err)
	require.False(t, noOp.Changed)
	require.False(t, noOp.Replayed)
	require.Equal(t, stored.Revision, noOp.Revision)
	require.True(t, noOp.UpdatedAt.Equal(stored.UpdatedAt))
	noOpReplay, err := ss.UserSettings().Replace(ctx, &store.UserSettingsReplacement{
		UserID: user.ID, Source: exact, FormatVersion: 1,
		ExpectedRevision: stored.Revision, NextRevision: model.NewUserSettingsRevision(), UpdatedAt: replacedAt.Add(3 * time.Second),
	}, noOpCommand)
	require.NoError(t, err)
	require.True(t, noOpReplay.Replayed)
	require.False(t, noOpReplay.Changed)
	require.Equal(t, stored.Revision, noOpReplay.Revision)
	audits, err = ss.Audit().List(ctx, store.AuditListOptions{
		Action: "user.settings.replace", Limit: 10,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	require.NoError(t, err)
	require.Len(t, audits, 1)

	_, err = ss.UserSettings().Replace(ctx, &store.UserSettingsReplacement{
		UserID: user.ID, Source: "{}\n", FormatVersion: 1,
		ExpectedRevision: stored.Revision, NextRevision: model.NewUserSettingsRevision(), UpdatedAt: replacedAt.Add(4 * time.Second),
		AuditEvent: userSettingsAudit(user.ID, institution.ID, stored.Revision, model.NewUserSettingsRevision(), 3),
	}, userSettingsCommand(user.ID, "replace-noop", "{}\n"))
	var idempotencyConflict *store.ErrIdempotencyConflict
	require.ErrorAs(t, err, &idempotencyConflict)

	stale := model.NewUserSettingsRevision()
	_, err = ss.UserSettings().Replace(ctx, &store.UserSettingsReplacement{
		UserID: user.ID, Source: "{}\n", FormatVersion: 1,
		ExpectedRevision: stale, NextRevision: model.NewUserSettingsRevision(), UpdatedAt: replacedAt.Add(5 * time.Second),
		AuditEvent: userSettingsAudit(user.ID, institution.ID, stale, model.NewUserSettingsRevision(), 3),
	}, userSettingsCommand(user.ID, "replace-stale", "{}\n"))
	var revisionConflict *store.ErrUserSettingsRevisionConflict
	require.ErrorAs(t, err, &revisionConflict)
	require.Equal(t, stored.Revision, revisionConflict.CurrentRevision)

	rollbackUser := saveUser(t, ctx, ss)
	rollbackCurrent, err := ss.UserSettings().Get(ctx, rollbackUser.ID)
	require.NoError(t, err)
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
	require.Error(t, err, "Replace() succeeded with an audit event that could not commit")
	rollbackStored, err := ss.UserSettings().Get(ctx, rollbackUser.ID)
	require.NoError(t, err)
	require.Equal(t, rollbackCurrent.Source, rollbackStored.Source)
	require.Equal(t, rollbackCurrent.Revision, rollbackStored.Revision)
	validAudit := userSettingsAudit(
		rollbackUser.ID, institution.ID, rollbackCurrent.Revision, rollbackNext, len(rollbackSource),
	)
	_, err = ss.UserSettings().Replace(ctx, &store.UserSettingsReplacement{
		UserID: rollbackUser.ID, Source: rollbackSource, FormatVersion: 1,
		ExpectedRevision: rollbackCurrent.Revision, NextRevision: rollbackNext,
		UpdatedAt: rollbackCurrent.UpdatedAt.Add(time.Second), AuditEvent: validAudit,
	}, rollbackCommand)
	require.NoError(t, err)

	concurrentCurrent, err := ss.UserSettings().Get(ctx, user.ID)
	require.NoError(t, err)
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
			require.FailNow(t, fmt.Sprintf("concurrent Replace() error = %v", outcome.err))
		}
	}
	require.Equal(t, 1, successes, "concurrent outcomes success/conflict")
	require.Equal(t, 1, conflicts, "concurrent outcomes success/conflict")
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
