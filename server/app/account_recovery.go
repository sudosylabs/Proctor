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
	"net/http"
	"net/url"
	"strings"
	"time"

	mailpkg "github.com/sudosylabs/proctor/packages/mail"
	"github.com/sudosylabs/proctor/server/mlog"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/platform"
	"github.com/sudosylabs/proctor/server/store"
)

const (
	auditEmailVerificationRequest  = "authentication.email_verification.request"
	auditEmailVerificationComplete = "authentication.email_verification.complete"
	auditPasswordResetRequest      = "authentication.password_reset.request"
	auditPasswordResetComplete     = "authentication.password_reset.complete"
)

func (a *App) RequestEmailVerification(
	ctx context.Context,
	principal model.Principal,
	metadata model.RequestMetadata,
	source string,
) *model.AppError {
	if !a.Mailer().Enabled() {
		return accountRecoveryUnavailableError(
			"RequestEmailVerification",
			platform.ErrMailDisabled,
		)
	}
	if appErr := a.checkAccountRecoveryRateLimit(
		ctx, "email-verification-request", principal.UserId, source,
	); appErr != nil {
		return appErr
	}
	user, err := a.Store().User().Get(ctx, principal.UserId)
	if err != nil {
		return accountRecoveryStoreError("RequestEmailVerification.user", err)
	}
	if !user.IsActive() {
		return invalidTokenError("RequestEmailVerification.user")
	}
	if user.EmailVerified {
		return nil
	}
	institution, appErr := a.accountRecoveryInstitution(ctx)
	if appErr != nil {
		return appErr
	}
	rawToken := model.NewCredentialToken()
	now := a.authentication.now()
	token := &model.UserToken{
		UserId:    user.Id,
		Purpose:   model.UserTokenEmailVerification,
		TokenHash: model.HashToken(rawToken),
		Target:    user.Email,
		ExpiresAt: now.Add(
			a.Config().Authentication.AccountRecovery.EmailVerificationTTL.Duration,
		).UnixMilli(),
	}
	event := recoveryAuditEvent(
		auditEmailVerificationRequest,
		model.Resource{Type: model.ResourceUser, Id: user.Id},
		institution.Id,
		metadata,
		a.Cluster().NodeID(),
		&principal,
		"session",
	)
	if _, err := a.Store().UserToken().Issue(ctx, token, event); err != nil {
		return accountRecoveryStoreError("RequestEmailVerification.issue", err)
	}
	link, err := accountCredentialLink(
		a.Config().Server.PublicURL,
		"/account/verify-email",
		rawToken,
	)
	if err != nil {
		return accountRecoveryUnavailableError("RequestEmailVerification.link", err)
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
		return accountRecoveryUnavailableError("RequestEmailVerification.send", err)
	}
	return nil
}

// RequestPasswordReset deliberately returns success for unknown, disabled, or
// external-only accounts and for per-account persistence/delivery failures.
// Operational failures are logged without the requested email or raw token.
func (a *App) RequestPasswordReset(
	ctx context.Context,
	email string,
	metadata model.RequestMetadata,
	source string,
) *model.AppError {
	if !a.Mailer().Enabled() {
		return accountRecoveryUnavailableError(
			"RequestPasswordReset",
			platform.ErrMailDisabled,
		)
	}
	normalizedEmail := strings.ToLower(strings.TrimSpace(model.SanitizeUnicode(email)))
	if appErr := a.checkAccountRecoveryRateLimit(
		ctx, "password-reset-request", normalizedEmail, source,
	); appErr != nil {
		return appErr
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
	institution, appErr := a.accountRecoveryInstitution(ctx)
	if appErr != nil {
		a.logHiddenRecoveryFailure(ctx, "password reset institution lookup failed", appErr)
		return nil
	}
	rawToken := model.NewCredentialToken()
	now := a.authentication.now()
	token := &model.UserToken{
		UserId:    user.Id,
		Purpose:   model.UserTokenPasswordReset,
		TokenHash: model.HashToken(rawToken),
		Target:    user.Email,
		ExpiresAt: now.Add(
			a.Config().Authentication.AccountRecovery.PasswordResetTTL.Duration,
		).UnixMilli(),
	}
	event := recoveryAuditEvent(
		auditPasswordResetRequest,
		model.Resource{Type: model.ResourceUser, Id: user.Id},
		institution.Id,
		metadata,
		a.Cluster().NodeID(),
		nil,
		"anonymous",
	)
	if _, err := a.Store().UserToken().Issue(ctx, token, event); err != nil {
		a.logHiddenRecoveryFailure(ctx, "password reset token issue failed", err)
		return nil
	}
	link, err := accountCredentialLink(
		a.Config().Server.PublicURL,
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
	rawToken string,
	metadata model.RequestMetadata,
	source string,
) (*model.User, *model.AppError) {
	if appErr := a.checkAccountRecoveryRateLimit(
		ctx,
		"email-verification-complete",
		recoveryCredentialRateIdentity(rawToken),
		source,
	); appErr != nil {
		return nil, appErr
	}
	if !validRawCredential(rawToken) {
		return nil, invalidAccountCredentialError("CompleteEmailVerification")
	}
	institution, appErr := a.accountRecoveryInstitution(ctx)
	if appErr != nil {
		return nil, appErr
	}
	event := recoveryAuditEvent(
		auditEmailVerificationComplete,
		model.Resource{Type: model.ResourceUser},
		institution.Id,
		metadata,
		a.Cluster().NodeID(),
		nil,
		"email_verification_token",
	)
	result, err := a.Store().UserToken().ConsumeEmailVerification(
		ctx,
		model.HashToken(rawToken),
		a.authentication.now().UnixMilli(),
		event,
	)
	if err != nil {
		if store.IsNotFound(err) {
			return nil, invalidAccountCredentialError("CompleteEmailVerification")
		}
		return nil, accountRecoveryStoreError("CompleteEmailVerification", err)
	}
	return result.User, nil
}

func (a *App) CompletePasswordReset(
	ctx context.Context,
	rawToken string,
	password string,
	metadata model.RequestMetadata,
	source string,
) (*model.User, *model.AppError) {
	if appErr := a.checkAccountRecoveryRateLimit(
		ctx,
		"password-reset-complete",
		recoveryCredentialRateIdentity(rawToken),
		source,
	); appErr != nil {
		return nil, appErr
	}
	if !validRawCredential(rawToken) {
		return nil, invalidAccountCredentialError("CompletePasswordReset")
	}
	passwordHash, err := a.authentication.hasher.Hash(password)
	if err != nil {
		return nil, model.NewAppError(
			"CompletePasswordReset",
			"authentication.password.invalid",
			nil,
			"",
			http.StatusBadRequest,
		).WithSafeFields(map[string]string{"field": "password"})
	}
	institution, appErr := a.accountRecoveryInstitution(ctx)
	if appErr != nil {
		return nil, appErr
	}
	event := recoveryAuditEvent(
		auditPasswordResetComplete,
		model.Resource{Type: model.ResourceUser},
		institution.Id,
		metadata,
		a.Cluster().NodeID(),
		nil,
		"password_reset_token",
	)
	result, err := a.Store().UserToken().ConsumePasswordReset(
		ctx,
		model.HashToken(rawToken),
		passwordHash,
		a.authentication.now().UnixMilli(),
		"password reset",
		event,
	)
	if err != nil {
		if store.IsNotFound(err) {
			return nil, invalidAccountCredentialError("CompletePasswordReset")
		}
		return nil, accountRecoveryStoreError("CompletePasswordReset", err)
	}
	a.authentication.deleteAuthenticationCache(ctx, result.RevokedAccessHashes)
	for _, session := range result.RevokedSessions {
		a.authentication.deleteActivityCache(ctx, session.Id)
	}
	return result.User, nil
}

func (a *App) accountRecoveryInstitution(
	ctx context.Context,
) (*model.Institution, *model.AppError) {
	institution, err := a.Store().Institution().GetSingleton(ctx)
	if err != nil {
		return nil, accountRecoveryStoreError("accountRecoveryInstitution", err)
	}
	return institution, nil
}

func (a *App) checkAccountRecoveryRateLimit(
	ctx context.Context,
	operation string,
	identity string,
	source string,
) *model.AppError {
	settings := a.Config().Authentication.AccountRecovery.RateLimit
	identityKey := "authentication/recovery/" + operation + "/identity/" +
		digestCacheKey(strings.ToLower(strings.TrimSpace(identity)))
	sourceKey := "authentication/recovery/" + operation + "/source/" +
		digestCacheKey(normalizeLoginSource(source))
	identityCount, err := a.Cache().Add(
		ctx, identityKey, 1, settings.Window.Duration,
	)
	if err != nil {
		return rateLimitUnavailableError(
			"checkAccountRecoveryRateLimit.identity",
			err,
		)
	}
	sourceCount, err := a.Cache().Add(
		ctx, sourceKey, 1, settings.Window.Duration,
	)
	if err != nil {
		return rateLimitUnavailableError(
			"checkAccountRecoveryRateLimit.source",
			err,
		)
	}
	if identityCount > int64(settings.MaximumAttempts) ||
		sourceCount > int64(settings.MaximumSourceAttempts) {
		return model.NewAppError(
			"checkAccountRecoveryRateLimit",
			"authentication.rate_limited",
			nil,
			"",
			http.StatusTooManyRequests,
		)
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
	_, err := a.Mailer().Send(ctx, mailpkg.Message{
		To: []mailpkg.Address{{
			Name: user.DisplayName, Address: user.Email,
		}},
		Subject: subject,
		Text:    textBody,
		HTML:    htmlBody,
		Date:    now,
	})
	return err
}

func (a *App) logHiddenRecoveryFailure(
	ctx context.Context,
	message string,
	err error,
) {
	a.Log().ErrorContext(
		ctx,
		message,
		mlog.Err(err),
	)
}

func invalidAccountCredentialError(where string) *model.AppError {
	return model.NewAppError(
		where,
		"authentication.account_token.invalid",
		nil,
		"",
		http.StatusBadRequest,
	)
}

func accountRecoveryStoreError(where string, err error) *model.AppError {
	return model.NewAppError(
		where,
		"authentication.account_recovery.unavailable",
		nil,
		"",
		http.StatusInternalServerError,
	).Wrap(err)
}

func accountRecoveryUnavailableError(where string, err error) *model.AppError {
	return model.NewAppError(
		where,
		"authentication.account_recovery.unavailable",
		nil,
		"",
		http.StatusServiceUnavailable,
	).Wrap(err)
}
