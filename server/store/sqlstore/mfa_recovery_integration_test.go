//go:build integration

// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type mfaRecoverySQLFixture struct {
	persistence      *SQLStore
	actor, target    *model.User
	principal        model.Principal
	targetSession    *model.Session
	targetCredential *model.SessionCredential
	access, refresh  string
}

func mfaRecoveryFixture(t *testing.T, ctx context.Context) mfaRecoverySQLFixture {
	t.Helper()
	s := openTestStore(t)
	resetTestStore(t, s)
	institution, err := s.Institution().Save(ctx, &model.Institution{Name: "mfa-recovery", DisplayName: "MFA Recovery Institution"})
	if err != nil {
		t.Fatal(err)
	}
	actor := saveIntegrationUser(t, ctx, s, &model.User{Username: "mfa-operator", Email: "operator@example.edu", DisplayName: "Operator"})
	target := saveIntegrationUser(t, ctx, s, &model.User{Username: "mfa-target", Email: "target@example.edu", DisplayName: "Target"})
	role, err := s.Role().Save(ctx, &model.Role{Name: model.SystemAdministratorRoleName, DisplayName: "System Administrator", Permissions: model.AllActions(), BuiltIn: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.RoleBinding().Save(ctx, &model.RoleBinding{UserID: actor.ID, RoleID: role.ID, ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(), StartsAt: model.NowUTC().Add(-time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	adminSession, adminCredential, _, _ := saveMFARecoverySQLSession(t, ctx, s, actor.ID, 0, false, true)
	targetSession, targetCredential, access, refresh := saveMFARecoverySQLSession(t, ctx, s, target.ID, 0, false, false)
	return mfaRecoverySQLFixture{s, actor, target, mfaSQLPrincipal(adminSession, adminCredential), targetSession, targetCredential, access, refresh}
}

func saveMFARecoverySQLSession(t *testing.T, ctx context.Context, s *SQLStore, userID model.UserID, generation int64, restricted, strong bool) (*model.Session, *model.SessionCredential, string, string) {
	t.Helper()
	at := model.NowUTC()
	access, refresh := model.NewCredentialToken(), model.NewCredentialToken()
	candidate := &model.Session{UserID: userID, AuthenticationGeneration: generation, MFARecoveryRequired: restricted, ClientType: model.SessionClientWeb, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor, AuthenticatedAt: at, IdleExpiresAt: at.Add(time.Hour), ExpiresAt: at.Add(2 * time.Hour)}
	if strong {
		candidate.AuthenticationStrength = model.AuthenticationMultiFactor
		candidate.MFACompletedAt = model.OptionalTimeFrom(at)
	}
	input := sessionCreationForSQLTest(t, ctx, s, candidate, []*model.SessionCredential{{Kind: model.SessionCredentialAccess, TokenHash: model.HashToken(access), ExpiresAt: at.Add(time.Hour)}, {Kind: model.SessionCredentialRefresh, TokenHash: model.HashToken(refresh), ExpiresAt: at.Add(2 * time.Hour)}}, 10)
	session, credentials, err := s.Session().Save(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	for _, credential := range credentials {
		if credential.Kind == model.SessionCredentialAccess {
			return session, credential, access, refresh
		}
	}
	t.Fatal("missing access credential")
	return nil, nil, "", ""
}

func (f mfaRecoverySQLFixture) resetInput(t *testing.T, ctx context.Context) *store.MFAAssistedReset {
	t.Helper()
	at := model.NowUTC().Truncate(time.Millisecond)
	audit, notice := mfaSQLSecurityNoticeFixture(t, ctx, f.persistence, f.target, model.MailTemplateIdentityMFAReset, at.UnixMilli())
	return &store.MFAAssistedReset{Principal: f.principal, UserID: f.target.ID, IdentityVerified: true, Reason: "Authenticator lost", VerificationReference: "case-123", RecentAuthenticationTTL: time.Hour, AuditEventID: audit.ID, Notice: notice, NoticeAt: at}
}

func TestMFAAssistedResetAtomicRetirementAndRestrictedReenrollment(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	f := mfaRecoveryFixture(t, ctx)
	s := f.persistence
	pending, err := s.MFA().SavePending(ctx, &model.MFACredential{UserID: f.target.ID, State: model.MFAStatePending, EncryptedSecret: "encrypted-secret", EncryptionKeyID: "0123456789abcdef0123456789abcdef", PendingExpiresAt: model.OptionalTimeFrom(model.NowUTC().Add(time.Minute))})
	if err != nil {
		t.Fatal(err)
	}
	oldCode := model.HashToken(model.NewCredentialToken())
	activationAt := model.NowUTC().Truncate(time.Millisecond)
	audit, notice := mfaSQLSecurityNoticeFixture(t, ctx, s, f.target, model.MailTemplateIdentityMFAEnabled, activationAt.UnixMilli())
	_, err = s.MFA().Activate(ctx, &store.MFAActivationMutation{Principal: mfaSQLPrincipal(f.targetSession, f.targetCredential), RecentAuthenticationTTL: time.Hour, CredentialID: pending.ID.String(), UserID: f.target.ID.String(), TimeStep: time.Now().Unix() / 30, RecoveryCodes: []*model.MFARecoveryCode{{CodeHash: oldCode}}, SessionID: f.targetSession.ID.String(), At: activationAt, AuditEventID: audit.ID.String(), AuditAt: activationAt.UnixMilli(), Notice: notice})
	if err != nil {
		t.Fatal(err)
	}
	patHash := model.HashToken(model.NewCredentialToken())
	at := model.NowUTC()
	if _, err = s.GetMaster().Exec(ctx, `INSERT INTO personal_access_tokens(id,created_at,updated_at,user_id,description,token_hash,scopes,expires_at) VALUES(?,?,?,?,'fixture',?,ARRAY['user.view_self'],?)`, model.NewPersonalAccessTokenID().String(), at, at, f.target.ID.String(), patHash, at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetMaster().Exec(ctx, `INSERT INTO user_tokens(id,created_at,updated_at,user_id,purpose,token_hash,target,expires_at) VALUES(?,?,?,?,'password_reset',?,?,?)`, model.NewUserTokenID().String(), at, at, f.target.ID.String(), model.HashToken(model.NewCredentialToken()), f.target.Email, at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	oldState := saveMFARecoveryExternalState(t, ctx, s, "campus", model.ExternalAuthenticationPurposeLogin, "")
	institution, err := s.Institution().GetSingleton(ctx)
	if err != nil {
		t.Fatal(err)
	}
	desktop, _, _, _ := issueDesktopAuthorizationForSQLTest(t, ctx, s, institution.ID, f.target.ID)
	patPreparation, err := s.PersonalAccessToken().PrepareMutation(ctx, &store.PersonalAccessTokenMutationPreparation{UserID: f.target.ID.String(), Kind: store.PersonalAccessTokenMutationCreate, Lifetime: time.Minute, Audit: personalAccessTokenPreparationAudit(f.target.ID, f.targetSession.ID, institution.ID)})
	if err != nil {
		t.Fatal(err)
	}
	linkAudit, err := s.Audit().Save(ctx, &model.AuditEvent{ActorID: f.target.ID, SessionID: f.targetSession.ID, Action: string(model.ActionExternalIdentityManage), Resource: model.Resource{Type: model.ResourceUser, ID: f.target.ID.String()}, ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(), Status: model.AuditStatusAttempt, NodeID: "recovery-fence"})
	if err != nil {
		t.Fatal(err)
	}
	linkState, err := s.ExternalLoginState().Save(ctx, &model.ExternalLoginState{Provider: "campus", Purpose: model.ExternalAuthenticationPurposeConnect, TargetUserID: f.target.ID, AuditEventID: linkAudit.ID.String(), StateHash: model.HashToken(model.NewCredentialToken()), BindingHash: model.HashToken(model.NewCredentialToken()), ReturnTo: "/account/connect-provider", ClientType: model.SessionClientWeb}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	input := f.resetInput(t, ctx)
	input.AuditEventID = model.NewAuditEventID()
	if _, err = s.MFA().ResetWithAudit(ctx, input); err == nil {
		t.Fatal("reset without durable audit committed")
	}
	state, err := s.MFA().GetRecoveryState(ctx, f.target.ID)
	if err != nil || state.Generation != 0 {
		t.Fatalf("failed reset changed generation: %#v %v", state, err)
	}
	if _, _, err = s.SessionCredential().GetSessionByTokenHash(ctx, model.HashToken(f.access), model.SessionCredentialAccess); err != nil {
		t.Fatal("failed reset revoked the Session")
	}
	input = f.resetInput(t, ctx)
	result, err := s.MFA().ResetWithAudit(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Recovery.Generation != 1 || !result.Recovery.ReenrollmentRequired || !slices.Contains(result.AccessTokenHashes, model.HashToken(f.access)) {
		t.Fatalf("reset result=%#v", result)
	}
	if _, _, err = s.SessionCredential().GetSessionByTokenHash(ctx, model.HashToken(f.access), model.SessionCredentialAccess); !store.IsNotFound(err) {
		t.Fatalf("retired access resolved: %v", err)
	}
	if _, err = s.PersonalAccessToken().Resolve(ctx, patHash, model.NowUTC(), time.Minute); !store.IsNotFound(err) {
		t.Fatalf("retired PAT resolved: %v", err)
	}
	if err = s.MFA().ConsumeSecondFactor(ctx, f.target.ID.String(), 0, oldCode, model.GetMillis()); !store.IsNotFound(err) {
		t.Fatalf("old recovery code survived: %v", err)
	}
	var pendingTokens int
	if err = s.GetMaster().Get(ctx, &pendingTokens, `SELECT COUNT(*) FROM user_tokens WHERE user_id=? AND archived_at IS NULL AND consumed_at IS NULL`, f.target.ID.String()); err != nil || pendingTokens != 0 {
		t.Fatalf("pending primary recovery grants=%d %v", pendingTokens, err)
	}
	var desktopRow browserAuthenticationRow
	if err = s.GetMaster().Get(ctx, &desktopRow, `SELECT `+browserAuthenticationColumns+` FROM browser_authentication_transactions WHERE id=?`, desktop.ID.String()); err != nil {
		t.Fatal(err)
	}
	retiredDesktop, err := desktopRow.model()
	if err != nil || retiredDesktop.State != model.BrowserAuthenticationStateCancelled {
		t.Fatalf("unfinished Desktop proof survived: %#v %v", retiredDesktop, err)
	}
	var preparations int
	if err = s.GetMaster().Get(ctx, &preparations, `SELECT count(*) FROM personal_access_token_mutation_preparations WHERE id=?`, patPreparation.ID); err != nil || preparations != 0 {
		t.Fatalf("unfinished PAT survived: %d %v", preparations, err)
	}
	patAudits, err := s.Audit().List(ctx, store.AuditListOptions{ActorId: f.target.ID.String(), Action: "personal_access_token.create", Limit: 10, Visibility: store.AuditVisibilityScope{InstitutionWide: true}})
	if err != nil || len(patAudits) != 1 || patAudits[0].Status != model.AuditStatusFail || patAudits[0].ErrorCode != "authentication.mfa.reenrollment_required" {
		t.Fatalf("unfinished PAT proof lacks its terminal audit: %#v %v", patAudits, err)
	}
	retiredAudit, err := s.Audit().Get(ctx, linkAudit.ID.String())
	if err != nil || retiredAudit.Status != model.AuditStatusFail {
		t.Fatalf("unfinished provider proof audit not terminal: %#v %v", retiredAudit, err)
	}
	var consumed bool
	if err = s.GetMaster().Get(ctx, &consumed, `SELECT consumed_at IS NOT NULL FROM external_login_states WHERE id=?`, linkState.ID.String()); err != nil || !consumed {
		t.Fatalf("unfinished provider link survived: %v %v", consumed, err)
	}
	freshSession, freshCredential, _, _ := saveMFARecoverySQLSession(t, ctx, s, f.target.ID, 1, true, false)
	otherSession, otherCredential, otherAccess, _ := saveMFARecoverySQLSession(t, ctx, s, f.target.ID, 1, true, false)
	principal := mfaSQLPrincipal(freshSession, freshCredential)
	if principal.Validate() == nil || principal.ValidateMFARecovery() != nil {
		t.Fatal("restricted Session has ordinary authority")
	}
	audit, _ = mfaSQLSecurityNoticeFixture(t, ctx, s, f.target, model.MailTemplateIdentityMFAEnabled, model.GetMillis())
	newPending, err := s.MFA().SavePendingWithAudit(ctx, &store.MFAPendingEnrollment{Principal: principal, Credential: &model.MFACredential{UserID: f.target.ID, State: model.MFAStatePending, EncryptedSecret: "new-secret", EncryptionKeyID: "0123456789abcdef0123456789abcdef"}, Lifetime: time.Minute, RecentAuthenticationTTL: time.Hour, AuditEventID: audit.ID})
	if err != nil {
		t.Fatal(err)
	}
	// Challenge never lifts the restriction, even with a former code and a valid Session.
	challengeAudit, _ := mfaSQLSecurityNoticeFixture(t, ctx, s, f.target, model.MailTemplateIdentityMFAEnabled, model.GetMillis())
	if _, err = s.MFA().ChallengeWithAudit(ctx, &store.MFAChallenge{Principal: principal, CredentialID: newPending.ID, RecoveryCodeHash: oldCode, VerifiedAt: model.NowUTC(), AuditEventID: challengeAudit.ID}); err == nil {
		t.Fatal("challenge bypassed required reenrollment")
	}
	activationAt = model.NowUTC().Truncate(time.Millisecond)
	audit, notice = mfaSQLSecurityNoticeFixture(t, ctx, s, f.target, model.MailTemplateIdentityMFAEnabled, activationAt.UnixMilli())
	activated, err := s.MFA().Activate(ctx, &store.MFAActivationMutation{Principal: principal, RecentAuthenticationTTL: time.Hour, CredentialID: newPending.ID.String(), UserID: f.target.ID.String(), TimeStep: time.Now().Unix() / 30, RecoveryCodes: []*model.MFARecoveryCode{{CodeHash: model.HashToken(model.NewCredentialToken())}}, SessionID: freshSession.ID.String(), At: activationAt, AuditEventID: audit.ID.String(), AuditAt: activationAt.UnixMilli(), Notice: notice})
	if err != nil {
		t.Fatal(err)
	}
	state, err = s.MFA().GetRecoveryState(ctx, f.target.ID)
	if err != nil || state.ReenrollmentRequired || state.Generation != 1 || !state.ResetAt.Time.Equal(result.Recovery.ResetAt.Time) {
		t.Fatalf("activation lost permanent fence: %#v %v", state, err)
	}
	if activated.Session.MFARecoveryRequired || !mfaSQLPrincipal(activated.Session, freshCredential).HasStrongAuthentication() {
		t.Fatal("activation did not restore exact current Session")
	}
	if _, _, err = s.SessionCredential().GetSessionByTokenHash(ctx, model.HashToken(otherAccess), model.SessionCredentialAccess); !store.IsNotFound(err) {
		t.Fatalf("other recovery Session gained normal access: %v", err)
	}
	if err = requireMFARecoveryPATSource(ctx, s.GetMaster(), f.target.ID.String(), f.targetSession.ID.String(), model.NowUTC()); err == nil {
		t.Fatal("old request can issue PAT after reenrollment")
	}
	if err = requireSessionAuthenticationGeneration(ctx, s.GetMaster(), otherSession); err == nil {
		t.Fatal("stale restricted Session accepted after activation")
	}
	// An old request may have been paused before reserving its audit. The
	// later audit timestamp cannot give its revoked source Session new rights.
	for _, source := range []struct {
		name      string
		sessionID model.SessionID
		permitted bool
	}{
		{name: "retired Session", sessionID: f.targetSession.ID},
		{name: "current Session", sessionID: activated.Session.ID, permitted: true},
	} {
		audit, err := s.Audit().Save(ctx, &model.AuditEvent{ActorID: f.target.ID, SessionID: source.sessionID, Action: string(model.ActionExternalIdentityManage), Resource: model.Resource{Type: model.ResourceUser, ID: f.target.ID.String()}, ScopeType: model.RoleScopeInstitution, ScopeID: model.NewInstitutionID().String(), Status: model.AuditStatusAttempt, NodeID: "recovery-fence"})
		if err != nil {
			t.Fatal(err)
		}
		err = requireMFARecoveryAuthenticationMethodSource(ctx, s.GetMaster(), f.target.ID, audit.ID.String())
		if (err == nil) != source.permitted {
			t.Fatalf("%s credential change authority: %v", source.name, err)
		}
		if !source.permitted {
			_, err = s.PasswordCredential().EnrollWithAudit(ctx, &store.PasswordCredentialEnrollment{Credential: &model.PasswordCredential{UserID: f.target.ID, PasswordHash: "new-primary"}, AuditEventID: audit.ID.String(), AuditAt: model.GetMillis()})
			if !store.IsNotFound(err) {
				t.Fatalf("old request reached password enrollment: %v", err)
			}
		}
	}
	_ = otherCredential
	if err = requireSessionIssuanceRecovery(ctx, s.GetMaster(), &model.Session{UserID: f.target.ID, AuthenticationGeneration: 1, AuthenticationMethod: "oidc", AuthenticationProviderID: "campus", ClientType: model.SessionClientWeb}, oldState.ID); !errors.Is(err, store.ErrAuthenticationGenerationChanged) {
		t.Fatalf("pre-reset unknown-User provider proof returned: %v", err)
	}
	storedAudit, err := s.Audit().Get(ctx, input.AuditEventID.String())
	if err != nil || storedAudit.Status != model.AuditStatusSuccess {
		t.Fatal("reset audit did not commit")
	}
}

func saveMFARecoveryExternalState(t *testing.T, ctx context.Context, s *SQLStore, provider string, purpose model.ExternalAuthenticationPurpose, target model.UserID) *model.ExternalLoginState {
	t.Helper()
	stateHash, bindingHash := model.HashToken(model.NewCredentialToken()), model.HashToken(model.NewCredentialToken())
	state, err := s.ExternalLoginState().Save(ctx, &model.ExternalLoginState{Provider: provider, Purpose: purpose, TargetUserID: target, StateHash: stateHash, BindingHash: bindingHash, ReturnTo: "/account/security", ClientType: model.SessionClientWeb}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	state, err = s.ExternalLoginState().Consume(ctx, provider, stateHash, bindingHash)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func TestMFAAssistedResetRechecksLiveAuthority(t *testing.T) {
	for _, name := range []string{"self", "missing verification", "revoked actor", "weak actor", "stale actor", "lost role"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			f := mfaRecoveryFixture(t, ctx)
			input := f.resetInput(t, ctx)
			var err error
			switch name {
			case "self":
				input.UserID = f.actor.ID
			case "missing verification":
				input.IdentityVerified = false
			case "revoked actor":
				_, err = f.persistence.GetMaster().Exec(ctx, `UPDATE sessions SET revoked_at=clock_timestamp(),updated_at=clock_timestamp(),revocation_reason='user_logout' WHERE id=?`, f.principal.SessionID.String())
			case "weak actor":
				_, err = f.persistence.GetMaster().Exec(ctx, `UPDATE sessions SET authentication_strength='single_factor',mfa_completed_at=NULL WHERE id=?`, f.principal.SessionID.String())
			case "stale actor":
				input.RecentAuthenticationTTL = time.Nanosecond
			case "lost role":
				_, err = f.persistence.GetMaster().Exec(ctx, `UPDATE role_bindings SET archived_at=clock_timestamp(),updated_at=clock_timestamp() WHERE user_id=?`, f.actor.ID.String())
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.persistence.MFA().ResetWithAudit(ctx, input); err == nil {
				t.Fatal("reset used stale or insufficient authority")
			}
			state, err := f.persistence.MFA().GetRecoveryState(ctx, f.target.ID)
			if err != nil || state.Generation != 0 {
				t.Fatal("denied reset changed recovery state")
			}
		})
	}
}
