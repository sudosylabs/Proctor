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
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/storetest"
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
	require.NoError(t, err)
	user := saveIntegrationUser(t, ctx, persistence, &model.User{Username: "policy-fence-user", Email: "policy-fence@example.edu"})
	credential, err := persistence.PasswordCredential().Save(ctx, &model.PasswordCredential{
		UserID: user.ID, PasswordHash: "encoded-original-password",
	})
	require.NoError(t, err)

	identity, err := persistence.ExternalIdentity().Save(ctx, &model.ExternalIdentity{UserID: user.ID, Provider: "campus",
		Subject: "policy-fence-subject", LastSeenAt: model.OptionalTimeFrom(model.NowUTC())})
	require.NoError(t, err)
	localSession, localCredentials := authenticationPolicyTestSession(user.ID, "password", "", "")
	savedLocal, _, err := storetest.SaveSession(ctx, persistence, sessionCreationForSQLTest(t, ctx, persistence, localSession, localCredentials, 10))
	require.NoError(t, err)
	externalSession, externalCredentials := authenticationPolicyTestSession(user.ID, "oidc", "campus", identity.ID)
	savedExternal, _, err := storetest.SaveSession(ctx, persistence, sessionCreationForSQLTest(t, ctx, persistence, externalSession, externalCredentials, 10))
	require.NoError(t, err)
	snapshot, err := persistence.AccessPolicy().Get(ctx, 0)
	require.NoError(t, err)
	withoutCollidingProviderIDs := snapshot.Policy.Clone()
	delete(withoutCollidingProviderIDs.ProviderAdmissions, "password")
	delete(withoutCollidingProviderIDs.ProviderAdmissions, "oidc")
	revocations, err := runSQLTransaction(ctx, persistence.GetMaster().Begin, "exact authentication provider revocation", func(ctx context.Context, tx *sqlxTxWrapper) ([]store.AccessPolicySessionRevocation, error) {
		return revokeSessionsForDisabledAccessMethods(ctx, tx, snapshot.Policy, withoutCollidingProviderIDs, model.NowUTC())
	})
	require.NoError(t, err)
	require.Empty(t, revocations)

	resetToken := &model.UserToken{UserID: user.ID, Purpose: model.UserTokenPasswordReset,
		TokenHash: model.HashToken(model.NewCredentialToken()), Target: user.Email, ExpiresAt: model.NowUTC().Add(time.Hour)}
	resetToken, err = authenticationPolicyTestIssue(t, ctx, persistence, resetToken, authenticationPolicyTestAudit(
		"authentication.password_reset.request", user.ID.String(), institution.ID.String(),
	))
	require.NoError(t, err)

	_, err = persistence.GetMaster().Exec(ctx, `UPDATE access_policies SET local_login_enabled=FALSE,
		invitation_local_credential_enabled=FALSE, provider_admissions='{}'::jsonb WHERE singleton=1`)
	require.NoError(t, err)

	blockedLocal, blockedLocalCredentials := authenticationPolicyTestSession(user.ID, "password", "", "")
	_, _, err = storetest.SaveSession(ctx, persistence, sessionCreationForSQLTest(t, ctx, persistence, blockedLocal, blockedLocalCredentials, 10))
	require.ErrorIs(t, err, store.ErrAuthenticationMethodDisabled)
	blockedExternal, blockedExternalCredentials := authenticationPolicyTestSession(user.ID, "oidc", "campus", identity.ID)
	_, _, err = storetest.SaveSession(ctx, persistence, sessionCreationForSQLTest(t, ctx, persistence, blockedExternal, blockedExternalCredentials, 10))
	require.ErrorIs(t, err, store.ErrAuthenticationMethodDisabled)
	blockedIssue := &model.UserToken{UserID: user.ID, Purpose: model.UserTokenPasswordReset,
		TokenHash: model.HashToken(model.NewCredentialToken()), Target: user.Email, ExpiresAt: model.NowUTC().Add(time.Hour)}
	_, err = authenticationPolicyTestIssue(t, ctx, persistence, blockedIssue, authenticationPolicyTestAudit(
		"authentication.password_reset.request", user.ID.String(), institution.ID.String(),
	))
	require.ErrorIs(t, err, store.ErrAuthenticationMethodDisabled)
	completion := authenticationPolicyTestResetCompletion(t, user, resetToken.TokenHash, "encoded-new-password",
		model.GetMillis(), authenticationPolicyTestCompletionAudit(institution.ID.String()))
	_, err = persistence.UserToken().ConsumePasswordReset(ctx, completion)
	require.ErrorIs(t, err, store.ErrAuthenticationMethodDisabled)
	unchangedCredential, err := persistence.PasswordCredential().GetByUser(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, credential.ID, unchangedCredential.ID)
	require.Equal(t, credential.PasswordHash, unchangedCredential.PasswordHash)
	retainedToken, err := persistence.UserToken().GetByHash(ctx, resetToken.TokenHash, model.UserTokenPasswordReset)
	require.NoError(t, err, "disabled reset consumed token=%#v err", retainedToken)
	require.False(t, retainedToken.ConsumedAt.Valid, "disabled reset consumed token=%#v err", retainedToken)
	for _, sessionID := range []model.SessionID{savedLocal.ID, savedExternal.ID} {
		retained, getErr := persistence.Session().Get(ctx, sessionID)
		require.NoError(t, getErr, "policy fence changed existing session %s: %#v", sessionID, retained)
		require.False(t, retained.RevokedAt.Valid, "policy fence changed existing session %s: %#v", sessionID, retained)
	}

	disabledIdentity, err := persistence.ExternalIdentity().Save(ctx, &model.ExternalIdentity{UserID: user.ID, Provider: "campus",
		Subject: "subject", LastSeenAt: model.OptionalTimeFrom(model.NowUTC())})
	require.NoError(t, err)
	_, err = persistence.ExternalIdentity().ResolveOrProvision(ctx, &store.ExternalIdentityResolutionRequest{Identity: &model.ExternalIdentity{
		Provider: disabledIdentity.Provider, Subject: disabledIdentity.Subject, LastSeenAt: model.OptionalTimeFrom(model.NowUTC()),
	}, Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{
		"campus": {},
	}}})
	require.ErrorIs(t, err, store.ErrAuthenticationMethodDisabled)
}

func authenticationPolicyTestSession(userID model.UserID, method, providerID string, identityID model.ExternalIdentityID) (*model.Session, []*model.SessionCredential) {
	now := model.NowUTC()
	absolute := now.Add(24 * time.Hour)
	return &model.Session{UserID: userID, ClientType: model.SessionClientWeb, AuthenticationMethod: method,
			AuthenticationProviderID: providerID, ExternalIdentityID: identityID, AuthenticationStrength: model.AuthenticationSingleFactor,
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

func authenticationPolicyTestIssue(t *testing.T, ctx context.Context, persistence store.Store, token *model.UserToken, audit *model.AuditEvent) (*model.UserToken, error) {
	t.Helper()
	token.PrepareCreate(model.NewUserTokenID(), model.NowUTC())
	key := model.MailTemplateIdentityVerifyEmail
	if token.Purpose == model.UserTokenPasswordReset {
		key = model.MailTemplateIdentityPasswordReset
	}
	occurrence, delivery, job := authenticationPolicyTestMail(t, token.UserID, model.MailOccurrenceID(token.ID.String()), model.MailOccurrenceAccountToken, key, model.JobTypeMailDeliverCredential, token.CreatedAt, token.ExpiresAt)
	return persistence.UserToken().Issue(ctx, &store.UserTokenMailIssue{Token: token, Occurrence: occurrence, Delivery: delivery, Job: job, AuditEvent: audit})
}

func authenticationPolicyTestResetCompletion(t *testing.T, user *model.User, tokenHash, passwordHash string, at int64, audit *model.AuditEvent) *store.PasswordResetCompletion {
	t.Helper()
	when := model.TimeFromMillis(at)
	occurrence, delivery, job := authenticationPolicyTestMail(t, user.ID, model.NewMailOccurrenceID(), model.MailOccurrenceSecurityNotice, model.MailTemplateIdentityPasswordChanged, model.JobTypeMailDeliver, when, when.Add(24*time.Hour))
	return &store.PasswordResetCompletion{TokenHash: tokenHash, PasswordHash: passwordHash, At: at, RevocationReason: model.SessionRevocationPasswordReset, AuditEvent: audit, Occurrence: occurrence, Delivery: delivery, Job: job}
}

func authenticationPolicyTestMail(t *testing.T, userID model.UserID, occurrenceID model.MailOccurrenceID, kind model.MailOccurrenceKind, key model.MailTemplateKey, jobType model.JobType, at, deadline time.Time) (*model.MailOccurrence, *model.MailDelivery, *model.Job) {
	t.Helper()
	deliveryID := model.NewMailDeliveryID()
	command, err := model.EncodeMailDeliveryCommand(model.MailDeliveryCommand{DeliveryID: deliveryID})
	require.NoError(t, err)
	job, err := model.NewJob(model.NewJobID(), jobType, command, deliveryID.String(), at, at, model.MailMaximumAttempts)
	require.NoError(t, err)
	occurrence := &model.MailOccurrence{ID: occurrenceID, Kind: kind, TemplateKey: key, ActorUserID: userID, CreatedAt: at}
	delivery := &model.MailDelivery{ID: deliveryID, OccurrenceID: occurrenceID, JobID: job.ID, TargetUserID: userID,
		TemplateKey: key, TemplateDigest: strings.Repeat("a", 64), MaskedRecipient: "u***@example.test",
		State: model.MailDeliveryQueued, CreatedAt: at, UpdatedAt: at, MessageDate: at, Deadline: deadline,
		MessageID: "<mail." + deliveryID.String() + "@example.test>", EncryptedPayload: json.RawMessage(`{"key_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`), Revision: 1}
	return occurrence, delivery, job
}
