// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"strings"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type ChangeUserEmailCommand struct {
	UserID string
	Email  string
}

type VerifyUserEmailPrivilegedCommand struct{ UserID string }

// UserEmailState is the narrow result of an explicit email-security mutation.
// It deliberately omits the mailbox and unrelated profile/account metadata.
type UserEmailState struct {
	UserID        model.UserID
	EmailVerified bool
}

func userEmailState(user *model.User) *UserEmailState {
	if user == nil {
		return nil
	}
	return &UserEmailState{UserID: user.ID, EmailVerified: user.EmailVerified}
}

func (a *App) ChangeUserEmail(ctx context.Context, invocation Invocation, command ChangeUserEmailCommand) (*UserEmailState, error) {
	if a == nil || a.userProfiles == nil || a.accountTokens == nil {
		return nil, NewError("administration.unavailable")
	}
	if err := requireStrongRecentSession(invocation.Principal(), a.accountTokens.now(), a.recentAuthenticationTTL); err != nil {
		return nil, err
	}
	if err := a.userProfiles.authorization.AuthorizeManage(ctx, invocation, strings.TrimSpace(command.UserID)); err != nil {
		return nil, err
	}
	id := strings.TrimSpace(command.UserID)
	newEmail := strings.ToLower(strings.TrimSpace(model.SanitizeUnicode(command.Email)))
	if !model.IsValidId(id) || !model.IsValidEmail(newEmail) {
		return nil, NewError("user.invalid").WithField("field", "email")
	}
	current, err := a.accountTokens.users.Get(ctx, id)
	if err != nil || current == nil || !current.IsActive() {
		return nil, userProfileError(err)
	}
	if current.Email == newEmail {
		return userEmailState(current), nil
	}
	at := model.TimeUTC(a.accountTokens.now())
	return runAuditedMutation(ctx, a.userProfiles.audit, mutationAttempt{Invocation: invocation, Action: model.ActionUserManage,
		Resource: model.Resource{Type: model.ResourceUser, ID: id}, Operation: "change_email",
		Value: map[string]any{"email_changed": true, "email_verified": false}, Prior: current.Auditable()}, func() time.Time { return at },
		func(ctx context.Context, reference mutationAttemptReference) (*UserEmailState, error) {
			updated, err := a.accountTokens.changeUserEmail(ctx, current, newEmail, at, reference)
			return userEmailState(updated), err
		}, userProfileError)
}

func (a *App) VerifyUserEmailPrivileged(ctx context.Context, invocation Invocation, command VerifyUserEmailPrivilegedCommand) (*UserEmailState, error) {
	if a == nil || a.userProfiles == nil || a.accountTokens == nil {
		return nil, NewError("administration.unavailable")
	}
	if err := requireStrongRecentSession(invocation.Principal(), a.accountTokens.now(), a.recentAuthenticationTTL); err != nil {
		return nil, err
	}
	if err := a.userProfiles.authorization.AuthorizeManage(ctx, invocation, strings.TrimSpace(command.UserID)); err != nil {
		return nil, err
	}
	id := strings.TrimSpace(command.UserID)
	if !model.IsValidId(id) {
		return nil, NewError("request.invalid").WithField("field", "user_id")
	}
	current, err := a.accountTokens.users.Get(ctx, id)
	if err != nil || current == nil || !current.IsActive() {
		return nil, userProfileError(err)
	}
	if current.EmailVerified {
		return userEmailState(current), nil
	}
	at := model.TimeUTC(a.accountTokens.now())
	return runAuditedMutation(ctx, a.userProfiles.audit, mutationAttempt{Invocation: invocation, Action: model.ActionUserManage,
		Resource: model.Resource{Type: model.ResourceUser, ID: id}, Operation: "verify_email_privileged",
		Value: map[string]any{"email_verified": true}, Prior: current.Auditable()}, func() time.Time { return at },
		func(ctx context.Context, reference mutationAttemptReference) (*UserEmailState, error) {
			updated, err := a.accountTokens.verifyUserEmailPrivileged(ctx, current, at, reference)
			return userEmailState(updated), err
		}, userProfileError)
}

func (s *accountTokenService) changeUserEmail(ctx context.Context, current *model.User, newEmail string, now time.Time, reference mutationAttemptReference) (*model.User, error) {
	rawToken := s.newToken()
	token := &model.UserToken{UserID: current.ID, Purpose: model.UserTokenEmailVerification, TokenHash: model.HashToken(rawToken), Target: newEmail, ExpiresAt: now.Add(s.policy.EmailVerificationTTL)}
	token.PrepareCreate(model.NewUserTokenID(), now)
	link, err := accountCredentialLink(s.publicURL, "/account/verify-email", rawToken)
	if err != nil {
		return nil, accountRecoveryUnavailable(err)
	}
	oldRecipient, newRecipient := *current, *current
	newRecipient.Email, newRecipient.EmailVerified = newEmail, false
	warning, err := s.mail.PrepareDirect(DirectMailPreparation{Recipient: &oldRecipient, OccurrenceID: model.NewMailOccurrenceID(), Kind: model.MailOccurrenceSecurityNotice, TemplateKey: model.MailTemplateIdentityEmailChangeWarningOld, At: now, Deadline: now.Add(24 * time.Hour), JobType: model.JobTypeMailDeliver})
	if err != nil {
		return nil, accountRecoveryUnavailable(err)
	}
	verification, err := s.mail.PrepareDirect(DirectMailPreparation{Recipient: &newRecipient, OccurrenceID: model.MailOccurrenceID(token.ID.String()), Kind: model.MailOccurrenceAccountToken, TemplateKey: model.MailTemplateIdentityEmailChangeVerifyNew, ActionURL: link, At: now, Deadline: token.ExpiresAt, JobType: model.JobTypeMailDeliverCredential})
	if err != nil {
		return nil, accountRecoveryUnavailable(err)
	}
	result, err := s.tokens.ChangeEmail(ctx, &store.UserEmailChange{UserID: current.ID, ExpectedRevision: current.Revision, NewEmail: newEmail, Token: token,
		TokenLifetime: s.policy.EmailVerificationTTL, WarningLifetime: 24 * time.Hour,
		WarningOccurrence: warning.Occurrence, WarningDelivery: warning.Delivery, WarningJob: warning.Job,
		VerificationOccurrence: verification.Occurrence, VerificationDelivery: verification.Delivery, VerificationJob: verification.Job,
		AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis})
	if err != nil {
		return nil, userProfileError(err)
	}
	return result.User, nil
}

func (s *accountTokenService) verifyUserEmailPrivileged(ctx context.Context, current *model.User, now time.Time, reference mutationAttemptReference) (*model.User, error) {
	prepared, err := s.mail.PrepareDirect(DirectMailPreparation{Recipient: current, OccurrenceID: model.NewMailOccurrenceID(), Kind: model.MailOccurrenceSecurityNotice, TemplateKey: model.MailTemplateIdentityEmailVerifiedByAdmin, At: now, Deadline: now.Add(24 * time.Hour), JobType: model.JobTypeMailDeliver})
	if err != nil {
		return nil, accountRecoveryUnavailable(err)
	}
	updated, err := s.tokens.VerifyEmailPrivileged(ctx, &store.PrivilegedEmailVerification{UserID: current.ID, ExpectedRevision: current.Revision, Occurrence: prepared.Occurrence, Delivery: prepared.Delivery, Job: prepared.Job,
		AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis})
	if err != nil {
		return nil, err
	}
	return updated, nil
}
