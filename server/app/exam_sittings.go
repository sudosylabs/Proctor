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
	"fmt"
	"time"

	examsitting "github.com/sudosylabs/proctor/server/app/exam/sitting"
	appexecution "github.com/sudosylabs/proctor/server/app/execution"
	apprealtime "github.com/sudosylabs/proctor/server/app/realtime"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type ExamSittingView = store.ExamSittingSnapshot

type ExamSittingPage struct {
	Items   []ExamSittingView
	HasMore bool
}

type ExamSittingNoShowPage struct {
	Items   []store.ExamSittingNoShow
	HasMore bool
}

type ListExamSittingNoShowsQuery struct {
	ExamID               model.ExamID
	SittingID            model.ExamSittingID
	AfterCandidateUserID model.UserID
	Limit                int
}

type ScheduleExamSittingCommand struct {
	ExamID           model.ExamID
	ExamRevisionID   model.ExamRevisionID
	ClassID          model.ClassID
	ScheduledStartAt time.Time
	ScheduledEndAt   time.Time
	IdempotencyKey   string
}

type GetExamSittingQuery struct {
	ExamID    model.ExamID
	SittingID model.ExamSittingID
}

type ListExamSittingsQuery struct {
	ExamID                 model.ExamID
	ClassID                model.ClassID
	States                 []model.ExamSittingState
	OverlapStartAt         time.Time
	OverlapEndAt           time.Time
	BeforeScheduledStartAt time.Time
	BeforeSittingID        model.ExamSittingID
	Limit                  int
}

type UpdateExamSittingScheduleCommand struct {
	ExamID           model.ExamID
	SittingID        model.ExamSittingID
	ExpectedRevision int64
	ExamRevisionID   *model.ExamRevisionID
	ClassID          *model.ClassID
	ScheduledStartAt *time.Time
	ScheduledEndAt   *time.Time
	IdempotencyKey   string
}

type CancelExamSittingCommand struct {
	ExamID           model.ExamID
	SittingID        model.ExamSittingID
	ExpectedRevision int64
	PrivateReason    string
	IdempotencyKey   string
}

type PauseExamSittingCommand struct {
	ExamID           model.ExamID
	SittingID        model.ExamSittingID
	ExpectedRevision int64
	PrivateReason    string
	IdempotencyKey   string
}

type ResumeExamSittingCommand = PauseExamSittingCommand
type CloseExamSittingCommand = PauseExamSittingCommand

type ExtendExamSittingCommand struct {
	ExamID           model.ExamID
	SittingID        model.ExamSittingID
	ExpectedRevision int64
	ScheduledEndAt   time.Time
	PrivateReason    string
	IdempotencyKey   string
}

type examSittingUseCases interface {
	Schedule(context.Context, examsitting.Call, examsitting.ScheduleCommand) (store.ExamSittingSnapshot, error)
	Get(context.Context, examsitting.Call, model.ExamID, model.ExamSittingID) (store.ExamSittingSnapshot, error)
	AuthorizeView(context.Context, examsitting.Call, model.ExamSittingID) error
	AuthorizeBrowserActivityView(context.Context, examsitting.Call, model.ExamSittingID) (model.AcademicUnitID, bool, error)
	AuthorizeSubmissionView(context.Context, examsitting.Call, model.ExamID, model.SubmissionID) error
	AuthorizeSubmissionReview(context.Context, examsitting.Call, model.ExamID, model.SubmissionID) (bool, error)
	AuthorizeSubmissionRelease(context.Context, examsitting.Call, model.ExamID, model.SubmissionID) (bool, error)
	AuthorizeManage(context.Context, examsitting.Call, model.ExamSittingID) (bool, error)
	List(context.Context, examsitting.Call, examsitting.ListQuery) (examsitting.Page, error)
	UpdateSchedule(context.Context, examsitting.Call, examsitting.UpdateScheduleCommand) (store.ExamSittingSnapshot, error)
	Cancel(context.Context, examsitting.Call, examsitting.CancelCommand) (store.ExamSittingSnapshot, error)
	Pause(context.Context, examsitting.Call, examsitting.PauseCommand) (store.ExamSittingSnapshot, error)
	Resume(context.Context, examsitting.Call, examsitting.ResumeCommand) (store.ExamSittingSnapshot, error)
	Extend(context.Context, examsitting.Call, examsitting.ExtendCommand) (store.ExamSittingSnapshot, error)
	EarlyClose(context.Context, examsitting.Call, examsitting.EarlyCloseCommand) (store.ExamSittingSnapshot, error)
	AdvanceDue(context.Context, examsitting.SystemCall, model.ExamSittingID) (store.ExamSittingLifecycleResult, error)
	FinishSealing(context.Context, examsitting.SystemCall, model.ExamSittingID) (store.ExamSittingLifecycleResult, error)
	ListNoShows(context.Context, examsitting.Call, model.ExamID, model.ExamSittingID, model.UserID, int) (examsitting.NoShowPage, error)
	ListLifecycleDue(context.Context, store.ExamSittingLifecycleDueOptions) ([]store.ExamSittingLifecycleDue, error)
}

func (a *App) ScheduleExamSitting(ctx context.Context, invocation Invocation, command ScheduleExamSittingCommand) (result ExamSittingView, resultErr error) {
	defer func() { a.recordOperational("exam_sitting", "schedule", resultErr) }()
	view, err := a.examSittings.Schedule(ctx, examsitting.NewCall(invocation.Principal(), invocation.RequestMetadata()), examsitting.ScheduleCommand{
		ExamID: command.ExamID, ExamRevisionID: command.ExamRevisionID, ClassID: command.ClassID,
		ScheduledStartAt: command.ScheduledStartAt, ScheduledEndAt: command.ScheduledEndAt, IdempotencyKey: command.IdempotencyKey,
	})
	if err != nil {
		return ExamSittingView{}, examSittingError(err, true)
	}
	return view, nil
}

func (a *App) GetExamSitting(ctx context.Context, invocation Invocation, query GetExamSittingQuery) (ExamSittingView, error) {
	view, err := a.examSittings.Get(ctx, examsitting.NewCall(invocation.Principal(), invocation.RequestMetadata()), query.ExamID, query.SittingID)
	if err != nil {
		return ExamSittingView{}, examSittingError(err, true)
	}
	return view, nil
}

func (a *App) ListExamSittings(ctx context.Context, invocation Invocation, query ListExamSittingsQuery) (ExamSittingPage, error) {
	page, err := a.examSittings.List(ctx, examsitting.NewCall(invocation.Principal(), invocation.RequestMetadata()), examsitting.ListQuery{
		ExamID: query.ExamID, ClassID: query.ClassID, States: append([]model.ExamSittingState(nil), query.States...),
		OverlapStartAt: query.OverlapStartAt, OverlapEndAt: query.OverlapEndAt,
		BeforeScheduledStartAt: query.BeforeScheduledStartAt, BeforeSittingID: query.BeforeSittingID, Limit: query.Limit,
	})
	if err != nil {
		return ExamSittingPage{}, examSittingError(err, true)
	}
	return ExamSittingPage{Items: page.Items, HasMore: page.HasMore}, nil
}

func (a *App) ListExamSittingNoShows(ctx context.Context, invocation Invocation,
	query ListExamSittingNoShowsQuery,
) (ExamSittingNoShowPage, error) {
	page, err := a.examSittings.ListNoShows(ctx, examsitting.NewCall(invocation.Principal(), invocation.RequestMetadata()),
		query.ExamID, query.SittingID, query.AfterCandidateUserID, query.Limit)
	if err != nil {
		return ExamSittingNoShowPage{}, examSittingError(err, true)
	}
	return ExamSittingNoShowPage{Items: page.Items, HasMore: page.HasMore}, nil
}

func (a *App) UpdateExamSittingSchedule(ctx context.Context, invocation Invocation, command UpdateExamSittingScheduleCommand) (ExamSittingView, error) {
	view, err := a.examSittings.UpdateSchedule(ctx, examsitting.NewCall(invocation.Principal(), invocation.RequestMetadata()), examsitting.UpdateScheduleCommand{
		ExamID: command.ExamID, SittingID: command.SittingID, ExpectedRevision: command.ExpectedRevision,
		ExamRevisionID: command.ExamRevisionID, ClassID: command.ClassID,
		ScheduledStartAt: command.ScheduledStartAt, ScheduledEndAt: command.ScheduledEndAt, IdempotencyKey: command.IdempotencyKey,
	})
	if err != nil {
		return ExamSittingView{}, examSittingError(err, true)
	}
	return view, nil
}

func (a *App) CancelExamSitting(ctx context.Context, invocation Invocation, command CancelExamSittingCommand) (ExamSittingView, error) {
	view, err := a.examSittings.Cancel(ctx, examsitting.NewCall(invocation.Principal(), invocation.RequestMetadata()), examsitting.CancelCommand{
		ExamID: command.ExamID, SittingID: command.SittingID, ExpectedRevision: command.ExpectedRevision,
		PrivateReason: command.PrivateReason, IdempotencyKey: command.IdempotencyKey,
	})
	if err != nil {
		return ExamSittingView{}, examSittingError(err, true)
	}
	return view, nil
}

func (a *App) PauseExamSitting(ctx context.Context, invocation Invocation, command PauseExamSittingCommand) (ExamSittingView, error) {
	return a.runExamSittingManagerTransition(ctx, invocation, "pause", command, a.examSittings.Pause)
}

func (a *App) ResumeExamSitting(ctx context.Context, invocation Invocation, command ResumeExamSittingCommand) (ExamSittingView, error) {
	return a.runExamSittingManagerTransition(ctx, invocation, "resume", command, a.examSittings.Resume)
}

func (a *App) CloseExamSitting(ctx context.Context, invocation Invocation, command CloseExamSittingCommand) (ExamSittingView, error) {
	return a.runExamSittingManagerTransition(ctx, invocation, "close", command, a.examSittings.EarlyClose)
}

type examSittingManagerTransitionUseCase func(context.Context, examsitting.Call, examsitting.PauseCommand) (store.ExamSittingSnapshot, error)

func (a *App) runExamSittingManagerTransition(ctx context.Context, invocation Invocation, metricEvent string,
	command PauseExamSittingCommand, run examSittingManagerTransitionUseCase,
) (result ExamSittingView, resultErr error) {
	defer func() { a.recordOperational("exam_sitting", metricEvent, resultErr) }()
	view, err := run(ctx, examsitting.NewCall(invocation.Principal(), invocation.RequestMetadata()), examsitting.PauseCommand{
		ExamID: command.ExamID, SittingID: command.SittingID, ExpectedRevision: command.ExpectedRevision,
		PrivateReason: command.PrivateReason, IdempotencyKey: command.IdempotencyKey,
	})
	if err != nil {
		return ExamSittingView{}, examSittingError(err, true)
	}
	return view, nil
}

func (a *App) ExtendExamSitting(ctx context.Context, invocation Invocation, command ExtendExamSittingCommand) (ExamSittingView, error) {
	view, err := a.examSittings.Extend(ctx, examsitting.NewCall(invocation.Principal(), invocation.RequestMetadata()), examsitting.ExtendCommand{
		ExamID: command.ExamID, SittingID: command.SittingID, ExpectedRevision: command.ExpectedRevision,
		ScheduledEndAt: command.ScheduledEndAt, PrivateReason: command.PrivateReason, IdempotencyKey: command.IdempotencyKey,
	})
	if err != nil {
		return ExamSittingView{}, examSittingError(err, true)
	}
	return view, nil
}

type examSittingLifecycleJobUseCases struct{ sittings examSittingUseCases }

func (useCases examSittingLifecycleJobUseCases) ReconcileExamSittingLifecycleFromJob(ctx context.Context, sittingID model.ExamSittingID, jobID model.JobID,
	attemptID model.JobAttemptID,
) (*store.ExamSittingLifecycleResult, error) {
	call := examsitting.SystemCall{JobID: jobID, AttemptID: attemptID}
	result, err := useCases.sittings.AdvanceDue(ctx, call, sittingID)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (useCases examSittingLifecycleJobUseCases) ListExamSittingLifecycleDueFromJob(ctx context.Context, options store.ExamSittingLifecycleDueOptions) ([]store.ExamSittingLifecycleDue, error) {
	return useCases.sittings.ListLifecycleDue(ctx, options)
}

func examSittingError(err error, conceal bool) error {
	if err == nil {
		return nil
	}
	if existing, ok := As(err); ok {
		if conceal && existing.Code() == "authorization.denied" {
			return NewError("resource.not_found").Wrap(err)
		}
		return err
	}
	var fault *examsitting.Fault
	if !errors.As(err, &fault) {
		return NewError("exam.sitting.unavailable").Wrap(err)
	}
	if conceal && fault.Code == "exam.sitting.not_found" {
		return NewError("resource.not_found").Wrap(err)
	}
	mapped := NewError(fault.Code)
	for key, value := range fault.SafeFields {
		mapped.WithField(key, fmt.Sprint(value))
	}
	return mapped.Wrap(err)
}

type examSittingAuthorizationAdapter struct{ authorization *accessControlService }

func (adapter examSittingAuthorizationAdapter) Authorize(ctx context.Context, call examsitting.Call, action model.Action, resource model.Resource) error {
	return adapter.authorization.authorizeCurrentState(ctx, call.Principal(), action, resource, call.RequestMetadata())
}

type examSittingAuditAdapter struct{ audit mutationAuditAdapter }

func (adapter examSittingAuditAdapter) Begin(ctx context.Context, call examsitting.Call, action model.Action, resource model.Resource,
	scopeType model.RoleScopeType, scopeID, operation string, value, prior map[string]any,
) (string, error) {
	return adapter.audit.BeginAtScope(ctx, NewInvocation(call.Principal(), call.RequestMetadata()), action, resource,
		scopeType, scopeID, operation, value, prior)
}

func (adapter examSittingAuditAdapter) Fail(ctx context.Context, id, code string) error {
	return adapter.audit.Fail(ctx, id, code)
}

type examSittingSystemAuditAdapter struct{ audit *auditService }

func (adapter examSittingSystemAuditAdapter) Begin(ctx context.Context, call examsitting.SystemCall, action model.Action,
	resource model.Resource, scopeType model.RoleScopeType, scopeID, operation string, value map[string]any,
) (string, error) {
	auditValue := make(map[string]any, len(value)+2)
	for key, item := range value {
		auditValue[key] = item
	}
	auditValue["job_id"] = call.JobID.String()
	auditValue["job_attempt_id"] = call.AttemptID.String()
	event, err := adapter.audit.BeginSystemCriticalActionAtScope(ctx, action, resource, scopeType, scopeID,
		map[string]any{"operation": operation, "value": auditValue})
	if err != nil {
		return "", err
	}
	return event.ID.String(), nil
}

func (adapter examSittingSystemAuditAdapter) Fail(ctx context.Context, id, code string) error {
	_, err := adapter.audit.CompleteCriticalAction(ctx, id, model.AuditStatusFail, code, nil)
	return err
}

type examSittingRealtimeEffects struct {
	realtime    *realtimeService
	execution   *appexecution.Service
	collections examCollectionInvalidationEffects
}

func (effects examSittingRealtimeEffects) publishBoardInvalidation(ctx context.Context, examID model.ExamID,
	sittingID model.ExamSittingID,
) error {
	return effects.collections.SittingBoardChanged(ctx, examID, sittingID)
}

func (effects examSittingRealtimeEffects) Scheduled(ctx context.Context, examID model.ExamID, sittingID model.ExamSittingID,
	state model.ExamSittingState, revision int64, at time.Time,
) error {
	event, err := apprealtime.NewExamSittingScheduledEvent(examID, sittingID, state, revision, at)
	if err != nil {
		return err
	}
	return errors.Join(
		effects.realtime.Publish(ctx, event),
		effects.publishBoardInvalidation(ctx, examID, sittingID),
		effects.collections.CandidateActivityChangedForSitting(ctx, sittingID),
	)
}

func (effects examSittingRealtimeEffects) ScheduleUpdated(ctx context.Context, examID model.ExamID, sittingID model.ExamSittingID,
	state model.ExamSittingState, revision int64, at time.Time,
) error {
	event, err := apprealtime.NewExamSittingScheduleUpdatedEvent(examID, sittingID, state, revision, at)
	if err != nil {
		return err
	}
	return errors.Join(
		effects.realtime.Publish(ctx, event),
		effects.publishBoardInvalidation(ctx, examID, sittingID),
		effects.collections.CandidateActivityChangedForSitting(ctx, sittingID),
	)
}

func (effects examSittingRealtimeEffects) Canceled(ctx context.Context, examID model.ExamID, sittingID model.ExamSittingID,
	state model.ExamSittingState, revision int64, at time.Time,
) error {
	event, err := apprealtime.NewExamSittingCanceledEvent(examID, sittingID, state, revision, at)
	if err != nil {
		return err
	}
	return errors.Join(
		effects.realtime.Publish(ctx, event),
		effects.publishBoardInvalidation(ctx, examID, sittingID),
		effects.collections.CandidateActivityChangedForSitting(ctx, sittingID),
	)
}

func (effects examSittingRealtimeEffects) LifecycleChanged(ctx context.Context, examID model.ExamID, sittingID model.ExamSittingID,
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
	var executionErr error
	if effects.execution != nil {
		switch transition {
		case store.ExamSittingTransitionManagerPaused:
			executionErr = effects.execution.FreezeSitting(ctx, sittingID, revision)
		case store.ExamSittingTransitionManagerResumed:
			executionErr = effects.execution.ThawSitting(ctx, sittingID, revision)
		case store.ExamSittingTransitionManagerClosed, store.ExamSittingTransitionScheduledEndReached,
			store.ExamSittingTransitionClosedNoAttempts, store.ExamSittingTransitionSealingCompleted:
			executionErr = effects.execution.ReleaseSitting(ctx, sittingID)
		}
	}
	return errors.Join(
		effects.realtime.Publish(ctx, managerEvent),
		effects.realtime.Publish(ctx, candidateEvent),
		effects.publishBoardInvalidation(ctx, examID, sittingID),
		effects.collections.CandidateActivityChangedForSitting(ctx, sittingID),
		executionErr,
	)
}

func (effects examSittingRealtimeEffects) Report(ctx context.Context, operation string, err error) {
	effects.realtime.reportTransientFailure(ctx, operation, err)
}

var _ examsitting.Authorizer = examSittingAuthorizationAdapter{}
var _ examsitting.Auditor = examSittingAuditAdapter{}
var _ examsitting.SystemAuditor = examSittingSystemAuditAdapter{}
var _ examsitting.Effects = examSittingRealtimeEffects{}
var _ examsitting.EffectFailures = examSittingRealtimeEffects{}
