// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
//
// Mattermost's first-user administrator behavior was used as a product
// reference. Proctor replaces its process-local first-user check with an
// explicit, audited, PostgreSQL-serialized installation aggregate.

package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
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
	BootstrapSecret          string
	Source                   string
}

// AdministratorRecoveryCommand is accepted only by the host-level offline
// capability. Password is private input and is replaced with an encoded hash
// before the named Store aggregate is invoked.
type AdministratorRecoveryCommand struct {
	InstitutionID    string
	UserID           string
	EnableLocalLogin bool
	Password         string
}

type AdministratorRecoveryResult struct {
	LocalLoginEnabled bool
	PasswordRotated   bool
}

// BootstrapProtectionPolicy is the immutable deployment-owned proof used by
// the public one-time bootstrap command. The service retains only its digest.
type BootstrapProtectionPolicy struct {
	Secret string
}

type installationStore interface {
	Get(context.Context) (*model.InstallationState, error)
	Bootstrap(context.Context, *store.InstallationBootstrap) (*model.InstallationBootstrapResult, error)
	ReconcileSystemAdministratorRole(context.Context, *store.SystemAdministratorRoleReconciliation) (*store.SystemAdministratorRoleReconciliationResult, error)
	RecoverAdministratorAccess(context.Context, *store.AdministratorRecovery) (*store.AdministratorRecoveryResult, error)
	ReconcileAdministratorRecovery(context.Context, *store.AdministratorRecoveryReconciliation) (*store.AdministratorRecoveryReconciliationResult, error)
}

// RecoverAdministratorAccess is intentionally absent from every network
// application interface. The module root borrows this method only for the
// explicit offline host command.
func (a *App) RecoverAdministratorAccess(ctx context.Context, command AdministratorRecoveryCommand) (*AdministratorRecoveryResult, error) {
	if a == nil || a.bootstrap == nil {
		return nil, errors.New("administrator recovery is unavailable")
	}
	return a.bootstrap.RecoverAdministratorAccess(ctx, command)
}

func (s *bootstrapService) RecoverAdministratorAccess(ctx context.Context, command AdministratorRecoveryCommand) (*AdministratorRecoveryResult, error) {
	institutionID, institutionErr := model.ParseInstitutionID(strings.TrimSpace(command.InstitutionID))
	userID, userErr := model.ParseUserID(strings.TrimSpace(command.UserID))
	if institutionErr != nil || userErr != nil || (!command.EnableLocalLogin && command.Password == "") {
		return nil, NewError("administrator_recovery.invalid")
	}
	passwordHash := ""
	if command.Password != "" {
		var err error
		passwordHash, err = s.hasher.Hash(command.Password)
		if err != nil {
			return nil, NewError("authentication.password.invalid").WithField("field", "password").Wrap(err)
		}
	}
	result, err := s.installations.RecoverAdministratorAccess(ctx, &store.AdministratorRecovery{
		InstitutionID: institutionID, UserID: userID,
		EnableLocalLogin: command.EnableLocalLogin, RotatePasswordHash: passwordHash,
	})
	if err != nil {
		return nil, NewError("administrator_recovery.failed").Wrap(err)
	}
	if result == nil {
		return nil, NewError("administrator_recovery.failed").Wrap(errors.New("persistence returned no recovery result"))
	}
	return &AdministratorRecoveryResult{
		LocalLoginEnabled: result.LocalLoginEnabled,
		PasswordRotated:   result.PasswordRotated,
	}, nil
}

// ReconcileAdministratorRecovery records every completed offline recovery in
// ordinary audit before this node starts workers or network transports.
func (a *App) ReconcileAdministratorRecovery(ctx context.Context) error {
	if a == nil || a.bootstrap == nil {
		return errors.New("administrator recovery reconciliation is unavailable")
	}
	return a.bootstrap.ReconcileAdministratorRecovery(ctx)
}

func (s *bootstrapService) ReconcileAdministratorRecovery(ctx context.Context) error {
	_, err := s.installations.ReconcileAdministratorRecovery(ctx, &store.AdministratorRecoveryReconciliation{NodeID: s.nodeID})
	if err != nil {
		return fmt.Errorf("reconcile offline administrator recovery: %w", err)
	}
	return nil
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
	bootstrapSecretDigest [sha256.Size]byte
	bootstrapAvailable    bool
	nodeID                string
	now                   func() time.Time
}

func newBootstrapService(
	installations installationStore,
	hasher passwordHash,
	attempts *authenticationAttemptAccounting,
	rateLimit LoginRateLimitPolicy,
	protection BootstrapProtectionPolicy,
	nodeID string,
	now func() time.Time,
) *bootstrapService {
	return &bootstrapService{
		installations: installations, hasher: hasher, attempts: attempts,
		rateLimitWindow: rateLimit.Window, maximumSourceAttempts: rateLimit.MaximumSourceAttempts,
		bootstrapSecretDigest: digestBootstrapSecret(protection.Secret),
		bootstrapAvailable:    protection.Secret != "",
		nodeID:                nodeID, now: now,
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
	if err := s.checkRateLimit(ctx, command.Source); err != nil {
		return nil, err
	}
	providedDigest := digestBootstrapSecret(command.BootstrapSecret)
	if !s.bootstrapAvailable || subtle.ConstantTimeCompare(providedDigest[:], s.bootstrapSecretDigest[:]) != 1 {
		return nil, NewError("installation.bootstrap_denied")
	}
	fingerprint, err := fingerprintBootstrapCommand(command)
	if err != nil {
		return nil, NewError("request.invalid").WithField("field", "bootstrap").Wrap(err)
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
	input := &store.InstallationBootstrap{
		BootstrapSecretDigest: providedDigest,
		CommandFingerprint:    fingerprint,
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
		RoleBinding:  &model.RoleBinding{},
		AccessPolicy: model.NewInitialAccessPolicy(model.NewAccessPolicyID(), administrator.CreatedAt),
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
	}
	result, err := s.installations.Bootstrap(ctx, input)
	if err != nil {
		if store.IsConflict(err) {
			return nil, NewError("installation.already_initialized").Wrap(err)
		}
		// The commit boundary may be ambiguous. Repeating the proof- and
		// fingerprint-fenced named aggregate either returns the retained exact
		// outcome or proves that this attempt did not win.
		result, retryErr := s.installations.Bootstrap(ctx, input)
		if retryErr == nil {
			return result, nil
		}
		if store.IsConflict(retryErr) {
			return nil, NewError("installation.already_initialized").Wrap(retryErr)
		}
		return nil, NewError("installation.unavailable").Wrap(errors.Join(err, retryErr))
	}
	return result, nil
}

func digestBootstrapSecret(secret string) [sha256.Size]byte {
	return sha256.Sum256(append([]byte("proctor:installation-bootstrap-secret:v1\x00"), []byte(secret)...))
}

func fingerprintBootstrapCommand(command BootstrapInstallationCommand) ([sha256.Size]byte, error) {
	payload, err := json.Marshal(struct {
		InstitutionName, InstitutionDisplayName, InstitutionDescription   string
		AdministratorUsername, AdministratorEmail                         string
		AdministratorDisplayName, AdministratorFirstName                  string
		AdministratorLastName, AdministratorLocale, AdministratorTimezone string
		Password                                                          string
	}{
		command.InstitutionName, command.InstitutionDisplayName, command.InstitutionDescription,
		command.AdministratorUsername, command.AdministratorEmail,
		command.AdministratorDisplayName, command.AdministratorFirstName,
		command.AdministratorLastName, command.AdministratorLocale, command.AdministratorTimezone,
		command.Password,
	})
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	// The raw deployment secret keys the fingerprint so the durable value does
	// not become a fast offline password verifier if the database is exposed.
	mac := hmac.New(sha256.New, []byte(command.BootstrapSecret))
	_, _ = mac.Write([]byte("proctor:installation-bootstrap-command:v1\x00"))
	_, _ = mac.Write(payload)
	var fingerprint [sha256.Size]byte
	copy(fingerprint[:], mac.Sum(nil))
	return fingerprint, nil
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
