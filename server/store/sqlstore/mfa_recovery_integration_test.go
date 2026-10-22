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
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/storetest"
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
	require.NoError(t, err)
	actor := saveIntegrationUser(t, ctx, s, &model.User{Username: "mfa-operator", Email: "operator@example.edu", DisplayName: "Operator"})
	target := saveIntegrationUser(t, ctx, s, &model.User{Username: "mfa-target", Email: "target@example.edu", DisplayName: "Target"})
	role, err := storetest.SaveRole(ctx, s, &model.Role{Name: model.SystemAdministratorRoleName, DisplayName: "System Administrator", Permissions: model.AllActions(), BuiltIn: true})
	require.NoError(t, err)
	_, err = storetest.SaveRoleBinding(ctx, s, &model.RoleBinding{UserID: actor.ID, RoleID: role.ID, ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(), StartsAt: model.NowUTC().Add(-time.Hour)})
	require.NoError(t, err)
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
	session, credentials, err := storetest.SaveSession(ctx, s, input)
	require.NoError(t, err)
	for _, credential := range credentials {
		if credential.Kind == model.SessionCredentialAccess {
			return session, credential, access, refresh
		}
	}
	require.FailNow(t, "missing access credential")
	return nil, nil, "", ""
}

func (f mfaRecoverySQLFixture) resetInput(t *testing.T, ctx context.Context) *store.MFAAssistedReset {
	t.Helper()
	at := model.NowUTC().Truncate(time.Millisecond)
	audit, notice := mfaSQLSecurityNoticeFixture(t, ctx, f.persistence, f.target, model.MailTemplateIdentityMFAReset, at)
	return &store.MFAAssistedReset{Principal: f.principal, UserID: f.target.ID, IdentityVerified: true, Reason: "Authenticator lost", VerificationReference: "case-123", RecentAuthenticationTTL: time.Hour, AuditEventID: audit.ID, Notice: notice, NoticeAt: at}
}

func TestMFAAssistedResetAtomicRetirementAndRestrictedReenrollment(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	f := mfaRecoveryFixture(t, ctx)
	s := f.persistence
	pending, err := s.MFA().SeedPending(ctx, &model.MFACredential{UserID: f.target.ID, State: model.MFAStatePending, EncryptedSecret: "encrypted-secret", EncryptionKeyID: "0123456789abcdef0123456789abcdef", PendingExpiresAt: model.OptionalTimeFrom(model.NowUTC().Add(time.Minute))})
	require.NoError(t, err)
	oldCode := model.HashToken(model.NewCredentialToken())
	activationAt := model.NowUTC().Truncate(time.Millisecond)
	audit, notice := mfaSQLSecurityNoticeFixture(t, ctx, s, f.target, model.MailTemplateIdentityMFAEnabled, activationAt)
	_, err = s.MFA().Activate(ctx, &store.MFAActivationMutation{Principal: mfaSQLPrincipal(f.targetSession, f.targetCredential), RecentAuthenticationTTL: time.Hour, CredentialID: pending.ID, UserID: f.target.ID, TimeStep: time.Now().Unix() / 30, RecoveryCodes: []*model.MFARecoveryCode{{CodeHash: oldCode}}, SessionID: f.targetSession.ID, At: activationAt, AuditEventID: audit.ID, AuditAt: activationAt, Notice: notice})
	require.NoError(t, err)
	patHash := model.HashToken(model.NewCredentialToken())
	at := model.NowUTC()
	_, err = s.GetMaster().Exec(ctx, `INSERT INTO personal_access_tokens(id,created_at,updated_at,user_id,description,token_hash,scopes,expires_at) VALUES(?,?,?,?,'fixture',?,ARRAY['user.view_self'],?)`, model.NewPersonalAccessTokenID().String(), at, at, f.target.ID.String(), patHash, at.Add(time.Hour))
	require.NoError(t, err)
	_, err = s.GetMaster().Exec(ctx, `INSERT INTO user_tokens(id,created_at,updated_at,user_id,purpose,token_hash,target,expires_at) VALUES(?,?,?,?,'password_reset',?,?,?)`, model.NewUserTokenID().String(), at, at, f.target.ID.String(), model.HashToken(model.NewCredentialToken()), f.target.Email, at.Add(time.Hour))
	require.NoError(t, err)
	oldState := saveMFARecoveryExternalState(t, ctx, s, "campus", model.ExternalAuthenticationPurposeLogin, "")
	institution, err := s.Institution().GetSingleton(ctx)
	require.NoError(t, err)
	desktop, _, _, _ := issueDesktopAuthorizationForSQLTest(t, ctx, s, institution.ID, f.target.ID)
	patPreparation, err := s.PersonalAccessToken().PrepareMutation(ctx, &store.PersonalAccessTokenMutationPreparation{UserID: f.target.ID, Kind: store.PersonalAccessTokenMutationCreate, Lifetime: time.Minute, AuditEvent: personalAccessTokenPreparationAudit(f.target.ID, f.targetSession.ID, institution.ID)})
	require.NoError(t, err)
	linkAudit, err := s.Audit().Save(ctx, &model.AuditEvent{ActorID: f.target.ID, SessionID: f.targetSession.ID, Action: string(model.ActionExternalIdentityManage), Resource: model.Resource{Type: model.ResourceUser, ID: f.target.ID.String()}, ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(), Status: model.AuditStatusAttempt, NodeID: "recovery-fence"})
	require.NoError(t, err)
	linkState, err := s.ExternalLoginState().Save(ctx, &model.ExternalLoginState{Provider: "campus", Purpose: model.ExternalAuthenticationPurposeConnect, TargetUserID: f.target.ID, AuditEventID: linkAudit.ID.String(), StateHash: model.HashToken(model.NewCredentialToken()), BindingHash: model.HashToken(model.NewCredentialToken()), ReturnTo: "/account/connect-provider", ClientType: model.SessionClientWeb}, time.Minute)
	require.NoError(t, err)
	input := f.resetInput(t, ctx)
	input.AuditEventID = model.NewAuditEventID()
	_, err = s.MFA().Reset(ctx, input)
	require.Error(t, err, "reset without durable audit committed")
	state, err := s.MFA().GetRecoveryState(ctx, f.target.ID)
	require.NoError(t, err, "failed reset changed generation: %#v", state)
	require.Equal(t, int64(0), state.Generation, "failed reset changed generation: %#v", state)
	_, _, err = s.SessionCredential().GetSessionByTokenHash(ctx, model.HashToken(f.access), model.SessionCredentialAccess)
	require.NoError(t, err, "failed reset revoked the Session")
	input = f.resetInput(t, ctx)
	result, err := s.MFA().Reset(ctx, input)
	require.NoError(t, err)
	require.Equal(t, int64(1), result.Recovery.Generation)
	require.True(t, result.Recovery.ReenrollmentRequired)
	require.Contains(t, result.AccessTokenHashes, model.HashToken(f.access))
	_, _, err = s.SessionCredential().GetSessionByTokenHash(ctx, model.HashToken(f.access), model.SessionCredentialAccess)
	require.True(t, store.IsNotFound(err), "retired access resolved")
	_, err = s.PersonalAccessToken().Resolve(ctx, patHash, model.NowUTC(), time.Minute)
	require.True(t, store.IsNotFound(err), "retired PAT resolved")
	err = s.MFA().ConsumeSecondFactor(ctx, f.target.ID, 0, oldCode, model.NowUTC())
	require.True(t, store.IsNotFound(err), "old recovery code survived")
	var pendingTokens int
	err = s.GetMaster().Get(ctx, &pendingTokens, `SELECT COUNT(*) FROM user_tokens WHERE user_id=? AND archived_at IS NULL AND consumed_at IS NULL`, f.target.ID.String())
	require.NoError(t, err, "pending primary recovery grants")
	require.Equal(t, 0, pendingTokens, "pending primary recovery grants")
	var desktopRow browserAuthenticationRow
	err = s.GetMaster().Get(ctx, &desktopRow, `SELECT `+browserAuthenticationColumns+` FROM browser_authentication_transactions WHERE id=?`, desktop.ID.String())
	require.NoError(t, err)
	retiredDesktop, err := desktopRow.model()
	require.NoError(t, err, "unfinished Desktop proof survived: %#v", retiredDesktop)
	require.Equal(t, model.BrowserAuthenticationStateCanceled, retiredDesktop.State, "unfinished Desktop proof survived: %#v", retiredDesktop)
	var preparations int
	err = s.GetMaster().Get(ctx, &preparations, `SELECT count(*) FROM personal_access_token_mutation_preparations WHERE id=?`, patPreparation.ID)
	require.NoError(t, err, "unfinished PAT survived")
	require.Equal(t, 0, preparations, "unfinished PAT survived")
	patAudits, err := s.Audit().List(ctx, store.AuditListOptions{ActorID: f.target.ID, Action: "personal_access_token.create", Limit: 10, Visibility: store.AuditVisibilityScope{InstitutionWide: true}})
	require.NoError(t, err, "unfinished PAT proof lacks its terminal audit")
	require.Len(t, patAudits, 1, "unfinished PAT proof lacks its terminal audit")
	require.Equal(t, model.AuditStatusFail, patAudits[0].Status, "unfinished PAT proof lacks its terminal audit")
	require.Equal(t, "authentication.mfa.reenrollment_required", patAudits[0].ErrorCode, "unfinished PAT proof lacks its terminal audit")
	retiredAudit, err := s.Audit().Get(ctx, linkAudit.ID)
	require.NoError(t, err, "unfinished provider proof audit not terminal: %#v", retiredAudit)
	require.Equal(t, model.AuditStatusFail, retiredAudit.Status, "unfinished provider proof audit not terminal: %#v", retiredAudit)
	var consumed bool
	err = s.GetMaster().Get(ctx, &consumed, `SELECT consumed_at IS NOT NULL FROM external_login_states WHERE id=?`, linkState.ID.String())
	require.NoError(t, err, "unfinished provider link survived")
	require.True(t, consumed, "unfinished provider link survived")
	freshSession, freshCredential, _, _ := saveMFARecoverySQLSession(t, ctx, s, f.target.ID, 1, true, false)
	otherSession, otherCredential, otherAccess, _ := saveMFARecoverySQLSession(t, ctx, s, f.target.ID, 1, true, false)
	principal := mfaSQLPrincipal(freshSession, freshCredential)
	require.Error(t, principal.Validate(), "restricted Session has ordinary authority")
	require.NoError(t, principal.ValidateMFARecovery(), "restricted Session has ordinary authority")
	audit, _ = mfaSQLSecurityNoticeFixture(t, ctx, s, f.target, model.MailTemplateIdentityMFAEnabled, model.NowUTC())
	newPending, err := s.MFA().SavePending(ctx, &store.MFAPendingEnrollment{Principal: principal, Credential: &model.MFACredential{UserID: f.target.ID, State: model.MFAStatePending, EncryptedSecret: "new-secret", EncryptionKeyID: "0123456789abcdef0123456789abcdef"}, Lifetime: time.Minute, RecentAuthenticationTTL: time.Hour, AuditEventID: audit.ID})
	require.NoError(t, err)
	// Challenge never lifts the restriction, even with a former code and a valid Session.
	challengeAudit, _ := mfaSQLSecurityNoticeFixture(t, ctx, s, f.target, model.MailTemplateIdentityMFAEnabled, model.NowUTC())
	_, err = s.MFA().Challenge(ctx, &store.MFAChallenge{Principal: principal, CredentialID: newPending.ID, RecoveryCodeHash: oldCode, VerifiedAt: model.NowUTC(), AuditEventID: challengeAudit.ID})
	require.Error(t, err, "challenge bypassed required reenrollment")
	activationAt = model.NowUTC().Truncate(time.Millisecond)
	audit, notice = mfaSQLSecurityNoticeFixture(t, ctx, s, f.target, model.MailTemplateIdentityMFAEnabled, activationAt)
	activated, err := s.MFA().Activate(ctx, &store.MFAActivationMutation{Principal: principal, RecentAuthenticationTTL: time.Hour, CredentialID: newPending.ID, UserID: f.target.ID, TimeStep: time.Now().Unix() / 30, RecoveryCodes: []*model.MFARecoveryCode{{CodeHash: model.HashToken(model.NewCredentialToken())}}, SessionID: freshSession.ID, At: activationAt, AuditEventID: audit.ID, AuditAt: activationAt, Notice: notice})
	require.NoError(t, err)
	state, err = s.MFA().GetRecoveryState(ctx, f.target.ID)
	require.NoError(t, err, "activation lost permanent fence: %#v", state)
	require.False(t, state.ReenrollmentRequired, "activation lost permanent fence: %#v", state)
	require.Equal(t, int64(1), state.Generation, "activation lost permanent fence: %#v", state)
	require.True(t, state.ResetAt.Time.Equal(result.Recovery.ResetAt.Time), "activation lost permanent fence: %#v", state)
	require.False(t, activated.Session.MFARecoveryRequired, "activation did not restore exact current Session")
	require.True(t, mfaSQLPrincipal(activated.Session, freshCredential).HasStrongAuthentication(), "activation did not restore exact current Session")
	_, _, err = s.SessionCredential().GetSessionByTokenHash(ctx, model.HashToken(otherAccess), model.SessionCredentialAccess)
	require.True(t, store.IsNotFound(err), "other recovery Session gained normal access")
	err = requireMFARecoveryPATSource(ctx, s.GetMaster(), f.target.ID.String(), f.targetSession.ID.String(), model.NowUTC())
	require.Error(t, err, "old request can issue PAT after reenrollment")
	err = requireSessionAuthenticationGeneration(ctx, s.GetMaster(), otherSession)
	require.Error(t, err, "stale restricted Session accepted after activation")
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
		t.Run(source.name, func(t *testing.T) {
			audit, err := s.Audit().Save(ctx, &model.AuditEvent{ActorID: f.target.ID, SessionID: source.sessionID, Action: string(model.ActionExternalIdentityManage), Resource: model.Resource{Type: model.ResourceUser, ID: f.target.ID.String()}, ScopeType: model.RoleScopeInstitution, ScopeID: model.NewInstitutionID().String(), Status: model.AuditStatusAttempt, NodeID: "recovery-fence"})
			require.NoError(t, err)
			err = requireMFARecoveryAuthenticationMethodSource(ctx, s.GetMaster(), f.target.ID, audit.ID)
			require.Equal(t, source.permitted, err == nil, "%s credential change authority: %v", source.name, err)
			if !source.permitted {
				_, err = s.PasswordCredential().Enroll(ctx, &store.PasswordCredentialEnrollment{Credential: &model.PasswordCredential{UserID: f.target.ID, PasswordHash: "new-primary"}, AuditEventID: audit.ID, AuditAt: model.NowUTC()})
				require.True(t, store.IsNotFound(err), "old request reached password enrollment")
			}
		})
	}
	_ = otherCredential
	err = requireSessionIssuanceRecovery(ctx, s.GetMaster(), &model.Session{UserID: f.target.ID, AuthenticationGeneration: 1, AuthenticationMethod: "oidc", AuthenticationProviderID: "campus", ClientType: model.SessionClientWeb}, oldState.ID)
	require.ErrorIs(t, err, store.ErrAuthenticationGenerationChanged)
	storedAudit, err := s.Audit().Get(ctx, input.AuditEventID)
	require.NoError(t, err, "reset audit did not commit")
	require.Equal(t, model.AuditStatusSuccess, storedAudit.Status, "reset audit did not commit")
}

func saveMFARecoveryExternalState(t *testing.T, ctx context.Context, s *SQLStore, provider string, purpose model.ExternalAuthenticationPurpose, target model.UserID) *model.ExternalLoginState {
	t.Helper()
	stateHash, bindingHash := model.HashToken(model.NewCredentialToken()), model.HashToken(model.NewCredentialToken())
	state, err := s.ExternalLoginState().Save(ctx, &model.ExternalLoginState{Provider: provider, Purpose: purpose, TargetUserID: target, StateHash: stateHash, BindingHash: bindingHash, ReturnTo: "/account/security", ClientType: model.SessionClientWeb}, time.Minute)
	require.NoError(t, err)
	state, err = s.ExternalLoginState().Consume(ctx, provider, stateHash, bindingHash)
	require.NoError(t, err)
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
			require.NoError(t, err)
			_, err = f.persistence.MFA().Reset(ctx, input)
			require.Error(t, err, "reset used stale or insufficient authority")
			state, err := f.persistence.MFA().GetRecoveryState(ctx, f.target.ID)
			require.NoError(t, err, "denied reset changed recovery state")
			require.Equal(t, int64(0), state.Generation, "denied reset changed recovery state")
		})
	}
}
