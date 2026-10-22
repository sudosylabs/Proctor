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

	_, err := ss.Installation().RecoverAdministratorAccess(ctx, &store.AdministratorRecovery{
		InstitutionID: model.NewInstitutionID(), UserID: model.NewUserID(), RotatePasswordHash: "new-hash",
	})
	require.True(t, store.IsNotFound(err), "RecoverAdministratorAccess(pristine) error")

	installed, err := ss.Installation().Bootstrap(ctx, testInstallationBootstrap(451))
	require.NoError(t, err)
	liveLeaseID := model.NewID()
	liveLease, err := ss.ServingNodeLease().Upsert(ctx, &store.ServingNodeLeaseClaim{
		NodeID: "serving-node-a", LeaseID: liveLeaseID, Lifetime: 30 * time.Second,
	})
	require.NoError(t, err)
	require.NotNil(t, liveLease)
	require.Equal(t, "serving-node-a", liveLease.NodeID)
	require.True(t, liveLease.ExpiresAt.After(liveLease.UpdatedAt))
	beforeLiveNodeRejection, err := ss.PasswordCredential().GetByUser(ctx, installed.Administrator.ID)
	require.NoError(t, err)
	_, err = ss.Installation().RecoverAdministratorAccess(ctx, &store.AdministratorRecovery{
		InstitutionID: installed.Institution.ID, UserID: installed.Administrator.ID,
		RotatePasswordHash: "must-not-commit-while-a-node-is-serving",
	})
	require.True(t, store.IsConflict(err), "RecoverAdministratorAccess(live serving node) error")
	afterLiveNodeRejection, err := ss.PasswordCredential().GetByUser(ctx, installed.Administrator.ID)
	require.NoError(t, err)
	require.Equal(t, beforeLiveNodeRejection.PasswordHash, afterLiveNodeRejection.PasswordHash, "live-node rejection changed the administrator credential")
	noPendingAfterLiveNode, err := ss.Installation().ReconcileAdministratorRecovery(ctx, &store.AdministratorRecoveryReconciliation{NodeID: "store-test-recovery"})
	require.NoError(t, err)
	require.NotNil(t, noPendingAfterLiveNode)
	require.Equal(t, 0, noPendingAfterLiveNode.Reconciled)
	require.NoError(t, ss.ServingNodeLease().Delete(ctx, "serving-node-a", liveLeaseID))
	staleLeaseID := model.NewID()
	_, err = ss.ServingNodeLease().Upsert(ctx, &store.ServingNodeLeaseClaim{
		NodeID: "stale-serving-node", LeaseID: staleLeaseID, Lifetime: store.ServingNodeLeaseMinimumLifetime,
	})
	require.NoError(t, err)
	time.Sleep(5 * store.ServingNodeLeaseMinimumLifetime)
	_, err = ss.ServingNodeLease().Upsert(ctx, &store.ServingNodeLeaseClaim{
		NodeID: "stale-serving-node", LeaseID: staleLeaseID, Lifetime: 30 * time.Second,
	})
	require.True(t, store.IsConflict(err), "Upsert(expired process incarnation) error")
	restartedLeaseID := model.NewID()
	_, err = ss.ServingNodeLease().Upsert(ctx, &store.ServingNodeLeaseClaim{
		NodeID: "stale-serving-node", LeaseID: restartedLeaseID, Lifetime: 30 * time.Second,
	})
	require.NoError(t, err)
	require.NoError(t, ss.ServingNodeLease().Delete(ctx, "stale-serving-node", restartedLeaseID))
	preservedSession, _, raw := saveSession(t, ctx, ss, installed.Administrator.ID.String(), 10)
	pendingMFA := savePendingMFA(t, ctx, ss, installed.Administrator.ID)
	mfaAt := model.MillisFromTime(pendingMFA.CreatedAt) + 1
	mfaAudit, mfaNotice := mfaSecurityNoticeFixture(t, ctx, ss, installed.Administrator, model.MailTemplateIdentityMFAEnabled, mfaAt)
	_, err = ss.MFA().Activate(ctx, &store.MFAActivationMutation{
		Principal: mfaPrincipal(t, ctx, ss, raw.access), RecentAuthenticationTTL: time.Hour,
		CredentialID: pendingMFA.ID, UserID: installed.Administrator.ID, TimeStep: time.Now().Unix() / 30,
		RecoveryCodes: []*model.MFARecoveryCode{{CodeHash: model.HashToken(model.NewCredentialToken())}},
		SessionID:     preservedSession.ID, At: model.TimeFromMillis(mfaAt), AuditEventID: mfaAudit.ID, AuditAt: mfaAt,
		Notice: mfaNotice,
	})
	require.NoError(t, err)
	before, err := ss.PasswordCredential().GetByUser(ctx, installed.Administrator.ID)
	require.NoError(t, err)

	input := &store.AdministratorRecovery{
		InstitutionID:      installed.Institution.ID,
		UserID:             installed.Administrator.ID,
		RotatePasswordHash: "$argon2id$v=19$m=19456,t=2,p=1$YWJjZGVmZ2hpamtsbW5vcA$YWJjZGVmZ2hpamtsbW5vcA",
	}
	recovered, err := ss.Installation().RecoverAdministratorAccess(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, recovered)
	require.True(t, recovered.PasswordRotated)
	require.False(t, recovered.LocalLoginEnabled)
	after, err := ss.PasswordCredential().GetByUser(ctx, installed.Administrator.ID)
	require.NoError(t, err)
	require.Equal(t, input.RotatePasswordHash, after.PasswordHash, "password credential before=%#v after=%#v", before, after)
	require.NotEqual(t, before.PasswordHash, after.PasswordHash, "password credential before=%#v after=%#v", before, after)
	require.Equal(t, before.Revision+1, after.Revision, "password credential before=%#v after=%#v", before, after)
	require.True(t, after.PasswordChangedAt.After(before.PasswordChangedAt), "password credential before=%#v after=%#v", before, after)
	sessions, listErr := ss.Session().ListByUser(ctx, everySession(installed.Administrator.ID))
	require.NoError(t, listErr, "recovery sessions")
	require.Len(t, sessions, 1, "recovery sessions")
	require.Equal(t, preservedSession.ID, sessions[0].ID, "recovery sessions")
	require.False(t, sessions[0].RevokedAt.Valid, "recovery sessions")
	activeMFA, err := ss.MFA().GetByUser(ctx, installed.Administrator.ID)
	require.NoError(t, err)
	require.True(t, activeMFA.IsActive())
	events, listErr := ss.Audit().List(ctx, store.AuditListOptions{
		Action: "authentication.administrator_recovery", Limit: 10,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	require.NoError(t, listErr, "pre-startup recovery audits")
	require.Empty(t, events, "pre-startup recovery audits")

	_, err = ss.Installation().RecoverAdministratorAccess(ctx, input)
	require.True(t, store.IsConflict(err), "RecoverAdministratorAccess(repeated pending) error")

	reconciled, err := ss.Installation().ReconcileAdministratorRecovery(ctx, &store.AdministratorRecoveryReconciliation{
		NodeID: "store-test-recovery",
	})
	require.NoError(t, err)
	require.NotNil(t, reconciled)
	require.Equal(t, 1, reconciled.Reconciled)
	events, err = ss.Audit().List(ctx, store.AuditListOptions{
		Action: "authentication.administrator_recovery", Limit: 10,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, model.AuditStatusSuccess, events[0].Status)
	require.True(t, events[0].ActorID.IsZero())
	require.Equal(t, model.Resource{Type: model.ResourceUser, ID: installed.Administrator.ID.String()}, events[0].Resource)
	var result struct {
		LocalLoginEnabled bool     `json:"local_login_enabled"`
		PasswordRotated   bool     `json:"password_rotated"`
		ChangedFields     []string `json:"changed_fields"`
	}
	require.NoError(t, json.Unmarshal(events[0].Result, &result))
	require.False(t, result.LocalLoginEnabled)
	require.True(t, result.PasswordRotated)
	require.Len(t, result.ChangedFields, 1)
	require.Equal(t, "password_credential", result.ChangedFields[0])
	noOp, err := ss.Installation().ReconcileAdministratorRecovery(ctx, &store.AdministratorRecoveryReconciliation{NodeID: "store-test-recovery"})
	require.NoError(t, err)
	require.NotNil(t, noOp)
	require.Equal(t, 0, noOp.Reconciled)

	_, err = ss.Installation().RecoverAdministratorAccess(ctx, &store.AdministratorRecovery{
		InstitutionID: model.NewInstitutionID(), UserID: installed.Administrator.ID, EnableLocalLogin: true,
	})
	require.True(t, store.IsConflict(err), "wrong-installation recovery error")
	_, err = ss.Installation().RecoverAdministratorAccess(ctx, &store.AdministratorRecovery{
		InstitutionID: installed.Institution.ID, UserID: model.NewUserID(), EnableLocalLogin: true,
	})
	require.True(t, store.IsConflict(err), "wrong-target recovery error")

	_, err = ss.ExternalIdentity().Save(ctx, &model.ExternalIdentity{
		UserID: installed.Administrator.ID, Provider: "recovery-provider", Subject: "recovery-admin",
		LastSeenAt: model.OptionalTimeFrom(model.NowUTC()),
	})
	require.NoError(t, err)
	snapshot, err := ss.AccessPolicy().Get(ctx, model.AccessPolicyTransitionHistoryLimit)
	require.NoError(t, err)
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
		KeyDigest:   sha256.Sum256([]byte("administrator-recovery-disable-local")),
		Fingerprint: sha256.Sum256([]byte("administrator-recovery-disable-local-command")),
		Retention:   time.Hour, Wait: time.Second,
	})
	require.NoError(t, err)
	beforeRejectedRotation, err := ss.PasswordCredential().GetByUser(ctx, installed.Administrator.ID)
	require.NoError(t, err)
	_, err = ss.Installation().RecoverAdministratorAccess(ctx, &store.AdministratorRecovery{
		InstitutionID: installed.Institution.ID, UserID: installed.Administrator.ID,
		RotatePasswordHash: "must-not-commit-while-local-login-is-disabled",
	})
	require.True(t, store.IsConflict(err), "rotate-only with disabled local login error")
	afterRejectedRotation, err := ss.PasswordCredential().GetByUser(ctx, installed.Administrator.ID)
	require.NoError(t, err)
	require.Equal(t, beforeRejectedRotation.PasswordHash, afterRejectedRotation.PasswordHash, "rejected rotate-only recovery changed the credential")

	enabled, err := ss.Installation().RecoverAdministratorAccess(ctx, &store.AdministratorRecovery{
		InstitutionID: installed.Institution.ID, UserID: installed.Administrator.ID, EnableLocalLogin: true,
	})
	require.NoError(t, err)
	require.NotNil(t, enabled)
	require.True(t, enabled.LocalLoginEnabled)
	require.False(t, enabled.PasswordRotated)
	_, err = ss.Installation().RecoverAdministratorAccess(ctx, &store.AdministratorRecovery{
		InstitutionID: installed.Institution.ID, UserID: installed.Administrator.ID, EnableLocalLogin: true,
	})
	require.True(t, store.IsConflict(err), "pending enable-local repeat error")
	_, err = ss.Installation().ReconcileAdministratorRecovery(ctx, &store.AdministratorRecoveryReconciliation{NodeID: "store-test-recovery"})
	require.NoError(t, err)
	_, err = ss.Installation().RecoverAdministratorAccess(ctx, &store.AdministratorRecovery{
		InstitutionID: installed.Institution.ID, UserID: installed.Administrator.ID, EnableLocalLogin: true,
	})
	require.True(t, store.IsConflict(err), "already-enabled recovery error")
	current, err := ss.AccessPolicy().Get(ctx, model.AccessPolicyTransitionHistoryLimit)
	require.NoError(t, err)
	require.True(t, current.Policy.LocalLoginEnabled)
	require.Equal(t, snapshot.Policy.Revision+2, current.Policy.Revision)
	require.Len(t, current.History, 1)
	require.Equal(t, snapshot.Policy.Revision+1, current.History[0].ToRevision)
	if len(probes) != 0 && probes[0].HoldServingNodeLeaseFence != nil && probes[0].WaitForBlockedTransactions != nil {
		blockerPID, release := probes[0].HoldServingNodeLeaseFence(t, ctx)
		leaseID := model.NewID()
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
		require.NoError(t, <-leaseDone)
		require.True(t, store.IsConflict((<-recoveryDone)), "recovery queued after serving lease error")
		require.NoError(t, ss.ServingNodeLease().Delete(ctx, "queued-serving-node", leaseID))
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
		require.NoError(t, <-done)
		_, err = ss.Installation().ReconcileAdministratorRecovery(ctx, &store.AdministratorRecoveryReconciliation{NodeID: "store-test-recovery"})
		require.NoError(t, err)
	}
}
