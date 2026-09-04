// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package workspace

import (
	"errors"

	applicationidempotency "github.com/sudosylabs/proctor/server/app/idempotency"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

const (
	idempotencyOperationCreateDirectory = "exam.starter_workspace.directory.create.v1"
	idempotencyOperationCreateFile      = "exam.starter_workspace.file.create.v1"
	idempotencyOperationMoveEntry       = "exam.starter_workspace.entry.move.v1"
	idempotencyOperationReplaceFile     = "exam.starter_workspace.file.replace.v1"
	idempotencyOperationRemoveEntry     = "exam.starter_workspace.entry.remove.v1"
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

func prepareWorkspaceIdempotency(call Call, operation, key string, examID model.ExamID, revision int64,
	expectedContentVersion model.WorkspaceContentVersion, entryID, path, mediaType string, size int64, sha256 string,
) (*store.CommandIdempotency, error) {
	return prepareIdempotency(call, operation, key, struct {
		ExamID                 string `json:"exam_id"`
		ExpectedDraftRevision  int64  `json:"expected_draft_revision"`
		ExpectedContentVersion string `json:"expected_content_version,omitempty"`
		EntryID                string `json:"entry_id,omitempty"`
		Path                   string `json:"path,omitempty"`
		MediaType              string `json:"media_type,omitempty"`
		Size                   int64  `json:"size,omitempty"`
		SHA256                 string `json:"sha256,omitempty"`
	}{examID.String(), revision, expectedContentVersion.String(), entryID, path, mediaType, size, sha256})
}
