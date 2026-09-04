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

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// TestAdministratorRecoveryStore exercises the offline installation-level
// aggregate. The supplied password hashes are deliberately synthetic: the
// Store owns atomic persistence, while the application owns password hashing.
type AdministratorRecoverySQLProbe struct {
	HoldAuthenticationPathFence func(*testing.T, context.Context) (int, func())
	HoldServingNodeLeaseFence   func(*testing.T, context.Context) (int, func())
	WaitForBlockedTransactions  func(*testing.T, context.Context, int, int)
}

func TestAdministratorRecoveryStore(t *testing.T, ss store.Store, probes ...AdministratorRecoverySQLProbe) {
	t.Helper()
	ctx := context.Background()

	if _, err := ss.Installation().RecoverAdministratorAccess(ctx, &store.AdministratorRecovery{
		InstitutionID: model.NewInstitutionID(), UserID: model.NewUserID(), RotatePasswordHash: "new-hash",
	}); !store.IsNotFound(err) {
		t.Fatalf("RecoverAdministratorAccess(pristine) error = %v, want not found", err)
	}

	installed, err := ss.Installation().Bootstrap(ctx, testInstallationBootstrap(451))
	requireNoError(t, err)
	liveLeaseID := model.NewId()
	liveLease, err := ss.ServingNodeLease().Upsert(ctx, &store.ServingNodeLeaseClaim{
		NodeID: "serving-node-a", LeaseID: liveLeaseID, Lifetime: 30 * time.Second,
	})
	requireNoError(t, err)
	if liveLease == nil || liveLease.NodeID != "serving-node-a" || !liveLease.ExpiresAt.After(liveLease.UpdatedAt) {
		t.Fatalf("serving node lease = %#v", liveLease)
	}
	beforeLiveNodeRejection, err := ss.PasswordCredential().GetByUser(ctx, installed.Administrator.ID.String())
	requireNoError(t, err)
	if _, err = ss.Installation().RecoverAdministratorAccess(ctx, &store.AdministratorRecovery{
		InstitutionID: installed.Institution.ID, UserID: installed.Administrator.ID,
		RotatePasswordHash: "must-not-commit-while-a-node-is-serving",
	}); !store.IsConflict(err) {
		t.Fatalf("RecoverAdministratorAccess(live serving node) error = %v, want conflict", err)
	}
	afterLiveNodeRejection, err := ss.PasswordCredential().GetByUser(ctx, installed.Administrator.ID.String())
	requireNoError(t, err)
	if afterLiveNodeRejection.PasswordHash != beforeLiveNodeRejection.PasswordHash {
		t.Fatal("live-node rejection changed the administrator credential")
	}
	noPendingAfterLiveNode, err := ss.Installation().ReconcileAdministratorRecovery(ctx, &store.AdministratorRecoveryReconciliation{NodeID: "store-test-recovery"})
	requireNoError(t, err)
	if noPendingAfterLiveNode == nil || noPendingAfterLiveNode.Reconciled != 0 {
		t.Fatalf("live-node rejection left pending recovery = %#v", noPendingAfterLiveNode)
	}
	requireNoError(t, ss.ServingNodeLease().Delete(ctx, "serving-node-a", liveLeaseID))
	staleLeaseID := model.NewId()
	_, err = ss.ServingNodeLease().Upsert(ctx, &store.ServingNodeLeaseClaim{
		NodeID: "stale-serving-node", LeaseID: staleLeaseID, Lifetime: store.ServingNodeLeaseMinimumLifetime,
	})
	requireNoError(t, err)
	time.Sleep(5 * store.ServingNodeLeaseMinimumLifetime)
	if _, err = ss.ServingNodeLease().Upsert(ctx, &store.ServingNodeLeaseClaim{
		NodeID: "stale-serving-node", LeaseID: staleLeaseID, Lifetime: 30 * time.Second,
	}); !store.IsConflict(err) {
		t.Fatalf("Upsert(expired process incarnation) error = %v, want conflict", err)
	}
	restartedLeaseID := model.NewId()
	if _, err = ss.ServingNodeLease().Upsert(ctx, &store.ServingNodeLeaseClaim{
		NodeID: "stale-serving-node", LeaseID: restartedLeaseID, Lifetime: 30 * time.Second,
	}); err != nil {
		t.Fatalf("Upsert(fresh process incarnation after expiry) error = %v", err)
	}
	requireNoError(t, ss.ServingNodeLease().Delete(ctx, "stale-serving-node", restartedLeaseID))
	preservedSession, _, _ := saveSession(t, ctx, ss, installed.Administrator.ID.String(), 10)
	pendingMFA := savePendingMFA(t, ctx, ss, installed.Administrator.ID)
	mfaAt := model.MillisFromTime(pendingMFA.CreatedAt) + 1
	mfaAudit, mfaNotice := mfaSecurityNoticeFixture(t, ctx, ss, installed.Administrator, model.MailTemplateIdentityMFAEnabled, mfaAt)
	_, err = ss.MFA().Activate(ctx, &store.MFAActivationMutation{
		CredentialID: pendingMFA.ID.String(), UserID: installed.Administrator.ID.String(), TimeStep: 451,
		RecoveryCodes: []*model.MFARecoveryCode{{CodeHash: model.HashToken(model.NewCredentialToken())}},
		SessionID:     preservedSession.ID.String(), At: mfaAt, AuditEventID: mfaAudit.ID.String(), AuditAt: mfaAt,
		Notice: mfaNotice,
	})
	requireNoError(t, err)
	before, err := ss.PasswordCredential().GetByUser(ctx, installed.Administrator.ID.String())
	requireNoError(t, err)

	input := &store.AdministratorRecovery{
		InstitutionID:      installed.Institution.ID,
		UserID:             installed.Administrator.ID,
		RotatePasswordHash: "$argon2id$v=19$m=19456,t=2,p=1$YWJjZGVmZ2hpamtsbW5vcA$YWJjZGVmZ2hpamtsbW5vcA",
	}
	recovered, err := ss.Installation().RecoverAdministratorAccess(ctx, input)
	requireNoError(t, err)
	if recovered == nil || !recovered.PasswordRotated || recovered.LocalLoginEnabled {
		t.Fatalf("RecoverAdministratorAccess() = %#v", recovered)
	}
	after, err := ss.PasswordCredential().GetByUser(ctx, installed.Administrator.ID.String())
	requireNoError(t, err)
	if after.PasswordHash != input.RotatePasswordHash || after.PasswordHash == before.PasswordHash ||
		!after.PasswordChangedAt.After(before.PasswordChangedAt) {
		t.Fatalf("password credential before=%#v after=%#v", before, after)
	}
	if sessions, listErr := ss.Session().ListByUser(ctx, installed.Administrator.ID.String()); listErr != nil || len(sessions) != 1 || sessions[0].ID != preservedSession.ID || sessions[0].RevokedAt.Valid {
		t.Fatalf("recovery sessions=%#v error=%v", sessions, listErr)
	}
	activeMFA, err := ss.MFA().GetByUser(ctx, installed.Administrator.ID.String())
	requireNoError(t, err)
	if !activeMFA.IsActive() {
		t.Fatalf("recovery changed MFA credential: %#v", activeMFA)
	}
	if events, listErr := ss.Audit().List(ctx, store.AuditListOptions{
		Action: "authentication.administrator_recovery", Limit: 10,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	}); listErr != nil || len(events) != 0 {
		t.Fatalf("pre-startup recovery audits=%#v error=%v", events, listErr)
	}

	if _, err := ss.Installation().RecoverAdministratorAccess(ctx, input); !store.IsConflict(err) {
		t.Fatalf("RecoverAdministratorAccess(repeated pending) error=%v, want conflict", err)
	}

	reconciled, err := ss.Installation().ReconcileAdministratorRecovery(ctx, &store.AdministratorRecoveryReconciliation{
		NodeID: "store-test-recovery",
	})
	requireNoError(t, err)
	if reconciled == nil || reconciled.Reconciled != 1 {
		t.Fatalf("ReconcileAdministratorRecovery() = %#v", reconciled)
	}
	events, err := ss.Audit().List(ctx, store.AuditListOptions{
		Action: "authentication.administrator_recovery", Limit: 10,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	requireNoError(t, err)
	if len(events) != 1 || events[0].Status != model.AuditStatusSuccess || !events[0].ActorID.IsZero() ||
		events[0].Resource != (model.Resource{Type: model.ResourceUser, ID: installed.Administrator.ID.String()}) {
		t.Fatalf("recovery audit = %#v", events)
	}
	var result struct {
		LocalLoginEnabled bool     `json:"local_login_enabled"`
		PasswordRotated   bool     `json:"password_rotated"`
		ChangedFields     []string `json:"changed_fields"`
	}
	if err := json.Unmarshal(events[0].Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.LocalLoginEnabled || !result.PasswordRotated || len(result.ChangedFields) != 1 || result.ChangedFields[0] != "password_credential" {
		t.Fatalf("recovery audit result = %#v", result)
	}
	noOp, err := ss.Installation().ReconcileAdministratorRecovery(ctx, &store.AdministratorRecoveryReconciliation{NodeID: "store-test-recovery"})
	requireNoError(t, err)
	if noOp == nil || noOp.Reconciled != 0 {
		t.Fatalf("ReconcileAdministratorRecovery(no-op) = %#v", noOp)
	}

	if _, err := ss.Installation().RecoverAdministratorAccess(ctx, &store.AdministratorRecovery{
		InstitutionID: model.NewInstitutionID(), UserID: installed.Administrator.ID, EnableLocalLogin: true,
	}); !store.IsConflict(err) {
		t.Fatalf("wrong-installation recovery error = %v, want conflict", err)
	}
	if _, err := ss.Installation().RecoverAdministratorAccess(ctx, &store.AdministratorRecovery{
		InstitutionID: installed.Institution.ID, UserID: model.NewUserID(), EnableLocalLogin: true,
	}); !store.IsConflict(err) {
		t.Fatalf("wrong-target recovery error = %v, want conflict", err)
	}

	_, err = ss.ExternalIdentity().Save(ctx, &model.ExternalIdentity{
		UserID: installed.Administrator.ID, Provider: "recovery-provider", Subject: "recovery-admin",
		LastSeenAt: model.OptionalTimeFrom(model.NowUTC()),
	})
	requireNoError(t, err)
	snapshot, err := ss.AccessPolicy().Get(ctx, model.AccessPolicyTransitionHistoryLimit)
	requireNoError(t, err)
	settings := snapshot.Policy.Settings()
	settings.LocalLoginEnabled = false
	settings.InvitationLocalCredentialEnabled = false
	settings.ProviderAdmissions["recovery-provider"] = model.ProviderAdmissionLinkedOnly
	capabilities := store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{
		"recovery-provider": {AutoProvision: true},
	}}
	replacement := &store.AccessPolicyReplacement{Preflight: store.AccessPolicyPreflight{
		ExpectedRevision: snapshot.Policy.Revision, Settings: settings, Capabilities: capabilities, CheckedAt: model.NowUTC(),
	}, ActorID: installed.Administrator.ID}
	prepareAccessPolicyAttempt(t, ctx, ss, installed, replacement)
	_, err = ss.AccessPolicy().Replace(ctx, replacement, &store.CommandIdempotency{
		UserID: installed.Administrator.ID, Operation: "access_policy.replace.v1",
		KeyDigest: sha256.Sum256([]byte("administrator-recovery-disable-local")), FingerprintVersion: 1,
		Fingerprint: sha256.Sum256([]byte("administrator-recovery-disable-local-command")), OutcomeVersion: 1,
		Retention: time.Hour, Wait: time.Second,
	})
	requireNoError(t, err)
	beforeRejectedRotation, err := ss.PasswordCredential().GetByUser(ctx, installed.Administrator.ID.String())
	requireNoError(t, err)
	if _, err := ss.Installation().RecoverAdministratorAccess(ctx, &store.AdministratorRecovery{
		InstitutionID: installed.Institution.ID, UserID: installed.Administrator.ID,
		RotatePasswordHash: "must-not-commit-while-local-login-is-disabled",
	}); !store.IsConflict(err) {
		t.Fatalf("rotate-only with disabled local login error=%v, want conflict", err)
	}
	afterRejectedRotation, err := ss.PasswordCredential().GetByUser(ctx, installed.Administrator.ID.String())
	requireNoError(t, err)
	if afterRejectedRotation.PasswordHash != beforeRejectedRotation.PasswordHash {
		t.Fatal("rejected rotate-only recovery changed the credential")
	}

	enabled, err := ss.Installation().RecoverAdministratorAccess(ctx, &store.AdministratorRecovery{
		InstitutionID: installed.Institution.ID, UserID: installed.Administrator.ID, EnableLocalLogin: true,
	})
	requireNoError(t, err)
	if enabled == nil || !enabled.LocalLoginEnabled || enabled.PasswordRotated {
		t.Fatalf("enable-local recovery = %#v", enabled)
	}
	if _, err := ss.Installation().RecoverAdministratorAccess(ctx, &store.AdministratorRecovery{
		InstitutionID: installed.Institution.ID, UserID: installed.Administrator.ID, EnableLocalLogin: true,
	}); !store.IsConflict(err) {
		t.Fatalf("pending enable-local repeat error=%v, want conflict", err)
	}
	_, err = ss.Installation().ReconcileAdministratorRecovery(ctx, &store.AdministratorRecoveryReconciliation{NodeID: "store-test-recovery"})
	requireNoError(t, err)
	if _, err := ss.Installation().RecoverAdministratorAccess(ctx, &store.AdministratorRecovery{
		InstitutionID: installed.Institution.ID, UserID: installed.Administrator.ID, EnableLocalLogin: true,
	}); !store.IsConflict(err) {
		t.Fatalf("already-enabled recovery error=%v, want conflict", err)
	}
	current, err := ss.AccessPolicy().Get(ctx, model.AccessPolicyTransitionHistoryLimit)
	requireNoError(t, err)
	if !current.Policy.LocalLoginEnabled || current.Policy.Revision != snapshot.Policy.Revision+2 ||
		len(current.History) != 1 || current.History[0].ToRevision != snapshot.Policy.Revision+1 {
		t.Fatalf("policy after exceptional offline revision = %#v", current)
	}
	if len(probes) != 0 && probes[0].HoldServingNodeLeaseFence != nil && probes[0].WaitForBlockedTransactions != nil {
		blockerPID, release := probes[0].HoldServingNodeLeaseFence(t, ctx)
		leaseID := model.NewId()
		leaseDone := make(chan error, 1)
		go func() {
			_, leaseErr := ss.ServingNodeLease().Upsert(ctx, &store.ServingNodeLeaseClaim{
				NodeID: "queued-serving-node", LeaseID: leaseID, Lifetime: 30 * time.Second,
			})
			leaseDone <- leaseErr
		}()
		probes[0].WaitForBlockedTransactions(t, ctx, blockerPID, 1)
		recoveryDone := make(chan error, 1)
		go func() {
			_, recoveryErr := ss.Installation().RecoverAdministratorAccess(ctx, &store.AdministratorRecovery{
				InstitutionID: installed.Institution.ID, UserID: installed.Administrator.ID,
				RotatePasswordHash: "must-not-win-serving-lease-race",
			})
			recoveryDone <- recoveryErr
		}()
		probes[0].WaitForBlockedTransactions(t, ctx, blockerPID, 2)
		release()
		requireNoError(t, <-leaseDone)
		if recoveryErr := <-recoveryDone; !store.IsConflict(recoveryErr) {
			t.Fatalf("recovery queued after serving lease error = %v, want conflict", recoveryErr)
		}
		requireNoError(t, ss.ServingNodeLease().Delete(ctx, "queued-serving-node", leaseID))
	}
	if len(probes) != 0 && probes[0].HoldAuthenticationPathFence != nil && probes[0].WaitForBlockedTransactions != nil {
		blockerPID, release := probes[0].HoldAuthenticationPathFence(t, ctx)
		done := make(chan error, 1)
		go func() {
			_, recoveryErr := ss.Installation().RecoverAdministratorAccess(ctx, &store.AdministratorRecovery{
				InstitutionID: installed.Institution.ID, UserID: installed.Administrator.ID,
				RotatePasswordHash: "serialized-recovery-password-hash",
			})
			done <- recoveryErr
		}()
		probes[0].WaitForBlockedTransactions(t, ctx, blockerPID, 1)
		release()
		requireNoError(t, <-done)
		_, err = ss.Installation().ReconcileAdministratorRecovery(ctx, &store.AdministratorRecoveryReconciliation{NodeID: "store-test-recovery"})
		requireNoError(t, err)
	}
}
