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

	"github.com/sudosylabs/proctor/server/app/exam/sitting"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/app/realtime"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// examSittingLifecycle is the Sitting lifecycle work the lifecycle Job
// drives; its tests substitute a fake.
type examSittingLifecycle interface {
	AdvanceDue(context.Context, invocation.System, model.ExamSittingID) (store.ExamSittingLifecycleResult, error)
	ListLifecycleDue(context.Context, store.ExamSittingLifecycleDueOptions) ([]store.ExamSittingLifecycleDue, error)
}

type examSittingLifecycleJobUseCases struct{ sittings examSittingLifecycle }

func (uc examSittingLifecycleJobUseCases) ReconcileExamSittingLifecycleFromJob(ctx context.Context, sittingID model.ExamSittingID, jobID model.JobID,
	attemptID model.JobAttemptID,
) (*store.ExamSittingLifecycleResult, error) {
	call := invocation.System{JobID: jobID, AttemptID: attemptID}
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

func (a examSittingSystemAuditAdapter) Begin(ctx context.Context, call invocation.System, action model.Action, resource model.Resource, scopeType model.RoleScopeType, scopeID, operation string, value map[string]any) (model.AuditEventID, error) {
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
	event, err := realtime.NewExamSittingScheduledEvent(examID, sittingID, state, revision, at)
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
	event, err := realtime.NewExamSittingScheduleUpdatedEvent(examID, sittingID, state, revision, at)
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
	event, err := realtime.NewExamSittingCanceledEvent(examID, sittingID, state, revision, at)
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
	managerEvent, err := realtime.NewExamSittingLifecycleChangedEvent(examID, sittingID, state, revision, string(transition), scheduledEndAt, changedAt)
	if err != nil {
		return err
	}
	candidateEvent, err := realtime.NewCandidateExamSittingLifecycleChangedEvent(examID, sittingID, state, revision, string(transition), scheduledEndAt, changedAt)
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

var _ sitting.Authorizer = (*accessControlService)(nil)
var _ sitting.Auditor = mutationAuditAdapter{}
var _ sitting.SystemAuditor = examSittingSystemAuditAdapter{}
var _ sitting.Effects = examSittingRealtimeEffects{}
var _ sitting.EffectFailures = examSittingRealtimeEffects{}
