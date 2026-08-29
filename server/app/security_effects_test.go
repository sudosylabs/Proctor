// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestAuthenticationCacheInvalidatorRequiresDependencies(t *testing.T) {
	t.Parallel()

	if _, err := newAuthenticationCacheInvalidator(nil, &securityEffectsDiagnosticsFake{}); err == nil {
		t.Fatal("nil cache was accepted")
	}
	if _, err := newAuthenticationCacheInvalidator(newAuthenticationCacheFake(), nil); err == nil {
		t.Fatal("nil diagnostics was accepted")
	}
}

func TestRealtimeServiceRequiresSecurityDependencies(t *testing.T) {
	t.Parallel()

	invalidator, err := newAuthenticationCacheInvalidator(
		newAuthenticationCacheFake(),
		&securityEffectsDiagnosticsFake{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newRealtimeService(nil, &securityEffectsRealtimeDiagnosticsFake{}); err == nil {
		t.Fatal("nil authentication invalidator was accepted")
	}
	if _, err := newRealtimeService(invalidator, nil); err == nil {
		t.Fatal("nil realtime diagnostics were accepted")
	}
}

func TestAuthenticationServiceRequiresSecurityDependencies(t *testing.T) {
	t.Parallel()

	persistence := newAuthenticationStoreFake()
	cache := newAuthenticationCacheFake()
	hasher, err := newPasswordHasher(testPasswordPolicy())
	if err != nil {
		t.Fatal(err)
	}
	type dependencies struct {
		users              store.UserStore
		passwords          store.PasswordCredentialStore
		sessions           store.SessionStore
		sessionCredentials store.SessionCredentialStore
		attempts           *authenticationAttemptAccounting
		effects            authenticationSecurityEffects
		mfa                authenticationMFAVerifier
		personalTokens     authenticationPATResolver
		newCredential      func() string
	}
	valid := dependencies{
		users: persistence.User(), passwords: persistence.PasswordCredential(),
		sessions: persistence.Session(), sessionCredentials: persistence.SessionCredential(),
		attempts: mustAuthenticationAttemptAccounting(t, cache),
		effects:  discardAuthenticationSecurityEffects{},
		mfa:      discardAuthenticationMFAVerifier{}, personalTokens: discardAuthenticationPATResolver{},
		newCredential: model.NewCredentialToken,
	}
	desktop := testAuthenticationDesktopDependencies(t, cache)
	construct := func(deps dependencies) error {
		_, constructorErr := newAuthenticationService(
			deps.users,
			deps.passwords,
			deps.sessions,
			deps.sessionCredentials,
			allowAllAuthenticationAccessPolicy(),
			cache,
			deps.attempts,
			deps.effects,
			hasher,
			deps.mfa,
			deps.personalTokens,
			SessionPolicy{},
			LoginRateLimitPolicy{},
			&securityEffectsDiagnosticsFake{},
			deps.newCredential,
			nil,
			desktop,
		)
		return constructorErr
	}
	tests := []struct {
		name   string
		mutate func(*dependencies)
	}{
		{name: "users", mutate: func(deps *dependencies) { deps.users = nil }},
		{name: "passwords", mutate: func(deps *dependencies) { deps.passwords = nil }},
		{name: "sessions", mutate: func(deps *dependencies) { deps.sessions = nil }},
		{name: "session credentials", mutate: func(deps *dependencies) { deps.sessionCredentials = nil }},
		{name: "attempt accounting", mutate: func(deps *dependencies) { deps.attempts = nil }},
		{name: "effects", mutate: func(deps *dependencies) { deps.effects = nil }},
		{name: "MFA verifier", mutate: func(deps *dependencies) { deps.mfa = nil }},
		{name: "PAT resolver", mutate: func(deps *dependencies) { deps.personalTokens = nil }},
		{name: "credential generator", mutate: func(deps *dependencies) { deps.newCredential = nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			test.mutate(&candidate)
			if err := construct(candidate); err == nil {
				t.Fatalf("nil %s dependency was accepted", test.name)
			}
		})
	}
}

func TestExternalAuthenticationServiceRequiresInvalidator(t *testing.T) {
	t.Parallel()

	persistence := newAuthenticationStoreFake()
	authentication := newTestAuthenticationService(t, persistence)
	audit, auditErr := newAuditService(securityAuditStore{}, securityInstitutionStore{}, model.NewId())
	if auditErr != nil {
		t.Fatal(auditErr)
	}
	_, err := newExternalAuthenticationService(
		securityEffectsProviderSourceFake{},
		securityExternalLoginStateStore{},
		securityInstitutionStore{},
		securityExternalIdentityStore{},
		persistence.Session(),
		allowAllAuthenticationAccessPolicy(),
		mustAuthenticationAttemptAccounting(t, newAuthenticationCacheFake()),
		authentication,
		nil,
		audit,
		mutationAuditAdapter{audit: audit},
		&accessPolicyCapabilitiesFake{},
		&externalInvitationAcceptorFake{},
		ExternalAuthenticationPolicy{},
		15*time.Minute,
		&securityEffectsDiagnosticsFake{},
		model.NewCredentialToken,
		nil,
	)
	if err == nil {
		t.Fatal("nil authentication invalidator was accepted")
	}
}

func TestAuditServiceRequiresDependencies(t *testing.T) {
	t.Parallel()

	if _, err := newAuditService(nil, securityInstitutionStore{}, model.NewId()); err == nil {
		t.Fatal("nil audit store was accepted")
	}
	if _, err := newAuditService(securityAuditStore{}, nil, model.NewId()); err == nil {
		t.Fatal("nil audit institution store was accepted")
	}
	if _, err := newAuditService(securityAuditStore{}, securityInstitutionStore{}, ""); err == nil {
		t.Fatal("empty audit node ID was accepted")
	}
}

type securityExternalLoginStateStore struct{ store.ExternalLoginStateStore }
type securityInstitutionStore struct{ store.InstitutionStore }
type securityExternalIdentityStore struct{ store.ExternalIdentityStore }
type securityAuditStore struct{ store.AuditStore }

func TestAuthenticationAndRealtimeRetainOnlyNarrowSiblingPorts(t *testing.T) {
	t.Parallel()

	authenticationType := reflect.TypeOf(authenticationService{})
	realtimePointerType := reflect.TypeOf((*realtimeService)(nil))
	for index := 0; index < authenticationType.NumField(); index++ {
		field := authenticationType.Field(index)
		if field.Type == realtimePointerType {
			t.Fatalf("authenticationService retains realtimeService in field %q", field.Name)
		}
		if field.Type.Kind() == reflect.Func && field.Name != "now" && field.Name != "newCredential" {
			t.Fatalf("authenticationService retains mutable callback field %q", field.Name)
		}
	}

	realtimeType := reflect.TypeOf(realtimeService{})
	authenticationPointerType := reflect.TypeOf((*authenticationService)(nil))
	for index := 0; index < realtimeType.NumField(); index++ {
		field := realtimeType.Field(index)
		if field.Type == authenticationPointerType {
			t.Fatalf("realtimeService retains authenticationService in field %q", field.Name)
		}
	}
}

func TestAuthenticationCacheInvalidatorOwnsKeysAndSafeDiagnostics(t *testing.T) {
	t.Parallel()

	cache := &securityEffectsCacheFake{
		authenticationCacheFake: newAuthenticationCacheFake(),
		deleteErrors: map[string]error{
			authenticationCachePrefix + "failed-hash": errors.New("delete failed"),
		},
	}
	diagnostics := &securityEffectsDiagnosticsFake{}
	invalidator, err := newAuthenticationCacheInvalidator(cache, diagnostics)
	if err != nil {
		t.Fatal(err)
	}

	invalidator.InvalidateAccessCredentials(
		context.Background(),
		[]string{"first-hash", "failed-hash"},
	)
	invalidator.InvalidateSessionActivity(
		context.Background(),
		[]string{"session-one", "session-two"},
	)

	wantDeletes := []string{
		authenticationCachePrefix + "first-hash",
		authenticationCachePrefix + "failed-hash",
		activityCachePrefix + "session-one",
		activityCachePrefix + "session-two",
	}
	if !reflect.DeepEqual(cache.deleted, wantDeletes) {
		t.Fatalf("deleted keys = %v, want %v", cache.deleted, wantDeletes)
	}
	if want := []string{"authentication cache delete failed"}; !reflect.DeepEqual(diagnostics.messages, want) {
		t.Fatalf("diagnostics = %v, want %v", diagnostics.messages, want)
	}
	if want := []string{"cache operation failed"}; !reflect.DeepEqual(diagnostics.errors, want) {
		t.Fatalf("diagnostic errors = %v, want %v", diagnostics.errors, want)
	}
}

type securityEffectsCacheFake struct {
	*authenticationCacheFake
	deleted      []string
	deleteErrors map[string]error
}

type securityEffectsProviderSourceFake struct{}

func (securityEffectsProviderSourceFake) Descriptors() []model.ExternalAuthenticationProvider {
	return nil
}

func (securityEffectsProviderSourceFake) Provider(string) (ExternalIdentityProvider, bool) {
	return nil, false
}

func (c *securityEffectsCacheFake) Delete(_ context.Context, key string) error {
	c.deleted = append(c.deleted, key)
	return c.deleteErrors[key]
}

type securityEffectsDiagnosticsFake struct {
	messages []string
	errors   []string
}

func (d *securityEffectsDiagnosticsFake) WarnContext(_ context.Context, message string, err error) {
	d.messages = append(d.messages, message)
	d.errors = append(d.errors, err.Error())
}

type securityEffectsRealtimeDiagnosticsFake struct{}

func (*securityEffectsRealtimeDiagnosticsFake) ErrorContext(context.Context, string, error) {}

func (*securityEffectsRealtimeDiagnosticsFake) ErrorContextWithEvent(
	context.Context,
	string,
	string,
	error,
) {
}

type discardAuthenticationSecurityEffects struct{}

func (discardAuthenticationSecurityEffects) AuthenticationCacheInvalidated(
	context.Context,
	string,
	[]string,
) {
}

func (discardAuthenticationSecurityEffects) SessionsRevoked(
	context.Context,
	string,
	[]string,
	[]string,
) {
}
