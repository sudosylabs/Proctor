// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type jobOperationsStoreFake struct {
	job         *model.Job
	listOptions store.JobListOptions
	mutation    *store.JobMutation
}

func (f *jobOperationsStoreFake) Get(context.Context, model.JobID) (*model.Job, error) {
	return f.job, nil
}
func (f *jobOperationsStoreFake) List(_ context.Context, options store.JobListOptions) ([]*model.Job, error) {
	f.listOptions = options
	return []*model.Job{f.job}, nil
}
func (f *jobOperationsStoreFake) ListAttemptsPage(context.Context, store.JobAttemptListOptions) ([]model.JobAttempt, error) {
	return []model.JobAttempt{}, nil
}
func (f *jobOperationsStoreFake) CancelWithAudit(_ context.Context, mutation *store.JobMutation) (*model.Job, error) {
	f.mutation = mutation
	return f.job.RequestCancellation(time.Now().UTC())
}
func (f *jobOperationsStoreFake) RetryWithAudit(_ context.Context, mutation *store.JobMutation) (*model.Job, error) {
	f.mutation = mutation
	return f.job.ExplicitRetry(time.Now().UTC())
}

type jobOperationsAuthorizerFake struct {
	actions  []model.Action
	resource model.Resource
}

func (f *jobOperationsAuthorizerFake) Authorize(_ context.Context, _ Invocation, action model.Action) (model.Resource, error) {
	f.actions = append(f.actions, action)
	return f.resource, nil
}

type jobOperationsAuditorFake struct{ operation string }

func (f *jobOperationsAuditorFake) Begin(_ context.Context, _ Invocation, _ model.Action, _ model.Resource, operation string, _, _ map[string]any) (string, error) {
	f.operation = operation
	return model.NewId(), nil
}
func (*jobOperationsAuditorFake) Fail(context.Context, string, string) error { return nil }

type noOpJobHandler struct{}

func (noOpJobHandler) Run(context.Context, JobExecution) JobOutcome { return JobOutcome{} }

func jobOperationsRegistry(t *testing.T) *JobRegistry {
	t.Helper()
	registry, err := NewJobRegistry([]JobDescriptor{{
		Type: model.JobTypeProfilePictureGenerateDefault, CommandVersions: []int{1}, ResultVersions: []int{1},
		Timeout: time.Minute, Concurrency: 1, MaximumAttempts: 8, LeaseDuration: time.Minute,
		HeartbeatInterval: 15 * time.Second, BaseRetryDelay: time.Second, MaximumRetryDelay: time.Minute,
		Cancelable: true, ExplicitRetryStatuses: []model.JobStatus{model.JobStatusFailed}, Visibility: JobVisibilityOperator,
		SuccessRetention: 24 * time.Hour, FailureRetention: 48 * time.Hour, Handler: noOpJobHandler{},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func TestJobOperationsExposeOnlySafeAllowlistedProjection(t *testing.T) {
	at := time.Now().UTC().Add(-time.Minute)
	job, err := model.NewJob(model.NewJobID(), model.JobTypeProfilePictureGenerateDefault, 1, json.RawMessage(`{"secret":"must-not-leak"}`), "private-dedupe", at, at, 8)
	if err != nil {
		t.Fatal(err)
	}
	persistence := &jobOperationsStoreFake{job: job}
	authorizer := &jobOperationsAuthorizerFake{resource: model.Resource{Type: model.ResourceInstitution, ID: model.NewId()}}
	service := newJobOperationsService(persistence, authorizer, &jobOperationsAuditorFake{}, jobOperationsRegistry(t), time.Now)

	page, appErr := service.List(context.Background(), Invocation{}, ListJobsQuery{Limit: 20})
	if appErr != nil {
		t.Fatal(appErr)
	}
	if len(page.Items) != 1 || page.Items[0].ID != job.ID || len(persistence.listOptions.Types) != 1 || authorizer.actions[0] != model.ActionJobView {
		t.Fatalf("safe list = %#v, options = %#v, actions = %#v", page, persistence.listOptions, authorizer.actions)
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
}

func TestJobOperationsCancelAndRetryOnlyWhenDescriptorDeclaresSafe(t *testing.T) {
	at := time.Now().UTC().Add(-time.Minute)
	queued, err := model.NewJob(model.NewJobID(), model.JobTypeProfilePictureGenerateDefault, 1, json.RawMessage(`{}`), "control", at, at, 8)
	if err != nil {
		t.Fatal(err)
	}
	persistence := &jobOperationsStoreFake{job: queued}
	authorizer := &jobOperationsAuthorizerFake{resource: model.Resource{Type: model.ResourceInstitution, ID: model.NewId()}}
	auditor := &jobOperationsAuditorFake{}
	service := newJobOperationsService(persistence, authorizer, auditor, jobOperationsRegistry(t), time.Now)

	if _, appErr := service.Cancel(context.Background(), Invocation{}, CancelJobCommand{ID: queued.ID}); appErr != nil {
		t.Fatal(appErr)
	}
	if persistence.mutation == nil || auditor.operation != "cancel" || authorizer.actions[len(authorizer.actions)-1] != model.ActionJobManage {
		t.Fatal("cancel was not authorized and audited")
	}

	running, _ := queued.Start(at.Add(time.Second))
	failed, _ := running.Fail("job.failed", at.Add(2*time.Second))
	persistence.job = failed
	if _, appErr := service.Retry(context.Background(), Invocation{}, RetryJobCommand{ID: failed.ID}); appErr != nil {
		t.Fatal(appErr)
	}
	if auditor.operation != "retry" {
		t.Fatal("retry was not audited")
	}
	page, _ := service.Attempts(context.Background(), Invocation{}, ListJobAttemptsQuery{JobID: failed.ID, Limit: 20})
	if page.Items == nil {
		t.Fatal("empty attempt history must be non-null")
	}
}
