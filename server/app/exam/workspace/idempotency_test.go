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
	"reflect"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func assertStoreBoundaryCommand(t *testing.T, got, want *store.CommandIdempotency) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Store idempotency = %#v; want %#v", got, want)
	}
}

func assertPreparedIdempotency(t *testing.T, got *store.CommandIdempotency, userID model.UserID, operation, key, document string) {
	t.Helper()
	wantKey := sha256.Sum256([]byte(key))
	wantFingerprint := sha256.Sum256([]byte(operation + "\x00v1\x00" + document))
	if got == nil || got.UserID != userID || got.Operation != operation || got.KeyDigest != wantKey ||
		got.FingerprintVersion != 1 || got.Fingerprint != wantFingerprint || got.OutcomeVersion != 1 ||
		got.Retention != 24*time.Hour || got.Wait != 2*time.Second {
		t.Fatalf("prepared idempotency = %#v; want user=%s operation=%q key=%x fingerprint=%x versions=1/1 retention=24h wait=2s",
			got, userID, operation, wantKey, wantFingerprint)
	}
}

func TestIdempotencyDocumentsAndStoreBoundaryCompatibility(t *testing.T) {
	t.Parallel()
	userID := model.NewUserID()
	call := NewCall(model.Principal{UserID: userID}, model.RequestMetadata{})
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
		prepared, err := prepareWorkspaceIdempotency(call, test.operation, test.key, examID, 2,
			model.WorkspaceContentVersion(test.version), test.entryID, test.path, test.mediaType, test.size, test.sha)
		if err != nil {
			t.Fatal(err)
		}
		assertPreparedIdempotency(t, prepared, userID, test.operation, test.key, test.document)
	}
}

func TestReplacementIdempotencyIncludesExpectedContentVersion(t *testing.T) {
	t.Parallel()
	call := NewCall(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{})
	examID, entryID := model.NewExamID(), model.NewStarterWorkspaceEntryID().String()
	first, err := prepareWorkspaceIdempotency(call, idempotencyOperationReplaceFile, "replace-key", examID, 2,
		model.WorkspaceContentVersion("aaaaaaaaaaaaaaaaaaaaaaaaaa"), entryID, "", "text/plain", 1, "checksum")
	if err != nil {
		t.Fatal(err)
	}
	second, err := prepareWorkspaceIdempotency(call, idempotencyOperationReplaceFile, "replace-key", examID, 2,
		model.WorkspaceContentVersion("bbbbbbbbbbbbbbbbbbbbbbbbbb"), entryID, "", "text/plain", 1, "checksum")
	if err != nil {
		t.Fatal(err)
	}
	if first.Fingerprint == second.Fingerprint {
		t.Fatal("expected content version was omitted")
	}
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
	if len(tests) != 5 {
		t.Fatalf("operation set collapsed: %#v", tests)
	}
	for operation, want := range tests {
		if operation != want {
			t.Errorf("operation = %q; want %q", operation, want)
		}
	}
}

func TestPrepareIdempotencyRequiresKey(t *testing.T) {
	t.Parallel()
	call := NewCall(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{})
	_, err := prepareIdempotency(call, "operation", "", struct{}{})
	if fault, ok := err.(*Fault); !ok || fault.Code != "idempotency.key_required" {
		t.Fatalf("error = %v", err)
	}
}
