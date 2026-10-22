// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package job

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type operatorStoreFake struct {
	*jobRunnerStoreFake
	job            *model.Job
	list           []*model.Job
	attempts       []model.JobAttempt
	mutation       *store.JobMutation
	listOptions    store.JobListOptions
	attemptOptions store.JobAttemptListOptions
}

func (f *operatorStoreFake) Get(context.Context, model.JobID) (*model.Job, error) { return f.job, nil }
func (f *operatorStoreFake) List(_ context.Context, options store.JobListOptions) ([]*model.Job, error) {
	f.listOptions = options
	return f.list, nil
}
func (f *operatorStoreFake) ListAttemptsPage(_ context.Context, options store.JobAttemptListOptions) ([]model.JobAttempt, error) {
	f.attemptOptions = options
	return f.attempts, nil
}
func (f *operatorStoreFake) Cancel(_ context.Context, mutation *store.JobMutation) (*model.Job, error) {
	f.mutation = mutation
	return f.job.RequestCancellation(f.job.UpdatedAt.Add(time.Second))
}
func (f *operatorStoreFake) Retry(_ context.Context, mutation *store.JobMutation) (*model.Job, error) {
	f.mutation = mutation
	return f.job.ExplicitRetry(f.job.UpdatedAt.Add(time.Second))
}

func TestEngineProjectsOnlySafeOperatorFields(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 11, 8, 0, 0, 0, time.UTC)
	record, err := model.NewJob(model.NewJobID(), model.JobTypeProfilePictureGenerateDefault, json.RawMessage(`{"secret":"must-not-leak"}`), "private-dedupe", at, at, 8)
	require.NoError(t, err)
	persistence := &operatorStoreFake{jobRunnerStoreFake: &jobRunnerStoreFake{}, job: record, list: []*model.Job{record}}
	engine, err := New(Config{
		Store: persistence, Descriptors: []Descriptor{testDescriptor(handlerFunc(func(context.Context, Execution) Outcome { return succeededOutcome() }))},
		NodeID: "node-a", Diagnostics: &jobDiagnosticsFake{}, Policy: Policy{PollInterval: time.Second},
	})
	require.NoError(t, err)
	page, err := engine.List(context.Background(), ListQuery{Limit: 20})
	require.NoError(t, err, "List() = %#v", page)
	require.Len(t, page.Items, 1, "List() = %#v", page)
	require.Equal(t, record.ID, page.Items[0].ID, "List() = %#v", page)
	encoded, err := json.Marshal(page)
	require.NoError(t, err)
	for _, forbidden := range []string{"must-not-leak", "private-dedupe", "command", "checkpoint", "result", "claim_token"} {
		require.NotContains(t, string(encoded), forbidden, "safe projection leaked")
	}
	require.Len(t, persistence.listOptions.Types, 1)
	require.Equal(t, record.Type, persistence.listOptions.Types[0])
	require.Equal(t, 20, persistence.listOptions.Limit)
}

func TestEngineProjectsSafeAttemptsAndPreservesCursor(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 11, 8, 0, 0, 0, time.UTC)
	record, err := model.NewJob(model.NewJobID(), model.JobTypeProfilePictureGenerateDefault, json.RawMessage(`{}`), "attempts", at, at, 8)
	require.NoError(t, err)
	attempt := model.JobAttempt{ID: model.NewJobAttemptID(), JobID: record.ID, Number: 3, Status: model.JobAttemptStatusRunning, NodeID: "private-node", ClaimToken: model.JobClaimToken(strings.Repeat("a", 64)), StartedAt: at, HeartbeatAt: at, LeaseExpiresAt: at.Add(time.Minute)}
	persistence := &operatorStoreFake{jobRunnerStoreFake: &jobRunnerStoreFake{}, job: record, attempts: []model.JobAttempt{attempt}}
	engine, err := New(Config{Store: persistence, Descriptors: []Descriptor{testDescriptor(handlerFunc(func(context.Context, Execution) Outcome { return succeededOutcome() }))}, NodeID: "node-a", Diagnostics: &jobDiagnosticsFake{}, Policy: Policy{PollInterval: time.Second}})
	require.NoError(t, err)
	page, err := engine.Attempts(context.Background(), AttemptListQuery{JobID: record.ID, BeforeNumber: 4, Limit: 20})
	require.NoError(t, err, "Attempts() = %#v, options=%#v", page, persistence.attemptOptions)
	require.Len(t, page.Items, 1, "Attempts() = %#v, options=%#v", page, persistence.attemptOptions)
	require.Equal(t, attempt.ID, page.Items[0].ID, "Attempts() = %#v, options=%#v", page, persistence.attemptOptions)
	require.Equal(t, 4, persistence.attemptOptions.BeforeNumber, "Attempts() = %#v, options=%#v", page, persistence.attemptOptions)
	require.Equal(t, 20, persistence.attemptOptions.Limit, "Attempts() = %#v, options=%#v", page, persistence.attemptOptions)
	encoded, err := json.Marshal(page)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "private-node")
	require.NotContains(t, string(encoded), strings.Repeat("a", 64))
}

func TestEnginePreparesAndAppliesDescriptorGovernedControls(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 11, 8, 0, 0, 0, time.UTC)
	queued, err := model.NewJob(model.NewJobID(), model.JobTypeProfilePictureGenerateDefault, json.RawMessage(`{}`), "control", at, at, 8)
	require.NoError(t, err)
	persistence := &operatorStoreFake{jobRunnerStoreFake: &jobRunnerStoreFake{}, job: queued}
	descriptor := testDescriptor(handlerFunc(func(context.Context, Execution) Outcome { return succeededOutcome() }))
	descriptor.Cancelable = true
	descriptor.ExplicitRetryStatuses = []model.JobStatus{model.JobStatusFailed}
	engine, err := New(Config{Store: persistence, Descriptors: []Descriptor{descriptor}, NodeID: "node-a", Diagnostics: &jobDiagnosticsFake{}, Policy: Policy{PollInterval: time.Second}})
	require.NoError(t, err)
	target, err := engine.PrepareCancellation(context.Background(), queued.ID)
	require.NoError(t, err, "PrepareCancellation()")
	require.Equal(t, queued.ID, target.View().ID, "PrepareCancellation()")
	target.Projection.ID = model.NewJobID()
	audit := AuditReference{EventID: model.NewAuditEventID(), At: at.Add(time.Second).UnixMilli()}
	_, err = engine.Cancel(context.Background(), target, AuditReference{})
	require.ErrorIs(t, err, ErrCancelUnsupported, "Cancel() with missing audit")
	require.Nil(t, persistence.mutation, "Cancel() with missing audit")
	_, err = engine.Cancel(context.Background(), target, audit)
	require.NoError(t, err)
	require.NotNil(t, persistence.mutation)
	require.Equal(t, queued.ID, persistence.mutation.ID)
	require.Equal(t, queued.Revision, persistence.mutation.ExpectedRevision)
	require.Equal(t, audit.EventID, persistence.mutation.AuditEventID)
	require.Equal(t, audit.At, persistence.mutation.AuditAt)

	running, err := queued.Start(at.Add(time.Second))
	require.NoError(t, err)
	failed, err := running.Fail("job.failed", at.Add(2*time.Second))
	require.NoError(t, err)
	persistence.job = failed
	target, err = engine.PrepareRetry(context.Background(), failed.ID)
	require.NoError(t, err)
	_, err = engine.Retry(context.Background(), target, audit)
	require.NoError(t, err)
	require.Equal(t, failed.ID, persistence.mutation.ID)
	require.Equal(t, failed.Revision, persistence.mutation.ExpectedRevision)
}

func TestEngineRejectsInvalidQueriesAndUnsupportedControls(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 11, 8, 0, 0, 0, time.UTC)
	record, err := model.NewJob(model.NewJobID(), model.JobTypeProfilePictureGenerateDefault, json.RawMessage(`{}`), "unsupported", at, at, 8)
	require.NoError(t, err)
	persistence := &operatorStoreFake{jobRunnerStoreFake: &jobRunnerStoreFake{}, job: record}
	engine, err := New(Config{Store: persistence, Descriptors: []Descriptor{testDescriptor(handlerFunc(func(context.Context, Execution) Outcome { return succeededOutcome() }))}, NodeID: "node-a", Diagnostics: &jobDiagnosticsFake{}, Policy: Policy{PollInterval: time.Second}})
	require.NoError(t, err)
	_, err = engine.List(context.Background(), ListQuery{Limit: 201})
	require.ErrorIs(t, err, ErrQueryInvalid)
	_, err = engine.PrepareCancellation(context.Background(), record.ID)
	require.ErrorIs(t, err, ErrCancelUnsupported)
	_, err = engine.PrepareRetry(context.Background(), record.ID)
	require.ErrorIs(t, err, ErrRetryUnsupported)
}
