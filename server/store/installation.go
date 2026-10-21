// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package store

import (
	"context"
	"crypto/sha256"

	"github.com/sudosylabs/proctor/server/model"
)

// InstallationBootstrap is the complete durable input for the one-time
// installation bootstrap transaction. PasswordHash is already encoded by the
// application password hasher and must never be logged or audited.
type InstallationBootstrap struct {
	BootstrapSecretDigest    [sha256.Size]byte
	CommandFingerprint       [sha256.Size]byte
	Institution              *model.Institution
	Administrator            *model.User
	AdministratorSettings    *model.UserSettingsDocument
	PasswordHash             string
	Role                     *model.Role
	RoleBinding              *model.RoleBinding
	AccessPolicy             *model.AccessPolicy
	AuditEvent               *model.AuditEvent
	DefaultProfilePictureJob *model.Job
}

// SystemAdministratorRoleReconciliation is the complete durable input for
// adding newly registered grantable actions to the protected built-in Role.
// Existing permissions are preserved so a rolling downgrade does not destroy
// actions unknown to the current binary.
type SystemAdministratorRoleReconciliation struct {
	RequiredPermissions []string
	ReconciledAt        int64
	AuditEvent          *model.AuditEvent
}

// SystemAdministratorRoleReconciliationResult reports the authoritative Role
// after reconciliation. A nil Role with Changed false means the installation
// is still pristine and therefore has nothing to reconcile.
type SystemAdministratorRoleReconciliationResult struct {
	Role             *model.Role
	Changed          bool
	AddedPermissions []string
}

// AdministratorRecovery is the secret-bearing input to the offline,
// installation-scoped recovery aggregate. RotatePasswordHash is already an
// encoded password hash; plaintext credentials never cross the Store boundary.
type AdministratorRecovery struct {
	InstitutionID      model.InstitutionID
	UserID             model.UserID
	EnableLocalLogin   bool
	RotatePasswordHash string
}

type AdministratorRecoveryResult struct {
	RecordID          model.AdministratorRecoveryRecordID
	LocalLoginEnabled bool
	PasswordRotated   bool
}

// AdministratorRecoveryReconciliation is the bounded startup command that
// turns pending offline security records into ordinary durable audit events.
type AdministratorRecoveryReconciliation struct {
	NodeID string
}

type AdministratorRecoveryReconciliationResult struct {
	Reconciled int
}

// InstallationStore owns the cross-model transaction that makes a pristine
// database into an initialized logical Proctor installation.
type InstallationStore interface {
	Get(context.Context) (*model.InstallationState, error)
	Bootstrap(context.Context, *InstallationBootstrap) (*model.InstallationBootstrapResult, error)
	ReconcileSystemAdministratorRole(context.Context, *SystemAdministratorRoleReconciliation) (*SystemAdministratorRoleReconciliationResult, error)
	RecoverAdministratorAccess(context.Context, *AdministratorRecovery) (*AdministratorRecoveryResult, error)
	ResetAdministratorMFA(context.Context, *AdministratorMFAReset) (*AdministratorMFAResetResult, error)
	ReconcileAdministratorRecovery(context.Context, *AdministratorRecoveryReconciliation) (*AdministratorRecoveryReconciliationResult, error)
}
