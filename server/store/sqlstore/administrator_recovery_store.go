// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type administratorRecoveryRow struct {
	ID                 string         `db:"id"`
	CreatedAt          time.Time      `db:"created_at"`
	InstitutionID      string         `db:"institution_id"`
	UserID             string         `db:"user_id"`
	LocalLoginEnabled  bool           `db:"local_login_enabled"`
	PasswordRotated    bool           `db:"password_rotated"`
	MFAReset           bool           `db:"mfa_reset"`
	MFAGeneration      sql.NullInt64  `db:"mfa_recovery_generation"`
	PolicyFromRevision sql.NullInt64  `db:"policy_from_revision"`
	PolicyToRevision   sql.NullInt64  `db:"policy_to_revision"`
	ReconciledAt       sql.NullTime   `db:"reconciled_at"`
	AuditEventID       sql.NullString `db:"audit_event_id"`
}

func (s SQLInstallationStore) RecoverAdministratorAccess(ctx context.Context, input *store.AdministratorRecovery) (*store.AdministratorRecoveryResult, error) {
	if err := validateAdministratorRecovery(input); err != nil {
		return nil, err
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "offline administrator recovery", func(ctx context.Context, tx *sqlxTxWrapper) (*store.AdministratorRecoveryResult, error) {
		databaseNow, err := lockOfflineAdministratorRecovery(ctx, tx, input.InstitutionID, input.UserID)
		if err != nil {
			return nil, err
		}
		policy, err := getAccessPolicy(ctx, tx, "FOR UPDATE")
		if err != nil {
			return nil, err
		}
		var credential passwordCredentialRow
		if err := tx.Get(ctx, &credential, `SELECT id, created_at, updated_at, archived_at, user_id, revision, password_hash, password_changed_at FROM password_credentials WHERE user_id=$1 AND archived_at IS NULL FOR UPDATE`, input.UserID.String()); err != nil {
			return nil, fmt.Errorf("get recovery password credential: %w", translateError("password_credential", input.UserID.String(), err))
		}
		if _, err := credential.model(); err != nil {
			return nil, err
		}
		if input.RotatePasswordHash != "" && !policy.LocalLoginEnabled && !input.EnableLocalLogin {
			return nil, store.NewErrConflict("administrator_recovery", "local_login_disabled", nil)
		}
		localLoginChanged := input.EnableLocalLogin && !policy.LocalLoginEnabled
		if !localLoginChanged && input.RotatePasswordHash == "" {
			return nil, store.NewErrConflict("administrator_recovery", "no_effect", nil)
		}
		if input.RotatePasswordHash != "" {
			if _, err := tx.Exec(ctx, `UPDATE password_credentials SET updated_at=GREATEST(updated_at, $1), password_hash=$2, revision=revision+1, password_changed_at=GREATEST(password_changed_at, $1) WHERE id=$3 AND user_id=$4 AND archived_at IS NULL`, databaseNow, input.RotatePasswordHash, credential.ID, input.UserID.String()); err != nil {
				return nil, fmt.Errorf("rotate recovery password: %w", translateError("password_credential", credential.ID, err))
			}
		}
		if localLoginChanged {
			if _, err := tx.Exec(ctx, `UPDATE access_policies SET revision=revision+1, updated_at=GREATEST(updated_at, $1), local_login_enabled=TRUE WHERE singleton=1 AND id=$2`, databaseNow, policy.ID.String()); err != nil {
				return nil, fmt.Errorf("enable local login for administrator recovery: %w", err)
			}
		}
		row := administratorRecoveryRow{
			ID: model.NewId(), CreatedAt: databaseNow, InstitutionID: input.InstitutionID.String(), UserID: input.UserID.String(),
			LocalLoginEnabled: localLoginChanged, PasswordRotated: input.RotatePasswordHash != "",
		}
		if localLoginChanged {
			row.PolicyFromRevision = sql.NullInt64{Int64: policy.Revision, Valid: true}
			row.PolicyToRevision = sql.NullInt64{Int64: policy.Revision + 1, Valid: true}
		}
		if _, err := tx.NamedExec(ctx, `INSERT INTO administrator_recovery_records (id, created_at, institution_id, user_id, local_login_enabled, password_rotated, policy_from_revision, policy_to_revision) VALUES (:id, :created_at, :institution_id, :user_id, :local_login_enabled, :password_rotated, :policy_from_revision, :policy_to_revision)`, &row); err != nil {
			return nil, fmt.Errorf("save administrator recovery record: %w", translateError("administrator_recovery", row.ID, err))
		}
		return administratorRecoveryResult(row), nil
	})
}

func (s SQLInstallationStore) ReconcileAdministratorRecovery(ctx context.Context, input *store.AdministratorRecoveryReconciliation) (*store.AdministratorRecoveryReconciliationResult, error) {
	if input == nil || strings.TrimSpace(input.NodeID) == "" || len(strings.TrimSpace(input.NodeID)) > 128 {
		return nil, store.NewErrInvalidInput("administrator_recovery", "reconciliation", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "reconcile offline administrator recovery", func(ctx context.Context, tx *sqlxTxWrapper) (*store.AdministratorRecoveryReconciliationResult, error) {
		row, err := getPendingAdministratorRecovery(ctx, tx)
		if errors.Is(err, sql.ErrNoRows) {
			return &store.AdministratorRecoveryReconciliationResult{}, nil
		}
		if err != nil {
			return nil, fmt.Errorf("read pending administrator recovery: %w", err)
		}
		var databaseNow time.Time
		if err := tx.Get(ctx, &databaseNow, `SELECT clock_timestamp()`); err != nil {
			return nil, fmt.Errorf("read administrator recovery reconciliation time: %w", err)
		}
		changedFields := make([]string, 0, 4)
		if row.LocalLoginEnabled {
			changedFields = append(changedFields, "local_login_enabled")
		}
		if row.PasswordRotated {
			changedFields = append(changedFields, "password_credential")
		}
		action := "authentication.administrator_recovery"
		resultFields := map[string]any{
			"changed_fields":       changedFields,
			"local_login_enabled":  row.LocalLoginEnabled,
			"password_rotated":     row.PasswordRotated,
			"policy_from_revision": administratorRecoveryNullableInt64(row.PolicyFromRevision),
			"policy_to_revision":   administratorRecoveryNullableInt64(row.PolicyToRevision),
		}
		if row.MFAReset {
			if !row.MFAGeneration.Valid || row.MFAGeneration.Int64 <= 0 {
				return nil, store.NewErrConflict("administrator_recovery", "mfa_evidence_invalid", nil)
			}
			action = "authentication.administrator_mfa_reset"
			resultFields = map[string]any{
				"changed_fields": []string{"mfa_credentials", "mfa_recovery_codes", "user_mfa_recovery", "user_access"},
				"mfa_reset":      true, "mfa_recovery_generation": row.MFAGeneration.Int64, "reenrollment_required": true,
			}
		}
		result, err := model.EncodeAuditData(resultFields)
		if err != nil {
			return nil, err
		}
		event := &model.AuditEvent{
			Action: action, Resource: model.Resource{Type: model.ResourceUser, ID: row.UserID},
			ScopeType: model.RoleScopeInstitution, ScopeID: row.InstitutionID, Status: model.AuditStatusSuccess,
			NodeID: strings.TrimSpace(input.NodeID), ClientType: "system", Result: result,
		}
		event.PrepareCreate(model.NewAuditEventID(), databaseNow)
		if err := event.Validate(); err != nil {
			return nil, store.NewErrInvalidInput("audit_event", "administrator_recovery", nil).Wrap(err)
		}
		if err := insertInstallationAudit(ctx, tx, event); err != nil {
			return nil, fmt.Errorf("save administrator recovery audit: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE administrator_recovery_records SET reconciled_at=$1, audit_event_id=$2 WHERE id=$3 AND reconciled_at IS NULL`, databaseNow, event.ID.String(), row.ID); err != nil {
			return nil, fmt.Errorf("complete administrator recovery record: %w", err)
		}
		return &store.AdministratorRecoveryReconciliationResult{Reconciled: 1}, nil
	})
}

func getPendingAdministratorRecovery(ctx context.Context, executor sqlxExecutor) (administratorRecoveryRow, error) {
	var row administratorRecoveryRow
	err := executor.Get(ctx, &row, `SELECT id, created_at, institution_id, user_id, local_login_enabled, password_rotated, mfa_reset, mfa_recovery_generation, policy_from_revision, policy_to_revision, reconciled_at, audit_event_id FROM administrator_recovery_records WHERE reconciled_at IS NULL ORDER BY created_at, id LIMIT 1 FOR UPDATE`)
	return row, err
}

func administratorRecoveryResult(row administratorRecoveryRow) *store.AdministratorRecoveryResult {
	return &store.AdministratorRecoveryResult{RecordID: row.ID, LocalLoginEnabled: row.LocalLoginEnabled, PasswordRotated: row.PasswordRotated}
}

func administratorRecoveryNullableInt64(value sql.NullInt64) any {
	if !value.Valid {
		return nil
	}
	return value.Int64
}

func validateAdministratorRecovery(input *store.AdministratorRecovery) error {
	if input == nil || !input.InstitutionID.IsValid() || !input.UserID.IsValid() ||
		(!input.EnableLocalLogin && input.RotatePasswordHash == "") || len(input.RotatePasswordHash) > 4096 {
		return store.NewErrInvalidInput("administrator_recovery", "value", nil)
	}
	return nil
}

var _ store.InstallationStore = (*SQLInstallationStore)(nil)
