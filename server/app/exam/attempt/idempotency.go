// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package attempt

import (
	"errors"

	applicationidempotency "github.com/sudosylabs/proctor/server/app/idempotency"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func prepareIdempotency(call Call, operation, key string, semantic any) (*store.CommandIdempotency, error) {
	if key == "" {
		return nil, &Fault{Code: "idempotency.key_required"}
	}
	prepared, err := applicationidempotency.Prepare(call.Principal().UserID, operation, key, semantic)
	var encodingError *applicationidempotency.SemanticEncodingError
	switch {
	case errors.Is(err, applicationidempotency.ErrInvalidPrincipal):
		return nil, &Fault{Code: "idempotency.invalid_key", Cause: err}
	case errors.As(err, &encodingError):
		return nil, &Fault{Code: "request.invalid", Cause: err}
	default:
		return prepared, err
	}
}

func prepareWorkspaceMutationIdempotency(call Call, key string, attemptID model.ExamAttemptID,
	operation model.AttemptWorkspaceMutationKind, semantic any,
) (*store.CommandIdempotency, error) {
	// Origin deliberately remains outside the version-1 durable fingerprint.
	// Candidate and execution-host mutations commit the same Workspace change,
	// and post-commit effects are not repeated on replay. Keeping the historical
	// document also preserves retries across rolling deployments and terminals
	// that were already observing an event.
	return prepareIdempotency(call, store.ExamAttemptWorkspaceMutationOperation, key, struct {
		AttemptID string `json:"exam_attempt_id"`
		Operation string `json:"operation"`
		Command   any    `json:"command"`
	}{attemptID.String(), string(operation), semantic})
}

func prepareConnectIdempotency(call Call, command ConnectCommand) (*store.CommandIdempotency, error) {
	principal := call.Principal()
	return prepareIdempotency(call, store.ExamAttemptConnectOperation, command.IdempotencyKey, struct {
		SittingID                string `json:"exam_sitting_id"`
		SessionID                string `json:"session_id"`
		ContinuityCredentialHash string `json:"continuity_credential_hash"`
	}{command.SittingID.String(), principal.SessionID.String(), model.HashToken(command.ContinuityCredential)})
}

func prepareReallowIdempotency(call Call, command ReallowCommand) (*store.CommandIdempotency, error) {
	return prepareIdempotency(call, store.ExamAttemptReallowOperation, command.IdempotencyKey, struct {
		ExamID                  string `json:"exam_id"`
		SittingID               string `json:"exam_sitting_id"`
		AttemptID               string `json:"exam_attempt_id"`
		SuspensionID            string `json:"suspension_id"`
		ExpectedAttemptRevision int64  `json:"expected_attempt_revision"`
		PrivateReason           string `json:"private_reason"`
	}{command.ExamID.String(), command.SittingID.String(), command.AttemptID.String(), command.SuspensionID.String(), command.ExpectedAttemptRevision, command.PrivateReason})
}

func prepareSubmissionIdempotency(call Call, key string, attemptID model.ExamAttemptID,
	expectedWorkspaceCursor, finalFocusLossSequence int64,
) (*store.CommandIdempotency, error) {
	return prepareIdempotency(call, store.ExamSubmissionSealOperation, key, struct {
		AttemptID              string `json:"exam_attempt_id"`
		WorkspaceCursor        int64  `json:"expected_workspace_cursor"`
		FinalFocusLossSequence int64  `json:"final_focus_loss_sequence"`
	}{attemptID.String(), expectedWorkspaceCursor, finalFocusLossSequence})
}
