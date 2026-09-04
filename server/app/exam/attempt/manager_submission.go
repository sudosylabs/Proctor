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

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type ManagerEndCommand struct {
	ExamID                  model.ExamID
	SittingID               model.ExamSittingID
	AttemptID               model.ExamAttemptID
	ExpectedAttemptRevision int64
	PrivateReason           string
	IdempotencyKey          string
}

func (service *Service) EndByManager(ctx context.Context, call Call,
	command ManagerEndCommand,
) (SubmissionResult, error) {
	principal := call.Principal()
	if principal.Validate() != nil {
		return SubmissionResult{}, &Fault{Code: "authentication.invalid_token"}
	}
	if !command.ExamID.IsValid() || !command.SittingID.IsValid() || !command.AttemptID.IsValid() ||
		command.ExpectedAttemptRevision < 1 || !validManagerEndReason(command.PrivateReason) {
		return SubmissionResult{}, invalid("manager_end")
	}
	idempotency, err := prepareManagerEndIdempotency(call, command)
	if err != nil {
		return SubmissionResult{}, err
	}
	managerSnapshot, err := service.deps.Persistence.Get(ctx, command.ExamID, command.AttemptID)
	if err != nil {
		return SubmissionResult{}, mapStore(err)
	}
	if managerSnapshot == nil || managerSnapshot.Attempt == nil || managerSnapshot.Attempt.Validate() != nil ||
		managerSnapshot.Attempt.ExamID != command.ExamID ||
		!validActiveSuspension(managerSnapshot.ActiveSuspension, managerSnapshot.Attempt.ID) {
		return SubmissionResult{}, unavailable(errors.New("inconsistent manager-ended Attempt projection"))
	}
	if managerSnapshot.Attempt.SittingID != command.SittingID {
		return SubmissionResult{}, &Fault{Code: "exam.attempt.not_found"}
	}
	sitting, err := service.deps.Sittings.Resolve(ctx, managerSnapshot.Attempt.SittingID)
	if err != nil {
		return SubmissionResult{}, mapStore(err)
	}
	if sitting == nil || sitting.Sitting == nil || sitting.Sitting.ID != managerSnapshot.Attempt.SittingID ||
		sitting.Sitting.ExamID != managerSnapshot.Attempt.ExamID || !sitting.Sitting.ClassID.IsValid() {
		return SubmissionResult{}, unavailable(errors.New("inconsistent manager-ended Sitting projection"))
	}
	override, err := service.deps.Managers.AuthorizeSittingManage(ctx, call, managerSnapshot.Attempt.SittingID)
	if err != nil {
		return SubmissionResult{}, err
	}
	action := model.ActionExamSittingManage
	if override {
		action = model.ActionExamSittingManageOverride
	}
	auditID, err := service.deps.Auditor.Begin(ctx, call, action,
		model.Resource{Type: model.ResourceExamSitting, ID: managerSnapshot.Attempt.SittingID.String()}, model.RoleScopeClass,
		sitting.Sitting.ClassID.String(), store.ExamSubmissionManagerEndOperation,
		map[string]any{"exam_id": managerSnapshot.Attempt.ExamID.String(),
			"exam_sitting_id": managerSnapshot.Attempt.SittingID.String(), "exam_attempt_id": managerSnapshot.Attempt.ID.String(),
			"expected_attempt_revision": command.ExpectedAttemptRevision})
	if err != nil {
		return SubmissionResult{}, err
	}
	if managerSnapshot.Attempt.CandidateUserID == principal.UserID {
		return SubmissionResult{}, service.failAudit(ctx, auditID, &Fault{Code: "exam.attempt.not_found"})
	}
	request := store.ExamSubmissionManagerEndRequest{ExamID: command.ExamID, SittingID: command.SittingID,
		AttemptID: command.AttemptID, ActorUserID: principal.UserID, ManagerOverride: override,
		ExpectedAttemptRevision: command.ExpectedAttemptRevision, PrivateReason: command.PrivateReason}
	preparation, err := service.deps.Submissions.PrepareManagerEnd(ctx, request)
	if err != nil {
		return SubmissionResult{}, service.failAudit(ctx, auditID, mapStore(err))
	}
	if preparation == nil || !validAutomaticSealTarget(preparation.Target, command.SittingID) ||
		preparation.Target.ExamID != command.ExamID || preparation.Target.AttemptID != command.AttemptID ||
		preparation.ExpectedAttemptRevision != command.ExpectedAttemptRevision || preparation.SealAt.IsZero() {
		return SubmissionResult{}, service.failAudit(ctx, auditID,
			unavailable(errors.New("invalid manager-ended Submission preparation")))
	}
	if preparation.Target.CandidateUserID != managerSnapshot.Attempt.CandidateUserID ||
		preparation.Target.ClassID != sitting.Sitting.ClassID {
		return SubmissionResult{}, service.failAudit(ctx, auditID,
			unavailable(errors.New("mismatched manager-ended Submission preparation")))
	}
	proposed := service.deps.NewSubmission()
	if !proposed.IsValid() {
		return SubmissionResult{}, service.failAudit(ctx, auditID, invalid("submission_id"))
	}
	var notice *store.PreparedMail
	var expectedRecipientRevision int64
	if !preparation.Replayed {
		preparedMail, prepareErr := service.deps.Mail.PrepareSubmissionReceipt(ctx, SubmissionMailPreparation{
			CandidateUserID: preparation.Target.CandidateUserID, ExamID: preparation.Target.ExamID,
			SittingID: preparation.Target.SittingID, SubmissionID: proposed, SealedAt: preparation.SealAt,
			Provenance: model.ExamSubmissionManagerEndedAttempt,
		})
		if prepareErr != nil || preparedMail == nil || preparedMail.Notice == nil || preparedMail.ExpectedRecipientRevision < 1 {
			if prepareErr == nil {
				prepareErr = errors.New("invalid manager-ended Submission receipt mail preparation")
			}
			return SubmissionResult{}, service.failAudit(ctx, auditID, unavailable(prepareErr))
		}
		notice, expectedRecipientRevision = preparedMail.Notice, preparedMail.ExpectedRecipientRevision
	}
	stored, err := service.deps.Submissions.EndByManager(ctx, &store.ExamSubmissionManagerEnd{Request: request,
		Target: preparation.Target, SubmissionID: proposed, AuditEventID: auditID,
		AuditAt: model.MillisFromTime(preparation.SealAt), Notice: notice,
		ExpectedRecipientRevision: expectedRecipientRevision}, idempotency)
	if err != nil {
		return SubmissionResult{}, service.failAudit(ctx, auditID, mapStore(err))
	}
	result, err := projectManagerEndResult(stored, preparation.Target, command, proposed)
	if err != nil {
		return SubmissionResult{}, err
	}
	if !result.Replayed {
		if effectErr := service.deps.Effects.AttemptSubmitted(ctx, result); effectErr != nil {
			service.deps.EffectFailures.Report(ctx, "exam_attempt_manager_ended", effectErr)
		}
	}
	return result, nil
}

func validManagerEndReason(reason string) bool {
	return utf8.ValidString(reason) && reason == strings.TrimSpace(reason) && utf8.RuneCountInString(reason) >= 1 &&
		utf8.RuneCountInString(reason) <= 1000 && len(reason) <= 4000
}

func projectManagerEndResult(stored *store.ExamSubmissionManagerEndResult,
	target store.ExamSubmissionAutomaticSealTarget, command ManagerEndCommand, proposed model.SubmissionID,
) (SubmissionResult, error) {
	if stored == nil || stored.Receipt.State != model.ExamAttemptSubmitted || stored.Receipt.AttemptID != command.AttemptID ||
		!stored.Receipt.ExamRevisionID.IsValid() || !stored.Receipt.SubmissionID.IsValid() || stored.Receipt.WorkspaceCursor < 0 ||
		!validWorkspaceSHA256(stored.Receipt.ManifestDigest) || stored.Receipt.SubmittedAt.IsZero() ||
		stored.ExamID != command.ExamID || stored.SittingID != command.SittingID || stored.ClassID != target.ClassID ||
		stored.CandidateUserID != target.CandidateUserID || stored.ParticipationID != target.ParticipationID ||
		stored.Generation != target.Generation || stored.ConnectionID != target.ConnectionID ||
		(!stored.Replayed && stored.Receipt.SubmissionID != proposed) || (stored.Replayed && stored.ConnectionClosed) {
		return SubmissionResult{}, unavailable(errors.New("inconsistent manager-ended Submission result"))
	}
	return SubmissionResult{Receipt: stored.Receipt, Provenance: model.ExamSubmissionManagerEndedAttempt,
		ExamID: stored.ExamID, SittingID: stored.SittingID, ClassID: stored.ClassID,
		CandidateUserID: stored.CandidateUserID, ParticipationID: stored.ParticipationID,
		Generation: stored.Generation, ConnectionID: stored.ConnectionID, Replayed: stored.Replayed}, nil
}
