// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// The authority snapshot is application metadata only. Its digest is never sent
// to a host. Renewed lease deadlines do not change the intent, but expiration,
// control ordering, Participation replacement and correction/lifecycle changes do.
type executionControlAuthority struct {
	CredentialUsable      bool   `db:"credential_usable"`
	UserRevision          int64  `db:"user_revision"`
	SittingWithinDeadline bool   `db:"sitting_within_deadline"`
	AttemptState          string `db:"attempt_state"`
	AttemptRevision       int64  `db:"attempt_revision"`
	SittingState          string `db:"sitting_state"`
	SittingRevision       int64  `db:"sitting_revision"`
	CurrentRevisionID     string `db:"current_revision_id"`
	CorrectionPending     bool   `db:"correction_pending"`
	ParticipationID       string `db:"participation_id"`
	LeaseUsable           bool   `db:"lease_usable"`
	SecurityAllowed       bool   `db:"security_allowed"`
	FreezeRequired        bool   `db:"freeze_required"`
	ControlLedger         []byte `db:"control_ledger"`
}

func (a executionControlAuthority) desired(grant *model.ExecutionGrant) model.ExecutionControlState {
	if !a.SittingWithinDeadline || grant.State == model.ExecutionGrantReleased || a.AttemptState != string(model.ExamAttemptActive) ||
		(a.SittingState != string(model.ExamSittingOpen) && a.SittingState != string(model.ExamSittingPaused)) {
		return model.ExecutionControlRevoked
	}
	if a.SittingState != string(model.ExamSittingOpen) || a.CorrectionPending || !a.CredentialUsable || !a.LeaseUsable || !a.SecurityAllowed || a.FreezeRequired {
		return model.ExecutionControlFrozen
	}
	return model.ExecutionControlRunning
}

func (a executionControlAuthority) digest() string {
	// This private fixed struct contains no maps, floats, or user-controlled text.
	body, _ := json.Marshal(a)
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}

// Callers have locked a/s before entering. The Attempt lock serializes changes
// to the current owner, control ledger and correction acknowledgements.
func readExecutionControlAuthority(ctx context.Context, tx *sqlxTxWrapper, attemptID model.ExamAttemptID) (executionControlAuthority, error) {
	var authority executionControlAuthority
	err := tx.Get(ctx, &authority, `SELECT s.scheduled_end_at>clock_timestamp() AS sitting_within_deadline,a.state AS attempt_state,a.revision AS attempt_revision,
 s.state AS sitting_state,s.revision AS sitting_revision,s.exam_revision_id AS current_revision_id,
 `+pendingTerminalCorrectionSQL+` AS correction_pending,
 COALESCE(se.archived_at IS NULL AND se.revoked_at IS NULL AND se.expires_at>clock_timestamp()
 AND se.idle_expires_at>clock_timestamp() AND dr.revoked_at IS NULL
 AND u.archived_at IS NULL AND u.disabled_at IS NULL AND se.user_id=a.candidate_user_id
 AND o.session_id=se.id AND o.registration_id=dr.id AND o.key_thumbprint=dr.key_thumbprint
 AND se.dpop_key_thumbprint=dr.key_thumbprint,false) AS credential_usable,
 COALESCE(u.revision,0) AS user_revision,
 COALESCE(p.id,'') AS participation_id,COALESCE(p.lease_expires_at>clock_timestamp(),false) AS lease_usable,
 COALESCE(o.security_interaction_allowed,false) AS security_allowed,COALESCE(o.freeze_required,true) AS freeze_required,
 COALESCE(o.control_ledger_canonical,''::bytea) AS control_ledger
 FROM exam_attempts a JOIN exam_sittings s ON s.id=a.exam_sitting_id AND s.exam_id=a.exam_id
 LEFT JOIN exam_attempt_participations p ON p.exam_attempt_id=a.id AND p.state='active'
 LEFT JOIN exam_attempt_security_owners o ON o.participation_id=p.id
 LEFT JOIN sessions se ON se.id=p.session_id
 LEFT JOIN desktop_registrations dr ON dr.id=se.desktop_registration_id AND dr.user_id=se.user_id
 LEFT JOIN users u ON u.id=a.candidate_user_id
 WHERE a.id=$1`, attemptID.String())
	return authority, err
}

func lockExecutionControl(ctx context.Context, tx *sqlxTxWrapper, id model.ExecutionGrantID) (*model.ExecutionGrant, executionControlAuthority, error) {
	var empty executionControlAuthority
	var attemptID string
	if err := tx.Get(ctx, &attemptID, `SELECT a.id FROM execution_grants g
 JOIN exam_attempts a ON a.id=g.exam_attempt_id JOIN exam_sittings s ON s.id=a.exam_sitting_id AND s.exam_id=a.exam_id
 WHERE g.id=$1 FOR UPDATE OF a FOR SHARE OF s`, id.String()); err != nil {
		return nil, empty, translateError("execution_grant", id.String(), err)
	}
	// Recheck and lock the live credential rows as well as the Attempt owner.
	// Revocation/disable cannot commit between the decision and acknowledgement.
	var credentialRows []string
	if err := tx.Select(ctx, &credentialRows, `SELECT p.id FROM exam_attempt_participations p
 JOIN sessions se ON se.id=p.session_id JOIN desktop_registrations dr ON dr.id=se.desktop_registration_id AND dr.user_id=se.user_id
 JOIN users u ON u.id=se.user_id WHERE p.exam_attempt_id=$1 AND p.state='active' FOR SHARE OF p,se,dr,u`, attemptID); err != nil {
		return nil, empty, err
	}
	var row executionGrantRow
	if err := tx.Get(ctx, &row, `SELECT `+executionGrantColumns+` FROM execution_grants WHERE id=$1 FOR UPDATE`, id.String()); err != nil {
		return nil, empty, translateError("execution_grant", id.String(), err)
	}
	grant, err := executionGrantModel(row)
	if err != nil {
		return nil, empty, err
	}
	authority, err := readExecutionControlAuthority(ctx, tx, grant.AttemptID)
	return grant, authority, err
}

func (s SQLExecutionGrantStore) PrepareControl(ctx context.Context, id model.ExecutionGrantID, epoch string, at time.Time) (*model.ExecutionGrant, error) {
	if !id.IsValid() || !model.ValidExecutionEnvironmentEpoch(epoch) || at.IsZero() {
		return nil, store.NewErrInvalidInput("execution_grant", "control", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "prepare execution control", func(ctx context.Context, tx *sqlxTxWrapper) (*model.ExecutionGrant, error) {
		grant, authority, err := lockExecutionControl(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		if grant.EnvironmentEpoch != epoch && (grant.EnvironmentEpoch != "" || grant.State != model.ExecutionGrantReserved) {
			return nil, store.NewErrConflict("execution_grant", "environment_epoch", nil)
		}
		desired, digest := authority.desired(grant), authority.digest()
		if grant.EnvironmentEpoch == epoch && grant.DesiredControlState == desired && grant.ControlAuthorityDigest == digest {
			return grant, nil
		}
		if grant.ControlRevision >= 1<<53-1 || grant.DesiredControlState == model.ExecutionControlRevoked {
			return nil, store.NewErrConflict("execution_grant", "control_revision", nil)
		}
		var row executionGrantRow
		err = tx.Get(ctx, &row, `UPDATE execution_grants SET environment_epoch=$2,control_revision=control_revision+1,
   desired_control_state=$3,control_authority_digest=$4,updated_at=GREATEST(updated_at,$5),revision=revision+1
   WHERE id=$1 RETURNING `+executionGrantColumns, id.String(), epoch, string(desired), digest, model.TimeUTC(at))
		if err != nil {
			return nil, err
		}
		return executionGrantModel(row)
	})
}

func (s SQLExecutionGrantStore) AcknowledgeControl(ctx context.Context, fence model.ExecutionFence, state model.ExecutionControlState, at time.Time) (*model.ExecutionGrant, error) {
	if fence.Validate() != nil || at.IsZero() || (state != model.ExecutionControlRunning && state != model.ExecutionControlFrozen && state != model.ExecutionControlRevoked) {
		return nil, store.NewErrInvalidInput("execution_grant", "control_acknowledgement", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "acknowledge execution control", func(ctx context.Context, tx *sqlxTxWrapper) (*model.ExecutionGrant, error) {
		grant, authority, err := lockExecutionControl(ctx, tx, fence.GrantID)
		if err != nil {
			return nil, err
		}
		if grant.Fence() != fence || grant.DesiredControlState != state || authority.desired(grant) != state || grant.ControlAuthorityDigest != authority.digest() {
			return nil, store.NewErrConflict("execution_grant", "control_acknowledgement", nil)
		}
		if grant.ControlAcknowledgedRevision == fence.ControlRevision {
			return grant, nil
		}
		var row executionGrantRow
		err = tx.Get(ctx, &row, `UPDATE execution_grants SET control_acknowledged_revision=control_revision,
   updated_at=GREATEST(updated_at,$2),revision=revision+1 WHERE id=$1 RETURNING `+executionGrantColumns, fence.GrantID.String(), model.TimeUTC(at))
		if errors.Is(err, sql.ErrNoRows) {
			return nil, store.NewErrConflict("execution_grant", "control_acknowledgement", err)
		}
		if err != nil {
			return nil, err
		}
		return executionGrantModel(row)
	})
}
