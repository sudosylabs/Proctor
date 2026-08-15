// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	examsitting "github.com/sudosylabs/proctor/server/app/exam/sitting"
	apprealtime "github.com/sudosylabs/proctor/server/app/realtime"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type ExamSittingView = store.ExamSittingSnapshot

type ExamSittingPage struct {
	Items   []ExamSittingView
	HasMore bool
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
	AuthorizeSubmissionView(context.Context, examsitting.Call, model.ExamID, model.SubmissionID) error
	AuthorizeManage(context.Context, examsitting.Call, model.ExamSittingID) (bool, error)
	List(context.Context, examsitting.Call, examsitting.ListQuery) (examsitting.Page, error)
	UpdateSchedule(context.Context, examsitting.Call, examsitting.UpdateScheduleCommand) (store.ExamSittingSnapshot, error)
	Cancel(context.Context, examsitting.Call, examsitting.CancelCommand) (store.ExamSittingSnapshot, error)
	Pause(context.Context, examsitting.Call, examsitting.PauseCommand) (store.ExamSittingSnapshot, error)
	Resume(context.Context, examsitting.Call, examsitting.ResumeCommand) (store.ExamSittingSnapshot, error)
	Extend(context.Context, examsitting.Call, examsitting.ExtendCommand) (store.ExamSittingSnapshot, error)
	EarlyClose(context.Context, examsitting.Call, examsitting.EarlyCloseCommand) (store.ExamSittingSnapshot, error)
	AdvanceDue(context.Context, examsitting.SystemCall, model.ExamSittingID) (store.ExamSittingLifecycleResult, error)
	CloseIfNoAttempts(context.Context, examsitting.SystemCall, model.ExamSittingID) (store.ExamSittingLifecycleResult, error)
	ListLifecycleDue(context.Context, store.ExamSittingLifecycleDueOptions) ([]store.ExamSittingLifecycleDue, error)
}

func (a *App) ScheduleExamSitting(ctx context.Context, invocation Invocation, command ScheduleExamSittingCommand) (ExamSittingView, error) {
	idempotency, err := newExamSittingIdempotency(invocation, "exam.sitting.schedule.v1", command.IdempotencyKey, struct {
		ExamID           model.ExamID         `json:"exam_id"`
		ExamRevisionID   model.ExamRevisionID `json:"exam_revision_id"`
		ClassID          model.ClassID        `json:"class_id"`
		ScheduledStartAt time.Time            `json:"scheduled_start_at"`
		ScheduledEndAt   time.Time            `json:"scheduled_end_at"`
	}{command.ExamID, command.ExamRevisionID, command.ClassID, model.TimeUTC(command.ScheduledStartAt), model.TimeUTC(command.ScheduledEndAt)})
	if err != nil {
		return ExamSittingView{}, err
	}
	view, err := a.examSittings.Schedule(ctx, examsitting.NewCall(invocation.Principal(), invocation.RequestMetadata()), examsitting.ScheduleCommand{
		ExamID: command.ExamID, ExamRevisionID: command.ExamRevisionID, ClassID: command.ClassID,
		ScheduledStartAt: command.ScheduledStartAt, ScheduledEndAt: command.ScheduledEndAt, Idempotency: idempotency,
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

func (a *App) UpdateExamSittingSchedule(ctx context.Context, invocation Invocation, command UpdateExamSittingScheduleCommand) (ExamSittingView, error) {
	idempotency, err := newExamSittingScheduleUpdateIdempotency(invocation, command)
	if err != nil {
		return ExamSittingView{}, err
	}
	view, err := a.examSittings.UpdateSchedule(ctx, examsitting.NewCall(invocation.Principal(), invocation.RequestMetadata()), examsitting.UpdateScheduleCommand{
		ExamID: command.ExamID, SittingID: command.SittingID, ExpectedRevision: command.ExpectedRevision,
		ExamRevisionID: command.ExamRevisionID, ClassID: command.ClassID,
		ScheduledStartAt: command.ScheduledStartAt, ScheduledEndAt: command.ScheduledEndAt, Idempotency: idempotency,
	})
	if err != nil {
		return ExamSittingView{}, examSittingError(err, true)
	}
	return view, nil
}

func newExamSittingScheduleUpdateIdempotency(invocation Invocation, command UpdateExamSittingScheduleCommand) (*store.CommandIdempotency, error) {
	return newExamSittingIdempotency(invocation, "exam.sitting.schedule.update.v1", command.IdempotencyKey, struct {
		ExamID           model.ExamID          `json:"exam_id"`
		SittingID        model.ExamSittingID   `json:"exam_sitting_id"`
		ExpectedRevision int64                 `json:"expected_revision"`
		ExamRevisionID   *model.ExamRevisionID `json:"exam_revision_id,omitempty"`
		ClassID          *model.ClassID        `json:"class_id,omitempty"`
		ScheduledStartAt *time.Time            `json:"scheduled_start_at,omitempty"`
		ScheduledEndAt   *time.Time            `json:"scheduled_end_at,omitempty"`
	}{command.ExamID, command.SittingID, command.ExpectedRevision, command.ExamRevisionID, command.ClassID,
		canonicalTimePointer(command.ScheduledStartAt), canonicalTimePointer(command.ScheduledEndAt)})
}

func (a *App) CancelExamSitting(ctx context.Context, invocation Invocation, command CancelExamSittingCommand) (ExamSittingView, error) {
	idempotency, err := newExamSittingIdempotency(invocation, "exam.sitting.cancel.v1", command.IdempotencyKey, struct {
		ExamID           model.ExamID        `json:"exam_id"`
		SittingID        model.ExamSittingID `json:"exam_sitting_id"`
		ExpectedRevision int64               `json:"expected_revision"`
		PrivateReason    string              `json:"private_reason"`
	}{command.ExamID, command.SittingID, command.ExpectedRevision, command.PrivateReason})
	if err != nil {
		return ExamSittingView{}, err
	}
	view, err := a.examSittings.Cancel(ctx, examsitting.NewCall(invocation.Principal(), invocation.RequestMetadata()), examsitting.CancelCommand{
		ExamID: command.ExamID, SittingID: command.SittingID, ExpectedRevision: command.ExpectedRevision,
		PrivateReason: command.PrivateReason, Idempotency: idempotency,
	})
	if err != nil {
		return ExamSittingView{}, examSittingError(err, true)
	}
	return view, nil
}

func (a *App) PauseExamSitting(ctx context.Context, invocation Invocation, command PauseExamSittingCommand) (ExamSittingView, error) {
	return a.runExamSittingManagerTransition(ctx, invocation, "exam.sitting.pause.v1", command, a.examSittings.Pause)
}

func (a *App) ResumeExamSitting(ctx context.Context, invocation Invocation, command ResumeExamSittingCommand) (ExamSittingView, error) {
	return a.runExamSittingManagerTransition(ctx, invocation, "exam.sitting.resume.v1", command, a.examSittings.Resume)
}

func (a *App) CloseExamSitting(ctx context.Context, invocation Invocation, command CloseExamSittingCommand) (ExamSittingView, error) {
	return a.runExamSittingManagerTransition(ctx, invocation, "exam.sitting.close.v1", command, a.examSittings.EarlyClose)
}

type examSittingManagerTransitionUseCase func(context.Context, examsitting.Call, examsitting.PauseCommand) (store.ExamSittingSnapshot, error)

func (a *App) runExamSittingManagerTransition(ctx context.Context, invocation Invocation, operation string,
	command PauseExamSittingCommand, run examSittingManagerTransitionUseCase,
) (ExamSittingView, error) {
	idempotency, err := newExamSittingIdempotency(invocation, operation, command.IdempotencyKey, struct {
		ExamID           model.ExamID        `json:"exam_id"`
		SittingID        model.ExamSittingID `json:"exam_sitting_id"`
		ExpectedRevision int64               `json:"expected_revision"`
		PrivateReason    string              `json:"private_reason"`
	}{command.ExamID, command.SittingID, command.ExpectedRevision, command.PrivateReason})
	if err != nil {
		return ExamSittingView{}, err
	}
	view, err := run(ctx, examsitting.NewCall(invocation.Principal(), invocation.RequestMetadata()), examsitting.PauseCommand{
		ExamID: command.ExamID, SittingID: command.SittingID, ExpectedRevision: command.ExpectedRevision,
		PrivateReason: command.PrivateReason, Idempotency: idempotency,
	})
	if err != nil {
		return ExamSittingView{}, examSittingError(err, true)
	}
	return view, nil
}

func (a *App) ExtendExamSitting(ctx context.Context, invocation Invocation, command ExtendExamSittingCommand) (ExamSittingView, error) {
	idempotency, err := newExamSittingIdempotency(invocation, "exam.sitting.extend.v1", command.IdempotencyKey, struct {
		ExamID           model.ExamID        `json:"exam_id"`
		SittingID        model.ExamSittingID `json:"exam_sitting_id"`
		ExpectedRevision int64               `json:"expected_revision"`
		ScheduledEndAt   time.Time           `json:"scheduled_end_at"`
		PrivateReason    string              `json:"private_reason"`
	}{command.ExamID, command.SittingID, command.ExpectedRevision, model.TimeUTC(command.ScheduledEndAt), command.PrivateReason})
	if err != nil {
		return ExamSittingView{}, err
	}
	view, err := a.examSittings.Extend(ctx, examsitting.NewCall(invocation.Principal(), invocation.RequestMetadata()), examsitting.ExtendCommand{
		ExamID: command.ExamID, SittingID: command.SittingID, ExpectedRevision: command.ExpectedRevision,
		ScheduledEndAt: command.ScheduledEndAt, PrivateReason: command.PrivateReason, Idempotency: idempotency,
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
	if result.Value != nil && result.Value.Sitting != nil && result.Value.Sitting.State == model.ExamSittingClosing {
		closed, closeErr := useCases.sittings.CloseIfNoAttempts(ctx, call, sittingID)
		if closeErr != nil {
			return nil, closeErr
		}
		return &closed, nil
	}
	return &result, nil
}

func (useCases examSittingLifecycleJobUseCases) ListExamSittingLifecycleDueFromJob(ctx context.Context, options store.ExamSittingLifecycleDueOptions) ([]store.ExamSittingLifecycleDue, error) {
	return useCases.sittings.ListLifecycleDue(ctx, options)
}

func newExamSittingIdempotency(invocation Invocation, operation, key string, fingerprint any) (*store.CommandIdempotency, error) {
	if key == "" {
		return nil, NewError("idempotency.key_required")
	}
	return newCommandIdempotency(invocation, operation, key, fingerprint)
}

func canonicalTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	canonical := model.TimeUTC(*value)
	return &canonical
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

type examSittingRealtimeEffects struct{ realtime *realtimeService }

func (effects examSittingRealtimeEffects) Scheduled(ctx context.Context, examID model.ExamID, sittingID model.ExamSittingID,
	state model.ExamSittingState, revision int64, at time.Time,
) error {
	event, err := apprealtime.NewExamSittingScheduledEvent(examID, sittingID, state, revision, at)
	if err != nil {
		return err
	}
	return effects.realtime.Publish(ctx, event)
}

func (effects examSittingRealtimeEffects) ScheduleUpdated(ctx context.Context, examID model.ExamID, sittingID model.ExamSittingID,
	state model.ExamSittingState, revision int64, at time.Time,
) error {
	event, err := apprealtime.NewExamSittingScheduleUpdatedEvent(examID, sittingID, state, revision, at)
	if err != nil {
		return err
	}
	return effects.realtime.Publish(ctx, event)
}

func (effects examSittingRealtimeEffects) Canceled(ctx context.Context, examID model.ExamID, sittingID model.ExamSittingID,
	state model.ExamSittingState, revision int64, at time.Time,
) error {
	event, err := apprealtime.NewExamSittingCanceledEvent(examID, sittingID, state, revision, at)
	if err != nil {
		return err
	}
	return effects.realtime.Publish(ctx, event)
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
	return errors.Join(
		effects.realtime.Publish(ctx, managerEvent),
		effects.realtime.Publish(ctx, candidateEvent),
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
