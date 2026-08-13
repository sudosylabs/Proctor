// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"reflect"
	"strings"
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
	attempts, err := newAuthenticationAttemptAccounting(newAuthenticationCacheFake())
	if err != nil {
		t.Fatal(err)
	}
	service := &externalAuthenticationService{
		registry:    externalProviderSourceFake{provider: externalProviderFake{}},
		loginStates: states,
		attempts:    attempts,
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
		attempts:    newExternalAuthenticationAttempts(t, newAuthenticationCacheFake()),
		issuer:      externalSessionIssuerFake{},
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
		{"attempt accounting", func(a *externalAuthenticationConstructorArgs) { a.attempts = nil }},
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
	attempts      *authenticationAttemptAccounting
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
		a.attempts, a.issuer, a.invalidator, a.audit, ExternalAuthenticationPolicy{},
		a.diagnostics, a.newCredential, a.now,
	)
}

func TestExternalAuthenticationInitiationUsesProviderScopedSourceAccounting(t *testing.T) {
	t.Parallel()

	cache := newAuthenticationCacheFake()
	provider := &recordingExternalProvider{}
	service := externalAuthenticationBeginService(
		t,
		externalProviderSourceSet{provider: provider, ids: map[string]bool{
			"campus-a": true,
			"campus-b": true,
		}},
		cache,
		1,
	)

	if _, err := service.begin(
		context.Background(), "campus-a", "/", model.SessionClientWeb,
		"", "", "192.0.2.10:4000",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := service.begin(
		context.Background(), " CAMPUS-A ", "/", model.SessionClientWeb,
		"", "", "192.0.2.10:5000",
	); !Is(err, "authentication.rate_limited") {
		t.Fatalf("normalized-source threshold error = %v", err)
	}
	if _, err := service.begin(
		context.Background(), "campus-b", "/", model.SessionClientWeb,
		"", "", "192.0.2.10:6000",
	); err != nil {
		t.Fatalf("provider-isolated attempt failed: %v", err)
	}

	cache.mu.Lock()
	defer cache.mu.Unlock()
	if len(cache.counters) != 2 {
		t.Fatalf("counter keys = %d, want 2", len(cache.counters))
	}
	for key := range cache.counters {
		if !strings.HasPrefix(key, "authentication/attempts/external-authentication/source/") {
			t.Fatalf("unexpected counter key %q", key)
		}
		if strings.Contains(key, "campus") || strings.Contains(key, "192.0.2.10") {
			t.Fatalf("counter key exposes provider or source: %q", key)
		}
	}
}

func TestExternalAuthenticationInitiationAllowsMaximumThenLimitsNext(t *testing.T) {
	t.Parallel()

	provider := &recordingExternalProvider{}
	service := externalAuthenticationBeginService(
		t,
		externalProviderSourceSet{provider: provider, ids: map[string]bool{"campus": true}},
		newAuthenticationCacheFake(),
		2,
	)
	for attempt := 1; attempt <= 2; attempt++ {
		if _, err := service.begin(
			context.Background(), "campus", "/", model.SessionClientWeb,
			"", "", "198.51.100.7",
		); err != nil {
			t.Fatalf("attempt %d failed: %v", attempt, err)
		}
	}
	if _, err := service.begin(
		context.Background(), "campus", "/", model.SessionClientWeb,
		"", "", "198.51.100.7",
	); !Is(err, "authentication.rate_limited") {
		t.Fatalf("attempt beyond maximum error = %v", err)
	}
	if provider.beginCalls != 2 {
		t.Fatalf("provider Begin calls = %d, want 2", provider.beginCalls)
	}
}

func TestExternalAuthenticationInitiationPreservesValidationAndFailureOrdering(t *testing.T) {
	t.Parallel()

	cache := &externalAuthenticationAttemptFaultCache{
		authenticationCacheFake: newAuthenticationCacheFake(),
		err:                     errors.New("counter unavailable"),
	}
	provider := &recordingExternalProvider{}
	service := externalAuthenticationBeginService(
		t,
		externalProviderSourceSet{provider: provider, ids: map[string]bool{"campus": true}},
		cache,
		2,
	)

	if _, err := service.begin(
		context.Background(), "missing", "/", model.SessionClientWeb,
		"", "", "203.0.113.8",
	); !Is(err, "authentication.external.provider_not_found") {
		t.Fatalf("missing-provider error = %v", err)
	}
	if cache.addCalls != 0 {
		t.Fatalf("provider validation occurred after %d counter calls", cache.addCalls)
	}
	if _, err := service.begin(
		context.Background(), "campus", "https://unsafe.example", model.SessionClientWeb,
		"", "", "203.0.113.8",
	); !Is(err, "authentication.external.request.invalid") {
		t.Fatalf("invalid-request error = %v", err)
	}
	if cache.addCalls != 0 {
		t.Fatalf("request validation occurred after %d counter calls", cache.addCalls)
	}
	if _, err := service.begin(
		context.Background(), "campus", "/", model.SessionClientWeb,
		"", "", "203.0.113.8",
	); !Is(err, "authentication.rate_limit_unavailable") {
		t.Fatalf("counter failure error = %v", err)
	}
	if provider.beginCalls != 0 {
		t.Fatalf("provider began before accounting succeeded")
	}
}

func externalAuthenticationBeginService(
	t *testing.T,
	registry externalProviderSource,
	cache authenticationAttemptCache,
	maximum int,
) *externalAuthenticationService {
	t.Helper()
	attempts := newExternalAuthenticationAttempts(t, cache)
	return &externalAuthenticationService{
		registry:    registry,
		loginStates: &externalLoginStateStoreFake{},
		attempts:    attempts,
		policy: ExternalAuthenticationPolicy{
			PublicURL:     "https://proctor.example.test",
			LoginStateTTL: 10 * time.Minute,
			LoginRateLimit: LoginRateLimitPolicy{
				Window: time.Minute, MaximumSourceAttempts: maximum,
			},
		},
		newCredential: model.NewCredentialToken,
		now:           time.Now,
	}
}

func newExternalAuthenticationAttempts(
	t *testing.T,
	cache authenticationAttemptCache,
) *authenticationAttemptAccounting {
	t.Helper()
	attempts, err := newAuthenticationAttemptAccounting(cache)
	if err != nil {
		t.Fatal(err)
	}
	return attempts
}

type externalProviderSourceSet struct {
	provider ExternalIdentityProvider
	ids      map[string]bool
}

func (s externalProviderSourceSet) Descriptors() []model.ExternalAuthenticationProvider { return nil }
func (s externalProviderSourceSet) Provider(id string) (ExternalIdentityProvider, bool) {
	return s.provider, s.ids[id]
}

type recordingExternalProvider struct{ beginCalls int }

func (*recordingExternalProvider) Descriptor() model.ExternalAuthenticationProvider {
	return model.ExternalAuthenticationProvider{Id: "campus", Type: "oidc"}
}
func (*recordingExternalProvider) AutoProvision() bool { return false }
func (p *recordingExternalProvider) Begin(
	context.Context,
	ExternalProviderBeginRequest,
) (*ExternalProviderBeginResponse, error) {
	p.beginCalls++
	return &ExternalProviderBeginResponse{RedirectURL: "https://identity.example.test/login"}, nil
}
func (*recordingExternalProvider) State(model.ExternalAuthenticationCallback) (string, error) {
	return "", nil
}
func (*recordingExternalProvider) Complete(
	context.Context,
	ExternalProviderCompleteRequest,
) (*model.ExternalAuthenticationAssertion, error) {
	return nil, nil
}

type externalAuthenticationAttemptFaultCache struct {
	*authenticationCacheFake
	err      error
	addCalls int
}

func (c *externalAuthenticationAttemptFaultCache) Add(
	context.Context,
	string,
	int64,
	time.Duration,
) (int64, error) {
	c.addCalls++
	return 0, c.err
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
