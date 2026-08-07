// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sudosylabs/proctor/server/mlog"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/platform"
	"github.com/sudosylabs/proctor/server/platform/externalauth"
	"github.com/sudosylabs/proctor/server/store"
)

type ExternalAuthenticationService struct {
	platform       *platform.Service
	authentication *AuthenticationService
	audit          *AuditService
	now            func() time.Time
}

func newExternalAuthenticationService(
	applicationPlatform *platform.Service,
	authentication *AuthenticationService,
	audit *AuditService,
) *ExternalAuthenticationService {
	return &ExternalAuthenticationService{
		platform: applicationPlatform, authentication: authentication,
		audit: audit, now: time.Now,
	}
}

func (a *App) ExternalAuthenticationProviders() []model.ExternalAuthenticationProvider {
	return a.externalAuthentication.providers()
}

func (s *ExternalAuthenticationService) providers() []model.ExternalAuthenticationProvider {
	return s.platform.ExternalAuthenticationProviders()
}

func (a *App) BeginExternalAuthentication(
	ctx context.Context,
	providerID string,
	returnTo string,
	clientType model.SessionClientType,
	deviceID string,
	deviceName string,
	source string,
) (*model.ExternalAuthenticationStart, *model.AppError) {
	return a.externalAuthentication.begin(
		ctx,
		providerID,
		returnTo,
		clientType,
		deviceID,
		deviceName,
		source,
	)
}

func (s *ExternalAuthenticationService) begin(
	ctx context.Context,
	providerID string,
	returnTo string,
	clientType model.SessionClientType,
	deviceID string,
	deviceName string,
	source string,
) (*model.ExternalAuthenticationStart, *model.AppError) {
	providerID = strings.ToLower(strings.TrimSpace(providerID))
	provider, exists := s.platform.ExternalAuthenticationProvider(providerID)
	if !exists {
		return nil, externalProviderNotFoundError("BeginExternalAuthentication")
	}
	if returnTo == "" {
		returnTo = "/"
	}
	if !model.IsSafeRelativeURL(returnTo) ||
		!clientType.IsValid() ||
		clientType == model.SessionClientCLI ||
		len(deviceID) > model.SessionDeviceIdMaxLength ||
		utf8.RuneCountInString(deviceName) > model.SessionDeviceNameMaxRunes {
		return nil, model.NewAppError(
			"BeginExternalAuthentication",
			"authentication.external.request.invalid",
			nil,
			"",
			http.StatusBadRequest,
		)
	}
	if appErr := s.checkInitiationRateLimit(ctx, providerID, source); appErr != nil {
		return nil, appErr
	}
	stateToken := model.NewCredentialToken()
	bindingToken := model.NewCredentialToken()
	now := s.now().UnixMilli()
	expiresAt := now + s.platform.Config().
		Authentication.External.LoginStateTTL.Milliseconds()
	callbackURL, err := externalAuthenticationCallbackURL(
		s.platform.Config().Server.PublicURL,
		providerID,
	)
	if err != nil {
		return nil, internalAuthenticationError(
			"BeginExternalAuthentication.callback_url",
			err,
		)
	}
	challenge, err := provider.Begin(
		ctx,
		externalauth.BeginRequest{
			CallbackURL: callbackURL,
			State:       stateToken,
			Proof:       bindingToken,
		},
	)
	if err != nil {
		return nil, externalProviderOperationError(
			"BeginExternalAuthentication.provider_url",
			err,
		)
	}
	if challenge == nil || challenge.RedirectURL == "" {
		return nil, internalAuthenticationError(
			"BeginExternalAuthentication.provider_url",
			errors.New("external provider returned an empty login challenge"),
		)
	}
	stateStore := s.platform.Store().ExternalLoginState()
	if stateStore == nil {
		return nil, internalAuthenticationError(
			"BeginExternalAuthentication.store",
			errors.New("external login state store is unavailable"),
		)
	}
	if _, err := stateStore.Save(ctx, &model.ExternalLoginState{
		Provider: providerID, StateHash: model.HashToken(stateToken),
		BindingHash: model.HashToken(bindingToken), ReturnTo: returnTo,
		ClientType: clientType, DeviceId: deviceID, DeviceName: deviceName,
		ExpiresAt: expiresAt,
	}); err != nil {
		return nil, internalAuthenticationError(
			"BeginExternalAuthentication.save_state",
			err,
		)
	}
	return &model.ExternalAuthenticationStart{
		RedirectURL: challenge.RedirectURL,
		Binding:     bindingToken,
		ExpiresAt:   expiresAt,
	}, nil
}

func (a *App) CompleteExternalAuthentication(
	ctx context.Context,
	providerID string,
	bindingToken string,
	callback model.ExternalAuthenticationCallback,
	metadata model.RequestMetadata,
) (*model.ExternalAuthenticationCompletion, *model.AppError) {
	return a.externalAuthentication.complete(
		ctx,
		providerID,
		bindingToken,
		callback,
		metadata,
	)
}

func (s *ExternalAuthenticationService) complete(
	ctx context.Context,
	providerID string,
	bindingToken string,
	callback model.ExternalAuthenticationCallback,
	metadata model.RequestMetadata,
) (*model.ExternalAuthenticationCompletion, *model.AppError) {
	providerID = strings.ToLower(strings.TrimSpace(providerID))
	provider, exists := s.platform.ExternalAuthenticationProvider(providerID)
	if !exists {
		return nil, externalProviderNotFoundError("CompleteExternalAuthentication")
	}
	method := provider.Descriptor().Type
	stateToken, providerErr := provider.State(callback)
	if providerErr != nil ||
		!validRawCredential(stateToken) ||
		!validRawCredential(bindingToken) {
		return nil, invalidExternalAuthenticationError(
			"CompleteExternalAuthentication.state",
		)
	}
	stateStore := s.platform.Store().ExternalLoginState()
	if stateStore == nil {
		return nil, internalAuthenticationError(
			"CompleteExternalAuthentication.store",
			errors.New("external login state store is unavailable"),
		)
	}
	stateHash := model.HashToken(stateToken)
	state, err := stateStore.GetByStateHash(ctx, stateHash)
	now := s.now().UnixMilli()
	if err != nil || state == nil || state.Provider != providerID ||
		state.ConsumedAt != 0 || state.ExpiresAt <= now {
		if err != nil && !store.IsNotFound(err) {
			return nil, internalAuthenticationError(
				"CompleteExternalAuthentication.get_state",
				err,
			)
		}
		return nil, invalidExternalAuthenticationError(
			"CompleteExternalAuthentication.state",
		)
	}
	callbackURL, err := externalAuthenticationCallbackURL(
		s.platform.Config().Server.PublicURL,
		providerID,
	)
	if err != nil {
		return nil, internalAuthenticationError(
			"CompleteExternalAuthentication.callback_url",
			err,
		)
	}
	state, err = stateStore.Consume(
		ctx,
		providerID,
		stateHash,
		model.HashToken(bindingToken),
		now,
	)
	if err != nil {
		if store.IsNotFound(err) {
			return nil, invalidExternalAuthenticationError(
				"CompleteExternalAuthentication.consume_state",
			)
		}
		return nil, internalAuthenticationError(
			"CompleteExternalAuthentication.consume_state",
			err,
		)
	}

	institution, err := s.platform.Store().Institution().GetSingleton(ctx)
	if err != nil {
		return nil, internalAuthenticationError(
			"CompleteExternalAuthentication.institution",
			err,
		)
	}
	assertion, providerErr := provider.Complete(
		ctx,
		externalauth.CompleteRequest{
			CallbackURL: callbackURL,
			State:       stateToken,
			Proof:       bindingToken,
			Callback:    callback,
		},
	)
	if providerErr != nil {
		errorCode := "authentication.external.rejected"
		status := http.StatusUnauthorized
		if !errors.Is(
			providerErr,
			externalauth.ErrAuthenticationRejected,
		) {
			errorCode = "authentication.external.unavailable"
			status = http.StatusBadGateway
		}
		if appErr := s.audit.RecordExternalAuthenticationFailure(
			ctx,
			providerID,
			method,
			metadata,
			institution.Id,
			errorCode,
		); appErr != nil {
			return nil, appErr
		}
		return nil, model.NewAppError(
			"CompleteExternalAuthentication.provider",
			errorCode,
			nil,
			"",
			status,
		).Wrap(providerErr)
	}
	if assertion == nil || assertion.ProviderId != providerID {
		if auditErr := s.audit.RecordExternalAuthenticationFailure(
			ctx,
			providerID,
			method,
			metadata,
			institution.Id,
			"authentication.external.invalid_assertion",
		); auditErr != nil {
			return nil, auditErr
		}
		return nil, internalAuthenticationError(
			"CompleteExternalAuthentication.assertion",
			errors.New("provider returned a mismatched assertion"),
		)
	}
	userCandidate := externalUserCandidate(assertion)
	provisionParameters, appErr := model.EncodeAuditData(map[string]string{
		"provider": providerID,
	})
	if appErr != nil {
		return nil, appErr
	}
	identityStore := s.platform.Store().ExternalIdentity()
	if identityStore == nil {
		return nil, internalAuthenticationError(
			"CompleteExternalAuthentication.identity_store",
			errors.New("external identity store is unavailable"),
		)
	}
	resolution, err := identityStore.ResolveOrProvision(
		ctx,
		&model.ExternalIdentity{
			Provider: providerID, Subject: assertion.Subject, LastSeenAt: now,
		},
		userCandidate,
		provider.AutoProvision(),
		&model.AuditEvent{
			Action:    "authentication.external_provision",
			ScopeType: model.RoleScopeInstitution,
			ScopeId:   institution.Id, Status: model.AuditStatusSuccess,
			RequestId:  metadata.RequestId,
			NodeId:     s.platform.Cluster().NodeID(),
			ClientType: string(state.ClientType), AuthMethod: method,
			IPAddress: metadata.IPAddress, UserAgent: metadata.UserAgent,
			Parameters: provisionParameters,
		},
	)
	if err != nil {
		var conflict *store.ErrConflict
		errorCode := "authentication.external.internal"
		if errors.As(err, &conflict) {
			errorCode = "authentication.external.account_conflict"
		} else if store.IsNotFound(err) {
			errorCode = "authentication.external.account_not_linked"
		}
		if auditErr := s.audit.RecordExternalAuthenticationFailure(
			ctx,
			providerID,
			method,
			metadata,
			institution.Id,
			errorCode,
		); auditErr != nil {
			return nil, auditErr
		}
		switch {
		case errors.As(err, &conflict):
			return nil, model.NewAppError(
				"CompleteExternalAuthentication.resolve",
				"authentication.external.account_conflict",
				nil,
				"",
				http.StatusConflict,
			).Wrap(err)
		case store.IsNotFound(err):
			return nil, model.NewAppError(
				"CompleteExternalAuthentication.resolve",
				"authentication.external.account_not_linked",
				nil,
				"",
				http.StatusForbidden,
			)
		default:
			return nil, internalAuthenticationError(
				"CompleteExternalAuthentication.resolve",
				err,
			)
		}
	}
	if !resolution.User.IsActive() {
		if appErr := s.audit.RecordExternalAuthenticationFailure(
			ctx,
			providerID,
			method,
			metadata,
			institution.Id,
			"authentication.external.inactive_account",
		); appErr != nil {
			return nil, appErr
		}
		return nil, invalidExternalAuthenticationError(
			"CompleteExternalAuthentication.user",
		)
	}

	auditEvent, appErr := s.audit.BeginAuthentication(
		ctx,
		resolution.User.Id,
		method,
		providerID,
		state.ClientType,
		metadata,
		institution.Id,
	)
	if appErr != nil {
		return nil, appErr
	}
	mfaCompletedAt := int64(0)
	if assertion.AuthenticationStrength == model.AuthenticationMultiFactor {
		mfaCompletedAt = assertion.AuthenticatedAt
	}
	session, tokens, sessionErr := s.authentication.createSession(
		ctx,
		resolution.User,
		state.ClientType,
		state.DeviceId,
		state.DeviceName,
		method,
		assertion.AuthenticationStrength,
		assertion.AuthenticatedAt,
		mfaCompletedAt,
	)
	if sessionErr != nil {
		legacy := toLegacyAppError("ExternalAuthentication.complete.session", sessionErr)
		if _, completionErr := s.audit.CompleteCriticalAction(
			ctx,
			auditEvent.Id,
			model.AuditStatusFail,
			legacy.ErrorCode(),
			nil,
		); completionErr != nil {
			return nil, completionErr
		}
		return nil, legacy
	}
	if _, appErr := s.audit.CompleteCriticalAction(
		ctx,
		auditEvent.Id,
		model.AuditStatusSuccess,
		"",
		session.Auditable(),
	); appErr != nil {
		s.revokeUnreportedSession(ctx, session)
		return nil, appErr
	}
	return &model.ExternalAuthenticationCompletion{
		User: resolution.User, Session: session, Tokens: tokens,
		ReturnTo: state.ReturnTo,
	}, nil
}

func (s *ExternalAuthenticationService) checkInitiationRateLimit(
	ctx context.Context,
	providerID string,
	source string,
) *model.AppError {
	settings := s.platform.Config().Authentication.LoginRateLimit
	key := "authentication/external/source/" +
		digestCacheKey(providerID+"\x00"+normalizeLoginSource(source))
	count, err := s.platform.Cache().Add(
		ctx,
		key,
		1,
		settings.Window.Duration,
	)
	if err != nil {
		return rateLimitUnavailableError(
			"BeginExternalAuthentication.rate_limit",
			err,
		)
	}
	if count > int64(settings.MaximumSourceAttempts) {
		return model.NewAppError(
			"BeginExternalAuthentication",
			"authentication.rate_limited",
			nil,
			"",
			http.StatusTooManyRequests,
		)
	}
	return nil
}

func (s *ExternalAuthenticationService) revokeUnreportedSession(
	ctx context.Context,
	session *model.Session,
) {
	hashes, err := s.platform.Store().Session().Revoke(
		ctx,
		session.Id,
		session.UserId,
		s.now().UnixMilli(),
		"authentication audit completion failed",
	)
	if err != nil {
		s.platform.Log().ErrorContext(
			ctx,
			"failed to revoke unaudited external session",
			mlog.String("session_id", session.Id),
			mlog.Err(err),
		)
		return
	}
	s.authentication.deleteAuthenticationCache(ctx, hashes)
}

func externalUserCandidate(
	assertion *model.ExternalAuthenticationAssertion,
) *model.User {
	displayName := strings.TrimSpace(assertion.DisplayName)
	if displayName == "" {
		displayName = strings.TrimSpace(
			assertion.FirstName + " " + assertion.LastName,
		)
	}
	return &model.User{
		Username: assertion.Username, Email: assertion.Email,
		EmailVerified: assertion.EmailVerified, DisplayName: displayName,
		FirstName: assertion.FirstName, LastName: assertion.LastName,
	}
}

func externalAuthenticationCallbackURL(
	publicURL string,
	providerID string,
) (string, error) {
	callback, err := url.Parse(publicURL)
	if err != nil || callback.Host == "" {
		return "", errors.New("public URL is invalid")
	}
	callback.Path = strings.TrimSuffix(callback.Path, "/") +
		model.APIURLSuffix + "/auth/providers/" +
		url.PathEscape(providerID) + "/callback"
	callback.RawQuery = ""
	callback.Fragment = ""
	return callback.String(), nil
}

func externalProviderNotFoundError(where string) *model.AppError {
	return model.NewAppError(
		where,
		"authentication.external.provider_not_found",
		nil,
		"",
		http.StatusNotFound,
	)
}

func invalidExternalAuthenticationError(where string) *model.AppError {
	return model.NewAppError(
		where,
		"authentication.external.invalid",
		nil,
		"",
		http.StatusUnauthorized,
	)
}

func externalProviderOperationError(
	where string,
	err error,
) *model.AppError {
	if errors.Is(err, externalauth.ErrAuthenticationRejected) {
		return model.NewAppError(
			where,
			"authentication.external.rejected",
			nil,
			"",
			http.StatusUnauthorized,
		).Wrap(err)
	}
	if errors.Is(err, externalauth.ErrProviderUnavailable) ||
		errors.Is(err, externalauth.ErrInvalidResponse) {
		return model.NewAppError(
			where,
			"authentication.external.unavailable",
			nil,
			"",
			http.StatusBadGateway,
		).Wrap(err)
	}
	return internalAuthenticationError(where, err)
}
