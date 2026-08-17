// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// Adapted from Mattermost server/channels/app/email/email.go and
// authentication.go. Proctor uses purpose-specific hashed credentials,
// target-bound single-use consumption, generic public recovery responses,
// fail-closed rate limiting, and durable transactional security audits.

package app

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/url"
	"strings"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

const (
	auditEmailVerificationRequest  = "authentication.email_verification.request"
	auditEmailVerificationComplete = "authentication.email_verification.complete"
	auditPasswordResetRequest      = "authentication.password_reset.request"
	auditPasswordResetComplete     = "authentication.password_reset.complete"
)

type accountRecoveryAttemptOperation uint8

const (
	accountRecoveryAttemptEmailVerificationRequest accountRecoveryAttemptOperation = iota + 1
	accountRecoveryAttemptPasswordResetRequest
	accountRecoveryAttemptEmailVerificationComplete
	accountRecoveryAttemptPasswordResetComplete
)

func (operation accountRecoveryAttemptOperation) qualifier() (string, bool) {
	switch operation {
	case accountRecoveryAttemptEmailVerificationRequest:
		return "email-verification-request", true
	case accountRecoveryAttemptPasswordResetRequest:
		return "password-reset-request", true
	case accountRecoveryAttemptEmailVerificationComplete:
		return "email-verification-complete", true
	case accountRecoveryAttemptPasswordResetComplete:
		return "password-reset-complete", true
	default:
		return "", false
	}
}

// RequestEmailVerificationCommand issues a verification token for the caller.
type RequestEmailVerificationCommand struct {
	Source string
}

// CompleteEmailVerificationCommand consumes a verification credential.
type CompleteEmailVerificationCommand struct {
	Token  string
	Source string
}

// RequestPasswordResetCommand issues a password-reset token when eligible.
type RequestPasswordResetCommand struct {
	Email  string
	Source string
}

// CompletePasswordResetCommand consumes a reset token and sets a new password.
type CompletePasswordResetCommand struct {
	Token    string
	Password string
	Source   string
}

func (a *App) RequestEmailVerification(
	ctx context.Context,
	invocation Invocation,
	command RequestEmailVerificationCommand,
) error {
	return a.accountTokens.RequestEmailVerification(ctx, invocation, command)
}

func (a *App) RequestPasswordReset(
	ctx context.Context,
	invocation Invocation,
	command RequestPasswordResetCommand,
) error {
	return a.accountTokens.RequestPasswordReset(ctx, invocation, command)
}

func (a *App) CompleteEmailVerification(
	ctx context.Context,
	invocation Invocation,
	command CompleteEmailVerificationCommand,
) (*model.User, error) {
	return a.accountTokens.CompleteEmailVerification(ctx, invocation, command)
}

func (a *App) CompletePasswordReset(
	ctx context.Context,
	invocation Invocation,
	command CompletePasswordResetCommand,
) (*model.User, error) {
	return a.accountTokens.CompletePasswordReset(ctx, invocation, command)
}

func (s *accountTokenService) RequestEmailVerification(
	ctx context.Context,
	invocation Invocation,
	command RequestEmailVerificationCommand,
) error {
	if !s.mailer.Enabled() {
		return accountRecoveryUnavailable(fmt.Errorf("mail delivery is disabled"))
	}
	principal := invocation.Principal()
	if err := s.checkAccountRecoveryRateLimit(
		ctx,
		accountRecoveryAttemptEmailVerificationRequest,
		principal.UserID.String(),
		command.Source,
	); err != nil {
		return err
	}
	user, err := s.users.Get(ctx, principal.UserID.String())
	if err != nil {
		return accountRecoveryStoreFailure(err)
	}
	if !user.IsActive() {
		return invalidTokenAppError()
	}
	if user.EmailVerified {
		return nil
	}
	institution, err := s.accountRecoveryInstitution(ctx)
	if err != nil {
		return err
	}
	rawToken := s.newToken()
	now := s.now()
	token := &model.UserToken{
		UserID:    user.ID,
		Purpose:   model.UserTokenEmailVerification,
		TokenHash: model.HashToken(rawToken),
		Target:    user.Email,
		ExpiresAt: now.Add(s.policy.EmailVerificationTTL),
	}
	event := s.audit.Success(
		auditEmailVerificationRequest,
		model.Resource{Type: model.ResourceUser, ID: user.ID.String()},
		institution.ID.String(),
		invocation,
		&principal,
		"session",
	)
	if _, err := s.tokens.Issue(ctx, token, event); err != nil {
		return accountRecoveryStoreFailure(err)
	}
	link, err := accountCredentialLink(
		s.publicURL,
		"/account/verify-email",
		rawToken,
	)
	if err != nil {
		return accountRecoveryUnavailable(err)
	}
	if err := s.sendAccountCredentialMail(
		ctx,
		user,
		"Verify your Proctor email address",
		"Verify this email address for your Proctor account:\n\n"+link+
			"\n\nIf you did not request this message, you can ignore it.",
		"<p>Verify this email address for your Proctor account:</p>"+
			`<p><a href="`+html.EscapeString(link)+`">Verify email address</a></p>`+
			"<p>If you did not request this message, you can ignore it.</p>",
		now,
	); err != nil {
		return accountRecoveryUnavailable(err)
	}
	return nil
}

// RequestPasswordReset deliberately returns success for unknown, disabled, or
// external-only accounts and for per-account persistence/delivery failures.
// Operational failures are logged without the requested email or raw token.
func (s *accountTokenService) RequestPasswordReset(
	ctx context.Context,
	invocation Invocation,
	command RequestPasswordResetCommand,
) error {
	localLoginAllowed, err := s.accessPolicy.AllowsLocalLogin(ctx)
	if err != nil {
		s.logHiddenRecoveryFailure(ctx, "password reset access policy lookup failed", err)
		return nil
	}
	if !localLoginAllowed {
		return nil
	}
	if !s.mailer.Enabled() {
		return accountRecoveryUnavailable(fmt.Errorf("mail delivery is disabled"))
	}
	normalizedEmail := strings.ToLower(strings.TrimSpace(model.SanitizeUnicode(command.Email)))
	if err := s.checkAccountRecoveryRateLimit(
		ctx,
		accountRecoveryAttemptPasswordResetRequest,
		normalizedEmail,
		command.Source,
	); err != nil {
		return err
	}
	if !model.IsValidEmail(normalizedEmail) {
		return nil
	}
	user, err := s.users.GetByEmail(ctx, normalizedEmail)
	if err != nil {
		if !store.IsNotFound(err) {
			s.logHiddenRecoveryFailure(ctx, "password reset user lookup failed", err)
		}
		return nil
	}
	if !user.IsActive() {
		return nil
	}
	if _, err := s.passwords.GetByUser(ctx, user.ID.String()); err != nil {
		if !store.IsNotFound(err) {
			s.logHiddenRecoveryFailure(ctx, "password credential lookup failed", err)
		}
		return nil
	}
	institution, err := s.accountRecoveryInstitution(ctx)
	if err != nil {
		s.logHiddenRecoveryFailure(ctx, "password reset institution lookup failed", err)
		return nil
	}
	rawToken := s.newToken()
	now := s.now()
	token := &model.UserToken{
		UserID:    user.ID,
		Purpose:   model.UserTokenPasswordReset,
		TokenHash: model.HashToken(rawToken),
		Target:    user.Email,
		ExpiresAt: now.Add(s.policy.PasswordResetTTL),
	}
	event := s.audit.Success(
		auditPasswordResetRequest,
		model.Resource{Type: model.ResourceUser, ID: user.ID.String()},
		institution.ID.String(),
		invocation,
		nil,
		"anonymous",
	)
	if _, err := s.tokens.Issue(ctx, token, event); err != nil {
		s.logHiddenRecoveryFailure(ctx, "password reset token issue failed", err)
		return nil
	}
	link, err := accountCredentialLink(
		s.publicURL,
		"/account/reset-password",
		rawToken,
	)
	if err != nil {
		s.logHiddenRecoveryFailure(ctx, "password reset link generation failed", err)
		return nil
	}
	if err := s.sendAccountCredentialMail(
		ctx,
		user,
		"Reset your Proctor password",
		"Reset your Proctor password using this link:\n\n"+link+
			"\n\nIf you did not request a reset, you can ignore this message.",
		"<p>Reset your Proctor password using this link:</p>"+
			`<p><a href="`+html.EscapeString(link)+`">Reset password</a></p>`+
			"<p>If you did not request a reset, you can ignore this message.</p>",
		now,
	); err != nil {
		s.logHiddenRecoveryFailure(ctx, "password reset delivery failed", err)
	}
	return nil
}

func (s *accountTokenService) CompleteEmailVerification(
	ctx context.Context,
	invocation Invocation,
	command CompleteEmailVerificationCommand,
) (*model.User, error) {
	if err := s.checkAccountRecoveryRateLimit(
		ctx,
		accountRecoveryAttemptEmailVerificationComplete,
		recoveryCredentialRateIdentity(command.Token),
		command.Source,
	); err != nil {
		return nil, err
	}
	if !validRawCredential(command.Token) {
		return nil, invalidAccountCredential()
	}
	institution, err := s.accountRecoveryInstitution(ctx)
	if err != nil {
		return nil, err
	}
	event := s.audit.Success(
		auditEmailVerificationComplete,
		model.Resource{Type: model.ResourceUser},
		institution.ID.String(),
		invocation,
		nil,
		"email_verification_token",
	)
	result, err := s.tokens.ConsumeEmailVerification(
		ctx,
		model.HashToken(command.Token),
		s.now().UnixMilli(),
		event,
	)
	if err != nil {
		if store.IsNotFound(err) {
			return nil, invalidAccountCredential()
		}
		return nil, accountRecoveryStoreFailure(err)
	}
	return result.User, nil
}

func (s *accountTokenService) CompletePasswordReset(
	ctx context.Context,
	invocation Invocation,
	command CompletePasswordResetCommand,
) (*model.User, error) {
	if err := s.checkAccountRecoveryRateLimit(
		ctx,
		accountRecoveryAttemptPasswordResetComplete,
		recoveryCredentialRateIdentity(command.Token),
		command.Source,
	); err != nil {
		return nil, err
	}
	localLoginAllowed, err := s.accessPolicy.AllowsLocalLogin(ctx)
	if err != nil {
		return nil, accountRecoveryStoreFailure(err)
	}
	if !localLoginAllowed {
		return nil, invalidAccountCredential()
	}
	if !validRawCredential(command.Token) {
		return nil, invalidAccountCredential()
	}
	passwordHash, err := s.hasher.Hash(command.Password)
	if err != nil {
		return nil, NewError("authentication.password.invalid").
			WithField("field", "password").
			Wrap(err)
	}
	institution, err := s.accountRecoveryInstitution(ctx)
	if err != nil {
		return nil, err
	}
	event := s.audit.Success(
		auditPasswordResetComplete,
		model.Resource{Type: model.ResourceUser},
		institution.ID.String(),
		invocation,
		nil,
		"password_reset_token",
	)
	result, err := s.tokens.ConsumePasswordReset(
		ctx,
		model.HashToken(command.Token),
		passwordHash,
		s.now().UnixMilli(),
		"password reset",
		event,
	)
	if err != nil {
		if errors.Is(err, store.ErrAuthenticationMethodDisabled) {
			return nil, invalidAccountCredential()
		}
		if store.IsNotFound(err) {
			return nil, invalidAccountCredential()
		}
		return nil, accountRecoveryStoreFailure(err)
	}
	s.effects.SessionsRevoked(
		ctx,
		result.User.ID.String(),
		sessionIds(result.RevokedSessions),
		result.RevokedAccessHashes,
	)
	return result.User, nil
}

func (s *accountTokenService) accountRecoveryInstitution(ctx context.Context) (*model.Institution, error) {
	institution, err := s.institutions.GetSingleton(ctx)
	if err != nil {
		return nil, accountRecoveryStoreFailure(err)
	}
	return institution, nil
}

func (s *accountTokenService) checkAccountRecoveryRateLimit(
	ctx context.Context,
	operation accountRecoveryAttemptOperation,
	identity string,
	source string,
) error {
	qualifier, valid := operation.qualifier()
	if !valid {
		return rateLimitUnavailableAppError(fmt.Errorf("account recovery attempt operation is invalid"))
	}
	settings := s.policy.RateLimit
	_, limited, err := s.attempts.account(ctx, authenticationAttemptIntent{
		purpose:   authenticationAttemptPurposeAccountRecovery,
		qualifier: qualifier,
		window:    settings.Window,
		limits: []authenticationAttemptLimit{
			{
				dimension: authenticationAttemptDimensionIdentity,
				maximum:   settings.MaximumAttempts,
				identity:  identity,
			},
			{
				dimension: authenticationAttemptDimensionSource,
				maximum:   settings.MaximumSourceAttempts,
				source:    source,
			},
		},
	})
	if err != nil {
		return rateLimitUnavailableAppError(err)
	}
	if limited {
		return NewError("authentication.rate_limited")
	}
	return nil
}

func recoveryCredentialRateIdentity(rawToken string) string {
	return model.HashToken(rawToken)
}

func recoveryAuditEvent(
	action string,
	resource model.Resource,
	institutionID string,
	metadata model.RequestMetadata,
	nodeID string,
	principal *model.Principal,
	authenticationMethod string,
) *model.AuditEvent {
	event := &model.AuditEvent{
		Action: action, Resource: resource,
		ScopeType: model.RoleScopeInstitution, ScopeID: institutionID,
		Status: model.AuditStatusSuccess, RequestID: metadata.RequestID,
		NodeID: nodeID, AuthMethod: authenticationMethod,
		IPAddress: metadata.IPAddress, UserAgent: metadata.UserAgent,
	}
	if principal != nil {
		event.ActorID = principal.UserID
		event.SessionID = principal.SessionID
		event.ClientType = string(principal.ClientType)
		event.AuthMethod = principal.AuthenticationMethod
	}
	return event
}

func accountCredentialLink(
	publicURL string,
	path string,
	rawToken string,
) (string, error) {
	base, err := url.Parse(publicURL)
	if err != nil || !base.IsAbs() || base.Host == "" {
		return "", fmt.Errorf("invalid public URL")
	}
	base.Path = strings.TrimRight(base.Path, "/") + path
	base.RawPath = ""
	base.RawQuery = ""
	base.Fragment = "token=" + url.QueryEscape(rawToken)
	return base.String(), nil
}

func (s *accountTokenService) sendAccountCredentialMail(
	ctx context.Context,
	user *model.User,
	subject string,
	textBody string,
	htmlBody string,
	now time.Time,
) error {
	return s.mailer.SendCredentialMail(
		ctx,
		user.DisplayName,
		user.Email,
		subject,
		textBody,
		htmlBody,
		now,
	)
}

func (s *accountTokenService) logHiddenRecoveryFailure(
	ctx context.Context,
	message string,
	err error,
) {
	s.diagnostics.ErrorContext(ctx, message, err)
}

func invalidAccountCredential() error {
	return NewError("authentication.account_token.invalid")
}

func accountRecoveryStoreFailure(err error) error {
	return NewError("authentication.account_recovery.unavailable").Wrap(err)
}

func accountRecoveryUnavailable(err error) error {
	return NewError("authentication.account_recovery.unavailable").Wrap(err)
}
