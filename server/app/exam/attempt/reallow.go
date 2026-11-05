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
	"strings"
	"unicode/utf8"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type ReallowCommand struct {
	ExamID                  model.ExamID
	SittingID               model.ExamSittingID
	AttemptID               model.ExamAttemptID
	SuspensionID            model.AttemptSuspensionID
	ExpectedAttemptRevision int64
	PrivateReason           string
	IdempotencyKey          string
}

type ReallowResult struct {
	ExamID          model.ExamID
	SittingID       model.ExamSittingID
	ClassID         model.ClassID
	CandidateUserID model.UserID
	Attempt         model.ExamAttempt
	Suspension      store.ExamAttemptSuspensionView
	Replayed        bool
}

// ReallowExamAttempt idempotently closes the active Attempt Suspension and
// returns the Attempt to active, fenced by ExpectedAttemptRevision. It
// authorizes Sitting management on the exact Sitting before reading the
// Attempt and preserves all evidence.
func (s *Service) ReallowExamAttempt(ctx context.Context, invocation invocation.Invocation, command ReallowCommand) (_ ReallowResult, resultErr error) {
	defer func() { resultErr = publicFailures.Conceal(resultErr) }()
	principal := invocation.Principal()
	if principal.Validate() != nil {
		return ReallowResult{}, apperror.New("authentication.token_invalid")
	}
	if !command.ExamID.IsValid() || !command.SittingID.IsValid() || !command.AttemptID.IsValid() ||
		!command.SuspensionID.IsValid() || command.ExpectedAttemptRevision < 1 || !validReallowReason(command.PrivateReason) {
		return ReallowResult{}, apperror.Invalid("exam.attempt.invalid", "reallow")
	}
	idempotency, err := prepareReallowIdempotency(invocation, command)
	if err != nil {
		return ReallowResult{}, err
	}
	// Authorize the exact Sitting before reading the Attempt so existence is
	// not probeable; later exits complete the audit attempt as failed.
	management, err := s.deps.Managers.AuthorizeSittingManage(ctx, invocation, command.SittingID)
	override := management.Override
	if err != nil {
		return ReallowResult{}, err
	}
	snapshot, err := s.deps.Sittings.Resolve(ctx, command.SittingID)
	if err != nil {
		return ReallowResult{}, storeError(storeErrors, err)
	}
	if snapshot == nil || snapshot.Sitting == nil || snapshot.Sitting.ID != command.SittingID || !snapshot.Sitting.ClassID.IsValid() {
		return ReallowResult{}, apperror.New("exam.attempt.unavailable").Wrap(errors.New("incomplete Sitting ownership projection"))
	}
	action := model.ActionExamSittingManage
	if override {
		action = model.ActionExamSittingManageOverride
	}
	auditID, err := s.deps.Auditor.BeginAtScope(ctx, invocation, action,
		model.Resource{Type: model.ResourceExamSitting, ID: command.SittingID.String()}, model.RoleScopeClass, snapshot.Sitting.ClassID.String(),
		store.ExamAttemptReallowOperation, map[string]any{"exam_id": command.ExamID.String(), "exam_sitting_id": command.SittingID.String(),
			"exam_attempt_id": command.AttemptID.String(), "suspension_id": command.SuspensionID.String(),
			"expected_attempt_revision": command.ExpectedAttemptRevision}, nil)
	if err != nil {
		return ReallowResult{}, err
	}
	if snapshot.Sitting.ExamID != command.ExamID {
		return ReallowResult{}, s.failAudit(ctx, auditID, apperror.New("exam.attempt.not_found"))
	}
	managerSnapshot, err := s.deps.Persistence.Get(ctx, command.ExamID, command.AttemptID)
	if err != nil {
		return ReallowResult{}, s.failAudit(ctx, auditID, err)
	}
	if managerSnapshot == nil || managerSnapshot.Attempt == nil || managerSnapshot.Attempt.Validate() != nil ||
		managerSnapshot.Attempt.ExamID != command.ExamID ||
		!validActiveSuspension(managerSnapshot.ActiveSuspension, managerSnapshot.Attempt.ID) {
		return ReallowResult{}, s.failAudit(ctx, auditID, apperror.New("exam.attempt.unavailable").Wrap(errors.New("inconsistent re-allow Attempt projection")))
	}
	if managerSnapshot.Attempt.SittingID != command.SittingID || managerSnapshot.Attempt.CandidateUserID == principal.UserID {
		return ReallowResult{}, s.failAudit(ctx, auditID, apperror.New("exam.attempt.not_found"))
	}
	at := model.TimeUTC(s.deps.Now())
	stored, err := s.deps.Persistence.ReallowAttempt(ctx, &store.ExamAttemptReallow{
		ExamID: command.ExamID, SittingID: command.SittingID, AttemptID: command.AttemptID, SuspensionID: command.SuspensionID,
		ActorUserID: principal.UserID, ManagerOverride: override, ExpectedRevision: command.ExpectedAttemptRevision,
		PrivateReason: command.PrivateReason, AuditEventID: auditID, AuditAt: at,
	}, idempotency)
	if err != nil {
		return ReallowResult{}, s.failAudit(ctx, auditID, err)
	}
	if stored == nil || stored.Attempt == nil || stored.Suspension == nil || stored.ExamID != command.ExamID ||
		stored.SittingID != command.SittingID || stored.ClassID != snapshot.Sitting.ClassID || !stored.CandidateUserID.IsValid() ||
		stored.Attempt.ID != command.AttemptID || stored.Attempt.ExamID != command.ExamID || stored.Attempt.SittingID != command.SittingID ||
		stored.Attempt.CandidateUserID != stored.CandidateUserID || stored.Attempt.State != model.ExamAttemptReady || stored.Attempt.Validate() != nil ||
		stored.Attempt.Revision != command.ExpectedAttemptRevision+1 || stored.Suspension.ID != command.SuspensionID ||
		!validClosedSuspension(stored.Suspension, command.AttemptID, principal.UserID) {
		return ReallowResult{}, apperror.New("exam.attempt.unavailable").Wrap(errors.New("inconsistent Attempt re-allow outcome"))
	}
	result := ReallowResult{ExamID: stored.ExamID, SittingID: stored.SittingID, ClassID: stored.ClassID,
		CandidateUserID: stored.CandidateUserID, Attempt: *stored.Attempt, Suspension: *stored.Suspension, Replayed: stored.Replayed}
	if !result.Replayed {
		if effectErr := s.deps.Effects.AttemptReallowed(ctx, result); effectErr != nil {
			s.deps.EffectFailures.Report(ctx, "exam_attempt_reallowed", effectErr)
		}
	}
	return result, nil
}

func validReallowReason(reason string) bool {
	return utf8.ValidString(reason) && reason == strings.TrimSpace(reason) && utf8.RuneCountInString(reason) >= 1 &&
		utf8.RuneCountInString(reason) <= model.AttemptSuspensionPrivateReasonMaximumRunes && len(reason) <= 4000
}

func validClosedSuspension(view *store.ExamAttemptSuspensionView, attemptID model.ExamAttemptID, actorID model.UserID) bool {
	return view != nil && view.ID.IsValid() && view.AttemptID == attemptID && view.ParticipationID.IsValid() && validSuspensionProvenance(view) &&
		view.Generation > 0 && view.State == model.AttemptSuspensionClosed && !view.StartedAt.IsZero() &&
		view.EndedAt.Valid && !view.EndedAt.Time.Before(view.StartedAt) && view.ReallowedByUserID == actorID
}
