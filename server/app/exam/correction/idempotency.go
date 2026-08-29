// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package correction

import (
	"errors"

	applicationidempotency "github.com/sudosylabs/proctor/server/app/idempotency"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

const idempotencyOperationApplyCorrection = "exam.sitting.correction.apply.v1"

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

func prepareStageIdempotency(call Call, command StageResourceContentCommand) (*store.CommandIdempotency, error) {
	return prepareIdempotency(call, store.ExamCorrectionResourceStageOperation, command.IdempotencyKey, struct {
		ExamID         string `json:"exam_id"`
		SittingID      string `json:"exam_sitting_id"`
		BaseRevisionID string `json:"base_revision_id"`
		Target         string `json:"target"`
		ResourceID     string `json:"resource_id,omitempty"`
		MediaType      string `json:"media_type"`
		Size           int64  `json:"size"`
		SHA256         string `json:"sha256"`
	}{command.ExamID.String(), command.SittingID.String(), command.BaseRevisionID.String(), string(command.Target), command.ResourceID.String(), string(command.MediaType), command.Size, command.ExpectedSHA256})
}

func prepareApplyIdempotency(call Call, command ApplyCommand) (*store.CommandIdempotency, error) {
	var browserPolicy string
	if command.BrowserPolicy.Present {
		encoded, err := model.EncodeBrowserPolicy(command.BrowserPolicy.Policy)
		if err != nil {
			return nil, &Fault{Code: "request.invalid", Cause: err}
		}
		browserPolicy = string(encoded)
	}
	resources := make([]struct {
		ResourceID          string `json:"resource_id"`
		DisplayName         string `json:"display_name"`
		DescriptionMarkdown string `json:"description_markdown"`
		StageID             string `json:"stage_id,omitempty"`
	}, len(command.Resources))
	for index, item := range command.Resources {
		resources[index].ResourceID = item.ResourceID.String()
		resources[index].DisplayName = item.DisplayName
		resources[index].DescriptionMarkdown = item.DescriptionMarkdown
		resources[index].StageID = item.StageID.String()
	}
	return prepareIdempotency(call, idempotencyOperationApplyCorrection, command.IdempotencyKey, struct {
		ExamID                    string `json:"exam_id"`
		SittingID                 string `json:"exam_sitting_id"`
		ExpectedSittingRevision   int64  `json:"expected_sitting_revision"`
		ExpectedCurrentRevisionID string `json:"expected_current_revision_id"`
		InstructionsPresent       bool   `json:"instructions_present"`
		InstructionsMarkdown      string `json:"instructions_markdown"`
		BrowserPolicyPresent      bool   `json:"browser_policy_present"`
		BrowserPolicy             string `json:"browser_policy"`
		Resources                 any    `json:"resources"`
		CandidateSummary          string `json:"candidate_summary"`
		AcknowledgementRequired   bool   `json:"acknowledgement_required"`
		PrivateReason             string `json:"private_reason"`
	}{command.ExamID.String(), command.SittingID.String(), command.ExpectedSittingRevision, command.ExpectedCurrentRevisionID.String(), command.Instructions.Present, command.Instructions.Markdown, command.BrowserPolicy.Present, browserPolicy, resources, command.CandidateSummary, command.AcknowledgementRequired, command.PrivateReason})
}
