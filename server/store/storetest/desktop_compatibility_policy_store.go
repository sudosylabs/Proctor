// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestDesktopCompatibilityPolicyStore(t *testing.T, stores store.Store) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	bootstrap, err := stores.Installation().Bootstrap(ctx, testInstallationBootstrap(811))
	requireNoError(t, err)
	initial, err := stores.DesktopCompatibilityPolicy().Get(ctx)
	requireNoError(t, err)
	if initial.InstitutionID != bootstrap.Institution.ID || initial.Revision != 1 ||
		initial.MinimumDesktopRelease != "" || len(initial.RevokedDesktopBuildIDs) != 0 ||
		initial.AdministratorMessage != "" || initial.Availability != model.DesktopAvailabilityReady ||
		initial.RetryAt.Valid {
		t.Fatalf("initial policy = %#v", initial)
	}

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
	requireNoError(t, err)
	if result.Replayed || !result.Changed || result.Policy.Revision != 2 ||
		result.Policy.MinimumDesktopRelease != settings.MinimumDesktopRelease ||
		len(result.Policy.RevokedDesktopBuildIDs) != 2 ||
		result.Policy.AdministratorMessage != settings.AdministratorMessage ||
		result.Policy.Availability != model.DesktopAvailabilityMaintenance ||
		!result.Policy.RetryAt.Valid || !result.Policy.RetryAt.Time.Equal(retryAt) {
		t.Fatalf("replacement = %#v", result)
	}
	assertDesktopCompatibilityPolicyAuditIsSafe(t, ctx, stores, replacement.AuditEventID, settings)

	replayReplacement := &store.DesktopCompatibilityPolicyReplacement{
		ActorID:          bootstrap.Administrator.ID,
		ExpectedRevision: initial.Revision,
		Settings:         settings,
	}
	prepareDesktopCompatibilityPolicyAttempt(t, ctx, stores, bootstrap, replayReplacement)
	replay, err := stores.DesktopCompatibilityPolicy().Replace(ctx, replayReplacement, command)
	requireNoError(t, err)
	if !replay.Replayed || !replay.Changed || replay.Policy.Revision != 2 {
		t.Fatalf("replay = %#v", replay)
	}
	assertDesktopCompatibilityPolicyAuditIsSafe(t, ctx, stores, replayReplacement.AuditEventID, settings)

	conflictingCommand := *command
	conflictingCommand.Fingerprint = sha256.Sum256([]byte("different-desktop-policy-command"))
	conflictReplacement := *replayReplacement
	prepareDesktopCompatibilityPolicyAttempt(t, ctx, stores, bootstrap, &conflictReplacement)
	_, err = stores.DesktopCompatibilityPolicy().Replace(ctx, &conflictReplacement, &conflictingCommand)
	var idempotencyConflict *store.ErrIdempotencyConflict
	if !errors.As(err, &idempotencyConflict) {
		t.Fatalf("conflicting idempotency key error = %v", err)
	}

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
	if !errors.As(err, &revisionConflict) || revisionConflict.CurrentRevision != 2 {
		t.Fatalf("stale revision error = %v", err)
	}

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
	requireNoError(t, err)
	if noOpResult.Changed || noOpResult.Policy.Revision != 2 {
		t.Fatalf("no-op replacement = %#v", noOpResult)
	}

	ordinary, err := stores.User().Create(ctx, testUserCreation(newUser(), nil))
	requireNoError(t, err)
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
	if !errors.As(err, &administratorConflict) ||
		administratorConflict.Constraint != "actor_not_system_administrator" {
		t.Fatalf("ordinary actor error = %v", err)
	}
}

func desktopCompatibilityPolicyCommand(userID model.UserID, identity string) *store.CommandIdempotency {
	return &store.CommandIdempotency{
		UserID:             userID,
		Operation:          "desktop_compatibility_policy.replace.v1",
		KeyDigest:          sha256.Sum256([]byte("desktop-policy-key:" + identity)),
		FingerprintVersion: 1,
		Fingerprint:        sha256.Sum256([]byte("desktop-policy-command:" + identity)),
		OutcomeVersion:     1,
		Retention:          time.Hour,
		Wait:               time.Second,
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
	requireNoError(t, err)
	replacement.AuditEventID = event.ID.String()
	replacement.AuditAt = model.MillisFromTime(event.CreatedAt)
}

func assertDesktopCompatibilityPolicyAuditIsSafe(
	t *testing.T,
	ctx context.Context,
	stores store.Store,
	eventID string,
	settings model.DesktopCompatibilityPolicySettings,
) {
	t.Helper()
	event, err := stores.Audit().Get(ctx, eventID)
	requireNoError(t, err)
	if event.Status != model.AuditStatusSuccess {
		t.Fatalf("audit status = %q", event.Status)
	}
	var result map[string]any
	if err := json.Unmarshal(event.Result, &result); err != nil {
		t.Fatalf("decode audit result: %v", err)
	}
	encoded := string(event.Result)
	for _, sensitive := range append(settings.RevokedDesktopBuildIDs, settings.AdministratorMessage) {
		if strings.Contains(encoded, sensitive) {
			t.Fatalf("audit result exposed policy content %q: %s", sensitive, encoded)
		}
	}
	if result["revoked_build_count"] != float64(len(settings.RevokedDesktopBuildIDs)) ||
		result["administrator_message_set"] != true {
		t.Fatalf("audit result = %#v", result)
	}
}
