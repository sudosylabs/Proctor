// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package attempt

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
	wantFingerprint := sha256.Sum256([]byte(operation + "\x00" + document))
	require.NotNil(t, got, "prepared idempotency")
	require.Equal(t, userID, got.UserID, "prepared idempotency")
	require.Equal(t, operation, got.Operation, "prepared idempotency")
	require.Equal(t, wantKey, got.KeyDigest, "prepared idempotency")
	require.Equal(t, wantFingerprint, got.Fingerprint, "prepared idempotency")
	require.Equal(t, 24*time.Hour, got.Retention, "prepared idempotency")
	require.Equal(t, 2*time.Second, got.Wait, "prepared idempotency")
}

func TestWorkspaceMutationFingerprintCompatibility(t *testing.T) {
	t.Parallel()
	userID, attemptID := model.NewUserID(), model.NewExamAttemptID()
	entryID := model.NewAttemptWorkspaceEntryID()
	version := model.WorkspaceContentVersion("aaaaaaaaaaaaaaaaaaaaaaaaaa")
	invocation := invocation.New(model.Principal{UserID: userID}, model.RequestMetadata{})
	tests := []struct {
		name      string
		operation model.AttemptWorkspaceMutationKind
		semantic  any
		document  string
	}{
		{
			name: "create directory", operation: model.AttemptWorkspaceMutationCreateDirectory,
			semantic: struct {
				Path string `json:"path"`
			}{"src"}, document: `{"path":"src"}`,
		},
		{
			name: "create file", operation: model.AttemptWorkspaceMutationCreateFile,
			semantic: struct {
				Path, MediaType, SHA256 string
				Size                    int64
			}{"main.go", "application/octet-stream", "digest", 12},
			document: `{"Path":"main.go","MediaType":"application/octet-stream","SHA256":"digest","Size":12}`,
		},
		{
			name: "replace file", operation: model.AttemptWorkspaceMutationReplaceFile,
			semantic: struct {
				EntryID, Path, Version, MediaType, SHA256 string
				Size                                      int64
			}{entryID.String(), "main.go", version.String(), "application/octet-stream", "digest", 12},
			document: fmt.Sprintf(`{"EntryID":%q,"Path":"main.go","Version":%q,"MediaType":"application/octet-stream","SHA256":"digest","Size":12}`,
				entryID, version),
		},
		{
			name: "move", operation: model.AttemptWorkspaceMutationMoveEntry,
			semantic: struct{ EntryID, ExpectedPath, DestinationPath string }{entryID.String(), "old", "new"},
			document: fmt.Sprintf(`{"EntryID":%q,"ExpectedPath":"old","DestinationPath":"new"}`, entryID),
		},
		{
			name: "delete", operation: model.AttemptWorkspaceMutationDeleteEntry,
			semantic: struct{ EntryID, ExpectedPath, ExpectedContentVersion string }{entryID.String(), "old", version.String()},
			document: fmt.Sprintf(`{"EntryID":%q,"ExpectedPath":"old","ExpectedContentVersion":%q}`, entryID, version),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			prepared, err := prepareWorkspaceMutationIdempotency(invocation, "execution-key", attemptID, test.operation, test.semantic)
			require.NoError(t, err)
			outer := fmt.Sprintf(`{"exam_attempt_id":%q,"operation":%q,"command":%s}`,
				attemptID, test.operation, test.document)
			assertPreparedIdempotency(t, prepared, userID, store.ExamAttemptWorkspaceMutationOperation,
				"execution-key", outer)
		})
	}
}

func TestCommandIdempotencyDocumentsAndStoreBoundaryCompatibility(t *testing.T) {
	t.Parallel()
	userID, sessionID := model.NewUserID(), model.NewSessionID()
	invocation := invocation.New(model.Principal{UserID: userID, SessionID: sessionID}, model.RequestMetadata{})
	sittingID, attemptID := model.NewExamSittingID(), model.NewExamAttemptID()
	credential := model.NewCredentialToken()

	manifest := model.CurrentAttemptConfigurationManifestFingerprint()
	security := model.ConnectSecurity{Kind: "preflight", PreflightID: "fixture-preflight", ReportDigest: model.SHA256Fingerprint([]byte("fixture-report"))}
	connected, err := prepareConnectIdempotency(invocation, ConnectCommand{Security: security, SittingID: sittingID,
		ContinuityCredential: credential, ConfigurationManifestFingerprint: manifest, IdempotencyKey: "connect-key"})
	require.NoError(t, err)
	assertPreparedIdempotency(t, connected, userID, store.ExamAttemptConnectOperation, "connect-key",
		fmt.Sprintf(`{"exam_sitting_id":%q,"session_id":%q,"continuity_credential_hash":%q,"configuration_manifest_fingerprint":%q,"initial_configuration":null,"security":{"kind":"preflight","preflight_id":"fixture-preflight","report_digest":%q}}`,
			sittingID, sessionID, model.HashToken(credential), manifest, security.ReportDigest))

	examID, suspensionID := model.NewExamID(), model.NewAttemptSuspensionID()
	reallowed, err := prepareReallowIdempotency(invocation, ReallowCommand{ExamID: examID, SittingID: sittingID,
		AttemptID: attemptID, SuspensionID: suspensionID, ExpectedAttemptRevision: 3,
		PrivateReason: "reviewed", IdempotencyKey: "reallow-key"})
	require.NoError(t, err)
	assertPreparedIdempotency(t, reallowed, userID, store.ExamAttemptReallowOperation, "reallow-key",
		fmt.Sprintf(`{"exam_id":%q,"exam_sitting_id":%q,"exam_attempt_id":%q,"suspension_id":%q,"expected_attempt_revision":3,"private_reason":"reviewed"}`,
			examID, sittingID, attemptID, suspensionID))

	revisionID := model.NewExamRevisionID()
	submitted, err := prepareSubmissionIdempotency(invocation, "submit-key", attemptID, revisionID, 11, 7)
	require.NoError(t, err)
	assertPreparedIdempotency(t, submitted, userID, store.ExamSubmissionSealOperation, "submit-key",
		fmt.Sprintf(`{"exam_attempt_id":%q,"expected_current_revision_id":%q,"expected_workspace_cursor":11,"final_focus_loss_sequence":7}`,
			attemptID, revisionID))
}

func TestIdempotencyOperationCompatibility(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		store.ExamAttemptConnectOperation:           "exam.attempt.connect.v1",
		store.ExamAttemptReallowOperation:           "exam.attempt.reallow.v1",
		store.ExamAttemptWorkspaceMutationOperation: "exam.attempt.workspace.mutate.v1",
		store.ExamSubmissionSealOperation:           "exam.attempt.submit.v1",
	}
	require.Len(t, tests, 4)
	for operation, want := range tests {
		assert.Equal(t, want, operation, "operation")
	}
}

func TestPrepareIdempotencyFaultMapping(t *testing.T) {
	t.Parallel()
	valid := invocation.New(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{})
	_, err := applicationidempotency.Require(valid, "operation", "", struct{}{})
	require.Equal(t, "idempotency.key_required", faultCode(err), "missing key error")
	invalid := invocation.New(model.Principal{}, model.RequestMetadata{})
	_, err = applicationidempotency.Require(invalid, "operation", "key", struct{}{})
	require.Equal(t, "idempotency.key_invalid", faultCode(err), "invalid principal error")
	_, err = applicationidempotency.Require(valid, "operation", "key", make(chan struct{}))
	require.Equal(t, "request.invalid", faultCode(err), "encoding error")
}

func faultCode(err error) string {
	if fault, ok := apperror.As(err); ok {
		return fault.Code()
	}
	return ""
}
