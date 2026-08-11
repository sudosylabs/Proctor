// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
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
	mfa := mustTestMFAService(t)
	arguments := func(effects authenticationSecurityEffects) error {
		_, constructorErr := newAuthenticationService(
			persistence,
			cache,
			effects,
			hasher,
			mfa,
			SessionPolicy{},
			LoginRateLimitPolicy{},
			PersonalAccessTokenPolicy{},
			&securityEffectsDiagnosticsFake{},
			nil,
		)
		return constructorErr
	}
	if err := arguments(nil); err == nil {
		t.Fatal("nil authentication security effects were accepted")
	}
}

func TestExternalAuthenticationServiceRequiresInvalidator(t *testing.T) {
	t.Parallel()

	persistence := newAuthenticationStoreFake()
	authentication := newTestAuthenticationService(t, persistence)
	_, err := newExternalAuthenticationService(
		securityEffectsProviderSourceFake{},
		persistence,
		newAuthenticationCacheFake(),
		authentication,
		nil,
		newAuditService(persistence, model.NewId()),
		ExternalAuthenticationPolicy{},
		&securityEffectsDiagnosticsFake{},
		nil,
	)
	if err == nil {
		t.Fatal("nil authentication invalidator was accepted")
	}
}

func TestAuthenticationAndRealtimeRetainOnlyNarrowSiblingPorts(t *testing.T) {
	t.Parallel()

	authenticationType := reflect.TypeOf(AuthenticationService{})
	realtimePointerType := reflect.TypeOf((*RealtimeService)(nil))
	for index := 0; index < authenticationType.NumField(); index++ {
		field := authenticationType.Field(index)
		if field.Type == realtimePointerType {
			t.Fatalf("AuthenticationService retains RealtimeService in field %q", field.Name)
		}
		if field.Type.Kind() == reflect.Func && field.Name != "now" {
			t.Fatalf("AuthenticationService retains mutable callback field %q", field.Name)
		}
	}

	realtimeType := reflect.TypeOf(RealtimeService{})
	authenticationPointerType := reflect.TypeOf((*AuthenticationService)(nil))
	for index := 0; index < realtimeType.NumField(); index++ {
		field := realtimeType.Field(index)
		if field.Type == authenticationPointerType {
			t.Fatalf("RealtimeService retains AuthenticationService in field %q", field.Name)
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
