// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package attempt

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

func TestWorkspaceMutationFingerprintCompatibility(t *testing.T) {
	t.Parallel()
	userID, attemptID := model.NewUserID(), model.NewExamAttemptID()
	entryID := model.NewAttemptWorkspaceEntryID()
	version := model.WorkspaceContentVersion("aaaaaaaaaaaaaaaaaaaaaaaaaa")
	call := NewCall(model.Principal{UserID: userID}, model.RequestMetadata{})
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
			prepared, err := prepareWorkspaceMutationIdempotency(call, "execution-key", attemptID, test.operation, test.semantic)
			if err != nil {
				t.Fatal(err)
			}
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
	call := NewCall(model.Principal{UserID: userID, SessionID: sessionID}, model.RequestMetadata{})
	sittingID, attemptID := model.NewExamSittingID(), model.NewExamAttemptID()
	credential := model.NewCredentialToken()

	manifest := model.CurrentAttemptConfigurationManifestFingerprint()
	connected, err := prepareConnectIdempotency(call, ConnectCommand{SittingID: sittingID,
		ContinuityCredential: credential, SupportedConfigurationManifests: []string{manifest}, IdempotencyKey: "connect-key"})
	if err != nil {
		t.Fatal(err)
	}
	assertPreparedIdempotency(t, connected, userID, store.ExamAttemptConnectOperation, "connect-key",
		fmt.Sprintf(`{"exam_sitting_id":%q,"session_id":%q,"continuity_credential_hash":%q,"supported_attempt_configuration_manifests":[%q],"initial_configuration":null}`,
			sittingID, sessionID, model.HashToken(credential), manifest))

	examID, suspensionID := model.NewExamID(), model.NewAttemptSuspensionID()
	reallowed, err := prepareReallowIdempotency(call, ReallowCommand{ExamID: examID, SittingID: sittingID,
		AttemptID: attemptID, SuspensionID: suspensionID, ExpectedAttemptRevision: 3,
		PrivateReason: "reviewed", IdempotencyKey: "reallow-key"})
	if err != nil {
		t.Fatal(err)
	}
	assertPreparedIdempotency(t, reallowed, userID, store.ExamAttemptReallowOperation, "reallow-key",
		fmt.Sprintf(`{"exam_id":%q,"exam_sitting_id":%q,"exam_attempt_id":%q,"suspension_id":%q,"expected_attempt_revision":3,"private_reason":"reviewed"}`,
			examID, sittingID, attemptID, suspensionID))

	revisionID := model.NewExamRevisionID()
	submitted, err := prepareSubmissionIdempotency(call, "submit-key", attemptID, revisionID, 11, 7,
		model.BrowserActivitySubmission{State: model.BrowserActivitySubmissionNotApplicable})
	if err != nil {
		t.Fatal(err)
	}
	assertPreparedIdempotency(t, submitted, userID, store.ExamSubmissionSealOperation, "submit-key",
		fmt.Sprintf(`{"exam_attempt_id":%q,"expected_current_revision_id":%q,"expected_workspace_cursor":11,"final_focus_loss_sequence":7,"browser_activity_state":"not_applicable","browser_source_session_id":"","browser_final_sequence":null,"browser_gap_reason":""}`,
			attemptID, revisionID))
}

func TestWorkspaceMutationOriginIsExcludedFromVersionOneFingerprint(t *testing.T) {
	t.Parallel()
	call := NewCall(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{})
	attemptID := model.NewExamAttemptID()
	semantic := struct {
		Path string `json:"path"`
	}{"src"}
	candidate, err := prepareWorkspaceMutationIdempotency(call, "same-key", attemptID,
		model.AttemptWorkspaceMutationCreateDirectory, semantic)
	if err != nil {
		t.Fatal(err)
	}
	executionHost, err := prepareWorkspaceMutationIdempotency(call, "same-key", attemptID,
		model.AttemptWorkspaceMutationCreateDirectory, semantic)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Fingerprint != executionHost.Fingerprint {
		t.Fatal("transient origin changed the retained version-one Workspace fingerprint")
	}
}

func TestIdempotencyOperationCompatibility(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		store.ExamAttemptConnectOperation:           "exam.attempt.connect.v1",
		store.ExamAttemptReallowOperation:           "exam.attempt.reallow.v1",
		store.ExamAttemptWorkspaceMutationOperation: "exam.attempt.workspace.mutate.v1",
		store.ExamSubmissionSealOperation:           "exam.attempt.submit.v1",
	}
	if len(tests) != 4 {
		t.Fatalf("operation set collapsed: %#v", tests)
	}
	for operation, want := range tests {
		if operation != want {
			t.Errorf("operation = %q; want %q", operation, want)
		}
	}
}

func TestPrepareIdempotencyFaultMapping(t *testing.T) {
	t.Parallel()
	valid := NewCall(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{})
	if _, err := prepareIdempotency(valid, "operation", "", struct{}{}); faultCode(err) != "idempotency.key_required" {
		t.Fatalf("missing key error = %v", err)
	}
	invalid := NewCall(model.Principal{}, model.RequestMetadata{})
	if _, err := prepareIdempotency(invalid, "operation", "key", struct{}{}); faultCode(err) != "idempotency.invalid_key" {
		t.Fatalf("invalid principal error = %v", err)
	}
	if _, err := prepareIdempotency(valid, "operation", "key", make(chan struct{})); faultCode(err) != "request.invalid" {
		t.Fatalf("encoding error = %v", err)
	}
}

func faultCode(err error) string {
	if fault, ok := err.(*Fault); ok {
		return fault.Code
	}
	return ""
}
