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
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/model"
)

type jobOperatorEngineFake struct {
	events     *[]string
	view       job.View
	prepareErr error
	applyErr   error
	listQuery  job.ListQuery
	applyCalls int
}

func (f *jobOperatorEngineFake) List(_ context.Context, query job.ListQuery) (job.Page, error) {
	*f.events = append(*f.events, "list")
	f.listQuery = query
	return job.Page{Items: []job.View{f.view}}, nil
}
func (f *jobOperatorEngineFake) Get(context.Context, model.JobID) (job.View, error) {
	*f.events = append(*f.events, "get")
	return f.view, nil
}
func (f *jobOperatorEngineFake) Attempts(context.Context, job.AttemptListQuery) (job.AttemptPage, error) {
	*f.events = append(*f.events, "attempts")
	return job.AttemptPage{Items: make([]job.AttemptView, 0)}, nil
}
func (f *jobOperatorEngineFake) PrepareCancellation(context.Context, model.JobID) (job.ControlTarget, error) {
	*f.events = append(*f.events, "prepare-cancel")
	return job.ControlTarget{Projection: f.view}, f.prepareErr
}
func (f *jobOperatorEngineFake) PrepareRetry(context.Context, model.JobID) (job.ControlTarget, error) {
	*f.events = append(*f.events, "prepare-retry")
	return job.ControlTarget{Projection: f.view}, f.prepareErr
}
func (f *jobOperatorEngineFake) Cancel(context.Context, job.ControlTarget, job.AuditReference) (job.View, error) {
	*f.events = append(*f.events, "cancel")
	f.applyCalls++
	return f.view, f.applyErr
}
func (f *jobOperatorEngineFake) Retry(context.Context, job.ControlTarget, job.AuditReference) (job.View, error) {
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

func (f *jobOperationsAuthorizerFake) AuthorizeInstitution(_ context.Context, _ invocation.Invocation, action model.Action) (model.Resource, error) {
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

func (f *jobOperationsAuditorFake) Begin(_ context.Context, _ invocation.Invocation, _ model.Action, _ model.Resource, operation string, params, _ map[string]any) (model.AuditEventID, error) {
	*f.events = append(*f.events, "audit")
	f.operation = operation
	f.params = params
	return model.NewAuditEventID(), f.err
}
func (f *jobOperationsAuditorFake) Fail(context.Context, model.AuditEventID, string) error {
	*f.events = append(*f.events, "audit-fail")
	f.failCalls++
	return nil
}

func TestJobOperationsAuthorizeBeforeEngineInspection(t *testing.T) {
	t.Parallel()
	events := make([]string, 0, 2)
	view := job.View{ID: model.NewJobID(), Type: model.JobTypeProfilePictureGenerateDefault, Status: model.JobStatusQueued}
	engine := &jobOperatorEngineFake{events: &events, view: view}
	authorizer := &jobOperationsAuthorizerFake{events: &events, resource: model.Resource{Type: model.ResourceInstitution, ID: model.NewID()}}
	service, err := newJobOperationsService(jobOperationsServiceDependencies{Jobs: engine, Authorization: authorizer, Audit: &jobOperationsAuditorFake{events: &events}, Now: time.Now})
	require.NoError(t, err)

	page, appErr := service.ListJobs(context.Background(), invocation.Invocation{}, ListJobsQuery{Limit: 20})
	require.NoError(t, appErr)
	require.Len(t, page.Items, 1, "page=%#v query=%#v events=%v actions=%v", page, engine.listQuery, events, authorizer.actions)
	require.Equal(t, view.ID, page.Items[0].ID, "page=%#v query=%#v events=%v actions=%v", page, engine.listQuery, events, authorizer.actions)
	require.Equal(t, 20, engine.listQuery.Limit, "page=%#v query=%#v events=%v actions=%v", page, engine.listQuery, events, authorizer.actions)
	// Contract: authorization precedes the read.
	require.Equal(t, []string{"authorize", "list"}, events)
	require.Equal(t, model.ActionJobView, authorizer.actions[0], "page=%#v query=%#v events=%v actions=%v", page, engine.listQuery, events, authorizer.actions)

	events = events[:0]
	authorizer.err = errors.New("denied")
	_, appErr = service.GetJob(context.Background(), invocation.Invocation{}, GetJobQuery{ID: view.ID})
	require.Error(t, appErr, "Get() ignored authorization failure")
	require.Equal(t, []string{"authorize"}, events)
}

func TestJobOperationsAuditAfterPreparationAndBeforeTransition(t *testing.T) {
	t.Parallel()
	events := make([]string, 0, 4)
	view := job.View{ID: model.NewJobID(), Type: model.JobTypeProfilePictureGenerateDefault, Status: model.JobStatusQueued}
	engine := &jobOperatorEngineFake{events: &events, view: view}
	authorizer := &jobOperationsAuthorizerFake{events: &events, resource: model.Resource{Type: model.ResourceInstitution, ID: model.NewID()}}
	auditor := &jobOperationsAuditorFake{events: &events}
	service, err := newJobOperationsService(jobOperationsServiceDependencies{Jobs: engine, Authorization: authorizer, Audit: auditor, Now: time.Now})
	require.NoError(t, err)

	_, appErr := service.CancelJob(context.Background(), invocation.Invocation{}, CancelJobCommand{ID: view.ID})
	require.NoError(t, appErr)
	// Contract (guardrail 8): authorization precedes the audit, and the audit
	// begins before the durable write.
	require.Equal(t, []string{"authorize", "prepare-cancel", "audit", "cancel"}, events)
	require.Equal(t, "cancel", auditor.operation, "events")
	require.Equal(t, 1, engine.applyCalls, "events")
	require.Equal(t, view.ID.String(), auditor.params["job_id"])
	require.Equal(t, string(view.Type), auditor.params["type"])
	require.Equal(t, string(view.Status), auditor.params["status"])

	events = events[:0]
	auditor.err = errors.New("audit unavailable")
	_, appErr = service.RetryJob(context.Background(), invocation.Invocation{}, RetryJobCommand{ID: view.ID})
	require.Error(t, appErr, "Retry() ignored audit failure")
	// Contract (guardrail 8): authorization precedes the audit, and the audit
	// begins before the durable write.
	require.Equal(t, []string{"authorize", "prepare-retry", "audit"}, events)
	require.Equal(t, 1, engine.applyCalls, "failed-audit events")

	events = events[:0]
	auditor.err = nil
	engine.applyErr = errors.New("transition unavailable")
	_, appErr = service.RetryJob(context.Background(), invocation.Invocation{}, RetryJobCommand{ID: view.ID})
	require.Error(t, appErr, "Retry() ignored transition failure")
	// Contract (guardrail 8): authorization precedes the audit, the audit begins
	// before the durable write, and the audit outcome follows the write.
	require.Equal(t, []string{"authorize", "prepare-retry", "audit", "retry", "audit-fail"}, events)
	require.Equal(t, 1, auditor.failCalls, "failed-transition events")
}

func TestJobOperationsTranslateEnginePolicyErrors(t *testing.T) {
	t.Parallel()
	events := make([]string, 0, 2)
	engine := &jobOperatorEngineFake{events: &events, prepareErr: job.ErrCancelUnsupported}
	service, err := newJobOperationsService(jobOperationsServiceDependencies{Jobs: engine, Authorization: &jobOperationsAuthorizerFake{events: &events}, Audit: &jobOperationsAuditorFake{events: &events}, Now: time.Now})
	require.NoError(t, err)
	_, appErr := service.CancelJob(context.Background(), invocation.Invocation{}, CancelJobCommand{ID: model.NewJobID()})
	require.True(t, apperror.Is(appErr, "job.cancel.unsupported"), "Cancel() error=%#v", appErr)
}

func TestJobOperationsRequireDependencies(t *testing.T) {
	t.Parallel()

	engine := &jobOperatorEngineFake{}
	authorizer := &jobOperationsAuthorizerFake{}
	auditor := &jobOperationsAuditorFake{events: &[]string{}}
	tests := []struct {
		name          string
		jobs          jobOperatorEngine
		authorization institutionAuthorizer
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
			_, err := newJobOperationsService(jobOperationsServiceDependencies{Jobs: test.jobs, Authorization: test.authorization, Audit: test.audit, Now: test.now})
			require.Error(t, err, "nil %s dependency was accepted", test.name)
		})
	}
}
