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

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func populateCandidateTerminalProjection(ctx context.Context, executor sqlxExecutor, attemptID model.ExamAttemptID, terminal *store.CandidateTerminalCapability) error {
	var row struct {
		Epoch   sql.NullString `db:"epoch"`
		Applied int64          `db:"applied"`
		State   string         `db:"projection_state"`
	}
	err := executor.Get(ctx, &row, `SELECT NULLIF(g.environment_epoch,'') AS epoch,
 CASE WHEN g.environment_epoch<>'' THEN g.applied_workspace_cursor ELSE 0 END AS applied,
 CASE WHEN g.id IS NULL OR g.environment_epoch='' OR g.desired_control_state<>'running'
 THEN 'unavailable'
 WHEN g.state<>'ready' OR g.lifecycle_pending OR g.workspace_pending
 OR g.control_revision<>g.control_acknowledged_revision OR g.applied_workspace_cursor<w.cursor
 OR EXISTS(SELECT 1 FROM execution_projection_effects e WHERE e.execution_grant_id=g.id AND NOT e.completed)
 THEN 'synchronizing' ELSE 'ready' END AS projection_state
 FROM exam_attempt_workspaces w LEFT JOIN execution_grants g ON g.exam_attempt_id=w.exam_attempt_id
 AND g.state IN ('reserved','ready') WHERE w.exam_attempt_id=?`, attemptID.String())
	if err != nil {
		return translateError("exam_attempt_workspace", attemptID.String(), err)
	}
	terminal.EnvironmentEpoch = nil
	if row.Epoch.Valid {
		terminal.EnvironmentEpoch = &row.Epoch.String
	}
	terminal.AppliedWorkspaceCursor = row.Applied
	terminal.ProjectionState = store.ExecutionProjectionState(row.State)
	if terminal.State == store.CandidateTerminalAvailable && terminal.ProjectionState != store.ExecutionProjectionReady {
		terminal.State = store.CandidateTerminalTemporarilyUnavailable
	}
	return nil
}
