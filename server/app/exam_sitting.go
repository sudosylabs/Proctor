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

	examsitting "github.com/sudosylabs/proctor/server/app/exam/sitting"
	appinvocation "github.com/sudosylabs/proctor/server/app/invocation"
	apprealtime "github.com/sudosylabs/proctor/server/app/realtime"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// ExamSittingView is the bounded projection of one Exam Sitting. It carries no
// Class roster, authored Exam Revision content or private cancellation
// rationale.
type ExamSittingView = store.ExamSittingSnapshot

// ExamSittingPage is one page of Exam Sitting views.
type ExamSittingPage = examsitting.Page

// ExamSittingNoShowPage is one page of an Exam Sitting's enrolled candidates
// who never started an Exam Attempt.
type ExamSittingNoShowPage = examsitting.NoShowPage

// ListExamSittingNoShowsQuery pages the enrolled candidates of one Exam
// Sitting who never started an Exam Attempt.
type ListExamSittingNoShowsQuery = examsitting.ListNoShowsQuery

// ScheduleExamSittingCommand schedules an Exam Sitting delivering one Exam
// Revision to one Class in a time window; IdempotencyKey makes it replayable.
type ScheduleExamSittingCommand = examsitting.ScheduleCommand

// GetExamSittingQuery names one Exam Sitting of an Exam.
type GetExamSittingQuery = examsitting.GetQuery

// ListExamSittingsQuery pages an Exam's Sittings, optionally narrowed by
// Class, state and schedule overlap.
type ListExamSittingsQuery = examsitting.ListQuery

// UpdateExamSittingScheduleCommand changes an Exam Sitting's Exam Revision,
// Class or schedule; nil fields are left unchanged. It is fenced by
// ExpectedRevision and replayed by IdempotencyKey.
type UpdateExamSittingScheduleCommand = examsitting.UpdateScheduleCommand

// CancelExamSittingCommand cancels an Exam Sitting, fenced by ExpectedRevision
// and replayed by IdempotencyKey; PrivateReason is never shown to candidates.
type CancelExamSittingCommand = examsitting.CancelCommand

// PauseExamSittingCommand pauses a running Exam Sitting, fenced by
// ExpectedRevision and replayed by IdempotencyKey.
type PauseExamSittingCommand = examsitting.PauseCommand

// ResumeExamSittingCommand resumes a paused Exam Sitting, fenced by
// ExpectedRevision and replayed by IdempotencyKey.
type ResumeExamSittingCommand = examsitting.ResumeCommand

// CloseExamSittingCommand closes an Exam Sitting before its scheduled end,
// fenced by ExpectedRevision and replayed by IdempotencyKey.
type CloseExamSittingCommand = examsitting.EarlyCloseCommand

// ExtendExamSittingCommand moves an Exam Sitting's scheduled end to
// ScheduledEndAt, fenced by ExpectedRevision and replayed by IdempotencyKey.
type ExtendExamSittingCommand = examsitting.ExtendCommand

// examSittingLifecycle is the Sitting lifecycle work the lifecycle Job
// drives; its tests substitute a fake.
type examSittingLifecycle interface {
	AdvanceDue(context.Context, appinvocation.System, model.ExamSittingID) (store.ExamSittingLifecycleResult, error)
	ListLifecycleDue(context.Context, store.ExamSittingLifecycleDueOptions) ([]store.ExamSittingLifecycleDue, error)
}

type examSittingLifecycleJobUseCases struct{ sittings examSittingLifecycle }

func (uc examSittingLifecycleJobUseCases) ReconcileExamSittingLifecycleFromJob(ctx context.Context, sittingID model.ExamSittingID, jobID model.JobID,
	attemptID model.JobAttemptID,
) (*store.ExamSittingLifecycleResult, error) {
	call := appinvocation.System{JobID: jobID, AttemptID: attemptID}
	result, err := uc.sittings.AdvanceDue(ctx, call, sittingID)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (uc examSittingLifecycleJobUseCases) ListExamSittingLifecycleDueFromJob(ctx context.Context, options store.ExamSittingLifecycleDueOptions) ([]store.ExamSittingLifecycleDue, error) {
	return uc.sittings.ListLifecycleDue(ctx, options)
}

type examSittingSystemAuditAdapter struct{ audit *auditService }

func (a examSittingSystemAuditAdapter) Begin(ctx context.Context, call appinvocation.System, action model.Action, resource model.Resource, scopeType model.RoleScopeType, scopeID, operation string, value map[string]any) (model.AuditEventID, error) {
	auditValue := make(map[string]any, len(value)+2)
	for key, item := range value {
		auditValue[key] = item
	}
	auditValue["job_id"] = call.JobID.String()
	auditValue["job_attempt_id"] = call.AttemptID.String()
	event, err := a.audit.BeginSystemCriticalActionAtScope(ctx, action, resource, scopeType, scopeID,
		map[string]any{"operation": operation, "value": auditValue})
	if err != nil {
		return "", err
	}
	return event.ID, nil
}

func (a examSittingSystemAuditAdapter) Fail(ctx context.Context, id model.AuditEventID, code string) error {
	_, err := a.audit.CompleteCriticalAction(ctx, id, model.AuditStatusFail, code, nil)
	return err
}

type examSittingRealtimeEffects struct {
	realtime    *realtimeService
	collections examCollectionInvalidationEffects
}

func (e examSittingRealtimeEffects) publishBoardInvalidation(ctx context.Context, examID model.ExamID,
	sittingID model.ExamSittingID,
) error {
	return e.collections.SittingBoardChanged(ctx, examID, sittingID)
}

func (e examSittingRealtimeEffects) Scheduled(ctx context.Context, examID model.ExamID, sittingID model.ExamSittingID,
	state model.ExamSittingState, revision int64, at time.Time,
) error {
	event, err := apprealtime.NewExamSittingScheduledEvent(examID, sittingID, state, revision, at)
	if err != nil {
		return err
	}
	return errors.Join(
		e.realtime.PublishRealtimeEvent(ctx, event),
		e.publishBoardInvalidation(ctx, examID, sittingID),
		e.collections.CandidateActivityChangedForSitting(ctx, sittingID),
	)
}

func (e examSittingRealtimeEffects) ScheduleUpdated(ctx context.Context, examID model.ExamID, sittingID model.ExamSittingID,
	state model.ExamSittingState, revision int64, at time.Time,
) error {
	event, err := apprealtime.NewExamSittingScheduleUpdatedEvent(examID, sittingID, state, revision, at)
	if err != nil {
		return err
	}
	return errors.Join(
		e.realtime.PublishRealtimeEvent(ctx, event),
		e.publishBoardInvalidation(ctx, examID, sittingID),
		e.collections.CandidateActivityChangedForSitting(ctx, sittingID),
	)
}

func (e examSittingRealtimeEffects) Canceled(ctx context.Context, examID model.ExamID, sittingID model.ExamSittingID,
	state model.ExamSittingState, revision int64, at time.Time,
) error {
	event, err := apprealtime.NewExamSittingCanceledEvent(examID, sittingID, state, revision, at)
	if err != nil {
		return err
	}
	return errors.Join(
		e.realtime.PublishRealtimeEvent(ctx, event),
		e.publishBoardInvalidation(ctx, examID, sittingID),
		e.collections.CandidateActivityChangedForSitting(ctx, sittingID),
	)
}

func (e examSittingRealtimeEffects) LifecycleChanged(ctx context.Context, examID model.ExamID, sittingID model.ExamSittingID,
	state model.ExamSittingState, revision int64, transition store.ExamSittingLifecycleTransitionCode,
	scheduledEndAt, changedAt time.Time,
) error {
	managerEvent, err := apprealtime.NewExamSittingLifecycleChangedEvent(examID, sittingID, state, revision, string(transition), scheduledEndAt, changedAt)
	if err != nil {
		return err
	}
	candidateEvent, err := apprealtime.NewCandidateExamSittingLifecycleChangedEvent(examID, sittingID, state, revision, string(transition), scheduledEndAt, changedAt)
	if err != nil {
		return err
	}
	return errors.Join(
		e.realtime.PublishRealtimeEvent(ctx, managerEvent),
		e.realtime.PublishRealtimeEvent(ctx, candidateEvent),
		e.publishBoardInvalidation(ctx, examID, sittingID),
		e.collections.CandidateActivityChangedForSitting(ctx, sittingID),
	)
}

func (e examSittingRealtimeEffects) Report(ctx context.Context, operation string, err error) {
	e.realtime.reportTransientFailure(ctx, operation, err)
}

var _ examsitting.Authorizer = (*accessControlService)(nil)
var _ examsitting.Auditor = mutationAuditAdapter{}
var _ examsitting.SystemAuditor = examSittingSystemAuditAdapter{}
var _ examsitting.Effects = examSittingRealtimeEffects{}
var _ examsitting.EffectFailures = examSittingRealtimeEffects{}
