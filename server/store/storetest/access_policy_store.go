// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type AccessPolicySQLProbe struct {
	HoldAuthenticationPathFence func(*testing.T, context.Context) (int, func())
	WaitForBlockedTransactions  func(*testing.T, context.Context, int, int)
}

func TestAccessPolicyStore(t *testing.T, ss store.Store, probes ...AccessPolicySQLProbe) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	bootstrap, err := ss.Installation().Bootstrap(ctx, testInstallationBootstrap(301))
	requireNoError(t, err)
	snapshot, err := ss.AccessPolicy().Get(ctx, model.AccessPolicyTransitionHistoryLimit)
	requireNoError(t, err)
	if snapshot.Policy.ID != bootstrap.AccessPolicy.ID || snapshot.Policy.Revision != 1 || len(snapshot.History) != 0 {
		t.Fatalf("initial policy snapshot = %#v", snapshot)
	}

	settings := snapshot.Policy.Settings()
	settings.LocalLoginEnabled = false
	settings.InvitationLocalCredentialEnabled = false
	blocked, err := ss.AccessPolicy().Preflight(ctx, &store.AccessPolicyPreflight{ExpectedRevision: 1, Settings: settings,
		Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}}, CheckedAt: model.NowUTC()})
	requireNoError(t, err)
	if !hasAccessPolicyBlocker(blocked, store.AccessPolicyBlockerLastAdministratorPath, "") {
		t.Fatalf("local lockout blockers = %#v", blocked)
	}

	_, err = ss.ExternalIdentity().Save(ctx, &model.ExternalIdentity{UserID: bootstrap.Administrator.ID,
		Provider: "campus", Subject: "admin-subject", LastSeenAt: model.OptionalTimeFrom(model.NowUTC())})
	requireNoError(t, err)
	localOnlyResult, err := ss.User().Create(ctx, testUserCreation(newUser(), &model.PasswordCredential{PasswordHash: "encoded-password"}))
	requireNoError(t, err)
	_, err = ss.RoleBinding().Save(ctx, &model.RoleBinding{
		UserID: localOnlyResult.User.ID, RoleID: bootstrap.Role.ID,
		ScopeType: model.RoleScopeInstitution, ScopeID: bootstrap.Institution.ID.String(),
		StartsAt: model.TimeFromMillis(model.GetMillis() - 100),
	})
	requireNoError(t, err)
	settings.ProviderAdmissions["campus"] = model.ProviderAdmissionLinkedOnly
	capabilities := store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{"campus": {AutoProvision: true}}, DurableMail: true}
	blocked, err = ss.AccessPolicy().Preflight(ctx, &store.AccessPolicyPreflight{ExpectedRevision: 1, Settings: settings,
		Capabilities: capabilities, CheckedAt: model.NowUTC()})
	requireNoError(t, err)
	if len(blocked) != 0 {
		t.Fatalf("linked administrator blockers = %#v", blocked)
	}
	passwordSession, _, _ := saveSession(t, ctx, ss, bootstrap.Administrator.ID.String(), 10)

	command := &store.CommandIdempotency{UserID: bootstrap.Administrator.ID, Operation: "access_policy.replace.v1",
		KeyDigest: sha256.Sum256([]byte("access-policy-key")), FingerprintVersion: 1,
		Fingerprint: sha256.Sum256([]byte("access-policy-command")), OutcomeVersion: 1, Retention: time.Hour, Wait: time.Second}
	replacement := &store.AccessPolicyReplacement{Preflight: store.AccessPolicyPreflight{ExpectedRevision: 1, Settings: settings,
		Capabilities: capabilities, CheckedAt: model.NowUTC()}, ActorID: bootstrap.Administrator.ID,
	}
	prepareAccessPolicyAttempt(t, ctx, ss, bootstrap, replacement)
	result, err := ss.AccessPolicy().Replace(ctx, replacement, command)
	requireNoError(t, err)
	if result.Replayed || !result.Changed || result.Snapshot.Policy.Revision != 2 || len(result.Snapshot.History) != 1 ||
		result.Snapshot.History[0].ActorID != bootstrap.Administrator.ID {
		t.Fatalf("replacement result = %#v", result)
	}
	// The replacement deliberately leaves the first administrator's external
	// identity as the installation's only usable path. The second administrator
	// has a password, but local login is now disabled by policy.
	disableAttempt := saveUserProfileAuditAttempt(t, ctx, ss, bootstrap.Administrator.ID.String())
	_, err = ss.User().SetDisabledWithAudit(ctx, &store.UserDisabledStateChange{
		ID: bootstrap.Administrator.ID.String(), ExpectedRevision: bootstrap.Administrator.Revision,
		Disabled: true, ChangedAt: model.GetMillis(), RevocationReason: "administrator disabled account",
		AuditEventID: disableAttempt.ID.String(), AuditAt: model.MillisFromTime(disableAttempt.CreatedAt),
	})
	var lastPathConflict *store.ErrConflict
	if !errors.As(err, &lastPathConflict) || lastPathConflict.Constraint != "users_last_system_admin" {
		t.Fatalf("disable only usable administrator path error = %v", err)
	}
	if _, err = ss.RoleBinding().End(ctx, bootstrap.RoleBinding.ID.String(), model.GetMillis()); !errors.As(err, &lastPathConflict) || lastPathConflict.Constraint != "role_bindings_last_system_admin" {
		t.Fatalf("end only usable administrator path binding error = %v", err)
	}
	// Durable policy retains campus while deployment capability snapshots may
	// remove it. Omission must fail closed; restoring the exact provider makes
	// the external administrator usable again. Local credentials remain governed
	// only by LocalLoginEnabled.
	removedProviderAttempt := saveUserProfileAuditAttempt(t, ctx, ss, localOnlyResult.User.ID.String())
	_, err = ss.User().SetDisabledWithAudit(ctx, &store.UserDisabledStateChange{
		ID: localOnlyResult.User.ID.String(), ExpectedRevision: localOnlyResult.User.Revision,
		Disabled: true, ChangedAt: model.GetMillis(), RevocationReason: "administrator disabled account",
		AuditEventID: removedProviderAttempt.ID.String(), AuditAt: model.MillisFromTime(removedProviderAttempt.CreatedAt),
	})
	if !errors.As(err, &lastPathConflict) || lastPathConflict.Constraint != "users_last_system_admin" {
		t.Fatalf("removed provider counted as a usable administrator path: %v", err)
	}
	availableProviderAttempt := saveUserProfileAuditAttempt(t, ctx, ss, localOnlyResult.User.ID.String())
	disabledLocalOnly, err := ss.User().SetDisabledWithAudit(ctx, &store.UserDisabledStateChange{
		ID: localOnlyResult.User.ID.String(), ExpectedRevision: localOnlyResult.User.Revision,
		Disabled: true, Capabilities: capabilities, ChangedAt: model.GetMillis(),
		RevocationReason: "administrator disabled account", AuditEventID: availableProviderAttempt.ID.String(),
		AuditAt: model.MillisFromTime(availableProviderAttempt.CreatedAt),
	})
	requireNoError(t, err)
	reenableLocalOnlyAttempt := saveUserProfileAuditAttempt(t, ctx, ss, localOnlyResult.User.ID.String())
	_, err = ss.User().SetDisabledWithAudit(ctx, &store.UserDisabledStateChange{
		ID: localOnlyResult.User.ID.String(), ExpectedRevision: disabledLocalOnly.User.Revision,
		Disabled: false, ChangedAt: model.GetMillis(), AuditEventID: reenableLocalOnlyAttempt.ID.String(),
		AuditAt: model.MillisFromTime(reenableLocalOnlyAttempt.CreatedAt),
	})
	requireNoError(t, err)
	replayReplacement := &store.AccessPolicyReplacement{Preflight: replacement.Preflight, ActorID: bootstrap.Administrator.ID}
	prepareAccessPolicyAttempt(t, ctx, ss, bootstrap, replayReplacement)
	replay, err := ss.AccessPolicy().Replace(ctx, replayReplacement, command)
	requireNoError(t, err)
	if !replay.Replayed || replay.Snapshot.Policy.Revision != 2 {
		t.Fatalf("replay = %#v", replay)
	}
	conflictingCommand := *command
	conflictingCommand.Fingerprint = sha256.Sum256([]byte("different-access-policy-command"))
	conflictReplacement := &store.AccessPolicyReplacement{Preflight: replacement.Preflight, ActorID: bootstrap.Administrator.ID}
	prepareAccessPolicyAttempt(t, ctx, ss, bootstrap, conflictReplacement)
	_, err = ss.AccessPolicy().Replace(ctx, conflictReplacement, &conflictingCommand)
	var conflict *store.ErrIdempotencyConflict
	if !errors.As(err, &conflict) {
		t.Fatalf("conflicting key reuse error = %v", err)
	}
	_, err = ss.Audit().Complete(ctx, conflictReplacement.AuditEventID, model.AuditStatusFail, "idempotency.conflict", nil, conflictReplacement.AuditAt)
	requireNoError(t, err)
	for _, id := range []model.SessionID{passwordSession.ID} {
		preserved, getErr := ss.Session().Get(ctx, id.String())
		requireNoError(t, getErr)
		if preserved.RevokedAt.Valid {
			t.Fatalf("revoke_existing_sessions=false revoked %s", id)
		}
	}
	campusCandidate, campusCredentials, _ := newSession(bootstrap.Administrator.ID.String())
	campusIdentity, err := ss.ExternalIdentity().Save(ctx, &model.ExternalIdentity{UserID: bootstrap.Administrator.ID,
		Provider: "campus", Subject: "access-policy-session-" + model.NewId(), LastSeenAt: model.OptionalTimeFrom(model.NowUTC())})
	requireNoError(t, err)
	campusCandidate.AuthenticationMethod = "oidc"
	campusCandidate.AuthenticationProviderID = "campus"
	campusCandidate.ExternalIdentityID = campusIdentity.ID
	campusSession, _, err := ss.Session().Save(ctx, campusCandidate, campusCredentials, 10)
	requireNoError(t, err)

	reenabled := settings
	reenabled.LocalLoginEnabled = true
	reenabled.InvitationLocalCredentialEnabled = true
	reenableCommand := &store.CommandIdempotency{UserID: bootstrap.Administrator.ID, Operation: "access_policy.replace.v1",
		KeyDigest: sha256.Sum256([]byte("access-policy-reenable-key")), FingerprintVersion: 1,
		Fingerprint: sha256.Sum256([]byte("access-policy-reenable-command")), OutcomeVersion: 1, Retention: time.Hour, Wait: time.Second}
	reenableReplacement := &store.AccessPolicyReplacement{Preflight: store.AccessPolicyPreflight{
		ExpectedRevision: 2, Settings: reenabled, Capabilities: capabilities, CheckedAt: model.NowUTC(),
	}, ActorID: bootstrap.Administrator.ID}
	prepareAccessPolicyAttempt(t, ctx, ss, bootstrap, reenableReplacement)
	reenabledResult, err := ss.AccessPolicy().Replace(ctx, reenableReplacement, reenableCommand)
	requireNoError(t, err)
	revokeCommand := &store.CommandIdempotency{UserID: bootstrap.Administrator.ID, Operation: "access_policy.replace.v1",
		KeyDigest: sha256.Sum256([]byte("access-policy-revoke-key")), FingerprintVersion: 1,
		Fingerprint: sha256.Sum256([]byte("access-policy-revoke-command")), OutcomeVersion: 1, Retention: time.Hour, Wait: time.Second}
	revokeReplacement := &store.AccessPolicyReplacement{Preflight: store.AccessPolicyPreflight{
		ExpectedRevision: reenabledResult.Snapshot.Policy.Revision, Settings: settings, RevokeExistingSessions: true,
		Capabilities: capabilities, CheckedAt: model.NowUTC(),
	}, ActorID: bootstrap.Administrator.ID}
	prepareAccessPolicyAttempt(t, ctx, ss, bootstrap, revokeReplacement)
	providerRemoved := reenabled
	providerRemoved.ProviderAdmissions = map[string]model.ProviderAdmissionMode{}
	revokeReplacement.Preflight.Settings = providerRemoved
	result, err = ss.AccessPolicy().Replace(ctx, revokeReplacement, revokeCommand)
	requireNoError(t, err)
	if len(result.SessionRevocations) != 1 || len(result.SessionRevocations[0].SessionIDs) != 1 ||
		!containsSessionID(result.SessionRevocations[0].SessionIDs, campusSession.ID) ||
		len(result.SessionRevocations[0].AccessTokenHashes) != 1 {
		t.Fatalf("targeted revocations = %#v", result.SessionRevocations)
	}
	if len(result.Snapshot.History) == 0 || !containsString(result.Snapshot.History[0].ChangedFields, "revoke_existing_sessions") {
		t.Fatalf("revocation choice history = %#v", result.Snapshot.History)
	}
	revokeReplayReplacement := &store.AccessPolicyReplacement{Preflight: store.AccessPolicyPreflight{
		ExpectedRevision: reenabledResult.Snapshot.Policy.Revision, Settings: providerRemoved, RevokeExistingSessions: true,
		Capabilities: capabilities, CheckedAt: model.NowUTC(),
	}, ActorID: bootstrap.Administrator.ID}
	prepareAccessPolicyAttempt(t, ctx, ss, bootstrap, revokeReplayReplacement)
	revocationReplay, err := ss.AccessPolicy().Replace(ctx, revokeReplayReplacement, revokeCommand)
	requireNoError(t, err)
	if !revocationReplay.Replayed || len(revocationReplay.SessionRevocations) != 0 {
		t.Fatalf("revocation replay = %#v", revocationReplay)
	}
	revokedPassword, err := ss.Session().Get(ctx, passwordSession.ID.String())
	requireNoError(t, err)
	revokedCampus, err := ss.Session().Get(ctx, campusSession.ID.String())
	requireNoError(t, err)
	if revokedPassword.RevokedAt.Valid || !revokedCampus.RevokedAt.Valid {
		t.Fatalf("password=%#v campus=%#v", revokedPassword, revokedCampus)
	}

	removed := settings
	removed.ProviderAdmissions = map[string]model.ProviderAdmissionMode{"campus": model.ProviderAdmissionLinkedOnly}
	blocked, err = ss.AccessPolicy().Preflight(ctx, &store.AccessPolicyPreflight{ExpectedRevision: result.Snapshot.Policy.Revision, Settings: removed,
		Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}}, CheckedAt: model.NowUTC()})
	requireNoError(t, err)
	if !hasAccessPolicyBlocker(blocked, store.AccessPolicyBlockerProviderUnavailable, "campus") {
		t.Fatalf("removed provider blockers = %#v", blocked)
	}
	removed.ProviderAdmissions["campus"] = model.ProviderAdmissionAutoProvision
	blocked, err = ss.AccessPolicy().Preflight(ctx, &store.AccessPolicyPreflight{ExpectedRevision: result.Snapshot.Policy.Revision, Settings: removed,
		Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{"campus": {}}, DurableMail: true}, CheckedAt: model.NowUTC()})
	requireNoError(t, err)
	if !hasAccessPolicyBlocker(blocked, store.AccessPolicyBlockerProviderAdmissionUnsupported, "campus") {
		t.Fatalf("unsupported admission blockers = %#v", blocked)
	}
	removed.ProviderAdmissions["campus"] = model.ProviderAdmissionInvitationRequired
	blocked, err = ss.AccessPolicy().Preflight(ctx, &store.AccessPolicyPreflight{ExpectedRevision: result.Snapshot.Policy.Revision, Settings: removed,
		Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{"campus": {}}, DurableMail: false}, CheckedAt: model.NowUTC()})
	requireNoError(t, err)
	if !hasAccessPolicyBlocker(blocked, store.AccessPolicyBlockerInvitationMailUnavailable, "campus") {
		t.Fatalf("invitation delivery blockers = %#v", blocked)
	}

	current := result.Snapshot.Policy
	for i := 0; i < model.AccessPolicyTransitionHistoryLimit+1; i++ {
		boundedSettings := current.Settings()
		boundedSettings.DesktopAuthorizationEnabled = !boundedSettings.DesktopAuthorizationEnabled
		key := sha256.Sum256([]byte(fmt.Sprintf("access-policy-history-key-%d", i)))
		fingerprint := sha256.Sum256([]byte(fmt.Sprintf("access-policy-history-command-%d", i)))
		boundedCommand := &store.CommandIdempotency{UserID: bootstrap.Administrator.ID, Operation: "access_policy.replace.v1",
			KeyDigest: key, FingerprintVersion: 1, Fingerprint: fingerprint, OutcomeVersion: 1,
			Retention: time.Hour, Wait: time.Second}
		boundedReplacement := &store.AccessPolicyReplacement{Preflight: store.AccessPolicyPreflight{
			ExpectedRevision: current.Revision, Settings: boundedSettings, Capabilities: capabilities, CheckedAt: model.NowUTC(),
		}, ActorID: bootstrap.Administrator.ID}
		prepareAccessPolicyAttempt(t, ctx, ss, bootstrap, boundedReplacement)
		boundedResult, replaceErr := ss.AccessPolicy().Replace(ctx, boundedReplacement, boundedCommand)
		requireNoError(t, replaceErr)
		current = boundedResult.Snapshot.Policy
	}
	bounded, err := ss.AccessPolicy().Get(ctx, model.AccessPolicyTransitionHistoryLimit)
	requireNoError(t, err)
	if len(bounded.History) != model.AccessPolicyTransitionHistoryLimit || bounded.History[0].ToRevision != current.Revision ||
		bounded.History[len(bounded.History)-1].ToRevision != current.Revision-int64(model.AccessPolicyTransitionHistoryLimit)+1 {
		t.Fatalf("bounded transition history = %d %#v", len(bounded.History), bounded.History)
	}
	audits, err := ss.Audit().List(ctx, store.AuditListOptions{
		Action: string(model.ActionAccessPolicyManage), Limit: 200,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	requireNoError(t, err)
	if len(audits) != model.AccessPolicyTransitionHistoryLimit+7 {
		t.Fatalf("access policy audits = %d, want %d", len(audits), model.AccessPolicyTransitionHistoryLimit+7)
	}
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
		foundReplayAudit = foundReplayAudit || parameters.IdempotencyReplayed && model.IsValidId(parameters.OriginalAuditEventID)
		foundConflictAudit = foundConflictAudit || audit.Status == model.AuditStatusFail && audit.ErrorCode == "idempotency.conflict"
	}
	if !foundRevocationAudit || !foundReplayAudit || !foundConflictAudit {
		t.Fatalf("Access Policy audit coverage revocation=%v replay=%v conflict=%v", foundRevocationAudit, foundReplayAudit, foundConflictAudit)
	}
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
	command := &store.CommandIdempotency{UserID: bootstrap.Administrator.ID, Operation: "access_policy.replace.v1",
		KeyDigest: sha256.Sum256([]byte("access-policy-concurrent-key")), FingerprintVersion: 1,
		Fingerprint: sha256.Sum256([]byte("access-policy-concurrent-command")), OutcomeVersion: 1,
		Retention: time.Hour, Wait: time.Second}
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
	go func() {
		_, err := ss.User().SetDisabledWithAudit(ctx, &store.UserDisabledStateChange{
			ID: bootstrap.Administrator.ID.String(), ExpectedRevision: bootstrap.Administrator.Revision,
			Disabled: true, ChangedAt: model.GetMillis(), RevocationReason: "administrator disabled account",
			AuditEventID: disableAttempt.ID.String(), AuditAt: model.MillisFromTime(disableAttempt.CreatedAt),
		})
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
			t.Fatalf("concurrent %s error = %v", result.operation, result.err)
		}
	}
	if successes != 1 || rejected != 1 {
		t.Fatalf("concurrent policy/disable outcomes success=%d rejected=%d", successes, rejected)
	}
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
	requireNoError(t, err)
	replacement.AuditEventID = event.ID.String()
	replacement.AuditAt = model.MillisFromTime(event.CreatedAt)
}
