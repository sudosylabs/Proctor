// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"time"

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

type jobOperationsAuthorizer interface {
	Authorize(context.Context, Invocation, model.Action) (model.Resource, error)
}

type jobOperationsService struct {
	jobs          jobOperatorEngine
	authorization jobOperationsAuthorizer
	audit         mutationAuditor
	now           func() time.Time
}

type jobOperationsAuthorization struct {
	authorization *AuthorizationService
	institutions  store.InstitutionStore
}

func (a jobOperationsAuthorization) Authorize(ctx context.Context, invocation Invocation, action model.Action) (model.Resource, error) {
	institution, err := a.institutions.GetSingleton(ctx)
	if err != nil {
		return model.Resource{}, jobOperationsError(err)
	}
	resource := model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()}
	if err = a.authorization.authorizeCurrentState(ctx, invocation.Principal(), action, resource, invocation.RequestMetadata()); err != nil {
		return model.Resource{}, err
	}
	return resource, nil
}

func (a *App) jobOperationsService() (*jobOperationsService, error) {
	if a == nil || a.jobs == nil {
		return nil, NewError("job.unavailable")
	}
	return newJobOperationsService(a.jobs, jobOperationsAuthorization{authorization: a.authorization, institutions: a.store.Institution()}, mutationAuditAdapter{audit: a.audit}, time.Now), nil
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

func newJobOperationsService(jobs jobOperatorEngine, authorization jobOperationsAuthorizer, audit mutationAuditor, now func() time.Time) *jobOperationsService {
	return &jobOperationsService{jobs: jobs, authorization: authorization, audit: audit, now: now}
}

func (s *jobOperationsService) List(ctx context.Context, invocation Invocation, query ListJobsQuery) (JobPage, error) {
	if _, err := s.authorization.Authorize(ctx, invocation, model.ActionJobView); err != nil {
		return JobPage{}, err
	}
	page, err := s.jobs.List(ctx, jobengine.ListQuery{
		Statuses: query.Statuses, BeforeCreatedAt: query.BeforeCreatedAt,
		BeforeID: query.BeforeID, Limit: query.Limit,
	})
	if err != nil {
		return JobPage{}, jobOperationsError(err)
	}
	return page, nil
}

func (s *jobOperationsService) Get(ctx context.Context, invocation Invocation, query GetJobQuery) (JobView, error) {
	if _, err := s.authorization.Authorize(ctx, invocation, model.ActionJobView); err != nil {
		return JobView{}, err
	}
	view, err := s.jobs.Get(ctx, query.ID)
	if err != nil {
		return JobView{}, jobOperationsError(err)
	}
	return view, nil
}

func (s *jobOperationsService) Attempts(ctx context.Context, invocation Invocation, query ListJobAttemptsQuery) (JobAttemptPage, error) {
	if _, err := s.authorization.Authorize(ctx, invocation, model.ActionJobView); err != nil {
		return JobAttemptPage{}, err
	}
	page, err := s.jobs.Attempts(ctx, jobengine.AttemptListQuery{
		JobID: query.JobID, BeforeNumber: query.BeforeNumber, Limit: query.Limit,
	})
	if err != nil {
		return JobAttemptPage{}, jobOperationsError(err)
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
	resource, err := s.authorization.Authorize(ctx, invocation, model.ActionJobManage)
	if err != nil {
		return JobView{}, err
	}
	target, err := prepare(ctx, id)
	if err != nil {
		return JobView{}, jobOperationsError(err)
	}
	view := target.View()
	auditID, err := s.audit.Begin(ctx, invocation, model.ActionJobManage, resource, operation, map[string]any{"job_id": view.ID.String(), "type": string(view.Type), "status": string(view.Status)}, nil)
	if err != nil {
		return JobView{}, err
	}
	updated, err := apply(ctx, target, jobengine.AuditReference{EventID: auditID, At: s.now().UnixMilli()})
	if err != nil {
		_ = s.audit.Fail(ctx, auditID, "job.conflict")
		return JobView{}, jobOperationsError(err)
	}
	return updated, nil
}

func jobOperationsError(err error) error {
	if err == nil {
		return NewError("resource.not_found")
	}
	switch {
	case errors.Is(err, jobengine.ErrQueryInvalid):
		return NewError("job.query.invalid").Wrap(err)
	case errors.Is(err, jobengine.ErrCancelUnsupported):
		return NewError("job.cancel.unsupported").Wrap(err)
	case errors.Is(err, jobengine.ErrRetryUnsupported):
		return NewError("job.retry.unsupported").Wrap(err)
	case store.IsNotFound(err):
		return NewError("resource.not_found").Wrap(err)
	case store.IsConflict(err):
		return NewError("job.conflict").Wrap(err)
	default:
		return NewError("job.unavailable").Wrap(err)
	}
}
