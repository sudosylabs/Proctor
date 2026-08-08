// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// Mattermost's first-user administrator behavior was used as a product
// reference. Proctor replaces its process-local first-user check with an
// explicit, audited, PostgreSQL-serialized installation aggregate.

package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type GetInstallationStatusQuery struct{}

type BootstrapInstallationCommand struct {
	InstitutionName          string
	InstitutionDisplayName   string
	InstitutionDescription   string
	AdministratorUsername    string
	AdministratorEmail       string
	AdministratorDisplayName string
	AdministratorFirstName   string
	AdministratorLastName    string
	AdministratorLocale      string
	AdministratorTimezone    string
	Password                 string
	Source                   string
}

type installationStore interface {
	Get(context.Context) (*model.InstallationState, error)
	Bootstrap(context.Context, *store.InstallationBootstrap) (*model.InstallationBootstrapResult, error)
}

type passwordHash interface {
	Hash(string) (string, error)
}

type bootstrapRateLimiter interface {
	Allow(context.Context, string) error
}

type bootstrapService struct {
	installations installationStore
	hasher        passwordHash
	rateLimit     bootstrapRateLimiter
	nodeID        string
	now           func() time.Time
}

func newBootstrapService(
	installations installationStore,
	hasher passwordHash,
	rateLimit bootstrapRateLimiter,
	nodeID string,
	now func() time.Time,
) *bootstrapService {
	return &bootstrapService{
		installations: installations, hasher: hasher, rateLimit: rateLimit,
		nodeID: nodeID, now: now,
	}
}

func (a *App) GetInstallationStatus(ctx context.Context, _ GetInstallationStatusQuery) (*model.InstallationStatus, error) {
	return a.bootstrap.GetStatus(ctx)
}

func (s *bootstrapService) GetStatus(ctx context.Context) (*model.InstallationStatus, error) {
	state, err := s.installations.Get(ctx)
	if err != nil {
		if store.IsNotFound(err) {
			return &model.InstallationStatus{Initialized: false}, nil
		}
		return nil, NewError("installation.unavailable").Wrap(err)
	}
	if !state.IsValid() {
		return nil, NewError("installation.unavailable").Wrap(errors.New("persisted installation state is invalid"))
	}
	return &model.InstallationStatus{Initialized: true}, nil
}

func (a *App) BootstrapInstallation(ctx context.Context, invocation Invocation, command BootstrapInstallationCommand) (*model.InstallationBootstrapResult, error) {
	return a.bootstrap.Bootstrap(ctx, invocation, command)
}

func (s *bootstrapService) Bootstrap(ctx context.Context, invocation Invocation, command BootstrapInstallationCommand) (*model.InstallationBootstrapResult, error) {
	if strings.TrimSpace(command.InstitutionName) == "" || strings.TrimSpace(command.AdministratorUsername) == "" ||
		strings.TrimSpace(command.AdministratorEmail) == "" || command.Password == "" {
		return nil, NewError("request.invalid").WithField("field", "bootstrap")
	}
	status, err := s.GetStatus(ctx)
	if err != nil {
		return nil, err
	}
	if status.Initialized {
		return nil, NewError("installation.already_initialized")
	}
	if err := s.rateLimit.Allow(ctx, command.Source); err != nil {
		return nil, err
	}
	hash, err := s.hasher.Hash(command.Password)
	if err != nil {
		return nil, NewError("authentication.password.invalid").WithField("field", "password").Wrap(err)
	}
	metadata := invocation.RequestMetadata()
	result, err := s.installations.Bootstrap(ctx, &store.InstallationBootstrap{
		Institution: &model.Institution{
			Name: command.InstitutionName, DisplayName: command.InstitutionDisplayName,
			Description: command.InstitutionDescription,
		},
		Administrator: &model.User{
			Username: command.AdministratorUsername, Email: command.AdministratorEmail,
			DisplayName: command.AdministratorDisplayName, FirstName: command.AdministratorFirstName,
			LastName: command.AdministratorLastName, Locale: command.AdministratorLocale,
			Timezone: command.AdministratorTimezone,
		},
		PasswordHash: hash,
		Role: &model.Role{
			Name:        model.SystemAdministratorRoleName,
			DisplayName: "System Administrator",
			Description: "Protected administrator role for this Proctor installation.",
			Permissions: model.AllActions(),
			BuiltIn:     true,
		},
		RoleBinding: &model.RoleBinding{},
		AuditEvent: &model.AuditEvent{
			Action:     "installation.bootstrap",
			RequestID:  metadata.RequestId,
			NodeID:     s.nodeID,
			ClientType: "bootstrap",
			AuthMethod: "bootstrap",
			IPAddress:  metadata.IPAddress,
			UserAgent:  metadata.UserAgent,
		},
	})
	if err != nil {
		if store.IsConflict(err) {
			return nil, NewError("installation.already_initialized").Wrap(err)
		}
		return nil, NewError("installation.unavailable").Wrap(err)
	}
	return result, nil
}

type bootstrapCounterCache interface {
	Add(context.Context, string, int64, time.Duration) (int64, error)
}

type bootstrapRateLimit struct {
	cache                 bootstrapCounterCache
	window                time.Duration
	maximumSourceAttempts int
}

func (r bootstrapRateLimit) Allow(ctx context.Context, source string) error {
	key := "authentication/bootstrap/source/" + digestCacheKey(normalizeLoginSource(source))
	count, err := r.cache.Add(ctx, key, 1, r.window)
	if err != nil {
		return NewError("administration.unavailable").Wrap(err)
	}
	if count > int64(r.maximumSourceAttempts) {
		return NewError("authentication.rate_limited")
	}
	return nil
}
