// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package attempt

import (
	"encoding/json"
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
	var proposal json.RawMessage
	if command.InitialConfiguration != nil {
		canonical, err := command.InitialConfiguration.CanonicalAdmission()
		if err != nil {
			return nil, &Fault{Code: "request.invalid", Cause: err}
		}
		proposal = canonical
	}
	return prepareIdempotency(call, store.ExamAttemptConnectOperation, command.IdempotencyKey, struct {
		SittingID                string          `json:"exam_sitting_id"`
		SessionID                string          `json:"session_id"`
		ContinuityCredentialHash string          `json:"continuity_credential_hash"`
		SupportedManifests       []string        `json:"supported_attempt_configuration_manifests"`
		InitialConfiguration     json.RawMessage `json:"initial_configuration"`
	}{command.SittingID.String(), principal.SessionID.String(), model.HashToken(command.ContinuityCredential),
		append([]string(nil), command.SupportedConfigurationManifests...), proposal})
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

func prepareManagerEndIdempotency(call Call, command ManagerEndCommand) (*store.CommandIdempotency, error) {
	return prepareIdempotency(call, store.ExamSubmissionManagerEndOperation, command.IdempotencyKey, struct {
		ExamID                  string `json:"exam_id"`
		SittingID               string `json:"exam_sitting_id"`
		AttemptID               string `json:"exam_attempt_id"`
		ExpectedAttemptRevision int64  `json:"expected_attempt_revision"`
		PrivateReason           string `json:"private_reason"`
	}{command.ExamID.String(), command.SittingID.String(), command.AttemptID.String(),
		command.ExpectedAttemptRevision, command.PrivateReason})
}

func prepareSubmissionIdempotency(call Call, key string, attemptID model.ExamAttemptID,
	expectedCurrentRevisionID model.ExamRevisionID, expectedWorkspaceCursor, finalFocusLossSequence int64,
	browserActivity model.BrowserActivitySubmission,
) (*store.CommandIdempotency, error) {
	var finalSequence *int64
	if browserActivity.FinalSequence != nil {
		sequence := *browserActivity.FinalSequence
		finalSequence = &sequence
	}
	return prepareIdempotency(call, store.ExamSubmissionSealOperation, key, struct {
		AttemptID                 string                                   `json:"exam_attempt_id"`
		ExpectedCurrentRevisionID string                                   `json:"expected_current_revision_id"`
		WorkspaceCursor           int64                                    `json:"expected_workspace_cursor"`
		FinalFocusLossSequence    int64                                    `json:"final_focus_loss_sequence"`
		BrowserActivityState      model.BrowserActivitySubmissionState     `json:"browser_activity_state"`
		BrowserSourceSessionID    string                                   `json:"browser_source_session_id"`
		BrowserFinalSequence      *int64                                   `json:"browser_final_sequence"`
		BrowserGapReason          model.BrowserActivitySubmissionGapReason `json:"browser_gap_reason"`
	}{attemptID.String(), expectedCurrentRevisionID.String(), expectedWorkspaceCursor, finalFocusLossSequence,
		browserActivity.State, string(browserActivity.SourceSessionID), finalSequence, browserActivity.GapReason})
}
