// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

type expiringAuthenticationAttemptCache struct {
	mu      sync.Mutex
	now     func() time.Time
	entries map[string]expiringAuthenticationAttemptCounter
	deleted []string
}

type expiringAuthenticationAttemptCounter struct {
	count     int64
	expiresAt time.Time
}

func newExpiringAuthenticationAttemptCache(
	now func() time.Time,
) *expiringAuthenticationAttemptCache {
	return &expiringAuthenticationAttemptCache{
		now: now, entries: make(map[string]expiringAuthenticationAttemptCounter),
	}
}

func (c *expiringAuthenticationAttemptCache) Add(
	_ context.Context,
	key string,
	delta int64,
	ttl time.Duration,
) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	entry := c.entries[key]
	if !entry.expiresAt.After(now) {
		entry.count = 0
	}
	entry.count += delta
	entry.expiresAt = now.Add(ttl)
	c.entries[key] = entry
	return entry.count, nil
}

func (c *expiringAuthenticationAttemptCache) Delete(_ context.Context, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
	c.deleted = append(c.deleted, key)
	return nil
}

func (c *expiringAuthenticationAttemptCache) snapshot() map[string]expiringAuthenticationAttemptCounter {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make(map[string]expiringAuthenticationAttemptCounter, len(c.entries))
	for key, entry := range c.entries {
		result[key] = entry
	}
	return result
}

func localLoginAttemptIntent(identity, source string, maximum int) authenticationAttemptIntent {
	return authenticationAttemptIntent{
		purpose: authenticationAttemptPurposeLocalLogin,
		window:  time.Minute,
		limits: []authenticationAttemptLimit{
			{
				dimension: authenticationAttemptDimensionIdentitySource,
				maximum:   maximum,
				identity:  identity,
				source:    source,
			},
			{
				dimension: authenticationAttemptDimensionSource,
				maximum:   maximum * 10,
				source:    source,
			},
		},
	}
}

func TestAuthenticationAttemptIntentRejectsInvalidClosedInputs(t *testing.T) {
	t.Parallel()

	validLimit := authenticationAttemptLimit{
		dimension: authenticationAttemptDimensionSource,
		maximum:   1,
		source:    "127.0.0.1",
	}
	for _, test := range []struct {
		name   string
		intent authenticationAttemptIntent
	}{
		{name: "unknown purpose", intent: authenticationAttemptIntent{
			purpose: authenticationAttemptPurpose(255), window: time.Minute,
			limits: []authenticationAttemptLimit{validLimit},
		}},
		{name: "missing dimensions", intent: authenticationAttemptIntent{
			purpose: authenticationAttemptPurposeLocalLogin, window: time.Minute,
		}},
		{name: "unknown dimension", intent: authenticationAttemptIntent{
			purpose: authenticationAttemptPurposeLocalLogin, window: time.Minute,
			limits: []authenticationAttemptLimit{{dimension: authenticationAttemptDimension(255), maximum: 1}},
		}},
		{name: "duplicate dimension", intent: authenticationAttemptIntent{
			purpose: authenticationAttemptPurposeLocalLogin, window: time.Minute,
			limits: []authenticationAttemptLimit{validLimit, validLimit},
		}},
		{name: "missing window", intent: authenticationAttemptIntent{
			purpose: authenticationAttemptPurposeLocalLogin,
			limits:  []authenticationAttemptLimit{validLimit},
		}},
		{name: "invalid maximum", intent: authenticationAttemptIntent{
			purpose: authenticationAttemptPurposeLocalLogin, window: time.Minute,
			limits: []authenticationAttemptLimit{{
				dimension: authenticationAttemptDimensionSource, maximum: 0, source: "127.0.0.1",
			}},
		}},
		{name: "unbounded qualifier", intent: authenticationAttemptIntent{
			purpose:   authenticationAttemptPurposeAccountRecovery,
			qualifier: strings.Repeat("q", maxAuthenticationAttemptQualifierBytes+1),
			window:    time.Minute, limits: []authenticationAttemptLimit{validLimit},
		}},
		{name: "unbounded identity", intent: authenticationAttemptIntent{
			purpose: authenticationAttemptPurposeLocalLogin, window: time.Minute,
			limits: []authenticationAttemptLimit{{
				dimension: authenticationAttemptDimensionIdentity, maximum: 1,
				identity: strings.Repeat("i", maxAuthenticationAttemptIdentityBytes+1),
			}},
		}},
		{name: "unbounded source", intent: authenticationAttemptIntent{
			purpose: authenticationAttemptPurposeInstallationBootstrap, window: time.Minute,
			limits: []authenticationAttemptLimit{{
				dimension: authenticationAttemptDimensionSource, maximum: 1,
				source: strings.Repeat("s", maxAuthenticationAttemptSourceBytes+1),
			}},
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			cache := newExpiringAuthenticationAttemptCache(time.Now)
			accounting, err := newAuthenticationAttemptAccounting(cache)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err = accounting.account(context.Background(), test.intent); err == nil {
				t.Fatal("invalid intent was accepted")
			}
			if len(cache.snapshot()) != 0 {
				t.Fatal("invalid intent changed a counter")
			}
		})
	}
}

func TestAuthenticationAttemptAccountingNormalizesAndProtectsKeyMaterial(t *testing.T) {
	t.Parallel()

	cache := newExpiringAuthenticationAttemptCache(time.Now)
	accounting := mustAuthenticationAttemptAccounting(t, cache)
	first := localLoginAttemptIntent(" Student@Example.EDU ", " Example.COM:443 ", 10)
	second := localLoginAttemptIntent("student@example.edu", "example.com:8443", 10)
	if _, _, err := accounting.account(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if _, _, err := accounting.account(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	entries := cache.snapshot()
	if len(entries) != 2 {
		t.Fatalf("counter keys = %d, want normalized identity/source pair", len(entries))
	}
	for key, entry := range entries {
		if !strings.HasPrefix(key, "authentication/attempts/local-login/") {
			t.Fatalf("key = %q, want canonical namespace", key)
		}
		if strings.Contains(strings.ToLower(key), "student") ||
			strings.Contains(strings.ToLower(key), "example") {
			t.Fatalf("key exposes attempt material: %q", key)
		}
		if entry.count != 2 {
			t.Fatalf("counter %q = %d, want 2", key, entry.count)
		}
	}
}

func TestAuthenticationAttemptAccountingIsolatesAllFourCallerFlowsAndProtectsInputs(t *testing.T) {
	t.Parallel()

	cache := newExpiringAuthenticationAttemptCache(time.Now)
	accounting := mustAuthenticationAttemptAccounting(t, cache)
	policy := LoginRateLimitPolicy{
		Window: time.Minute, MaximumAttempts: 1, MaximumSourceAttempts: 1,
	}
	login := &authenticationService{attempts: accounting, loginRateLimit: policy}
	recovery := &accountTokenService{
		attempts: accounting,
		policy:   AccountRecoveryPolicy{RateLimit: policy},
	}
	external := &externalAuthenticationService{
		attempts: accounting,
		policy:   ExternalAuthenticationPolicy{LoginRateLimit: policy},
	}
	bootstrap := &bootstrapService{
		attempts: accounting, rateLimitWindow: policy.Window,
		maximumSourceAttempts: policy.MaximumSourceAttempts,
	}

	sensitiveValues := []string{
		"student@example.edu",
		"provider-one",
		"127.0.0.1",
	}
	type attempt func() error
	flows := []attempt{
		func() error {
			_, err := login.checkLoginRateLimit(
				context.Background(), " Student@Example.EDU ", "127.0.0.1:443",
			)
			return err
		},
		func() error {
			return recovery.checkAccountRecoveryRateLimit(
				context.Background(), accountRecoveryAttemptPasswordResetRequest,
				"student@example.edu", "127.0.0.1:443",
			)
		},
		func() error {
			return external.checkInitiationRateLimit(
				context.Background(), "provider-one", "127.0.0.1:443",
			)
		},
		func() error {
			return bootstrap.checkRateLimit(context.Background(), "127.0.0.1:443")
		},
	}
	for index, flow := range flows {
		if err := flow(); err != nil {
			t.Fatalf("flow %d first attempt: %v", index, err)
		}
		err := flow()
		if !Is(err, "authentication.rate_limited") {
			t.Fatalf("flow %d second attempt = %v, want rate limited", index, err)
		}
		for _, sensitive := range sensitiveValues {
			if strings.Contains(strings.ToLower(err.Error()), sensitive) {
				t.Fatalf("flow %d error exposes attempt input: %v", index, err)
			}
		}
	}
	entries := cache.snapshot()
	const expectedKeys = 6 // login 2 + recovery 2 + external 1 + bootstrap 1
	if len(entries) != expectedKeys {
		t.Fatalf("counter keys = %d, want %d isolated keys", len(entries), expectedKeys)
	}
	for key := range entries {
		for _, sensitive := range sensitiveValues {
			if strings.Contains(strings.ToLower(key), sensitive) {
				t.Fatalf("key exposes raw identity, source, operation, or provider qualifier: %q", key)
			}
		}
	}
}

func TestAuthenticationAttemptAccountingUsesSlidingExpirationAndThresholdEdge(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 13, 10, 0, 0, 0, time.UTC)
	cache := newExpiringAuthenticationAttemptCache(func() time.Time { return now })
	accounting := mustAuthenticationAttemptAccounting(t, cache)
	intent := authenticationAttemptIntent{
		purpose: authenticationAttemptPurposeLocalLogin,
		window:  time.Minute,
		limits: []authenticationAttemptLimit{{
			dimension: authenticationAttemptDimensionSource, maximum: 2,
			source: "192.0.2.1:443",
		}},
	}
	for attempt := 1; attempt <= 2; attempt++ {
		_, limited, err := accounting.account(context.Background(), intent)
		if err != nil || limited {
			t.Fatalf("attempt %d = limited %v, error %v", attempt, limited, err)
		}
		now = now.Add(45 * time.Second)
	}
	_, limited, err := accounting.account(context.Background(), intent)
	if err != nil || !limited {
		t.Fatalf("attempt after maximum = limited %v, error %v", limited, err)
	}

	now = now.Add(61 * time.Second)
	_, limited, err = accounting.account(context.Background(), intent)
	if err != nil || limited {
		t.Fatalf("attempt after inactivity window = limited %v, error %v", limited, err)
	}
}

func TestAuthenticationAttemptReceiptResetsOnlySelectedDimension(t *testing.T) {
	t.Parallel()

	cache := newExpiringAuthenticationAttemptCache(time.Now)
	accounting := mustAuthenticationAttemptAccounting(t, cache)
	receipt, limited, err := accounting.account(
		context.Background(),
		localLoginAttemptIntent("student", "192.0.2.2:443", 5),
	)
	if err != nil || limited {
		t.Fatalf("account = limited %v, error %v", limited, err)
	}
	if err = accounting.reset(
		context.Background(), receipt, authenticationAttemptDimensionIdentitySource,
	); err != nil {
		t.Fatal(err)
	}
	entries := cache.snapshot()
	if len(entries) != 1 {
		t.Fatalf("remaining counters = %d, want source-only counter", len(entries))
	}
	for key, entry := range entries {
		if !strings.Contains(key, "/source/") || entry.count != 1 {
			t.Fatalf("remaining counter = %q %#v", key, entry)
		}
	}
}

func TestAuthenticationAttemptAccountingIsAtomicPerKeyUnderConcurrency(t *testing.T) {
	t.Parallel()

	cache := newExpiringAuthenticationAttemptCache(time.Now)
	accounting := mustAuthenticationAttemptAccounting(t, cache)
	intent := authenticationAttemptIntent{
		purpose: authenticationAttemptPurposeLocalLogin,
		window:  time.Minute,
		limits: []authenticationAttemptLimit{{
			dimension: authenticationAttemptDimensionSource, maximum: 100,
			source: "192.0.2.3:443",
		}},
	}
	var group sync.WaitGroup
	errorsSeen := make(chan error, 100)
	for range 100 {
		group.Add(1)
		go func() {
			defer group.Done()
			_, limited, err := accounting.account(context.Background(), intent)
			if err != nil {
				errorsSeen <- err
			} else if limited {
				errorsSeen <- errors.New("maximum attempt was unexpectedly limited")
			}
		}()
	}
	group.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		t.Error(err)
	}
	for _, entry := range cache.snapshot() {
		if entry.count != 100 {
			t.Fatalf("counter = %d, want 100", entry.count)
		}
	}
}

type faultingAuthenticationAttemptCache struct {
	*expiringAuthenticationAttemptCache
	failAt int
	calls  []string
}

func (c *faultingAuthenticationAttemptCache) Add(
	ctx context.Context,
	key string,
	delta int64,
	ttl time.Duration,
) (int64, error) {
	c.calls = append(c.calls, key)
	if len(c.calls) == c.failAt {
		return 0, errors.New("counter unavailable")
	}
	return c.expiringAuthenticationAttemptCache.Add(ctx, key, delta, ttl)
}

func TestAuthenticationAttemptAccountingPreservesSequentialPartialFailure(t *testing.T) {
	t.Parallel()

	for _, failAt := range []int{1, 2} {
		t.Run(string(rune('0'+failAt)), func(t *testing.T) {
			t.Parallel()
			cache := &faultingAuthenticationAttemptCache{
				expiringAuthenticationAttemptCache: newExpiringAuthenticationAttemptCache(time.Now),
				failAt:                             failAt,
			}
			accounting := mustAuthenticationAttemptAccounting(t, cache)
			if _, _, err := accounting.account(
				context.Background(), localLoginAttemptIntent("student", "192.0.2.4", 5),
			); err == nil {
				t.Fatal("cache failure was ignored")
			}
			if len(cache.calls) != failAt {
				t.Fatalf("Add calls = %d, want %d", len(cache.calls), failAt)
			}
			entries := cache.snapshot()
			if failAt == 1 && len(entries) != 0 {
				t.Fatal("first-counter failure changed cache")
			}
			if failAt == 2 {
				if len(entries) != 1 {
					t.Fatalf("partial counters = %d, want 1", len(entries))
				}
				if !strings.Contains(cache.calls[0], "/identity-source/") ||
					!strings.Contains(cache.calls[1], "/source/") {
					t.Fatalf("counter order = %#v", cache.calls)
				}
			}
		})
	}
}

type loginAttemptFaultCache struct {
	*authenticationCacheFake
	failAddAt int
	addCalls  int
	deleteErr error
}

func (c *loginAttemptFaultCache) Add(
	ctx context.Context,
	key string,
	delta int64,
	ttl time.Duration,
) (int64, error) {
	c.addCalls++
	if c.addCalls == c.failAddAt {
		return 0, errors.New("attempt cache unavailable")
	}
	return c.authenticationCacheFake.Add(ctx, key, delta, ttl)
}

func (c *loginAttemptFaultCache) Delete(ctx context.Context, key string) error {
	if c.deleteErr != nil && strings.HasPrefix(key, "authentication/attempts/") {
		return c.deleteErr
	}
	return c.authenticationCacheFake.Delete(ctx, key)
}

func TestLoginMapsEitherCounterFailureToUnavailableWithoutRollback(t *testing.T) {
	t.Parallel()

	for _, failAt := range []int{1, 2} {
		t.Run(string(rune('0'+failAt)), func(t *testing.T) {
			t.Parallel()
			cache := &loginAttemptFaultCache{
				authenticationCacheFake: newAuthenticationCacheFake(),
				failAddAt:               failAt,
			}
			service := newTestAuthenticationServiceWithCache(
				t, newAuthenticationStoreFake(), cache,
			)
			_, err := service.login(context.Background(), LoginCommand{
				LoginID: "student@example.edu", Password: "wrong-password",
				ClientType: model.SessionClientCLI, Source: "192.0.2.8:443",
			})
			if !Is(err, "authentication.rate_limit_unavailable") {
				t.Fatalf("login error = %v", err)
			}
			cache.mu.Lock()
			counterCount := len(cache.counters)
			cache.mu.Unlock()
			if failAt == 1 && counterCount != 0 {
				t.Fatalf("first failure retained %d counters", counterCount)
			}
			if failAt == 2 && counterCount != 1 {
				t.Fatalf("second failure retained %d counters, want 1", counterCount)
			}
		})
	}
}

func TestSuccessfulLoginTreatsAttemptResetFailureAsDiagnosticOnly(t *testing.T) {
	t.Parallel()

	cache := &loginAttemptFaultCache{
		authenticationCacheFake: newAuthenticationCacheFake(),
		deleteErr:               errors.New("attempt reset unavailable"),
	}
	persistence := newAuthenticationStoreFake()
	service := newTestAuthenticationServiceWithCache(t, persistence, cache)
	diagnostics := &securityEffectsDiagnosticsFake{}
	service.diagnostics = diagnostics
	user, err := service.createLocalUser(context.Background(), CreateLocalUserCommand{
		User: &model.User{
			Username: "reset-user", Email: "reset-user@example.edu",
		},
		Password: "CorrectHorseBatteryStaple1!",
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.login(context.Background(), LoginCommand{
		LoginID: user.Username, Password: "CorrectHorseBatteryStaple1!",
		ClientType: model.SessionClientCLI, Source: "192.0.2.9:443",
	})
	if err != nil || result == nil {
		t.Fatalf("login result = %#v, error %v", result, err)
	}
	if len(diagnostics.messages) != 1 ||
		diagnostics.messages[0] != "login rate-limit reset failed" {
		t.Fatalf("diagnostics = %#v", diagnostics.messages)
	}
	for _, diagnostic := range append(append([]string(nil), diagnostics.messages...), diagnostics.errors...) {
		lower := strings.ToLower(diagnostic)
		if strings.Contains(lower, "reset-user") || strings.Contains(lower, "192.0.2.9") {
			t.Fatalf("diagnostic exposes raw login identity or source: %q", diagnostic)
		}
	}
}
