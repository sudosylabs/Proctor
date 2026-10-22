// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package resource

import (
	applicationidempotency "github.com/sudosylabs/proctor/server/app/idempotency"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

const (
	idempotencyOperationAddResource            = "exam.resource.add"
	idempotencyOperationReplaceResourceContent = "exam.resource.content.replace"
	idempotencyOperationEditResourceMetadata   = "exam.resource.metadata.edit"
	idempotencyOperationReorderResources       = "exam.resource.reorder"
	idempotencyOperationRemoveResource         = "exam.resource.remove"
)

func prepareResourceIdempotency(invocation invocation.Invocation, operation, key string, examID model.ExamID, revision int64, resourceID, name, description string, media model.ExamResourceMediaType, size int64, sha string, ids []string) (*store.CommandIdempotency, error) {
	return applicationidempotency.Require(invocation, operation, key, struct {
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

func prepareMetadataIdempotency(invocation invocation.Invocation, command EditMetadataCommand) (*store.CommandIdempotency, error) {
	return applicationidempotency.Require(invocation, idempotencyOperationEditResourceMetadata, command.IdempotencyKey, struct {
		ExamID                string  `json:"exam_id"`
		ExpectedDraftRevision int64   `json:"expected_draft_revision"`
		ResourceID            string  `json:"resource_id"`
		DisplayName           *string `json:"display_name"`
		DescriptionMarkdown   *string `json:"description_markdown"`
	}{command.ExamID.String(), command.ExpectedDraftRevision, command.ResourceID.String(), command.DisplayName, command.DescriptionMarkdown})
}
