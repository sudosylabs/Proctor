//go:build integration

// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestAuthenticationTerminalCommitsRecheckCurrentAccessPolicy(t *testing.T) {
	ctx := context.Background()
	persistence := openTestStore(t)
	resetPristineTestStore(t, persistence)
	seedTestAuthenticationPolicy(t, persistence, map[string]model.ProviderAdmissionMode{
		"campus":   model.ProviderAdmissionLinkedOnly,
		"oidc":     model.ProviderAdmissionLinkedOnly,
		"password": model.ProviderAdmissionLinkedOnly,
	})
	institution, err := persistence.Institution().Save(ctx, &model.Institution{Name: "policy-fence", DisplayName: "Policy Fence"})
	if err != nil {
		t.Fatal(err)
	}
	user := saveIntegrationUser(t, ctx, persistence, &model.User{Username: "policy-fence-user", Email: "policy-fence@example.edu"})
	credential, err := persistence.PasswordCredential().Save(ctx, &model.PasswordCredential{
		UserID: user.ID, PasswordHash: "encoded-original-password",
	})
	if err != nil {
		t.Fatal(err)
	}

	localSession, localCredentials := authenticationPolicyTestSession(user.ID, "password", "")
	savedLocal, _, err := persistence.Session().Save(ctx, localSession, localCredentials, 10)
	if err != nil {
		t.Fatal(err)
	}
	externalSession, externalCredentials := authenticationPolicyTestSession(user.ID, "oidc", "campus")
	savedExternal, _, err := persistence.Session().Save(ctx, externalSession, externalCredentials, 10)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := persistence.AccessPolicy().Get(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	withoutCollidingProviderIDs := snapshot.Policy.Clone()
	delete(withoutCollidingProviderIDs.ProviderAdmissions, "password")
	delete(withoutCollidingProviderIDs.ProviderAdmissions, "oidc")
	revocations, err := runSQLTransaction(ctx, persistence.GetMaster().Begin, "exact authentication provider revocation", func(ctx context.Context, tx *sqlxTxWrapper) ([]store.AccessPolicySessionRevocation, error) {
		return revokeSessionsForDisabledAccessMethods(ctx, tx, snapshot.Policy, withoutCollidingProviderIDs, model.NowUTC())
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(revocations) != 0 {
		t.Fatalf("provider IDs colliding with password/oidc revoked unrelated sessions: %#v", revocations)
	}

	resetToken := &model.UserToken{UserID: user.ID, Purpose: model.UserTokenPasswordReset,
		TokenHash: model.HashToken(model.NewCredentialToken()), Target: user.Email, ExpiresAt: model.NowUTC().Add(time.Hour)}
	resetToken, err = persistence.UserToken().Issue(ctx, resetToken, authenticationPolicyTestAudit(
		"authentication.password_reset.request", user.ID.String(), institution.ID.String(),
	))
	if err != nil {
		t.Fatal(err)
	}

	if _, err = persistence.GetMaster().Exec(ctx, `UPDATE access_policies SET local_login_enabled=FALSE,
		invitation_local_credential_enabled=FALSE, provider_admissions='{}'::jsonb WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}

	blockedLocal, blockedLocalCredentials := authenticationPolicyTestSession(user.ID, "password", "")
	if _, _, err = persistence.Session().Save(ctx, blockedLocal, blockedLocalCredentials, 10); !errors.Is(err, store.ErrAuthenticationMethodDisabled) {
		t.Fatalf("disabled local session error = %v", err)
	}
	blockedExternal, blockedExternalCredentials := authenticationPolicyTestSession(user.ID, "oidc", "campus")
	if _, _, err = persistence.Session().Save(ctx, blockedExternal, blockedExternalCredentials, 10); !errors.Is(err, store.ErrAuthenticationMethodDisabled) {
		t.Fatalf("disabled provider session error = %v", err)
	}
	blockedIssue := &model.UserToken{UserID: user.ID, Purpose: model.UserTokenPasswordReset,
		TokenHash: model.HashToken(model.NewCredentialToken()), Target: user.Email, ExpiresAt: model.NowUTC().Add(time.Hour)}
	if _, err = persistence.UserToken().Issue(ctx, blockedIssue, authenticationPolicyTestAudit(
		"authentication.password_reset.request", user.ID.String(), institution.ID.String(),
	)); !errors.Is(err, store.ErrAuthenticationMethodDisabled) {
		t.Fatalf("disabled reset issue error = %v", err)
	}
	if _, err = persistence.UserToken().ConsumePasswordReset(ctx, resetToken.TokenHash, "encoded-new-password",
		model.GetMillis(), "password reset", authenticationPolicyTestCompletionAudit(institution.ID.String())); !errors.Is(err, store.ErrAuthenticationMethodDisabled) {
		t.Fatalf("disabled reset completion error = %v", err)
	}
	unchangedCredential, err := persistence.PasswordCredential().GetByUser(ctx, user.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	if unchangedCredential.ID != credential.ID || unchangedCredential.PasswordHash != credential.PasswordHash {
		t.Fatalf("disabled reset changed credential = %#v", unchangedCredential)
	}
	retainedToken, err := persistence.UserToken().GetByHash(ctx, resetToken.TokenHash, model.UserTokenPasswordReset)
	if err != nil || retainedToken.ConsumedAt.Valid {
		t.Fatalf("disabled reset consumed token=%#v err=%v", retainedToken, err)
	}
	for _, sessionID := range []model.SessionID{savedLocal.ID, savedExternal.ID} {
		retained, getErr := persistence.Session().Get(ctx, sessionID.String())
		if getErr != nil || retained.RevokedAt.Valid {
			t.Fatalf("policy fence changed existing session %s: %#v %v", sessionID, retained, getErr)
		}
	}

	identity, err := persistence.ExternalIdentity().Save(ctx, &model.ExternalIdentity{UserID: user.ID, Provider: "campus",
		Subject: "subject", LastSeenAt: model.OptionalTimeFrom(model.NowUTC())})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = persistence.ExternalIdentity().ResolveOrProvision(ctx, &store.ExternalIdentityResolutionRequest{Identity: &model.ExternalIdentity{
		Provider: identity.Provider, Subject: identity.Subject, LastSeenAt: model.OptionalTimeFrom(model.NowUTC()),
	}}); !errors.Is(err, store.ErrAuthenticationMethodDisabled) {
		t.Fatalf("disabled provider resolution error = %v", err)
	}
}

func authenticationPolicyTestSession(userID model.UserID, method, providerID string) (*model.Session, []*model.SessionCredential) {
	now := model.NowUTC()
	absolute := now.Add(24 * time.Hour)
	return &model.Session{UserID: userID, ClientType: model.SessionClientWeb, AuthenticationMethod: method,
			AuthenticationProviderID: providerID, AuthenticationStrength: model.AuthenticationSingleFactor,
			AuthenticatedAt: now, LastActivityAt: now, IdleExpiresAt: now.Add(time.Hour), ExpiresAt: absolute},
		[]*model.SessionCredential{{Kind: model.SessionCredentialAccess, TokenHash: model.HashToken(model.NewCredentialToken()), ExpiresAt: now.Add(15 * time.Minute)},
			{Kind: model.SessionCredentialRefresh, TokenHash: model.HashToken(model.NewCredentialToken()), ExpiresAt: absolute}}
}

func authenticationPolicyTestAudit(action, userID, institutionID string) *model.AuditEvent {
	return &model.AuditEvent{Action: action, Resource: model.Resource{Type: model.ResourceUser, ID: userID},
		ScopeType: model.RoleScopeInstitution, ScopeID: institutionID, Status: model.AuditStatusSuccess,
		NodeID: "policy-fence-test", AuthMethod: "test"}
}

func authenticationPolicyTestCompletionAudit(institutionID string) *model.AuditEvent {
	return &model.AuditEvent{Action: "authentication.password_reset.complete", Resource: model.Resource{Type: model.ResourceUser},
		ScopeType: model.RoleScopeInstitution, ScopeID: institutionID, Status: model.AuditStatusSuccess,
		NodeID: "policy-fence-test", AuthMethod: "password_reset_token"}
}
