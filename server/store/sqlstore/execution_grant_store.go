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
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type SQLExecutionGrantStore struct{ *SQLStore }

type executionGrantRow struct {
	ID                     string         `db:"id"`
	AttemptID              string         `db:"exam_attempt_id"`
	HostID                 string         `db:"host_id"`
	Image                  string         `db:"image"`
	Network                string         `db:"network"`
	State                  string         `db:"state"`
	AppliedSittingState    string         `db:"applied_sitting_state"`
	AppliedSittingRevision int64          `db:"applied_sitting_revision"`
	LifecyclePending       bool           `db:"lifecycle_pending"`
	PendingSittingState    sql.NullString `db:"pending_sitting_state"`
	PendingSittingRevision sql.NullInt64  `db:"pending_sitting_revision"`
	CreatedAt              time.Time      `db:"created_at"`
	UpdatedAt              time.Time      `db:"updated_at"`
	ReleasedAt             sql.NullTime   `db:"released_at"`
	RevokedAt              sql.NullTime   `db:"revoked_at"`
	Revision               int64          `db:"revision"`
}

type executionGrantConvergenceRow struct {
	executionGrantRow
	AttemptState            string `db:"attempt_state"`
	SittingState            string `db:"sitting_state"`
	SittingRevision         int64  `db:"sitting_revision"`
	AcknowledgementRequired bool   `db:"acknowledgement_required"`
}

const executionGrantColumns = `id,exam_attempt_id,host_id,image,network,state,applied_sitting_state,applied_sitting_revision,lifecycle_pending,pending_sitting_state,pending_sitting_revision,created_at,updated_at,released_at,revoked_at,revision`

func newSQLExecutionGrantStore(sqlStore *SQLStore) store.ExecutionGrantStore {
	return &SQLExecutionGrantStore{SQLStore: sqlStore}
}

func (s SQLExecutionGrantStore) Current(ctx context.Context, attemptID model.ExamAttemptID) (*model.ExecutionGrant, error) {
	if !attemptID.IsValid() {
		return nil, store.NewErrInvalidInput("execution_grant", "exam_attempt_id", nil)
	}
	var row executionGrantRow
	err := s.GetMaster().Get(ctx, &row, `SELECT `+executionGrantColumns+` FROM execution_grants
		WHERE exam_attempt_id=$1 AND state IN ('reserved','ready')`, attemptID.String())
	if err != nil {
		return nil, translateError("execution_grant", attemptID.String(), err)
	}
	return executionGrantModel(row)
}

func (s SQLExecutionGrantStore) Reserve(ctx context.Context, reservation store.ExecutionGrantReservation) (*model.ExecutionGrant, error) {
	if err := validateExecutionReservation(reservation); err != nil {
		return nil, err
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "reserve execution grant", func(ctx context.Context, tx *sqlxTxWrapper) (*model.ExecutionGrant, error) {
		if err := lockExecutableAttempt(ctx, tx, reservation.AttemptID); err != nil {
			return nil, err
		}
		var current executionGrantRow
		err := tx.Get(ctx, &current, `SELECT `+executionGrantColumns+` FROM execution_grants
			WHERE exam_attempt_id=$1 AND state IN ('reserved','ready') FOR UPDATE`, reservation.AttemptID.String())
		if err == nil {
			if current.HostID == reservation.HostID && current.Image == reservation.Image && current.Network == string(reservation.Network) {
				return executionGrantModel(current)
			}
			return nil, store.NewErrConflict("execution_grant", "execution_grants_one_active_attempt_idx", nil)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("read current execution grant: %w", err)
		}
		row, err := insertExecutionGrant(ctx, tx, reservation)
		if err != nil {
			return nil, err
		}
		return executionGrantModel(row)
	})
}

func (s SQLExecutionGrantStore) Reassign(ctx context.Context, change store.ExecutionGrantReassignment) (*store.ExecutionGrantReassignmentResult, error) {
	if !change.CurrentID.IsValid() || change.CurrentRevision < 1 || validateExecutionReservation(change.Replacement) != nil ||
		change.Replacement.ID == change.CurrentID {
		return nil, store.NewErrInvalidInput("execution_grant", "reassign", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "reassign execution grant", func(ctx context.Context, tx *sqlxTxWrapper) (*store.ExecutionGrantReassignmentResult, error) {
		if err := lockExecutableAttempt(ctx, tx, change.Replacement.AttemptID); err != nil {
			return nil, err
		}
		var previous executionGrantRow
		err := tx.Get(ctx, &previous, `SELECT `+executionGrantColumns+` FROM execution_grants
			WHERE id=$1 AND exam_attempt_id=$2 AND state IN ('reserved','ready') FOR UPDATE`,
			change.CurrentID.String(), change.Replacement.AttemptID.String())
		if err != nil {
			return nil, translateError("execution_grant", change.CurrentID.String(), err)
		}
		if previous.Revision != change.CurrentRevision {
			return nil, store.NewErrConflict("execution_grant", "revision", nil)
		}
		if err := tx.Get(ctx, &previous, `UPDATE execution_grants SET state='released',released_at=$1,updated_at=$1,
			lifecycle_pending=false,pending_sitting_state=NULL,pending_sitting_revision=NULL,revision=revision+1
			WHERE id=$2 AND revision=$3 RETURNING `+executionGrantColumns,
			model.TimeUTC(change.Replacement.At), change.CurrentID.String(), change.CurrentRevision); err != nil {
			return nil, fmt.Errorf("release replaced execution grant: %w", err)
		}
		current, err := insertExecutionGrant(ctx, tx, change.Replacement)
		if err != nil {
			return nil, err
		}
		previousModel, err := executionGrantModel(previous)
		if err != nil {
			return nil, err
		}
		currentModel, err := executionGrantModel(current)
		if err != nil {
			return nil, err
		}
		return &store.ExecutionGrantReassignmentResult{Previous: previousModel, Current: currentModel}, nil
	})
}

func (s SQLExecutionGrantStore) MarkReady(ctx context.Context, id model.ExecutionGrantID, revision int64, at time.Time) (*model.ExecutionGrant, error) {
	if !id.IsValid() || revision < 1 || at.IsZero() {
		return nil, store.NewErrInvalidInput("execution_grant", "mark_ready", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "mark execution grant ready", func(ctx context.Context, tx *sqlxTxWrapper) (*model.ExecutionGrant, error) {
		var attemptID string
		if err := tx.Get(ctx, &attemptID, `SELECT exam_attempt_id FROM execution_grants WHERE id=$1`, id.String()); err != nil {
			return nil, translateError("execution_grant", id.String(), err)
		}
		parsedAttemptID, err := model.ParseExamAttemptID(attemptID)
		if err != nil {
			return nil, invalidPersistedState("execution_grant", "exam_attempt_id", err)
		}
		if err = lockExecutableAttempt(ctx, tx, parsedAttemptID); err != nil {
			return nil, err
		}
		var row executionGrantRow
		err = tx.Get(ctx, &row, `UPDATE execution_grants SET state='ready',updated_at=$1,revision=revision+1
			WHERE id=$2 AND revision=$3 AND state='reserved' RETURNING `+executionGrantColumns,
			model.TimeUTC(at), id.String(), revision)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, store.NewErrConflict("execution_grant", "revision_or_state", err)
		}
		if err != nil {
			return nil, fmt.Errorf("mark execution grant ready: %w", err)
		}
		return executionGrantModel(row)
	})
}

func (s SQLExecutionGrantStore) PrepareSittingStateEffect(ctx context.Context, id model.ExecutionGrantID, revision int64, state model.ExamSittingState, sittingRevision int64, at time.Time) (*model.ExecutionGrant, error) {
	if !id.IsValid() || revision < 1 || (state != model.ExamSittingOpen && state != model.ExamSittingPaused) || sittingRevision < 1 || at.IsZero() {
		return nil, store.NewErrInvalidInput("execution_grant", "prepare_sitting_state", nil)
	}
	var row executionGrantRow
	err := s.GetMaster().Get(ctx, &row, `UPDATE execution_grants g
		SET lifecycle_pending=true,pending_sitting_state=$1,pending_sitting_revision=$2,updated_at=$3,revision=g.revision+1
		FROM exam_attempts a,exam_sittings s
		WHERE g.id=$4 AND g.revision=$5 AND g.state='ready' AND NOT g.lifecycle_pending
		AND a.id=g.exam_attempt_id AND s.id=a.exam_sitting_id AND s.exam_id=a.exam_id AND s.state=$1 AND s.revision=$2
		RETURNING `+prefixedExecutionGrantColumns("g"), string(state), sittingRevision, model.TimeUTC(at), id.String(), revision)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.NewErrConflict("execution_grant", "sitting_state", nil)
	}
	if err != nil {
		return nil, fmt.Errorf("prepare execution grant Sitting state effect: %w", err)
	}
	return executionGrantModel(row)
}

func (s SQLExecutionGrantStore) MarkSittingStateApplied(ctx context.Context, id model.ExecutionGrantID, revision int64, state model.ExamSittingState, sittingRevision int64, at time.Time) (*model.ExecutionGrant, error) {
	if !id.IsValid() || revision < 1 || (state != model.ExamSittingOpen && state != model.ExamSittingPaused) || sittingRevision < 1 || at.IsZero() {
		return nil, store.NewErrInvalidInput("execution_grant", "applied_sitting_state", nil)
	}
	var row executionGrantRow
	err := s.GetMaster().Get(ctx, &row, `UPDATE execution_grants g
		SET applied_sitting_state=$1,applied_sitting_revision=$2,lifecycle_pending=false,
		pending_sitting_state=NULL,pending_sitting_revision=NULL,updated_at=$3,revision=g.revision+1
		FROM exam_attempts a,exam_sittings s
		WHERE g.id=$4 AND g.revision=$5 AND g.state='ready' AND g.lifecycle_pending
		AND g.pending_sitting_state=$1 AND g.pending_sitting_revision=$2
		AND a.id=g.exam_attempt_id AND s.id=a.exam_sitting_id AND s.exam_id=a.exam_id AND s.state=$1 AND s.revision=$2
		RETURNING `+prefixedExecutionGrantColumns("g"), string(state), sittingRevision, model.TimeUTC(at), id.String(), revision)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.NewErrConflict("execution_grant", "sitting_state", nil)
	}
	if err != nil {
		return nil, fmt.Errorf("mark execution grant Sitting state applied: %w", err)
	}
	return executionGrantModel(row)
}

func (s SQLExecutionGrantStore) Release(ctx context.Context, attemptID model.ExamAttemptID, at time.Time) (*model.ExecutionGrant, error) {
	if !attemptID.IsValid() || at.IsZero() {
		return nil, store.NewErrInvalidInput("execution_grant", "release", nil)
	}
	var row executionGrantRow
	err := s.GetMaster().Get(ctx, &row, `UPDATE execution_grants SET state='released',released_at=$1,updated_at=$1,
		lifecycle_pending=false,pending_sitting_state=NULL,pending_sitting_revision=NULL,revision=revision+1
		WHERE exam_attempt_id=$2 AND state IN ('reserved','ready') RETURNING `+executionGrantColumns,
		model.TimeUTC(at), attemptID.String())
	if err != nil {
		return nil, translateError("execution_grant", attemptID.String(), err)
	}
	return executionGrantModel(row)
}

func (s SQLExecutionGrantStore) ReleaseGrant(ctx context.Context, id model.ExecutionGrantID, at time.Time) (*model.ExecutionGrant, error) {
	if !id.IsValid() || at.IsZero() {
		return nil, store.NewErrInvalidInput("execution_grant", "release_grant", nil)
	}
	var row executionGrantRow
	err := s.GetMaster().Get(ctx, &row, `UPDATE execution_grants SET state='released',released_at=$1,updated_at=$1,
		lifecycle_pending=false,pending_sitting_state=NULL,pending_sitting_revision=NULL,revision=revision+1
		WHERE id=$2 AND state IN ('reserved','ready') RETURNING `+executionGrantColumns, model.TimeUTC(at), id.String())
	if err != nil {
		return nil, translateError("execution_grant", id.String(), err)
	}
	return executionGrantModel(row)
}

func (s SQLExecutionGrantStore) MarkRevoked(ctx context.Context, id model.ExecutionGrantID, revision int64, at time.Time) (*model.ExecutionGrant, error) {
	return s.transition(ctx, id, revision, at, "released", `revoked_at=$1,updated_at=$1,revision=revision+1`, "mark execution grant revoked")
}

func (s SQLExecutionGrantStore) ListPendingRevocations(ctx context.Context, limit int) ([]*model.ExecutionGrant, error) {
	if limit < 1 || limit > 200 {
		return nil, store.NewErrInvalidInput("execution_grant", "limit", limit)
	}
	var rows []executionGrantRow
	if err := s.GetMaster().Select(ctx, &rows, `SELECT `+executionGrantColumns+` FROM execution_grants
		WHERE state='released' AND revoked_at IS NULL ORDER BY released_at,id LIMIT $1`, limit); err != nil {
		return nil, fmt.Errorf("list pending execution revocations: %w", err)
	}
	result := make([]*model.ExecutionGrant, 0, len(rows))
	for _, row := range rows {
		grant, err := executionGrantModel(row)
		if err != nil {
			return nil, err
		}
		result = append(result, grant)
	}
	return result, nil
}

type sqlExecutionLifecycleLease struct {
	conn    *sqlx.Conn
	grantID model.ExecutionGrantID
	once    sync.Once
	err     error
}

func (lease *sqlExecutionLifecycleLease) Validate(ctx context.Context) error {
	var alive int
	if err := lease.conn.GetContext(ctx, &alive, `SELECT 1`); err != nil {
		return fmt.Errorf("validate execution lifecycle advisory lock connection: %w", err)
	}
	if alive != 1 {
		return errors.New("execution lifecycle advisory lock connection is invalid")
	}
	return nil
}

func (lease *sqlExecutionLifecycleLease) Release(ctx context.Context) error {
	lease.once.Do(func() {
		var unlocked bool
		lease.err = lease.conn.GetContext(ctx, &unlocked, `SELECT pg_advisory_unlock(hashtextextended($1,0))`, lease.grantID.String())
		if lease.err == nil && !unlocked {
			lease.err = errors.New("execution lifecycle advisory lock was not held")
		}
		lease.err = errors.Join(lease.err, lease.conn.Close())
	})
	return lease.err
}

func (s SQLExecutionGrantStore) AcquireLifecycleLease(ctx context.Context, id model.ExecutionGrantID) (store.ExecutionLifecycleLease, error) {
	if !id.IsValid() {
		return nil, store.NewErrInvalidInput("execution_grant", "lifecycle_lease", nil)
	}
	conn, err := s.GetMaster().DB().Connx(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire execution lifecycle connection: %w", err)
	}
	if _, err = conn.ExecContext(ctx, `SELECT pg_advisory_lock(hashtextextended($1,0))`, id.String()); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("acquire execution lifecycle advisory lock: %w", err)
	}
	return &sqlExecutionLifecycleLease{conn: conn, grantID: id}, nil
}

func (s SQLExecutionGrantStore) CurrentForReconciliation(ctx context.Context, id model.ExecutionGrantID) (*store.ExecutionGrantConvergence, error) {
	if !id.IsValid() {
		return nil, store.NewErrInvalidInput("execution_grant", "reconciliation", nil)
	}
	var row executionGrantConvergenceRow
	if err := s.GetMaster().Get(ctx, &row, `SELECT `+prefixedExecutionGrantColumns("g")+`,
		a.state AS attempt_state,s.state AS sitting_state,s.revision AS sitting_revision,
		`+pendingCorrectionAcknowledgementSQL+` AS acknowledgement_required FROM execution_grants g
		JOIN exam_attempts a ON a.id=g.exam_attempt_id
		JOIN exam_sittings s ON s.id=a.exam_sitting_id AND s.exam_id=a.exam_id
		WHERE g.id=$1 AND g.state IN ('reserved','ready')`, id.String()); err != nil {
		return nil, translateError("execution_grant", id.String(), err)
	}
	return executionGrantConvergenceModel(row)
}

func (s SQLExecutionGrantStore) ListCurrentForReconciliation(ctx context.Context, after model.ExecutionGrantID, limit int) ([]store.ExecutionGrantConvergence, error) {
	if limit < 1 || limit > 200 || (!after.IsZero() && !after.IsValid()) {
		return nil, store.NewErrInvalidInput("execution_grant", "reconciliation_page", nil)
	}
	var rows []executionGrantConvergenceRow
	if err := s.GetMaster().Select(ctx, &rows, `SELECT `+prefixedExecutionGrantColumns("g")+`,
		a.state AS attempt_state,s.state AS sitting_state,s.revision AS sitting_revision,
		`+pendingCorrectionAcknowledgementSQL+` AS acknowledgement_required FROM execution_grants g
		JOIN exam_attempts a ON a.id=g.exam_attempt_id
		JOIN exam_sittings s ON s.id=a.exam_sitting_id AND s.exam_id=a.exam_id
		WHERE g.state IN ('reserved','ready') AND g.id>$1 ORDER BY g.id LIMIT $2`, after.String(), limit); err != nil {
		return nil, fmt.Errorf("list current execution grants for reconciliation: %w", err)
	}
	result := make([]store.ExecutionGrantConvergence, 0, len(rows))
	for _, row := range rows {
		value, err := executionGrantConvergenceModel(row)
		if err != nil {
			return nil, err
		}
		result = append(result, *value)
	}
	return result, nil
}

func executionGrantConvergenceModel(row executionGrantConvergenceRow) (*store.ExecutionGrantConvergence, error) {
	grant, err := executionGrantModel(row.executionGrantRow)
	if err != nil {
		return nil, err
	}
	attemptState := model.ExamAttemptState(row.AttemptState)
	if attemptState != model.ExamAttemptActive && attemptState != model.ExamAttemptSuspended && attemptState != model.ExamAttemptSubmitted {
		return nil, fmt.Errorf("execution grant persisted invalid Attempt state")
	}
	sittingState := model.ExamSittingState(row.SittingState)
	if !sittingState.IsValid() || row.SittingRevision < 1 {
		return nil, fmt.Errorf("execution grant persisted invalid Sitting lifecycle")
	}
	return &store.ExecutionGrantConvergence{Grant: grant, AttemptState: attemptState,
		SittingState: sittingState, SittingRevision: row.SittingRevision,
		AcknowledgementRequired: row.AcknowledgementRequired}, nil
}

func (s SQLExecutionGrantStore) ListCurrentForSitting(ctx context.Context, sittingID model.ExamSittingID, after model.ExecutionGrantID, limit int) ([]*model.ExecutionGrant, error) {
	if !sittingID.IsValid() || limit < 1 || limit > 200 || (!after.IsZero() && !after.IsValid()) {
		return nil, store.NewErrInvalidInput("execution_grant", "sitting_page", nil)
	}
	var rows []executionGrantRow
	if err := s.GetMaster().Select(ctx, &rows, `SELECT `+prefixedExecutionGrantColumns("g")+` FROM execution_grants g
		JOIN exam_attempts a ON a.id=g.exam_attempt_id WHERE a.exam_sitting_id=$1
		AND g.state IN ('reserved','ready') AND g.id>$2 ORDER BY g.id LIMIT $3`, sittingID.String(), after.String(), limit); err != nil {
		return nil, fmt.Errorf("list current execution grants for Sitting: %w", err)
	}
	result := make([]*model.ExecutionGrant, 0, len(rows))
	for _, row := range rows {
		grant, err := executionGrantModel(row)
		if err != nil {
			return nil, err
		}
		result = append(result, grant)
	}
	return result, nil
}

func prefixedExecutionGrantColumns(prefix string) string {
	return prefix + ".id," + prefix + ".exam_attempt_id," + prefix + ".host_id," + prefix + ".image," + prefix + ".network," +
		prefix + ".state," + prefix + ".applied_sitting_state," + prefix + ".applied_sitting_revision," +
		prefix + ".lifecycle_pending," + prefix + ".pending_sitting_state," + prefix + ".pending_sitting_revision," +
		prefix + ".created_at," + prefix + ".updated_at," + prefix + ".released_at," + prefix + ".revoked_at," + prefix + ".revision"
}

type executionWorkspaceNodeRow struct {
	Kind            string         `db:"kind"`
	Path            string         `db:"path"`
	ContentVersion  sql.NullString `db:"content_version"`
	SizeBytes       sql.NullInt64  `db:"size_bytes"`
	SHA256          sql.NullString `db:"sha256"`
	StorageOrigin   sql.NullString `db:"storage_origin"`
	StarterObjectID sql.NullString `db:"starter_object_id"`
	AttemptObjectID sql.NullString `db:"attempt_object_id"`
}

func (s SQLExecutionGrantStore) WorkspaceSnapshot(ctx context.Context, attemptID model.ExamAttemptID) (*store.ExecutionWorkspaceSnapshot, error) {
	if !attemptID.IsValid() {
		return nil, store.NewErrInvalidInput("execution_workspace", "exam_attempt_id", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "read execution workspace snapshot", func(ctx context.Context, tx *sqlxTxWrapper) (*store.ExecutionWorkspaceSnapshot, error) {
		var cursor int64
		err := tx.Get(ctx, &cursor, `SELECT w.cursor FROM exam_attempts a
			JOIN exam_sittings s ON s.id=a.exam_sitting_id AND s.exam_id=a.exam_id
			JOIN exam_attempt_workspaces w ON w.exam_attempt_id=a.id
			WHERE a.id=$1 AND a.state='active' AND s.state='open' FOR SHARE OF a,s,w`, attemptID.String())
		if err != nil {
			return nil, translateError("execution_workspace", attemptID.String(), err)
		}
		var rows []executionWorkspaceNodeRow
		if err := tx.Select(ctx, &rows, `SELECT e.kind,e.path,o.content_version,o.size_bytes,o.sha256,o.storage_origin,
			o.starter_object_id,o.id AS attempt_object_id FROM exam_attempt_workspace_entries e
			JOIN exam_attempt_workspaces w ON w.id=e.workspace_id
			LEFT JOIN exam_attempt_workspace_objects o ON o.workspace_id=e.workspace_id AND o.id=e.current_object_id
			WHERE w.exam_attempt_id=$1 ORDER BY e.path LIMIT $2`, attemptID.String(), model.AttemptWorkspaceMaximumEntries+1); err != nil {
			return nil, fmt.Errorf("read execution workspace entries: %w", err)
		}
		if len(rows) > model.AttemptWorkspaceMaximumEntries {
			return nil, store.NewErrConflict("execution_workspace", "maximum_entries", nil)
		}
		result := &store.ExecutionWorkspaceSnapshot{Cursor: cursor, Nodes: make([]store.ExecutionWorkspaceNode, 0, len(rows))}
		for _, row := range rows {
			node, err := executionWorkspaceNode(row)
			if err != nil {
				return nil, err
			}
			result.Nodes = append(result.Nodes, node)
		}
		return result, nil
	})
}

func executionWorkspaceNode(row executionWorkspaceNodeRow) (store.ExecutionWorkspaceNode, error) {
	node := store.ExecutionWorkspaceNode{Kind: model.StarterWorkspaceEntryKind(row.Kind), Path: row.Path}
	if node.Kind == model.StarterWorkspaceEntryDirectory {
		if row.ContentVersion.Valid || row.SizeBytes.Valid || row.SHA256.Valid || row.StorageOrigin.Valid ||
			row.StarterObjectID.Valid || row.AttemptObjectID.Valid {
			return node, fmt.Errorf("execution workspace persisted directory has content")
		}
		return node, nil
	}
	if node.Kind != model.StarterWorkspaceEntryFile || !row.ContentVersion.Valid || !row.SizeBytes.Valid ||
		!row.SHA256.Valid || !row.StorageOrigin.Valid || !row.AttemptObjectID.Valid {
		return node, fmt.Errorf("execution workspace persisted file is incomplete")
	}
	version, err := model.ParseWorkspaceContentVersion(row.ContentVersion.String)
	if err != nil {
		return node, fmt.Errorf("execution workspace persisted content version: %w", err)
	}
	attemptObjectID, err := model.ParseAttemptWorkspaceObjectID(row.AttemptObjectID.String)
	if err != nil {
		return node, fmt.Errorf("execution workspace persisted object ID: %w", err)
	}
	node.ContentVersion, node.SizeBytes, node.SHA256 = version, row.SizeBytes.Int64, row.SHA256.String
	node.StorageOrigin, node.AttemptObjectID = model.AttemptWorkspaceObjectStorage(row.StorageOrigin.String), attemptObjectID
	if row.StarterObjectID.Valid {
		starterObjectID, err := model.ParseStarterWorkspaceObjectID(row.StarterObjectID.String)
		if err != nil {
			return node, fmt.Errorf("execution workspace persisted starter object ID: %w", err)
		}
		node.StarterObjectID = starterObjectID
	}
	if (node.StorageOrigin == model.AttemptWorkspaceStorageStarter) != node.StarterObjectID.IsValid() ||
		(node.StorageOrigin == model.AttemptWorkspaceStorageAttempt) == node.StarterObjectID.IsValid() {
		return node, fmt.Errorf("execution workspace persisted object origin is inconsistent")
	}
	return node, nil
}

func (s SQLExecutionGrantStore) transition(ctx context.Context, id model.ExecutionGrantID, revision int64, at time.Time, state, set, operation string) (*model.ExecutionGrant, error) {
	if !id.IsValid() || revision < 1 || at.IsZero() {
		return nil, store.NewErrInvalidInput("execution_grant", operation, nil)
	}
	var row executionGrantRow
	err := s.GetMaster().Get(ctx, &row, `UPDATE execution_grants SET `+set+`
		WHERE id=$2 AND revision=$3 AND state=$4 RETURNING `+executionGrantColumns,
		model.TimeUTC(at), id.String(), revision, state)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.NewErrConflict("execution_grant", "revision_or_state", err)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", operation, err)
	}
	return executionGrantModel(row)
}

func validateExecutionReservation(value store.ExecutionGrantReservation) error {
	grant := &model.ExecutionGrant{ID: value.ID, AttemptID: value.AttemptID, HostID: value.HostID,
		Image: value.Image, Network: value.Network, State: model.ExecutionGrantReserved,
		AppliedSittingState:    model.ExamSittingOpen,
		AppliedSittingRevision: 1,
		CreatedAt:              model.TimeUTC(value.At), UpdatedAt: model.TimeUTC(value.At), Revision: 1}
	if err := grant.Validate(); err != nil {
		return store.NewErrInvalidInput("execution_grant", "reservation", nil).Wrap(err)
	}
	return nil
}

func lockExecutableAttempt(ctx context.Context, tx *sqlxTxWrapper, attemptID model.ExamAttemptID) error {
	var row struct {
		Allowed             bool   `db:"allowed"`
		SittingID           string `db:"sitting_id"`
		AdmissionRevisionID string `db:"admission_revision_id"`
		CurrentRevisionID   string `db:"current_revision_id"`
	}
	err := tx.Get(ctx, &row, `SELECT a.state='active' AND s.state='open' AS allowed,s.id AS sitting_id,
		a.admission_revision_id,s.exam_revision_id AS current_revision_id FROM exam_attempts a
		JOIN exam_sittings s ON s.id=a.exam_sitting_id AND s.exam_id=a.exam_id WHERE a.id=$1 FOR UPDATE OF a,s`, attemptID.String())
	if err != nil {
		return translateError("exam_attempt", attemptID.String(), err)
	}
	if !row.Allowed {
		return store.NewErrConflict("execution_grant", "attempt_not_executable", nil)
	}
	pending, err := hasPendingCandidateCorrectionAcknowledgement(ctx, tx, attemptID.String(), row.SittingID,
		row.AdmissionRevisionID, row.CurrentRevisionID)
	if err != nil {
		return err
	}
	if pending {
		return store.NewErrConflict("execution_grant", "exam_correction_acknowledgement_required", nil)
	}
	return nil
}

const pendingCorrectionAcknowledgementSQL = `EXISTS (
	SELECT 1 FROM exam_sitting_live_corrections live
	JOIN exam_revisions correction ON correction.id=live.correction_revision_id AND correction.exam_id=live.exam_id
	JOIN exam_revisions admission ON admission.id=a.admission_revision_id AND admission.exam_id=a.exam_id
	JOIN exam_revisions current_revision ON current_revision.id=s.exam_revision_id AND current_revision.exam_id=a.exam_id
	WHERE live.exam_sitting_id=s.id AND correction.number>admission.number
	AND correction.number<=current_revision.number AND correction.publication_kind='live_correction'
	AND correction.candidate_correction_acknowledgement_required=true
	AND NOT EXISTS (SELECT 1 FROM exam_attempt_correction_acknowledgements acknowledgement
		WHERE acknowledgement.exam_attempt_id=a.id AND acknowledgement.correction_revision_id=correction.id))`

func insertExecutionGrant(ctx context.Context, tx *sqlxTxWrapper, value store.ExecutionGrantReservation) (executionGrantRow, error) {
	var row executionGrantRow
	err := tx.Get(ctx, &row, `INSERT INTO execution_grants
		(id,exam_attempt_id,host_id,image,network,state,applied_sitting_state,applied_sitting_revision,created_at,updated_at,revision)
		SELECT $1,$2,$3,$4,$5,'reserved','open',s.revision,$6,$6,1 FROM exam_attempts a
		JOIN exam_sittings s ON s.id=a.exam_sitting_id AND s.exam_id=a.exam_id WHERE a.id=$2::varchar RETURNING `+executionGrantColumns,
		value.ID.String(), value.AttemptID.String(), value.HostID, value.Image, string(value.Network), model.TimeUTC(value.At))
	if err != nil {
		return row, fmt.Errorf("insert execution grant: %w", translateError("execution_grant", value.ID.String(), err))
	}
	return row, nil
}

func executionGrantModel(row executionGrantRow) (*model.ExecutionGrant, error) {
	id, err := model.ParseExecutionGrantID(row.ID)
	if err != nil {
		return nil, fmt.Errorf("execution grant persisted ID: %w", err)
	}
	attemptID, err := model.ParseExamAttemptID(row.AttemptID)
	if err != nil {
		return nil, fmt.Errorf("execution grant persisted Attempt ID: %w", err)
	}
	grant := &model.ExecutionGrant{ID: id, AttemptID: attemptID, HostID: row.HostID, Image: row.Image,
		Network: model.ExecutionNetwork(row.Network), State: model.ExecutionGrantState(row.State),
		AppliedSittingState:    model.ExamSittingState(row.AppliedSittingState),
		AppliedSittingRevision: row.AppliedSittingRevision,
		CreatedAt:              model.TimeUTC(row.CreatedAt), UpdatedAt: model.TimeUTC(row.UpdatedAt),
		ReleasedAt: OptionalTimeFromNullTime(row.ReleasedAt), RevokedAt: OptionalTimeFromNullTime(row.RevokedAt), Revision: row.Revision}
	grant.LifecyclePending = row.LifecyclePending
	if row.PendingSittingState.Valid {
		grant.PendingSittingState = model.ExamSittingState(row.PendingSittingState.String)
	}
	if row.PendingSittingRevision.Valid {
		grant.PendingSittingRevision = row.PendingSittingRevision.Int64
	}
	if err := grant.Validate(); err != nil {
		return nil, fmt.Errorf("execution grant persisted state: %w", err)
	}
	return grant, nil
}

var _ store.ExecutionGrantStore = (*SQLExecutionGrantStore)(nil)
