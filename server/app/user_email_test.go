// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestEmailChangeAuditOmitsOldAndNewMailboxes(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	user := &model.User{ID: model.NewUserID(), CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour), Revision: 4,
		Username: "student", Email: "old-private@example.edu", EmailVerified: true, DisplayName: "Student", Locale: "en", Timezone: "UTC",
		DefaultProfilePictureSeed: "0000000000000000000000000000000000000000000000000000000000000000"}
	events := []string{}
	auditor := &mutationAttemptAuditorFake{events: &events, beginID: model.NewAuditEventID().String()}
	tokens := &accountTokenStoreFake{}
	application := newAccountTokenTestApp(t, accountTokenTestDependencies{
		users: &accountTokenUserStoreFake{byID: user}, passwords: &accountTokenPasswordStoreFake{}, tokens: tokens,
		institution: &model.Institution{ID: model.NewInstitutionID()}, mailer: &emailTransitionMailerFake{}, now: func() time.Time { return now },
	})
	application.accountTokenService.administration = userEmailAdministration{
		authorization: &userProfileAuthorizerFake{events: &events}, audit: auditor, recentAuthenticationTTL: 15 * time.Minute,
	}
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID: model.PrincipalCredentialID(model.NewID()), CredentialType: model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationMultiFactor,
		AuthenticatedAt: now, MFACompletedAt: model.OptionalTimeFrom(now), ClientType: model.SessionClientWeb}

	_, err := application.ChangeUserEmail(context.Background(), invocation.New(principal, model.RequestMetadata{}),
		ChangeUserEmailCommand{UserID: user.ID, Email: "new-private@example.edu"})
	require.NoError(t, err)
	encoded, err := json.Marshal(map[string]any{"parameters": auditor.attempt.Value, "prior": auditor.attempt.Prior})
	require.NoError(t, err)
	for _, mailbox := range []string{"old-private@example.edu", "new-private@example.edu"} {
		require.NotContains(t, string(encoded), mailbox, "audit projection exposed mailbox")
	}
}

func TestEmailChangePreparesFrozenOldWarningAndNewTargetVerification(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	user := &model.User{ID: model.NewUserID(), CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour), Revision: 4,
		Username: "student", Email: "old@example.edu", EmailVerified: true, DisplayName: "Student", Locale: "en", Timezone: "UTC",
		DefaultProfilePictureSeed: "0000000000000000000000000000000000000000000000000000000000000000"}
	tokens := &accountTokenStoreFake{}
	mailer := &emailTransitionMailerFake{}
	app := newAccountTokenTestApp(t, accountTokenTestDependencies{users: &accountTokenUserStoreFake{byID: user}, passwords: &accountTokenPasswordStoreFake{},
		tokens: tokens, institution: &model.Institution{ID: model.NewInstitutionID()}, mailer: mailer, now: func() time.Time { return now }})
	result, err := app.accountTokenService.changeUserEmail(context.Background(), user, "new@example.edu", now, mutationAttemptReference{ID: model.NewAuditEventID(), MutationAt: now})
	require.NoError(t, err)
	require.Equal(t, "new@example.edu", result.Email, "result/aggregate = %#v", result)
	require.NotNil(t, tokens.emailChange, "result/aggregate = %#v", result)
	require.Equal(t, "new@example.edu", tokens.emailChange.Token.Target, "result/aggregate = %#v", result)
	require.Equal(t, 24*time.Hour, tokens.emailChange.TokenLifetime, "result/aggregate = %#v", result)
	require.Equal(t, 72*time.Hour, tokens.emailChange.WarningLifetime, "result/aggregate = %#v", result)
	undo := tokens.emailChange.UndoToken
	require.NotNil(t, undo)
	require.Equal(t, model.UserTokenEmailChangeUndo, undo.Purpose)
	require.Equal(t, "old@example.edu", undo.Target, "the undo token must restore the previous address")
	require.Equal(t, model.HashToken(accountTokenTestRawToken), undo.TokenHash, "the undo token must be stored only as a hash")
	require.Equal(t, now.Add(72*time.Hour), undo.ExpiresAt)
	require.Len(t, mailer.requests, 2)
	require.Equal(t, "old@example.edu", mailer.requests[0].Recipient.Email)
	require.Equal(t, model.MailTemplateIdentityEmailChangeWarningOld, mailer.requests[0].TemplateKey)
	require.Equal(t, model.MailOccurrenceID(undo.ID.String()), mailer.requests[0].OccurrenceID)
	require.Equal(t, "https://proctor.example.edu/account/undo-email-change#token="+accountTokenTestRawToken, mailer.requests[0].ActionURL)
	require.Equal(t, undo.ExpiresAt, mailer.requests[0].Deadline)
	require.Equal(t, "new@example.edu", mailer.requests[1].Recipient.Email)
	require.Equal(t, model.MailTemplateIdentityEmailChangeVerifyNew, mailer.requests[1].TemplateKey)
	require.NotEmpty(t, mailer.requests[1].ActionURL)
	user.Email = "later@example.edu"
	require.Equal(t, "old@example.edu", mailer.requests[0].Recipient.Email, "prepared recipients followed a later profile mutation")
	require.Equal(t, "new@example.edu", mailer.requests[1].Recipient.Email, "prepared recipients followed a later profile mutation")
}

type emailChangeUndoStoreFake struct {
	accountTokenStoreFake
	input  *store.EmailChangeUndo
	result *store.EmailChangeUndoResult
	err    error
}

func (s *emailChangeUndoStoreFake) ConsumeEmailChangeUndo(_ context.Context, input *store.EmailChangeUndo) (*store.EmailChangeUndoResult, error) {
	s.input = input
	return s.result, s.err
}

func TestEmailChangeUndoRestoresThroughTheStoreAndRevokesAfterCommit(t *testing.T) {
	userID := model.NewUserID()
	revoked := &model.Session{ID: model.NewSessionID(), UserID: userID}
	tokens := &emailChangeUndoStoreFake{result: &store.EmailChangeUndoResult{User: &model.User{ID: userID},
		RevokedSessions: []*model.Session{revoked}, RevokedAccessHashes: []string{"access-hash"}}}
	var effects []model.SessionID
	institution := &model.Institution{ID: model.NewInstitutionID()}
	application := newAccountTokenTestApp(t, accountTokenTestDependencies{users: &accountTokenUserStoreFake{}, passwords: &accountTokenPasswordStoreFake{},
		tokens: tokens, institution: institution, mailer: &accountTokenMailerFake{enabled: true},
		effects: &accountTokenEffectsFake{called: func(_ model.UserID, ids []model.SessionID, _ []string) { effects = ids }}})

	err := application.CompleteEmailChangeUndo(context.Background(), invocation.Invocation{}, CompleteEmailChangeUndoCommand{Token: accountTokenTestRawToken, Source: "192.0.2.10"})
	require.NoError(t, err)
	require.Equal(t, model.HashToken(accountTokenTestRawToken), tokens.input.TokenHash)
	require.Equal(t, model.SessionRevocationEmailChangeUndone, tokens.input.RevocationReason)
	require.Equal(t, string(model.ActionAuthenticationEmailChangeUndo), tokens.input.AuditEvent.Action)
	require.Equal(t, institution.ID.String(), tokens.input.AuditEvent.ScopeID)
	require.Equal(t, []model.SessionID{revoked.ID}, effects)
}

func TestEmailChangeUndoConcealsUnknownTokensAndNamesAnAddressConflict(t *testing.T) {
	for name, tc := range map[string]struct {
		token string
		err   error
		code  string
	}{
		"malformed token": {token: "short", code: "authentication.account_token.invalid"},
		"unknown token":   {token: accountTokenTestRawToken, err: store.NewErrNotFound("user_token", ""), code: "authentication.account_token.invalid"},
		"address in use":  {token: accountTokenTestRawToken, err: store.NewErrConflict("user", "email_change_undo_target", nil), code: "authentication.email_change_undo.conflict"},
		"store failure":   {token: accountTokenTestRawToken, err: errors.New("database unavailable"), code: "authentication.account_recovery.unavailable"},
	} {
		t.Run(name, func(t *testing.T) {
			tokens := &emailChangeUndoStoreFake{err: tc.err}
			application := newAccountTokenTestApp(t, accountTokenTestDependencies{users: &accountTokenUserStoreFake{}, passwords: &accountTokenPasswordStoreFake{},
				tokens: tokens, institution: &model.Institution{ID: model.NewInstitutionID()}, mailer: &accountTokenMailerFake{enabled: true}})
			err := application.CompleteEmailChangeUndo(context.Background(), invocation.Invocation{}, CompleteEmailChangeUndoCommand{Token: tc.token, Source: "192.0.2.11"})
			require.Equal(t, tc.code, apperror.CodeOf(err, ""))
		})
	}
}

func TestEmailChangeUndoAndNoticeAreRateLimited(t *testing.T) {
	application := newAccountTokenTestApp(t, accountTokenTestDependencies{users: &accountTokenUserStoreFake{}, passwords: &accountTokenPasswordStoreFake{},
		tokens: &emailChangeUndoStoreFake{err: store.NewErrNotFound("user_token", "")}, institution: &model.Institution{ID: model.NewInstitutionID()},
		mailer: &accountTokenMailerFake{enabled: true}, rateLimit: LoginRateLimitPolicy{Window: time.Minute, MaximumAttempts: 2, MaximumSourceAttempts: 100}})
	command := CompleteEmailChangeUndoCommand{Token: accountTokenTestRawToken, Source: "192.0.2.12"}
	for range 2 {
		err := application.CompleteEmailChangeUndo(context.Background(), invocation.Invocation{}, command)
		require.Equal(t, "authentication.account_token.invalid", apperror.CodeOf(err, ""))
	}
	err := application.CompleteEmailChangeUndo(context.Background(), invocation.Invocation{}, command)
	require.Equal(t, "authentication.rate_limited", apperror.CodeOf(err, ""))

	now := time.Unix(100, 0).UTC()
	user := &model.User{ID: model.NewUserID(), CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour), Revision: 4,
		Username: "student", Email: "old@example.edu", EmailVerified: true, DisplayName: "Student", Locale: "en", Timezone: "UTC",
		DefaultProfilePictureSeed: "0000000000000000000000000000000000000000000000000000000000000000"}
	events := []string{}
	changes := newAccountTokenTestApp(t, accountTokenTestDependencies{users: &accountTokenUserStoreFake{byID: user}, passwords: &accountTokenPasswordStoreFake{},
		tokens: &accountTokenStoreFake{}, institution: &model.Institution{ID: model.NewInstitutionID()}, mailer: &emailTransitionMailerFake{},
		now: func() time.Time { return now }, rateLimit: LoginRateLimitPolicy{Window: time.Minute, MaximumAttempts: 1, MaximumSourceAttempts: 100}})
	changes.accountTokenService.administration = userEmailAdministration{
		authorization: &userProfileAuthorizerFake{events: &events}, audit: &mutationAttemptAuditorFake{events: &events, beginID: model.NewAuditEventID().String()},
		recentAuthenticationTTL: 15 * time.Minute,
	}
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID: model.PrincipalCredentialID(model.NewID()), CredentialType: model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationMultiFactor,
		AuthenticatedAt: now, MFACompletedAt: model.OptionalTimeFrom(now), ClientType: model.SessionClientWeb}
	_, err = changes.ChangeUserEmail(context.Background(), invocation.New(principal, model.RequestMetadata{IPAddress: "192.0.2.13"}),
		ChangeUserEmailCommand{UserID: user.ID, Email: "first@example.edu"})
	require.NoError(t, err)
	_, err = changes.ChangeUserEmail(context.Background(), invocation.New(principal, model.RequestMetadata{IPAddress: "192.0.2.13"}),
		ChangeUserEmailCommand{UserID: user.ID, Email: "second@example.edu"})
	require.Equal(t, "authentication.rate_limited", apperror.CodeOf(err, ""), "a second change notice inside the window")
}

type emailTransitionMailerFake struct{ requests []DirectMailPreparation }

func (*emailTransitionMailerFake) Enabled() bool { return true }
func (m *emailTransitionMailerFake) prepare(request DirectMailPreparation) (*store.PreparedMail, error) {
	copyRecipient := *request.Recipient
	request.Recipient = &copyRecipient
	m.requests = append(m.requests, request)
	return &store.PreparedMail{Occurrence: &model.MailOccurrence{ID: request.OccurrenceID, Kind: request.Kind, TemplateKey: request.TemplateKey, ActorUserID: request.Recipient.ID, CreatedAt: request.At},
		Delivery: &model.MailDelivery{TargetUserID: request.Recipient.ID, TemplateKey: request.TemplateKey, Deadline: request.Deadline}, Job: &model.Job{Type: request.JobType}}, nil
}

func (m *emailTransitionMailerFake) PrepareEmailChangeWarning(request AccountTokenMailPreparation) (*store.PreparedMail, error) {
	return m.prepare(DirectMailPreparation{Recipient: request.Recipient, OccurrenceID: request.OccurrenceID,
		Kind: model.MailOccurrenceAccountToken, TemplateKey: model.MailTemplateIdentityEmailChangeWarningOld,
		ActionURL: request.ActionURL, At: request.At, Deadline: request.Deadline, JobType: model.JobTypeMailDeliverCredential})
}
func (m *emailTransitionMailerFake) PrepareEmailChangeVerification(request AccountTokenMailPreparation) (*store.PreparedMail, error) {
	return m.prepare(DirectMailPreparation{Recipient: request.Recipient, OccurrenceID: request.OccurrenceID,
		Kind: model.MailOccurrenceAccountToken, TemplateKey: model.MailTemplateIdentityEmailChangeVerifyNew,
		ActionURL: request.ActionURL, At: request.At, Deadline: request.Deadline, JobType: model.JobTypeMailDeliverCredential})
}
func (m *emailTransitionMailerFake) PrepareEmailVerifiedByAdministrator(request NoticeMailPreparation) (*store.PreparedMail, error) {
	return m.prepare(DirectMailPreparation{Recipient: request.Recipient, OccurrenceID: model.NewMailOccurrenceID(),
		Kind: model.MailOccurrenceSecurityNotice, TemplateKey: model.MailTemplateIdentityEmailVerifiedByAdmin,
		At: request.At, Deadline: request.At.Add(24 * time.Hour), JobType: model.JobTypeMailDeliver})
}
func (m *emailTransitionMailerFake) PrepareEmailVerification(AccountTokenMailPreparation) (*store.PreparedMail, error) {
	return nil, errors.New("unexpected email verification")
}
func (m *emailTransitionMailerFake) PreparePasswordReset(AccountTokenMailPreparation) (*store.PreparedMail, error) {
	return nil, errors.New("unexpected password reset")
}
func (m *emailTransitionMailerFake) PreparePasswordChanged(NoticeMailPreparation) (*store.PreparedMail, error) {
	return nil, errors.New("unexpected password change")
}
