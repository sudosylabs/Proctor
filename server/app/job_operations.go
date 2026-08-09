// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// JobView is the deliberately safe operator projection. Typed commands,
// checkpoints, results, deduplication keys, claim tokens, and storage details
// never cross this application boundary.
type JobView struct {
	ID              model.JobID
	Type            model.JobType
	Status          model.JobStatus
	CreatedAt       time.Time
	UpdatedAt       time.Time
	AvailableAt     time.Time
	StartedAt       model.OptionalTime
	CompletedAt     model.OptionalTime
	PublicErrorCode string
	AttemptCount    int
	MaximumAttempts int
	Progress        *model.JobProgress
	Revision        int64
}

type JobAttemptView struct {
	ID              model.JobAttemptID
	Number          int
	Status          model.JobAttemptStatus
	StartedAt       time.Time
	HeartbeatAt     time.Time
	LeaseExpiresAt  time.Time
	CompletedAt     model.OptionalTime
	PublicErrorCode string
}

type JobPage struct{ Items []JobView }
type JobAttemptPage struct{ Items []JobAttemptView }

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

type jobOperationsStore interface {
	Get(context.Context, model.JobID) (*model.Job, error)
	List(context.Context, store.JobListOptions) ([]*model.Job, error)
	ListAttemptsPage(context.Context, store.JobAttemptListOptions) ([]model.JobAttempt, error)
	CancelWithAudit(context.Context, *store.JobMutation) (*model.Job, error)
	RetryWithAudit(context.Context, *store.JobMutation) (*model.Job, error)
}

type jobOperationsAuthorizer interface {
	Authorize(context.Context, Invocation, model.Action) (model.Resource, error)
}

type jobOperationsService struct {
	jobs          jobOperationsStore
	authorization jobOperationsAuthorizer
	audit         mutationAuditor
	registry      *JobRegistry
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
	return newJobOperationsService(a.store.Job(), jobOperationsAuthorization{authorization: a.authorization, institutions: a.store.Institution()}, mutationAuditAdapter{audit: a.audit}, a.jobs.registry, time.Now), nil
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

func newJobOperationsService(jobs jobOperationsStore, authorization jobOperationsAuthorizer, audit mutationAuditor, registry *JobRegistry, now func() time.Time) *jobOperationsService {
	return &jobOperationsService{jobs: jobs, authorization: authorization, audit: audit, registry: registry, now: now}
}

func (s *jobOperationsService) List(ctx context.Context, invocation Invocation, query ListJobsQuery) (JobPage, error) {
	if _, err := s.authorization.Authorize(ctx, invocation, model.ActionJobView); err != nil {
		return JobPage{}, err
	}
	if query.Limit == 0 {
		query.Limit = 50
	}
	if query.Limit < 1 || query.Limit > 200 || (!query.BeforeID.IsZero() && (!query.BeforeID.IsValid() || query.BeforeCreatedAt.IsZero())) {
		return JobPage{}, NewError("job.query.invalid")
	}
	for _, status := range query.Statuses {
		if !isPublicJobStatus(status) {
			return JobPage{}, NewError("job.query.invalid")
		}
	}
	types := s.operatorTypes()
	jobs, err := s.jobs.List(ctx, store.JobListOptions{Types: types, Statuses: query.Statuses, BeforeCreatedAt: query.BeforeCreatedAt, BeforeID: query.BeforeID, Limit: query.Limit})
	if err != nil {
		return JobPage{}, jobOperationsError(err)
	}
	page := JobPage{Items: make([]JobView, 0, len(jobs))}
	for _, job := range jobs {
		if s.operatorVisible(job.Type) {
			page.Items = append(page.Items, jobViewFromModel(job))
		}
	}
	return page, nil
}

func (s *jobOperationsService) Get(ctx context.Context, invocation Invocation, query GetJobQuery) (JobView, error) {
	if _, err := s.authorization.Authorize(ctx, invocation, model.ActionJobView); err != nil {
		return JobView{}, err
	}
	job, err := s.jobs.Get(ctx, query.ID)
	if err != nil {
		return JobView{}, jobOperationsError(err)
	}
	if !s.operatorVisible(job.Type) {
		return JobView{}, NewError("resource.not_found")
	}
	return jobViewFromModel(job), nil
}

func (s *jobOperationsService) Attempts(ctx context.Context, invocation Invocation, query ListJobAttemptsQuery) (JobAttemptPage, error) {
	if _, err := s.authorization.Authorize(ctx, invocation, model.ActionJobView); err != nil {
		return JobAttemptPage{}, err
	}
	job, err := s.jobs.Get(ctx, query.JobID)
	if err != nil {
		return JobAttemptPage{}, jobOperationsError(err)
	}
	if !s.operatorVisible(job.Type) {
		return JobAttemptPage{}, NewError("resource.not_found")
	}
	if query.Limit == 0 {
		query.Limit = 50
	}
	if query.Limit < 1 || query.Limit > 200 || query.BeforeNumber < 0 {
		return JobAttemptPage{}, NewError("job.query.invalid")
	}
	attempts, err := s.jobs.ListAttemptsPage(ctx, store.JobAttemptListOptions{JobID: query.JobID, BeforeNumber: query.BeforeNumber, Limit: query.Limit})
	if err != nil {
		return JobAttemptPage{}, jobOperationsError(err)
	}
	page := JobAttemptPage{Items: make([]JobAttemptView, 0, len(attempts))}
	for _, attempt := range attempts {
		page.Items = append(page.Items, jobAttemptViewFromModel(attempt))
	}
	return page, nil
}

func (s *jobOperationsService) Cancel(ctx context.Context, invocation Invocation, command CancelJobCommand) (JobView, error) {
	resource, err := s.authorization.Authorize(ctx, invocation, model.ActionJobManage)
	if err != nil {
		return JobView{}, err
	}
	job, err := s.jobs.Get(ctx, command.ID)
	if err != nil {
		return JobView{}, jobOperationsError(err)
	}
	descriptor, err := s.registry.Descriptor(job.Type)
	if err != nil || descriptor.Visibility != JobVisibilityOperator {
		return JobView{}, NewError("resource.not_found")
	}
	if !descriptor.Cancelable {
		return JobView{}, NewError("job.cancel.unsupported")
	}
	return s.mutate(ctx, invocation, resource, job, "cancel", s.jobs.CancelWithAudit)
}

func (s *jobOperationsService) Retry(ctx context.Context, invocation Invocation, command RetryJobCommand) (JobView, error) {
	resource, err := s.authorization.Authorize(ctx, invocation, model.ActionJobManage)
	if err != nil {
		return JobView{}, err
	}
	job, err := s.jobs.Get(ctx, command.ID)
	if err != nil {
		return JobView{}, jobOperationsError(err)
	}
	descriptor, err := s.registry.Descriptor(job.Type)
	if err != nil || descriptor.Visibility != JobVisibilityOperator {
		return JobView{}, NewError("resource.not_found")
	}
	if !descriptor.SupportsExplicitRetry(job.Status) {
		return JobView{}, NewError("job.retry.unsupported")
	}
	return s.mutate(ctx, invocation, resource, job, "retry", s.jobs.RetryWithAudit)
}

func (s *jobOperationsService) mutate(ctx context.Context, invocation Invocation, resource model.Resource, job *model.Job, operation string, apply func(context.Context, *store.JobMutation) (*model.Job, error)) (JobView, error) {
	auditID, err := s.audit.Begin(ctx, invocation, model.ActionJobManage, resource, operation, map[string]any{"job_id": job.ID.String(), "type": string(job.Type), "status": string(job.Status)}, nil)
	if err != nil {
		return JobView{}, err
	}
	updated, err := apply(ctx, &store.JobMutation{ID: job.ID, ExpectedRevision: job.Revision, AuditEventID: auditID, AuditAt: s.now().UnixMilli()})
	if err != nil {
		_ = s.audit.Fail(ctx, auditID, "job.conflict")
		return JobView{}, jobOperationsError(err)
	}
	return jobViewFromModel(updated), nil
}

func (s *jobOperationsService) operatorTypes() []model.JobType {
	result := make([]model.JobType, 0)
	for _, jobType := range s.registry.Types() {
		if s.operatorVisible(jobType) {
			result = append(result, jobType)
		}
	}
	return result
}
func (s *jobOperationsService) operatorVisible(jobType model.JobType) bool {
	descriptor, err := s.registry.Descriptor(jobType)
	return err == nil && descriptor.Visibility == JobVisibilityOperator
}

func jobViewFromModel(job *model.Job) JobView {
	view := JobView{ID: job.ID, Type: job.Type, Status: job.Status, CreatedAt: job.CreatedAt, UpdatedAt: job.UpdatedAt, AvailableAt: job.AvailableAt, StartedAt: job.StartedAt, CompletedAt: job.CompletedAt, PublicErrorCode: job.PublicErrorCode, AttemptCount: job.AttemptCount, MaximumAttempts: job.MaximumAttempts, Revision: job.Revision}
	if job.Progress != nil {
		progress := *job.Progress
		view.Progress = &progress
	}
	return view
}
func jobAttemptViewFromModel(attempt model.JobAttempt) JobAttemptView {
	return JobAttemptView{ID: attempt.ID, Number: attempt.Number, Status: attempt.Status, StartedAt: attempt.StartedAt, HeartbeatAt: attempt.HeartbeatAt, LeaseExpiresAt: attempt.LeaseExpiresAt, CompletedAt: attempt.CompletedAt, PublicErrorCode: attempt.PublicErrorCode}
}

func jobOperationsError(err error) error {
	if err == nil {
		return NewError("resource.not_found")
	}
	if store.IsNotFound(err) {
		return NewError("resource.not_found").Wrap(err)
	}
	if store.IsConflict(err) {
		return NewError("job.conflict").Wrap(err)
	}
	return NewError("job.unavailable").Wrap(err)
}

func isPublicJobStatus(status model.JobStatus) bool {
	switch status {
	case model.JobStatusQueued, model.JobStatusRunning, model.JobStatusCancelRequested, model.JobStatusSucceeded, model.JobStatusFailed, model.JobStatusCanceled:
		return true
	default:
		return false
	}
}
