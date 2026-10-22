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
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

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
	List(context.Context, job.ListQuery) (job.Page, error)
	Get(context.Context, model.JobID) (job.View, error)
	Attempts(context.Context, job.AttemptListQuery) (job.AttemptPage, error)
	PrepareCancellation(context.Context, model.JobID) (job.ControlTarget, error)
	PrepareRetry(context.Context, model.JobID) (job.ControlTarget, error)
	Cancel(context.Context, job.ControlTarget, job.AuditReference) (job.View, error)
	Retry(context.Context, job.ControlTarget, job.AuditReference) (job.View, error)
}

// jobOperationsServiceDependencies are the collaborators of jobOperationsService;
// newJobOperationsService validates them once and jobOperationsService keeps them in deps.
type jobOperationsServiceDependencies struct {
	Jobs          jobOperatorEngine
	Authorization institutionAuthorizer
	Audit         mutationAuditor
	Now           func() time.Time
}

type jobOperationsService struct {
	deps jobOperationsServiceDependencies
}

type jobOperationsAuthorization struct {
	authorization *accessControlService
	institutions  store.InstitutionStore
}

func (a jobOperationsAuthorization) AuthorizeInstitution(ctx context.Context, invocation invocation.Invocation, action model.Action) (model.Resource, error) {
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

func newJobOperationsService(deps jobOperationsServiceDependencies) (*jobOperationsService, error) {
	if deps.Jobs == nil {
		return nil, errors.New("job operations engine is required")
	}
	if deps.Authorization == nil {
		return nil, errors.New("job operations authorization is required")
	}
	if deps.Audit == nil {
		return nil, errors.New("job operations audit is required")
	}
	if deps.Now == nil {
		return nil, errors.New("job operations clock is required")
	}
	return &jobOperationsService{deps: deps}, nil
}

func (s *jobOperationsService) ListJobs(ctx context.Context, invocation invocation.Invocation, query ListJobsQuery) (job.Page, error) {
	if s == nil {
		return job.Page{}, apperror.New("job.unavailable")
	}
	if _, err := s.deps.Authorization.AuthorizeInstitution(ctx, invocation, model.ActionJobView); err != nil {
		return job.Page{}, err
	}
	page, err := s.deps.Jobs.List(ctx, job.ListQuery{
		Statuses: query.Statuses, BeforeCreatedAt: query.BeforeCreatedAt,
		BeforeID: query.BeforeID, Limit: query.Limit,
	})
	if err != nil {
		return job.Page{}, jobOperationsStoreErrors.Translate(err)
	}
	return page, nil
}

func (s *jobOperationsService) GetJob(ctx context.Context, invocation invocation.Invocation, query GetJobQuery) (job.View, error) {
	if s == nil {
		return job.View{}, apperror.New("job.unavailable")
	}
	if _, err := s.deps.Authorization.AuthorizeInstitution(ctx, invocation, model.ActionJobView); err != nil {
		return job.View{}, err
	}
	view, err := s.deps.Jobs.Get(ctx, query.ID)
	if err != nil {
		return job.View{}, jobOperationsStoreErrors.Translate(err)
	}
	return view, nil
}

func (s *jobOperationsService) ListJobAttempts(ctx context.Context, invocation invocation.Invocation, query ListJobAttemptsQuery) (job.AttemptPage, error) {
	if s == nil {
		return job.AttemptPage{}, apperror.New("job.unavailable")
	}
	if _, err := s.deps.Authorization.AuthorizeInstitution(ctx, invocation, model.ActionJobView); err != nil {
		return job.AttemptPage{}, err
	}
	page, err := s.deps.Jobs.Attempts(ctx, job.AttemptListQuery{
		JobID: query.JobID, BeforeNumber: query.BeforeNumber, Limit: query.Limit,
	})
	if err != nil {
		return job.AttemptPage{}, jobOperationsStoreErrors.Translate(err)
	}
	return page, nil
}

func (s *jobOperationsService) CancelJob(ctx context.Context, invocation invocation.Invocation, command CancelJobCommand) (job.View, error) {
	if s == nil {
		return job.View{}, apperror.New("job.unavailable")
	}
	return s.mutate(ctx, invocation, "cancel", command.ID, s.deps.Jobs.PrepareCancellation, s.deps.Jobs.Cancel)
}

func (s *jobOperationsService) RetryJob(ctx context.Context, invocation invocation.Invocation, command RetryJobCommand) (job.View, error) {
	if s == nil {
		return job.View{}, apperror.New("job.unavailable")
	}
	return s.mutate(ctx, invocation, "retry", command.ID, s.deps.Jobs.PrepareRetry, s.deps.Jobs.Retry)
}

func (s *jobOperationsService) mutate(ctx context.Context, invocation invocation.Invocation, operation string, id model.JobID, prepare func(context.Context, model.JobID) (job.ControlTarget, error), apply func(context.Context, job.ControlTarget, job.AuditReference) (job.View, error)) (job.View, error) {
	resource, err := s.deps.Authorization.AuthorizeInstitution(ctx, invocation, model.ActionJobManage)
	if err != nil {
		return job.View{}, err
	}
	target, err := prepare(ctx, id)
	if err != nil {
		return job.View{}, jobOperationsStoreErrors.Translate(err)
	}
	view := target.View()
	auditID, err := s.deps.Audit.Begin(ctx, invocation, model.ActionJobManage, resource, operation, map[string]any{"job_id": view.ID.String(), "type": string(view.Type), "status": string(view.Status)}, nil)
	if err != nil {
		return job.View{}, err
	}
	updated, err := apply(ctx, target, job.AuditReference{EventID: auditID, At: s.deps.Now().UnixMilli()})
	if err != nil {
		_ = s.deps.Audit.Fail(ctx, auditID, "job.conflict")
		return job.View{}, jobOperationsStoreErrors.Translate(err)
	}
	return updated, nil
}

var jobOperationsStoreErrors = apperror.StoreTable{
	Rules: []apperror.StoreRule{
		{Kind: apperror.StoreSentinel, Sentinel: job.ErrQueryInvalid, Code: "job.query.invalid"},
		{Kind: apperror.StoreSentinel, Sentinel: job.ErrCancelUnsupported, Code: "job.cancel.unsupported"},
		{Kind: apperror.StoreSentinel, Sentinel: job.ErrRetryUnsupported, Code: "job.retry.unsupported"},
	},
	NotFound:    "resource.not_found",
	Conflict:    "job.conflict",
	Unavailable: "job.unavailable",
}
