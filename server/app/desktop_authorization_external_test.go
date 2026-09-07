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
	// The facade has only the external-authentication module. It cannot reach
	// through a sibling's Store to initiate this purpose-specific handoff.
	application := &App{externalAuthentication: service}
	result, err := application.BeginDesktopExternalAuthentication(t.Context(), Invocation{}, BeginDesktopExternalAuthenticationCommand{
		ProviderID: "campus", Binding: binding, State: state, Source: "192.0.2.10",
	})
	if err != nil || result == nil {
		t.Fatalf("begin Desktop external authentication = %#v, %v", result, err)
	}
	saved := service.loginStates.(*externalLoginStateStoreFake).saved
	if saved == nil || saved.Purpose != model.ExternalAuthenticationPurposeDesktopAuthorization ||
		saved.BrowserAuthenticationTransactionID != transactionID || saved.ClientType != model.SessionClientWeb ||
		saved.ReturnTo != "/authorize/desktop?state="+state || saved.TargetUserID != "" || saved.InvitationID != "" {
		t.Fatalf("Desktop login state = %#v", saved)
	}
	if transactions.contextCalls != 1 || transactions.contextBinding != model.HashToken(binding) {
		t.Fatalf("Desktop lookup calls = %d, binding = %q", transactions.contextCalls, transactions.contextBinding)
	}
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
			application := &App{externalAuthentication: service}
			result, err := application.BeginDesktopExternalAuthentication(t.Context(), Invocation{}, BeginDesktopExternalAuthenticationCommand{
				ProviderID: "campus", Binding: test.binding, State: test.state, Source: "192.0.2.10",
			})
			if result != nil || !Is(err, test.code) {
				t.Fatalf("begin = %#v, %v; want %s", result, err, test.code)
			}
			if test.transactions.contextCalls != test.lookups || provider.beginCalls != 0 || service.loginStates.(*externalLoginStateStoreFake).saved != nil {
				t.Fatal("invalid Desktop proof reached provider or login-state persistence")
			}
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
			assertion := &model.ExternalAuthenticationAssertion{ProviderId: "campus", Subject: identity.Subject, Email: user.Email,
				EmailVerified: true, AuthenticationStrength: model.AuthenticationMultiFactor, AuthenticatedAt: at.UnixMilli()}
			service.registry = externalProviderSourceFake{provider: providerConnectionProvider{state: stateToken, assertion: assertion}}
			service.loginStates = &externalLoginStateStoreFake{get: state, consumeResult: state, storeNow: at}
			service.identities, service.authentication, service.invitationAcceptor = identities, issuer, acceptor
			service.accessPolicy = authenticationAccessPolicyFake{providers: map[string]bool{"campus": true},
				providerModes: map[string]model.ProviderAdmissionMode{"campus": model.ProviderAdmissionInvitationRequired}}
			service.audit, service.now = externalHandoffAudit{}, func() time.Time { return at }
			application := &App{externalAuthentication: service}
			result, err := application.CompleteExternalAuthentication(t.Context(), Invocation{}, CompleteExternalAuthenticationCommand{
				ProviderID: "campus", Binding: binding,
			})
			if err != nil || result == nil || result.ReturnTo != state.ReturnTo {
				t.Fatalf("complete %s = %#v, %v", purpose, result, err)
			}
			if purpose == model.ExternalAuthenticationPurposeDesktopAuthorization {
				proof := transactions.authenticated
				if proof == nil || proof.TransactionID != state.BrowserAuthenticationTransactionID || proof.UserID != user.ID ||
					proof.ExternalIdentityID != identity.ID || proof.AuthenticationProviderID != "campus" || proof.AuthenticationMethod != "oidc" ||
					proof.AuthenticatedAt != at.UnixMilli() || proof.MFACompletedAt != at.UnixMilli() {
					t.Fatalf("Desktop authentication proof = %#v", proof)
				}
			} else if transactions.authenticated != nil {
				t.Fatalf("%s authenticated a Desktop transaction", purpose)
			}
			if purpose == model.ExternalAuthenticationPurposeLogin {
				if issuer.calls != 1 || result.Session != issuer.session || result.Tokens != issuer.tokens {
					t.Fatalf("ordinary login did not issue its Web Session: %#v", result)
				}
			} else if issuer.calls != 0 || result.Session != nil || result.Tokens != nil {
				t.Fatalf("%s created an ordinary Session", purpose)
			}
			if (identities.linkCalls == 1) != (purpose == model.ExternalAuthenticationPurposeConnect) ||
				(acceptor.state != nil) != (purpose == model.ExternalAuthenticationPurposeInvitationAdmission) {
				t.Fatal("callback crossed a different authentication purpose")
			}
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
	if err != nil {
		t.Fatal(err)
	}
	service, err := (externalAuthenticationConstructorArgs{
		registry: externalProviderSourceFake{provider: externalProviderFake{}}, loginStates: &externalLoginStateStoreFake{},
		institutions: providerConnectionInstitutionStoreFake{institution: &model.Institution{ID: model.NewInstitutionID()}},
		identities:   externalIdentityStoreFake{}, sessions: externalSessionStoreFake{},
		attempts: newExternalAuthenticationAttempts(t, newAuthenticationCacheFake()), issuer: externalSessionIssuerFake{},
		invalidator: externalInvalidatorFake{}, audit: externalAuditFake{}, diagnostics: &securityEffectsDiagnosticsFake{},
		acceptor: &externalInvitationAcceptorFake{}, desktop: desktop, newCredential: model.NewCredentialToken, now: time.Now,
	}).build()
	if err != nil {
		t.Fatal(err)
	}
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

func (s *externalHandoffIdentityStore) LinkWithAudit(context.Context, *store.ExternalIdentityLink) (*store.AuthenticationMethodMutationResult, error) {
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
