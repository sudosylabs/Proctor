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
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type AccessPolicySQLProbe struct {
	HoldAuthenticationPathFence func(*testing.T, context.Context) (int, func())
	WaitForBlockedTransactions  func(*testing.T, context.Context, int, int)
	// LeadDatabaseClock moves one Session's node-clock instants after the
	// database clock, as a creating node whose clock leads PostgreSQL does.
	LeadDatabaseClock func(*testing.T, context.Context, model.SessionID)
}

func TestAccessPolicyStore(t *testing.T, ss store.Store, probes ...AccessPolicySQLProbe) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	bootstrap, err := ss.Installation().Bootstrap(ctx, testInstallationBootstrap(301))
	require.NoError(t, err)
	snapshot, err := ss.AccessPolicy().Get(ctx, model.AccessPolicyTransitionHistoryLimit)
	require.NoError(t, err)
	require.Equal(t, bootstrap.AccessPolicy.ID, snapshot.Policy.ID)
	require.Equal(t, int64(1), snapshot.Policy.Revision)
	require.Empty(t, snapshot.History)

	settings := snapshot.Policy.Settings()
	settings.LocalLoginEnabled = false
	settings.InvitationLocalCredentialEnabled = false
	blocked, err := ss.AccessPolicy().Preflight(ctx, &store.AccessPolicyPreflight{ExpectedRevision: 1, Settings: settings,
		Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}}, CheckedAt: model.NowUTC()})
	require.NoError(t, err)
	require.True(t, hasAccessPolicyBlocker(blocked, store.AccessPolicyBlockerLastAdministratorPath, ""), "local lockout blockers = %#v", blocked)

	_, err = ss.ExternalIdentity().Save(ctx, &model.ExternalIdentity{UserID: bootstrap.Administrator.ID,
		Provider: "campus", Subject: "admin-subject", LastSeenAt: model.OptionalTimeFrom(model.NowUTC())})
	require.NoError(t, err)
	localOnlyResult, err := ss.User().Create(ctx, testUserCreation(newUser(), &model.PasswordCredential{PasswordHash: "encoded-password"}))
	require.NoError(t, err)
	_, err = SaveRoleBinding(ctx, ss, &model.RoleBinding{
		UserID: localOnlyResult.User.ID, RoleID: bootstrap.Role.ID,
		ScopeType: model.RoleScopeInstitution, ScopeID: bootstrap.Institution.ID.String(),
		StartsAt: model.TimeFromMillis(model.GetMillis() - 100),
	})
	require.NoError(t, err)
	settings.ProviderAdmissions["campus"] = model.ProviderAdmissionLinkedOnly
	capabilities := store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{"campus": {AutoProvision: true}}, DurableMail: true}
	blocked, err = ss.AccessPolicy().Preflight(ctx, &store.AccessPolicyPreflight{ExpectedRevision: 1, Settings: settings,
		Capabilities: capabilities, CheckedAt: model.NowUTC()})
	require.NoError(t, err)
	require.Empty(t, blocked)
	passwordSession, _, _ := saveSession(t, ctx, ss, bootstrap.Administrator.ID.String(), 10)

	command := &store.CommandIdempotency{UserID: bootstrap.Administrator.ID, Operation: "access_policy.replace",
		KeyDigest:   sha256.Sum256([]byte("access-policy-key")),
		Fingerprint: sha256.Sum256([]byte("access-policy-command")), Retention: time.Hour, Wait: time.Second}
	replacement := &store.AccessPolicyReplacement{Preflight: store.AccessPolicyPreflight{ExpectedRevision: 1, Settings: settings,
		Capabilities: capabilities, CheckedAt: model.NowUTC()}, ActorID: bootstrap.Administrator.ID,
	}
	prepareAccessPolicyAttempt(t, ctx, ss, bootstrap, replacement)
	result, err := ss.AccessPolicy().Replace(ctx, replacement, command)
	require.NoError(t, err)
	require.False(t, result.Replayed)
	require.True(t, result.Changed)
	require.Equal(t, int64(2), result.Snapshot.Policy.Revision)
	require.Len(t, result.Snapshot.History, 1)
	require.Equal(t, bootstrap.Administrator.ID, result.Snapshot.History[0].ActorID)
	// The replacement deliberately leaves the first administrator's external
	// identity as the installation's only usable path. The second administrator
	// has a password, but local login is now disabled by policy.
	disableAttempt := saveUserProfileAuditAttempt(t, ctx, ss, bootstrap.Administrator.ID.String())
	_, err = ss.User().SetDisabled(ctx, userDisabledStateChangeWithNotice(t, &store.UserDisabledStateChange{
		ID: bootstrap.Administrator.ID, ExpectedRevision: bootstrap.Administrator.Revision,
		Disabled: true, ChangedAt: model.GetMillis(), RevocationReason: model.SessionRevocationAccountDisabled,
		AuditEventID: disableAttempt.ID, AuditAt: model.MillisFromTime(disableAttempt.CreatedAt),
	}))
	var lastPathConflict *store.ErrConflict
	require.ErrorAs(t, err, &lastPathConflict)
	require.Equal(t, "users_last_system_admin", lastPathConflict.Constraint)
	_, err = EndRoleBinding(ctx, ss, bootstrap.RoleBinding.ID, model.GetMillis())
	require.ErrorAs(t, err, &lastPathConflict)
	require.Equal(t, "role_bindings_last_system_admin", lastPathConflict.Constraint)
	// Durable policy retains campus while deployment capability snapshots may
	// remove it. Omission must fail closed; restoring the exact provider makes
	// the external administrator usable again. Local credentials remain governed
	// only by LocalLoginEnabled.
	removedProviderAttempt := saveUserProfileAuditAttempt(t, ctx, ss, localOnlyResult.User.ID.String())
	_, err = ss.User().SetDisabled(ctx, userDisabledStateChangeWithNotice(t, &store.UserDisabledStateChange{
		ID: localOnlyResult.User.ID, ExpectedRevision: localOnlyResult.User.Revision,
		Disabled: true, ChangedAt: model.GetMillis(), RevocationReason: model.SessionRevocationAccountDisabled,
		AuditEventID: removedProviderAttempt.ID, AuditAt: model.MillisFromTime(removedProviderAttempt.CreatedAt),
	}))
	require.ErrorAs(t, err, &lastPathConflict)
	require.Equal(t, "users_last_system_admin", lastPathConflict.Constraint)
	availableProviderAttempt := saveUserProfileAuditAttempt(t, ctx, ss, localOnlyResult.User.ID.String())
	disabledLocalOnly, err := ss.User().SetDisabled(ctx, userDisabledStateChangeWithNotice(t, &store.UserDisabledStateChange{
		ID: localOnlyResult.User.ID, ExpectedRevision: localOnlyResult.User.Revision,
		Disabled: true, Capabilities: capabilities, ChangedAt: model.GetMillis(),
		RevocationReason: model.SessionRevocationAccountDisabled, AuditEventID: availableProviderAttempt.ID,
		AuditAt: model.MillisFromTime(availableProviderAttempt.CreatedAt),
	}))
	require.NoError(t, err)
	reenableLocalOnlyAttempt := saveUserProfileAuditAttempt(t, ctx, ss, localOnlyResult.User.ID.String())
	_, err = ss.User().SetDisabled(ctx, userDisabledStateChangeWithNotice(t, &store.UserDisabledStateChange{
		ID: localOnlyResult.User.ID, ExpectedRevision: disabledLocalOnly.User.Revision,
		Disabled: false, ChangedAt: model.GetMillis(), AuditEventID: reenableLocalOnlyAttempt.ID,
		AuditAt: model.MillisFromTime(reenableLocalOnlyAttempt.CreatedAt),
	}))
	require.NoError(t, err)
	replayReplacement := &store.AccessPolicyReplacement{Preflight: replacement.Preflight, ActorID: bootstrap.Administrator.ID}
	prepareAccessPolicyAttempt(t, ctx, ss, bootstrap, replayReplacement)
	replay, err := ss.AccessPolicy().Replace(ctx, replayReplacement, command)
	require.NoError(t, err)
	require.True(t, replay.Replayed)
	require.Equal(t, int64(2), replay.Snapshot.Policy.Revision)
	conflictingCommand := *command
	conflictingCommand.Fingerprint = sha256.Sum256([]byte("different-access-policy-command"))
	conflictReplacement := &store.AccessPolicyReplacement{Preflight: replacement.Preflight, ActorID: bootstrap.Administrator.ID}
	prepareAccessPolicyAttempt(t, ctx, ss, bootstrap, conflictReplacement)
	_, err = ss.AccessPolicy().Replace(ctx, conflictReplacement, &conflictingCommand)
	var conflict *store.ErrIdempotencyConflict
	require.ErrorAs(t, err, &conflict)
	_, err = ss.Audit().Complete(ctx, conflictReplacement.AuditEventID, model.AuditStatusFail, "idempotency.conflict", nil, conflictReplacement.AuditAt)
	require.NoError(t, err)
	for _, id := range []model.SessionID{passwordSession.ID} {
		preserved, getErr := ss.Session().Get(ctx, id)
		require.NoError(t, getErr)
		require.False(t, preserved.RevokedAt.Valid, "revoke_existing_sessions=false revoked %s", id)
	}
	campusCandidate, campusCredentials, _ := newSession(bootstrap.Administrator.ID.String())
	campusIdentity, err := ss.ExternalIdentity().Save(ctx, &model.ExternalIdentity{UserID: bootstrap.Administrator.ID,
		Provider: "campus", Subject: "access-policy-session-" + model.NewID(), LastSeenAt: model.OptionalTimeFrom(model.NowUTC())})
	require.NoError(t, err)
	campusCandidate.AuthenticationMethod = "oidc"
	campusCandidate.AuthenticationProviderID = "campus"
	campusCandidate.ExternalIdentityID = campusIdentity.ID
	campusSession, _, err := SaveSession(ctx, ss, testSessionCreation(t, ctx, ss, campusCandidate, campusCredentials, 10))
	require.NoError(t, err)
	if len(probes) > 0 && probes[0].LeadDatabaseClock != nil {
		probes[0].LeadDatabaseClock(t, ctx, campusSession.ID)
	}

	reenabled := settings
	reenabled.LocalLoginEnabled = true
	reenabled.InvitationLocalCredentialEnabled = true
	reenableCommand := &store.CommandIdempotency{UserID: bootstrap.Administrator.ID, Operation: "access_policy.replace",
		KeyDigest:   sha256.Sum256([]byte("access-policy-reenable-key")),
		Fingerprint: sha256.Sum256([]byte("access-policy-reenable-command")), Retention: time.Hour, Wait: time.Second}
	reenableReplacement := &store.AccessPolicyReplacement{Preflight: store.AccessPolicyPreflight{
		ExpectedRevision: 2, Settings: reenabled, Capabilities: capabilities, CheckedAt: model.NowUTC(),
	}, ActorID: bootstrap.Administrator.ID}
	prepareAccessPolicyAttempt(t, ctx, ss, bootstrap, reenableReplacement)
	reenabledResult, err := ss.AccessPolicy().Replace(ctx, reenableReplacement, reenableCommand)
	require.NoError(t, err)
	revokeCommand := &store.CommandIdempotency{UserID: bootstrap.Administrator.ID, Operation: "access_policy.replace",
		KeyDigest:   sha256.Sum256([]byte("access-policy-revoke-key")),
		Fingerprint: sha256.Sum256([]byte("access-policy-revoke-command")), Retention: time.Hour, Wait: time.Second}
	revokeReplacement := &store.AccessPolicyReplacement{Preflight: store.AccessPolicyPreflight{
		ExpectedRevision: reenabledResult.Snapshot.Policy.Revision, Settings: settings, RevokeExistingSessions: true,
		Capabilities: capabilities, CheckedAt: model.NowUTC(),
	}, ActorID: bootstrap.Administrator.ID}
	prepareAccessPolicyAttempt(t, ctx, ss, bootstrap, revokeReplacement)
	providerRemoved := reenabled
	providerRemoved.ProviderAdmissions = map[string]model.ProviderAdmissionMode{}
	revokeReplacement.Preflight.Settings = providerRemoved
	result, err = ss.AccessPolicy().Replace(ctx, revokeReplacement, revokeCommand)
	require.NoError(t, err)
	require.Len(t, result.SessionRevocations, 1)
	require.Len(t, result.SessionRevocations[0].SessionIDs, 1)
	require.True(t, containsSessionID(result.SessionRevocations[0].SessionIDs, campusSession.ID))
	require.Len(t, result.SessionRevocations[0].AccessTokenHashes, 1)
	require.NotEmpty(t, result.Snapshot.History)
	require.True(t, containsString(result.Snapshot.History[0].ChangedFields, "revoke_existing_sessions"))
	revokeReplayReplacement := &store.AccessPolicyReplacement{Preflight: store.AccessPolicyPreflight{
		ExpectedRevision: reenabledResult.Snapshot.Policy.Revision, Settings: providerRemoved, RevokeExistingSessions: true,
		Capabilities: capabilities, CheckedAt: model.NowUTC(),
	}, ActorID: bootstrap.Administrator.ID}
	prepareAccessPolicyAttempt(t, ctx, ss, bootstrap, revokeReplayReplacement)
	revocationReplay, err := ss.AccessPolicy().Replace(ctx, revokeReplayReplacement, revokeCommand)
	require.NoError(t, err)
	require.True(t, revocationReplay.Replayed)
	require.Empty(t, revocationReplay.SessionRevocations)
	revokedPassword, err := ss.Session().Get(ctx, passwordSession.ID)
	require.NoError(t, err)
	revokedCampus, err := ss.Session().Get(ctx, campusSession.ID)
	require.NoError(t, err)
	require.False(t, revokedPassword.RevokedAt.Valid, "password=%#v campus=%#v", revokedPassword, revokedCampus)
	require.True(t, revokedCampus.RevokedAt.Valid, "password=%#v campus=%#v", revokedPassword, revokedCampus)
	require.False(t, revokedCampus.RevokedAt.Time.Before(revokedCampus.CreatedAt), "password=%#v campus=%#v", revokedPassword, revokedCampus)

	removed := settings
	removed.ProviderAdmissions = map[string]model.ProviderAdmissionMode{"campus": model.ProviderAdmissionLinkedOnly}
	blocked, err = ss.AccessPolicy().Preflight(ctx, &store.AccessPolicyPreflight{ExpectedRevision: result.Snapshot.Policy.Revision, Settings: removed,
		Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}}, CheckedAt: model.NowUTC()})
	require.NoError(t, err)
	require.True(t, hasAccessPolicyBlocker(blocked, store.AccessPolicyBlockerProviderUnavailable, "campus"), "removed provider blockers = %#v", blocked)
	removed.ProviderAdmissions["campus"] = model.ProviderAdmissionAutoProvision
	blocked, err = ss.AccessPolicy().Preflight(ctx, &store.AccessPolicyPreflight{ExpectedRevision: result.Snapshot.Policy.Revision, Settings: removed,
		Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{"campus": {}}, DurableMail: true}, CheckedAt: model.NowUTC()})
	require.NoError(t, err)
	require.True(t, hasAccessPolicyBlocker(blocked, store.AccessPolicyBlockerProviderAdmissionUnsupported, "campus"), "unsupported admission blockers = %#v", blocked)
	removed.ProviderAdmissions["campus"] = model.ProviderAdmissionInvitationRequired
	blocked, err = ss.AccessPolicy().Preflight(ctx, &store.AccessPolicyPreflight{ExpectedRevision: result.Snapshot.Policy.Revision, Settings: removed,
		Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{"campus": {}}, DurableMail: false}, CheckedAt: model.NowUTC()})
	require.NoError(t, err)
	require.True(t, hasAccessPolicyBlocker(blocked, store.AccessPolicyBlockerInvitationMailUnavailable, "campus"), "invitation delivery blockers = %#v", blocked)

	current := result.Snapshot.Policy
	for i := 0; i < model.AccessPolicyTransitionHistoryLimit+1; i++ {
		boundedSettings := current.Settings()
		boundedSettings.DesktopAuthorizationEnabled = !boundedSettings.DesktopAuthorizationEnabled
		key := sha256.Sum256([]byte(fmt.Sprintf("access-policy-history-key-%d", i)))
		fingerprint := sha256.Sum256([]byte(fmt.Sprintf("access-policy-history-command-%d", i)))
		boundedCommand := &store.CommandIdempotency{UserID: bootstrap.Administrator.ID, Operation: "access_policy.replace",
			KeyDigest: key, Fingerprint: fingerprint,
			Retention: time.Hour, Wait: time.Second}
		boundedReplacement := &store.AccessPolicyReplacement{Preflight: store.AccessPolicyPreflight{
			ExpectedRevision: current.Revision, Settings: boundedSettings, Capabilities: capabilities, CheckedAt: model.NowUTC(),
		}, ActorID: bootstrap.Administrator.ID}
		prepareAccessPolicyAttempt(t, ctx, ss, bootstrap, boundedReplacement)
		boundedResult, replaceErr := ss.AccessPolicy().Replace(ctx, boundedReplacement, boundedCommand)
		require.NoError(t, replaceErr)
		current = boundedResult.Snapshot.Policy
	}
	bounded, err := ss.AccessPolicy().Get(ctx, model.AccessPolicyTransitionHistoryLimit)
	require.NoError(t, err)
	require.Len(t, bounded.History, model.AccessPolicyTransitionHistoryLimit, "bounded transition history")
	require.Equal(t, current.Revision, bounded.History[0].ToRevision, "bounded transition history")
	require.Equal(t, current.Revision-int64(model.AccessPolicyTransitionHistoryLimit)+1, bounded.History[len(bounded.History)-1].ToRevision, "bounded transition history")
	audits, err := ss.Audit().List(ctx, store.AuditListOptions{
		Action: string(model.ActionAccessPolicyManage), Limit: 200,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	require.NoError(t, err)
	require.Len(t, audits, model.AccessPolicyTransitionHistoryLimit+7, "access policy audits")
	var foundRevocationAudit, foundReplayAudit, foundConflictAudit bool
	for _, audit := range audits {
		var parameters struct {
			RevokeExistingSessions bool   `json:"revoke_existing_sessions"`
			RevokedSessionCount    int    `json:"revoked_session_count"`
			IdempotencyReplayed    bool   `json:"idempotency_replayed"`
			OriginalAuditEventID   string `json:"original_audit_event_id"`
		}
		if json.Unmarshal(audit.Result, &parameters) == nil && parameters.RevokeExistingSessions && parameters.RevokedSessionCount == 1 {
			foundRevocationAudit = true
		}
		foundReplayAudit = foundReplayAudit || parameters.IdempotencyReplayed && model.AuditEventID(parameters.OriginalAuditEventID).IsValid()
		foundConflictAudit = foundConflictAudit || audit.Status == model.AuditStatusFail && audit.ErrorCode == "idempotency.conflict"
	}
	require.True(t, foundRevocationAudit, "Access Policy audit coverage revocation")
	require.True(t, foundReplayAudit, "Access Policy audit coverage revocation")
	require.True(t, foundConflictAudit, "Access Policy audit coverage revocation")
	if len(probes) != 0 && probes[0].HoldAuthenticationPathFence != nil && probes[0].WaitForBlockedTransactions != nil {
		testConcurrentAccessPolicyAndAdministratorDisable(t, ctx, ss, bootstrap, current, probes[0])
	}
}

func testConcurrentAccessPolicyAndAdministratorDisable(t *testing.T, ctx context.Context, ss store.Store,
	bootstrap *model.InstallationBootstrapResult, current *model.AccessPolicy, probe AccessPolicySQLProbe,
) {
	t.Helper()
	candidate := current.Settings()
	candidate.LocalLoginEnabled = false
	candidate.InvitationLocalCredentialEnabled = false
	candidate.ProviderAdmissions = map[string]model.ProviderAdmissionMode{"campus": model.ProviderAdmissionLinkedOnly}
	capabilities := store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{"campus": {AutoProvision: true}}, DurableMail: true}
	replacement := &store.AccessPolicyReplacement{Preflight: store.AccessPolicyPreflight{
		ExpectedRevision: current.Revision, Settings: candidate, Capabilities: capabilities, CheckedAt: model.NowUTC(),
	}, ActorID: bootstrap.Administrator.ID}
	prepareAccessPolicyAttempt(t, ctx, ss, bootstrap, replacement)
	command := &store.CommandIdempotency{UserID: bootstrap.Administrator.ID, Operation: "access_policy.replace",
		KeyDigest:   sha256.Sum256([]byte("access-policy-concurrent-key")),
		Fingerprint: sha256.Sum256([]byte("access-policy-concurrent-command")),
		Retention:   time.Hour, Wait: time.Second}
	disableAttempt := saveUserProfileAuditAttempt(t, ctx, ss, bootstrap.Administrator.ID.String())

	blockerPID, release := probe.HoldAuthenticationPathFence(t, ctx)
	released := false
	defer func() {
		if !released {
			release()
		}
	}()
	type outcome struct {
		operation string
		err       error
	}
	outcomes := make(chan outcome, 2)
	go func() {
		_, err := ss.AccessPolicy().Replace(ctx, replacement, command)
		outcomes <- outcome{operation: "policy", err: err}
	}()
	disable := userDisabledStateChangeWithNotice(t, &store.UserDisabledStateChange{
		ID: bootstrap.Administrator.ID, ExpectedRevision: bootstrap.Administrator.Revision,
		Disabled: true, ChangedAt: model.GetMillis(), RevocationReason: model.SessionRevocationAccountDisabled,
		AuditEventID: disableAttempt.ID, AuditAt: model.MillisFromTime(disableAttempt.CreatedAt),
	})
	go func() {
		_, err := ss.User().SetDisabled(ctx, disable)
		outcomes <- outcome{operation: "disable", err: err}
	}()
	probe.WaitForBlockedTransactions(t, ctx, blockerPID, 2)
	release()
	released = true

	successes, rejected := 0, 0
	for range 2 {
		result := <-outcomes
		if result.err == nil {
			successes++
			continue
		}
		var policyBlocked *store.ErrAccessPolicyBlocked
		var userConflict *store.ErrConflict
		switch {
		case result.operation == "policy" && errors.As(result.err, &policyBlocked) &&
			hasAccessPolicyBlocker(policyBlocked.Blockers, store.AccessPolicyBlockerLastAdministratorPath, ""):
			rejected++
		case result.operation == "disable" && errors.As(result.err, &userConflict) &&
			userConflict.Constraint == "users_last_system_admin":
			rejected++
		default:
			require.FailNow(t, fmt.Sprintf("concurrent %s error = %v", result.operation, result.err))
		}
	}
	require.Equal(t, 1, successes, "concurrent policy/disable outcomes success")
	require.Equal(t, 1, rejected, "concurrent policy/disable outcomes success")
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func containsSessionID(values []model.SessionID, target model.SessionID) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func hasAccessPolicyBlocker(blockers []store.AccessPolicyBlocker, code store.AccessPolicyBlockerCode, providerID string) bool {
	for _, blocker := range blockers {
		if blocker.Code == code && blocker.ProviderID == providerID {
			return true
		}
	}
	return false
}

func prepareAccessPolicyAttempt(t *testing.T, ctx context.Context, ss store.Store, bootstrap *model.InstallationBootstrapResult, replacement *store.AccessPolicyReplacement) {
	t.Helper()
	event, err := ss.Audit().Save(ctx, &model.AuditEvent{ActorID: bootstrap.Administrator.ID, Action: string(model.ActionAccessPolicyManage),
		Resource:  model.Resource{Type: model.ResourceInstitution, ID: bootstrap.Institution.ID.String()},
		ScopeType: model.RoleScopeInstitution, ScopeID: bootstrap.Institution.ID.String(), Status: model.AuditStatusAttempt,
		NodeID: "store-test", ClientType: string(model.SessionClientWeb), AuthMethod: "password"})
	require.NoError(t, err)
	replacement.AuditEventID = event.ID
	replacement.AuditAt = model.MillisFromTime(event.CreatedAt)
}
