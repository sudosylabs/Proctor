// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package attempt

import (
	"context"
	"errors"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// ManualSuspensionCommand is an Exam Manager's kick: the immediate manual
// control that blocks an active Attempt until an authorized re-allow.
type ManualSuspensionCommand struct {
	ExamID                  model.ExamID
	SittingID               model.ExamSittingID
	AttemptID               model.ExamAttemptID
	ExpectedAttemptRevision int64
	PrivateReason           string
	IdempotencyKey          string
}

// ManualSuspensionResult is the committed, private-reason-free kick outcome.
// Connection is the kicked generation's latest Connection, if any;
// ConnectionClosed is true when the kick closed it.
type ManualSuspensionResult struct {
	ExamID           model.ExamID
	SittingID        model.ExamSittingID
	ClassID          model.ClassID
	CandidateUserID  model.UserID
	Attempt          model.ExamAttempt
	Participation    store.ExamAttemptParticipationView
	Connection       *store.ExamAttemptManagerConnection
	ConnectionClosed bool
	Suspension       store.ExamAttemptSuspensionView
	Replayed         bool
}

// SuspendByManager authorizes the exact Sitting before reading the Attempt,
// audits the attempt, and lets the named Store operation end the live
// generation and open one manual Suspension episode atomically. Effects are
// published only for a fresh commit.
func (service *Service) SuspendByManager(ctx context.Context, call Call, command ManualSuspensionCommand) (ManualSuspensionResult, error) {
	principal := call.Principal()
	if principal.Validate() != nil {
		return ManualSuspensionResult{}, &Fault{Code: "authentication.invalid_token"}
	}
	if !command.ExamID.IsValid() || !command.SittingID.IsValid() || !command.AttemptID.IsValid() ||
		command.ExpectedAttemptRevision < 1 || !validReallowReason(command.PrivateReason) {
		return ManualSuspensionResult{}, invalid("manual_suspension")
	}
	idempotency, err := prepareManualSuspensionIdempotency(call, command)
	if err != nil {
		return ManualSuspensionResult{}, err
	}
	override, err := service.deps.Managers.AuthorizeSittingManage(ctx, call, command.SittingID)
	if err != nil {
		return ManualSuspensionResult{}, err
	}
	sitting, err := service.deps.Sittings.Resolve(ctx, command.SittingID)
	if err != nil {
		return ManualSuspensionResult{}, mapStore(err)
	}
	if sitting == nil || sitting.Sitting == nil || sitting.Sitting.ID != command.SittingID || !sitting.Sitting.ClassID.IsValid() {
		return ManualSuspensionResult{}, unavailable(errors.New("incomplete Sitting ownership projection"))
	}
	action := model.ActionExamSittingManage
	if override {
		action = model.ActionExamSittingManageOverride
	}
	auditID, err := service.deps.Auditor.Begin(ctx, call, action,
		model.Resource{Type: model.ResourceExamSitting, ID: command.SittingID.String()}, model.RoleScopeClass, sitting.Sitting.ClassID.String(),
		store.ExamAttemptSuspendOperation, map[string]any{"exam_id": command.ExamID.String(), "exam_sitting_id": command.SittingID.String(),
			"exam_attempt_id": command.AttemptID.String(), "expected_attempt_revision": command.ExpectedAttemptRevision})
	if err != nil {
		return ManualSuspensionResult{}, err
	}
	if sitting.Sitting.ExamID != command.ExamID {
		return ManualSuspensionResult{}, service.failAudit(ctx, auditID, &Fault{Code: "exam.attempt.not_found"})
	}
	managerSnapshot, err := service.deps.Persistence.Get(ctx, command.ExamID, command.AttemptID)
	if err != nil {
		return ManualSuspensionResult{}, service.failAudit(ctx, auditID, err)
	}
	if managerSnapshot == nil || managerSnapshot.Attempt == nil || managerSnapshot.Attempt.Validate() != nil ||
		managerSnapshot.Attempt.ExamID != command.ExamID ||
		!validActiveSuspension(managerSnapshot.ActiveSuspension, managerSnapshot.Attempt.ID) {
		return ManualSuspensionResult{}, service.failAudit(ctx, auditID, unavailable(errors.New("inconsistent manual suspension Attempt projection")))
	}
	if managerSnapshot.Attempt.SittingID != command.SittingID || managerSnapshot.Attempt.CandidateUserID == principal.UserID {
		return ManualSuspensionResult{}, service.failAudit(ctx, auditID, &Fault{Code: "exam.attempt.not_found"})
	}
	suspensionID := service.deps.NewSuspension()
	if !suspensionID.IsValid() {
		return ManualSuspensionResult{}, service.failAudit(ctx, auditID, invalid("suspension_id"))
	}
	stored, err := service.deps.Persistence.SuspendAttempt(ctx, &store.ExamAttemptManualSuspension{
		ExamID: command.ExamID, SittingID: command.SittingID, AttemptID: command.AttemptID, SuspensionID: suspensionID,
		ActorUserID: principal.UserID, ManagerOverride: override, ExpectedAttemptRevision: command.ExpectedAttemptRevision,
		PrivateReason: command.PrivateReason, AuditEventID: auditID, AuditAt: model.MillisFromTime(service.deps.Now()),
	}, idempotency)
	if err != nil {
		return ManualSuspensionResult{}, service.failAudit(ctx, auditID, err)
	}
	result, err := projectManualSuspension(stored, command, sitting.Sitting.ClassID, principal.UserID)
	if err != nil {
		return ManualSuspensionResult{}, err
	}
	if !result.Replayed {
		if effectErr := service.deps.Effects.AttemptSuspendedByManager(ctx, result); effectErr != nil {
			service.deps.EffectFailures.Report(ctx, "exam_attempt_suspended_by_manager", effectErr)
		}
	}
	return result, nil
}

func projectManualSuspension(stored *store.ExamAttemptManualSuspensionResult, command ManualSuspensionCommand,
	classID model.ClassID, actorID model.UserID,
) (ManualSuspensionResult, error) {
	if stored == nil || stored.Attempt == nil || stored.Participation == nil || stored.Suspension == nil ||
		stored.ExamID != command.ExamID || stored.SittingID != command.SittingID || stored.ClassID != classID ||
		!stored.CandidateUserID.IsValid() || stored.Attempt.ID != command.AttemptID || stored.Attempt.ExamID != command.ExamID ||
		stored.Attempt.SittingID != command.SittingID || stored.Attempt.CandidateUserID != stored.CandidateUserID ||
		stored.Attempt.State != model.ExamAttemptSuspended || stored.Attempt.Validate() != nil ||
		stored.Attempt.Revision != command.ExpectedAttemptRevision+1 || !validParticipationView(stored.Participation) ||
		stored.Participation.AttemptID != command.AttemptID || stored.Participation.State != model.AttemptParticipationEnded ||
		stored.Participation.EndReason != model.AttemptParticipationEndKicked ||
		!validActiveSuspension(stored.Suspension, command.AttemptID) || stored.Suspension.Source != model.AttemptSuspensionSourceManual ||
		stored.Suspension.ParticipationID != stored.Participation.ID || stored.Suspension.Generation != stored.Participation.Generation ||
		stored.Suspension.SuspendedByUserID != actorID ||
		stored.ConnectionClosed != (stored.Connection != nil) ||
		(stored.Connection != nil && (!stored.Connection.ID.IsValid() || stored.Connection.State != model.AttemptConnectionClosed ||
			stored.Connection.CloseReason != model.AttemptConnectionCloseKicked || !stored.Connection.ClosedAt.Valid)) {
		return ManualSuspensionResult{}, unavailable(errors.New("inconsistent Attempt manual suspension outcome"))
	}
	result := ManualSuspensionResult{ExamID: stored.ExamID, SittingID: stored.SittingID, ClassID: stored.ClassID,
		CandidateUserID: stored.CandidateUserID, Attempt: *stored.Attempt, Participation: *stored.Participation,
		ConnectionClosed: stored.ConnectionClosed, Suspension: *stored.Suspension, Replayed: stored.Replayed}
	if stored.Connection != nil {
		connection := *stored.Connection
		result.Connection = &connection
	}
	return result, nil
}
