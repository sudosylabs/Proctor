// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestDesktopExternalAuthenticationStartUsesBoundTransactionOwner(t *testing.T) {
	t.Parallel()
	transactionID := model.NewBrowserAuthenticationTransactionID()
	binding, state := model.NewCredentialToken(), model.NewCredentialToken()
	transactions := &desktopAuthorizationStoreFake{contextResult: &store.DesktopAuthorizationContext{
		ID: transactionID, State: model.BrowserAuthenticationStateBound,
	}}
	service := newDesktopExternalAuthenticationFixture(t, transactions)
	// This App embeds only the external-authentication module. It cannot reach
	// through a sibling's Store to initiate this purpose-specific handoff.
	application := &App{externalAuthenticationService: service}
	result, err := application.BeginDesktopExternalAuthentication(t.Context(), invocation.Invocation{}, BeginDesktopExternalAuthenticationCommand{
		ProviderID: "campus", Binding: binding, State: state, Source: "192.0.2.10",
	})
	require.NoError(t, err, "begin Desktop external authentication")
	require.NotNil(t, result, "begin Desktop external authentication")
	saved := service.loginStates.(*externalLoginStateStoreFake).saved
	require.NotNil(t, saved)
	require.Equal(t, model.ExternalAuthenticationPurposeDesktopAuthorization, saved.Purpose)
	require.Equal(t, transactionID, saved.BrowserAuthenticationTransactionID)
	require.Equal(t, model.SessionClientWeb, saved.ClientType)
	require.Equal(t, "/authorize/desktop?state="+state, saved.ReturnTo)
	require.Empty(t, saved.TargetUserID)
	require.Empty(t, saved.InvitationID)
	require.Equal(t, 1, transactions.contextCalls, "Desktop lookup calls")
	require.Equal(t, model.HashToken(binding), transactions.contextBinding, "Desktop lookup calls")
}

func TestDesktopExternalAuthenticationRejectsUnboundProofBeforeProvider(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name         string
		binding      string
		state        string
		transactions desktopAuthorizationStoreFake
		code         string
		lookups      int
	}{
		{name: "invalid binding", binding: "invalid", state: model.NewCredentialToken(), code: "authentication.desktop_authorization.invalid"},
		{name: "invalid state", binding: model.NewCredentialToken(), state: "invalid", code: "authentication.desktop_authorization.invalid"},
		{name: "wrong purpose or expired", binding: model.NewCredentialToken(), state: model.NewCredentialToken(),
			transactions: desktopAuthorizationStoreFake{contextErr: store.NewErrNotFound("browser_authentication_transaction", "")},
			code:         "authentication.desktop_authorization.rejected", lookups: 1},
		{name: "missing context", binding: model.NewCredentialToken(), state: model.NewCredentialToken(),
			transactions: desktopAuthorizationStoreFake{contextNil: true}, code: "authentication.desktop_authorization.unavailable", lookups: 1},
		{name: "invalid transaction", binding: model.NewCredentialToken(), state: model.NewCredentialToken(),
			transactions: desktopAuthorizationStoreFake{contextResult: &store.DesktopAuthorizationContext{State: model.BrowserAuthenticationStateBound}},
			code:         "authentication.desktop_authorization.unavailable", lookups: 1},
		{name: "already authenticated", binding: model.NewCredentialToken(), state: model.NewCredentialToken(),
			transactions: desktopAuthorizationStoreFake{contextResult: &store.DesktopAuthorizationContext{
				ID: model.NewBrowserAuthenticationTransactionID(), State: model.BrowserAuthenticationStateAuthenticated}},
			code: "authentication.desktop_authorization.unavailable", lookups: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := newDesktopExternalAuthenticationFixture(t, &test.transactions)
			provider := &recordingExternalProvider{}
			service.registry = externalProviderSourceFake{provider: provider}
			application := &App{externalAuthenticationService: service}
			result, err := application.BeginDesktopExternalAuthentication(t.Context(), invocation.Invocation{}, BeginDesktopExternalAuthenticationCommand{
				ProviderID: "campus", Binding: test.binding, State: test.state, Source: "192.0.2.10",
			})
			require.Nil(t, result, "begin = %#v, %v; want %s", result, err, test.code)
			require.True(t, apperror.Is(err, test.code), "begin = %#v, %v; want %s", result, err, test.code)
			require.Equal(t, test.lookups, test.transactions.contextCalls, "invalid Desktop proof reached provider or login-state persistence")
			require.Equal(t, 0, provider.beginCalls, "invalid Desktop proof reached provider or login-state persistence")
			require.Nil(t, service.loginStates.(*externalLoginStateStoreFake).saved, "invalid Desktop proof reached provider or login-state persistence")
		})
	}
}

func TestExternalAuthenticationCompletionIsolatesDesktopPurpose(t *testing.T) {
	t.Parallel()
	for _, purpose := range []model.ExternalAuthenticationPurpose{
		model.ExternalAuthenticationPurposeLogin, model.ExternalAuthenticationPurposeDesktopAuthorization,
		model.ExternalAuthenticationPurposeConnect, model.ExternalAuthenticationPurposeInvitationAdmission,
	} {
		t.Run(string(purpose), func(t *testing.T) {
			at := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
			transactions := &desktopAuthorizationStoreFake{}
			service := newDesktopExternalAuthenticationFixture(t, transactions)
			user := &model.User{ID: model.NewUserID(), Username: "student", Email: "student@example.edu"}
			identity := &model.ExternalIdentity{ID: model.NewExternalIdentityID(), UserID: user.ID, Provider: "campus", Subject: "opaque-subject"}
			identities := &externalHandoffIdentityStore{user: user, identity: identity}
			issuer := &externalHandoffSessionIssuer{session: &model.Session{ID: model.NewSessionID(), UserID: user.ID}, tokens: &model.AuthenticationTokens{}}
			acceptor := &externalInvitationAcceptorFake{result: &store.ExternalIdentityInvitationAcceptanceResult{User: user, Identity: identity}}
			stateToken, binding := model.NewCredentialToken(), model.NewCredentialToken()
			state := &model.ExternalLoginState{Provider: "campus", Purpose: purpose,
				StateHash: model.HashToken(stateToken), BindingHash: model.HashToken(binding), ClientType: model.SessionClientWeb,
				ReturnTo: "/authorize/desktop?state=" + model.NewCredentialToken(), ExpiresAt: at.Add(time.Minute)}
			switch purpose {
			case model.ExternalAuthenticationPurposeDesktopAuthorization:
				state.BrowserAuthenticationTransactionID = model.NewBrowserAuthenticationTransactionID()
			case model.ExternalAuthenticationPurposeConnect:
				state.TargetUserID, state.AuditEventID = user.ID, model.NewAuditEventID().String()
			case model.ExternalAuthenticationPurposeInvitationAdmission:
				state.InvitationID = model.NewInvitationID()
			}
			assertion := &model.ExternalAuthenticationAssertion{ProviderID: "campus", Subject: identity.Subject, Email: user.Email,
				EmailVerified: true, AuthenticationStrength: model.AuthenticationMultiFactor, AuthenticatedAt: at.UnixMilli()}
			service.registry = externalProviderSourceFake{provider: providerConnectionProvider{state: stateToken, assertion: assertion}}
			service.loginStates = &externalLoginStateStoreFake{get: state, consumeResult: state, storeNow: at}
			service.identities, service.authentication, service.invitationAcceptor = identities, issuer, acceptor
			service.accessPolicy = authenticationAccessPolicyFake{providers: map[string]bool{"campus": true},
				providerModes: map[string]model.ProviderAdmissionMode{"campus": model.ProviderAdmissionInvitationRequired}}
			service.audit, service.now = externalHandoffAudit{}, func() time.Time { return at }
			application := &App{externalAuthenticationService: service}
			result, err := application.CompleteExternalAuthentication(t.Context(), invocation.Invocation{}, CompleteExternalAuthenticationCommand{
				ProviderID: "campus", Binding: binding,
			})
			require.NoError(t, err, "complete %s", purpose)
			require.NotNil(t, result, "complete %s", purpose)
			require.Equal(t, state.ReturnTo, result.ReturnTo, "complete %s", purpose)
			if purpose == model.ExternalAuthenticationPurposeDesktopAuthorization {
				proof := transactions.authenticated
				require.NotNil(t, proof)
				require.Equal(t, state.BrowserAuthenticationTransactionID, proof.TransactionID)
				require.Equal(t, user.ID, proof.UserID)
				require.Equal(t, identity.ID, proof.ExternalIdentityID)
				require.Equal(t, "campus", proof.AuthenticationProviderID)
				require.Equal(t, "oidc", proof.AuthenticationMethod)
				require.Equal(t, at.UnixMilli(), proof.AuthenticatedAt)
				require.Equal(t, at.UnixMilli(), proof.MFACompletedAt)
			} else {
				require.Nil(t, transactions.authenticated, "%s authenticated a Desktop transaction", purpose)
			}
			if purpose == model.ExternalAuthenticationPurposeLogin {
				require.Equal(t, 1, issuer.calls)
				require.Same(t, issuer.session, result.Session)
				require.Same(t, issuer.tokens, result.Tokens)
			} else {
				require.Zero(t, issuer.calls, "%s created an ordinary Session", purpose)
				require.Nil(t, result.Session, "%s created an ordinary Session", purpose)
				require.Nil(t, result.Tokens, "%s created an ordinary Session", purpose)
			}
			require.Equal(t, purpose == model.ExternalAuthenticationPurposeConnect, identities.linkCalls == 1, "callback crossed a different authentication purpose")
			require.Equal(t, purpose == model.ExternalAuthenticationPurposeInvitationAdmission, acceptor.state != nil, "callback crossed a different authentication purpose")
		})
	}
}

func newDesktopExternalAuthenticationFixture(t *testing.T, transactions *desktopAuthorizationStoreFake) *externalAuthenticationService {
	t.Helper()
	desktop, err := newDesktopAuthorizationService(transactions,
		desktopAuthorizationInstitutionStoreFake{institution: &model.Institution{ID: model.NewInstitutionID()}},
		desktopAuthorizationAccessPolicyFake{enabled: true}, &accessPolicyCapabilitiesFake{},
		desktopAuthorizationAuditorFake{}, &desktopAuthorizationAttemptLimiterFake{}, testDesktopSessionPolicy(),
		DesktopAuthorizationPolicy{Issuer: "https://proctor.example.test"}, model.NewCredentialToken, time.Now,
		testDesktopAuthorizationIdentity(t, "https://proctor.example.test", time.Now))
	require.NoError(t, err)
	service, err := (externalAuthenticationConstructorArgs{
		registry: externalProviderSourceFake{provider: externalProviderFake{}}, loginStates: &externalLoginStateStoreFake{},
		institutions: providerConnectionInstitutionStoreFake{institution: &model.Institution{ID: model.NewInstitutionID()}},
		identities:   externalIdentityStoreFake{}, sessions: externalSessionStoreFake{},
		attempts: newExternalAuthenticationAttempts(t, newAuthenticationCacheFake()), issuer: externalSessionIssuerFake{},
		invalidator: externalInvalidatorFake{}, audit: externalAuditFake{}, diagnostics: &securityEffectsDiagnosticsFake{},
		acceptor: &externalInvitationAcceptorFake{}, desktop: desktop, newCredential: model.NewCredentialToken, now: time.Now,
	}).build()
	require.NoError(t, err)
	service.policy = ExternalAuthenticationPolicy{PublicURL: "https://proctor.example.test", LoginStateTTL: 5 * time.Minute,
		LoginRateLimit: LoginRateLimitPolicy{Window: time.Minute, MaximumSourceAttempts: 10}}
	return service
}

type externalDesktopAuthorizationFake struct{}

func (*externalDesktopAuthorizationFake) resolveExternalAuthentication(context.Context, string, string) (desktopExternalAuthenticationTarget, error) {
	return desktopExternalAuthenticationTarget{}, errors.New("unexpected Desktop authentication start")
}

func (*externalDesktopAuthorizationFake) authenticateExternal(context.Context, model.BrowserAuthenticationTransactionID,
	*model.User, *model.ExternalIdentity, string, *model.ExternalAuthenticationAssertion,
) error {
	return errors.New("unexpected Desktop authentication completion")
}

type externalHandoffIdentityStore struct {
	store.ExternalIdentityStore
	user      *model.User
	identity  *model.ExternalIdentity
	linkCalls int
}

func (s *externalHandoffIdentityStore) ResolveOrProvision(context.Context, *store.ExternalIdentityResolutionRequest) (*store.ExternalIdentityResolution, error) {
	return &store.ExternalIdentityResolution{User: s.user, Identity: s.identity}, nil
}

func (s *externalHandoffIdentityStore) Link(context.Context, *store.ExternalIdentityLink) (*store.AuthenticationMethodMutationResult, error) {
	s.linkCalls++
	return &store.AuthenticationMethodMutationResult{Identity: s.identity}, nil
}

type externalHandoffSessionIssuer struct {
	calls   int
	session *model.Session
	tokens  *model.AuthenticationTokens
}

func (s *externalHandoffSessionIssuer) createSession(context.Context, sessionIssuance) (*model.Session, *model.AuthenticationTokens, error) {
	s.calls++
	return s.session, s.tokens, nil
}

type externalHandoffAudit struct{ externalAuditFake }

func (externalHandoffAudit) BeginAuthentication(context.Context, string, string, string, model.SessionClientType, model.RequestMetadata, string) (*model.AuditEvent, error) {
	return &model.AuditEvent{ID: model.NewAuditEventID()}, nil
}

func (externalHandoffSessionIssuer) recoveryState(_ context.Context, userID model.UserID) (*model.UserMFARecovery, error) {
	return &model.UserMFARecovery{UserID: userID}, nil
}
