// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	jobengine "github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/model"
)

type jobOperatorEngineFake struct {
	events     *[]string
	view       JobView
	prepareErr error
	applyErr   error
	listQuery  jobengine.ListQuery
	applyCalls int
}

func (f *jobOperatorEngineFake) List(_ context.Context, query jobengine.ListQuery) (jobengine.Page, error) {
	*f.events = append(*f.events, "list")
	f.listQuery = query
	return JobPage{Items: []JobView{f.view}}, nil
}
func (f *jobOperatorEngineFake) Get(context.Context, model.JobID) (jobengine.View, error) {
	*f.events = append(*f.events, "get")
	return f.view, nil
}
func (f *jobOperatorEngineFake) Attempts(context.Context, jobengine.AttemptListQuery) (jobengine.AttemptPage, error) {
	*f.events = append(*f.events, "attempts")
	return JobAttemptPage{Items: make([]JobAttemptView, 0)}, nil
}
func (f *jobOperatorEngineFake) PrepareCancellation(context.Context, model.JobID) (jobengine.ControlTarget, error) {
	*f.events = append(*f.events, "prepare-cancel")
	return jobengine.ControlTarget{Projection: f.view}, f.prepareErr
}
func (f *jobOperatorEngineFake) PrepareRetry(context.Context, model.JobID) (jobengine.ControlTarget, error) {
	*f.events = append(*f.events, "prepare-retry")
	return jobengine.ControlTarget{Projection: f.view}, f.prepareErr
}
func (f *jobOperatorEngineFake) Cancel(context.Context, jobengine.ControlTarget, jobengine.AuditReference) (jobengine.View, error) {
	*f.events = append(*f.events, "cancel")
	f.applyCalls++
	return f.view, f.applyErr
}
func (f *jobOperatorEngineFake) Retry(context.Context, jobengine.ControlTarget, jobengine.AuditReference) (jobengine.View, error) {
	*f.events = append(*f.events, "retry")
	f.applyCalls++
	return f.view, f.applyErr
}

type jobOperationsAuthorizerFake struct {
	events   *[]string
	actions  []model.Action
	resource model.Resource
	err      error
}

func (f *jobOperationsAuthorizerFake) Authorize(_ context.Context, _ Invocation, action model.Action) (model.Resource, error) {
	*f.events = append(*f.events, "authorize")
	f.actions = append(f.actions, action)
	return f.resource, f.err
}

type jobOperationsAuditorFake struct {
	events    *[]string
	operation string
	params    map[string]any
	err       error
	failCalls int
}

func (f *jobOperationsAuditorFake) Begin(_ context.Context, _ Invocation, _ model.Action, _ model.Resource, operation string, params, _ map[string]any) (string, error) {
	*f.events = append(*f.events, "audit")
	f.operation = operation
	f.params = params
	return model.NewId(), f.err
}
func (f *jobOperationsAuditorFake) Fail(context.Context, string, string) error {
	*f.events = append(*f.events, "audit-fail")
	f.failCalls++
	return nil
}

func TestJobOperationsAuthorizeBeforeEngineInspection(t *testing.T) {
	t.Parallel()
	events := make([]string, 0, 2)
	view := JobView{ID: model.NewJobID(), Type: model.JobTypeProfilePictureGenerateDefault, Status: model.JobStatusQueued}
	engine := &jobOperatorEngineFake{events: &events, view: view}
	authorizer := &jobOperationsAuthorizerFake{events: &events, resource: model.Resource{Type: model.ResourceInstitution, ID: model.NewId()}}
	service, err := newJobOperationsService(engine, authorizer, &jobOperationsAuditorFake{events: &events}, time.Now)
	if err != nil {
		t.Fatal(err)
	}

	page, appErr := service.List(context.Background(), Invocation{}, ListJobsQuery{Limit: 20})
	if appErr != nil {
		t.Fatal(appErr)
	}
	if len(page.Items) != 1 || page.Items[0].ID != view.ID || engine.listQuery.Limit != 20 || !reflect.DeepEqual(events, []string{"authorize", "list"}) || authorizer.actions[0] != model.ActionJobView {
		t.Fatalf("page=%#v query=%#v events=%v actions=%v", page, engine.listQuery, events, authorizer.actions)
	}

	events = events[:0]
	authorizer.err = errors.New("denied")
	if _, appErr = service.Get(context.Background(), Invocation{}, GetJobQuery{ID: view.ID}); appErr == nil {
		t.Fatal("Get() ignored authorization failure")
	}
	if !reflect.DeepEqual(events, []string{"authorize"}) {
		t.Fatalf("denied events=%v", events)
	}
}

func TestJobOperationsAuditAfterPreparationAndBeforeTransition(t *testing.T) {
	t.Parallel()
	events := make([]string, 0, 4)
	view := JobView{ID: model.NewJobID(), Type: model.JobTypeProfilePictureGenerateDefault, Status: model.JobStatusQueued}
	engine := &jobOperatorEngineFake{events: &events, view: view}
	authorizer := &jobOperationsAuthorizerFake{events: &events, resource: model.Resource{Type: model.ResourceInstitution, ID: model.NewId()}}
	auditor := &jobOperationsAuditorFake{events: &events}
	service, err := newJobOperationsService(engine, authorizer, auditor, time.Now)
	if err != nil {
		t.Fatal(err)
	}

	if _, appErr := service.Cancel(context.Background(), Invocation{}, CancelJobCommand{ID: view.ID}); appErr != nil {
		t.Fatal(appErr)
	}
	if !reflect.DeepEqual(events, []string{"authorize", "prepare-cancel", "audit", "cancel"}) || auditor.operation != "cancel" || engine.applyCalls != 1 {
		t.Fatalf("events=%v operation=%q apply=%d", events, auditor.operation, engine.applyCalls)
	}
	if auditor.params["job_id"] != view.ID.String() || auditor.params["type"] != string(view.Type) || auditor.params["status"] != string(view.Status) {
		t.Fatalf("audit params=%#v", auditor.params)
	}

	events = events[:0]
	auditor.err = errors.New("audit unavailable")
	if _, appErr := service.Retry(context.Background(), Invocation{}, RetryJobCommand{ID: view.ID}); appErr == nil {
		t.Fatal("Retry() ignored audit failure")
	}
	if !reflect.DeepEqual(events, []string{"authorize", "prepare-retry", "audit"}) || engine.applyCalls != 1 {
		t.Fatalf("failed-audit events=%v apply=%d", events, engine.applyCalls)
	}

	events = events[:0]
	auditor.err = nil
	engine.applyErr = errors.New("transition unavailable")
	if _, appErr := service.Retry(context.Background(), Invocation{}, RetryJobCommand{ID: view.ID}); appErr == nil {
		t.Fatal("Retry() ignored transition failure")
	}
	if !reflect.DeepEqual(events, []string{"authorize", "prepare-retry", "audit", "retry", "audit-fail"}) || auditor.failCalls != 1 {
		t.Fatalf("failed-transition events=%v audit failures=%d", events, auditor.failCalls)
	}
}

func TestJobOperationsTranslateEnginePolicyErrors(t *testing.T) {
	t.Parallel()
	events := make([]string, 0, 2)
	engine := &jobOperatorEngineFake{events: &events, prepareErr: jobengine.ErrCancelUnsupported}
	service, err := newJobOperationsService(engine, &jobOperationsAuthorizerFake{events: &events}, &jobOperationsAuditorFake{events: &events}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if _, appErr := service.Cancel(context.Background(), Invocation{}, CancelJobCommand{ID: model.NewJobID()}); !Is(appErr, "job.cancel.unsupported") {
		t.Fatalf("Cancel() error=%#v", appErr)
	}
}

func TestJobOperationsRequireDependencies(t *testing.T) {
	t.Parallel()

	engine := &jobOperatorEngineFake{}
	authorizer := &jobOperationsAuthorizerFake{}
	auditor := &jobOperationsAuditorFake{events: &[]string{}}
	tests := []struct {
		name          string
		jobs          jobOperatorEngine
		authorization jobOperationsAuthorizer
		audit         mutationAuditor
		now           func() time.Time
	}{
		{name: "engine", authorization: authorizer, audit: auditor, now: time.Now},
		{name: "authorization", jobs: engine, audit: auditor, now: time.Now},
		{name: "audit", jobs: engine, authorization: authorizer, now: time.Now},
		{name: "clock", jobs: engine, authorization: authorizer, audit: auditor},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := newJobOperationsService(test.jobs, test.authorization, test.audit, test.now); err == nil {
				t.Fatalf("nil %s dependency was accepted", test.name)
			}
		})
	}
}
