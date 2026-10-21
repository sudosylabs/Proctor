// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package workspace

import (
	"crypto/sha256"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	applicationidempotency "github.com/sudosylabs/proctor/server/app/idempotency"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func assertPreparedIdempotency(t *testing.T, got *store.CommandIdempotency, userID model.UserID, operation, key, document string) {
	t.Helper()
	wantKey := sha256.Sum256([]byte(key))
	wantFingerprint := sha256.Sum256([]byte(operation + "\x00v1\x00" + document))
	require.NotNil(t, got, "prepared idempotency")
	require.Equal(t, userID, got.UserID, "prepared idempotency")
	require.Equal(t, operation, got.Operation, "prepared idempotency")
	require.Equal(t, wantKey, got.KeyDigest, "prepared idempotency")
	require.Equal(t, 1, got.FingerprintVersion, "prepared idempotency")
	require.Equal(t, wantFingerprint, got.Fingerprint, "prepared idempotency")
	require.Equal(t, 1, got.OutcomeVersion, "prepared idempotency")
	require.Equal(t, 24*time.Hour, got.Retention, "prepared idempotency")
	require.Equal(t, 2*time.Second, got.Wait, "prepared idempotency")
}

func TestIdempotencyDocumentsAndStoreBoundaryCompatibility(t *testing.T) {
	t.Parallel()
	userID := model.NewUserID()
	invocation := invocation.New(model.Principal{UserID: userID}, model.RequestMetadata{})
	examID, entryID := model.NewExamID(), model.NewStarterWorkspaceEntryID().String()
	version := model.WorkspaceContentVersion("aaaaaaaaaaaaaaaaaaaaaaaaaa")
	tests := []struct {
		operation, key, version, entryID, path, mediaType, sha, document string
		size                                                             int64
	}{
		{idempotencyOperationCreateDirectory, "directory-key", "", "", "src", "", "", fmt.Sprintf(`{"exam_id":%q,"expected_draft_revision":2,"path":"src"}`, examID), 0},
		{idempotencyOperationCreateFile, "file-key", "", "", "main.go", "text/plain", "digest", fmt.Sprintf(`{"exam_id":%q,"expected_draft_revision":2,"path":"main.go","media_type":"text/plain","size":12,"sha256":"digest"}`, examID), 12},
		{idempotencyOperationMoveEntry, "move-key", "", entryID, "src/main.go", "", "", fmt.Sprintf(`{"exam_id":%q,"expected_draft_revision":2,"entry_id":%q,"path":"src/main.go"}`, examID, entryID), 0},
		{idempotencyOperationReplaceFile, "replace-key", version.String(), entryID, "", "text/plain", "digest", fmt.Sprintf(`{"exam_id":%q,"expected_draft_revision":2,"expected_content_version":%q,"entry_id":%q,"media_type":"text/plain","size":12,"sha256":"digest"}`, examID, version, entryID), 12},
		{idempotencyOperationRemoveEntry, "remove-key", "", entryID, "", "", "", fmt.Sprintf(`{"exam_id":%q,"expected_draft_revision":2,"entry_id":%q}`, examID, entryID), 0},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			prepared, err := prepareWorkspaceIdempotency(invocation, test.operation, test.key, examID, 2,
				model.WorkspaceContentVersion(test.version), test.entryID, test.path, test.mediaType, test.size, test.sha)
			require.NoError(t, err)
			assertPreparedIdempotency(t, prepared, userID, test.operation, test.key, test.document)
		})
	}
}

func TestReplacementIdempotencyIncludesExpectedContentVersion(t *testing.T) {
	t.Parallel()
	invocation := invocation.New(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{})
	examID, entryID := model.NewExamID(), model.NewStarterWorkspaceEntryID().String()
	first, err := prepareWorkspaceIdempotency(invocation, idempotencyOperationReplaceFile, "replace-key", examID, 2,
		model.WorkspaceContentVersion("aaaaaaaaaaaaaaaaaaaaaaaaaa"), entryID, "", "text/plain", 1, "checksum")
	require.NoError(t, err)
	second, err := prepareWorkspaceIdempotency(invocation, idempotencyOperationReplaceFile, "replace-key", examID, 2,
		model.WorkspaceContentVersion("bbbbbbbbbbbbbbbbbbbbbbbbbb"), entryID, "", "text/plain", 1, "checksum")
	require.NoError(t, err)
	require.NotEqual(t, second.Fingerprint, first.Fingerprint, "expected content version was omitted")
}

func TestWorkspaceIdempotencyOperationCompatibility(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		idempotencyOperationCreateDirectory: "exam.starter_workspace.directory.create.v1",
		idempotencyOperationCreateFile:      "exam.starter_workspace.file.create.v1",
		idempotencyOperationMoveEntry:       "exam.starter_workspace.entry.move.v1",
		idempotencyOperationReplaceFile:     "exam.starter_workspace.file.replace.v1",
		idempotencyOperationRemoveEntry:     "exam.starter_workspace.entry.remove.v1",
	}
	require.Len(t, tests, 5)
	for operation, want := range tests {
		assert.Equal(t, want, operation, "operation")
	}
}

func TestPrepareIdempotencyRequiresKey(t *testing.T) {
	t.Parallel()
	invocation := invocation.New(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{})
	_, err := applicationidempotency.Require(invocation, "operation", "", struct{}{})
	fault, ok := apperror.As(err)
	require.True(t, ok)
	require.Equal(t, "idempotency.key_required", fault.Code())
}
