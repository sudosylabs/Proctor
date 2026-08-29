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

type desktopAuthorizationStore interface {
	CreateDesktopAuthorization(context.Context, *store.DesktopAuthorizationCreation) (*store.DesktopAuthorizationCreated, error)
	BindDesktopAuthorization(context.Context, *store.DesktopAuthorizationBinding) (*store.DesktopAuthorizationBound, error)
	GetDesktopAuthorizationContext(context.Context, string) (*store.DesktopAuthorizationContext, error)
	AuthenticateDesktopAuthorization(context.Context, *store.DesktopAuthorizationAuthentication) (*store.DesktopAuthorizationAuthenticationResult, error)
	ResetDesktopAuthorizationAccount(context.Context, *store.DesktopAuthorizationAccountReset) error
	IssueCode(context.Context, *store.DesktopAuthorizationCodeIssue) (*store.DesktopAuthorizationCodeIssued, error)
	Cancel(context.Context, *store.DesktopAuthorizationCancellation) error
	ResolveDesktopAuthorizationExchange(context.Context, *store.DesktopAuthorizationExchangeProof) (model.BrowserAuthenticationTransactionID, error)
	Exchange(context.Context, *store.DesktopAuthorizationExchange) (*store.DesktopAuthorizationExchangeResult, error)
}

type desktopAuthorizationAccessPolicy interface {
	AllowsLocalLogin(context.Context) (bool, error)
	AllowsDesktopAuthorization(context.Context, string, string) (bool, error)
}

type desktopAuthorizationUserStore interface {
	Get(context.Context, string) (*model.User, error)
}

type DesktopAuthorizationPolicy struct {
	Issuer                       string
	AllowLoopbackHTTPDevelopment bool
	TransactionLifetime          time.Duration
	CodeLifetime                 time.Duration
}

type desktopAuthorizationAuditor interface {
	PrepareIssue(context.Context, model.UserID, model.InstitutionID) (*model.AuditEvent, error)
	PrepareExchange(context.Context, Invocation, model.InstitutionID) (*model.AuditEvent, error)
	Fail(context.Context, string, string) error
}

type desktopAuthorizationAttemptLimiter interface {
	Check(context.Context, desktopAuthorizationAttemptOperation, string, string) error
}

type desktopAuthorizationAttemptOperation uint8

const (
	desktopAuthorizationAttemptStart desktopAuthorizationAttemptOperation = iota + 1
	desktopAuthorizationAttemptExchange
)

func (o desktopAuthorizationAttemptOperation) qualifier() (string, bool) {
	switch o {
	case desktopAuthorizationAttemptStart:
		return "start", true
	case desktopAuthorizationAttemptExchange:
		return "exchange", true
	default:
		return "", false
	}
}

type desktopAuthorizationAttemptAccounting struct {
	attempts *authenticationAttemptAccounting
	policy   LoginRateLimitPolicy
}

func (a desktopAuthorizationAttemptAccounting) Check(ctx context.Context, operation desktopAuthorizationAttemptOperation, identity, source string) error {
	qualifier, valid := operation.qualifier()
	if !valid || a.attempts == nil || a.policy.Window <= 0 || a.policy.MaximumAttempts <= 0 || a.policy.MaximumSourceAttempts <= 0 {
		return rateLimitUnavailableAppError(errors.New("desktop authorization attempt accounting is unavailable"))
	}
	_, limited, err := a.attempts.account(ctx, authenticationAttemptIntent{
		purpose: authenticationAttemptPurposeDesktopAuthorization, qualifier: qualifier, window: a.policy.Window,
		limits: []authenticationAttemptLimit{
			{dimension: authenticationAttemptDimensionIdentity, maximum: a.policy.MaximumAttempts, identity: identity},
			{dimension: authenticationAttemptDimensionSource, maximum: a.policy.MaximumSourceAttempts, source: source},
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

type desktopAuthorizationService struct {
	transactions   desktopAuthorizationStore
	institutions   store.InstitutionStore
	users          desktopAuthorizationUserStore
	authentication *authenticationService
	accessPolicy   desktopAuthorizationAccessPolicy
	capabilities   accessPolicyCapabilitySource
	audit          desktopAuthorizationAuditor
	attempts       desktopAuthorizationAttemptLimiter
	sessionPolicy  SessionPolicy
	policy         DesktopAuthorizationPolicy
	newCredential  func() string
	now            func() time.Time
	compatibility  desktopAuthorizationCompatibility
	dpop           *dpopSecurity
}

type desktopAuthorizationCompatibility interface {
	Evaluate(context.Context, DesktopCompatibilityQuery) (DesktopCompatibilityResult, error)
}

type desktopAuthorizationIdentityDependencies struct {
	users          desktopAuthorizationUserStore
	authentication *authenticationService
	compatibility  desktopAuthorizationCompatibility
	dpop           *dpopSecurity
}

type StartDesktopAuthorizationCommand struct {
	CallbackURL      string
	State            string
	CodeChallenge    string
	DeviceID         string
	DeviceName       string
	PublicJWK        model.DesktopPublicJWK
	DesktopRelease   string
	DesktopBuildID   string
	Platform         model.DesktopPlatform
	Architecture     model.DesktopArchitecture
	RealtimeProtocol int
	Source           string
}

type DesktopAuthorizationStart struct {
	AuthorizationURL string
	ExpiresAt        int64
	DPoPNonce        string
}

type BindDesktopAuthorizationCommand struct{ Handle, BrowserProof, State string }
type DesktopAuthorizationBinding struct {
	Binding   string
	ExpiresAt int64
}

type DesktopAuthorizationAccount struct {
	ID          model.UserID
	Username    string
	DisplayName string
}

type DesktopAuthorizationBrowserContext struct {
	State             model.BrowserAuthenticationState
	Account           *DesktopAuthorizationAccount
	LocalLoginEnabled bool
	ExternalProviders []model.ExternalAuthenticationProvider
	DeviceName        string
	ExpiresAt         int64
}

type AuthenticateDesktopAuthorizationCommand struct{ Binding string }
type AuthenticateDesktopAuthorizationLocallyCommand struct {
	Binding  string
	LoginID  string
	Password string
	MFACode  string
	Source   string
}
type ApproveDesktopAuthorizationCommand struct{ Binding, State string }
type DesktopAuthorizationApproval struct {
	RedirectURL string
	ExpiresAt   int64
}
type ExchangeDesktopAuthorizationCommand struct {
	Code, State, CodeVerifier, Source string
	DPoPProof                         string
	PublicJWK                         model.DesktopPublicJWK
	DesktopRelease, DesktopBuildID    string
	Platform                          model.DesktopPlatform
	Architecture                      model.DesktopArchitecture
	RealtimeProtocol                  int
}
type DesktopAuthorizationExchangeResult struct {
	Session      *model.Session
	Registration *model.DesktopRegistration
	Tokens       *model.AuthenticationTokens
	DPoPNonce    string
}

func newDesktopAuthorizationService(
	transactions desktopAuthorizationStore,
	institutions store.InstitutionStore,
	accessPolicy desktopAuthorizationAccessPolicy,
	capabilities accessPolicyCapabilitySource,
	audit desktopAuthorizationAuditor,
	attempts desktopAuthorizationAttemptLimiter,
	sessionPolicy SessionPolicy,
	policy DesktopAuthorizationPolicy,
	newCredential func() string,
	now func() time.Time,
	identity desktopAuthorizationIdentityDependencies,
) (*desktopAuthorizationService, error) {
	if transactions == nil || institutions == nil {
		return nil, errors.New("desktop authorization persistence is required")
	}
	if accessPolicy == nil || capabilities == nil || audit == nil || attempts == nil {
		return nil, errors.New("desktop authorization access policy is required")
	}
	if newCredential == nil || now == nil {
		return nil, errors.New("desktop authorization security dependencies are required")
	}
	if policy.TransactionLifetime == 0 {
		policy.TransactionLifetime = model.BrowserAuthenticationTransactionLifetime
	}
	if policy.CodeLifetime == 0 {
		policy.CodeLifetime = model.DesktopAuthorizationCodeLifetime
	}
	if policy.TransactionLifetime <= 0 ||
		policy.TransactionLifetime > model.BrowserAuthenticationTransactionLifetime ||
		policy.CodeLifetime <= 0 || policy.CodeLifetime > model.DesktopAuthorizationCodeLifetime {
		return nil, errors.New("desktop authorization lifetime policy is invalid")
	}
	if err := model.ValidateBrowserAuthenticationIssuer(policy.Issuer, policy.AllowLoopbackHTTPDevelopment); err != nil {
		return nil, errors.New("desktop authorization issuer policy is invalid")
	}
	if sessionPolicy.AccessTTL <= 0 || sessionPolicy.RefreshTTL <= 0 || sessionPolicy.IdleTTL <= 0 ||
		sessionPolicy.AbsoluteTTL <= 0 || sessionPolicy.MaximumPerUser < 1 {
		return nil, errors.New("desktop authorization session policy is invalid")
	}
	if identity.users == nil || identity.authentication == nil ||
		identity.compatibility == nil || identity.dpop == nil {
		return nil, errors.New("desktop authorization identity dependencies are invalid")
	}
	return &desktopAuthorizationService{
		transactions: transactions, institutions: institutions, users: identity.users, authentication: identity.authentication, accessPolicy: accessPolicy,
		capabilities: capabilities, audit: audit, attempts: attempts, sessionPolicy: sessionPolicy,
		policy: policy, newCredential: newCredential, now: now,
		compatibility: identity.compatibility, dpop: identity.dpop,
	}, nil
}

func (s *desktopAuthorizationService) Bind(ctx context.Context, command BindDesktopAuthorizationCommand) (*DesktopAuthorizationBinding, error) {
	if !model.IsValidCredentialToken(command.Handle) || !model.IsValidCredentialToken(command.BrowserProof) ||
		!model.IsValidCredentialToken(command.State) {
		return nil, NewError("authentication.desktop_authorization.invalid")
	}
	binding := s.newCredential()
	if !model.IsValidCredentialToken(binding) || binding == command.Handle || binding == command.BrowserProof {
		return nil, NewError("authentication.desktop_authorization.unavailable")
	}
	bound, err := s.transactions.BindDesktopAuthorization(ctx, &store.DesktopAuthorizationBinding{
		HandleHash: model.HashToken(command.Handle), BrowserProofHash: model.HashToken(command.BrowserProof),
		StateHash: model.HashToken(command.State), BindingHash: model.HashToken(binding),
	})
	if err != nil {
		return nil, desktopAuthorizationStoreError(err)
	}
	if bound == nil || bound.ExpiresAt.IsZero() {
		return nil, NewError("authentication.desktop_authorization.unavailable")
	}
	return &DesktopAuthorizationBinding{Binding: binding, ExpiresAt: bound.ExpiresAt.UnixMilli()}, nil
}

func (s *desktopAuthorizationService) Context(ctx context.Context, binding string) (*DesktopAuthorizationBrowserContext, error) {
	if !model.IsValidCredentialToken(binding) {
		return nil, NewError("authentication.desktop_authorization.invalid")
	}
	current, err := s.transactions.GetDesktopAuthorizationContext(ctx, model.HashToken(binding))
	if err != nil || current == nil {
		return nil, desktopAuthorizationStoreError(err)
	}
	result := &DesktopAuthorizationBrowserContext{State: current.State, DeviceName: current.DeviceName, ExpiresAt: current.ExpiresAt.UnixMilli()}
	if current.State == model.BrowserAuthenticationStateAuthenticated {
		user, getErr := s.users.Get(ctx, current.UserID.String())
		if getErr != nil || user == nil || !user.IsActive() {
			return nil, NewError("authentication.desktop_authorization.unavailable").Wrap(getErr)
		}
		result.Account = &DesktopAuthorizationAccount{ID: user.ID, Username: user.Username, DisplayName: user.DisplayName}
		return result, nil
	}
	if current.State != model.BrowserAuthenticationStateBound {
		return nil, NewError("authentication.desktop_authorization.rejected")
	}
	local, policyErr := s.accessPolicy.AllowsLocalLogin(ctx)
	if policyErr != nil {
		return nil, NewError("authentication.desktop_authorization.unavailable").Wrap(policyErr)
	}
	if local {
		local, policyErr = s.accessPolicy.AllowsDesktopAuthorization(ctx, "password", "")
		if policyErr != nil {
			return nil, NewError("authentication.desktop_authorization.unavailable").Wrap(policyErr)
		}
	}
	result.LocalLoginEnabled = local
	for _, configured := range s.capabilities.Snapshot().Providers {
		allowed, allowErr := s.accessPolicy.AllowsDesktopAuthorization(ctx, configured.Descriptor.Type, configured.Descriptor.Id)
		if allowErr != nil {
			return nil, NewError("authentication.desktop_authorization.unavailable").Wrap(allowErr)
		}
		if allowed {
			result.ExternalProviders = append(result.ExternalProviders, configured.Descriptor)
		}
	}
	return result, nil
}

func (s *desktopAuthorizationService) AuthenticateSession(ctx context.Context, invocation Invocation, command AuthenticateDesktopAuthorizationCommand) (*DesktopAuthorizationBrowserContext, error) {
	principal := invocation.Principal()
	if principal.Validate() != nil || principal.CredentialType != model.CredentialSessionAccess ||
		principal.ClientType != model.SessionClientWeb || !model.IsValidCredentialToken(command.Binding) {
		return nil, NewError("authentication.desktop_authorization.invalid")
	}
	result, err := s.authenticate(ctx, command.Binding, store.DesktopAuthorizationAuthentication{
		UserID: principal.UserID, AuthenticationMethod: principal.AuthenticationMethod,
		AuthenticationProviderID: principal.AuthenticationProviderID, ExternalIdentityID: principal.ExternalIdentityID,
		AuthenticationStrength: principal.AuthenticationStrength, AuthenticatedAt: principal.AuthenticatedAt.UnixMilli(),
		MFACompletedAt: principal.MFACompletedAt.Millis(),
	})
	if err != nil || result.Denied {
		return nil, err
	}
	return s.Context(ctx, command.Binding)
}

func (s *desktopAuthorizationService) AuthenticateLocal(ctx context.Context, command AuthenticateDesktopAuthorizationLocallyCommand) (*DesktopAuthorizationBrowserContext, error) {
	if !model.IsValidCredentialToken(command.Binding) {
		return nil, NewError("authentication.desktop_authorization.invalid")
	}
	proof, err := s.authentication.authenticateLocal(ctx, LoginCommand{
		LoginID: command.LoginID, Password: command.Password, MFACode: command.MFACode,
		ClientType: model.SessionClientWeb, Source: command.Source,
	})
	if err != nil {
		return nil, err
	}
	result, err := s.authenticate(ctx, command.Binding, store.DesktopAuthorizationAuthentication{
		UserID: proof.User.ID, AuthenticationMethod: "password", AuthenticationStrength: proof.AuthenticationStrength,
		AuthenticatedAt: proof.AuthenticatedAt, MFACompletedAt: proof.MFACompletedAt,
	})
	if err != nil {
		return nil, err
	}
	if !result.Denied {
		s.authentication.resetLocalAuthenticationAttempts(ctx, proof.receipt)
	}
	return s.Context(ctx, command.Binding)
}

func (s *desktopAuthorizationService) authenticate(ctx context.Context, binding string, input store.DesktopAuthorizationAuthentication) (*store.DesktopAuthorizationAuthenticationResult, error) {
	input.BindingHash = model.HashToken(binding)
	input.Capabilities = accessDeploymentCapabilities(s.capabilities.Snapshot())
	result, err := s.transactions.AuthenticateDesktopAuthorization(ctx, &input)
	if err != nil {
		return nil, desktopAuthorizationStoreError(err)
	}
	if result == nil {
		return nil, NewError("authentication.desktop_authorization.unavailable")
	}
	if result.Denied {
		return result, NewError("authentication.desktop_authorization.account_session_locked")
	}
	return result, nil
}

func (s *desktopAuthorizationService) authenticateExternal(ctx context.Context, transactionID model.BrowserAuthenticationTransactionID,
	user *model.User, identity *model.ExternalIdentity, method string, assertion *model.ExternalAuthenticationAssertion,
) error {
	if !transactionID.IsValid() || user == nil || !user.IsActive() || identity == nil ||
		identity.UserID != user.ID || assertion == nil || assertion.ProviderId != identity.Provider {
		return NewError("authentication.desktop_authorization.invalid")
	}
	mfaCompletedAt := int64(0)
	if assertion.AuthenticationStrength == model.AuthenticationMultiFactor {
		mfaCompletedAt = assertion.AuthenticatedAt
	}
	input := &store.DesktopAuthorizationAuthentication{
		TransactionID: transactionID, UserID: user.ID, AuthenticationMethod: method,
		AuthenticationProviderID: identity.Provider, ExternalIdentityID: identity.ID,
		AuthenticationStrength: assertion.AuthenticationStrength, AuthenticatedAt: assertion.AuthenticatedAt,
		MFACompletedAt: mfaCompletedAt, Capabilities: accessDeploymentCapabilities(s.capabilities.Snapshot()),
	}
	result, err := s.transactions.AuthenticateDesktopAuthorization(ctx, input)
	if err != nil {
		return desktopAuthorizationStoreError(err)
	}
	if result == nil {
		return NewError("authentication.desktop_authorization.unavailable")
	}
	if result.Denied {
		return NewError("authentication.desktop_authorization.account_session_locked")
	}
	return nil
}

func (s *desktopAuthorizationService) ResetAccount(ctx context.Context, binding string) error {
	if !model.IsValidCredentialToken(binding) {
		return NewError("authentication.desktop_authorization.invalid")
	}
	if err := s.transactions.ResetDesktopAuthorizationAccount(ctx, &store.DesktopAuthorizationAccountReset{BindingHash: model.HashToken(binding)}); err != nil {
		return desktopAuthorizationStoreError(err)
	}
	return nil
}

func (s *desktopAuthorizationService) Approve(ctx context.Context, _ Invocation, command ApproveDesktopAuthorizationCommand) (*DesktopAuthorizationApproval, error) {
	if !model.IsValidCredentialToken(command.Binding) || !model.IsValidCredentialToken(command.State) {
		return nil, NewError("authentication.desktop_authorization.invalid")
	}
	current, err := s.transactions.GetDesktopAuthorizationContext(ctx, model.HashToken(command.Binding))
	if err != nil || current == nil || current.State != model.BrowserAuthenticationStateAuthenticated || !current.UserID.IsValid() {
		return nil, desktopAuthorizationStoreError(err)
	}
	institution, err := s.institutions.GetSingleton(ctx)
	if err != nil || institution == nil || !institution.ID.IsValid() {
		return nil, NewError("authentication.desktop_authorization.unavailable").Wrap(err)
	}
	audit, err := s.audit.PrepareIssue(ctx, current.UserID, institution.ID)
	if err != nil || audit == nil || !audit.ID.IsValid() {
		return nil, NewError("audit.unavailable").Wrap(err)
	}
	code := s.newCredential()
	if !model.IsValidCredentialToken(code) {
		return nil, NewError("authentication.desktop_authorization.unavailable")
	}
	at := model.TimeUTC(s.now())
	issued, err := s.transactions.IssueCode(ctx, &store.DesktopAuthorizationCodeIssue{
		BindingHash: model.HashToken(command.Binding), StateHash: model.HashToken(command.State),
		CodeHash: model.HashToken(code), ExpectedUserID: current.UserID,
		CodeLifetime: s.policy.CodeLifetime,
		Capabilities: accessDeploymentCapabilities(s.capabilities.Snapshot()), AuditEventID: audit.ID.String(), AuditAt: at.UnixMilli(),
	})
	if err != nil {
		failure := desktopAuthorizationStoreError(err)
		if auditErr := s.audit.Fail(ctx, audit.ID.String(), failure.Code()); auditErr != nil {
			return nil, NewError("audit.unavailable").Wrap(auditErr)
		}
		return nil, failure
	}
	if issued == nil || model.ValidateDesktopAuthorizationCallback(issued.CallbackURL) != nil || issued.CodeExpiresAt.IsZero() {
		failure := NewError("authentication.desktop_authorization.unavailable")
		if auditErr := s.audit.Fail(ctx, audit.ID.String(), failure.Code()); auditErr != nil {
			return nil, NewError("audit.unavailable").Wrap(auditErr)
		}
		return nil, failure
	}
	redirect, err := desktopAuthorizationRedirectURL(issued.CallbackURL, code, command.State)
	if err != nil {
		return nil, NewError("authentication.desktop_authorization.unavailable").Wrap(err)
	}
	return &DesktopAuthorizationApproval{RedirectURL: redirect, ExpiresAt: issued.CodeExpiresAt.UnixMilli()}, nil
}

func (s *desktopAuthorizationService) Cancel(ctx context.Context, command ApproveDesktopAuthorizationCommand) error {
	if !model.IsValidCredentialToken(command.Binding) || !model.IsValidCredentialToken(command.State) {
		return NewError("authentication.desktop_authorization.invalid")
	}
	err := s.transactions.Cancel(ctx, &store.DesktopAuthorizationCancellation{
		BindingHash: model.HashToken(command.Binding), StateHash: model.HashToken(command.State),
	})
	if err != nil {
		return desktopAuthorizationStoreError(err)
	}
	return nil
}

func (s *desktopAuthorizationService) Exchange(ctx context.Context, invocation Invocation, command ExchangeDesktopAuthorizationCommand) (*DesktopAuthorizationExchangeResult, error) {
	if !model.IsValidCredentialToken(command.Code) || !model.IsValidCredentialToken(command.State) || !model.IsValidPKCEVerifier(command.CodeVerifier) {
		return nil, NewError("authentication.desktop_authorization.invalid")
	}
	if s.dpop == nil || s.compatibility == nil || command.PublicJWK.Validate() != nil {
		return nil, NewError("authentication.desktop_authorization.unavailable")
	}
	thumbprint, err := command.PublicJWK.Thumbprint()
	if err != nil {
		return nil, NewError("authentication.dpop.invalid")
	}
	codeHash := model.HashToken(command.Code)
	if err = s.attempts.Check(ctx, desktopAuthorizationAttemptExchange, codeHash, command.Source); err != nil {
		return nil, err
	}
	authorizationID, err := s.transactions.ResolveDesktopAuthorizationExchange(ctx, &store.DesktopAuthorizationExchangeProof{
		CodeHash: codeHash, StateHash: model.HashToken(command.State), CodeChallenge: model.PKCES256Challenge(command.CodeVerifier),
		Issuer: strings.TrimSuffix(s.policy.Issuer, "/"), ExpectedKeyThumbprint: thumbprint,
		DesktopRelease: command.DesktopRelease, DesktopBuildID: command.DesktopBuildID, DesktopPlatform: command.Platform,
		DesktopArchitecture: command.Architecture, DesktopRealtimeProtocol: command.RealtimeProtocol,
	})
	if err != nil {
		return nil, desktopAuthorizationStoreError(err)
	}
	binding := dpopBinding{Kind: dpopBindingAuthorization, AuthorizationID: authorizationID,
		KeyThumbprint: thumbprint, Origin: s.dpop.policy.Origin}
	target, targetErr := canonicalDPoPTarget(s.dpop.policy.Origin + "/api/v1/auth/desktop/token")
	if targetErr != nil {
		return nil, NewError("authentication.desktop_authorization.unavailable").Wrap(targetErr)
	}
	if _, err = s.dpop.Verify(ctx, command.DPoPProof, "POST", target, "", &command.PublicJWK, binding); err != nil {
		return nil, err
	}
	compatibility, err := s.compatibility.Evaluate(ctx, desktopCompatibilityQueryFromAuthorization(command))
	if err != nil {
		return nil, NewError("authentication.desktop_authorization.unavailable").Wrap(err)
	}
	if compatibility.PolicyRevision < 1 {
		return nil, NewError("authentication.desktop_authorization.unavailable")
	}
	if compatibility.Compatibility != DesktopCompatibilityCompatible {
		return nil, NewError("authentication.desktop_authorization.incompatible").WithField("reason", compatibility.Reason)
	}
	if compatibility.Availability != model.DesktopAvailabilityReady {
		return nil, NewError("authentication.desktop_authorization.unavailable").WithField("reason", string(compatibility.Availability))
	}
	institution, err := s.institutions.GetSingleton(ctx)
	if err != nil || institution == nil || !institution.ID.IsValid() {
		return nil, NewError("authentication.desktop_authorization.unavailable").Wrap(err)
	}
	audit, err := s.audit.PrepareExchange(ctx, invocation, institution.ID)
	if err != nil || audit == nil || !audit.ID.IsValid() {
		return nil, NewError("audit.unavailable").Wrap(err)
	}
	at := model.TimeUTC(s.now())
	accessToken, refreshToken := s.newCredential(), s.newCredential()
	if !model.IsValidCredentialToken(accessToken) || !model.IsValidCredentialToken(refreshToken) {
		return nil, NewError("authentication.desktop_authorization.unavailable")
	}
	result, err := s.transactions.Exchange(ctx, &store.DesktopAuthorizationExchange{
		CodeHash: codeHash, StateHash: model.HashToken(command.State),
		CodeChallenge: model.PKCES256Challenge(command.CodeVerifier), Issuer: strings.TrimSuffix(s.policy.Issuer, "/"),
		ExpectedPublicJWK: command.PublicJWK, ExpectedKeyThumbprint: thumbprint,
		DesktopRelease: command.DesktopRelease, DesktopBuildID: command.DesktopBuildID,
		DesktopPlatform: command.Platform, DesktopArchitecture: command.Architecture,
		DesktopRealtimeProtocol:            command.RealtimeProtocol,
		DesktopCompatibilityPolicyRevision: compatibility.PolicyRevision,
		AccessTokenHash:                    model.HashToken(accessToken), RefreshTokenHash: model.HashToken(refreshToken),
		AccessLifetime: s.sessionPolicy.AccessTTL, RefreshLifetime: s.sessionPolicy.RefreshTTL,
		IdleLifetime: s.sessionPolicy.IdleTTL, AbsoluteLifetime: s.sessionPolicy.AbsoluteTTL,
		MaximumActive: s.sessionPolicy.MaximumPerUser, Capabilities: accessDeploymentCapabilities(s.capabilities.Snapshot()),
		AuditEventID: audit.ID.String(), AuditAt: at.UnixMilli(),
	})
	if err != nil {
		failure := desktopAuthorizationStoreError(err)
		if auditErr := s.audit.Fail(ctx, audit.ID.String(), failure.Code()); auditErr != nil {
			return nil, NewError("audit.unavailable").Wrap(auditErr)
		}
		return nil, failure
	}
	if result != nil && result.Denied {
		return nil, NewError("authentication.desktop_authorization.account_session_locked")
	}
	if !validDesktopAuthorizationExchangeResult(result, s.sessionPolicy) {
		failure := NewError("authentication.desktop_authorization.unavailable")
		if auditErr := s.audit.Fail(ctx, audit.ID.String(), failure.Code()); auditErr != nil {
			return nil, NewError("audit.unavailable").Wrap(auditErr)
		}
		return nil, failure
	}
	nonce, nonceErr := s.dpop.IssueNonce(ctx, dpopBinding{Kind: dpopBindingSession,
		SessionID: result.Session.ID, DesktopRegistrationID: result.Registration.ID,
		KeyThumbprint: result.Session.DPoPKeyThumbprint, Origin: s.dpop.policy.Origin})
	if nonceErr != nil {
		// The credentials were never disclosed, but the SQL exchange has
		// committed. Revoke that exact Session synchronously so a cache outage
		// cannot consume the per-user Session allowance with inaccessible
		// Sessions. The active Desktop Registration remains reusable by a fresh
		// authorization after nonce storage recovers.
		revokeCtx := context.WithoutCancel(ctx)
		hashes, revokeErr := s.authentication.sessions.Revoke(revokeCtx, result.Session.ID.String(),
			result.Session.UserID.String(), model.MillisFromTime(s.now()), model.SessionRevocationDesktopAuthorizationFailed)
		if revokeErr != nil {
			return nil, NewError("authentication.desktop_authorization.unavailable").Wrap(errors.Join(nonceErr, revokeErr))
		}
		s.authentication.securityEffects.SessionsRevoked(revokeCtx, result.Session.UserID.String(),
			[]string{result.Session.ID.String()}, hashes)
		return nil, nonceErr
	}
	return &DesktopAuthorizationExchangeResult{Session: result.Session, Registration: result.Registration,
		Tokens: &model.AuthenticationTokens{TokenType: "DPoP", AccessToken: accessToken,
			RefreshToken: refreshToken, AccessExpiresAt: result.AccessExpiresAt, RefreshExpiresAt: result.RefreshExpiresAt},
		DPoPNonce: nonce}, nil
}

func validDesktopAuthorizationExchangeResult(result *store.DesktopAuthorizationExchangeResult, policy SessionPolicy) bool {
	if result == nil || result.Session == nil || result.Registration == nil || result.Registration.Validate() != nil ||
		result.Session.DesktopRegistrationID != result.Registration.ID ||
		result.Session.DPoPKeyThumbprint != result.Registration.KeyThumbprint || result.Session.Validate() != nil ||
		result.Session.ClientType != model.SessionClientDesktop || result.Session.ArchivedAt.Valid || result.Session.RevokedAt.Valid ||
		result.AccessExpiresAt.IsZero() || result.RefreshExpiresAt.IsZero() ||
		!result.AccessExpiresAt.After(result.Session.CreatedAt) || !result.RefreshExpiresAt.After(result.Session.CreatedAt) {
		return false
	}
	return !result.AccessExpiresAt.After(result.Session.CreatedAt.Add(policy.AccessTTL)) &&
		!result.RefreshExpiresAt.After(result.Session.CreatedAt.Add(policy.RefreshTTL))
}

func desktopAuthorizationRedirectURL(callback, code, state string) (string, error) {
	if model.ValidateDesktopAuthorizationCallback(callback) != nil {
		return "", model.ErrInvalidDesktopAuthorizationCallback
	}
	parsed, err := url.Parse(callback)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	query.Set("code", code)
	query.Set("state", state)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func desktopAuthorizationStoreError(err error) *Error {
	if store.IsNotFound(err) || errors.Is(err, store.ErrAuthenticationMethodDisabled) {
		return NewError("authentication.desktop_authorization.rejected")
	}
	var conflict *store.ErrConflict
	if errors.As(err, &conflict) && conflict.Constraint == "sessions_maximum_per_user" {
		return NewError("authentication.sessions.maximum_reached")
	}
	return NewError("authentication.desktop_authorization.unavailable").Wrap(err)
}

func minTime(left, right time.Time) time.Time {
	if left.Before(right) {
		return left
	}
	return right
}

func (a *App) StartDesktopAuthorization(ctx context.Context, _ Invocation, command StartDesktopAuthorizationCommand) (*DesktopAuthorizationStart, error) {
	return a.desktopAuthorization.Start(ctx, command)
}

func (a *App) BindDesktopAuthorization(ctx context.Context, _ Invocation, command BindDesktopAuthorizationCommand) (*DesktopAuthorizationBinding, error) {
	return a.desktopAuthorization.Bind(ctx, command)
}

func (a *App) DesktopAuthorizationContext(ctx context.Context, _ Invocation, binding string) (*DesktopAuthorizationBrowserContext, error) {
	return a.desktopAuthorization.Context(ctx, binding)
}

func (a *App) AuthenticateDesktopAuthorizationSession(ctx context.Context, invocation Invocation, command AuthenticateDesktopAuthorizationCommand) (*DesktopAuthorizationBrowserContext, error) {
	return a.desktopAuthorization.AuthenticateSession(ctx, invocation, command)
}

func (a *App) AuthenticateDesktopAuthorizationLocally(ctx context.Context, _ Invocation, command AuthenticateDesktopAuthorizationLocallyCommand) (*DesktopAuthorizationBrowserContext, error) {
	return a.desktopAuthorization.AuthenticateLocal(ctx, command)
}

func (a *App) ResetDesktopAuthorizationAccount(ctx context.Context, _ Invocation, binding string) error {
	return a.desktopAuthorization.ResetAccount(ctx, binding)
}

func (a *App) ApproveDesktopAuthorization(ctx context.Context, invocation Invocation, command ApproveDesktopAuthorizationCommand) (*DesktopAuthorizationApproval, error) {
	return a.desktopAuthorization.Approve(ctx, invocation, command)
}

func (a *App) CancelDesktopAuthorization(ctx context.Context, _ Invocation, command ApproveDesktopAuthorizationCommand) error {
	return a.desktopAuthorization.Cancel(ctx, command)
}

func (a *App) ExchangeDesktopAuthorization(ctx context.Context, invocation Invocation, command ExchangeDesktopAuthorizationCommand) (*DesktopAuthorizationExchangeResult, error) {
	return a.desktopAuthorization.Exchange(ctx, invocation, command)
}

type desktopAuthorizationAuditAdapter struct{ audit *auditService }

func (a desktopAuthorizationAuditAdapter) PrepareIssue(ctx context.Context, userID model.UserID, institutionID model.InstitutionID) (*model.AuditEvent, error) {
	if a.audit == nil || !userID.IsValid() || !institutionID.IsValid() {
		return nil, NewError("audit.unavailable")
	}
	return a.audit.BeginSystemCriticalActionAtScope(ctx, model.Action("authentication.desktop_authorization"),
		model.Resource{Type: model.ResourceInstitution, ID: institutionID.String()}, model.RoleScopeInstitution,
		institutionID.String(), map[string]any{"operation": "issue_code", "user_id": userID.String()})
}

func (a desktopAuthorizationAuditAdapter) PrepareExchange(ctx context.Context, _ Invocation, institutionID model.InstitutionID) (*model.AuditEvent, error) {
	if a.audit == nil || !institutionID.IsValid() {
		return nil, NewError("audit.unavailable")
	}
	return a.audit.BeginSystemCriticalActionAtScope(ctx, model.Action("authentication.desktop_session_issue"),
		model.Resource{Type: model.ResourceInstitution, ID: institutionID.String()}, model.RoleScopeInstitution,
		institutionID.String(), map[string]any{"operation": "exchange"})
}

func (a desktopAuthorizationAuditAdapter) Fail(ctx context.Context, auditID, errorCode string) error {
	if a.audit == nil {
		return NewError("audit.unavailable")
	}
	return (mutationAuditAdapter{audit: a.audit}).Fail(ctx, auditID, errorCode)
}

func (s *desktopAuthorizationService) Start(
	ctx context.Context,
	command StartDesktopAuthorizationCommand,
) (*DesktopAuthorizationStart, error) {
	if err := s.attempts.Check(ctx, desktopAuthorizationAttemptStart, model.HashToken(command.State), command.Source); err != nil {
		return nil, err
	}
	if model.ValidateDesktopAuthorizationCallback(command.CallbackURL) != nil ||
		!model.IsValidCredentialToken(command.State) ||
		!model.IsValidCredentialToken(command.CodeChallenge) ||
		command.PublicJWK.Validate() != nil || len(command.DeviceID) > model.SessionDeviceIdMaxLength ||
		utf8.RuneCountInString(command.DeviceName) > model.SessionDeviceNameMaxRunes {
		return nil, NewError("authentication.desktop_authorization.invalid")
	}
	if s.dpop == nil || s.compatibility == nil {
		return nil, NewError("authentication.desktop_authorization.unavailable")
	}
	compatibility, err := s.compatibility.Evaluate(ctx, desktopCompatibilityQueryFromStart(command))
	if err != nil {
		return nil, NewError("authentication.desktop_authorization.unavailable").Wrap(err)
	}
	if compatibility.Compatibility != DesktopCompatibilityCompatible {
		return nil, NewError("authentication.desktop_authorization.incompatible").WithField("reason", compatibility.Reason)
	}
	if compatibility.Availability != model.DesktopAvailabilityReady {
		return nil, NewError("authentication.desktop_authorization.unavailable").WithField("reason", string(compatibility.Availability))
	}
	thumbprint, err := command.PublicJWK.Thumbprint()
	if err != nil {
		return nil, NewError("authentication.desktop_authorization.invalid")
	}
	enabled, err := s.hasAuthenticationChoice(ctx)
	if err != nil {
		return nil, NewError("authentication.desktop_authorization.unavailable").Wrap(err)
	}
	if !enabled {
		return nil, NewError("authentication.desktop_authorization.disabled")
	}
	institution, err := s.institutions.GetSingleton(ctx)
	if err != nil || institution == nil || !institution.ID.IsValid() {
		return nil, NewError("authentication.desktop_authorization.unavailable").Wrap(err)
	}
	handle := s.newCredential()
	proof := s.newCredential()
	if !model.IsValidCredentialToken(handle) || !model.IsValidCredentialToken(proof) || handle == proof {
		return nil, NewError("authentication.desktop_authorization.unavailable")
	}
	creation := &store.DesktopAuthorizationCreation{
		ID: model.NewBrowserAuthenticationTransactionID(), InstitutionID: institution.ID,
		Issuer:     strings.TrimSuffix(s.policy.Issuer, "/"),
		HandleHash: model.HashToken(handle), BrowserProofHash: model.HashToken(proof),
		StateHash: model.HashToken(command.State), CallbackURL: command.CallbackURL,
		CodeChallenge: command.CodeChallenge,
		DeviceID:      command.DeviceID, DeviceName: command.DeviceName,
		ProposedPublicJWK: command.PublicJWK, ProposedKeyThumbprint: thumbprint,
		DesktopRelease: command.DesktopRelease, DesktopBuildID: command.DesktopBuildID,
		DesktopPlatform: command.Platform, DesktopArchitecture: command.Architecture,
		DesktopRealtimeProtocol: command.RealtimeProtocol,
		Lifetime:                s.policy.TransactionLifetime,
	}
	saved, err := s.transactions.CreateDesktopAuthorization(ctx, creation)
	if err != nil {
		return nil, NewError("authentication.desktop_authorization.unavailable").Wrap(err)
	}
	if saved == nil || saved.ID != creation.ID || saved.ExpiresAt.IsZero() {
		return nil, NewError("authentication.desktop_authorization.unavailable")
	}
	nonce, err := s.dpop.IssueNonce(ctx, dpopBinding{Kind: dpopBindingAuthorization,
		AuthorizationID: creation.ID, KeyThumbprint: thumbprint, Origin: s.dpop.policy.Origin})
	if err != nil {
		return nil, err
	}
	authorizationURL, err := desktopAuthorizationURL(creation.Issuer, handle, proof, command.State)
	if err != nil {
		return nil, NewError("authentication.desktop_authorization.unavailable").Wrap(err)
	}
	return &DesktopAuthorizationStart{
		AuthorizationURL: authorizationURL,
		ExpiresAt:        saved.ExpiresAt.UnixMilli(),
		DPoPNonce:        nonce,
	}, nil
}

func desktopCompatibilityQueryFromStart(command StartDesktopAuthorizationCommand) DesktopCompatibilityQuery {
	return desktopCompatibilityQueryFromBuild(desktopAuthorizationBuild{
		release: command.DesktopRelease, buildID: command.DesktopBuildID, platform: command.Platform,
		architecture: command.Architecture, realtimeProtocol: command.RealtimeProtocol,
	})
}

func desktopCompatibilityQueryFromAuthorization(command ExchangeDesktopAuthorizationCommand) DesktopCompatibilityQuery {
	return desktopCompatibilityQueryFromBuild(desktopAuthorizationBuild{
		release: command.DesktopRelease, buildID: command.DesktopBuildID, platform: command.Platform,
		architecture: command.Architecture, realtimeProtocol: command.RealtimeProtocol,
	})
}

type desktopAuthorizationBuild struct {
	release          string
	buildID          string
	platform         model.DesktopPlatform
	architecture     model.DesktopArchitecture
	realtimeProtocol int
}

func desktopCompatibilityQueryFromBuild(build desktopAuthorizationBuild) DesktopCompatibilityQuery {
	return DesktopCompatibilityQuery{
		DesktopRelease: build.release, DesktopBuildID: build.buildID, Platform: string(build.platform),
		Architecture: string(build.architecture), RealtimeProtocol: build.realtimeProtocol,
	}
}

func (s *desktopAuthorizationService) hasAuthenticationChoice(ctx context.Context) (bool, error) {
	allowed, err := s.accessPolicy.AllowsDesktopAuthorization(ctx, "password", "")
	if err != nil || allowed {
		return allowed, err
	}
	for _, provider := range s.capabilities.Snapshot().Providers {
		allowed, err = s.accessPolicy.AllowsDesktopAuthorization(ctx, provider.Descriptor.Type, provider.Descriptor.Id)
		if err != nil || allowed {
			return allowed, err
		}
	}
	return false, nil
}

func desktopAuthorizationURL(issuer, handle, proof, state string) (string, error) {
	parsed, err := url.Parse(issuer)
	if err != nil || parsed.Host == "" {
		return "", errors.New("desktop authorization issuer is invalid")
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, "/") + "/authorize/desktop"
	query := parsed.Query()
	query.Set("request", handle)
	query.Set("state", state)
	parsed.RawQuery = query.Encode()
	parsed.Fragment = "proof=" + proof
	return parsed.String(), nil
}
