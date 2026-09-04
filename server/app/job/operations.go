// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package job

import (
	"context"
	"errors"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

var (
	ErrQueryInvalid      = errors.New("job query is invalid")
	ErrCancelUnsupported = errors.New("job cancellation is unsupported")
	ErrRetryUnsupported  = errors.New("job retry is unsupported")
)

// View is the canonical safe Job projection. Commands, checkpoints, results,
// deduplication keys, claim data, and internal errors are intentionally absent.
type View struct {
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

// AttemptView is the canonical safe Job Attempt projection. Claim ownership,
// tokens, checkpoints, and internal errors are intentionally absent.
type AttemptView struct {
	ID              model.JobAttemptID
	Number          int
	Status          model.JobAttemptStatus
	StartedAt       time.Time
	HeartbeatAt     time.Time
	LeaseExpiresAt  time.Time
	CompletedAt     model.OptionalTime
	PublicErrorCode string
}

type Page struct{ Items []View }
type AttemptPage struct{ Items []AttemptView }

type ListQuery struct {
	Statuses        []model.JobStatus
	BeforeCreatedAt time.Time
	BeforeID        model.JobID
	Limit           int
}

type AttemptListQuery struct {
	JobID               model.JobID
	BeforeNumber, Limit int
}

type controlKind string

const (
	controlCancel controlKind = "cancel"
	controlRetry  controlKind = "retry"
)

// ControlTarget is an engine-validated transition target. Its transition kind
// and optimistic-concurrency revision cannot be constructed by callers.
type ControlTarget struct {
	Projection View
	id         model.JobID
	revision   int64
	kind       controlKind
}

func (t ControlTarget) View() View { return cloneView(t.Projection) }

// AuditReference identifies the durable audit attempt committed atomically by
// the Job Store transition.
type AuditReference struct {
	EventID string
	At      int64
}

func (r *Engine) List(ctx context.Context, query ListQuery) (Page, error) {
	if query.Limit == 0 {
		query.Limit = 50
	}
	if query.Limit < 1 || query.Limit > 200 || (!query.BeforeID.IsZero() && (!query.BeforeID.IsValid() || query.BeforeCreatedAt.IsZero())) {
		return Page{}, ErrQueryInvalid
	}
	for _, status := range query.Statuses {
		if !publicStatus(status) {
			return Page{}, ErrQueryInvalid
		}
	}
	records, err := r.jobs.List(ctx, store.JobListOptions{Types: r.operatorTypes(), Statuses: query.Statuses, BeforeCreatedAt: query.BeforeCreatedAt, BeforeID: query.BeforeID, Limit: query.Limit})
	if err != nil {
		return Page{}, err
	}
	page := Page{Items: make([]View, 0, len(records))}
	for _, record := range records {
		if record == nil {
			return Page{}, errors.New("job store returned a nil record")
		}
		if r.operatorVisible(record.Type) {
			page.Items = append(page.Items, project(record))
		}
	}
	return page, nil
}

func (r *Engine) Get(ctx context.Context, id model.JobID) (View, error) {
	record, err := r.operatorJob(ctx, id)
	if err != nil {
		return View{}, err
	}
	return project(record), nil
}

func (r *Engine) Attempts(ctx context.Context, query AttemptListQuery) (AttemptPage, error) {
	if _, err := r.operatorJob(ctx, query.JobID); err != nil {
		return AttemptPage{}, err
	}
	if query.Limit == 0 {
		query.Limit = 50
	}
	if query.Limit < 1 || query.Limit > 200 || query.BeforeNumber < 0 {
		return AttemptPage{}, ErrQueryInvalid
	}
	records, err := r.jobs.ListAttemptsPage(ctx, store.JobAttemptListOptions{JobID: query.JobID, BeforeNumber: query.BeforeNumber, Limit: query.Limit})
	if err != nil {
		return AttemptPage{}, err
	}
	page := AttemptPage{Items: make([]AttemptView, 0, len(records))}
	for _, record := range records {
		page.Items = append(page.Items, projectAttempt(record))
	}
	return page, nil
}

func (r *Engine) PrepareCancellation(ctx context.Context, id model.JobID) (ControlTarget, error) {
	record, err := r.operatorJob(ctx, id)
	if err != nil {
		return ControlTarget{}, err
	}
	descriptor, err := r.registry.Descriptor(record.Type)
	if err != nil || !descriptor.Cancelable {
		return ControlTarget{}, ErrCancelUnsupported
	}
	return ControlTarget{Projection: project(record), id: record.ID, revision: record.Revision, kind: controlCancel}, nil
}

func (r *Engine) PrepareRetry(ctx context.Context, id model.JobID) (ControlTarget, error) {
	record, err := r.operatorJob(ctx, id)
	if err != nil {
		return ControlTarget{}, err
	}
	descriptor, err := r.registry.Descriptor(record.Type)
	if err != nil || !descriptor.SupportsExplicitRetry(record.Status) {
		return ControlTarget{}, ErrRetryUnsupported
	}
	return ControlTarget{Projection: project(record), id: record.ID, revision: record.Revision, kind: controlRetry}, nil
}

func (r *Engine) Cancel(ctx context.Context, target ControlTarget, audit AuditReference) (View, error) {
	if target.kind != controlCancel || !validAuditReference(audit) {
		return View{}, ErrCancelUnsupported
	}
	record, err := r.jobs.CancelWithAudit(ctx, &store.JobMutation{ID: target.id, ExpectedRevision: target.revision, AuditEventID: audit.EventID, AuditAt: audit.At})
	if err != nil {
		return View{}, err
	}
	return project(record), nil
}

func (r *Engine) Retry(ctx context.Context, target ControlTarget, audit AuditReference) (View, error) {
	if target.kind != controlRetry || !validAuditReference(audit) {
		return View{}, ErrRetryUnsupported
	}
	record, err := r.jobs.RetryWithAudit(ctx, &store.JobMutation{ID: target.id, ExpectedRevision: target.revision, AuditEventID: audit.EventID, AuditAt: audit.At})
	if err != nil {
		return View{}, err
	}
	return project(record), nil
}

func (r *Engine) operatorJob(ctx context.Context, id model.JobID) (*model.Job, error) {
	record, err := r.jobs.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if record == nil || !r.operatorVisible(record.Type) {
		return nil, store.NewErrNotFound("job", id.String())
	}
	return record, nil
}

func (r *Engine) operatorTypes() []model.JobType {
	result := make([]model.JobType, 0)
	for _, jobType := range r.registry.Types() {
		if r.operatorVisible(jobType) {
			result = append(result, jobType)
		}
	}
	return result
}

func (r *Engine) operatorVisible(jobType model.JobType) bool {
	descriptor, err := r.registry.Descriptor(jobType)
	return err == nil && descriptor.Visibility == VisibilityOperator
}

func project(record *model.Job) View {
	view := View{ID: record.ID, Type: record.Type, Status: record.Status, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt, AvailableAt: record.AvailableAt, StartedAt: record.StartedAt, CompletedAt: record.CompletedAt, PublicErrorCode: record.PublicErrorCode, AttemptCount: record.AttemptCount, MaximumAttempts: record.MaximumAttempts, Revision: record.Revision}
	if record.Progress != nil {
		progress := *record.Progress
		view.Progress = &progress
	}
	return view
}

func cloneView(view View) View {
	if view.Progress != nil {
		progress := *view.Progress
		view.Progress = &progress
	}
	return view
}

func projectAttempt(record model.JobAttempt) AttemptView {
	return AttemptView{ID: record.ID, Number: record.Number, Status: record.Status, StartedAt: record.StartedAt, HeartbeatAt: record.HeartbeatAt, LeaseExpiresAt: record.LeaseExpiresAt, CompletedAt: record.CompletedAt, PublicErrorCode: record.PublicErrorCode}
}

func validAuditReference(audit AuditReference) bool { return audit.EventID != "" && audit.At > 0 }

func publicStatus(status model.JobStatus) bool {
	switch status {
	case model.JobStatusQueued, model.JobStatusRunning, model.JobStatusCancelRequested, model.JobStatusSucceeded, model.JobStatusFailed, model.JobStatusCanceled:
		return true
	default:
		return false
	}
}
