// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"context"
	"fmt"
	"strings"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// MarkObjectReclaimable schedules a staged Attempt-owned Workspace object for
// cleanup after the reclaim safety window when no entry, Submission manifest
// or recorded mutation outcome still references it.
func (s *SQLExamAttemptWorkspaceStore) MarkObjectReclaimable(ctx context.Context, objectID model.AttemptWorkspaceObjectID) error {
	if !objectID.IsValid() {
		return store.NewErrInvalidInput("attempt_workspace_object", "reclaim", nil)
	}
	result, err := s.GetMaster().Exec(ctx, `UPDATE exam_attempt_workspace_objects objects
		SET state='reclaimable',updated_at=GREATEST(objects.updated_at,statement_timestamp()),
			reclaim_after=statement_timestamp()+(? * INTERVAL '1 millisecond')
		WHERE objects.id=? AND objects.storage_origin='attempt' AND objects.state='staged'
			AND NOT EXISTS (SELECT 1 FROM exam_attempt_workspace_entries entries WHERE entries.current_object_id=objects.id)
			AND NOT EXISTS (SELECT 1 FROM exam_submission_manifest_entries submitted WHERE submitted.attempt_object_id=objects.id)
			AND NOT EXISTS (SELECT 1 FROM command_outcomes outcomes
				WHERE outcomes.operation=? AND jsonb_exists(outcomes.outcome->'o',objects.id))
			AND NOT EXISTS (SELECT 1 FROM command_outcomes outcomes
				WHERE outcomes.original_audit_event_id=objects.retired_by_audit_event_id)`,
		model.AttemptWorkspaceReclaimSafetyWindow.Milliseconds(), objectID.String(), store.ExamAttemptWorkspaceMutationOperation)
	if err != nil {
		return fmt.Errorf("mark Attempt Workspace object reclaimable: %w", err)
	}
	if affected, rowsErr := result.RowsAffected(); rowsErr != nil {
		return fmt.Errorf("inspect Attempt Workspace object reclamation: %w", rowsErr)
	} else if affected != 1 {
		return store.NewErrConflict("attempt_workspace_object", "attempt_workspace_object_state", nil)
	}
	return nil
}

// ClaimObjectsForCleanup claims up to the input limit of due Attempt Workspace objects for physical cleanup under the input claim token.
func (s *SQLExamAttemptWorkspaceStore) ClaimObjectsForCleanup(ctx context.Context, input *store.AttemptWorkspaceCleanupClaim) ([]model.AttemptWorkspaceObject, error) {
	limit := input.Limit
	claimToken := input.ClaimToken
	if limit < 1 || limit > 200 || strings.TrimSpace(claimToken) == "" || strings.TrimSpace(claimToken) != claimToken || len(claimToken) > model.AttemptWorkspaceClaimTokenMaxBytes {
		return nil, store.NewErrInvalidInput("attempt_workspace_object", "cleanup_claim", nil)
	}
	var rows []attemptWorkspaceObjectRow
	// The lifecycle CHECK requires claimed_at = updated_at. A release committed
	// after this statement began can already carry a later updated_at, so the
	// claim instant follows the same GREATEST as the modification time.
	err := s.GetMaster().Select(ctx, &rows, `WITH candidates AS (
		SELECT objects.id FROM exam_attempt_workspace_objects objects
		WHERE objects.storage_origin='attempt'
			AND ((objects.state='staged' AND objects.expires_at+(? * INTERVAL '1 millisecond')<=statement_timestamp())
			 OR (objects.state='reclaimable' AND objects.reclaim_after<=statement_timestamp())
			 OR (objects.state='claimed' AND objects.claimed_at+(? * INTERVAL '1 millisecond')<=statement_timestamp()))
			AND NOT EXISTS (SELECT 1 FROM exam_attempt_workspace_entries entries WHERE entries.current_object_id=objects.id)
			AND NOT EXISTS (SELECT 1 FROM exam_submission_manifest_entries submitted WHERE submitted.attempt_object_id=objects.id)
			AND NOT EXISTS (SELECT 1 FROM command_outcomes outcomes
				WHERE outcomes.operation=? AND jsonb_exists(outcomes.outcome->'o',objects.id))
			AND NOT EXISTS (SELECT 1 FROM command_outcomes outcomes
				WHERE outcomes.original_audit_event_id=objects.retired_by_audit_event_id)
		ORDER BY COALESCE(objects.reclaim_after,objects.expires_at,objects.claimed_at),objects.id
		FOR UPDATE SKIP LOCKED LIMIT ?
	) UPDATE exam_attempt_workspace_objects objects
		SET state='claimed',updated_at=GREATEST(objects.updated_at,statement_timestamp()),
			reclaim_after=COALESCE(objects.reclaim_after,statement_timestamp()),claim_token=?,
			claimed_at=GREATEST(objects.updated_at,statement_timestamp())
		FROM candidates WHERE objects.id=candidates.id
		RETURNING objects.id,objects.workspace_id,objects.storage_origin,objects.starter_object_id,objects.state,
			objects.content_version,objects.media_type,objects.size_bytes,objects.sha256,objects.created_at,objects.updated_at,
			objects.expires_at,objects.reclaim_after,objects.claim_token,objects.claimed_at`,
		model.AttemptWorkspaceReclaimSafetyWindow.Milliseconds(), model.AttemptWorkspaceCleanupClaimLease.Milliseconds(),
		store.ExamAttemptWorkspaceMutationOperation, limit, claimToken)
	if err != nil {
		return nil, fmt.Errorf("claim Attempt Workspace objects for cleanup: %w", err)
	}
	objects := make([]model.AttemptWorkspaceObject, 0, len(rows))
	for _, row := range rows {
		object, modelErr := row.model()
		if modelErr != nil {
			return nil, modelErr
		}
		objects = append(objects, *object)
	}
	return objects, nil
}

// CompleteObjectCleanup records that the claimed Attempt Workspace object was removed from storage; repeating it with the same claim token is a no-op.
func (s *SQLExamAttemptWorkspaceStore) CompleteObjectCleanup(ctx context.Context, input *store.AttemptWorkspaceCleanupCompletion) error {
	objectID := input.ObjectID
	claimToken := input.ClaimToken
	if !validAttemptWorkspaceCleanupSelector(objectID, claimToken) {
		return store.NewErrInvalidInput("attempt_workspace_object", "cleanup_completion", nil)
	}
	result, err := s.GetMaster().Exec(ctx, `DELETE FROM exam_attempt_workspace_objects objects
		WHERE objects.id=? AND objects.storage_origin='attempt' AND objects.state='claimed' AND objects.claim_token=?
			AND NOT EXISTS (SELECT 1 FROM exam_attempt_workspace_entries entries WHERE entries.current_object_id=objects.id)
			AND NOT EXISTS (SELECT 1 FROM exam_submission_manifest_entries submitted WHERE submitted.attempt_object_id=objects.id)
			AND NOT EXISTS (SELECT 1 FROM command_outcomes outcomes
				WHERE outcomes.operation=? AND jsonb_exists(outcomes.outcome->'o',objects.id))
			AND NOT EXISTS (SELECT 1 FROM command_outcomes outcomes
				WHERE outcomes.original_audit_event_id=objects.retired_by_audit_event_id)`,
		objectID.String(), claimToken, store.ExamAttemptWorkspaceMutationOperation)
	if err != nil {
		return fmt.Errorf("complete Attempt Workspace object cleanup: %w", translateError("attempt_workspace_object", objectID.String(), err))
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect Attempt Workspace object cleanup: %w", err)
	}
	if affected == 1 {
		return nil
	}
	var exists bool
	if err = s.GetMaster().Get(ctx, &exists, `SELECT EXISTS (SELECT 1 FROM exam_attempt_workspace_objects WHERE id=?)`, objectID.String()); err != nil {
		return fmt.Errorf("inspect completed Attempt Workspace object cleanup: %w", err)
	}
	if exists {
		return store.NewErrConflict("attempt_workspace_object", "attempt_workspace_cleanup_claim", nil)
	}
	return nil
}

// ReleaseObjectCleanup returns a claimed Attempt Workspace object to the cleanup queue without removing it.
func (s *SQLExamAttemptWorkspaceStore) ReleaseObjectCleanup(ctx context.Context, input *store.AttemptWorkspaceCleanupRelease) error {
	objectID := input.ObjectID
	claimToken := input.ClaimToken
	if !validAttemptWorkspaceCleanupSelector(objectID, claimToken) {
		return store.NewErrInvalidInput("attempt_workspace_object", "cleanup_release", nil)
	}
	result, err := s.GetMaster().Exec(ctx, `UPDATE exam_attempt_workspace_objects
		SET state='reclaimable',updated_at=GREATEST(updated_at,statement_timestamp()),claim_token=NULL,claimed_at=NULL
		WHERE id=? AND storage_origin='attempt' AND state='claimed' AND claim_token=?`, objectID.String(), claimToken)
	if err != nil {
		return fmt.Errorf("release Attempt Workspace object cleanup: %w", err)
	}
	if affected, rowsErr := result.RowsAffected(); rowsErr != nil {
		return fmt.Errorf("inspect Attempt Workspace cleanup release: %w", rowsErr)
	} else if affected != 1 {
		return store.NewErrConflict("attempt_workspace_object", "attempt_workspace_cleanup_claim", nil)
	}
	return nil
}

func validAttemptWorkspaceCleanupSelector(objectID model.AttemptWorkspaceObjectID, claimToken string) bool {
	return objectID.IsValid() && strings.TrimSpace(claimToken) != "" && strings.TrimSpace(claimToken) == claimToken &&
		len(claimToken) <= model.AttemptWorkspaceClaimTokenMaxBytes
}

var _ store.ExamAttemptWorkspaceStore = (*SQLExamAttemptWorkspaceStore)(nil)
