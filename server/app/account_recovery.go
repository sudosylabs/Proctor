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
	if !a.mailer.Enabled() {
		return accountRecoveryUnavailable(fmt.Errorf("mail delivery is disabled"))
	}
	principal := invocation.Principal()
	if err := a.checkAccountRecoveryRateLimit(
		ctx, "email-verification-request", principal.UserId, command.Source,
	); err != nil {
		return err
	}
	user, err := a.Store().User().Get(ctx, principal.UserId)
	if err != nil {
		return accountRecoveryStoreFailure(err)
	}
	if !user.IsActive() {
		return invalidTokenAppError()
	}
	if user.EmailVerified {
		return nil
	}
	institution, err := a.accountRecoveryInstitution(ctx)
	if err != nil {
		return err
	}
	rawToken := model.NewCredentialToken()
	now := a.authentication.now()
	token := &model.UserToken{
		UserId:    user.Id,
		Purpose:   model.UserTokenEmailVerification,
		TokenHash: model.HashToken(rawToken),
		Target:    user.Email,
		ExpiresAt: now.Add(a.accountRecovery.EmailVerificationTTL).UnixMilli(),
	}
	metadata := invocation.RequestMetadata()
	event := recoveryAuditEvent(
		auditEmailVerificationRequest,
		model.Resource{Type: model.ResourceUser, Id: user.Id},
		institution.ID.String(),
		metadata,
		a.nodeID,
		&principal,
		"session",
	)
	if _, err := a.Store().UserToken().Issue(ctx, token, event); err != nil {
		return accountRecoveryStoreFailure(err)
	}
	link, err := accountCredentialLink(
		a.publicURL,
		"/account/verify-email",
		rawToken,
	)
	if err != nil {
		return accountRecoveryUnavailable(err)
	}
	if err := a.sendAccountCredentialMail(
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
func (a *App) RequestPasswordReset(
	ctx context.Context,
	invocation Invocation,
	command RequestPasswordResetCommand,
) error {
	if !a.mailer.Enabled() {
		return accountRecoveryUnavailable(fmt.Errorf("mail delivery is disabled"))
	}
	normalizedEmail := strings.ToLower(strings.TrimSpace(model.SanitizeUnicode(command.Email)))
	if err := a.checkAccountRecoveryRateLimit(
		ctx, "password-reset-request", normalizedEmail, command.Source,
	); err != nil {
		return err
	}
	if !model.IsValidEmail(normalizedEmail) {
		return nil
	}
	user, err := a.Store().User().GetByEmail(ctx, normalizedEmail)
	if err != nil {
		if !store.IsNotFound(err) {
			a.logHiddenRecoveryFailure(ctx, "password reset user lookup failed", err)
		}
		return nil
	}
	if !user.IsActive() {
		return nil
	}
	if _, err := a.Store().PasswordCredential().GetByUser(ctx, user.Id); err != nil {
		if !store.IsNotFound(err) {
			a.logHiddenRecoveryFailure(ctx, "password credential lookup failed", err)
		}
		return nil
	}
	institution, err := a.accountRecoveryInstitution(ctx)
	if err != nil {
		a.logHiddenRecoveryFailure(ctx, "password reset institution lookup failed", err)
		return nil
	}
	rawToken := model.NewCredentialToken()
	now := a.authentication.now()
	token := &model.UserToken{
		UserId:    user.Id,
		Purpose:   model.UserTokenPasswordReset,
		TokenHash: model.HashToken(rawToken),
		Target:    user.Email,
		ExpiresAt: now.Add(a.accountRecovery.PasswordResetTTL).UnixMilli(),
	}
	metadata := invocation.RequestMetadata()
	event := recoveryAuditEvent(
		auditPasswordResetRequest,
		model.Resource{Type: model.ResourceUser, Id: user.Id},
		institution.ID.String(),
		metadata,
		a.nodeID,
		nil,
		"anonymous",
	)
	if _, err := a.Store().UserToken().Issue(ctx, token, event); err != nil {
		a.logHiddenRecoveryFailure(ctx, "password reset token issue failed", err)
		return nil
	}
	link, err := accountCredentialLink(
		a.publicURL,
		"/account/reset-password",
		rawToken,
	)
	if err != nil {
		a.logHiddenRecoveryFailure(ctx, "password reset link generation failed", err)
		return nil
	}
	if err := a.sendAccountCredentialMail(
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
		a.logHiddenRecoveryFailure(ctx, "password reset delivery failed", err)
	}
	return nil
}

func (a *App) CompleteEmailVerification(
	ctx context.Context,
	invocation Invocation,
	command CompleteEmailVerificationCommand,
) (*model.User, error) {
	if err := a.checkAccountRecoveryRateLimit(
		ctx,
		"email-verification-complete",
		recoveryCredentialRateIdentity(command.Token),
		command.Source,
	); err != nil {
		return nil, err
	}
	if !validRawCredential(command.Token) {
		return nil, invalidAccountCredential()
	}
	institution, err := a.accountRecoveryInstitution(ctx)
	if err != nil {
		return nil, err
	}
	metadata := invocation.RequestMetadata()
	event := recoveryAuditEvent(
		auditEmailVerificationComplete,
		model.Resource{Type: model.ResourceUser},
		institution.ID.String(),
		metadata,
		a.nodeID,
		nil,
		"email_verification_token",
	)
	result, err := a.Store().UserToken().ConsumeEmailVerification(
		ctx,
		model.HashToken(command.Token),
		a.authentication.now().UnixMilli(),
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

func (a *App) CompletePasswordReset(
	ctx context.Context,
	invocation Invocation,
	command CompletePasswordResetCommand,
) (*model.User, error) {
	if err := a.checkAccountRecoveryRateLimit(
		ctx,
		"password-reset-complete",
		recoveryCredentialRateIdentity(command.Token),
		command.Source,
	); err != nil {
		return nil, err
	}
	if !validRawCredential(command.Token) {
		return nil, invalidAccountCredential()
	}
	passwordHash, err := a.authentication.hasher.Hash(command.Password)
	if err != nil {
		return nil, NewError("authentication.password.invalid").
			WithField("field", "password").
			Wrap(err)
	}
	institution, err := a.accountRecoveryInstitution(ctx)
	if err != nil {
		return nil, err
	}
	metadata := invocation.RequestMetadata()
	event := recoveryAuditEvent(
		auditPasswordResetComplete,
		model.Resource{Type: model.ResourceUser},
		institution.ID.String(),
		metadata,
		a.nodeID,
		nil,
		"password_reset_token",
	)
	result, err := a.Store().UserToken().ConsumePasswordReset(
		ctx,
		model.HashToken(command.Token),
		passwordHash,
		a.authentication.now().UnixMilli(),
		"password reset",
		event,
	)
	if err != nil {
		if store.IsNotFound(err) {
			return nil, invalidAccountCredential()
		}
		return nil, accountRecoveryStoreFailure(err)
	}
	a.authentication.deleteAuthenticationCache(ctx, result.RevokedAccessHashes)
	for _, session := range result.RevokedSessions {
		a.authentication.deleteActivityCache(ctx, session.Id)
	}
	a.realtime.PropagateSessionRevocation(
		ctx,
		result.User.Id,
		sessionIds(result.RevokedSessions),
		result.RevokedAccessHashes,
	)
	return result.User, nil
}

func (a *App) accountRecoveryInstitution(ctx context.Context) (*model.Institution, error) {
	institution, err := a.Store().Institution().GetSingleton(ctx)
	if err != nil {
		return nil, accountRecoveryStoreFailure(err)
	}
	return institution, nil
}

func (a *App) checkAccountRecoveryRateLimit(
	ctx context.Context,
	operation string,
	identity string,
	source string,
) error {
	settings := a.accountRecovery.RateLimit
	identityKey := "authentication/recovery/" + operation + "/identity/" +
		digestCacheKey(strings.ToLower(strings.TrimSpace(identity)))
	sourceKey := "authentication/recovery/" + operation + "/source/" +
		digestCacheKey(normalizeLoginSource(source))
	identityCount, err := a.cache.Add(ctx, identityKey, 1, settings.Window)
	if err != nil {
		return rateLimitUnavailableAppError(err)
	}
	sourceCount, err := a.cache.Add(ctx, sourceKey, 1, settings.Window)
	if err != nil {
		return rateLimitUnavailableAppError(err)
	}
	if identityCount > int64(settings.MaximumAttempts) ||
		sourceCount > int64(settings.MaximumSourceAttempts) {
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
		ScopeType: model.RoleScopeInstitution, ScopeId: institutionID,
		Status: model.AuditStatusSuccess, RequestId: metadata.RequestId,
		NodeId: nodeID, AuthMethod: authenticationMethod,
		IPAddress: metadata.IPAddress, UserAgent: metadata.UserAgent,
	}
	if principal != nil {
		event.ActorId = principal.UserId
		event.SessionId = principal.SessionId
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

func (a *App) sendAccountCredentialMail(
	ctx context.Context,
	user *model.User,
	subject string,
	textBody string,
	htmlBody string,
	now time.Time,
) error {
	return a.mailer.SendCredentialMail(
		ctx,
		user.DisplayName,
		user.Email,
		subject,
		textBody,
		htmlBody,
		now,
	)
}

func (a *App) logHiddenRecoveryFailure(
	ctx context.Context,
	message string,
	err error,
) {
	if a.recoveryDiagnostics == nil {
		return
	}
	a.recoveryDiagnostics.ErrorContext(ctx, message, err)
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
