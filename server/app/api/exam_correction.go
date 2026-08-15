// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

const examSittingCorrectionUploadBodyLimit = model.ExamResourceMaximumBytes + 64*1024

type ExamSittingCorrectionApplication interface {
	StageExamSittingCorrectionResourceContent(context.Context, application.Invocation, application.StageExamSittingCorrectionResourceContentCommand) (application.ExamSittingCorrectionResourceStage, error)
	ApplyExamSittingCorrection(context.Context, application.Invocation, application.ApplyExamSittingCorrectionCommand) (application.ExamSittingCorrectionResult, error)
}

type examSittingCorrectionHTTPModule struct {
	application ExamSittingCorrectionApplication
}

type examSittingCorrectionStageMetadata struct {
	BaseRevisionID     string `json:"base_revision_id"`
	TargetKind         string `json:"target_kind"`
	ReplacesResourceID string `json:"replaces_resource_id,omitempty"`
	MediaType          string `json:"media_type"`
	Size               *int64 `json:"size"`
	SHA256             string `json:"sha256"`
}

type examSittingCorrectionStageResponse struct {
	StageID    string `json:"stage_id"`
	ResourceID string `json:"resource_id"`
	MediaType  string `json:"media_type"`
	Size       int64  `json:"size"`
	SHA256     string `json:"sha256"`
	ExpiresAt  string `json:"expires_at"`
}

type applyExamSittingCorrectionRequest struct {
	ExpectedSittingRevision   int64                                  `json:"expected_sitting_revision"`
	ExpectedCurrentRevisionID string                                 `json:"expected_current_revision_id"`
	InstructionsMarkdown      Optional[string]                       `json:"instructions_markdown"`
	Reason                    string                                 `json:"reason"`
	Resources                 []examSittingCorrectionResourceRequest `json:"resources"`
}

type examSittingCorrectionResourceRequest struct {
	ResourceID          string `json:"resource_id"`
	DisplayName         string `json:"display_name"`
	DescriptionMarkdown string `json:"description_markdown"`
	StageID             string `json:"stage_id,omitempty"`
}

type examSittingCorrectionResponse struct {
	ExamID             string `json:"exam_id"`
	ExamSittingID      string `json:"exam_sitting_id"`
	PreviousRevisionID string `json:"previous_revision_id"`
	RevisionID         string `json:"revision_id"`
	RevisionNumber     int64  `json:"revision_number"`
	SittingRevision    int64  `json:"sitting_revision"`
	SittingState       string `json:"sitting_state"`
	EffectiveAt        string `json:"effective_at"`
}

func (body *applyExamSittingCorrectionRequest) UnmarshalJSON(encoded []byte) error {
	type wire applyExamSittingCorrectionRequest
	var decoded wire
	if err := decodeDuplicateFreeExamCorrectionObject(encoded, &decoded); err != nil {
		return err
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &members); err != nil {
		return err
	}
	resources, exists := members["resources"]
	if !exists || bytes.Equal(bytes.TrimSpace(resources), []byte("null")) {
		return errors.New("resources is required and must be an array")
	}
	if decoded.InstructionsMarkdown.IsNull() {
		return errors.New("instructions_markdown must be a string when present")
	}
	if len(decoded.Resources) > 10 {
		return errors.New("resources exceeds the maximum of 10")
	}
	*body = applyExamSittingCorrectionRequest(decoded)
	return nil
}

func (item *examSittingCorrectionResourceRequest) UnmarshalJSON(encoded []byte) error {
	type wire examSittingCorrectionResourceRequest
	var decoded wire
	if err := decodeDuplicateFreeExamCorrectionObject(encoded, &decoded); err != nil {
		return err
	}
	*item = examSittingCorrectionResourceRequest(decoded)
	return nil
}

func decodeDuplicateFreeExamCorrectionObject(encoded []byte, target any) error {
	if !utf8.Valid(encoded) {
		return errors.New("Exam Sitting correction must be valid UTF-8")
	}
	if err := rejectDuplicateTopLevelJSONMembers(encoded); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return errors.New("Exam Sitting correction contains trailing JSON")
	}
	return nil
}

func examSittingCorrectionResource(app ExamSittingCorrectionApplication) resource {
	module := examSittingCorrectionHTTPModule{application: app}
	stages := apiPath(literal("exams"), canonicalID("exam_id"), literal("sittings"), canonicalID("exam_sitting_id"), literal("correction-resource-stages"))
	corrections := apiPath(literal("exams"), canonicalID("exam_id"), literal("sittings"), canonicalID("exam_sitting_id"), literal("corrections"))
	return newResource(
		"exam-sitting-corrections",
		idempotentProtocolRoute(IdempotencyRequired, examSittingCorrectionUploadBodyLimit, "exam-sitting-correction-resource-stage", RouteProtocolStreamingUpload, AuthPrincipalRequired, http.MethodPost, stages, examSittingCorrectionStageErrorCodes(), module.stage),
		idempotentPrincipalRoute(IdempotencyRequired, http.MethodPost, corrections, examSittingCorrectionApplyErrorCodes(), module.apply),
	)
}

func examSittingCorrectionMutationErrorCodes(specific ...string) []string {
	common := []string{
		"request.invalid", "resource.not_found", "exam.sitting.correction.invalid",
		"exam.sitting.correction.conflict", "exam.sitting.correction.unavailable",
		"exam.archived", "exam.sitting.revision_conflict", "exam.sitting.state_conflict", "exam.sitting.deadline_reached",
	}
	common = append(common, specific...)
	return academicMutationErrorCodes(append(common,
		"idempotency.key_required", "idempotency.invalid_key", "idempotency.conflict", "idempotency.in_progress")...)
}

func examSittingCorrectionStageErrorCodes() []string {
	return examSittingCorrectionMutationErrorCodes(
		"exam.sitting.correction.invalid_content",
		"exam.sitting.correction.stage_invalid",
	)
}

func examSittingCorrectionApplyErrorCodes() []string {
	return examSittingCorrectionMutationErrorCodes(
		"exam.sitting.correction.no_changes",
		"exam.sitting.correction.manifest_invalid",
		"exam.sitting.correction.stage_invalid",
		"exam.resource.limit",
	)
}

func (module examSittingCorrectionHTTPModule) stage(request operationRequest) (protocolResult, error) {
	examID, sittingID, err := examSittingCorrectionIDs(request)
	if err != nil {
		return protocolResult{}, err
	}
	var metadata examSittingCorrectionStageMetadata
	content, err := decodeExamResourceMultipart(request.request, &metadata)
	if err != nil {
		return protocolResult{}, invalidRequestError("multipart", err)
	}
	if metadata.Size == nil {
		return protocolResult{}, invalidRequestError("multipart", errors.New("size is required"))
	}
	baseRevisionID, err := model.ParseExamRevisionID(metadata.BaseRevisionID)
	if err != nil {
		return protocolResult{}, invalidRequestError("base_revision_id", err)
	}
	target := application.ExamSittingCorrectionResourceTarget(metadata.TargetKind)
	var resourceID model.ExamResourceID
	switch target {
	case application.ExamSittingCorrectionResourceAddition:
		if metadata.ReplacesResourceID != "" {
			return protocolResult{}, invalidRequestError("replaces_resource_id", errors.New("must be omitted for an addition"))
		}
	case application.ExamSittingCorrectionResourceReplacement:
		if metadata.ReplacesResourceID == "" {
			return protocolResult{}, invalidRequestError("replaces_resource_id", errors.New("is required for a replacement"))
		}
		resourceID, err = model.ParseExamResourceID(metadata.ReplacesResourceID)
		if err != nil {
			return protocolResult{}, invalidRequestError("replaces_resource_id", err)
		}
	default:
		return protocolResult{}, invalidRequestError("target_kind", errors.New("must be addition or replacement"))
	}
	result, err := module.application.StageExamSittingCorrectionResourceContent(request.context, request.invocation(), application.StageExamSittingCorrectionResourceContentCommand{
		ExamID: examID, SittingID: sittingID, BaseRevisionID: baseRevisionID, Target: target, ResourceID: resourceID,
		MediaType: model.ExamResourceMediaType(metadata.MediaType), Body: content, Size: *metadata.Size,
		ExpectedSHA256: metadata.SHA256, IdempotencyKey: request.idempotencyKey,
	})
	if err != nil {
		return protocolResult{}, err
	}
	response := examSittingCorrectionStageResponse{
		StageID: result.StageID.String(), ResourceID: result.ResourceID.String(), MediaType: string(result.MediaType),
		Size: result.Size, SHA256: result.SHA256, ExpiresAt: model.TimeUTC(result.ExpiresAt).Format(time.RFC3339Nano),
	}
	return streamingUploadProtocolResult(http.StatusCreated, response), nil
}

func (module examSittingCorrectionHTTPModule) apply(request operationRequest) (operationResult, error) {
	examID, sittingID, err := examSittingCorrectionIDs(request)
	if err != nil {
		return operationResult{}, err
	}
	var body applyExamSittingCorrectionRequest
	if err = request.decodeJSON(&body, "applyExamSittingCorrection"); err != nil {
		return operationResult{}, err
	}
	if body.ExpectedSittingRevision < 1 || !validExamSittingCorrectionReason(body.Reason) {
		return operationResult{}, invalidRequestError("correction", errors.New("expected revision and reason are invalid"))
	}
	currentRevisionID, err := model.ParseExamRevisionID(body.ExpectedCurrentRevisionID)
	if err != nil {
		return operationResult{}, invalidRequestError("expected_current_revision_id", err)
	}
	manifest := make([]application.ExamSittingCorrectionResourceManifestItem, len(body.Resources))
	resourceIDs := make(map[model.ExamResourceID]struct{}, len(body.Resources))
	stageIDs := make(map[model.ExamCorrectionResourceStageID]struct{}, len(body.Resources))
	for index, item := range body.Resources {
		resourceID, parseErr := model.ParseExamResourceID(item.ResourceID)
		if parseErr != nil {
			return operationResult{}, invalidRequestError("resources", parseErr)
		}
		if _, exists := resourceIDs[resourceID]; exists {
			return operationResult{}, invalidRequestError("resources", errors.New("resource_id values must be unique"))
		}
		resourceIDs[resourceID] = struct{}{}
		var stageID model.ExamCorrectionResourceStageID
		if item.StageID != "" {
			stageID, parseErr = model.ParseExamCorrectionResourceStageID(item.StageID)
			if parseErr != nil {
				return operationResult{}, invalidRequestError("resources", parseErr)
			}
			if _, exists := stageIDs[stageID]; exists {
				return operationResult{}, invalidRequestError("resources", errors.New("stage_id values must be unique"))
			}
			stageIDs[stageID] = struct{}{}
		}
		manifest[index] = application.ExamSittingCorrectionResourceManifestItem{
			ResourceID: resourceID, DisplayName: item.DisplayName,
			DescriptionMarkdown: item.DescriptionMarkdown, StageID: stageID,
		}
	}
	instructions := application.ExamSittingCorrectionInstructions{Present: body.InstructionsMarkdown.IsSet()}
	if value := body.InstructionsMarkdown.ValuePointer(); value != nil {
		instructions.Markdown = *value
	}
	result, err := module.application.ApplyExamSittingCorrection(request.context, request.invocation(), application.ApplyExamSittingCorrectionCommand{
		ExamID: examID, SittingID: sittingID, ExpectedSittingRevision: body.ExpectedSittingRevision,
		ExpectedCurrentRevisionID: currentRevisionID, Instructions: instructions, Resources: manifest,
		PrivateReason: body.Reason, IdempotencyKey: request.idempotencyKey,
	})
	if err != nil {
		return operationResult{}, err
	}
	response := examSittingCorrectionResponse{
		ExamID: result.ExamID.String(), ExamSittingID: result.SittingID.String(),
		PreviousRevisionID: result.PreviousRevisionID.String(), RevisionID: result.RevisionID.String(),
		RevisionNumber: result.RevisionNumber, SittingRevision: result.SittingRevision,
		SittingState: string(result.SittingState), EffectiveAt: model.TimeUTC(result.EffectiveAt).Format(time.RFC3339Nano),
	}
	return jsonResult(http.StatusCreated, response), nil
}

func examSittingCorrectionIDs(request operationRequest) (model.ExamID, model.ExamSittingID, error) {
	examID, err := examResourceExamID(request)
	if err != nil {
		return "", "", err
	}
	rawSittingID, err := request.params.RequireExamSittingId()
	if err != nil {
		return "", "", err
	}
	sittingID, err := model.ParseExamSittingID(rawSittingID)
	if err != nil {
		return "", "", invalidRequestError("exam_sitting_id", err)
	}
	return examID, sittingID, nil
}

func validExamSittingCorrectionReason(reason string) bool {
	return utf8.ValidString(reason) && strings.TrimSpace(reason) == reason && utf8.RuneCountInString(reason) >= 1 &&
		utf8.RuneCountInString(reason) <= 1000 && len(reason) <= 4000
}
