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
	"strings"
	"testing"
	"time"

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
	if _, err := ss.Installation().ResetAdministratorMFA(ctx, &store.AdministratorMFAReset{
		InstitutionID: model.NewInstitutionID(), UserID: model.NewUserID(), MFAEnabled: true,
	}); !store.IsNotFound(err) {
		t.Fatalf("pristine reset error=%v", err)
	}
	installed, err := ss.Installation().Bootstrap(ctx, testInstallationBootstrap(492))
	requireNoError(t, err)
	userID := installed.Administrator.ID
	input := &store.AdministratorMFAReset{InstitutionID: installed.Institution.ID, UserID: userID, MFAEnabled: true}
	session, _, raw := saveSession(t, ctx, ss, userID.String(), 10)
	pending := savePendingMFA(t, ctx, ss, userID)
	at := model.NowUTC()
	audit, notice := mfaSecurityNoticeFixture(t, ctx, ss, installed.Administrator, model.MailTemplateIdentityMFAEnabled, at.UnixMilli())
	_, err = ss.MFA().Activate(ctx, &store.MFAActivationMutation{
		Principal: mfaPrincipal(t, ctx, ss, raw.access), RecentAuthenticationTTL: time.Hour,
		CredentialID: pending.ID.String(), UserID: userID.String(), TimeStep: time.Now().Unix() / 30,
		RecoveryCodes: []*model.MFARecoveryCode{{CodeHash: model.HashToken(model.NewCredentialToken())}},
		SessionID:     session.ID.String(), At: at, AuditEventID: audit.ID.String(), AuditAt: at.UnixMilli(), Notice: notice,
	})
	requireNoError(t, err)
	passwordBefore, err := ss.PasswordCredential().GetByUser(ctx, userID.String())
	requireNoError(t, err)
	policyBefore, err := ss.AccessPolicy().Get(ctx, 0)
	requireNoError(t, err)
	token, err := createPersonalAccessTokenWithNotice(t, ctx, ss, newPersonalAccessToken(userID.String(), model.NewCredentialToken()), 10)
	requireNoError(t, err)
	assertUnchanged := func(t *testing.T) {
		t.Helper()
		current, getErr := ss.MFA().GetByUser(ctx, userID.String())
		requireNoError(t, getErr)
		if current.ID != pending.ID || !current.IsActive() {
			t.Fatalf("rejected reset changed MFA=%#v", current)
		}
		state, getErr := ss.MFA().GetRecoveryState(ctx, userID)
		requireNoError(t, getErr)
		if state.Generation != 0 || state.ReenrollmentRequired {
			t.Fatalf("rejected reset changed generation=%#v", state)
		}
		currentSession, getErr := ss.Session().Get(ctx, session.ID.String())
		requireNoError(t, getErr)
		if currentSession.RevokedAt.Valid {
			t.Fatal("rejected reset revoked Session")
		}
		currentToken, getErr := ss.PersonalAccessToken().Get(ctx, token.ID.String())
		requireNoError(t, getErr)
		if currentToken.RevokedAt.Valid {
			t.Fatal("rejected reset revoked PAT")
		}
	}
	for _, invalid := range []*store.AdministratorMFAReset{
		{InstitutionID: input.InstitutionID, UserID: userID},
		{InstitutionID: model.NewInstitutionID(), UserID: userID, MFAEnabled: true},
		{InstitutionID: input.InstitutionID, UserID: model.NewUserID(), MFAEnabled: true},
	} {
		if _, err := ss.Installation().ResetAdministratorMFA(ctx, invalid); err == nil {
			t.Fatal("invalid reset succeeded")
		}
		assertUnchanged(t)
	}
	leaseID := model.NewId()
	_, err = ss.ServingNodeLease().Upsert(ctx, &store.ServingNodeLeaseClaim{NodeID: "mfa-reset-serving", LeaseID: leaseID, Lifetime: 30 * time.Second})
	requireNoError(t, err)
	if _, err = ss.Installation().ResetAdministratorMFA(ctx, input); !store.IsConflict(err) {
		t.Fatalf("live-node reset error=%v", err)
	}
	assertUnchanged(t)
	requireNoError(t, ss.ServingNodeLease().Delete(ctx, "mfa-reset-serving", leaseID))
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
		requireNoError(t, <-leaseDone)
		if err := <-resetDone; !store.IsConflict(err) {
			t.Fatalf("reset racing startup error=%v", err)
		}
		assertUnchanged(t)
		requireNoError(t, ss.ServingNodeLease().Delete(ctx, "mfa-reset-starting", leaseID))
	}
	if len(probes) > 0 && probes[0].RejectRecoveryEvidence != nil {
		release := probes[0].RejectRecoveryEvidence(t, ctx)
		_, resetErr := ss.Installation().ResetAdministratorMFA(ctx, input)
		release()
		if resetErr == nil || !strings.Contains(resetErr.Error(), "test_reject_mfa_recovery") {
			t.Fatalf("reset did not reach injected durable evidence failure: %v", resetErr)
		}
		assertUnchanged(t)
	}

	result, err := ss.Installation().ResetAdministratorMFA(ctx, input)
	requireNoError(t, err)
	if result == nil || !model.IsValidId(result.RecordID) || result.Recovery == nil || !result.Recovery.ReenrollmentRequired || result.Recovery.Generation != 1 {
		t.Fatalf("reset result=%#v", result)
	}
	if _, err = ss.MFA().GetByUser(ctx, userID.String()); !store.IsNotFound(err) {
		t.Fatalf("reset retained MFA error=%v", err)
	}
	count, err := ss.MFA().CountRecoveryCodes(ctx, userID.String())
	requireNoError(t, err)
	if count != 0 {
		t.Fatalf("reset retained %d recovery codes", count)
	}
	currentSession, err := ss.Session().Get(ctx, session.ID.String())
	requireNoError(t, err)
	if !currentSession.RevokedAt.Valid || currentSession.RevocationReason != model.SessionRevocationMFAReset {
		t.Fatalf("reset Session=%#v", currentSession)
	}
	currentToken, err := ss.PersonalAccessToken().Get(ctx, token.ID.String())
	requireNoError(t, err)
	if !currentToken.RevokedAt.Valid {
		t.Fatal("reset retained PAT")
	}
	passwordAfter, err := ss.PasswordCredential().GetByUser(ctx, userID.String())
	requireNoError(t, err)
	policyAfter, err := ss.AccessPolicy().Get(ctx, 0)
	requireNoError(t, err)
	if passwordAfter.PasswordHash != passwordBefore.PasswordHash || passwordAfter.Revision != passwordBefore.Revision || policyAfter.Policy.Revision != policyBefore.Policy.Revision {
		t.Fatal("MFA reset changed primary identity or policy")
	}
	if _, err = ss.Installation().ResetAdministratorMFA(ctx, input); !store.IsConflict(err) {
		t.Fatalf("pending repeated reset error=%v", err)
	}
	if _, err = ss.Installation().RecoverAdministratorAccess(ctx, &store.AdministratorRecovery{InstitutionID: input.InstitutionID, UserID: userID, RotatePasswordHash: "must-not-commit"}); !store.IsConflict(err) {
		t.Fatalf("pending cross-command recovery error=%v", err)
	}
	assertAdministratorMFARecoveryAudit(t, ctx, ss, input, 0)
	reconciled, err := ss.Installation().ReconcileAdministratorRecovery(ctx, &store.AdministratorRecoveryReconciliation{NodeID: "mfa-recovery-startup"})
	requireNoError(t, err)
	if reconciled.Reconciled != 1 {
		t.Fatalf("reconciliation=%#v", reconciled)
	}
	assertAdministratorMFARecoveryAudit(t, ctx, ss, input, 1)
	reconciled, err = ss.Installation().ReconcileAdministratorRecovery(ctx, &store.AdministratorRecoveryReconciliation{NodeID: "mfa-recovery-startup-again"})
	requireNoError(t, err)
	if reconciled.Reconciled != 0 {
		t.Fatalf("replayed reconciliation=%#v", reconciled)
	}
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
				if out.result.Recovery.Generation != 2 {
					t.Fatalf("concurrent generation=%d", out.result.Recovery.Generation)
				}
			} else if store.IsConflict(out.err) {
				rejected++
			} else {
				t.Fatal(out.err)
			}
		}
		if committed != 1 || rejected != 1 {
			t.Fatalf("competing reset commits=%d rejections=%d", committed, rejected)
		}
	}
}

func assertAdministratorMFARecoveryAudit(t *testing.T, ctx context.Context, ss store.Store, input *store.AdministratorMFAReset, want int) {
	t.Helper()
	events, err := ss.Audit().List(ctx, store.AuditListOptions{Action: "authentication.administrator_mfa_reset", Limit: 10, Visibility: store.AuditVisibilityScope{InstitutionWide: true}})
	requireNoError(t, err)
	if len(events) != want {
		t.Fatalf("MFA recovery audits=%d want=%d", len(events), want)
	}
	for _, event := range events {
		if !event.ActorID.IsZero() || !event.SessionID.IsZero() || event.ClientType != "system" || event.Status != model.AuditStatusSuccess || event.Resource.ID != input.UserID.String() || event.ScopeID != input.InstitutionID.String() {
			t.Fatalf("host recovery audit=%#v", event)
		}
		var result struct {
			MFAReset     bool  `json:"mfa_reset"`
			Generation   int64 `json:"mfa_recovery_generation"`
			Reenrollment bool  `json:"reenrollment_required"`
		}
		requireNoError(t, json.Unmarshal(event.Result, &result))
		if !result.MFAReset || result.Generation != 1 || !result.Reenrollment {
			t.Fatalf("recovery projection=%#v", result)
		}
	}
}

func TestAdministratorMFAResetExternalStore(t *testing.T, ss store.Store) {
	t.Helper()
	ctx := context.Background()
	installed, err := ss.Installation().Bootstrap(ctx, testInstallationBootstrap(493))
	requireNoError(t, err)
	identity, err := ss.ExternalIdentity().Save(ctx, &model.ExternalIdentity{UserID: installed.Administrator.ID, Provider: "recovery-provider", Subject: "exact-existing-subject", LastSeenAt: model.OptionalTimeFrom(model.NowUTC())})
	requireNoError(t, err)
	snapshot, err := ss.AccessPolicy().Get(ctx, model.AccessPolicyTransitionHistoryLimit)
	requireNoError(t, err)
	settings := snapshot.Policy.Settings()
	settings.LocalLoginEnabled, settings.InvitationLocalCredentialEnabled = false, false
	settings.ProviderAdmissions["recovery-provider"] = model.ProviderAdmissionLinkedOnly
	capabilities := store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{"recovery-provider": {}}}
	replacement := &store.AccessPolicyReplacement{Preflight: store.AccessPolicyPreflight{ExpectedRevision: snapshot.Policy.Revision, Settings: settings, Capabilities: capabilities, CheckedAt: model.NowUTC()}, ActorID: installed.Administrator.ID}
	prepareAccessPolicyAttempt(t, ctx, ss, installed, replacement)
	_, err = ss.AccessPolicy().Replace(ctx, replacement, &store.CommandIdempotency{UserID: installed.Administrator.ID, Operation: "access_policy.replace.v1",
		KeyDigest: sha256.Sum256([]byte("offline-mfa-external")), FingerprintVersion: 1, Fingerprint: sha256.Sum256([]byte("offline-mfa-external-command")), OutcomeVersion: 1, Retention: time.Hour, Wait: time.Second})
	requireNoError(t, err)
	removeAudit := saveAuthenticationMethodAuditAttempt(t, ctx, ss, installed.Administrator.ID.String(), "remove_password")
	_, err = ss.PasswordCredential().RemoveWithAudit(ctx, &store.PasswordCredentialRemoval{UserID: installed.Administrator.ID,
		Capabilities: capabilities, ChangedAt: model.GetMillis(), RevocationReason: model.SessionRevocationPasswordRemoved,
		AuditEventID: removeAudit.ID.String(), AuditAt: model.GetMillis()})
	requireNoError(t, err)
	input := &store.AdministratorMFAReset{InstitutionID: installed.Institution.ID, UserID: installed.Administrator.ID, MFAEnabled: true}
	if _, err = ss.Installation().ResetAdministratorMFA(ctx, input); !store.IsConflict(err) {
		t.Fatalf("unavailable provider reset error=%v", err)
	}
	state, err := ss.MFA().GetRecoveryState(ctx, input.UserID)
	requireNoError(t, err)
	if state.Generation != 0 {
		t.Fatal("unavailable provider changed recovery restriction")
	}
	input.Capabilities = capabilities
	result, err := ss.Installation().ResetAdministratorMFA(ctx, input)
	requireNoError(t, err)
	if result.Recovery.Generation != 1 || !result.Recovery.ReenrollmentRequired {
		t.Fatalf("external reset=%#v", result)
	}
	currentPolicy, err := ss.AccessPolicy().Get(ctx, 0)
	requireNoError(t, err)
	currentIdentity, err := ss.ExternalIdentity().GetByProviderSubject(ctx, identity.Provider, identity.Subject)
	requireNoError(t, err)
	if currentPolicy.Policy.LocalLoginEnabled || currentPolicy.Policy.Revision != snapshot.Policy.Revision+1 || currentIdentity.ID != identity.ID || currentIdentity.UserID != identity.UserID {
		t.Fatal("external-only reset changed primary identity or policy")
	}
	if _, err = ss.PasswordCredential().GetByUser(ctx, input.UserID.String()); !store.IsNotFound(err) {
		t.Fatalf("external-only reset recreated a password credential: %v", err)
	}
}
