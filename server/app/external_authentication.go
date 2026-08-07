// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/platform/externalauth"
	"github.com/sudosylabs/proctor/server/store"
)

// externalProviderSource is the protocol-neutral registry surface consumed by
// application external-login orchestration. Concrete CAS/OIDC adapters remain
// outside package app (ADR-0010, ticket #35).
type externalProviderSource interface {
	Descriptors() []model.ExternalAuthenticationProvider
	Provider(id string) (externalauth.Provider, bool)
}

// ExternalAuthenticationPolicy is the deployment projection for external login.
type ExternalAuthenticationPolicy struct {
	PublicURL     string
	LoginStateTTL time.Duration
	LoginRateLimit LoginRateLimitPolicy
	NodeID        string
}

type ExternalAuthenticationService struct {
	registry       externalProviderSource
	store          store.Store
	cache          authenticationCache
	authentication *AuthenticationService
	audit          *AuditService
	policy         ExternalAuthenticationPolicy
	diagnostics    authenticationDiagnostics
	now            func() time.Time
}

func newExternalAuthenticationService(
	registry externalProviderSource,
	persistence store.Store,
	cache authenticationCache,
	authentication *AuthenticationService,
	audit *AuditService,
	policy ExternalAuthenticationPolicy,
	diagnostics authenticationDiagnostics,
	now func() time.Time,
) *ExternalAuthenticationService {
	if now == nil {
		now = time.Now
	}
	return &ExternalAuthenticationService{
		registry: registry, store: persistence, cache: cache,
		authentication: authentication, audit: audit, policy: policy,
		diagnostics: diagnostics, now: now,
	}
}

func (a *App) ExternalAuthenticationProviders() []model.ExternalAuthenticationProvider {
	return a.externalAuthentication.providers()
}

func (s *ExternalAuthenticationService) providers() []model.ExternalAuthenticationProvider {
	return s.registry.Descriptors()
}

// BeginExternalAuthenticationCommand starts a browser external login.
type BeginExternalAuthenticationCommand struct {
	ProviderID string
	ReturnTo   string
	ClientType model.SessionClientType
	DeviceID   string
	DeviceName string
	Source     string
}

// CompleteExternalAuthenticationCommand finishes a browser external login.
type CompleteExternalAuthenticationCommand struct {
	ProviderID string
	Callback   model.ExternalAuthenticationCallback
	Binding    string
	Source     string
}

func (a *App) BeginExternalAuthentication(
	ctx context.Context,
	_ Invocation,
	command BeginExternalAuthenticationCommand,
) (*model.ExternalAuthenticationStart, error) {
	result, appErr := a.externalAuthentication.begin(
		ctx,
		command.ProviderID,
		command.ReturnTo,
		command.ClientType,
		command.DeviceID,
		command.DeviceName,
		command.Source,
	)
	return result, appErr
}

func (s *ExternalAuthenticationService) begin(
	ctx context.Context,
	providerID string,
	returnTo string,
	clientType model.SessionClientType,
	deviceID string,
	deviceName string,
	source string,
) (*model.ExternalAuthenticationStart, error) {
	providerID = strings.ToLower(strings.TrimSpace(providerID))
	provider, exists := s.registry.Provider(providerID)
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
		return nil, NewError("authentication.external.request.invalid")
	}
	if appErr := s.checkInitiationRateLimit(ctx, providerID, source); appErr != nil {
		return nil, appErr
	}
	stateToken := model.NewCredentialToken()
	bindingToken := model.NewCredentialToken()
	now := s.now().UnixMilli()
	expiresAt := now + s.policy.LoginStateTTL.Milliseconds()
	callbackURL, err := externalAuthenticationCallbackURL(
		s.policy.PublicURL,
		providerID,
	)
	if err != nil {
		return nil, authenticationUnavailable(err,
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
		return nil, authenticationUnavailable(errors.New("external provider returned an empty login challenge"),
		)
	}
	stateStore := s.store.ExternalLoginState()
	if stateStore == nil {
		return nil, authenticationUnavailable(errors.New("external login state store is unavailable"),
		)
	}
	if _, err := stateStore.Save(ctx, &model.ExternalLoginState{
		Provider: providerID, StateHash: model.HashToken(stateToken),
		BindingHash: model.HashToken(bindingToken), ReturnTo: returnTo,
		ClientType: clientType, DeviceId: deviceID, DeviceName: deviceName,
		ExpiresAt: expiresAt,
	}); err != nil {
		return nil, authenticationUnavailable(err,
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
	invocation Invocation,
	command CompleteExternalAuthenticationCommand,
) (*model.ExternalAuthenticationCompletion, error) {
	result, appErr := a.externalAuthentication.complete(
		ctx,
		command.ProviderID,
		command.Binding,
		command.Callback,
		invocation.RequestMetadata(),
	)
	return result, appErr
}

func (s *ExternalAuthenticationService) complete(
	ctx context.Context,
	providerID string,
	bindingToken string,
	callback model.ExternalAuthenticationCallback,
	metadata model.RequestMetadata,
) (*model.ExternalAuthenticationCompletion, error) {
	providerID = strings.ToLower(strings.TrimSpace(providerID))
	provider, exists := s.registry.Provider(providerID)
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
	stateStore := s.store.ExternalLoginState()
	if stateStore == nil {
		return nil, authenticationUnavailable(errors.New("external login state store is unavailable"),
		)
	}
	stateHash := model.HashToken(stateToken)
	state, err := stateStore.GetByStateHash(ctx, stateHash)
	now := s.now().UnixMilli()
	if err != nil || state == nil || state.Provider != providerID ||
		state.ConsumedAt != 0 || state.ExpiresAt <= now {
		if err != nil && !store.IsNotFound(err) {
			return nil, authenticationUnavailable(err,
			)
		}
		return nil, invalidExternalAuthenticationError(
			"CompleteExternalAuthentication.state",
		)
	}
	callbackURL, err := externalAuthenticationCallbackURL(
		s.policy.PublicURL,
		providerID,
	)
	if err != nil {
		return nil, authenticationUnavailable(err,
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
		return nil, authenticationUnavailable(err,
		)
	}

	institution, err := s.store.Institution().GetSingleton(ctx)
	if err != nil {
		return nil, authenticationUnavailable(err,
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
		if !errors.Is(
			providerErr,
			externalauth.ErrAuthenticationRejected,
		) {
			errorCode = "authentication.external.unavailable"
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
		return nil, NewError(errorCode).Wrap(providerErr)
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
		return nil, authenticationUnavailable(errors.New("provider returned a mismatched assertion"),
		)
	}
	userCandidate := externalUserCandidate(assertion)
	provisionParameters, appErr := model.EncodeAuditData(map[string]string{
		"provider": providerID,
	})
	if appErr != nil {
		return nil, appErr
	}
	identityStore := s.store.ExternalIdentity()
	if identityStore == nil {
		return nil, authenticationUnavailable(errors.New("external identity store is unavailable"),
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
			NodeId:     s.policy.NodeID,
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
			return nil, NewError("authentication.external.account_conflict").Wrap(err)
		case store.IsNotFound(err):
			return nil, NewError("authentication.external.account_not_linked")
		default:
			return nil, authenticationUnavailable(err,
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
		legacy := sessionErr
		if _, completionErr := s.audit.CompleteCriticalAction(
			ctx,
			auditEvent.Id,
			model.AuditStatusFail,
			func() string { if f,ok:=As(legacy); ok { return f.Code() }; return "authentication.internal" }(),
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
) error {
	settings := s.policy.LoginRateLimit
	key := "authentication/external/source/" +
		digestCacheKey(providerID+"\x00"+normalizeLoginSource(source))
	count, err := s.cache.Add(
		ctx,
		key,
		1,
		settings.Window,
	)
	if err != nil {
		return rateLimitUnavailableAppError(err)
	}
	if count > int64(settings.MaximumSourceAttempts) {
		return NewError("authentication.rate_limited")
	}
	return nil
}

func (s *ExternalAuthenticationService) revokeUnreportedSession(
	ctx context.Context,
	session *model.Session,
) {
	hashes, err := s.store.Session().Revoke(
		ctx,
		session.Id,
		session.UserId,
		s.now().UnixMilli(),
		"authentication audit completion failed",
	)
	if err != nil {
		if s.diagnostics != nil {
			s.diagnostics.WarnContext(ctx, "failed to revoke unaudited external session", err)
		}
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

func externalProviderNotFoundError(where string) error {
	_ = where
	return NewError("authentication.external.provider_not_found")
}

func invalidExternalAuthenticationError(where string) error {
	_ = where
	return NewError("authentication.external.invalid")
}

func externalProviderOperationError(
	where string,
	err error,
) error {
	_ = where
	if errors.Is(err, externalauth.ErrAuthenticationRejected) {
		return NewError("authentication.external.rejected").Wrap(err)
	}
	if errors.Is(err, externalauth.ErrProviderUnavailable) ||
		errors.Is(err, externalauth.ErrInvalidResponse) {
		return NewError("authentication.external.unavailable").Wrap(err)
	}
	return authenticationUnavailable(err)
}


// platformExternalProviders adapts platform registry accessors to the narrow
// application externalProviderSource port without exposing concrete protocol
// adapters to application orchestration.
type platformExternalProviders struct {
	service interface {
		ExternalAuthenticationProviders() []model.ExternalAuthenticationProvider
		ExternalAuthenticationProvider(string) (externalauth.Provider, bool)
	}
}

func (p platformExternalProviders) Descriptors() []model.ExternalAuthenticationProvider {
	return p.service.ExternalAuthenticationProviders()
}

func (p platformExternalProviders) Provider(id string) (externalauth.Provider, bool) {
	return p.service.ExternalAuthenticationProvider(id)
}
