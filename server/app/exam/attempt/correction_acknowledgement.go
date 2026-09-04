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

type AcknowledgeCorrectionCommand struct {
	Access                    WorkspaceMutationAccess
	CorrectionRevisionID      model.ExamRevisionID
	ExpectedCurrentRevisionID model.ExamRevisionID
	IdempotencyKey            string
}

type CorrectionAcknowledgementResult struct {
	CorrectionRevisionID model.ExamRevisionID
	CurrentRevisionID    model.ExamRevisionID
	AcknowledgedAt       model.OptionalTime
}

func (service *Service) AcknowledgeCorrection(ctx context.Context, call Call, command AcknowledgeCorrectionCommand) (CorrectionAcknowledgementResult, error) {
	selector, err := candidateSelector(call, command.Access.CandidateAccess)
	if err != nil || !command.Access.ParticipationID.IsValid() || command.Access.Generation < 1 ||
		!command.CorrectionRevisionID.IsValid() || !command.ExpectedCurrentRevisionID.IsValid() {
		return CorrectionAcknowledgementResult{}, invalid("correction_acknowledgement")
	}
	idempotency, err := prepareIdempotency(call, store.ExamAttemptCorrectionAcknowledgementOperation, command.IdempotencyKey, struct {
		AttemptID                 string `json:"exam_attempt_id"`
		ParticipationID           string `json:"participation_id"`
		Generation                int64  `json:"generation"`
		CorrectionRevisionID      string `json:"correction_revision_id"`
		ExpectedCurrentRevisionID string `json:"expected_current_revision_id"`
	}{selector.AttemptID.String(), command.Access.ParticipationID.String(), command.Access.Generation,
		command.CorrectionRevisionID.String(), command.ExpectedCurrentRevisionID.String()})
	if err != nil {
		return CorrectionAcknowledgementResult{}, err
	}
	input := store.ExamAttemptCorrectionAcknowledgement{Access: selector, ParticipationID: command.Access.ParticipationID,
		Generation: command.Access.Generation, CorrectionRevisionID: command.CorrectionRevisionID,
		ExpectedCurrentRevisionID: command.ExpectedCurrentRevisionID}
	target, err := service.deps.Persistence.ResolveCorrectionAcknowledgementTarget(ctx, input)
	if err != nil {
		return CorrectionAcknowledgementResult{}, mapStore(err)
	}
	if target == nil || target.AttemptID != selector.AttemptID || target.CandidateUserID != selector.CandidateUserID ||
		target.CorrectionRevisionID != command.CorrectionRevisionID || target.CurrentRevisionID != command.ExpectedCurrentRevisionID ||
		!target.SittingID.IsValid() || !target.ClassID.IsValid() {
		return CorrectionAcknowledgementResult{}, unavailable(errors.New("inconsistent correction acknowledgement target"))
	}
	resource := model.Resource{Type: model.ResourceExamSitting, ID: target.SittingID.String()}
	if err = service.deps.Auditor.RecordAuthorizationDecision(ctx, call, model.ActionExamSittingParticipate,
		resource, model.RoleScopeClass, target.ClassID.String(), true); err != nil {
		return CorrectionAcknowledgementResult{}, err
	}
	auditEvent, err := service.deps.Auditor.Prepare(ctx, call, model.ActionExamSittingParticipate,
		resource, model.RoleScopeClass,
		target.ClassID.String(), store.ExamAttemptCorrectionAcknowledgementOperation,
		map[string]any{"exam_attempt_id": target.AttemptID.String(), "correction_revision_id": target.CorrectionRevisionID.String(),
			"expected_current_revision_id": target.CurrentRevisionID.String()})
	if err != nil {
		return CorrectionAcknowledgementResult{}, err
	}
	input.AuditEvent = auditEvent
	stored, err := service.deps.Persistence.AcknowledgeCorrection(ctx, &input, idempotency)
	if err != nil {
		return CorrectionAcknowledgementResult{}, mapStore(err)
	}
	if stored == nil || stored.AttemptID != selector.AttemptID || stored.CorrectionRevisionID != command.CorrectionRevisionID ||
		stored.CurrentRevisionID != command.ExpectedCurrentRevisionID || stored.AcknowledgedAt.IsZero() {
		return CorrectionAcknowledgementResult{}, unavailable(errors.New("inconsistent correction acknowledgement outcome"))
	}
	return CorrectionAcknowledgementResult{CorrectionRevisionID: stored.CorrectionRevisionID,
		CurrentRevisionID: stored.CurrentRevisionID, AcknowledgedAt: model.OptionalTimeFrom(stored.AcknowledgedAt)}, nil
}
