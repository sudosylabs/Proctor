// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package commands

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server"
)

func jobSummaryFixture() server.JobSummary {
	at := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	return server.JobSummary{
		ID: "job-1", Type: "mail.deliver", State: "queued", AttemptCount: 2, MaximumAttempts: 8,
		LastErrorCode: "mail.smtp_unavailable", CreatedAt: at.Add(-time.Hour), UpdatedAt: at, AvailableAt: at.Add(time.Minute),
	}
}

func TestJobsListPassesFiltersAndPrintsABoundedTable(t *testing.T) {
	t.Parallel()

	execute := testExecutors()
	execute.listJobs = func(_ context.Context, path string, query server.JobQuery) (server.JobList, error) {
		require.Equal(t, "/etc/proctor.json", path)
		require.Equal(t, server.JobQuery{Types: []string{"mail.deliver", "mail.cleanup"}, States: []string{"queued", "failed"}, Limit: 5}, query)
		return server.JobList{Jobs: []server.JobSummary{jobSummaryFixture()}, HasMore: true}, nil
	}

	code, stdout, stderr := executeForTest(context.Background(), []string{
		"jobs", "list", "--config", "/etc/proctor.json",
		"--type", "mail.deliver,mail.cleanup", "--state", "queued", "--state", "failed", "--limit", "5",
	}, nil, execute)
	require.Equal(t, 0, code, "stderr=%q", stderr)
	require.Empty(t, stderr)
	require.Equal(t, "ID     TYPE          STATE   ATTEMPTS  LAST ERROR             AVAILABLE             UPDATED\n"+
		"job-1  mail.deliver  queued  2/8       mail.smtp_unavailable  2026-10-23T12:01:00Z  2026-10-23T12:00:00Z\n"+
		"more Jobs match; narrow --type or --state, or raise --limit\n", stdout)
}

func TestJobsListRejectsUnknownFiltersAsUsage(t *testing.T) {
	t.Parallel()

	execute := testExecutors()
	execute.listJobs = func(context.Context, string, server.JobQuery) (server.JobList, error) {
		require.FailNow(t, "an invalid query reached the database")
		return server.JobList{}, nil
	}
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "type", args: []string{"--type", "mail.nope"}, want: `unknown job type "mail.nope"`},
		{name: "state", args: []string{"--state", "paused"}, want: `unknown job state "paused"`},
		{name: "limit", args: []string{"--limit", "201"}, want: "--limit must be between 1 and 200"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			code, stdout, stderr := executeForTest(context.Background(), append([]string{"jobs", "list"}, tt.args...), nil, execute)
			require.Equal(t, 2, code)
			require.Empty(t, stdout)
			require.Contains(t, stderr, tt.want)
		})
	}
}

func TestJobsShowPrintsTheJobAndAttemptsWithoutPayloads(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	completed := at.Add(time.Second)
	detail := server.JobDetail{
		JobSummary: jobSummaryFixture(),
		Attempts: []server.JobAttemptSummary{{
			Number: 2, State: "failed", NodeID: "node-a", StartedAt: at, HeartbeatAt: at, LeaseExpiresAt: at.Add(time.Minute),
			CompletedAt: &completed, ErrorCode: "mail.smtp_unavailable",
		}},
		MoreAttempts: true,
	}
	execute := testExecutors()
	execute.showJob = func(_ context.Context, path, id string) (server.JobDetail, error) {
		require.Equal(t, "/etc/proctor.json", path)
		require.Equal(t, "job-1", id)
		return detail, nil
	}

	code, stdout, stderr := executeForTest(context.Background(), []string{"jobs", "show", "job-1", "--config", "/etc/proctor.json"}, nil, execute)
	require.Equal(t, 0, code, "stderr=%q", stderr)
	require.Equal(t, "ID:               job-1\n"+
		"Type:             mail.deliver\n"+
		"State:            queued\n"+
		"Attempts:         2/8\n"+
		"Last error code:  mail.smtp_unavailable\n"+
		"Available:        2026-10-23T12:01:00Z\n"+
		"Created:          2026-10-23T11:00:00Z\n"+
		"Updated:          2026-10-23T12:00:00Z\n"+
		"Started:          -\n"+
		"Completed:        -\n"+
		"\n"+
		"ATTEMPT  STATE   NODE    STARTED               HEARTBEAT             LEASE EXPIRES         COMPLETED             ERROR\n"+
		"2        failed  node-a  2026-10-23T12:00:00Z  2026-10-23T12:00:00Z  2026-10-23T12:01:00Z  2026-10-23T12:00:01Z  mail.smtp_unavailable\n"+
		"older attempts are not shown\n", stdout)

	code, stdout, stderr = executeForTest(context.Background(), []string{"jobs", "show", "job-1", "--json", "--config", "/etc/proctor.json"}, nil, execute)
	require.Equal(t, 0, code, "stderr=%q", stderr)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &decoded))
	require.Equal(t, "job-1", decoded["id"])
	require.Equal(t, "mail.smtp_unavailable", decoded["last_error_code"])
	require.Len(t, decoded["attempts"], 1)
	for _, member := range []string{"command", "checkpoint", "result", "dedupe_key", "claim_token"} {
		require.NotContains(t, decoded, member)
	}
}

func TestJobsShowRequiresExactlyOneID(t *testing.T) {
	t.Parallel()

	code, _, stderr := executeForTest(context.Background(), []string{"jobs", "show"}, nil, testExecutors())
	require.Equal(t, 2, code)
	require.Contains(t, stderr, "accepts 1 arg(s)")
}
