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
	"crypto/sha256"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/storetest"
)

// The tests in this file move rows created by the store a minute ahead of
// PostgreSQL time, as a creating node whose clock leads the database does.
// PostgreSQL time still decides validity; recorded transitions must never
// precede the lifecycles they follow, and returned models equal stored rows.

func timeAuthorityAccessLead(t *testing.T, ctx context.Context, persistence *SQLStore, query string, args ...any) {
	t.Helper()
	if _, err := persistence.GetMaster().Exec(ctx, query, args...); err != nil {
		t.Fatal(err)
	}
}

func timeAuthorityAccessCheck(t *testing.T, ctx context.Context, persistence *SQLStore, query string, args ...any) {
	t.Helper()
	var ok bool
	if err := persistence.GetMaster().Get(ctx, &ok, query, args...); err != nil || !ok {
		t.Fatalf("stored lifecycle check %q = %v, %v", query, ok, err)
	}
}

func timeAuthorityAccessAttempt(t *testing.T, ctx context.Context, persistence *SQLStore, userID model.UserID, action string) *model.AuditEvent {
	t.Helper()
	scopeID := model.NewID()
	event, err := persistence.Audit().Save(ctx, &model.AuditEvent{ActorID: userID, Action: action,
		Resource: model.Resource{Type: model.ResourceInstitution, ID: scopeID}, ScopeType: model.RoleScopeInstitution,
		ScopeID: scopeID, Status: model.AuditStatusAttempt, NodeID: "time-authority", ClientType: string(model.SessionClientWeb), AuthMethod: "password"})
	if err != nil {
		t.Fatal(err)
	}
	return event
}

func timeAuthorityAccessSession(t *testing.T, ctx context.Context, persistence *SQLStore, name string) (*model.User, *model.Session, []*model.SessionCredential) {
	t.Helper()
	user := saveIntegrationUser(t, ctx, persistence, &model.User{Username: name, Email: name + "@example.edu"})
	session, credentials := authenticationPolicyTestSession(user.ID, "password", "", "")
	saved, savedCredentials, err := storetest.SaveSession(ctx, persistence, sessionCreationForSQLTest(t, ctx, persistence, session, credentials, 10))
	if err != nil {
		t.Fatal(err)
	}
	return user, saved, savedCredentials
}

func TestTimeAuthorityAccessPolicyReplacementAfterLeadingPolicyClock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	f := mfaRecoveryFixture(t, ctx)
	s := f.persistence
	timeAuthorityAccessLead(t, ctx, s, `UPDATE access_policies SET created_at=created_at+INTERVAL '1 minute', updated_at=updated_at+INTERVAL '1 minute'`)
	before, err := s.AccessPolicy().Get(ctx, model.AccessPolicyTransitionHistoryLimit)
	if err != nil {
		t.Fatal(err)
	}
	settings := before.Policy.Settings()
	settings.PublicRegistrationEnabled = !settings.PublicRegistrationEnabled
	audit := timeAuthorityAccessAttempt(t, ctx, s, f.actor.ID, string(model.ActionAccessPolicyManage))
	replacement := &store.AccessPolicyReplacement{Preflight: store.AccessPolicyPreflight{ExpectedRevision: before.Policy.Revision,
		Settings: settings, Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}}, CheckedAt: model.NowUTC()},
		ActorID: f.actor.ID, AuditEventID: audit.ID, AuditAt: model.MillisFromTime(audit.CreatedAt)}
	command := &store.CommandIdempotency{UserID: f.actor.ID, Operation: "access_policy.replace.v1",
		KeyDigest: sha256.Sum256([]byte("time-authority-access-policy")), FingerprintVersion: 1,
		Fingerprint: sha256.Sum256([]byte("time-authority-access-policy-command")), OutcomeVersion: 1, Retention: time.Hour, Wait: time.Second}
	result, err := s.AccessPolicy().Replace(ctx, replacement, command)
	if err != nil {
		t.Fatal(err)
	}
	policy := result.Snapshot.Policy
	if !result.Changed || policy.UpdatedAt.Before(before.Policy.UpdatedAt) || len(result.Snapshot.History) == 0 ||
		!result.Snapshot.History[0].ChangedAt.Equal(policy.UpdatedAt) {
		t.Fatalf("replacement after leading policy clock = %#v", result.Snapshot)
	}
	stored, err := s.AccessPolicy().Get(ctx, model.AccessPolicyTransitionHistoryLimit)
	if err != nil || !stored.Policy.UpdatedAt.Equal(policy.UpdatedAt) || stored.Policy.Revision != policy.Revision {
		t.Fatalf("stored policy = %#v, %v; want %#v", stored, err, policy)
	}
}

func TestTimeAuthorityAccessPasswordReauthenticationAfterLeadingSessionClock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	f := mfaRecoveryFixture(t, ctx)
	s := f.persistence
	leadSessionDatabaseClock(t, ctx, s, f.targetSession.ID)
	session, err := s.Session().Get(ctx, f.targetSession.ID)
	if err != nil {
		t.Fatal(err)
	}
	audit, _ := mfaSQLSecurityNoticeFixture(t, ctx, s, f.target, model.MailTemplateIdentityMFAEnabled, model.GetMillis())
	result, err := s.Session().ReauthenticatePassword(ctx, &store.SessionPasswordReauthentication{SessionID: session.ID,
		CredentialID: f.targetCredential.ID, UserID: f.target.ID, PasswordProof: passwordProofForSQLTest(t, ctx, s, f.target.ID), AuditEventID: audit.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Session.ReauthenticatedAt.Valid || result.Session.ReauthenticatedAt.Time.Before(session.AuthenticatedAt) {
		t.Fatalf("reauthenticated Session = %#v", result.Session)
	}
	stored, err := s.Session().Get(ctx, session.ID)
	if err != nil || !stored.ReauthenticatedAt.Time.Equal(result.Session.ReauthenticatedAt.Time) || !stored.UpdatedAt.Equal(result.Session.UpdatedAt) {
		t.Fatalf("stored Session = %#v, %v; want %#v", stored, err, result.Session)
	}
}

func TestTimeAuthorityAccessMFAUpgradeAfterLeadingSessionClock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s := openTestStore(t)
	resetTestStore(t, s)
	user, session, _ := timeAuthorityAccessSession(t, ctx, s, "time-authority-mfa-upgrade")
	leadSessionDatabaseClock(t, ctx, s, session.ID)
	if _, err := s.MFA().UpgradeSession(ctx, session.ID, user.ID, model.NowUTC()); err != nil {
		t.Fatal(err)
	}
	stored, err := s.Session().Get(ctx, session.ID)
	if err != nil || stored.AuthenticationStrength != model.AuthenticationMultiFactor ||
		!stored.MFACompletedAt.Valid || stored.MFACompletedAt.Time.Before(stored.AuthenticatedAt) {
		t.Fatalf("upgraded Session = %#v, %v", stored, err)
	}
}

func TestTimeAuthorityAccessRecoveryCodeConsumptionAfterLeadingCodeClock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s := openTestStore(t)
	resetTestStore(t, s)
	user := saveIntegrationUser(t, ctx, s, &model.User{Username: "time-authority-recovery-code", Email: "time-authority-recovery-code@example.edu"})
	codeHash := model.HashToken(model.NewCredentialToken())
	timeAuthorityAccessLead(t, ctx, s, `INSERT INTO mfa_credentials(id,created_at,updated_at,user_id,state,encrypted_secret,encryption_key_id,activated_at,last_used_time_step)
		VALUES(?,clock_timestamp()+INTERVAL '1 minute',clock_timestamp()+INTERVAL '1 minute',?,'active','encrypted-secret','0123456789abcdef0123456789abcdef',clock_timestamp()+INTERVAL '1 minute',1)`,
		model.NewMFACredentialID().String(), user.ID.String())
	timeAuthorityAccessLead(t, ctx, s, `INSERT INTO mfa_recovery_codes(id,created_at,updated_at,user_id,code_hash)
		VALUES(?,clock_timestamp()+INTERVAL '1 minute',clock_timestamp()+INTERVAL '1 minute',?,?)`,
		model.NewMFARecoveryCodeID().String(), user.ID.String(), codeHash)
	if err := s.MFA().ConsumeSecondFactor(ctx, user.ID, 0, codeHash, model.GetMillis()); err != nil {
		t.Fatal(err)
	}
	timeAuthorityAccessCheck(t, ctx, s, `SELECT consumed_at >= created_at FROM mfa_recovery_codes WHERE code_hash=?`, codeHash)
}

func TestTimeAuthorityAccessMFAResetAfterLeadingNodeClock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	f := mfaRecoveryFixture(t, ctx)
	s := f.persistence
	leadSessionDatabaseClock(t, ctx, s, f.targetSession.ID)
	tokenID := model.NewUserTokenID()
	timeAuthorityAccessLead(t, ctx, s, `INSERT INTO user_tokens(id,created_at,updated_at,user_id,purpose,token_hash,target,expires_at)
		VALUES(?,clock_timestamp()+INTERVAL '1 minute',clock_timestamp()+INTERVAL '1 minute',?,'password_reset',?,?,clock_timestamp()+INTERVAL '1 hour')`,
		tokenID.String(), f.target.ID.String(), model.HashToken(model.NewCredentialToken()), f.target.Email)
	result, err := s.MFA().Reset(ctx, f.resetInput(t, ctx))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Sessions) != 1 {
		t.Fatalf("reset Sessions = %#v", result.Sessions)
	}
	returned := result.Sessions[0]
	stored, err := s.Session().Get(ctx, returned.ID)
	if err != nil || !stored.RevokedAt.Valid || !stored.RevokedAt.Time.Equal(returned.RevokedAt.Time) || !stored.UpdatedAt.Equal(returned.UpdatedAt) {
		t.Fatalf("stored reset Session = %#v, %v; returned %#v", stored, err, returned)
	}
	timeAuthorityAccessCheck(t, ctx, s, `SELECT archived_at >= created_at FROM user_tokens WHERE id=?`, tokenID.String())
}

func TestTimeAuthorityAccessExternalIdentityUnlinkAfterLeadingIdentityClock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s := openTestStore(t)
	resetTestStore(t, s)
	user := saveIntegrationUser(t, ctx, s, &model.User{Username: "time-authority-unlink", Email: "time-authority-unlink@example.edu"})
	passwordProofForSQLTest(t, ctx, s, user.ID)
	identity, err := s.ExternalIdentity().Save(ctx, &model.ExternalIdentity{UserID: user.ID, Provider: "campus", Subject: "time-authority-unlink"})
	if err != nil {
		t.Fatal(err)
	}
	timeAuthorityAccessLead(t, ctx, s, `UPDATE external_identities SET created_at=created_at+INTERVAL '1 minute', updated_at=updated_at+INTERVAL '1 minute' WHERE id=?`, identity.ID.String())
	audit := timeAuthorityAccessAttempt(t, ctx, s, user.ID, "authentication.external_identity.unlink")
	result, err := s.ExternalIdentity().Unlink(ctx, &store.ExternalIdentityUnlink{ID: identity.ID, UserID: user.ID,
		Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}}, ChangedAt: model.GetMillis(),
		RevocationReason: model.SessionRevocationExternalIdentityUnlinked, AuditEventID: audit.ID, AuditAt: model.MillisFromTime(audit.CreatedAt)})
	if err != nil {
		t.Fatal(err)
	}
	if err = result.Identity.Validate(); err != nil {
		t.Fatalf("unlinked identity = %#v: %v", result.Identity, err)
	}
	timeAuthorityAccessCheck(t, ctx, s, `SELECT archived_at=? AND updated_at=? FROM external_identities WHERE id=?`,
		result.Identity.ArchivedAt.Time, result.Identity.UpdatedAt, identity.ID.String())
}

func TestTimeAuthorityAccessDesktopRegistrationRevocationAfterLeadingNodeClock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s := openTestStore(t)
	resetTestStore(t, s)
	institution, err := s.Institution().Save(ctx, &model.Institution{Name: "time-authority-desktop", DisplayName: "Time Authority Desktop"})
	if err != nil {
		t.Fatal(err)
	}
	user := saveIntegrationUser(t, ctx, s, &model.User{Username: "time-authority-desktop", Email: "time-authority-desktop@example.edu"})
	_, code, state, verifier := issueDesktopAuthorizationForSQLTest(t, ctx, s, institution.ID, user.ID)
	exchanged, err := s.BrowserAuthentication().Exchange(ctx, desktopAuthorizationExchangeForSQLTest(code, state, verifier,
		saveDesktopAuthorizationAuditForSQLTest(t, ctx, s, institution.ID, user.ID, "exchange")))
	if err != nil {
		t.Fatal(err)
	}
	timeAuthorityAccessLead(t, ctx, s, `UPDATE desktop_registrations SET created_at=created_at+INTERVAL '1 minute',
		updated_at=updated_at+INTERVAL '1 minute', last_used_at=last_used_at+INTERVAL '1 minute' WHERE id=?`, exchanged.Registration.ID.String())
	leadSessionDatabaseClock(t, ctx, s, exchanged.Session.ID)
	audit := saveDesktopAuthorizationAuditForSQLTest(t, ctx, s, institution.ID, user.ID, "registration-revoke")
	revoked, err := s.DesktopRegistration().Revoke(ctx, &store.DesktopRegistrationRevocation{RegistrationID: exchanged.Registration.ID,
		UserID: user.ID, RevokedAt: model.GetMillis(), AuditEventID: audit.ID, AuditAt: model.GetMillis()})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := s.DesktopRegistration().Get(ctx, exchanged.Registration.ID)
	if err != nil || !stored.RevokedAt.Valid || !stored.RevokedAt.Time.Equal(revoked.Registration.RevokedAt.Time) ||
		!stored.UpdatedAt.Equal(revoked.Registration.UpdatedAt) {
		t.Fatalf("stored registration = %#v, %v; returned %#v", stored, err, revoked.Registration)
	}
	session, err := s.Session().Get(ctx, exchanged.Session.ID)
	if err != nil || len(revoked.Sessions) != 1 || !session.RevokedAt.Time.Equal(revoked.Sessions[0].RevokedAt.Time) {
		t.Fatalf("stored Session = %#v, %v; returned %#v", session, err, revoked.Sessions)
	}
	timeAuthorityAccessCheck(t, ctx, s, `SELECT bool_and(revoked_at >= created_at) FROM session_credentials WHERE session_id=?`, exchanged.Session.ID.String())
}

func TestTimeAuthorityAccessRefreshReplayAfterLeadingSessionClock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s := openTestStore(t)
	resetTestStore(t, s)
	_, session, credentials := timeAuthorityAccessSession(t, ctx, s, "time-authority-refresh")
	var refreshHash string
	for _, credential := range credentials {
		if credential.Kind == model.SessionCredentialRefresh {
			refreshHash = credential.TokenHash
		}
	}
	rotate := func() (*store.SessionRotation, error) {
		now := model.NowUTC()
		return s.SessionCredential().RotateRefresh(ctx, refreshHash,
			&model.SessionCredential{TokenHash: model.HashToken(model.NewCredentialToken()), ExpiresAt: now.Add(10 * time.Minute)},
			&model.SessionCredential{TokenHash: model.HashToken(model.NewCredentialToken()), ExpiresAt: now.Add(time.Hour)},
			now, now.Add(30*time.Minute))
	}
	if _, err := rotate(); err != nil {
		t.Fatal(err)
	}
	leadSessionDatabaseClock(t, ctx, s, session.ID)
	timeAuthorityAccessLead(t, ctx, s, `UPDATE session_credentials SET used_at=used_at+INTERVAL '1 minute',
		revoked_at=revoked_at+INTERVAL '1 minute' WHERE session_id=?`, session.ID.String())
	replay, err := rotate()
	if err != nil {
		t.Fatal(err)
	}
	if !replay.ReplayDetected || !replay.Session.RevokedAt.Valid {
		t.Fatalf("refresh replay = %#v", replay)
	}
	stored, err := s.Session().Get(ctx, session.ID)
	if err != nil || !stored.RevokedAt.Time.Equal(replay.Session.RevokedAt.Time) || !stored.UpdatedAt.Equal(replay.Session.UpdatedAt) {
		t.Fatalf("stored replayed Session = %#v, %v; returned %#v", stored, err, replay.Session)
	}
	timeAuthorityAccessCheck(t, ctx, s, `SELECT bool_and(revoked_at >= created_at) FROM session_credentials WHERE session_id=?`, session.ID.String())
}

func TestTimeAuthorityAccessSessionRevocationModelAfterLeadingSessionClock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s := openTestStore(t)
	resetTestStore(t, s)
	user, session, _ := timeAuthorityAccessSession(t, ctx, s, "time-authority-session-revoke")
	leadSessionDatabaseClock(t, ctx, s, session.ID)
	audit := timeAuthorityAccessAttempt(t, ctx, s, user.ID, "authentication.session.revoke")
	result, err := s.Session().Revoke(ctx, &store.SessionRevocation{SessionID: session.ID, UserID: user.ID, RevokedAt: model.GetMillis(),
		Reason: model.SessionRevocationUserSession, AuditEventID: audit.ID, AuditAt: model.MillisFromTime(audit.CreatedAt)})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := s.Session().Get(ctx, session.ID)
	if err != nil || !stored.RevokedAt.Time.Equal(result.Session.RevokedAt.Time) || !stored.UpdatedAt.Equal(result.Session.UpdatedAt) {
		t.Fatalf("stored revoked Session = %#v, %v; returned %#v", stored, err, result.Session)
	}
}

func TestTimeAuthorityAccessPersonalAccessTokenUseAfterLeadingTokenClock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s := openTestStore(t)
	resetTestStore(t, s)
	user := saveIntegrationUser(t, ctx, s, &model.User{Username: "time-authority-pat", Email: "time-authority-pat@example.edu"})
	tokenID, tokenHash := model.NewPersonalAccessTokenID(), model.HashToken(model.NewCredentialToken())
	timeAuthorityAccessLead(t, ctx, s, `INSERT INTO personal_access_tokens(id,created_at,updated_at,user_id,description,token_hash,scopes,expires_at)
		VALUES(?,clock_timestamp()+INTERVAL '1 minute',clock_timestamp()+INTERVAL '1 minute',?,'time authority',?,ARRAY['user.view_self'],clock_timestamp()+INTERVAL '1 hour')`,
		tokenID.String(), user.ID.String(), tokenHash)
	resolved, err := s.PersonalAccessToken().Resolve(ctx, tokenHash, model.NowUTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := s.PersonalAccessToken().Get(ctx, tokenID)
	if err != nil || !stored.LastUsedAt.Valid || !stored.LastUsedAt.Time.Equal(resolved.Token.LastUsedAt.Time) ||
		!stored.UpdatedAt.Equal(resolved.Token.UpdatedAt) {
		t.Fatalf("stored token = %#v, %v; resolved %#v", stored, err, resolved.Token)
	}
}
