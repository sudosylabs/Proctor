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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
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
	mfaResetInput          *store.AdministratorMFAReset
	mfaResetResult         *store.AdministratorMFAResetResult
	mfaResetErr            error
	recoveryReconcileInput *store.AdministratorRecoveryReconciliation
	recoveryReconcileErr   error
}

func (s *installationStoreFake) ResetAdministratorMFA(_ context.Context, input *store.AdministratorMFAReset) (*store.AdministratorMFAResetResult, error) {
	*s.events = append(*s.events, "reset-administrator-mfa")
	s.mfaResetInput = input
	return s.mfaResetResult, s.mfaResetErr
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

func (h *passwordHasherFake) Hash(context.Context, string) (string, error) {
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
	require.NoError(t, err)
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
		administratorRecoveryPolicy{},
	)
	status, err := service.GetInstallationStatus(context.Background(), GetInstallationStatusQuery{})
	require.NoError(t, err, "status=%#v err", status)
	require.False(t, status.Initialized, "status=%#v err", status)
}

func TestBootstrapPasswordWorkFailuresStopBeforeAggregate(t *testing.T) {
	testPasswordHashFailures(t, "installation.unavailable", func(t *testing.T, ctx context.Context, hasher passwordHash) error {
		events := []string{}
		persistence := &installationStoreFake{events: &events}
		service := newBootstrapService(persistence, hasher,
			bootstrapAttemptAccounting(t, &bootstrapAttemptCacheFake{}), bootstrapRateLimitPolicy(10),
			bootstrapProtection(), "node-bootstrap", time.Now, administratorRecoveryPolicy{})
		result, err := service.BootstrapInstallation(ctx, Invocation{}, BootstrapInstallationCommand{
			InstitutionName: "test", AdministratorUsername: "administrator", AdministratorEmail: "administrator@example.edu",
			Password: "correct horse battery staple", BootstrapSecret: bootstrapProtection().Secret, Source: "192.0.2.35",
		})
		require.Nil(t, result, "failed password work reached installation bootstrap")
		require.Nil(t, persistence.input, "failed password work reached installation bootstrap")
		require.Empty(t, events, "failed password work reached installation bootstrap")
		return err
	})
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
		administratorRecoveryPolicy{},
	)
	got, err := service.BootstrapInstallation(context.Background(), NewInvocation(model.Principal{}, model.RequestMetadata{RequestID: "req"}), BootstrapInstallationCommand{
		InstitutionName: "northbridge", InstitutionDisplayName: "Northbridge",
		AdministratorUsername: "admin", AdministratorEmail: "admin@example.com",
		Password: "password-value", BootstrapSecret: bootstrapProtection().Secret, Source: "127.0.0.1",
	})
	require.NoError(t, err)
	require.Equal(t, result.State.InstitutionID, got.State.InstitutionID, "result/input = %#v / %#v", got, persistence.input)
	require.Equal(t, "encoded", persistence.input.PasswordHash, "result/input = %#v / %#v", got, persistence.input)
	require.Equal(t, "node-a", persistence.input.AuditEvent.NodeID)
	require.True(t, persistence.input.Role.BuiltIn)
	require.NotNil(t, persistence.input.AccessPolicy)
	require.Equal(t, int64(1), persistence.input.AccessPolicy.Revision)
	require.True(t, persistence.input.AccessPolicy.LocalLoginEnabled)
	require.False(t, persistence.input.AccessPolicy.PublicRegistrationEnabled)
	require.True(t, persistence.input.AccessPolicy.DesktopAuthorizationEnabled)
	require.NotEqual(t, [32]byte{}, persistence.input.BootstrapSecretDigest)
	require.NotEqual(t, [32]byte{}, persistence.input.CommandFingerprint)
	require.Equal(t, model.AllActions(), persistence.input.Role.Permissions, "bootstrap system-administrator permissions")
	require.NotNil(t, persistence.input.DefaultProfilePictureJob)
	require.Equal(t, persistence.input.Administrator.ID.String(), persistence.input.DefaultProfilePictureJob.DedupeKey)
	require.NotNil(t, persistence.input.AdministratorSettings)
	require.Equal(t, persistence.input.Administrator.ID, persistence.input.AdministratorSettings.UserID)
	require.Equal(t, model.UserSettingsInitialSource, persistence.input.AdministratorSettings.Source)
	require.Equal(t, model.UserSettingsFormatVersion1, persistence.input.AdministratorSettings.FormatVersion)
	// Contract: the rate limit is consulted before the expensive password hash,
	// which precedes the durable bootstrap.
	require.Equal(t, []string{"rate-limit", "hash-password", "bootstrap"}, events)
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
		administratorRecoveryPolicy{},
	)
	_, err := service.BootstrapInstallation(context.Background(), Invocation{}, BootstrapInstallationCommand{
		InstitutionName: "northbridge", InstitutionDisplayName: "Northbridge",
		AdministratorUsername: "admin", AdministratorEmail: "admin@example.com", Password: "x",
		BootstrapSecret: bootstrapProtection().Secret,
	})
	require.True(t, apperror.Is(err, "installation.already_initialized"), "error = %v", err)
	// Contract: the rate limit is consulted before the expensive password hash,
	// which precedes the durable bootstrap.
	require.Equal(t, []string{"rate-limit", "hash-password", "bootstrap"}, events)
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
		administratorRecoveryPolicy{},
	)
	_, err := service.BootstrapInstallation(context.Background(), Invocation{}, BootstrapInstallationCommand{
		InstitutionName: "northbridge", InstitutionDisplayName: "Northbridge",
		AdministratorUsername: "admin", AdministratorEmail: "admin@example.com", Password: "x",
		BootstrapSecret: bootstrapProtection().Secret,
	})
	require.True(t, apperror.Is(err, "installation.already_initialized"), "error = %v", err)
}

func TestBootstrapRejectsWrongSecretBeforePasswordHashingOrPersistence(t *testing.T) {
	t.Parallel()

	events := []string{}
	service := newBootstrapService(
		&installationStoreFake{events: &events},
		&passwordHasherFake{events: &events, hash: "encoded"},
		bootstrapAttemptAccounting(t, &bootstrapAttemptCacheFake{events: &events}),
		bootstrapRateLimitPolicy(10), bootstrapProtection(), "node-a", time.Now,
		administratorRecoveryPolicy{},
	)
	_, err := service.BootstrapInstallation(context.Background(), Invocation{}, BootstrapInstallationCommand{
		InstitutionName: "northbridge", AdministratorUsername: "admin",
		AdministratorEmail: "admin@example.com", Password: "password-value",
		BootstrapSecret: "wrong-bootstrap-secret-that-is-still-long-enough", Source: "192.0.2.10:443",
	})
	require.True(t, apperror.Is(err, "installation.bootstrap_denied"), "error = %v", err)
	require.Equal(t, []string{"rate-limit"}, events, "events")
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
		administratorRecoveryPolicy{},
	)
	result, err := service.BootstrapInstallation(context.Background(), Invocation{}, BootstrapInstallationCommand{
		InstitutionName: "northbridge", AdministratorUsername: "admin",
		AdministratorEmail: "admin@example.com", Password: "password-value",
		BootstrapSecret: bootstrapProtection().Secret, Source: "192.0.2.10:443",
	})
	require.NoError(t, err, "result")
	require.Same(t, committed, result, "result")
	require.Equal(t, 2, persistence.calls, "result")
}

func TestBootstrapCommandFingerprintIsExactAndDeploymentSecretKeyed(t *testing.T) {
	t.Parallel()

	command := BootstrapInstallationCommand{
		InstitutionName: "northbridge", AdministratorUsername: "admin",
		AdministratorEmail: "admin@example.com", Password: "password-value",
		BootstrapSecret: bootstrapProtection().Secret,
	}
	first, err := fingerprintBootstrapCommand(command)
	require.NoError(t, err)
	second, err := fingerprintBootstrapCommand(command)
	require.NoError(t, err, "same command fingerprint")
	require.Equal(t, second, first, "same command fingerprint")
	changedPassword := command
	changedPassword.Password = "different-password"
	passwordFingerprint, err := fingerprintBootstrapCommand(changedPassword)
	require.NoError(t, err, "changed password fingerprint")
	require.NotEqual(t, first, passwordFingerprint, "changed password fingerprint")
	changedSecret := command
	changedSecret.BootstrapSecret = "different-operator-bootstrap-secret-32-bytes"
	secretFingerprint, err := fingerprintBootstrapCommand(changedSecret)
	require.NoError(t, err, "changed secret fingerprint")
	require.NotEqual(t, first, secretFingerprint, "changed secret fingerprint")
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
		administratorRecoveryPolicy{},
	)
	command := BootstrapInstallationCommand{
		InstitutionName: "northbridge", AdministratorUsername: "admin",
		AdministratorEmail: "admin@example.com", Password: "password-value",
		BootstrapSecret: bootstrapProtection().Secret,
		Source:          " 192.0.2.10:443 ",
	}
	for attempt := 1; attempt <= 2; attempt++ {
		_, err := service.BootstrapInstallation(context.Background(), Invocation{}, command)
		require.NoError(t, err, "attempt %d error", attempt)
	}
	_, err := service.BootstrapInstallation(context.Background(), Invocation{}, command)
	require.True(t, apperror.Is(err, "authentication.rate_limited"), "attempt after maximum error = %v", err)

	command.Source = "192.0.2.11:443"
	_, err = service.BootstrapInstallation(context.Background(), Invocation{}, command)
	require.NoError(t, err)

	entries := cache.snapshot()
	require.Len(t, entries, 2, "source counters")
	for key := range entries {
		require.True(t, strings.HasPrefix(key, "authentication/attempts/installation-bootstrap/source/"), "counter key = %q", key)
		require.NotContains(t, key, "192.0.2")
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
		administratorRecoveryPolicy{},
	)
	_, err := service.BootstrapInstallation(context.Background(), Invocation{}, BootstrapInstallationCommand{
		InstitutionName: "northbridge", AdministratorUsername: "admin",
		AdministratorEmail: "admin@example.com", Password: "password-value",
		BootstrapSecret: bootstrapProtection().Secret,
		Source:          "192.0.2.10:443",
	})
	require.True(t, apperror.Is(err, "administration.unavailable"))
	require.ErrorIs(t, err, cacheFailure)
	want := []string{"rate-limit"}
	require.Equal(t, want, events, "events")
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
		administratorRecoveryPolicy{},
	)
	_, err := service.BootstrapInstallation(context.Background(), Invocation{}, BootstrapInstallationCommand{
		InstitutionName: "northbridge", AdministratorUsername: "admin",
		AdministratorEmail: "admin@example.com", Password: "password-value",
		BootstrapSecret: bootstrapProtection().Secret,
		Source:          "192.0.2.10:443",
	})
	require.True(t, apperror.Is(err, "administration.unavailable"), "error = %v", err)
	want := []string{}
	require.Equal(t, want, events, "events")
}
