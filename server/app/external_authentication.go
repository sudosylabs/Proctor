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
	"github.com/sudosylabs/proctor/server/store"
)

// Sentinel errors returned by the composition-owned external provider adapter.
// Application code never imports concrete protocol packages.
var (
	ErrExternalAuthenticationRejected    = errors.New("external authentication rejected")
	ErrExternalAuthenticationInvalid     = errors.New("external authentication response is invalid")
	ErrExternalAuthenticationUnavailable = errors.New("external authentication provider is unavailable")
)

// ExternalProviderBeginRequest is the protocol-neutral start challenge.
type ExternalProviderBeginRequest struct {
	CallbackURL string
	State       string
	Proof       string
}

// ExternalProviderBeginResponse is the protocol-neutral redirect challenge.
type ExternalProviderBeginResponse struct {
	RedirectURL string
}

// ExternalProviderCompleteRequest is the protocol-neutral callback completion.
type ExternalProviderCompleteRequest struct {
	CallbackURL string
	State       string
	Proof       string
	Callback    model.ExternalAuthenticationCallback
}

// ExternalIdentityProvider is the app-owned protocol-neutral provider port.
// Composition adapts concrete CAS/OIDC implementations to this surface.
type ExternalIdentityProvider interface {
	Descriptor() model.ExternalAuthenticationProvider
	AutoProvision() bool
	Begin(context.Context, ExternalProviderBeginRequest) (*ExternalProviderBeginResponse, error)
	State(model.ExternalAuthenticationCallback) (string, error)
	Complete(
		context.Context,
		ExternalProviderCompleteRequest,
	) (*model.ExternalAuthenticationAssertion, error)
}

// externalProviderSource is the protocol-neutral registry surface consumed by
// application external-login orchestration. Concrete CAS/OIDC adapters remain
// outside package app.
type externalProviderSource interface {
	Descriptors() []model.ExternalAuthenticationProvider
	Provider(id string) (ExternalIdentityProvider, bool)
}

// ExternalAuthenticationPolicy is the deployment projection for external login.
type ExternalAuthenticationPolicy struct {
	PublicURL      string
	LoginStateTTL  time.Duration
	LoginRateLimit LoginRateLimitPolicy
	NodeID         string
}

type ExternalAuthenticationService struct {
	registry       externalProviderSource
	store          store.Store
	cache          authenticationCache
	authentication *AuthenticationService
	invalidator    authenticationInvalidator
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
	invalidator authenticationInvalidator,
	audit *AuditService,
	policy ExternalAuthenticationPolicy,
	diagnostics authenticationDiagnostics,
	now func() time.Time,
) (*ExternalAuthenticationService, error) {
	if registry == nil {
		return nil, errors.New("external authentication provider registry is required")
	}
	if persistence == nil {
		return nil, errors.New("external authentication store is required")
	}
	if cache == nil {
		return nil, errors.New("external authentication cache is required")
	}
	if authentication == nil {
		return nil, errors.New("authentication service is required")
	}
	if invalidator == nil {
		return nil, errors.New("authentication invalidator is required")
	}
	if audit == nil {
		return nil, errors.New("audit service is required")
	}
	if diagnostics == nil {
		return nil, errors.New("external authentication diagnostics are required")
	}
	if now == nil {
		now = time.Now
	}
	return &ExternalAuthenticationService{
		registry: registry, store: persistence, cache: cache,
		authentication: authentication, invalidator: invalidator, audit: audit, policy: policy,
		diagnostics: diagnostics, now: now,
	}, nil
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
		return nil, authenticationUnavailable(err)
	}
	challenge, err := provider.Begin(
		ctx,
		ExternalProviderBeginRequest{
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
		return nil, authenticationUnavailable(errors.New("external provider returned an empty login challenge"))
	}
	stateStore := s.store.ExternalLoginState()
	if stateStore == nil {
		return nil, authenticationUnavailable(errors.New("external login state store is unavailable"))
	}
	if _, err := stateStore.Save(ctx, &model.ExternalLoginState{
		Provider: providerID, StateHash: model.HashToken(stateToken),
		BindingHash: model.HashToken(bindingToken), ReturnTo: returnTo,
		ClientType: clientType, DeviceID: deviceID, DeviceName: deviceName,
		ExpiresAt: model.TimeFromMillis(expiresAt),
	}); err != nil {
		return nil, authenticationUnavailable(err)
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
		return nil, authenticationUnavailable(errors.New("external login state store is unavailable"))
	}
	stateHash := model.HashToken(stateToken)
	state, err := stateStore.GetByStateHash(ctx, stateHash)
	nowTime := s.now()
	now := nowTime.UnixMilli()
	if err != nil || state == nil || state.Provider != providerID ||
		state.ConsumedAt.Valid || !state.ExpiresAt.After(nowTime) {
		if err != nil && !store.IsNotFound(err) {
			return nil, authenticationUnavailable(err)
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
		return nil, authenticationUnavailable(err)
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
		return nil, authenticationUnavailable(err)
	}

	institution, err := s.store.Institution().GetSingleton(ctx)
	if err != nil {
		return nil, authenticationUnavailable(err)
	}
	assertion, providerErr := provider.Complete(
		ctx,
		ExternalProviderCompleteRequest{
			CallbackURL: callbackURL,
			State:       stateToken,
			Proof:       bindingToken,
			Callback:    callback,
		},
	)
	if providerErr != nil {
		errorCode := "authentication.external.rejected"
		if !errors.Is(providerErr, ErrExternalAuthenticationRejected) {
			errorCode = "authentication.external.unavailable"
		}
		if appErr := s.audit.RecordExternalAuthenticationFailure(
			ctx,
			providerID,
			method,
			metadata,
			institution.ID.String(),
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
			institution.ID.String(),
			"authentication.external.invalid_assertion",
		); auditErr != nil {
			return nil, auditErr
		}
		return nil, authenticationUnavailable(errors.New("provider returned a mismatched assertion"))
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
		return nil, authenticationUnavailable(errors.New("external identity store is unavailable"))
	}
	var defaultPictureJob *model.Job
	if provider.AutoProvision() {
		userCandidate, defaultPictureJob, err = prepareUserDefaultProfilePictureJob(userCandidate, nowTime)
		if err != nil {
			return nil, authenticationUnavailable(err)
		}
	}
	resolution, err := identityStore.ResolveOrProvision(
		ctx, &store.ExternalIdentityResolutionRequest{
			Identity: &model.ExternalIdentity{
				Provider: providerID, Subject: assertion.Subject,
				LastSeenAt: model.OptionalTimeFromMillis(now),
			},
			User: userCandidate, AutoProvision: provider.AutoProvision(),
			ProvisionAudit: &model.AuditEvent{
				Action:    "authentication.external_provision",
				ScopeType: model.RoleScopeInstitution,
				ScopeID:   institution.ID.String(), Status: model.AuditStatusSuccess,
				RequestID:  metadata.RequestID,
				NodeID:     s.policy.NodeID,
				ClientType: string(state.ClientType), AuthMethod: method,
				IPAddress: metadata.IPAddress, UserAgent: metadata.UserAgent,
				Parameters: provisionParameters,
			}, DefaultProfilePictureJob: defaultPictureJob,
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
			institution.ID.String(),
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
			return nil, authenticationUnavailable(err)
		}
	}
	if !resolution.User.IsActive() {
		if appErr := s.audit.RecordExternalAuthenticationFailure(
			ctx,
			providerID,
			method,
			metadata,
			institution.ID.String(),
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
		resolution.User.ID.String(),
		method,
		providerID,
		state.ClientType,
		metadata,
		institution.ID.String(),
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
		state.DeviceID,
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
			auditEvent.ID.String(),
			model.AuditStatusFail,
			func() string {
				if f, ok := As(legacy); ok {
					return f.Code()
				}
				return "authentication.internal"
			}(),
			nil,
		); completionErr != nil {
			return nil, completionErr
		}
		return nil, legacy
	}
	if _, appErr := s.audit.CompleteCriticalAction(
		ctx,
		auditEvent.ID.String(),
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
		session.ID.String(),
		session.UserID.String(),
		s.now().UnixMilli(),
		"authentication audit completion failed",
	)
	if err != nil {
		if s.diagnostics != nil {
			s.diagnostics.WarnContext(ctx, "failed to revoke unaudited external session", err)
		}
		return
	}
	s.invalidator.InvalidateAccessCredentials(ctx, hashes)
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
	if errors.Is(err, ErrExternalAuthenticationRejected) {
		return NewError("authentication.external.rejected").Wrap(err)
	}
	if errors.Is(err, ErrExternalAuthenticationUnavailable) ||
		errors.Is(err, ErrExternalAuthenticationInvalid) {
		return NewError("authentication.external.unavailable").Wrap(err)
	}
	return authenticationUnavailable(err)
}
