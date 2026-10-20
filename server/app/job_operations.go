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
	"time"

	"github.com/sudosylabs/proctor/server/app/apperror"
	jobengine "github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// Stable application-facing names keep transports independent of child-package
// organization while the Job engine owns their canonical safe representation.
type JobView = jobengine.View
type JobAttemptView = jobengine.AttemptView
type JobPage = jobengine.Page
type JobAttemptPage = jobengine.AttemptPage

type ListJobsQuery struct {
	Statuses        []model.JobStatus
	BeforeCreatedAt time.Time
	BeforeID        model.JobID
	Limit           int
}

type GetJobQuery struct{ ID model.JobID }
type ListJobAttemptsQuery struct {
	JobID               model.JobID
	BeforeNumber, Limit int
}
type CancelJobCommand struct{ ID model.JobID }
type RetryJobCommand struct{ ID model.JobID }

type jobOperatorEngine interface {
	List(context.Context, jobengine.ListQuery) (jobengine.Page, error)
	Get(context.Context, model.JobID) (jobengine.View, error)
	Attempts(context.Context, jobengine.AttemptListQuery) (jobengine.AttemptPage, error)
	PrepareCancellation(context.Context, model.JobID) (jobengine.ControlTarget, error)
	PrepareRetry(context.Context, model.JobID) (jobengine.ControlTarget, error)
	Cancel(context.Context, jobengine.ControlTarget, jobengine.AuditReference) (jobengine.View, error)
	Retry(context.Context, jobengine.ControlTarget, jobengine.AuditReference) (jobengine.View, error)
}

type jobOperationsService struct {
	jobs          jobOperatorEngine
	authorization institutionAuthorizer
	audit         mutationAuditor
	now           func() time.Time
}

type jobOperationsAuthorization struct {
	authorization *accessControlService
	institutions  store.InstitutionStore
}

func (a jobOperationsAuthorization) AuthorizeInstitution(ctx context.Context, invocation Invocation, action model.Action) (model.Resource, error) {
	institution, err := a.institutions.GetSingleton(ctx)
	if err != nil {
		return model.Resource{}, jobOperationsStoreErrors.Translate(err)
	}
	resource := model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()}
	if err = a.authorization.Authorize(ctx, invocation, action, resource); err != nil {
		return model.Resource{}, err
	}
	return resource, nil
}

func (a *App) jobOperationsService() (*jobOperationsService, error) {
	if a == nil || a.jobOperations == nil {
		return nil, apperror.New("job.unavailable")
	}
	return a.jobOperations, nil
}

func (a *App) ListJobs(ctx context.Context, invocation Invocation, query ListJobsQuery) (JobPage, error) {
	service, err := a.jobOperationsService()
	if err != nil {
		return JobPage{}, err
	}
	return service.List(ctx, invocation, query)
}

func (a *App) GetJob(ctx context.Context, invocation Invocation, query GetJobQuery) (JobView, error) {
	service, err := a.jobOperationsService()
	if err != nil {
		return JobView{}, err
	}
	return service.Get(ctx, invocation, query)
}

func (a *App) ListJobAttempts(ctx context.Context, invocation Invocation, query ListJobAttemptsQuery) (JobAttemptPage, error) {
	service, err := a.jobOperationsService()
	if err != nil {
		return JobAttemptPage{}, err
	}
	return service.Attempts(ctx, invocation, query)
}

func (a *App) CancelJob(ctx context.Context, invocation Invocation, command CancelJobCommand) (JobView, error) {
	service, err := a.jobOperationsService()
	if err != nil {
		return JobView{}, err
	}
	return service.Cancel(ctx, invocation, command)
}

func (a *App) RetryJob(ctx context.Context, invocation Invocation, command RetryJobCommand) (JobView, error) {
	service, err := a.jobOperationsService()
	if err != nil {
		return JobView{}, err
	}
	return service.Retry(ctx, invocation, command)
}

func newJobOperationsService(jobs jobOperatorEngine, authorization institutionAuthorizer, audit mutationAuditor, now func() time.Time) (*jobOperationsService, error) {
	if jobs == nil {
		return nil, errors.New("job operations engine is required")
	}
	if authorization == nil {
		return nil, errors.New("job operations authorization is required")
	}
	if audit == nil {
		return nil, errors.New("job operations audit is required")
	}
	if now == nil {
		return nil, errors.New("job operations clock is required")
	}
	return &jobOperationsService{jobs: jobs, authorization: authorization, audit: audit, now: now}, nil
}

func (s *jobOperationsService) List(ctx context.Context, invocation Invocation, query ListJobsQuery) (JobPage, error) {
	if _, err := s.authorization.AuthorizeInstitution(ctx, invocation, model.ActionJobView); err != nil {
		return JobPage{}, err
	}
	page, err := s.jobs.List(ctx, jobengine.ListQuery{
		Statuses: query.Statuses, BeforeCreatedAt: query.BeforeCreatedAt,
		BeforeID: query.BeforeID, Limit: query.Limit,
	})
	if err != nil {
		return JobPage{}, jobOperationsStoreErrors.Translate(err)
	}
	return page, nil
}

func (s *jobOperationsService) Get(ctx context.Context, invocation Invocation, query GetJobQuery) (JobView, error) {
	if _, err := s.authorization.AuthorizeInstitution(ctx, invocation, model.ActionJobView); err != nil {
		return JobView{}, err
	}
	view, err := s.jobs.Get(ctx, query.ID)
	if err != nil {
		return JobView{}, jobOperationsStoreErrors.Translate(err)
	}
	return view, nil
}

func (s *jobOperationsService) Attempts(ctx context.Context, invocation Invocation, query ListJobAttemptsQuery) (JobAttemptPage, error) {
	if _, err := s.authorization.AuthorizeInstitution(ctx, invocation, model.ActionJobView); err != nil {
		return JobAttemptPage{}, err
	}
	page, err := s.jobs.Attempts(ctx, jobengine.AttemptListQuery{
		JobID: query.JobID, BeforeNumber: query.BeforeNumber, Limit: query.Limit,
	})
	if err != nil {
		return JobAttemptPage{}, jobOperationsStoreErrors.Translate(err)
	}
	return page, nil
}

func (s *jobOperationsService) Cancel(ctx context.Context, invocation Invocation, command CancelJobCommand) (JobView, error) {
	return s.mutate(ctx, invocation, "cancel", command.ID, s.jobs.PrepareCancellation, s.jobs.Cancel)
}

func (s *jobOperationsService) Retry(ctx context.Context, invocation Invocation, command RetryJobCommand) (JobView, error) {
	return s.mutate(ctx, invocation, "retry", command.ID, s.jobs.PrepareRetry, s.jobs.Retry)
}

func (s *jobOperationsService) mutate(ctx context.Context, invocation Invocation, operation string, id model.JobID, prepare func(context.Context, model.JobID) (jobengine.ControlTarget, error), apply func(context.Context, jobengine.ControlTarget, jobengine.AuditReference) (jobengine.View, error)) (JobView, error) {
	resource, err := s.authorization.AuthorizeInstitution(ctx, invocation, model.ActionJobManage)
	if err != nil {
		return JobView{}, err
	}
	target, err := prepare(ctx, id)
	if err != nil {
		return JobView{}, jobOperationsStoreErrors.Translate(err)
	}
	view := target.View()
	auditID, err := s.audit.Begin(ctx, invocation, model.ActionJobManage, resource, operation, map[string]any{"job_id": view.ID.String(), "type": string(view.Type), "status": string(view.Status)}, nil)
	if err != nil {
		return JobView{}, err
	}
	updated, err := apply(ctx, target, jobengine.AuditReference{EventID: auditID, At: s.now().UnixMilli()})
	if err != nil {
		_ = s.audit.Fail(ctx, auditID, "job.conflict")
		return JobView{}, jobOperationsStoreErrors.Translate(err)
	}
	return updated, nil
}

var jobOperationsStoreErrors = apperror.StoreTable{
	Rules: []apperror.StoreRule{
		{Kind: apperror.StoreSentinel, Sentinel: jobengine.ErrQueryInvalid, Code: "job.query.invalid"},
		{Kind: apperror.StoreSentinel, Sentinel: jobengine.ErrCancelUnsupported, Code: "job.cancel.unsupported"},
		{Kind: apperror.StoreSentinel, Sentinel: jobengine.ErrRetryUnsupported, Code: "job.retry.unsupported"},
	},
	NotFound:    "resource.not_found",
	Conflict:    "job.conflict",
	Unavailable: "job.unavailable",
}
