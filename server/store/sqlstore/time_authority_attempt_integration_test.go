//go:build integration

// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

// TestTimeAuthorityAttemptWorkspaceCleanupClaimFollowsLaterModification claims
// a reclaimable Attempt Workspace object whose modification time is after the
// claiming statement's PostgreSQL time, as when a cleanup release commits after
// the claim statement began. The lifecycle CHECK requires claimed_at to equal
// updated_at, so the claim must record the same raised instant.
func TestTimeAuthorityAttemptWorkspaceCleanupClaimFollowsLaterModification(t *testing.T) {
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	ctx := context.Background()
	objectID := model.NewAttemptWorkspaceObjectID()
	// The object is the only row under test; replica role skips the
	// Workspace and Starter foreign keys while every CHECK still applies.
	_, err := runSQLTransaction(ctx, persistence.GetMaster().Begin, "insert leading Attempt Workspace object fixture",
		func(ctx context.Context, tx *sqlxTxWrapper) (struct{}, error) {
			if _, err := tx.Exec(ctx, `SET LOCAL session_replication_role = replica`); err != nil {
				return struct{}{}, err
			}
			_, err := tx.Exec(ctx, `INSERT INTO exam_attempt_workspace_objects
				(id,workspace_id,admission_revision_id,storage_origin,state,created_at,updated_at,reclaim_after)
				VALUES (?,?,?,'attempt','reclaimable',statement_timestamp()+INTERVAL '1 minute',
					statement_timestamp()+INTERVAL '1 minute',statement_timestamp()-INTERVAL '1 second')`,
				objectID.String(), model.NewExamAttemptWorkspaceID().String(), model.NewExamRevisionID().String())
			return struct{}{}, err
		})
	require.NoError(t, err)

	claimed, err := NewSQLExamAttemptWorkspaceStore(persistence).ClaimObjectsForCleanup(ctx, 10, "time-authority-claim")
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	require.Equal(t, objectID, claimed[0].ID)
	require.Equal(t, model.AttemptWorkspaceObjectClaimed, claimed[0].State)
	require.True(t, claimed[0].ClaimedAt.Valid)
	require.True(t, claimed[0].ClaimedAt.Time.Equal(claimed[0].UpdatedAt))
	require.False(t, claimed[0].UpdatedAt.Before(claimed[0].CreatedAt))
	var row attemptWorkspaceObjectRow
	err = persistence.GetMaster().Get(ctx, &row, attemptWorkspaceObjectSelect+` WHERE id=?`, objectID.String())
	require.NoError(t, err)
	stored, err := row.model()
	require.NoError(t, err)
	require.True(t, stored.ClaimedAt.Time.Equal(claimed[0].ClaimedAt.Time), "stored")
	require.True(t, stored.UpdatedAt.Equal(claimed[0].UpdatedAt), "stored")
	require.Equal(t, claimed[0].ClaimToken, stored.ClaimToken, "stored")
}
