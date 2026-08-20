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

type externalInvitationAcceptor interface {
	AcceptExternalIdentity(context.Context, *model.ExternalLoginState, *model.ExternalAuthenticationAssertion,
		store.AccessDeploymentCapabilities, model.RequestMetadata, string) (*store.ExternalIdentityInvitationAcceptanceResult, error)
}

// ExternalAuthenticationPolicy is the deployment projection for external login.
type ExternalAuthenticationPolicy struct {
	PublicURL      string
	LoginStateTTL  time.Duration
	LoginRateLimit LoginRateLimitPolicy
	NodeID         string
}

type externalAuthenticationAudit interface {
	BeginAuthentication(context.Context, string, string, string, model.SessionClientType, model.RequestMetadata, string) (*model.AuditEvent, error)
	RecordExternalAuthenticationFailure(context.Context, string, string, model.RequestMetadata, string, string) error
	CompleteCriticalAction(context.Context, string, model.AuditStatus, string, any) (*model.AuditEvent, error)
}

type externalAuthenticationService struct {
	registry                externalProviderSource
	loginStates             store.ExternalLoginStateStore
	institutions            store.InstitutionStore
	identities              store.ExternalIdentityStore
	sessions                store.SessionStore
	accessPolicy            authenticationAccessPolicy
	attempts                *authenticationAttemptAccounting
	authentication          authenticationSessionIssuer
	invalidator             authenticationInvalidator
	audit                   externalAuthenticationAudit
	mutationAudit           mutationAuditor
	capabilities            accessPolicyCapabilitySource
	policy                  ExternalAuthenticationPolicy
	recentAuthenticationTTL time.Duration
	diagnostics             authenticationDiagnostics
	invitationAcceptor      externalInvitationAcceptor
	newCredential           func() string
	now                     func() time.Time
}

func newExternalAuthenticationService(
	registry externalProviderSource,
	loginStates store.ExternalLoginStateStore,
	institutions store.InstitutionStore,
	identities store.ExternalIdentityStore,
	sessions store.SessionStore,
	accessPolicy authenticationAccessPolicy,
	attempts *authenticationAttemptAccounting,
	authentication authenticationSessionIssuer,
	invalidator authenticationInvalidator,
	audit externalAuthenticationAudit,
	mutationAudit mutationAuditor,
	capabilities accessPolicyCapabilitySource,
	invitationAcceptor externalInvitationAcceptor,
	policy ExternalAuthenticationPolicy,
	recentAuthenticationTTL time.Duration,
	diagnostics authenticationDiagnostics,
	newCredential func() string,
	now func() time.Time,
) (*externalAuthenticationService, error) {
	if registry == nil {
		return nil, errors.New("external authentication provider registry is required")
	}
	if loginStates == nil || institutions == nil || identities == nil || sessions == nil {
		return nil, errors.New("external authentication persistence is required")
	}
	if accessPolicy == nil {
		return nil, errors.New("external authentication access policy is required")
	}
	if attempts == nil {
		return nil, errors.New("external authentication attempt accounting is required")
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
	if mutationAudit == nil || capabilities == nil || invitationAcceptor == nil || recentAuthenticationTTL <= 0 {
		return nil, errors.New("external authentication method lifecycle dependencies are required")
	}
	if diagnostics == nil {
		return nil, errors.New("external authentication diagnostics are required")
	}
	if newCredential == nil {
		return nil, errors.New("external authentication credential generator is required")
	}
	if now == nil {
		return nil, errors.New("external authentication clock is required")
	}
	return &externalAuthenticationService{
		registry: registry, loginStates: loginStates, institutions: institutions,
		identities: identities, sessions: sessions, accessPolicy: accessPolicy, attempts: attempts,
		authentication: authentication, invalidator: invalidator, audit: audit, mutationAudit: mutationAudit,
		capabilities: capabilities, invitationAcceptor: invitationAcceptor, policy: policy, recentAuthenticationTTL: recentAuthenticationTTL,
		diagnostics: diagnostics, newCredential: newCredential, now: now,
	}, nil
}

func (a *App) ExternalAuthenticationProviders(ctx context.Context) ([]model.ExternalAuthenticationProvider, error) {
	return a.externalAuthentication.providers(ctx)
}

func (s *externalAuthenticationService) providers(ctx context.Context) ([]model.ExternalAuthenticationProvider, error) {
	providers, err := s.accessPolicy.AvailableExternalProviders(ctx, s.registry.Descriptors())
	if err != nil {
		return nil, authenticationUnavailable(err)
	}
	return providers, nil
}

// BeginExternalAuthenticationCommand starts a browser external login.
type BeginExternalAuthenticationCommand struct {
	ProviderID      string
	InvitationClaim string
	ReturnTo        string
	ClientType      model.SessionClientType
	DeviceID        string
	DeviceName      string
	Source          string
}

// CompleteExternalAuthenticationCommand finishes a browser external login.
type CompleteExternalAuthenticationCommand struct {
	ProviderID string
	Callback   model.ExternalAuthenticationCallback
	Binding    string
	Source     string
}

type BeginProviderConnectionCommand struct {
	ProviderID string
	ReturnTo   string
	Source     string
}

func (a *App) BeginProviderConnection(ctx context.Context, invocation Invocation, command BeginProviderConnectionCommand) (*model.ExternalAuthenticationStart, error) {
	if err := requireStrongRecentSession(invocation.Principal(), a.externalAuthentication.now(), a.externalAuthentication.recentAuthenticationTTL); err != nil {
		return nil, err
	}
	auditID, err := a.externalAuthentication.mutationAudit.Begin(ctx, invocation, model.ActionExternalIdentityManage,
		model.Resource{Type: model.ResourceUser, ID: invocation.Principal().UserID.String()}, "connect_provider",
		map[string]any{"provider": strings.ToLower(strings.TrimSpace(command.ProviderID))}, nil)
	if err != nil {
		return nil, err
	}
	start, appErr := a.externalAuthentication.beginForPurpose(ctx, command.ProviderID, command.ReturnTo,
		model.SessionClientWeb, "", "", command.Source, model.ExternalAuthenticationPurposeConnect,
		invocation.Principal().UserID, auditID, "")
	if appErr != nil {
		if failErr := a.externalAuthentication.mutationAudit.Fail(ctx, auditID, appErrorCode(appErr)); failErr != nil {
			return nil, failErr
		}
	}
	return start, appErr
}

func appErrorCode(err error) string {
	if appErr, ok := As(err); ok {
		return appErr.Code()
	}
	return "authentication.internal"
}

func authenticationMethodMutationError(err error) error {
	var conflict *store.ErrConflict
	switch {
	case errors.Is(err, store.ErrAuthenticationMethodDisabled):
		return NewError("authentication.method.disabled")
	case errors.Is(err, store.ErrLastUsableAuthenticationMethod):
		return NewError("authentication.method.last_usable")
	case store.IsNotFound(err):
		return NewError("authentication.method.not_found")
	case errors.As(err, &conflict):
		if conflict.Constraint == "external_identities_provider_subject_key" {
			return NewError("authentication.method.provider_conflict")
		}
		return NewError("authentication.method.conflict")
	default:
		return NewError("authentication.method.unavailable").Wrap(err)
	}
}

func (a *App) BeginExternalAuthentication(
	ctx context.Context,
	_ Invocation,
	command BeginExternalAuthenticationCommand,
) (*model.ExternalAuthenticationStart, error) {
	result, appErr := a.externalAuthentication.beginWithInvitationClaim(
		ctx,
		command.ProviderID,
		command.ReturnTo,
		command.ClientType,
		command.DeviceID,
		command.DeviceName,
		command.Source, command.InvitationClaim,
	)
	return result, appErr
}

func (s *externalAuthenticationService) begin(
	ctx context.Context,
	providerID string,
	returnTo string,
	clientType model.SessionClientType,
	deviceID string,
	deviceName string,
	source string,
) (*model.ExternalAuthenticationStart, error) {
	return s.beginWithInvitationClaim(ctx, providerID, returnTo, clientType, deviceID, deviceName, source, "")
}

func (s *externalAuthenticationService) beginWithInvitationClaim(
	ctx context.Context, providerID, returnTo string, clientType model.SessionClientType,
	deviceID, deviceName, source, invitationClaim string,
) (*model.ExternalAuthenticationStart, error) {
	return s.beginForPurpose(ctx, providerID, returnTo, clientType, deviceID, deviceName, source,
		model.ExternalAuthenticationPurposeLogin, "", "", invitationClaim)
}

func (s *externalAuthenticationService) beginForPurpose(
	ctx context.Context, providerID, returnTo string, clientType model.SessionClientType,
	deviceID, deviceName, source string, purpose model.ExternalAuthenticationPurpose, targetUserID model.UserID,
	auditEventID, invitationClaim string,
) (*model.ExternalAuthenticationStart, error) {
	providerID = strings.ToLower(strings.TrimSpace(providerID))
	provider, exists := s.registry.Provider(providerID)
	if !exists {
		return nil, externalProviderNotFoundError("BeginExternalAuthentication")
	}
	admission, err := s.accessPolicy.ExternalProviderAdmission(ctx, providerID)
	if err != nil {
		return nil, authenticationUnavailable(err)
	}
	if admission.Mode == "" {
		return nil, externalProviderNotFoundError("BeginExternalAuthentication")
	}
	if returnTo == "" {
		returnTo = "/"
	}
	if !model.IsSafeRelativeURL(returnTo) ||
		clientType != model.SessionClientWeb ||
		len(deviceID) > model.SessionDeviceIdMaxLength ||
		utf8.RuneCountInString(deviceName) > model.SessionDeviceNameMaxRunes {
		return nil, NewError("authentication.external.request.invalid")
	}
	if purpose == model.ExternalAuthenticationPurposeConnect && !targetUserID.IsValid() {
		return nil, invalidTokenAppError()
	}
	invitationClaimHash := ""
	if invitationClaim != "" {
		if purpose != model.ExternalAuthenticationPurposeLogin ||
			admission.Mode != model.ProviderAdmissionInvitationRequired ||
			!admission.InvitationAdmissionEnabled ||
			!model.IsValidCredentialToken(invitationClaim) {
			return nil, NewError("authentication.external.account_not_linked")
		}
		purpose = model.ExternalAuthenticationPurposeInvitationAdmission
		invitationClaimHash = model.HashInvitationClaim(invitationClaim)
	}
	if appErr := s.checkInitiationRateLimit(ctx, providerID, source); appErr != nil {
		return nil, appErr
	}
	stateToken := s.newCredential()
	bindingToken := s.newCredential()
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
	state := &model.ExternalLoginState{
		Provider: providerID, StateHash: model.HashToken(stateToken),
		Purpose: purpose, TargetUserID: targetUserID, AuditEventID: auditEventID,
		BindingHash: model.HashToken(bindingToken), ReturnTo: returnTo,
		ClientType: clientType, DeviceID: deviceID, DeviceName: deviceName,
	}
	var saved *model.ExternalLoginState
	if invitationClaimHash != "" {
		saved, err = s.loginStates.SaveInvitationAdmission(ctx, state, s.policy.LoginStateTTL, invitationClaimHash)
	} else {
		saved, err = s.loginStates.Save(ctx, state, s.policy.LoginStateTTL)
	}
	if err != nil {
		if invitationClaimHash != "" && store.IsNotFound(err) {
			return nil, NewError("authentication.external.account_not_linked")
		}
		return nil, authenticationUnavailable(err)
	}
	if saved == nil || saved.ExpiresAt.IsZero() {
		return nil, authenticationUnavailable(errors.New("external login state persistence returned an invalid deadline"))
	}
	return &model.ExternalAuthenticationStart{
		RedirectURL: challenge.RedirectURL,
		Binding:     bindingToken,
		ExpiresAt:   saved.ExpiresAt.UnixMilli(),
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

func (s *externalAuthenticationService) complete(
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
	admission, err := s.accessPolicy.ExternalProviderAdmission(ctx, providerID)
	if err != nil {
		return nil, authenticationUnavailable(err)
	}
	if admission.Mode == "" {
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
	stateHash := model.HashToken(stateToken)
	state, err := s.loginStates.GetByStateHash(ctx, stateHash)
	if err != nil || state == nil || state.Provider != providerID ||
		state.ClientType != model.SessionClientWeb || state.ConsumedAt.Valid {
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
	state, err = s.loginStates.Consume(
		ctx,
		providerID,
		stateHash,
		model.HashToken(bindingToken),
	)
	if err != nil {
		if store.IsNotFound(err) {
			return nil, invalidExternalAuthenticationError(
				"CompleteExternalAuthentication.consume_state",
			)
		}
		return nil, authenticationUnavailable(err)
	}
	nowTime := s.now()
	now := nowTime.UnixMilli()

	institution, err := s.institutions.GetSingleton(ctx)
	if err != nil {
		return nil, s.failConsumedProviderConnection(ctx, state, authenticationUnavailable(err))
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
			return nil, s.failConsumedProviderConnection(ctx, state, appErr)
		}
		return nil, s.failConsumedProviderConnection(ctx, state, NewError(errorCode).Wrap(providerErr))
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
			return nil, s.failConsumedProviderConnection(ctx, state, auditErr)
		}
		return nil, s.failConsumedProviderConnection(ctx, state, authenticationUnavailable(errors.New("provider returned a mismatched assertion")))
	}
	if state.Purpose == model.ExternalAuthenticationPurposeConnect {
		return s.completeProviderConnection(ctx, state, assertion, metadata, institution)
	}
	if state.Purpose == model.ExternalAuthenticationPurposeInvitationAdmission {
		if admission.Mode != model.ProviderAdmissionInvitationRequired || !admission.InvitationAdmissionEnabled {
			if auditErr := s.audit.RecordExternalAuthenticationFailure(ctx, providerID, method, metadata,
				institution.ID.String(), "authentication.external.invalid"); auditErr != nil {
				return nil, auditErr
			}
			return nil, invalidExternalAuthenticationError("CompleteExternalAuthentication.invitation_policy")
		}
		if !state.InvitationID.IsValid() || s.invitationAcceptor == nil {
			return nil, invalidExternalAuthenticationError("CompleteExternalAuthentication.invitation")
		}
		providerEmail := strings.ToLower(strings.TrimSpace(model.SanitizeUnicode(assertion.Email)))
		if !assertion.EmailVerified || !model.IsValidEmail(providerEmail) {
			if auditErr := s.audit.RecordExternalAuthenticationFailure(ctx, providerID, method, metadata,
				institution.ID.String(), "authentication.external.account_not_linked"); auditErr != nil {
				return nil, auditErr
			}
			return nil, NewError("authentication.external.account_not_linked")
		}
		result, acceptErr := s.invitationAcceptor.AcceptExternalIdentity(ctx, state, assertion,
			accessDeploymentCapabilities(s.capabilities.Snapshot()), metadata, method)
		if acceptErr != nil {
			if errors.Is(acceptErr, store.ErrAuthenticationMethodDisabled) {
				if auditErr := s.audit.RecordExternalAuthenticationFailure(ctx, providerID, method, metadata,
					institution.ID.String(), "authentication.external.invalid"); auditErr != nil {
					return nil, auditErr
				}
				return nil, invalidExternalAuthenticationError("CompleteExternalAuthentication.invitation_policy")
			}
			var conflict *store.ErrConflict
			if errors.As(acceptErr, &conflict) {
				return nil, NewError("authentication.external.account_conflict").Wrap(acceptErr)
			}
			if store.IsNotFound(acceptErr) {
				return nil, NewError("authentication.external.account_not_linked")
			}
			return nil, authenticationUnavailable(acceptErr)
		}
		if result == nil || result.User == nil || result.Identity == nil {
			return nil, authenticationUnavailable(errors.New("external Invitation acceptance returned invalid provenance"))
		}
		return &model.ExternalAuthenticationCompletion{User: result.User, ReturnTo: state.ReturnTo}, nil
	}
	var userCandidate *model.User
	var defaultPictureJob *model.Job
	var userSettings *model.UserSettingsDocument
	var provisionAudit *model.AuditEvent
	autoProvision := admission.Mode == model.ProviderAdmissionAutoProvision && provider.AutoProvision()
	if autoProvision {
		userCandidate = externalUserCandidate(assertion)
		userCandidate, defaultPictureJob, err = prepareUserDefaultProfilePictureJob(userCandidate, nowTime)
		if err != nil {
			return nil, authenticationUnavailable(err)
		}
		userSettings, err = prepareInitialUserSettingsDocument(userCandidate)
		if err != nil {
			return nil, authenticationUnavailable(err)
		}
		provisionParameters, appErr := model.EncodeAuditData(map[string]string{
			"provider": providerID,
		})
		if appErr != nil {
			return nil, appErr
		}
		provisionAudit = &model.AuditEvent{
			Action:    "authentication.external_provision",
			ScopeType: model.RoleScopeInstitution,
			ScopeID:   institution.ID.String(), Status: model.AuditStatusSuccess,
			RequestID:  metadata.RequestID,
			NodeID:     s.policy.NodeID,
			ClientType: string(state.ClientType), AuthMethod: method,
			IPAddress: metadata.IPAddress, UserAgent: metadata.UserAgent,
			Parameters: provisionParameters,
		}
	}
	resolution, err := s.identities.ResolveOrProvision(
		ctx, &store.ExternalIdentityResolutionRequest{
			Identity: &model.ExternalIdentity{
				Provider: providerID, Subject: assertion.Subject,
				LastSeenAt: model.OptionalTimeFromMillis(now),
			},
			User: userCandidate, Settings: userSettings,
			Capabilities:   accessDeploymentCapabilities(s.capabilities.Snapshot()),
			ProvisionAudit: provisionAudit, DefaultProfilePictureJob: defaultPictureJob,
		},
	)
	if err != nil {
		var conflict *store.ErrConflict
		errorCode := "authentication.external.internal"
		if errors.Is(err, store.ErrAuthenticationMethodDisabled) {
			errorCode = "authentication.external.invalid"
		} else if errors.As(err, &conflict) {
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
		case errors.Is(err, store.ErrAuthenticationMethodDisabled):
			return nil, invalidExternalAuthenticationError("CompleteExternalAuthentication.policy")
		case errors.As(err, &conflict):
			return nil, NewError("authentication.external.account_conflict").Wrap(err)
		case store.IsNotFound(err):
			return nil, NewError("authentication.external.account_not_linked")
		default:
			return nil, authenticationUnavailable(err)
		}
	}
	if resolution == nil || resolution.User == nil || resolution.Identity == nil ||
		!resolution.Identity.ID.IsValid() || resolution.Identity.UserID != resolution.User.ID ||
		resolution.Identity.Provider != providerID {
		if auditErr := s.audit.RecordExternalAuthenticationFailure(ctx, providerID, method, metadata,
			institution.ID.String(), "authentication.external.internal"); auditErr != nil {
			return nil, auditErr
		}
		return nil, authenticationUnavailable(errors.New("external identity resolution returned invalid provenance"))
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
		sessionIssuance{
			User: resolution.User, ClientType: state.ClientType,
			DeviceID: state.DeviceID, DeviceName: state.DeviceName,
			AuthenticationMethod: method, AuthenticationProviderID: providerID,
			ExternalIdentityID:     resolution.Identity.ID,
			AuthenticationStrength: assertion.AuthenticationStrength,
			AuthenticatedAt:        assertion.AuthenticatedAt, MFACompletedAt: mfaCompletedAt,
		},
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

func (s *externalAuthenticationService) failConsumedProviderConnection(ctx context.Context, state *model.ExternalLoginState, failure error) error {
	if state == nil || state.Purpose != model.ExternalAuthenticationPurposeConnect || state.AuditEventID == "" {
		return failure
	}
	if err := s.mutationAudit.Fail(ctx, state.AuditEventID, appErrorCode(failure)); err != nil {
		return err
	}
	return failure
}

func (s *externalAuthenticationService) completeProviderConnection(
	ctx context.Context, state *model.ExternalLoginState, assertion *model.ExternalAuthenticationAssertion,
	metadata model.RequestMetadata, institution *model.Institution,
) (*model.ExternalAuthenticationCompletion, error) {
	if state == nil || state.Purpose != model.ExternalAuthenticationPurposeConnect ||
		!state.TargetUserID.IsValid() || !state.ConsumedAt.Valid || assertion == nil {
		return nil, invalidExternalAuthenticationError("CompleteProviderConnection.state")
	}
	connectedAt := state.ConsumedAt.Time.Truncate(time.Millisecond)
	result, err := s.identities.LinkWithAudit(ctx, &store.ExternalIdentityLink{
		Identity: &model.ExternalIdentity{UserID: state.TargetUserID, Provider: state.Provider,
			Subject: assertion.Subject, LastSeenAt: model.OptionalTimeFrom(connectedAt)},
		Capabilities: accessDeploymentCapabilities(s.capabilities.Snapshot()),
		AuditEventID: state.AuditEventID, AuditAt: connectedAt.UnixMilli(),
	})
	if err != nil {
		mapped := authenticationMethodMutationError(err)
		if failErr := s.mutationAudit.Fail(ctx, state.AuditEventID, appErrorCode(mapped)); failErr != nil {
			return nil, failErr
		}
		return nil, mapped
	}
	_ = metadata
	_ = institution
	_ = result
	return &model.ExternalAuthenticationCompletion{ReturnTo: state.ReturnTo}, nil
}

func (s *externalAuthenticationService) checkInitiationRateLimit(
	ctx context.Context,
	providerID string,
	source string,
) error {
	settings := s.policy.LoginRateLimit
	_, limited, err := s.attempts.account(ctx, authenticationAttemptIntent{
		purpose:   authenticationAttemptPurposeExternalAuthentication,
		qualifier: providerID,
		window:    settings.Window,
		limits: []authenticationAttemptLimit{{
			dimension: authenticationAttemptDimensionSource,
			maximum:   settings.MaximumSourceAttempts,
			source:    source,
		}},
	})
	if err != nil {
		return rateLimitUnavailableAppError(err)
	}
	if limited {
		return NewError("authentication.rate_limited")
	}
	return nil
}

func (s *externalAuthenticationService) revokeUnreportedSession(
	ctx context.Context,
	session *model.Session,
) {
	hashes, err := s.sessions.Revoke(
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
