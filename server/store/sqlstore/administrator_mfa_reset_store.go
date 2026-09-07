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
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// lockOfflineAdministratorRecovery fences both host recovery commands against
// live or starting nodes, protected administrator changes, and each other.
// Its pending evidence is reconciled before a normal node begins serving.
func lockOfflineAdministratorRecovery(ctx context.Context, tx *sqlxTxWrapper, institutionID model.InstitutionID, userID model.UserID) (time.Time, error) {
	if err := lockServingNodeLeaseFence(ctx, tx); err != nil {
		return time.Time{}, err
	}
	if err := lockSystemAdministratorAuthenticationPaths(ctx, tx); err != nil {
		return time.Time{}, err
	}
	var installation installationStateRow
	if err := tx.Get(ctx, &installation, `SELECT initialized_at, institution_id, administrator_user_id FROM installation_states WHERE singleton=1 FOR UPDATE`); err != nil {
		return time.Time{}, translateError("installation", "singleton", err)
	}
	if installation.InstitutionID != institutionID.String() {
		return time.Time{}, store.NewErrConflict("administrator_recovery", "installation_mismatch", nil)
	}
	var at time.Time
	if err := tx.Get(ctx, &at, `SELECT clock_timestamp()`); err != nil {
		return time.Time{}, fmt.Errorf("read administrator recovery time: %w", err)
	}
	var serving bool
	if err := tx.Get(ctx, &serving, `SELECT EXISTS(SELECT 1 FROM serving_node_leases WHERE expires_at>?)`, at); err != nil {
		return time.Time{}, fmt.Errorf("check live serving nodes: %w", err)
	}
	if serving {
		return time.Time{}, store.NewErrConflict("administrator_recovery", "serving_node_active", nil)
	}
	if _, err := getPendingAdministratorRecovery(ctx, tx); err == nil {
		return time.Time{}, store.NewErrConflict("administrator_recovery", "pending", nil)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, fmt.Errorf("read pending administrator recovery: %w", err)
	}
	active, err := isActiveSystemAdministrator(ctx, tx, userID.String(), at)
	if err != nil {
		return time.Time{}, err
	}
	if !active {
		return time.Time{}, store.NewErrConflict("administrator_recovery", "target_not_active_system_administrator", nil)
	}
	return model.TimeUTC(at), nil
}

func (s SQLInstallationStore) ResetAdministratorMFA(ctx context.Context, input *store.AdministratorMFAReset) (*store.AdministratorMFAResetResult, error) {
	if input == nil || !input.InstitutionID.IsValid() || !input.UserID.IsValid() || !input.MFAEnabled || !validAccessDeploymentCapabilities(input.Capabilities) {
		return nil, store.NewErrInvalidInput("administrator_recovery", "mfa_reset", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "offline administrator MFA reset", func(ctx context.Context, tx *sqlxTxWrapper) (*store.AdministratorMFAResetResult, error) {
		if _, err := lockOfflineAdministratorRecovery(ctx, tx, input.InstitutionID, input.UserID); err != nil {
			return nil, err
		}
		if err := lockUserSessions(ctx, tx, input.UserID.String()); err != nil {
			return nil, err
		}
		if err := lockMFAUser(ctx, tx, input.UserID.String()); err != nil {
			return nil, err
		}
		if err := lockPersonalAccessTokensForUser(ctx, tx, input.UserID.String()); err != nil {
			return nil, err
		}
		policy, err := getAccessPolicy(ctx, tx, "FOR SHARE")
		if err != nil {
			return nil, err
		}
		// Recovery may be restarted for an already restricted administrator.
		// Only an existing primary method is required here; this User is not
		// counted as an ordinary usable administrator path until reenrollment.
		usable, err := hasUsableUserAuthenticationPath(ctx, tx, input.UserID.String(), policy.Settings(), input.Capabilities, false, "")
		if err != nil {
			return nil, err
		}
		if !usable {
			return nil, store.NewErrConflict("administrator_recovery", "primary_authentication_unavailable", nil)
		}
		var at time.Time
		if err = tx.Get(ctx, &at, `SELECT clock_timestamp()`); err != nil {
			return nil, err
		}
		// A binding can reach its scheduled end while another fence is held.
		active, err := isActiveSystemAdministrator(ctx, tx, input.UserID.String(), at)
		if err != nil {
			return nil, err
		}
		if !active {
			return nil, store.NewErrConflict("administrator_recovery", "target_not_active_system_administrator", nil)
		}
		reset, err := resetMFAUserAccess(ctx, tx, input.UserID, model.TimeUTC(at))
		if err != nil {
			return nil, err
		}
		if reset == nil || reset.Recovery == nil || reset.Recovery.Validate() != nil || !reset.Recovery.ReenrollmentRequired {
			return nil, invalidPersistedState("administrator_recovery", "mfa_reset", errors.New("reset returned no valid recovery restriction"))
		}
		id := model.NewId()
		if _, err = tx.Exec(ctx, `INSERT INTO administrator_recovery_records
			(id,created_at,institution_id,user_id,local_login_enabled,password_rotated,mfa_reset,mfa_recovery_generation)
			VALUES(?,?,?,?,false,false,true,?)`, id, at, input.InstitutionID.String(), input.UserID.String(), reset.Recovery.Generation); err != nil {
			return nil, fmt.Errorf("save administrator MFA recovery evidence: %w", err)
		}
		return &store.AdministratorMFAResetResult{RecordID: id, Recovery: reset.Recovery}, nil
	})
}
