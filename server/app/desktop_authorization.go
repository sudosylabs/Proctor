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
	Create(context.Context, *model.BrowserAuthenticationTransaction) (*model.BrowserAuthenticationTransaction, error)
	IssueCode(context.Context, *store.DesktopAuthorizationCodeIssue) (*model.BrowserAuthenticationTransaction, error)
	Cancel(context.Context, *store.DesktopAuthorizationCancellation) (*model.BrowserAuthenticationTransaction, error)
	Exchange(context.Context, *store.DesktopAuthorizationExchange) (*store.DesktopAuthorizationExchangeResult, error)
}

type desktopAuthorizationAccessPolicy interface {
	AllowsDesktopAuthorization(context.Context, string, string) (bool, error)
}

type DesktopAuthorizationPolicy struct {
	Issuer                       string
	AllowLoopbackHTTPDevelopment bool
	TransactionLifetime          time.Duration
	CodeLifetime                 time.Duration
}

type desktopAuthorizationAuditor interface {
	PrepareIssue(context.Context, Invocation, model.InstitutionID) (*model.AuditEvent, error)
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
	transactions  desktopAuthorizationStore
	institutions  store.InstitutionStore
	accessPolicy  desktopAuthorizationAccessPolicy
	capabilities  accessPolicyCapabilitySource
	audit         desktopAuthorizationAuditor
	attempts      desktopAuthorizationAttemptLimiter
	sessionPolicy SessionPolicy
	policy        DesktopAuthorizationPolicy
	newCredential func() string
	now           func() time.Time
}

type StartDesktopAuthorizationCommand struct {
	CallbackURL          string
	State                string
	CodeChallenge        string
	AuthenticationMethod string
	ProviderID           string
	DeviceID             string
	DeviceName           string
	Source               string
}

type DesktopAuthorizationStart struct {
	AuthorizationURL string
	ExpiresAt        int64
}

type ApproveDesktopAuthorizationCommand struct{ Handle, BrowserProof, State string }
type DesktopAuthorizationApproval struct {
	RedirectURL string
	ExpiresAt   int64
}
type ExchangeDesktopAuthorizationCommand struct{ Code, State, CodeVerifier, Source string }
type DesktopAuthorizationExchangeResult struct {
	Session *model.Session
	Tokens  *model.AuthenticationTokens
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
	if err := model.ValidateDesktopAuthorizationIssuer(policy.Issuer, policy.AllowLoopbackHTTPDevelopment); err != nil {
		return nil, errors.New("desktop authorization issuer policy is invalid")
	}
	if sessionPolicy.AccessTTL <= 0 || sessionPolicy.RefreshTTL <= 0 || sessionPolicy.IdleTTL <= 0 ||
		sessionPolicy.AbsoluteTTL <= 0 || sessionPolicy.MaximumPerUser < 1 {
		return nil, errors.New("desktop authorization session policy is invalid")
	}
	return &desktopAuthorizationService{
		transactions: transactions, institutions: institutions, accessPolicy: accessPolicy,
		capabilities: capabilities, audit: audit, attempts: attempts, sessionPolicy: sessionPolicy,
		policy: policy, newCredential: newCredential, now: now,
	}, nil
}

func (s *desktopAuthorizationService) Approve(ctx context.Context, invocation Invocation, command ApproveDesktopAuthorizationCommand) (*DesktopAuthorizationApproval, error) {
	principal := invocation.Principal()
	if principal.Validate() != nil || principal.CredentialType != model.CredentialSessionAccess ||
		principal.ClientType != model.SessionClientWeb || !model.IsValidCredentialToken(command.Handle) ||
		!model.IsValidCredentialToken(command.BrowserProof) || !model.IsValidCredentialToken(command.State) {
		return nil, NewError("authentication.desktop_authorization.invalid")
	}
	institution, err := s.institutions.GetSingleton(ctx)
	if err != nil || institution == nil || !institution.ID.IsValid() {
		return nil, NewError("authentication.desktop_authorization.unavailable").Wrap(err)
	}
	audit, err := s.audit.PrepareIssue(ctx, invocation, institution.ID)
	if err != nil || audit == nil || !audit.ID.IsValid() {
		return nil, NewError("audit.unavailable").Wrap(err)
	}
	code := s.newCredential()
	if !model.IsValidCredentialToken(code) {
		return nil, NewError("authentication.desktop_authorization.unavailable")
	}
	at := model.TimeUTC(s.now())
	issued, err := s.transactions.IssueCode(ctx, &store.DesktopAuthorizationCodeIssue{
		HandleHash: model.HashToken(command.Handle), BrowserProofHash: model.HashToken(command.BrowserProof),
		StateHash: model.HashToken(command.State), UserID: principal.UserID,
		AuthenticationMethod: principal.AuthenticationMethod, AuthenticationProviderID: principal.AuthenticationProviderID,
		ExternalIdentityID:     principal.ExternalIdentityID,
		AuthenticationStrength: principal.AuthenticationStrength, AuthenticatedAt: principal.AuthenticatedAt.UnixMilli(),
		MFACompletedAt: principal.MFACompletedAt.Millis(), CodeHash: model.HashToken(code),
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
	if issued == nil || issued.Validate() != nil || issued.State != model.BrowserAuthenticationStateCodeIssued {
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
	return &DesktopAuthorizationApproval{RedirectURL: redirect, ExpiresAt: issued.CodeExpiresAt.Millis()}, nil
}

func (s *desktopAuthorizationService) Cancel(ctx context.Context, command ApproveDesktopAuthorizationCommand) error {
	if !model.IsValidCredentialToken(command.Handle) || !model.IsValidCredentialToken(command.BrowserProof) || !model.IsValidCredentialToken(command.State) {
		return NewError("authentication.desktop_authorization.invalid")
	}
	_, err := s.transactions.Cancel(ctx, &store.DesktopAuthorizationCancellation{HandleHash: model.HashToken(command.Handle),
		BrowserProofHash: model.HashToken(command.BrowserProof), StateHash: model.HashToken(command.State), CancelledAt: s.now().UnixMilli()})
	if err != nil {
		return desktopAuthorizationStoreError(err)
	}
	return nil
}

func (s *desktopAuthorizationService) Exchange(ctx context.Context, invocation Invocation, command ExchangeDesktopAuthorizationCommand) (*DesktopAuthorizationExchangeResult, error) {
	if err := s.attempts.Check(ctx, desktopAuthorizationAttemptExchange, model.HashToken(command.Code), command.Source); err != nil {
		return nil, err
	}
	if !model.IsValidCredentialToken(command.Code) || !model.IsValidCredentialToken(command.State) || !model.IsValidPKCEVerifier(command.CodeVerifier) {
		return nil, NewError("authentication.desktop_authorization.invalid")
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
		CodeHash: model.HashToken(command.Code), StateHash: model.HashToken(command.State),
		CodeChallenge: model.PKCES256Challenge(command.CodeVerifier), Issuer: strings.TrimSuffix(s.policy.Issuer, "/"),
		AccessTokenHash: model.HashToken(accessToken), RefreshTokenHash: model.HashToken(refreshToken),
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
	accessExpiry, refreshExpiry := desktopAuthorizationCredentialExpiries(result)
	if result == nil || result.Session == nil || accessExpiry.IsZero() || refreshExpiry.IsZero() {
		failure := NewError("authentication.desktop_authorization.unavailable")
		if auditErr := s.audit.Fail(ctx, audit.ID.String(), failure.Code()); auditErr != nil {
			return nil, NewError("audit.unavailable").Wrap(auditErr)
		}
		return nil, failure
	}
	return &DesktopAuthorizationExchangeResult{Session: result.Session, Tokens: &model.AuthenticationTokens{AccessToken: accessToken,
		RefreshToken: refreshToken, AccessExpiresAt: accessExpiry, RefreshExpiresAt: refreshExpiry}}, nil
}

func desktopAuthorizationCredentialExpiries(result *store.DesktopAuthorizationExchangeResult) (time.Time, time.Time) {
	if result == nil {
		return time.Time{}, time.Time{}
	}
	var access, refresh time.Time
	for _, credential := range result.Credentials {
		if credential == nil {
			continue
		}
		switch credential.Kind {
		case model.SessionCredentialAccess:
			access = credential.ExpiresAt
		case model.SessionCredentialRefresh:
			refresh = credential.ExpiresAt
		}
	}
	return access, refresh
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

func (a desktopAuthorizationAuditAdapter) PrepareIssue(ctx context.Context, invocation Invocation, institutionID model.InstitutionID) (*model.AuditEvent, error) {
	if a.audit == nil || !institutionID.IsValid() {
		return nil, NewError("audit.unavailable")
	}
	return a.audit.BeginCriticalActionAtScope(ctx, invocation.Principal(), model.Action("authentication.desktop_authorization"),
		model.Resource{Type: model.ResourceInstitution, ID: institutionID.String()}, model.RoleScopeInstitution,
		institutionID.String(), invocation.RequestMetadata(), map[string]any{"operation": "issue_code"}, nil)
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
		len(command.DeviceID) > model.SessionDeviceIdMaxLength ||
		utf8.RuneCountInString(command.DeviceName) > model.SessionDeviceNameMaxRunes {
		return nil, NewError("authentication.desktop_authorization.invalid")
	}
	if !s.configuredAuthenticationPath(command.AuthenticationMethod, command.ProviderID) {
		return nil, NewError("authentication.desktop_authorization.rejected")
	}
	enabled, err := s.accessPolicy.AllowsDesktopAuthorization(ctx, command.AuthenticationMethod, command.ProviderID)
	if err != nil {
		return nil, NewError("authentication.desktop_authorization.unavailable").Wrap(err)
	}
	if !enabled {
		return nil, NewError("authentication.desktop_authorization.disabled")
	}
	institution, err := s.institutions.GetSingleton(ctx)
	if err != nil {
		return nil, NewError("authentication.desktop_authorization.unavailable").Wrap(err)
	}
	handle := s.newCredential()
	proof := s.newCredential()
	if !model.IsValidCredentialToken(handle) || !model.IsValidCredentialToken(proof) {
		return nil, NewError("authentication.desktop_authorization.unavailable")
	}
	at := model.TimeUTC(s.now())
	transaction := &model.BrowserAuthenticationTransaction{
		Purpose:       model.BrowserAuthenticationPurposeDesktopAuthorization,
		InstitutionID: institution.ID, Issuer: strings.TrimSuffix(s.policy.Issuer, "/"),
		HandleHash: model.HashToken(handle), BrowserProofHash: model.HashToken(proof),
		StateHash: model.HashToken(command.State), CallbackURL: command.CallbackURL,
		CodeChallenge: command.CodeChallenge, ClientType: model.SessionClientDesktop,
		ExpectedAuthenticationMethod: command.AuthenticationMethod,
		ExpectedProviderID:           command.ProviderID,
		DeviceID:                     command.DeviceID, DeviceName: command.DeviceName,
		ExpiresAt: at.Add(s.policy.TransactionLifetime),
	}
	transaction.PrepareCreate(model.NewBrowserAuthenticationTransactionID(), at)
	if transaction.Validate() != nil {
		return nil, NewError("authentication.desktop_authorization.invalid")
	}
	saved, err := s.transactions.Create(ctx, transaction)
	if err != nil {
		return nil, NewError("authentication.desktop_authorization.unavailable").Wrap(err)
	}
	if saved == nil || saved.Validate() != nil {
		return nil, NewError("authentication.desktop_authorization.unavailable")
	}
	authorizationURL, err := desktopAuthorizationURL(saved.Issuer, handle, proof, command.State)
	if err != nil {
		return nil, NewError("authentication.desktop_authorization.unavailable").Wrap(err)
	}
	return &DesktopAuthorizationStart{
		AuthorizationURL: authorizationURL,
		ExpiresAt:        saved.ExpiresAt.UnixMilli(),
	}, nil
}

func (s *desktopAuthorizationService) configuredAuthenticationPath(method, providerID string) bool {
	if method == "password" {
		return providerID == ""
	}
	for _, provider := range s.capabilities.Snapshot().Providers {
		if provider.Descriptor.Id == providerID && provider.Descriptor.Type == method {
			return true
		}
	}
	return false
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
