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
	"errors"
	"strings"
	"testing"
	"time"

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
func (f *operatorStoreFake) CancelWithAudit(_ context.Context, mutation *store.JobMutation) (*model.Job, error) {
	f.mutation = mutation
	return f.job.RequestCancellation(f.job.UpdatedAt.Add(time.Second))
}
func (f *operatorStoreFake) RetryWithAudit(_ context.Context, mutation *store.JobMutation) (*model.Job, error) {
	f.mutation = mutation
	return f.job.ExplicitRetry(f.job.UpdatedAt.Add(time.Second))
}

func TestEngineProjectsOnlySafeOperatorFields(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 11, 8, 0, 0, 0, time.UTC)
	record, err := model.NewJob(model.NewJobID(), model.JobTypeProfilePictureGenerateDefault, 1, json.RawMessage(`{"secret":"must-not-leak"}`), "private-dedupe", at, at, 8)
	if err != nil {
		t.Fatal(err)
	}
	persistence := &operatorStoreFake{jobRunnerStoreFake: &jobRunnerStoreFake{}, job: record, list: []*model.Job{record}}
	engine, err := New(Config{
		Store: persistence, Descriptors: []Descriptor{testDescriptor(handlerFunc(func(context.Context, Execution) Outcome { return succeededOutcome() }))},
		NodeID: "node-a", Diagnostics: &jobDiagnosticsFake{}, Policy: Policy{PollInterval: time.Second},
	})
	if err != nil {
		t.Fatal(err)
	}
	page, err := engine.List(context.Background(), ListQuery{Limit: 20})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != record.ID {
		t.Fatalf("List() = %#v, %v", page, err)
	}
	encoded, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"must-not-leak", "private-dedupe", "command", "checkpoint", "result", "claim_token"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("safe projection leaked %q: %s", forbidden, encoded)
		}
	}
	if len(persistence.listOptions.Types) != 1 || persistence.listOptions.Types[0] != record.Type || persistence.listOptions.Limit != 20 {
		t.Fatalf("List() store options = %#v", persistence.listOptions)
	}
}

func TestEngineProjectsSafeAttemptsAndPreservesCursor(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 11, 8, 0, 0, 0, time.UTC)
	record, err := model.NewJob(model.NewJobID(), model.JobTypeProfilePictureGenerateDefault, 1, json.RawMessage(`{}`), "attempts", at, at, 8)
	if err != nil {
		t.Fatal(err)
	}
	attempt := model.JobAttempt{ID: model.NewJobAttemptID(), JobID: record.ID, Number: 3, Status: model.JobAttemptStatusRunning, NodeID: "private-node", ClaimToken: model.JobClaimToken(strings.Repeat("a", 64)), StartedAt: at, HeartbeatAt: at, LeaseExpiresAt: at.Add(time.Minute)}
	persistence := &operatorStoreFake{jobRunnerStoreFake: &jobRunnerStoreFake{}, job: record, attempts: []model.JobAttempt{attempt}}
	engine, err := New(Config{Store: persistence, Descriptors: []Descriptor{testDescriptor(handlerFunc(func(context.Context, Execution) Outcome { return succeededOutcome() }))}, NodeID: "node-a", Diagnostics: &jobDiagnosticsFake{}, Policy: Policy{PollInterval: time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	page, err := engine.Attempts(context.Background(), AttemptListQuery{JobID: record.ID, BeforeNumber: 4, Limit: 20})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != attempt.ID || persistence.attemptOptions.BeforeNumber != 4 || persistence.attemptOptions.Limit != 20 {
		t.Fatalf("Attempts() = %#v, options=%#v, %v", page, persistence.attemptOptions, err)
	}
	encoded, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "private-node") || strings.Contains(string(encoded), strings.Repeat("a", 64)) {
		t.Fatalf("attempt projection leaked claim data: %s", encoded)
	}
}

func TestEnginePreparesAndAppliesDescriptorGovernedControls(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 11, 8, 0, 0, 0, time.UTC)
	queued, err := model.NewJob(model.NewJobID(), model.JobTypeProfilePictureGenerateDefault, 1, json.RawMessage(`{}`), "control", at, at, 8)
	if err != nil {
		t.Fatal(err)
	}
	persistence := &operatorStoreFake{jobRunnerStoreFake: &jobRunnerStoreFake{}, job: queued}
	descriptor := testDescriptor(handlerFunc(func(context.Context, Execution) Outcome { return succeededOutcome() }))
	descriptor.Cancelable = true
	descriptor.ExplicitRetryStatuses = []model.JobStatus{model.JobStatusFailed}
	engine, err := New(Config{Store: persistence, Descriptors: []Descriptor{descriptor}, NodeID: "node-a", Diagnostics: &jobDiagnosticsFake{}, Policy: Policy{PollInterval: time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	target, err := engine.PrepareCancellation(context.Background(), queued.ID)
	if err != nil || target.View().ID != queued.ID {
		t.Fatalf("PrepareCancellation() = %#v, %v", target.View(), err)
	}
	target.Projection.ID = model.NewJobID()
	audit := AuditReference{EventID: model.NewId(), At: at.Add(time.Second).UnixMilli()}
	if _, err = engine.Cancel(context.Background(), target, AuditReference{}); !errors.Is(err, ErrCancelUnsupported) || persistence.mutation != nil {
		t.Fatalf("Cancel() with missing audit = %v, mutation=%#v", err, persistence.mutation)
	}
	if _, err = engine.Cancel(context.Background(), target, audit); err != nil {
		t.Fatal(err)
	}
	if persistence.mutation == nil || persistence.mutation.ID != queued.ID || persistence.mutation.ExpectedRevision != queued.Revision || persistence.mutation.AuditEventID != audit.EventID || persistence.mutation.AuditAt != audit.At {
		t.Fatalf("cancel mutation = %#v", persistence.mutation)
	}

	running, err := queued.Start(at.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	failed, err := running.Fail("job.failed", at.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	persistence.job = failed
	target, err = engine.PrepareRetry(context.Background(), failed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = engine.Retry(context.Background(), target, audit); err != nil {
		t.Fatal(err)
	}
	if persistence.mutation.ID != failed.ID || persistence.mutation.ExpectedRevision != failed.Revision {
		t.Fatalf("retry mutation = %#v", persistence.mutation)
	}
}

func TestEngineRejectsInvalidQueriesAndUnsupportedControls(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 11, 8, 0, 0, 0, time.UTC)
	record, err := model.NewJob(model.NewJobID(), model.JobTypeProfilePictureGenerateDefault, 1, json.RawMessage(`{}`), "unsupported", at, at, 8)
	if err != nil {
		t.Fatal(err)
	}
	persistence := &operatorStoreFake{jobRunnerStoreFake: &jobRunnerStoreFake{}, job: record}
	engine, err := New(Config{Store: persistence, Descriptors: []Descriptor{testDescriptor(handlerFunc(func(context.Context, Execution) Outcome { return succeededOutcome() }))}, NodeID: "node-a", Diagnostics: &jobDiagnosticsFake{}, Policy: Policy{PollInterval: time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = engine.List(context.Background(), ListQuery{Limit: 201}); !errors.Is(err, ErrQueryInvalid) {
		t.Fatalf("List() invalid query error = %v", err)
	}
	if _, err = engine.PrepareCancellation(context.Background(), record.ID); !errors.Is(err, ErrCancelUnsupported) {
		t.Fatalf("PrepareCancellation() error = %v", err)
	}
	if _, err = engine.PrepareRetry(context.Background(), record.ID); !errors.Is(err, ErrRetryUnsupported) {
		t.Fatalf("PrepareRetry() error = %v", err)
	}
}
