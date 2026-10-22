// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package workspace

import (
	applicationidempotency "github.com/sudosylabs/proctor/server/app/idempotency"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

const (
	idempotencyOperationCreateDirectory = "exam.starter_workspace.directory.create"
	idempotencyOperationCreateFile      = "exam.starter_workspace.file.create"
	idempotencyOperationMoveEntry       = "exam.starter_workspace.entry.move"
	idempotencyOperationReplaceFile     = "exam.starter_workspace.file.replace"
	idempotencyOperationRemoveEntry     = "exam.starter_workspace.entry.remove"
)

func prepareWorkspaceIdempotency(invocation invocation.Invocation, operation, key string, examID model.ExamID, revision int64,
	expectedContentVersion model.WorkspaceContentVersion, entryID, path, mediaType string, size int64, sha256 string,
) (*store.CommandIdempotency, error) {
	return applicationidempotency.Require(invocation, operation, key, struct {
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
