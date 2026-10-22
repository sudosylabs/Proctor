// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package attempt

import (
	"encoding/json"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/idempotency"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func prepareWorkspaceMutationIdempotency(invocation invocation.Invocation, key string, attemptID model.ExamAttemptID,
	operation model.AttemptWorkspaceMutationKind, semantic any,
) (*store.CommandIdempotency, error) {
	return idempotency.Require(invocation, store.ExamAttemptWorkspaceMutationOperation, key, struct {
		AttemptID string `json:"exam_attempt_id"`
		Operation string `json:"operation"`
		Command   any    `json:"command"`
	}{attemptID.String(), string(operation), semantic})
}

func prepareConnectIdempotency(invocation invocation.Invocation, command ConnectCommand) (*store.CommandIdempotency, error) {
	principal := invocation.Principal()
	var proposal json.RawMessage
	if command.InitialConfiguration != nil {
		canonical, err := command.InitialConfiguration.CanonicalAdmission()
		if err != nil {
			return nil, apperror.New("request.invalid").Wrap(err)
		}
		proposal = canonical
	}
	return idempotency.Require(invocation, store.ExamAttemptConnectOperation, command.IdempotencyKey, struct {
		SittingID                string                `json:"exam_sitting_id"`
		SessionID                string                `json:"session_id"`
		ContinuityCredentialHash string                `json:"continuity_credential_hash"`
		ConfigurationManifest    string                `json:"configuration_manifest_fingerprint"`
		InitialConfiguration     json.RawMessage       `json:"initial_configuration"`
		Security                 model.ConnectSecurity `json:"security"`
	}{command.SittingID.String(), principal.SessionID.String(), model.HashToken(command.ContinuityCredential),
		command.ConfigurationManifestFingerprint, proposal, command.Security})
}

func prepareReallowIdempotency(invocation invocation.Invocation, command ReallowCommand) (*store.CommandIdempotency, error) {
	return idempotency.Require(invocation, store.ExamAttemptReallowOperation, command.IdempotencyKey, struct {
		ExamID                  string `json:"exam_id"`
		SittingID               string `json:"exam_sitting_id"`
		AttemptID               string `json:"exam_attempt_id"`
		SuspensionID            string `json:"suspension_id"`
		ExpectedAttemptRevision int64  `json:"expected_attempt_revision"`
		PrivateReason           string `json:"private_reason"`
	}{command.ExamID.String(), command.SittingID.String(), command.AttemptID.String(), command.SuspensionID.String(), command.ExpectedAttemptRevision, command.PrivateReason})
}

func prepareManualSuspensionIdempotency(invocation invocation.Invocation, command ManualSuspensionCommand) (*store.CommandIdempotency, error) {
	return idempotency.Require(invocation, store.ExamAttemptSuspendOperation, command.IdempotencyKey, struct {
		ExamID                  string `json:"exam_id"`
		SittingID               string `json:"exam_sitting_id"`
		AttemptID               string `json:"exam_attempt_id"`
		ExpectedAttemptRevision int64  `json:"expected_attempt_revision"`
		PrivateReason           string `json:"private_reason"`
	}{command.ExamID.String(), command.SittingID.String(), command.AttemptID.String(),
		command.ExpectedAttemptRevision, command.PrivateReason})
}

func prepareManagerEndIdempotency(invocation invocation.Invocation, command ManagerEndCommand) (*store.CommandIdempotency, error) {
	return idempotency.Require(invocation, store.ExamSubmissionManagerEndOperation, command.IdempotencyKey, struct {
		ExamID                  string `json:"exam_id"`
		SittingID               string `json:"exam_sitting_id"`
		AttemptID               string `json:"exam_attempt_id"`
		ExpectedAttemptRevision int64  `json:"expected_attempt_revision"`
		PrivateReason           string `json:"private_reason"`
	}{command.ExamID.String(), command.SittingID.String(), command.AttemptID.String(),
		command.ExpectedAttemptRevision, command.PrivateReason})
}

func prepareSubmissionIdempotency(invocation invocation.Invocation, key string, attemptID model.ExamAttemptID, expectedCurrentRevisionID model.ExamRevisionID, expectedWorkspaceCursor, finalFocusLossSequence int64) (*store.CommandIdempotency, error) {
	return idempotency.Require(invocation, store.ExamSubmissionSealOperation, key, struct {
		AttemptID                 string `json:"exam_attempt_id"`
		ExpectedCurrentRevisionID string `json:"expected_current_revision_id"`
		WorkspaceCursor           int64  `json:"expected_workspace_cursor"`
		FinalFocusLossSequence    int64  `json:"final_focus_loss_sequence"`
	}{attemptID.String(), expectedCurrentRevisionID.String(), expectedWorkspaceCursor, finalFocusLossSequence})
}
