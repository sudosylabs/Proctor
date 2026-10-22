// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"strings"
	"time"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/app/mail"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type ChangeUserEmailCommand struct {
	UserID model.UserID
	Email  string
}

// VerifyUserEmailPrivilegedCommand asks an administrator to mark a User's
// email verified without a verification token. It requires a strong recent
// Session and is a no-op when the email is already verified.
type VerifyUserEmailPrivilegedCommand struct{ UserID model.UserID }

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

// userEmailAdministration carries the policy an administrator email-security
// mutation needs beyond the account-token mechanics. Composition sets it once.
type userEmailAdministration struct {
	authorization           resourceAuthorizer
	audit                   mutationAuditor
	recentAuthenticationTTL time.Duration
}

func (s *accountTokenService) ChangeUserEmail(ctx context.Context, invocation invocation.Invocation, command ChangeUserEmailCommand) (*UserEmailState, error) {
	if s.administration.authorization == nil || s.administration.audit == nil {
		return nil, apperror.New("administration.unavailable")
	}
	if err := requireStrongRecentSession(invocation.Principal(), s.now(), s.administration.recentAuthenticationTTL); err != nil {
		return nil, err
	}
	if err := s.administration.authorization.Authorize(ctx, invocation, model.ActionUserManage, model.Resource{Type: model.ResourceUser, ID: command.UserID.String()}); err != nil {
		return nil, err
	}
	id := command.UserID
	newEmail := strings.ToLower(strings.TrimSpace(model.SanitizeUnicode(command.Email)))
	if !id.IsValid() || !model.IsValidEmail(newEmail) {
		return nil, apperror.New("user.invalid").WithField("field", "email")
	}
	current, err := s.users.Get(ctx, id)
	if err != nil || current == nil || !current.IsActive() {
		return nil, userStoreErrors.Translate(err)
	}
	if current.Email == newEmail {
		return userEmailState(current), nil
	}
	at := model.TimeUTC(s.now())
	return runAuditedMutation(ctx, s.administration.audit, mutationAttempt{Invocation: invocation, Action: model.ActionUserManage,
		Resource: model.Resource{Type: model.ResourceUser, ID: id.String()}, Operation: "change_email",
		Value: map[string]any{"email_changed": true, "email_verified": false}, Prior: current.Auditable()}, func() time.Time { return at },
		func(ctx context.Context, reference mutationAttemptReference) (*UserEmailState, error) {
			updated, err := s.changeUserEmail(ctx, current, newEmail, at, reference)
			return userEmailState(updated), err
		}, userStoreErrors.Translate)
}

func (s *accountTokenService) VerifyUserEmailPrivileged(ctx context.Context, invocation invocation.Invocation, command VerifyUserEmailPrivilegedCommand) (*UserEmailState, error) {
	if s.administration.authorization == nil || s.administration.audit == nil {
		return nil, apperror.New("administration.unavailable")
	}
	if err := requireStrongRecentSession(invocation.Principal(), s.now(), s.administration.recentAuthenticationTTL); err != nil {
		return nil, err
	}
	if err := s.administration.authorization.Authorize(ctx, invocation, model.ActionUserManage, model.Resource{Type: model.ResourceUser, ID: command.UserID.String()}); err != nil {
		return nil, err
	}
	id := command.UserID
	if !id.IsValid() {
		return nil, apperror.New("request.invalid").WithField("field", "user_id")
	}
	current, err := s.users.Get(ctx, id)
	if err != nil || current == nil || !current.IsActive() {
		return nil, userStoreErrors.Translate(err)
	}
	if current.EmailVerified {
		return userEmailState(current), nil
	}
	at := model.TimeUTC(s.now())
	return runAuditedMutation(ctx, s.administration.audit, mutationAttempt{Invocation: invocation, Action: model.ActionUserManage,
		Resource: model.Resource{Type: model.ResourceUser, ID: id.String()}, Operation: "verify_email_privileged",
		Value: map[string]any{"email_verified": true}, Prior: current.Auditable()}, func() time.Time { return at },
		func(ctx context.Context, reference mutationAttemptReference) (*UserEmailState, error) {
			updated, err := s.verifyUserEmailPrivileged(ctx, current, at, reference)
			return userEmailState(updated), err
		}, userStoreErrors.Translate)
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
	warning, err := s.mail.PrepareEmailChangeWarning(mail.NoticePreparation{Recipient: &oldRecipient, At: now})
	if err != nil {
		return nil, accountRecoveryUnavailable(err)
	}
	verification, err := s.mail.PrepareEmailChangeVerification(mail.AccountTokenPreparation{Recipient: &newRecipient,
		OccurrenceID: model.MailOccurrenceID(token.ID.String()), ActionURL: link, At: now, Deadline: token.ExpiresAt})
	if err != nil {
		return nil, accountRecoveryUnavailable(err)
	}
	result, err := s.tokens.ChangeEmail(ctx, &store.UserEmailChange{UserID: current.ID, ExpectedRevision: current.Revision, NewEmail: newEmail, Token: token,
		TokenLifetime: s.policy.EmailVerificationTTL, WarningLifetime: 24 * time.Hour,
		WarningOccurrence: warning.Occurrence, WarningDelivery: warning.Delivery, WarningJob: warning.Job,
		VerificationOccurrence: verification.Occurrence, VerificationDelivery: verification.Delivery, VerificationJob: verification.Job,
		AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis})
	if err != nil {
		return nil, userStoreErrors.Translate(err)
	}
	return result.User, nil
}

func (s *accountTokenService) verifyUserEmailPrivileged(ctx context.Context, current *model.User, now time.Time, reference mutationAttemptReference) (*model.User, error) {
	prepared, err := s.mail.PrepareEmailVerifiedByAdministrator(mail.NoticePreparation{Recipient: current, At: now})
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
