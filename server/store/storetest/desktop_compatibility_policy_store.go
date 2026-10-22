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
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestDesktopCompatibilityPolicyStore(t *testing.T, stores store.Store) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	bootstrap, err := stores.Installation().Bootstrap(ctx, testInstallationBootstrap(811))
	require.NoError(t, err)
	initial, err := stores.DesktopCompatibilityPolicy().Get(ctx)
	require.NoError(t, err)
	require.Equal(t, bootstrap.Institution.ID, initial.InstitutionID)
	require.Equal(t, int64(1), initial.Revision)
	require.Empty(t, initial.MinimumDesktopRelease)
	require.Empty(t, initial.RevokedDesktopBuildIDs)
	require.Empty(t, initial.AdministratorMessage)
	require.Equal(t, model.DesktopAvailabilityReady, initial.Availability)
	require.False(t, initial.RetryAt.Valid)

	retryAt := time.Unix(2_000_000_000, 0).UTC()
	settings := model.DesktopCompatibilityPolicySettings{
		MinimumDesktopRelease:  "1.2.0",
		RevokedDesktopBuildIDs: []string{"desktop-build-17", "desktop-build-23"},
		AdministratorMessage:   "Update Proctor Desktop before your next exam.",
		Availability:           model.DesktopAvailabilityMaintenance,
		RetryAt:                model.OptionalTimeFrom(retryAt),
	}
	replacement := &store.DesktopCompatibilityPolicyReplacement{
		ActorID:          bootstrap.Administrator.ID,
		ExpectedRevision: initial.Revision,
		Settings:         settings,
	}
	prepareDesktopCompatibilityPolicyAttempt(t, ctx, stores, bootstrap, replacement)
	command := desktopCompatibilityPolicyCommand(bootstrap.Administrator.ID, "replace")
	result, err := stores.DesktopCompatibilityPolicy().Replace(ctx, replacement, command)
	require.NoError(t, err)
	require.False(t, result.Replayed)
	require.True(t, result.Changed)
	require.Equal(t, int64(2), result.Policy.Revision)
	require.Equal(t, settings.MinimumDesktopRelease, result.Policy.MinimumDesktopRelease)
	require.Len(t, result.Policy.RevokedDesktopBuildIDs, 2)
	require.Equal(t, settings.AdministratorMessage, result.Policy.AdministratorMessage)
	require.Equal(t, model.DesktopAvailabilityMaintenance, result.Policy.Availability)
	require.True(t, result.Policy.RetryAt.Valid)
	require.True(t, result.Policy.RetryAt.Time.Equal(retryAt))
	assertDesktopCompatibilityPolicyAuditIsSafe(t, ctx, stores, replacement.AuditEventID, settings)

	replayReplacement := &store.DesktopCompatibilityPolicyReplacement{
		ActorID:          bootstrap.Administrator.ID,
		ExpectedRevision: initial.Revision,
		Settings:         settings,
	}
	prepareDesktopCompatibilityPolicyAttempt(t, ctx, stores, bootstrap, replayReplacement)
	replay, err := stores.DesktopCompatibilityPolicy().Replace(ctx, replayReplacement, command)
	require.NoError(t, err)
	require.True(t, replay.Replayed)
	require.True(t, replay.Changed)
	require.Equal(t, int64(2), replay.Policy.Revision)
	assertDesktopCompatibilityPolicyAuditIsSafe(t, ctx, stores, replayReplacement.AuditEventID, settings)

	conflictingCommand := *command
	conflictingCommand.Fingerprint = sha256.Sum256([]byte("different-desktop-policy-command"))
	conflictReplacement := *replayReplacement
	prepareDesktopCompatibilityPolicyAttempt(t, ctx, stores, bootstrap, &conflictReplacement)
	_, err = stores.DesktopCompatibilityPolicy().Replace(ctx, &conflictReplacement, &conflictingCommand)
	var idempotencyConflict *store.ErrIdempotencyConflict
	require.ErrorAs(t, err, &idempotencyConflict)

	stale := &store.DesktopCompatibilityPolicyReplacement{
		ActorID:          bootstrap.Administrator.ID,
		ExpectedRevision: 1,
		Settings:         settings,
	}
	prepareDesktopCompatibilityPolicyAttempt(t, ctx, stores, bootstrap, stale)
	_, err = stores.DesktopCompatibilityPolicy().Replace(
		ctx,
		stale,
		desktopCompatibilityPolicyCommand(bootstrap.Administrator.ID, "stale"),
	)
	var revisionConflict *store.ErrDesktopCompatibilityPolicyRevisionConflict
	require.ErrorAs(t, err, &revisionConflict)
	require.Equal(t, int64(2), revisionConflict.CurrentRevision)

	noOp := &store.DesktopCompatibilityPolicyReplacement{
		ActorID:          bootstrap.Administrator.ID,
		ExpectedRevision: 2,
		Settings:         settings,
	}
	prepareDesktopCompatibilityPolicyAttempt(t, ctx, stores, bootstrap, noOp)
	noOpResult, err := stores.DesktopCompatibilityPolicy().Replace(
		ctx,
		noOp,
		desktopCompatibilityPolicyCommand(bootstrap.Administrator.ID, "no-op"),
	)
	require.NoError(t, err)
	require.False(t, noOpResult.Changed)
	require.Equal(t, int64(2), noOpResult.Policy.Revision)

	ordinary, err := stores.User().Create(ctx, testUserCreation(newUser(), nil))
	require.NoError(t, err)
	unauthorized := &store.DesktopCompatibilityPolicyReplacement{
		ActorID:          ordinary.User.ID,
		ExpectedRevision: 2,
		Settings:         settings,
	}
	prepareDesktopCompatibilityPolicyAttemptForActor(
		t,
		ctx,
		stores,
		bootstrap.Institution.ID,
		ordinary.User.ID,
		unauthorized,
	)
	_, err = stores.DesktopCompatibilityPolicy().Replace(
		ctx,
		unauthorized,
		desktopCompatibilityPolicyCommand(ordinary.User.ID, "unauthorized"),
	)
	var administratorConflict *store.ErrConflict
	require.ErrorAs(t, err, &administratorConflict)
	require.Equal(t, "actor_not_system_administrator", administratorConflict.Constraint)
}

func desktopCompatibilityPolicyCommand(userID model.UserID, identity string) *store.CommandIdempotency {
	return &store.CommandIdempotency{
		UserID:      userID,
		Operation:   "desktop_compatibility_policy.replace",
		KeyDigest:   sha256.Sum256([]byte("desktop-policy-key:" + identity)),
		Fingerprint: sha256.Sum256([]byte("desktop-policy-command:" + identity)),
		Retention:   time.Hour,
		Wait:        time.Second,
	}
}

func prepareDesktopCompatibilityPolicyAttempt(
	t *testing.T,
	ctx context.Context,
	stores store.Store,
	bootstrap *model.InstallationBootstrapResult,
	replacement *store.DesktopCompatibilityPolicyReplacement,
) {
	t.Helper()
	prepareDesktopCompatibilityPolicyAttemptForActor(
		t,
		ctx,
		stores,
		bootstrap.Institution.ID,
		bootstrap.Administrator.ID,
		replacement,
	)
}

func prepareDesktopCompatibilityPolicyAttemptForActor(
	t *testing.T,
	ctx context.Context,
	stores store.Store,
	institutionID model.InstitutionID,
	actorID model.UserID,
	replacement *store.DesktopCompatibilityPolicyReplacement,
) {
	t.Helper()
	event, err := stores.Audit().Save(ctx, &model.AuditEvent{
		ActorID: actorID,
		Action:  string(model.ActionDesktopCompatibilityPolicyManage),
		Resource: model.Resource{
			Type: model.ResourceInstitution,
			ID:   institutionID.String(),
		},
		ScopeType:  model.RoleScopeInstitution,
		ScopeID:    institutionID.String(),
		Status:     model.AuditStatusAttempt,
		NodeID:     "store-test",
		ClientType: string(model.SessionClientWeb),
		AuthMethod: "password",
	})
	require.NoError(t, err)
	replacement.AuditEventID = event.ID
	replacement.AuditAt = event.CreatedAt
}

func assertDesktopCompatibilityPolicyAuditIsSafe(
	t *testing.T,
	ctx context.Context,
	stores store.Store,
	eventID model.AuditEventID,
	settings model.DesktopCompatibilityPolicySettings,
) {
	t.Helper()
	event, err := stores.Audit().Get(ctx, eventID)
	require.NoError(t, err)
	require.Equal(t, model.AuditStatusSuccess, event.Status)
	var result map[string]any
	require.NoError(t, json.Unmarshal(event.Result, &result))
	encoded := string(event.Result)
	for _, sensitive := range append(settings.RevokedDesktopBuildIDs, settings.AdministratorMessage) {
		require.NotContains(t, encoded, sensitive, "audit result exposed policy content")
	}
	require.Equal(t, float64(len(settings.RevokedDesktopBuildIDs)), result["revoked_build_count"])
	require.Equal(t, true, result["administrator_message_set"])
}
