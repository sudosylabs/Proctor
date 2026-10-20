// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package review

import (
	"errors"

	"github.com/sudosylabs/proctor/server/app/apperror"
	applicationidempotency "github.com/sudosylabs/proctor/server/app/idempotency"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func prepareIdempotency(call Call, operation, key string, semantic any) (*store.CommandIdempotency, error) {
	if key == "" {
		return nil, apperror.New("idempotency.key_required")
	}
	prepared, err := applicationidempotency.Prepare(call.Principal().UserID, operation, key, semantic)
	var encodingError *applicationidempotency.SemanticEncodingError
	switch {
	case errors.Is(err, applicationidempotency.ErrInvalidPrincipal):
		return nil, apperror.New("idempotency.key_invalid").Wrap(err)
	case errors.As(err, &encodingError):
		return nil, apperror.New("request.invalid").Wrap(err)
	default:
		return prepared, err
	}
}

func prepareDecisionIdempotency(call Call, command SaveDecisionCommand) (*store.CommandIdempotency, error) {
	return prepareIdempotency(call, store.ExamIntegrityReviewDecisionOperation, command.IdempotencyKey, struct {
		SubmissionID             string `json:"submission_id"`
		ReviewID                 string `json:"submission_review_id,omitempty"`
		FlagID                   string `json:"integrity_flag_id"`
		ExpectedReviewRevision   int64  `json:"expected_review_revision"`
		ExpectedDecisionRevision int64  `json:"expected_decision_revision"`
		Outcome                  string `json:"outcome"`
		PrivateRationale         string `json:"private_rationale"`
	}{command.SubmissionID.String(), command.ReviewID.String(), command.FlagID.String(), command.ExpectedReviewRevision, command.ExpectedDecisionRevision, string(command.Outcome), command.PrivateRationale})
}

func prepareDraftIdempotency(call Call, command UpdateDraftCommand) (*store.CommandIdempotency, error) {
	return prepareIdempotency(call, store.ExamIntegrityReviewDraftOperation, command.IdempotencyKey, struct {
		SubmissionID           string `json:"submission_id"`
		ReviewID               string `json:"submission_review_id,omitempty"`
		ExpectedReviewRevision int64  `json:"expected_review_revision"`
		ManagerNotes           string `json:"manager_notes"`
		StudentRemarksMarkdown string `json:"student_remarks_markdown"`
	}{command.SubmissionID.String(), command.ReviewID.String(), command.ExpectedReviewRevision, command.ManagerNotes, command.StudentRemarksMarkdown})
}

func prepareTerminalIdempotency(call Call, operation, key string, submissionID model.SubmissionID,
	reviewID model.SubmissionReviewID, expectedRevision int64,
) (*store.CommandIdempotency, error) {
	return prepareIdempotency(call, operation, key, struct {
		SubmissionID     string `json:"submission_id"`
		ReviewID         string `json:"submission_review_id"`
		ExpectedRevision int64  `json:"expected_review_revision"`
	}{submissionID.String(), reviewID.String(), expectedRevision})
}
