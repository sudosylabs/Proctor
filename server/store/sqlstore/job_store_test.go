// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

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
			assertPersistedJobStateError(t, err, "job", test.field)
			if strings.Contains(err.Error(), "bad") {
				t.Fatalf("persisted-state error exposed raw value: %v", err)
			}
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
			assertPersistedJobStateError(t, err, "job_attempt", test.field)
			if strings.Contains(err.Error(), "bad") {
				t.Fatalf("persisted-state error exposed raw value: %v", err)
			}
		})
	}
}

func validJobRow(t *testing.T) jobRow {
	t.Helper()
	now := time.UnixMilli(1_700_000_000_000).UTC()
	return jobRow{
		ID: model.NewJobID().String(), Type: string(model.JobTypeCleanup), Status: string(model.JobStatusQueued),
		CreatedAt: now, UpdatedAt: now, AvailableAt: now,
		CommandVersion: 1, Command: jsonValue(json.RawMessage(`{}`)),
		DedupeKey: model.NewId(), DedupePolicy: string(model.JobDedupeActive),
		MaximumAttempts: 3, Revision: 1,
	}
}

func validJobAttemptRow(t *testing.T) jobAttemptRow {
	t.Helper()
	now := time.UnixMilli(1_700_000_000_000).UTC()
	token, err := model.NewJobClaimToken()
	if err != nil {
		t.Fatal(err)
	}
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

func assertPersistedJobStateError(t *testing.T, err error, entity, field string) {
	t.Helper()
	var persisted *persistedStateError
	if !errors.As(err, &persisted) {
		t.Fatalf("model() error = %v, want persisted-state error", err)
	}
	if persisted.Entity != entity || persisted.Field != field {
		t.Fatalf("persisted-state context = %s.%s, want %s.%s", persisted.Entity, persisted.Field, entity, field)
	}
}
