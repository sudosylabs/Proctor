// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package resource

import (
	"errors"

	applicationidempotency "github.com/sudosylabs/proctor/server/app/idempotency"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

const (
	idempotencyOperationAddResource            = "exam.resource.add.v1"
	idempotencyOperationReplaceResourceContent = "exam.resource.content.replace.v1"
	idempotencyOperationEditResourceMetadata   = "exam.resource.metadata.edit.v1"
	idempotencyOperationReorderResources       = "exam.resource.reorder.v1"
	idempotencyOperationRemoveResource         = "exam.resource.remove.v1"
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

func prepareResourceIdempotency(call Call, operation, key string, examID model.ExamID, revision int64, resourceID, name, description string, media model.ExamResourceMediaType, size int64, sha string, ids []string) (*store.CommandIdempotency, error) {
	return prepareIdempotency(call, operation, key, struct {
		ExamID                string   `json:"exam_id"`
		ExpectedDraftRevision int64    `json:"expected_draft_revision"`
		ResourceID            string   `json:"resource_id,omitempty"`
		DisplayName           string   `json:"display_name,omitempty"`
		DescriptionMarkdown   string   `json:"description_markdown,omitempty"`
		MediaType             string   `json:"media_type,omitempty"`
		Size                  int64    `json:"size,omitempty"`
		SHA256                string   `json:"sha256,omitempty"`
		ResourceIDs           []string `json:"resource_ids,omitempty"`
	}{examID.String(), revision, resourceID, name, description, string(media), size, sha, ids})
}

func prepareMetadataIdempotency(call Call, command EditMetadataCommand) (*store.CommandIdempotency, error) {
	return prepareIdempotency(call, idempotencyOperationEditResourceMetadata, command.IdempotencyKey, struct {
		ExamID                string  `json:"exam_id"`
		ExpectedDraftRevision int64   `json:"expected_draft_revision"`
		ResourceID            string  `json:"resource_id"`
		DisplayName           *string `json:"display_name"`
		DescriptionMarkdown   *string `json:"description_markdown"`
	}{command.ExamID.String(), command.ExpectedDraftRevision, command.ResourceID.String(), command.DisplayName, command.DescriptionMarkdown})
}
