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
	"encoding/json"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type executionProjectionRow struct {
	executionWorkspaceNodeRow
	Cursor          int64          `db:"cursor"`
	Operation       string         `db:"operation"`
	OldPath         sql.NullString `db:"old_path"`
	NewPath         sql.NullString `db:"new_path"`
	ResultVersion   sql.NullString `db:"result_version"`
	ExpectedVersion sql.NullString `db:"expected_content_version"`
	SourceGrantID   sql.NullString `db:"source_grant_id"`
	ChangedAt       time.Time      `db:"changed_at"`
	Recursive       bool           `db:"recursive"`
	ObjectState     sql.NullString `db:"object_state"`
}

func (s SQLExecutionGrantStore) WorkspaceChanges(ctx context.Context, fence model.ExecutionFence, limit int) (*store.ExecutionWorkspacePage, error) {
	if fence.Validate() != nil || limit < 1 || limit > 128 {
		return nil, store.NewErrInvalidInput("execution_grant", "workspace_changes", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "read execution projection journal", func(ctx context.Context, tx *sqlxTxWrapper) (*store.ExecutionWorkspacePage, error) {
		grant, authority, err := lockExecutionControl(ctx, tx, fence.GrantID)
		if err != nil {
			return nil, err
		}
		if grant.Fence() != fence || grant.State != model.ExecutionGrantReady || grant.ControlAcknowledgedRevision != grant.ControlRevision ||
			grant.DesiredControlState != model.ExecutionControlRunning || authority.desired(grant) != model.ExecutionControlRunning || grant.ControlAuthorityDigest != authority.digest() {
			return nil, store.NewErrConflict("execution_grant", "projection_fence", nil)
		}
		return listExecutionWorkspaceChanges(ctx, tx, grant, limit)
	})
}

func listExecutionWorkspaceChanges(ctx context.Context, tx *sqlxTxWrapper, grant *model.ExecutionGrant, limit int) (*store.ExecutionWorkspacePage, error) {
	var workspace struct {
		ID     string `db:"id"`
		Cursor int64  `db:"cursor"`
	}
	if err := tx.Get(ctx, &workspace, `SELECT id,cursor FROM exam_attempt_workspaces WHERE exam_attempt_id=$1 FOR SHARE`, grant.AttemptID.String()); err != nil {
		return nil, err
	}
	workspaceID, err := model.ParseExamAttemptWorkspaceID(workspace.ID)
	if err != nil {
		return nil, invalidPersistedState("execution_workspace", "workspace_id", err)
	}
	page := &store.ExecutionWorkspacePage{CurrentCursor: workspace.Cursor, Changes: []store.ExecutionWorkspaceChange{}}
	gap := func() (*store.ExecutionWorkspacePage, error) {
		page.RefreshRequired = true
		page.HasMore = false
		page.Changes = []store.ExecutionWorkspaceChange{}
		return page, nil
	}
	if grant.AppliedWorkspaceCursor > workspace.Cursor {
		return gap()
	}
	var rows []executionProjectionRow
	if err := tx.Select(ctx, &rows, `SELECT j.cursor,j.entry_id,j.entry_kind AS kind,COALESCE(j.new_path,j.old_path) AS path,
  j.operation,j.old_path,j.new_path,j.content_version AS result_version,j.expected_content_version,j.source_grant_id,j.changed_at,j.recursive,
  o.content_version,o.size_bytes,o.sha256,o.storage_origin,o.starter_object_id,o.id AS attempt_object_id,o.state AS object_state
  FROM exam_attempt_workspace_journal j LEFT JOIN exam_attempt_workspace_objects o ON o.id=j.projected_object_id AND o.workspace_id=j.workspace_id
  WHERE j.workspace_id=$1 AND j.cursor>$2 AND j.cursor<=$3 ORDER BY j.cursor LIMIT $4`, workspace.ID, grant.AppliedWorkspaceCursor, workspace.Cursor, limit); err != nil {
		return nil, err
	}
	if len(rows) == 0 && grant.AppliedWorkspaceCursor < workspace.Cursor {
		return gap()
	}
	envelope, err := json.Marshal(page)
	if err != nil {
		return nil, err
	}
	metadataBytes := len(envelope)
	for i, row := range rows {
		if row.Cursor != grant.AppliedWorkspaceCursor+int64(i)+1 {
			return gap()
		}
		entryID, err := model.ParseAttemptWorkspaceEntryID(row.EntryID)
		if err != nil {
			return nil, invalidPersistedState("execution_workspace", "entry_id", err)
		}
		change := store.ExecutionWorkspaceChange{Change: model.AttemptWorkspaceJournalEntry{WorkspaceID: workspaceID, Cursor: row.Cursor, EntryID: entryID,
			EntryKind: model.StarterWorkspaceEntryKind(row.Kind), Operation: model.AttemptWorkspaceMutationKind(row.Operation),
			OldPath: row.OldPath.String, NewPath: row.NewPath.String, ChangedAt: model.TimeUTC(row.ChangedAt), Recursive: row.Recursive}}
		if row.ResultVersion.Valid {
			change.Change.ContentVersion, err = model.ParseWorkspaceContentVersion(row.ResultVersion.String)
			if err != nil {
				return nil, invalidPersistedState("execution_workspace", "result_version", err)
			}
		}
		if row.ExpectedVersion.Valid {
			change.ExpectedContentVersion, err = model.ParseWorkspaceContentVersion(row.ExpectedVersion.String)
			if err != nil {
				return nil, invalidPersistedState("execution_workspace", "expected_version", err)
			}
		}
		if row.SourceGrantID.Valid {
			change.SourceGrantID, err = model.ParseExecutionGrantID(row.SourceGrantID.String)
			if err != nil {
				return nil, invalidPersistedState("execution_workspace", "source_grant_id", err)
			}
		}
		if err := change.Change.Validate(); err != nil {
			return nil, invalidPersistedState("execution_workspace", "journal", err)
		}
		requiresVersion := change.Change.EntryKind == model.StarterWorkspaceEntryFile && change.Change.Operation != model.AttemptWorkspaceMutationCreateFile
		if requiresVersion != change.ExpectedContentVersion.IsValid() {
			return nil, invalidPersistedState("execution_workspace", "expected_version", nil)
		}
		if change.Change.Operation == model.AttemptWorkspaceMutationCreateFile || change.Change.Operation == model.AttemptWorkspaceMutationReplaceFile {
			if !row.ObjectState.Valid || row.ObjectState.String == string(model.AttemptWorkspaceObjectClaimed) {
				return gap()
			}
			node, err := executionWorkspaceNode(row.executionWorkspaceNodeRow)
			if err != nil {
				return nil, err
			}
			if node.ContentVersion != change.Change.ContentVersion {
				return nil, invalidPersistedState("execution_workspace", "object_version", nil)
			}
			change.Content = &node
		}
		metadata, err := json.Marshal(change)
		if err != nil {
			return nil, err
		}
		added := len(metadata)
		if len(page.Changes) > 0 {
			added++
		}
		if metadataBytes+added > 256<<10 {
			break
		}
		metadataBytes += added
		page.Changes = append(page.Changes, change)
	}
	if len(page.Changes) == 0 && grant.AppliedWorkspaceCursor < workspace.Cursor {
		return gap()
	}
	if len(page.Changes) > 0 {
		page.HasMore = page.Changes[len(page.Changes)-1].Change.Cursor < workspace.Cursor
	}
	return page, nil
}
