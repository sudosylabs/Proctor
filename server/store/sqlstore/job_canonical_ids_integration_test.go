//go:build integration

// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lib/pq"

	"github.com/sudosylabs/proctor/server/model"
)

func TestJobCanonicalIDConstraintsRejectNoncanonicalValues(t *testing.T) {
	ss := openTestStore(t)
	resetTestStore(t, ss)
	ctx := context.Background()
	now := model.NowUTC()
	jobID := model.NewJobID()
	if _, err := ss.GetMaster().Exec(ctx, `INSERT INTO jobs (id, type, status, created_at, updated_at, available_at, command_version, command, dedupe_key, maximum_attempts, revision) VALUES (?, ?, 'queued', ?, ?, ?, 1, '{}'::jsonb, ?, 3, 1)`, jobID.String(), string(model.JobTypeCleanup), now, now, now, jobID.String()); err != nil {
		t.Fatal(err)
	}
	token, err := model.NewJobClaimToken()
	if err != nil {
		t.Fatal(err)
	}
	attemptID := model.NewJobAttemptID()
	if _, err = ss.GetMaster().Exec(ctx, `INSERT INTO job_attempts (id, job_id, number, status, node_id, claim_token, started_at, heartbeat_at, lease_expires_at) VALUES (?, ?, 1, 'running', 'node-a', ?, ?, ?, ?)`, attemptID.String(), jobID.String(), string(token), now, now, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err = ss.GetMaster().Exec(ctx, `INSERT INTO job_permanent_occurrences (type, dedupe_key, job_id, created_at) VALUES (?, ?, ?, ?)`, string(model.JobTypeCleanup), "occurrence", jobID.String(), now); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		query      string
		argument   string
		constraint string
	}{
		{name: "job id", query: "UPDATE jobs SET id = 'bad' WHERE id = ?", argument: jobID.String(), constraint: "jobs_id_canonical_check"},
		{name: "attempt id", query: "UPDATE job_attempts SET id = 'bad' WHERE id = ?", argument: attemptID.String(), constraint: "job_attempts_id_canonical_check"},
		{name: "attempt job id", query: "UPDATE job_attempts SET job_id = 'bad' WHERE id = ?", argument: attemptID.String(), constraint: "job_attempts_job_id_canonical_check"},
		{name: "occurrence job id", query: "UPDATE job_permanent_occurrences SET job_id = 'bad' WHERE dedupe_key = ?", argument: "occurrence", constraint: "job_permanent_occurrences_job_id_canonical_check"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ss.GetMaster().Exec(ctx, test.query, test.argument)
			var postgresErr *pq.Error
			if !errors.As(err, &postgresErr) || string(postgresErr.Code) != "23514" || postgresErr.Constraint != test.constraint {
				t.Fatalf("constraint error = %#v, want check violation %s", err, test.constraint)
			}
		})
	}
}
