// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type executionProjectionEffectRow struct {
	GrantID         string `db:"execution_grant_id"`
	MutationID      string `db:"mutation_id"`
	Epoch           string `db:"environment_epoch"`
	ControlRevision int64  `db:"control_revision"`
	FromCursor      int64  `db:"from_workspace_cursor"`
	ThroughCursor   int64  `db:"through_workspace_cursor"`
	HostCursor      int64  `db:"expected_host_cursor"`
	Initial         bool   `db:"initial"`
	Digest          []byte `db:"request_digest"`
	Body            []byte `db:"request_canonical"`
	Completed       bool   `db:"completed"`
	Rejected        bool   `db:"rejected"`
}

const executionProjectionEffectColumns = `execution_grant_id,mutation_id,environment_epoch,control_revision,from_workspace_cursor,through_workspace_cursor,expected_host_cursor,initial,request_digest,request_canonical,completed,rejected`

func projectionEffect(row executionProjectionEffectRow) (*store.ExecutionProjectionEffect, error) {
	id, err := model.ParseExecutionGrantID(row.GrantID)
	if err != nil {
		return nil, invalidPersistedState("execution_projection", "grant_id", err)
	}
	fence := model.ExecutionFence{GrantID: id, EnvironmentEpoch: row.Epoch, ControlRevision: row.ControlRevision}
	if fence.Validate() != nil || !model.IsValidId(row.MutationID) || row.FromCursor < 0 || row.ThroughCursor < row.FromCursor || row.ThroughCursor > 1<<53-1 || row.HostCursor < 0 || row.HostCursor > 1<<53-1 || len(row.Digest) != sha256.Size {
		return nil, invalidPersistedState("execution_projection", "metadata", nil)
	}
	if row.Rejected {
		return &store.ExecutionProjectionEffect{Rejected: true}, nil
	}
	if row.Completed {
		receipt := &store.ExecutionProjectionReceipt{Fence: model.ExecutionFence{GrantID: id, EnvironmentEpoch: row.Epoch, ControlRevision: row.ControlRevision}, MutationID: row.MutationID, AppliedWorkspaceCursor: row.ThroughCursor, HostCursor: row.HostCursor}
		return &store.ExecutionProjectionEffect{Receipt: receipt}, nil
	}
	var request store.ExecutionProjectionRequest
	digest := sha256.Sum256(row.Body)
	if json.Unmarshal(row.Body, &request) != nil || request.Validate() != nil || !bytes.Equal(digest[:], row.Digest) || request.Fence.GrantID != id || request.MutationID != row.MutationID ||
		request.Fence.EnvironmentEpoch != row.Epoch || request.Fence.ControlRevision != row.ControlRevision || request.FromWorkspaceCursor != row.FromCursor ||
		request.ThroughWorkspaceCursor != row.ThroughCursor || request.ExpectedHostCursor != row.HostCursor || request.Initial != row.Initial {
		return nil, invalidPersistedState("execution_projection", "request", nil)
	}
	return &store.ExecutionProjectionEffect{Request: &request}, nil
}

func (s SQLExecutionGrantStore) PendingProjection(ctx context.Context, id model.ExecutionGrantID) (*store.ExecutionProjectionRequest, error) {
	if !id.IsValid() {
		return nil, store.NewErrInvalidInput("execution_projection", "grant_id", nil)
	}
	var row executionProjectionEffectRow
	err := s.GetMaster().Get(ctx, &row, `SELECT `+executionProjectionEffectColumns+` FROM execution_projection_effects
 WHERE execution_grant_id=$1 AND NOT completed`, id.String())
	if err != nil {
		return nil, translateError("execution_projection", id.String(), err)
	}
	effect, err := projectionEffect(row)
	if err != nil {
		return nil, err
	}
	return effect.Request, nil
}

func (s SQLExecutionGrantStore) PrepareProjection(ctx context.Context, request store.ExecutionProjectionRequest) (*store.ExecutionProjectionEffect, error) {
	if request.Validate() != nil {
		return nil, store.NewErrInvalidInput("execution_projection", "request", nil)
	}
	body, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(body)
	return runSQLTransaction(ctx, s.GetMaster().Begin, "prepare execution projection", func(ctx context.Context, tx *sqlxTxWrapper) (*store.ExecutionProjectionEffect, error) {
		grant, authority, err := lockExecutionControl(ctx, tx, request.Fence.GrantID)
		if err != nil {
			return nil, err
		}
		if grant.Fence() != request.Fence || grant.State == model.ExecutionGrantReleased {
			return nil, store.NewErrConflict("execution_projection", "fence", nil)
		}
		var existing executionProjectionEffectRow
		err = tx.Get(ctx, &existing, `SELECT `+executionProjectionEffectColumns+` FROM execution_projection_effects WHERE execution_grant_id=$1 AND mutation_id=$2`, grant.ID.String(), request.MutationID)
		if err == nil {
			if !bytes.Equal(existing.Digest, digest[:]) {
				return nil, store.NewErrConflict("execution_projection", "mutation_id", nil)
			}
			if existing.Rejected {
				return nil, store.NewErrConflict("execution_projection", "rejected_mutation", nil)
			}
			if !existing.Completed && (grant.ControlAcknowledgedRevision != grant.ControlRevision || grant.DesiredControlState != model.ExecutionControlRunning || authority.desired(grant) != model.ExecutionControlRunning || grant.ControlAuthorityDigest != authority.digest()) {
				return nil, store.NewErrConflict("execution_projection", "current_state", nil)
			}
			return projectionEffect(existing)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if grant.ControlAcknowledgedRevision != grant.ControlRevision || grant.DesiredControlState != model.ExecutionControlRunning || authority.desired(grant) != model.ExecutionControlRunning || grant.ControlAuthorityDigest != authority.digest() ||
			request.ExpectedHostCursor != grant.ProcessedHostSequence || grant.LifecyclePending || grant.WorkspacePending || grant.AppliedWorkspaceCursor != request.FromWorkspaceCursor ||
			(request.Initial && grant.State != model.ExecutionGrantReserved) || (!request.Initial && grant.State != model.ExecutionGrantReady) {
			return nil, store.NewErrConflict("execution_projection", "current_state", nil)
		}
		var count int
		if err := tx.Get(ctx, &count, `SELECT count(*) FROM execution_projection_effects WHERE execution_grant_id=$1`, grant.ID.String()); err != nil {
			return nil, err
		}
		if count >= 65536 {
			return nil, store.NewErrConflict("execution_projection", "receipt_capacity", nil)
		}
		if err := validateProjectionAuthority(ctx, tx, grant, request); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO execution_projection_effects (`+executionProjectionEffectColumns+`) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,false,false)`, grant.ID.String(), request.MutationID, request.Fence.EnvironmentEpoch, request.Fence.ControlRevision, request.FromWorkspaceCursor, request.ThroughWorkspaceCursor, request.ExpectedHostCursor, request.Initial, digest[:], body); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `UPDATE execution_grants SET workspace_pending=true,pending_workspace_cursor=$2,updated_at=GREATEST(updated_at,statement_timestamp()),revision=revision+1 WHERE id=$1`, grant.ID.String(), request.ThroughWorkspaceCursor); err != nil {
			return nil, err
		}
		// Decode the retained bytes so a caller cannot mutate the stored request by
		// retaining aliases to the arrays it supplied.
		return projectionEffect(executionProjectionEffectRow{GrantID: grant.ID.String(), MutationID: request.MutationID, Epoch: request.Fence.EnvironmentEpoch, ControlRevision: request.Fence.ControlRevision, FromCursor: request.FromWorkspaceCursor, ThroughCursor: request.ThroughWorkspaceCursor, HostCursor: request.ExpectedHostCursor, Initial: request.Initial, Digest: digest[:], Body: body})
	})
}

func validateProjectionAuthority(ctx context.Context, tx *sqlxTxWrapper, grant *model.ExecutionGrant, request store.ExecutionProjectionRequest) error {
	conflict := func() error { return store.NewErrConflict("execution_projection", "journal", nil) }
	if request.Initial {
		var cursor int64
		if err := tx.Get(ctx, &cursor, `SELECT cursor FROM exam_attempt_workspaces WHERE exam_attempt_id=$1 FOR SHARE`, grant.AttemptID.String()); err != nil {
			return err
		}
		if cursor != request.ThroughWorkspaceCursor {
			return conflict()
		}
		var rows []executionWorkspaceNodeRow
		if err := tx.Select(ctx, &rows, `SELECT e.id AS entry_id,e.kind,e.path,o.content_version,o.size_bytes,o.sha256,o.storage_origin,o.starter_object_id,o.id AS attempt_object_id
  FROM exam_attempt_workspace_entries e JOIN exam_attempt_workspaces w ON w.id=e.workspace_id
  LEFT JOIN exam_attempt_workspace_objects o ON o.id=e.current_object_id AND o.workspace_id=e.workspace_id
  WHERE w.exam_attempt_id=$1 ORDER BY e.path`, grant.AttemptID.String()); err != nil {
			return err
		}
		if len(rows) != len(request.Entries) {
			return conflict()
		}
		for i, row := range rows {
			node, err := executionWorkspaceNode(row)
			if err != nil {
				return err
			}
			entry := request.Entries[i]
			if entry.EntryID != node.EntryID || entry.Kind != node.Kind || entry.Path != node.Path || entry.ContentVersion != node.ContentVersion {
				return conflict()
			}
			if node.Kind == model.StarterWorkspaceEntryFile && (entry.Content == nil || entry.Content.Size != node.SizeBytes || entry.Content.SHA256 != node.SHA256) {
				return conflict()
			}
		}
		return nil
	}
	page, err := listExecutionWorkspaceChanges(ctx, tx, grant, len(request.Mutations))
	if err != nil {
		return err
	}
	if page.RefreshRequired || len(page.Changes) != len(request.Mutations) {
		return conflict()
	}
	for i, want := range page.Changes {
		got := request.Mutations[i]
		if !got.Change.ChangedAt.Equal(want.Change.ChangedAt) {
			return conflict()
		}
		got.Change.ChangedAt = want.Change.ChangedAt
		if got.Change != want.Change || got.ExpectedContentVersion != want.ExpectedContentVersion {
			return conflict()
		}
		if want.Content != nil && (got.Content == nil || got.Content.Size != want.Content.SizeBytes || got.Content.SHA256 != want.Content.SHA256) {
			return conflict()
		}
	}
	return nil
}

func (s SQLExecutionGrantStore) CompleteProjection(ctx context.Context, receipt store.ExecutionProjectionReceipt) (*model.ExecutionGrant, error) {
	if receipt.Fence.Validate() != nil || !model.IsValidId(receipt.MutationID) || receipt.AppliedWorkspaceCursor < 0 || receipt.HostCursor < 0 || receipt.AppliedWorkspaceCursor > 1<<53-1 || receipt.HostCursor > 1<<53-1 {
		return nil, store.NewErrInvalidInput("execution_projection", "receipt", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "complete execution projection", func(ctx context.Context, tx *sqlxTxWrapper) (*model.ExecutionGrant, error) {
		grant, _, err := lockExecutionControl(ctx, tx, receipt.Fence.GrantID)
		if err != nil {
			return nil, err
		}
		if grant.Fence() != receipt.Fence || grant.State == model.ExecutionGrantReleased {
			return nil, store.NewErrConflict("execution_projection", "fence", nil)
		}
		var row executionProjectionEffectRow
		if err := tx.Get(ctx, &row, `SELECT `+executionProjectionEffectColumns+` FROM execution_projection_effects WHERE execution_grant_id=$1 AND mutation_id=$2`, grant.ID.String(), receipt.MutationID); err != nil {
			return nil, translateError("execution_projection", receipt.MutationID, err)
		}
		if row.Epoch != receipt.Fence.EnvironmentEpoch || row.ControlRevision != receipt.Fence.ControlRevision || row.ThroughCursor != receipt.AppliedWorkspaceCursor || row.HostCursor != receipt.HostCursor {
			return nil, store.NewErrConflict("execution_projection", "receipt", nil)
		}
		if row.Rejected {
			return nil, store.NewErrConflict("execution_projection", "rejected_mutation", nil)
		}
		if row.Completed {
			return grant, nil
		}
		if !grant.WorkspacePending || grant.PendingWorkspaceCursor != row.ThroughCursor || grant.AppliedWorkspaceCursor != row.FromCursor {
			return nil, store.NewErrConflict("execution_projection", "current_state", nil)
		}
		var updated executionGrantRow
		if err := tx.Get(ctx, &updated, `UPDATE execution_grants SET state='ready',workspace_pending=false,pending_workspace_cursor=0,applied_workspace_cursor=$2,updated_at=GREATEST(updated_at,statement_timestamp()),revision=revision+1 WHERE id=$1 RETURNING `+executionGrantColumns, grant.ID.String(), row.ThroughCursor); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `UPDATE execution_projection_effects SET completed=true,request_canonical=NULL WHERE execution_grant_id=$1 AND mutation_id=$2`, grant.ID.String(), receipt.MutationID); err != nil {
			return nil, err
		}
		return executionGrantModel(updated)
	})
}

func (s SQLExecutionGrantStore) RejectProjection(ctx context.Context, fence model.ExecutionFence, mutationID string) (*model.ExecutionGrant, error) {
	if fence.Validate() != nil || !model.IsValidId(mutationID) {
		return nil, store.NewErrInvalidInput("execution_projection", "refusal", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "record execution projection refusal", func(ctx context.Context, tx *sqlxTxWrapper) (*model.ExecutionGrant, error) {
		grant, _, err := lockExecutionControl(ctx, tx, fence.GrantID)
		if err != nil {
			return nil, err
		}
		if grant.Fence() != fence || grant.State == model.ExecutionGrantReleased {
			return nil, store.NewErrConflict("execution_projection", "fence", nil)
		}
		var row executionProjectionEffectRow
		if err := tx.Get(ctx, &row, `SELECT `+executionProjectionEffectColumns+` FROM execution_projection_effects WHERE execution_grant_id=$1 AND mutation_id=$2`, grant.ID.String(), mutationID); err != nil {
			return nil, translateError("execution_projection", mutationID, err)
		}
		if row.Epoch != fence.EnvironmentEpoch || row.ControlRevision != fence.ControlRevision {
			return nil, store.NewErrConflict("execution_projection", "fence", nil)
		}
		if row.Rejected {
			return grant, nil
		}
		if row.Completed || !grant.WorkspacePending || grant.PendingWorkspaceCursor != row.ThroughCursor || grant.AppliedWorkspaceCursor != row.FromCursor {
			return nil, store.NewErrConflict("execution_projection", "current_state", nil)
		}
		if _, err := tx.Exec(ctx, `UPDATE execution_projection_effects SET completed=true,rejected=true,request_canonical=NULL WHERE execution_grant_id=$1 AND mutation_id=$2`, grant.ID.String(), mutationID); err != nil {
			return nil, err
		}
		var updated executionGrantRow
		if err := tx.Get(ctx, &updated, `UPDATE execution_grants SET workspace_pending=false,pending_workspace_cursor=0,updated_at=GREATEST(updated_at,statement_timestamp()),revision=revision+1 WHERE id=$1 RETURNING `+executionGrantColumns, grant.ID.String()); err != nil {
			return nil, err
		}
		return executionGrantModel(updated)
	})
}
