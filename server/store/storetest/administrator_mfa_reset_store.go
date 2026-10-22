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
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type AdministratorMFAResetSQLProbe struct {
	RejectRecoveryEvidence func(*testing.T, context.Context) func()
	ConcurrentPeer         store.InstallationStore
	ServingFence           AdministratorRecoverySQLProbe
}

func TestAdministratorMFAResetStore(t *testing.T, ss store.Store, probes ...AdministratorMFAResetSQLProbe) {
	t.Helper()
	ctx := context.Background()
	_, err := ss.Installation().ResetAdministratorMFA(ctx, &store.AdministratorMFAReset{
		InstitutionID: model.NewInstitutionID(), UserID: model.NewUserID(), MFAEnabled: true,
	})
	require.True(t, store.IsNotFound(err), "pristine reset error")
	installed, err := ss.Installation().Bootstrap(ctx, testInstallationBootstrap(492))
	require.NoError(t, err)
	userID := installed.Administrator.ID
	input := &store.AdministratorMFAReset{InstitutionID: installed.Institution.ID, UserID: userID, MFAEnabled: true}
	session, _, raw := saveSession(t, ctx, ss, userID.String(), 10)
	pending := savePendingMFA(t, ctx, ss, userID)
	at := model.NowUTC()
	audit, notice := mfaSecurityNoticeFixture(t, ctx, ss, installed.Administrator, model.MailTemplateIdentityMFAEnabled, at)
	_, err = ss.MFA().Activate(ctx, &store.MFAActivationMutation{
		Principal: mfaPrincipal(t, ctx, ss, raw.access), RecentAuthenticationTTL: time.Hour,
		CredentialID: pending.ID, UserID: userID, TimeStep: time.Now().Unix() / 30,
		RecoveryCodes: []*model.MFARecoveryCode{{CodeHash: model.HashToken(model.NewCredentialToken())}},
		SessionID:     session.ID, At: at, AuditEventID: audit.ID, AuditAt: at, Notice: notice,
	})
	require.NoError(t, err)
	passwordBefore, err := ss.PasswordCredential().GetByUser(ctx, userID)
	require.NoError(t, err)
	policyBefore, err := ss.AccessPolicy().Get(ctx, 0)
	require.NoError(t, err)
	token, err := createPersonalAccessTokenWithNotice(t, ctx, ss, newPersonalAccessToken(userID.String(), model.NewCredentialToken()), 10)
	require.NoError(t, err)
	assertUnchanged := func(t *testing.T) {
		t.Helper()
		current, getErr := ss.MFA().GetByUser(ctx, userID)
		require.NoError(t, getErr)
		require.Equal(t, pending.ID, current.ID)
		require.True(t, current.IsActive())
		state, getErr := ss.MFA().GetRecoveryState(ctx, userID)
		require.NoError(t, getErr)
		require.Equal(t, int64(0), state.Generation)
		require.False(t, state.ReenrollmentRequired)
		currentSession, getErr := ss.Session().Get(ctx, session.ID)
		require.NoError(t, getErr)
		require.False(t, currentSession.RevokedAt.Valid, "rejected reset revoked Session")
		currentToken, getErr := ss.PersonalAccessToken().Get(ctx, token.ID)
		require.NoError(t, getErr)
		require.False(t, currentToken.RevokedAt.Valid, "rejected reset revoked PAT")
	}
	for _, invalid := range []*store.AdministratorMFAReset{
		{InstitutionID: input.InstitutionID, UserID: userID},
		{InstitutionID: model.NewInstitutionID(), UserID: userID, MFAEnabled: true},
		{InstitutionID: input.InstitutionID, UserID: model.NewUserID(), MFAEnabled: true},
	} {
		_, err = ss.Installation().ResetAdministratorMFA(ctx, invalid)
		require.Error(t, err, "invalid reset succeeded")
		assertUnchanged(t)
	}
	leaseID := model.NewID()
	_, err = ss.ServingNodeLease().Upsert(ctx, &store.ServingNodeLeaseClaim{NodeID: "mfa-reset-serving", LeaseID: leaseID, Lifetime: 30 * time.Second})
	require.NoError(t, err)
	_, err = ss.Installation().ResetAdministratorMFA(ctx, input)
	require.True(t, store.IsConflict(err), "live-node reset error")
	assertUnchanged(t)
	require.NoError(t, ss.ServingNodeLease().Delete(ctx, "mfa-reset-serving", leaseID))
	if len(probes) > 0 && probes[0].ServingFence.HoldServingNodeLeaseFence != nil {
		fence := probes[0].ServingFence
		fenceCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		pid, release := fence.HoldServingNodeLeaseFence(t, fenceCtx)
		defer release()
		leaseDone, resetDone := make(chan error, 1), make(chan error, 1)
		go func() {
			_, err := ss.ServingNodeLease().Upsert(fenceCtx, &store.ServingNodeLeaseClaim{NodeID: "mfa-reset-starting", LeaseID: leaseID, Lifetime: 30 * time.Second})
			leaseDone <- err
		}()
		fence.WaitForBlockedTransactions(t, fenceCtx, pid, 1)
		go func() { _, err := ss.Installation().ResetAdministratorMFA(fenceCtx, input); resetDone <- err }()
		fence.WaitForBlockedTransactions(t, fenceCtx, pid, 2)
		release()
		require.NoError(t, <-leaseDone)
		require.True(t, store.IsConflict((<-resetDone)), "reset racing startup error")
		assertUnchanged(t)
		require.NoError(t, ss.ServingNodeLease().Delete(ctx, "mfa-reset-starting", leaseID))
	}
	if len(probes) > 0 && probes[0].RejectRecoveryEvidence != nil {
		release := probes[0].RejectRecoveryEvidence(t, ctx)
		_, resetErr := ss.Installation().ResetAdministratorMFA(ctx, input)
		release()
		require.Error(t, resetErr)
		require.Contains(t, resetErr.Error(), "test_reject_mfa_recovery")
		assertUnchanged(t)
	}

	result, err := ss.Installation().ResetAdministratorMFA(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.RecordID.IsValid())
	require.NotNil(t, result.Recovery)
	require.True(t, result.Recovery.ReenrollmentRequired)
	require.Equal(t, int64(1), result.Recovery.Generation)
	_, err = ss.MFA().GetByUser(ctx, userID)
	require.True(t, store.IsNotFound(err), "reset retained MFA error")
	count, err := ss.MFA().CountRecoveryCodes(ctx, userID)
	require.NoError(t, err)
	require.Equal(t, 0, count, "reset retained")
	currentSession, err := ss.Session().Get(ctx, session.ID)
	require.NoError(t, err)
	require.True(t, currentSession.RevokedAt.Valid)
	require.Equal(t, model.SessionRevocationMFAReset, currentSession.RevocationReason)
	currentToken, err := ss.PersonalAccessToken().Get(ctx, token.ID)
	require.NoError(t, err)
	require.True(t, currentToken.RevokedAt.Valid, "reset retained PAT")
	passwordAfter, err := ss.PasswordCredential().GetByUser(ctx, userID)
	require.NoError(t, err)
	policyAfter, err := ss.AccessPolicy().Get(ctx, 0)
	require.NoError(t, err)
	require.Equal(t, passwordBefore.PasswordHash, passwordAfter.PasswordHash, "MFA reset changed primary identity or policy")
	require.Equal(t, passwordBefore.Revision, passwordAfter.Revision, "MFA reset changed primary identity or policy")
	require.Equal(t, policyBefore.Policy.Revision, policyAfter.Policy.Revision, "MFA reset changed primary identity or policy")
	_, err = ss.Installation().ResetAdministratorMFA(ctx, input)
	require.True(t, store.IsConflict(err), "pending repeated reset error")
	_, err = ss.Installation().RecoverAdministratorAccess(ctx, &store.AdministratorRecovery{InstitutionID: input.InstitutionID, UserID: userID, RotatePasswordHash: "must-not-commit"})
	require.True(t, store.IsConflict(err), "pending cross-command recovery error")
	assertAdministratorMFARecoveryAudit(t, ctx, ss, input, 0)
	reconciled, err := ss.Installation().ReconcileAdministratorRecovery(ctx, &store.AdministratorRecoveryReconciliation{NodeID: "mfa-recovery-startup"})
	require.NoError(t, err)
	require.Equal(t, 1, reconciled.Reconciled)
	assertAdministratorMFARecoveryAudit(t, ctx, ss, input, 1)
	reconciled, err = ss.Installation().ReconcileAdministratorRecovery(ctx, &store.AdministratorRecoveryReconciliation{NodeID: "mfa-recovery-startup-again"})
	require.NoError(t, err)
	require.Equal(t, 0, reconciled.Reconciled)
	assertAdministratorMFARecoveryAudit(t, ctx, ss, input, 1)

	if len(probes) > 0 && probes[0].ConcurrentPeer != nil {
		type outcome struct {
			result *store.AdministratorMFAResetResult
			err    error
		}
		results := make(chan outcome, 2)
		for _, persistence := range []store.InstallationStore{ss.Installation(), probes[0].ConcurrentPeer} {
			go func(p store.InstallationStore) {
				result, err := p.ResetAdministratorMFA(ctx, input)
				results <- outcome{result, err}
			}(persistence)
		}
		committed, rejected := 0, 0
		for range 2 {
			out := <-results
			if out.err == nil {
				committed++
				require.Equal(t, int64(2), out.result.Recovery.Generation, "concurrent generation")
			} else if store.IsConflict(out.err) {
				rejected++
			} else {
				require.FailNow(t, fmt.Sprint(out.err))
			}
		}
		require.Equal(t, 1, committed, "competing reset commits")
		require.Equal(t, 1, rejected, "competing reset commits")
	}
}

func assertAdministratorMFARecoveryAudit(t *testing.T, ctx context.Context, ss store.Store, input *store.AdministratorMFAReset, want int) {
	t.Helper()
	events, err := ss.Audit().List(ctx, store.AuditListOptions{Action: "authentication.administrator_mfa_reset", Limit: 10, Visibility: store.AuditVisibilityScope{InstitutionWide: true}})
	require.NoError(t, err)
	require.Len(t, events, want, "MFA recovery audits")
	for _, event := range events {
		require.True(t, event.ActorID.IsZero())
		require.True(t, event.SessionID.IsZero())
		require.Equal(t, "system", event.ClientType)
		require.Equal(t, model.AuditStatusSuccess, event.Status)
		require.Equal(t, input.UserID.String(), event.Resource.ID)
		require.Equal(t, input.InstitutionID.String(), event.ScopeID)
		var result struct {
			MFAReset     bool  `json:"mfa_reset"`
			Generation   int64 `json:"mfa_recovery_generation"`
			Reenrollment bool  `json:"reenrollment_required"`
		}
		require.NoError(t, json.Unmarshal(event.Result, &result))
		require.True(t, result.MFAReset)
		require.Equal(t, int64(1), result.Generation)
		require.True(t, result.Reenrollment)
	}
}

func TestAdministratorMFAResetExternalStore(t *testing.T, ss store.Store) {
	t.Helper()
	ctx := context.Background()
	installed, err := ss.Installation().Bootstrap(ctx, testInstallationBootstrap(493))
	require.NoError(t, err)
	identity, err := ss.ExternalIdentity().Save(ctx, &model.ExternalIdentity{UserID: installed.Administrator.ID, Provider: "recovery-provider", Subject: "exact-existing-subject", LastSeenAt: model.OptionalTimeFrom(model.NowUTC())})
	require.NoError(t, err)
	snapshot, err := ss.AccessPolicy().Get(ctx, model.AccessPolicyTransitionHistoryLimit)
	require.NoError(t, err)
	settings := snapshot.Policy.Settings()
	settings.LocalLoginEnabled, settings.InvitationLocalCredentialEnabled = false, false
	settings.ProviderAdmissions["recovery-provider"] = model.ProviderAdmissionLinkedOnly
	capabilities := store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{"recovery-provider": {}}}
	replacement := &store.AccessPolicyReplacement{Preflight: store.AccessPolicyPreflight{ExpectedRevision: snapshot.Policy.Revision, Settings: settings, Capabilities: capabilities, CheckedAt: model.NowUTC()}, ActorID: installed.Administrator.ID}
	prepareAccessPolicyAttempt(t, ctx, ss, installed, replacement)
	_, err = ss.AccessPolicy().Replace(ctx, replacement, &store.CommandIdempotency{UserID: installed.Administrator.ID, Operation: "access_policy.replace",
		KeyDigest: sha256.Sum256([]byte("offline-mfa-external")), Fingerprint: sha256.Sum256([]byte("offline-mfa-external-command")), Retention: time.Hour, Wait: time.Second})
	require.NoError(t, err)
	removeAudit := saveAuthenticationMethodAuditAttempt(t, ctx, ss, installed.Administrator.ID.String(), "remove_password")
	_, err = ss.PasswordCredential().Remove(ctx, &store.PasswordCredentialRemoval{UserID: installed.Administrator.ID,
		Capabilities: capabilities, ChangedAt: model.NowUTC(), RevocationReason: model.SessionRevocationPasswordRemoved,
		AuditEventID: removeAudit.ID, AuditAt: model.NowUTC()})
	require.NoError(t, err)
	input := &store.AdministratorMFAReset{InstitutionID: installed.Institution.ID, UserID: installed.Administrator.ID, MFAEnabled: true}
	_, err = ss.Installation().ResetAdministratorMFA(ctx, input)
	require.True(t, store.IsConflict(err), "unavailable provider reset error")
	state, err := ss.MFA().GetRecoveryState(ctx, input.UserID)
	require.NoError(t, err)
	require.Equal(t, int64(0), state.Generation, "unavailable provider changed recovery restriction")
	input.Capabilities = capabilities
	result, err := ss.Installation().ResetAdministratorMFA(ctx, input)
	require.NoError(t, err)
	require.Equal(t, int64(1), result.Recovery.Generation)
	require.True(t, result.Recovery.ReenrollmentRequired)
	currentPolicy, err := ss.AccessPolicy().Get(ctx, 0)
	require.NoError(t, err)
	currentIdentity, err := ss.ExternalIdentity().GetByProviderSubject(ctx, identity.Provider, identity.Subject)
	require.NoError(t, err)
	require.False(t, currentPolicy.Policy.LocalLoginEnabled, "external-only reset changed primary identity or policy")
	require.Equal(t, snapshot.Policy.Revision+1, currentPolicy.Policy.Revision, "external-only reset changed primary identity or policy")
	require.Equal(t, identity.ID, currentIdentity.ID, "external-only reset changed primary identity or policy")
	require.Equal(t, identity.UserID, currentIdentity.UserID, "external-only reset changed primary identity or policy")
	_, err = ss.PasswordCredential().GetByUser(ctx, input.UserID)
	require.True(t, store.IsNotFound(err), "external-only reset recreated a password credential")
}
