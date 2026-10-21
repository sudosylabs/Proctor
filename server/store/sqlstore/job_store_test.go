// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

func TestJobRowRehydrationRejectsInvalidPersistedState(t *testing.T) {
	t.Parallel()

	valid := validJobRow(t)
	tests := []struct {
		name  string
		row   jobRow
		field string
	}{
		{name: "job id", row: replaceJobRow(valid, func(row *jobRow) { row.ID = "bad" }), field: "id"},
		{name: "domain state", row: replaceJobRow(valid, func(row *jobRow) { row.Status = "unknown" }), field: "value"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.row.model()
			requirePersistedStateError(t, err, "job", test.field)
			require.NotContains(t, err.Error(), "bad")
		})
	}
}

func TestJobAttemptRowRehydrationRejectsInvalidPersistedState(t *testing.T) {
	t.Parallel()

	valid := validJobAttemptRow(t)
	tests := []struct {
		name  string
		row   jobAttemptRow
		field string
	}{
		{name: "attempt id", row: replaceJobAttemptRow(valid, func(row *jobAttemptRow) { row.ID = "bad" }), field: "id"},
		{name: "job id", row: replaceJobAttemptRow(valid, func(row *jobAttemptRow) { row.JobID = "bad" }), field: "job_id"},
		{name: "non-id domain state", row: replaceJobAttemptRow(valid, func(row *jobAttemptRow) { row.ClaimToken = "bad" }), field: "value"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.row.model()
			requirePersistedStateError(t, err, "job_attempt", test.field)
			require.NotContains(t, err.Error(), "bad")
		})
	}
}

func TestJobTransactionPoliciesPreserveLegacyBranchSemantics(t *testing.T) {
	t.Parallel()

	failure := errors.New("database failure")
	tests := []struct {
		name string
		got  error
		want string
	}{
		{name: "permanent enqueue begin", got: permanentJobTransactionPolicy().beginError(failure), want: "begin permanent job enqueue: database failure"},
		{name: "permanent enqueue commit", got: permanentJobTransactionPolicy().commitError(&permanentJobEnqueueOutcome{created: true}, failure), want: "commit permanent job enqueue: database failure"},
		{name: "deduplicated enqueue commit", got: permanentJobTransactionPolicy().commitError(&permanentJobEnqueueOutcome{}, failure), want: "commit deduplicated permanent job enqueue: database failure"},
		{name: "job claim begin", got: jobClaimSQLTransactionPolicy().beginError(failure), want: "begin job claim: database failure"},
		{name: "job claim commit", got: jobClaimSQLTransactionPolicy().commitError(&jobClaimTransactionOutcome{}, failure), want: "commit job claim: database failure"},
		{name: "expired recovery commit", got: jobClaimSQLTransactionPolicy().commitError(&jobClaimTransactionOutcome{postCommitErr: errors.New("not found")}, failure), want: "commit expired job recovery: database failure"},
		{name: "history cleanup begin", got: jobHistorySQLTransactionPolicy().beginError(failure), want: "begin job history cleanup: database failure"},
		{name: "history cleanup commit", got: jobHistorySQLTransactionPolicy().commitError(&jobHistoryCleanupOutcome{}, failure), want: "commit job history cleanup: database failure"},
		{name: "empty history cleanup commit", got: jobHistorySQLTransactionPolicy().commitError(&jobHistoryCleanupOutcome{empty: true}, failure), want: "commit empty job history cleanup: database failure"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Error(t, test.got, "policy error")
			require.Equal(t, test.want, test.got.Error(), "policy error")
			require.ErrorIs(t, test.got, failure, "policy error")
		})
	}

	raw := rawSQLTransactionPolicy[bool](false, nil)
	require.False(t, raw.commit)
	require.Equal(t, failure, raw.beginError(failure))
	rawCommit := rawSQLTransactionPolicy[bool](true, func(_ bool, err error) error { return err })
	require.True(t, rawCommit.commit)
	require.Equal(t, failure, rawCommit.commitError(true, failure))
}

func validJobRow(t *testing.T) jobRow {
	t.Helper()
	now := time.UnixMilli(1_700_000_000_000).UTC()
	return jobRow{
		ID: model.NewJobID().String(), Type: string(model.JobTypeCleanup), Status: string(model.JobStatusQueued),
		CreatedAt: now, UpdatedAt: now, AvailableAt: now,
		CommandVersion: 1, Command: jsonValue(json.RawMessage(`{}`)),
		DedupeKey: model.NewID(), DedupePolicy: string(model.JobDedupeActive),
		MaximumAttempts: 3, Revision: 1,
	}
}

func validJobAttemptRow(t *testing.T) jobAttemptRow {
	t.Helper()
	now := time.UnixMilli(1_700_000_000_000).UTC()
	token, err := model.NewJobClaimToken()
	require.NoError(t, err)
	return jobAttemptRow{
		ID: model.NewJobAttemptID().String(), JobID: model.NewJobID().String(), Number: 1,
		Status: string(model.JobAttemptStatusRunning), NodeID: "node-a", ClaimToken: string(token),
		StartedAt: now, HeartbeatAt: now, LeaseExpiresAt: now.Add(time.Minute), CompletedAt: sql.NullTime{},
	}
}

func replaceJobRow(row jobRow, replace func(*jobRow)) jobRow {
	replace(&row)
	return row
}

func replaceJobAttemptRow(row jobAttemptRow, replace func(*jobAttemptRow)) jobAttemptRow {
	replace(&row)
	return row
}

// requirePersistedStateError asserts that err reports the corrupt persisted
// entity and field.
func requirePersistedStateError(t *testing.T, err error, entity, field string) {
	t.Helper()
	var persisted *persistedStateError
	require.ErrorAs(t, err, &persisted)
	require.Equal(t, entity, persisted.Entity)
	require.Equal(t, field, persisted.Field)
}
