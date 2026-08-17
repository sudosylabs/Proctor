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
	"fmt"
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
	ReconcileSystemAdministratorRole(context.Context, *store.SystemAdministratorRoleReconciliation) (*store.SystemAdministratorRoleReconciliationResult, error)
}

type passwordHash interface {
	Hash(string) (string, error)
}

type bootstrapService struct {
	installations         installationStore
	hasher                passwordHash
	attempts              *authenticationAttemptAccounting
	rateLimitWindow       time.Duration
	maximumSourceAttempts int
	nodeID                string
	now                   func() time.Time
}

func newBootstrapService(
	installations installationStore,
	hasher passwordHash,
	attempts *authenticationAttemptAccounting,
	rateLimit LoginRateLimitPolicy,
	nodeID string,
	now func() time.Time,
) *bootstrapService {
	return &bootstrapService{
		installations: installations, hasher: hasher, attempts: attempts,
		rateLimitWindow: rateLimit.Window, maximumSourceAttempts: rateLimit.MaximumSourceAttempts,
		nodeID: nodeID, now: now,
	}
}

// ReconcileSystemAdministratorRole adds newly registered grantable actions to
// the protected built-in Role before this node serves application traffic.
func (a *App) ReconcileSystemAdministratorRole(ctx context.Context) error {
	if a == nil || a.bootstrap == nil {
		return errors.New("system-administrator Role reconciliation is unavailable")
	}
	return a.bootstrap.ReconcileSystemAdministratorRole(ctx)
}

func (s *bootstrapService) ReconcileSystemAdministratorRole(ctx context.Context) error {
	_, err := s.installations.ReconcileSystemAdministratorRole(ctx, &store.SystemAdministratorRoleReconciliation{
		RequiredPermissions: model.AllActions(),
		ReconciledAt:        s.now().UnixMilli(),
		AuditEvent: &model.AuditEvent{
			Action: "role.system_admin.reconcile", Status: model.AuditStatusSuccess,
			NodeID: s.nodeID, ClientType: "system",
		},
	})
	if err != nil {
		return fmt.Errorf("reconcile protected system-administrator Role: %w", err)
	}
	return nil
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
	if err := state.Validate(); err != nil {
		return nil, NewError("installation.unavailable").Wrap(err)
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
	if err := s.checkRateLimit(ctx, command.Source); err != nil {
		return nil, err
	}
	hash, err := s.hasher.Hash(command.Password)
	if err != nil {
		return nil, NewError("authentication.password.invalid").WithField("field", "password").Wrap(err)
	}
	metadata := invocation.RequestMetadata()
	administrator, defaultPictureJob, err := prepareUserDefaultProfilePictureJob(&model.User{
		Username: command.AdministratorUsername, Email: command.AdministratorEmail,
		DisplayName: command.AdministratorDisplayName, FirstName: command.AdministratorFirstName,
		LastName: command.AdministratorLastName, Locale: command.AdministratorLocale,
		Timezone: command.AdministratorTimezone,
	}, s.now())
	if err != nil {
		return nil, NewError("request.invalid").WithField("field", "administrator").Wrap(err)
	}
	administratorSettings, err := prepareInitialUserSettingsDocument(administrator)
	if err != nil {
		return nil, NewError("request.invalid").WithField("field", "administrator").Wrap(err)
	}
	result, err := s.installations.Bootstrap(ctx, &store.InstallationBootstrap{
		Institution: &model.Institution{
			Name: command.InstitutionName, DisplayName: command.InstitutionDisplayName,
			Description: command.InstitutionDescription,
		},
		Administrator:         administrator,
		AdministratorSettings: administratorSettings,
		PasswordHash:          hash,
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
			RequestID:  metadata.RequestID,
			NodeID:     s.nodeID,
			ClientType: "bootstrap",
			AuthMethod: "bootstrap",
			IPAddress:  metadata.IPAddress,
			UserAgent:  metadata.UserAgent,
		},
		DefaultProfilePictureJob: defaultPictureJob,
	})
	if err != nil {
		if store.IsConflict(err) {
			return nil, NewError("installation.already_initialized").Wrap(err)
		}
		return nil, NewError("installation.unavailable").Wrap(err)
	}
	return result, nil
}

func (s *bootstrapService) checkRateLimit(ctx context.Context, source string) error {
	_, limited, err := s.attempts.account(ctx, authenticationAttemptIntent{
		purpose: authenticationAttemptPurposeInstallationBootstrap,
		window:  s.rateLimitWindow,
		limits: []authenticationAttemptLimit{{
			dimension: authenticationAttemptDimensionSource,
			maximum:   s.maximumSourceAttempts,
			source:    source,
		}},
	})
	if err != nil {
		return NewError("administration.unavailable").Wrap(err)
	}
	if limited {
		return NewError("authentication.rate_limited")
	}
	return nil
}
