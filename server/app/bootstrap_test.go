// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type installationStoreFake struct {
	events *[]string
	state  *model.InstallationState
	getErr error
	input  *store.InstallationBootstrap
	result *model.InstallationBootstrapResult
	err    error
}

func (s *installationStoreFake) Get(context.Context) (*model.InstallationState, error) {
	*s.events = append(*s.events, "get-status")
	return s.state, s.getErr
}

func (s *installationStoreFake) Bootstrap(_ context.Context, input *store.InstallationBootstrap) (*model.InstallationBootstrapResult, error) {
	*s.events = append(*s.events, "bootstrap")
	s.input = input
	return s.result, s.err
}

func (s *installationStoreFake) ReconcileSystemAdministratorRole(
	context.Context,
	*store.SystemAdministratorRoleReconciliation,
) (*store.SystemAdministratorRoleReconciliationResult, error) {
	return &store.SystemAdministratorRoleReconciliationResult{}, nil
}

type passwordHasherFake struct {
	events *[]string
	hash   string
	err    error
}

func (h *passwordHasherFake) Hash(string) (string, error) {
	*h.events = append(*h.events, "hash-password")
	return h.hash, h.err
}

type bootstrapAttemptCacheFake struct {
	mu     sync.Mutex
	events *[]string
	counts map[string]int64
	err    error
}

func (c *bootstrapAttemptCacheFake) Add(_ context.Context, key string, delta int64, _ time.Duration) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.events != nil {
		*c.events = append(*c.events, "rate-limit")
	}
	if c.err != nil {
		return 0, c.err
	}
	if c.counts == nil {
		c.counts = make(map[string]int64)
	}
	c.counts[key] += delta
	return c.counts[key], nil
}

func (c *bootstrapAttemptCacheFake) Delete(context.Context, string) error {
	return nil
}

func (c *bootstrapAttemptCacheFake) snapshot() map[string]int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make(map[string]int64, len(c.counts))
	for key, count := range c.counts {
		result[key] = count
	}
	return result
}

func bootstrapAttemptAccounting(t *testing.T, cache authenticationAttemptCache) *authenticationAttemptAccounting {
	t.Helper()
	accounting, err := newAuthenticationAttemptAccounting(cache)
	if err != nil {
		t.Fatal(err)
	}
	return accounting
}

func bootstrapRateLimitPolicy(maximum int) LoginRateLimitPolicy {
	return LoginRateLimitPolicy{Window: time.Minute, MaximumSourceAttempts: maximum}
}

func TestBootstrapStatusUninitializedOnNotFound(t *testing.T) {
	t.Parallel()
	events := []string{}
	service := newBootstrapService(
		&installationStoreFake{events: &events, getErr: store.NewErrNotFound("installation", "")},
		&passwordHasherFake{events: &events},
		bootstrapAttemptAccounting(t, &bootstrapAttemptCacheFake{events: &events}),
		bootstrapRateLimitPolicy(10),
		"node-a",
		time.Now,
	)
	status, err := service.GetStatus(context.Background())
	if err != nil || status.Initialized {
		t.Fatalf("status=%#v err=%v", status, err)
	}
}

func TestBootstrapCommitsAtomicAggregate(t *testing.T) {
	t.Parallel()
	events := []string{}
	result := &model.InstallationBootstrapResult{
		State: &model.InstallationState{InitializedAt: model.TimeFromMillis(1), InstitutionID: model.NewInstitutionID(), AdministratorUserID: model.NewUserID()},
	}
	persistence := &installationStoreFake{
		events: &events, getErr: store.NewErrNotFound("installation", ""), result: result,
	}
	service := newBootstrapService(
		persistence,
		&passwordHasherFake{events: &events, hash: "encoded"},
		bootstrapAttemptAccounting(t, &bootstrapAttemptCacheFake{events: &events}),
		bootstrapRateLimitPolicy(10),
		"node-a",
		func() time.Time { return time.UnixMilli(500) },
	)
	got, err := service.Bootstrap(context.Background(), NewInvocation(model.Principal{}, model.RequestMetadata{RequestID: "req"}), BootstrapInstallationCommand{
		InstitutionName: "northbridge", InstitutionDisplayName: "Northbridge",
		AdministratorUsername: "admin", AdministratorEmail: "admin@example.com",
		Password: "password-value", Source: "127.0.0.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.State.InstitutionID != result.State.InstitutionID || persistence.input.PasswordHash != "encoded" {
		t.Fatalf("result/input = %#v / %#v", got, persistence.input)
	}
	if persistence.input.AuditEvent.NodeID != "node-a" || persistence.input.Role.BuiltIn != true {
		t.Fatalf("bootstrap input = %#v", persistence.input)
	}
	if !reflect.DeepEqual(persistence.input.Role.Permissions, model.AllActions()) {
		t.Fatalf("bootstrap system-administrator permissions = %#v, want %#v", persistence.input.Role.Permissions, model.AllActions())
	}
	if persistence.input.DefaultProfilePictureJob == nil ||
		persistence.input.DefaultProfilePictureJob.DedupeKey != persistence.input.Administrator.ID.String() {
		t.Fatalf("bootstrap default-picture job = %#v", persistence.input.DefaultProfilePictureJob)
	}
	if persistence.input.AdministratorSettings == nil ||
		persistence.input.AdministratorSettings.UserID != persistence.input.Administrator.ID ||
		persistence.input.AdministratorSettings.Source != model.UserSettingsInitialSource ||
		persistence.input.AdministratorSettings.FormatVersion != model.UserSettingsFormatVersion1 {
		t.Fatalf("bootstrap administrator settings = %#v", persistence.input.AdministratorSettings)
	}
	want := []string{"get-status", "rate-limit", "hash-password", "bootstrap"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestBootstrapAlreadyInitialized(t *testing.T) {
	t.Parallel()
	events := []string{}
	service := newBootstrapService(
		&installationStoreFake{events: &events, state: &model.InstallationState{
			InitializedAt: model.TimeFromMillis(1), InstitutionID: model.NewInstitutionID(), AdministratorUserID: model.NewUserID(),
		}},
		&passwordHasherFake{events: &events},
		bootstrapAttemptAccounting(t, &bootstrapAttemptCacheFake{events: &events}),
		bootstrapRateLimitPolicy(10),
		"node-a",
		time.Now,
	)
	_, err := service.Bootstrap(context.Background(), Invocation{}, BootstrapInstallationCommand{
		InstitutionName: "northbridge", InstitutionDisplayName: "Northbridge",
		AdministratorUsername: "admin", AdministratorEmail: "admin@example.com", Password: "x",
	})
	if !Is(err, "installation.already_initialized") {
		t.Fatalf("error = %v", err)
	}
	want := []string{"get-status"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestBootstrapConflictMapsToAlreadyInitialized(t *testing.T) {
	t.Parallel()
	events := []string{}
	service := newBootstrapService(
		&installationStoreFake{
			events: &events, getErr: store.NewErrNotFound("installation", ""),
			err: store.NewErrConflict("installation", "already", errors.New("race")),
		},
		&passwordHasherFake{events: &events, hash: "encoded"},
		bootstrapAttemptAccounting(t, &bootstrapAttemptCacheFake{events: &events}),
		bootstrapRateLimitPolicy(10),
		"node-a",
		time.Now,
	)
	_, err := service.Bootstrap(context.Background(), Invocation{}, BootstrapInstallationCommand{
		InstitutionName: "northbridge", InstitutionDisplayName: "Northbridge",
		AdministratorUsername: "admin", AdministratorEmail: "admin@example.com", Password: "x",
	})
	if !Is(err, "installation.already_initialized") {
		t.Fatalf("error = %v", err)
	}
}

func TestBootstrapRateLimitUsesSourceOnlySharedAccounting(t *testing.T) {
	t.Parallel()

	events := []string{}
	cache := &bootstrapAttemptCacheFake{events: &events}
	service := newBootstrapService(
		&installationStoreFake{events: &events, getErr: store.NewErrNotFound("installation", "")},
		&passwordHasherFake{events: &events, hash: "encoded"},
		bootstrapAttemptAccounting(t, cache),
		bootstrapRateLimitPolicy(2),
		"node-a",
		time.Now,
	)
	command := BootstrapInstallationCommand{
		InstitutionName: "northbridge", AdministratorUsername: "admin",
		AdministratorEmail: "admin@example.com", Password: "password-value",
		Source: " 192.0.2.10:443 ",
	}
	for attempt := 1; attempt <= 2; attempt++ {
		if _, err := service.Bootstrap(context.Background(), Invocation{}, command); err != nil {
			t.Fatalf("attempt %d error = %v", attempt, err)
		}
	}
	if _, err := service.Bootstrap(context.Background(), Invocation{}, command); !Is(err, "authentication.rate_limited") {
		t.Fatalf("attempt after maximum error = %v", err)
	}

	command.Source = "192.0.2.11:443"
	if _, err := service.Bootstrap(context.Background(), Invocation{}, command); err != nil {
		t.Fatalf("isolated source error = %v", err)
	}

	entries := cache.snapshot()
	if len(entries) != 2 {
		t.Fatalf("source counters = %d, want 2 isolated keys", len(entries))
	}
	for key := range entries {
		if !strings.HasPrefix(key, "authentication/attempts/installation-bootstrap/source/") {
			t.Fatalf("counter key = %q", key)
		}
		if strings.Contains(key, "192.0.2") {
			t.Fatalf("counter key exposes source: %q", key)
		}
	}
}

func TestBootstrapRateLimitFailureIsAdministrationUnavailableBeforeHashing(t *testing.T) {
	t.Parallel()

	events := []string{}
	cacheFailure := errors.New("cache unavailable")
	service := newBootstrapService(
		&installationStoreFake{events: &events, getErr: store.NewErrNotFound("installation", "")},
		&passwordHasherFake{events: &events, hash: "encoded"},
		bootstrapAttemptAccounting(t, &bootstrapAttemptCacheFake{events: &events, err: cacheFailure}),
		bootstrapRateLimitPolicy(2),
		"node-a",
		time.Now,
	)
	_, err := service.Bootstrap(context.Background(), Invocation{}, BootstrapInstallationCommand{
		InstitutionName: "northbridge", AdministratorUsername: "admin",
		AdministratorEmail: "admin@example.com", Password: "password-value",
		Source: "192.0.2.10:443",
	})
	if !Is(err, "administration.unavailable") || !errors.Is(err, cacheFailure) {
		t.Fatalf("error = %v", err)
	}
	want := []string{"get-status", "rate-limit"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestBootstrapInvalidRateLimitPolicyFailsClosed(t *testing.T) {
	t.Parallel()

	events := []string{}
	service := newBootstrapService(
		&installationStoreFake{events: &events, getErr: store.NewErrNotFound("installation", "")},
		&passwordHasherFake{events: &events, hash: "encoded"},
		bootstrapAttemptAccounting(t, &bootstrapAttemptCacheFake{events: &events}),
		LoginRateLimitPolicy{},
		"node-a",
		time.Now,
	)
	_, err := service.Bootstrap(context.Background(), Invocation{}, BootstrapInstallationCommand{
		InstitutionName: "northbridge", AdministratorUsername: "admin",
		AdministratorEmail: "admin@example.com", Password: "password-value",
		Source: "192.0.2.10:443",
	})
	if !Is(err, "administration.unavailable") {
		t.Fatalf("error = %v", err)
	}
	want := []string{"get-status"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}
