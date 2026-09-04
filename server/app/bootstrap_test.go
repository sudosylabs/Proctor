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
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type installationStoreFake struct {
	events                 *[]string
	state                  *model.InstallationState
	getErr                 error
	input                  *store.InstallationBootstrap
	result                 *model.InstallationBootstrapResult
	err                    error
	errors                 []error
	calls                  int
	recoveryInput          *store.AdministratorRecovery
	recoveryResult         *store.AdministratorRecoveryResult
	recoveryErr            error
	recoveryReconcileInput *store.AdministratorRecoveryReconciliation
	recoveryReconcileErr   error
}

func (s *installationStoreFake) RecoverAdministratorAccess(_ context.Context, input *store.AdministratorRecovery) (*store.AdministratorRecoveryResult, error) {
	*s.events = append(*s.events, "recover-administrator")
	s.recoveryInput = input
	return s.recoveryResult, s.recoveryErr
}

func (s *installationStoreFake) ReconcileAdministratorRecovery(_ context.Context, input *store.AdministratorRecoveryReconciliation) (*store.AdministratorRecoveryReconciliationResult, error) {
	s.recoveryReconcileInput = input
	return &store.AdministratorRecoveryReconciliationResult{}, s.recoveryReconcileErr
}

func (s *installationStoreFake) Get(context.Context) (*model.InstallationState, error) {
	*s.events = append(*s.events, "get-status")
	return s.state, s.getErr
}

func (s *installationStoreFake) Bootstrap(_ context.Context, input *store.InstallationBootstrap) (*model.InstallationBootstrapResult, error) {
	*s.events = append(*s.events, "bootstrap")
	s.input = input
	s.calls++
	if len(s.errors) >= s.calls && s.errors[s.calls-1] != nil {
		return nil, s.errors[s.calls-1]
	}
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

func bootstrapProtection() BootstrapProtectionPolicy {
	return BootstrapProtectionPolicy{Secret: "operator-provided-bootstrap-secret-32-bytes"}
}

func TestBootstrapStatusUninitializedOnNotFound(t *testing.T) {
	t.Parallel()
	events := []string{}
	service := newBootstrapService(
		&installationStoreFake{events: &events, getErr: store.NewErrNotFound("installation", "")},
		&passwordHasherFake{events: &events},
		bootstrapAttemptAccounting(t, &bootstrapAttemptCacheFake{events: &events}),
		bootstrapRateLimitPolicy(10),
		bootstrapProtection(),
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
		bootstrapProtection(),
		"node-a",
		func() time.Time { return time.UnixMilli(500) },
	)
	got, err := service.Bootstrap(context.Background(), NewInvocation(model.Principal{}, model.RequestMetadata{RequestID: "req"}), BootstrapInstallationCommand{
		InstitutionName: "northbridge", InstitutionDisplayName: "Northbridge",
		AdministratorUsername: "admin", AdministratorEmail: "admin@example.com",
		Password: "password-value", BootstrapSecret: bootstrapProtection().Secret, Source: "127.0.0.1",
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
	if persistence.input.AccessPolicy == nil || persistence.input.AccessPolicy.Revision != 1 ||
		!persistence.input.AccessPolicy.LocalLoginEnabled ||
		persistence.input.AccessPolicy.PublicRegistrationEnabled ||
		!persistence.input.AccessPolicy.DesktopAuthorizationEnabled ||
		persistence.input.BootstrapSecretDigest == ([32]byte{}) ||
		persistence.input.CommandFingerprint == ([32]byte{}) {
		t.Fatalf("bootstrap protection/policy input = %#v", persistence.input)
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
	want := []string{"rate-limit", "hash-password", "bootstrap"}
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
		}, err: store.NewErrConflict("installation", "already", nil)},
		&passwordHasherFake{events: &events},
		bootstrapAttemptAccounting(t, &bootstrapAttemptCacheFake{events: &events}),
		bootstrapRateLimitPolicy(10),
		bootstrapProtection(),
		"node-a",
		time.Now,
	)
	_, err := service.Bootstrap(context.Background(), Invocation{}, BootstrapInstallationCommand{
		InstitutionName: "northbridge", InstitutionDisplayName: "Northbridge",
		AdministratorUsername: "admin", AdministratorEmail: "admin@example.com", Password: "x",
		BootstrapSecret: bootstrapProtection().Secret,
	})
	if !Is(err, "installation.already_initialized") {
		t.Fatalf("error = %v", err)
	}
	want := []string{"rate-limit", "hash-password", "bootstrap"}
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
		bootstrapProtection(),
		"node-a",
		time.Now,
	)
	_, err := service.Bootstrap(context.Background(), Invocation{}, BootstrapInstallationCommand{
		InstitutionName: "northbridge", InstitutionDisplayName: "Northbridge",
		AdministratorUsername: "admin", AdministratorEmail: "admin@example.com", Password: "x",
		BootstrapSecret: bootstrapProtection().Secret,
	})
	if !Is(err, "installation.already_initialized") {
		t.Fatalf("error = %v", err)
	}
}

func TestBootstrapRejectsWrongSecretBeforePasswordHashingOrPersistence(t *testing.T) {
	t.Parallel()

	events := []string{}
	service := newBootstrapService(
		&installationStoreFake{events: &events},
		&passwordHasherFake{events: &events, hash: "encoded"},
		bootstrapAttemptAccounting(t, &bootstrapAttemptCacheFake{events: &events}),
		bootstrapRateLimitPolicy(10), bootstrapProtection(), "node-a", time.Now,
	)
	_, err := service.Bootstrap(context.Background(), Invocation{}, BootstrapInstallationCommand{
		InstitutionName: "northbridge", AdministratorUsername: "admin",
		AdministratorEmail: "admin@example.com", Password: "password-value",
		BootstrapSecret: "wrong-bootstrap-secret-that-is-still-long-enough", Source: "192.0.2.10:443",
	})
	if !Is(err, "installation.bootstrap_denied") {
		t.Fatalf("error = %v", err)
	}
	if want := []string{"rate-limit"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestBootstrapReconcilesUnknownCommitByRepeatingTheFencedAggregate(t *testing.T) {
	t.Parallel()

	events := []string{}
	committed := &model.InstallationBootstrapResult{State: &model.InstallationState{
		InitializedAt: model.TimeFromMillis(1), InstitutionID: model.NewInstitutionID(),
		AdministratorUserID: model.NewUserID(),
	}}
	persistence := &installationStoreFake{
		events: &events, result: committed,
		errors: []error{errors.New("commit outcome unknown"), nil},
	}
	service := newBootstrapService(
		persistence, &passwordHasherFake{events: &events, hash: "encoded"},
		bootstrapAttemptAccounting(t, &bootstrapAttemptCacheFake{events: &events}),
		bootstrapRateLimitPolicy(10), bootstrapProtection(), "node-a", time.Now,
	)
	result, err := service.Bootstrap(context.Background(), Invocation{}, BootstrapInstallationCommand{
		InstitutionName: "northbridge", AdministratorUsername: "admin",
		AdministratorEmail: "admin@example.com", Password: "password-value",
		BootstrapSecret: bootstrapProtection().Secret, Source: "192.0.2.10:443",
	})
	if err != nil || result != committed || persistence.calls != 2 {
		t.Fatalf("result=%#v calls=%d error=%v", result, persistence.calls, err)
	}
}

func TestBootstrapCommandFingerprintIsExactAndDeploymentSecretKeyed(t *testing.T) {
	t.Parallel()

	command := BootstrapInstallationCommand{
		InstitutionName: "northbridge", AdministratorUsername: "admin",
		AdministratorEmail: "admin@example.com", Password: "password-value",
		BootstrapSecret: bootstrapProtection().Secret,
	}
	first, err := fingerprintBootstrapCommand(command)
	if err != nil {
		t.Fatal(err)
	}
	second, err := fingerprintBootstrapCommand(command)
	if err != nil || first != second {
		t.Fatalf("same command fingerprint = %x / %x, error=%v", first, second, err)
	}
	changedPassword := command
	changedPassword.Password = "different-password"
	passwordFingerprint, err := fingerprintBootstrapCommand(changedPassword)
	if err != nil || passwordFingerprint == first {
		t.Fatalf("changed password fingerprint = %x, error=%v", passwordFingerprint, err)
	}
	changedSecret := command
	changedSecret.BootstrapSecret = "different-operator-bootstrap-secret-32-bytes"
	secretFingerprint, err := fingerprintBootstrapCommand(changedSecret)
	if err != nil || secretFingerprint == first {
		t.Fatalf("changed secret fingerprint = %x, error=%v", secretFingerprint, err)
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
		bootstrapProtection(),
		"node-a",
		time.Now,
	)
	command := BootstrapInstallationCommand{
		InstitutionName: "northbridge", AdministratorUsername: "admin",
		AdministratorEmail: "admin@example.com", Password: "password-value",
		BootstrapSecret: bootstrapProtection().Secret,
		Source:          " 192.0.2.10:443 ",
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
		bootstrapProtection(),
		"node-a",
		time.Now,
	)
	_, err := service.Bootstrap(context.Background(), Invocation{}, BootstrapInstallationCommand{
		InstitutionName: "northbridge", AdministratorUsername: "admin",
		AdministratorEmail: "admin@example.com", Password: "password-value",
		BootstrapSecret: bootstrapProtection().Secret,
		Source:          "192.0.2.10:443",
	})
	if !Is(err, "administration.unavailable") || !errors.Is(err, cacheFailure) {
		t.Fatalf("error = %v", err)
	}
	want := []string{"rate-limit"}
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
		bootstrapProtection(),
		"node-a",
		time.Now,
	)
	_, err := service.Bootstrap(context.Background(), Invocation{}, BootstrapInstallationCommand{
		InstitutionName: "northbridge", AdministratorUsername: "admin",
		AdministratorEmail: "admin@example.com", Password: "password-value",
		BootstrapSecret: bootstrapProtection().Secret,
		Source:          "192.0.2.10:443",
	})
	if !Is(err, "administration.unavailable") {
		t.Fatalf("error = %v", err)
	}
	want := []string{}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}
