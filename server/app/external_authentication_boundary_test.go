// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestExternalAuthenticationRetainsExactPersistenceContracts(t *testing.T) {
	t.Parallel()

	root := reflect.TypeOf((*store.Store)(nil)).Elem()
	typeOfService := reflect.TypeOf(externalAuthenticationService{})
	for index := 0; index < typeOfService.NumField(); index++ {
		if typeOfService.Field(index).Type == root {
			t.Fatalf("externalAuthenticationService retains root Store in %q", typeOfService.Field(index).Name)
		}
	}
}

func TestExternalAuthenticationBeginUsesControlledCredentials(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	stateToken := "MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTIzNDU2Nzg5MDE"
	bindingToken := "YWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXpBQkNERUY"
	generated := []string{stateToken, bindingToken}
	next := 0
	states := &externalLoginStateStoreFake{}
	service := &externalAuthenticationService{
		registry:    externalProviderSourceFake{provider: externalProviderFake{}},
		loginStates: states,
		cache:       newAuthenticationCacheFake(),
		policy: ExternalAuthenticationPolicy{
			PublicURL: "https://proctor.example.test", LoginStateTTL: 10 * time.Minute,
			LoginRateLimit: LoginRateLimitPolicy{Window: time.Minute, MaximumSourceAttempts: 10},
		},
		newCredential: func() string { value := generated[next]; next++; return value },
		now:           func() time.Time { return at },
	}

	result, err := service.begin(
		context.Background(), "campus", "/after", model.SessionClientWeb,
		"browser", "Browser", "127.0.0.1",
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Binding != bindingToken || states.saved == nil ||
		states.saved.StateHash != model.HashToken(stateToken) ||
		states.saved.BindingHash != model.HashToken(bindingToken) ||
		states.saved.ExpiresAt.UnixMilli() != at.Add(10*time.Minute).UnixMilli() {
		t.Fatalf("result=%#v saved=%#v", result, states.saved)
	}
}

func TestExternalAuthenticationRequiresFocusedDependencies(t *testing.T) {
	t.Parallel()

	valid := externalAuthenticationConstructorArgs{
		registry:    externalProviderSourceFake{provider: externalProviderFake{}},
		loginStates: &externalLoginStateStoreFake{}, institutions: externalInstitutionStoreFake{},
		identities: externalIdentityStoreFake{}, sessions: externalSessionStoreFake{},
		cache: newAuthenticationCacheFake(), issuer: externalSessionIssuerFake{},
		invalidator: externalInvalidatorFake{}, audit: externalAuditFake{},
		diagnostics: &securityEffectsDiagnosticsFake{}, newCredential: model.NewCredentialToken,
		now: time.Now,
	}
	tests := []struct {
		name  string
		clear func(*externalAuthenticationConstructorArgs)
	}{
		{"login states", func(a *externalAuthenticationConstructorArgs) { a.loginStates = nil }},
		{"institutions", func(a *externalAuthenticationConstructorArgs) { a.institutions = nil }},
		{"identities", func(a *externalAuthenticationConstructorArgs) { a.identities = nil }},
		{"sessions", func(a *externalAuthenticationConstructorArgs) { a.sessions = nil }},
		{"generator", func(a *externalAuthenticationConstructorArgs) { a.newCredential = nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			test.clear(&candidate)
			if _, err := candidate.build(); err == nil {
				t.Fatalf("missing %s accepted", test.name)
			}
		})
	}
}

type externalAuthenticationConstructorArgs struct {
	registry      externalProviderSource
	loginStates   store.ExternalLoginStateStore
	institutions  store.InstitutionStore
	identities    store.ExternalIdentityStore
	sessions      store.SessionStore
	cache         authenticationCache
	issuer        authenticationSessionIssuer
	invalidator   authenticationInvalidator
	audit         externalAuthenticationAudit
	diagnostics   authenticationDiagnostics
	newCredential func() string
	now           func() time.Time
}

func (a externalAuthenticationConstructorArgs) build() (*externalAuthenticationService, error) {
	return newExternalAuthenticationService(
		a.registry, a.loginStates, a.institutions, a.identities, a.sessions,
		a.cache, a.issuer, a.invalidator, a.audit, ExternalAuthenticationPolicy{},
		a.diagnostics, a.newCredential, a.now,
	)
}

type externalProviderSourceFake struct{ provider ExternalIdentityProvider }

func (s externalProviderSourceFake) Descriptors() []model.ExternalAuthenticationProvider { return nil }
func (s externalProviderSourceFake) Provider(id string) (ExternalIdentityProvider, bool) {
	return s.provider, id == "campus"
}

type externalProviderFake struct{}

func (externalProviderFake) Descriptor() model.ExternalAuthenticationProvider {
	return model.ExternalAuthenticationProvider{Id: "campus", Type: "oidc"}
}
func (externalProviderFake) AutoProvision() bool { return false }
func (externalProviderFake) Begin(context.Context, ExternalProviderBeginRequest) (*ExternalProviderBeginResponse, error) {
	return &ExternalProviderBeginResponse{RedirectURL: "https://identity.example.test/login"}, nil
}
func (externalProviderFake) State(model.ExternalAuthenticationCallback) (string, error) {
	return "", nil
}
func (externalProviderFake) Complete(context.Context, ExternalProviderCompleteRequest) (*model.ExternalAuthenticationAssertion, error) {
	return nil, nil
}

type externalLoginStateStoreFake struct {
	store.ExternalLoginStateStore
	saved *model.ExternalLoginState
}

type externalInstitutionStoreFake struct{ store.InstitutionStore }
type externalIdentityStoreFake struct{ store.ExternalIdentityStore }
type externalSessionStoreFake struct{ store.SessionStore }

type externalSessionIssuerFake struct{}

func (externalSessionIssuerFake) createSession(context.Context, sessionIssuance) (*model.Session, *model.AuthenticationTokens, error) {
	return nil, nil, nil
}

type externalInvalidatorFake struct{}

func (externalInvalidatorFake) InvalidateAccessCredentials(context.Context, []string) {}
func (externalInvalidatorFake) InvalidateSessionActivity(context.Context, []string)   {}

type externalAuditFake struct{}

func (externalAuditFake) BeginAuthentication(context.Context, string, string, string, model.SessionClientType, model.RequestMetadata, string) (*model.AuditEvent, error) {
	return nil, nil
}
func (externalAuditFake) RecordExternalAuthenticationFailure(context.Context, string, string, model.RequestMetadata, string, string) error {
	return nil
}
func (externalAuditFake) CompleteCriticalAction(context.Context, string, model.AuditStatus, string, any) (*model.AuditEvent, error) {
	return nil, nil
}

func (s *externalLoginStateStoreFake) Save(_ context.Context, state *model.ExternalLoginState) (*model.ExternalLoginState, error) {
	s.saved = state
	return state, nil
}
