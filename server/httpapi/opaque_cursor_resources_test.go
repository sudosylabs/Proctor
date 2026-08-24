// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

func assertHTTPProblem(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	var problem Problem
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decode problem response: %v; body=%s", err, response.Body.String())
	}
	if response.Code != status || problem.Code != code {
		t.Fatalf("problem response = %d/%q; want %d/%q; body=%s", response.Code, problem.Code, status, code, response.Body.String())
	}
}

func TestResourceCursorContracts(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 24, 12, 34, 56, 789, time.UTC)
	auditID := model.NewAuditEventID().String()
	mailID := model.NewMailDeliveryID().String()
	jobID := model.NewJobID().String()
	examID := model.NewExamID().String()
	managerID := model.NewUserID().String()
	invitationID := model.NewInvitationID().String()
	revisionID := model.NewExamRevisionID().String()
	sittingID := model.NewExamSittingID()
	noShowID := model.NewUserID()
	entryID := model.NewAttemptWorkspaceEntryID()
	attemptID := model.NewExamAttemptID()
	reviewID := model.NewIntegrityEvidenceID().String()

	tests := []struct {
		name             string
		encode           func() (string, error)
		decode           func(string) (any, error)
		want             any
		plainKeyset      string
		malformed        string
		legacyDocuments  []string
		maximumRawLength int
	}{
		{
			name: "audit", encode: func() (string, error) {
				return encodeAuditCursor(auditCursor{CreateAt: 123, Id: auditID})
			}, decode: func(raw string) (any, error) { return decodeAuditCursor(raw) },
			want: auditCursor{Version: 1, CreateAt: 123, Id: auditID}, plainKeyset: auditID,
			malformed:       fmt.Sprintf(`{"version":1,"create_at":0,"id":%q}`, auditID),
			legacyDocuments: []string{fmt.Sprintf(`{"create_at":123,"id":%q}`, auditID)},
		},
		{
			name: "mail", encode: func() (string, error) {
				return encodeMailDeliveryCursor(mailDeliveryCursor{CreatedAt: at.Format(time.RFC3339Nano), ID: mailID})
			}, decode: func(raw string) (any, error) { return decodeMailDeliveryCursor(raw) },
			want: mailDeliveryCursor{Version: 1, CreatedAt: at.Format(time.RFC3339Nano), ID: mailID}, plainKeyset: mailID,
			malformed:       fmt.Sprintf(`{"version":1,"created_at":"invalid","id":%q}`, mailID),
			legacyDocuments: []string{fmt.Sprintf(`{"created_at":%q,"id":%q}`, at.Format(time.RFC3339Nano), mailID)},
		},
		{
			name: "job", encode: func() (string, error) {
				return encodeJobCursor(jobCursor{CreatedAt: 456, ID: jobID})
			}, decode: func(raw string) (any, error) { return decodeJobCursor(raw) },
			want: jobCursor{Version: 1, CreatedAt: 456, ID: jobID}, plainKeyset: jobID,
			malformed:       fmt.Sprintf(`{"version":1,"created_at":0,"id":%q}`, jobID),
			legacyDocuments: []string{fmt.Sprintf(`{"created_at":456,"id":%q}`, jobID)},
		},
		{
			name: "job attempt", encode: func() (string, error) { return encodeJobAttemptCursor(987654321) },
			decode: func(raw string) (any, error) { return decodeJobAttemptCursor(raw) },
			want:   987654321, plainKeyset: "987654321", malformed: `{"version":1,"number":0}`,
			legacyDocuments: []string{`{"number":987654321}`},
		},
		{
			name: "Exam catalog", encode: func() (string, error) {
				return encodeExamCatalogCursor(examCatalogCursor{UpdatedAt: at.Format(time.RFC3339Nano), ExamID: examID})
			}, decode: func(raw string) (any, error) { return decodeExamCatalogCursor(raw) },
			want: examCatalogCursor{Version: 1, UpdatedAt: at.Format(time.RFC3339Nano), ExamID: examID}, plainKeyset: examID,
			malformed:       fmt.Sprintf(`{"version":1,"updated_at":"invalid","exam_id":%q}`, examID),
			legacyDocuments: []string{fmt.Sprintf(`{"version":0,"updated_at":%q,"exam_id":%q}`, at.Format(time.RFC3339Nano), examID)},
		},
		{
			name: "Exam Manager", encode: func() (string, error) {
				return encodeExamManagerCursor(examManagerCursor{GrantedAt: at.Format(time.RFC3339Nano), UserID: managerID})
			}, decode: func(raw string) (any, error) { return decodeExamManagerCursor(raw) },
			want: examManagerCursor{Version: 1, GrantedAt: at.Format(time.RFC3339Nano), UserID: managerID}, plainKeyset: managerID,
			malformed: fmt.Sprintf(`{"version":1,"granted_at":"invalid","user_id":%q}`, managerID),
		},
		{
			name: "Invitation", encode: func() (string, error) {
				return encodeInvitationCursor(invitationCursor{CreatedAt: at.Format(time.RFC3339Nano), ID: invitationID})
			}, decode: func(raw string) (any, error) { return decodeInvitationCursor(raw) },
			want: invitationCursor{Version: 1, CreatedAt: at.Format(time.RFC3339Nano), ID: invitationID}, plainKeyset: invitationID,
			malformed: fmt.Sprintf(`{"version":1,"created_at":"invalid","id":%q}`, invitationID),
		},
		{
			name: "Exam Revision", encode: func() (string, error) {
				return encodeExamRevisionCursor(examRevisionCursor{Number: 7, RevisionID: revisionID})
			}, decode: func(raw string) (any, error) { return decodeExamRevisionCursor(raw) },
			want: examRevisionCursor{Version: 1, Number: 7, RevisionID: revisionID}, plainKeyset: revisionID,
			malformed: fmt.Sprintf(`{"version":1,"number":0,"revision_id":%q}`, revisionID),
		},
		{
			name: "Exam Sitting", encode: func() (string, error) {
				return encodeExamSittingCursor(examSittingCursor{StartAt: at, ID: sittingID})
			}, decode: func(raw string) (any, error) { return decodeExamSittingCursor(raw) },
			want: examSittingCursor{StartAt: at, ID: sittingID}, plainKeyset: sittingID.String(),
			malformed: fmt.Sprintf(`{"version":1,"start_at":"invalid","id":%q}`, sittingID),
		},
		{
			name: "no-show", encode: func() (string, error) { return encodeExamSittingNoShowCursor(noShowID) },
			decode: func(raw string) (any, error) { return decodeExamSittingNoShowCursor(raw) },
			want:   noShowID, plainKeyset: noShowID.String(),
			malformed: `{"version":1,"candidate_user_id":"invalid"}`, maximumRawLength: 342,
		},
		{
			name: "Submission manifest", encode: func() (string, error) {
				return encodeSubmissionManifestCursor(submissionManifestCursor{EntryID: entryID})
			}, decode: func(raw string) (any, error) { return decodeSubmissionManifestCursor(raw) },
			want: submissionManifestCursor{EntryID: entryID}, plainKeyset: entryID.String(),
			malformed: `{"version":1,"after_entry_id":"invalid"}`,
		},
		{
			name: "Exam Attempt", encode: func() (string, error) {
				return encodeExamAttemptManagerCursor(examAttemptManagerCursor{CreatedAt: at, ID: attemptID})
			}, decode: func(raw string) (any, error) { return decodeExamAttemptManagerCursor(raw) },
			want: examAttemptManagerCursor{CreatedAt: at, ID: attemptID}, plainKeyset: attemptID.String(),
			malformed: fmt.Sprintf(`{"version":1,"created_at":"invalid","id":%q}`, attemptID),
		},
		{
			name: "candidate Workspace", encode: func() (string, error) {
				return encodeCandidateWorkspaceCursor(candidateWorkspaceCursor{ExpectedCursor: 9, ID: entryID})
			}, decode: func(raw string) (any, error) { return decodeCandidateWorkspaceCursor(raw) },
			want: candidateWorkspaceCursor{ExpectedCursor: 9, ID: entryID}, plainKeyset: entryID.String(),
			malformed: fmt.Sprintf(`{"version":1,"workspace_cursor":-1,"after_entry_id":%q}`, entryID),
		},
		{
			name: "Integrity Review", encode: func() (string, error) {
				return encodeExamIntegrityReviewCursor("evidence", reviewID)
			}, decode: func(raw string) (any, error) {
				return decodeExamIntegrityReviewCursor(raw, "evidence")
			},
			want: reviewID, plainKeyset: reviewID,
			malformed: fmt.Sprintf(`{"version":1,"kind":"wrong","id":%q}`, reviewID),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			raw, err := test.encode()
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			got, err := test.decode(raw)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("round trip = %#v; want %#v", got, test.want)
			}
			if test.plainKeyset != "" && strings.Contains(raw, test.plainKeyset) {
				t.Fatalf("encoded cursor exposes plain keyset %q", test.plainKeyset)
			}

			documentBytes, err := base64.RawURLEncoding.Strict().DecodeString(raw)
			if err != nil {
				t.Fatalf("decode encoded document: %v", err)
			}
			var envelope map[string]any
			if err = json.Unmarshal(documentBytes, &envelope); err != nil {
				t.Fatalf("decode encoded JSON: %v", err)
			}
			if envelope["version"] != float64(1) {
				t.Fatalf("new cursor version = %#v; want 1", envelope["version"])
			}

			document := string(documentBytes)
			invalidDocuments := []string{
				strings.Replace(document, `"version":1`, `"version":2`, 1),
				strings.Replace(document, `"version":1`, `"version":1,"version":1`, 1),
				strings.Replace(document, "{", `{"unknown":true,`, 1),
				document + `{}`,
				test.malformed,
			}
			for _, invalidDocument := range invalidDocuments {
				invalid := base64.RawURLEncoding.EncodeToString([]byte(invalidDocument))
				if _, decodeErr := test.decode(invalid); decodeErr == nil {
					t.Errorf("decode accepted invalid document %s", invalidDocument)
				}
			}

			maximum := test.maximumRawLength
			if maximum == 0 {
				maximum = defaultOpaqueCursorMaximumEncodedLength
			}
			if _, err = test.decode(strings.Repeat("A", maximum+1)); err == nil {
				t.Error("decode accepted overlong cursor")
			}
			for _, legacyDocument := range test.legacyDocuments {
				legacy := base64.RawURLEncoding.EncodeToString([]byte(legacyDocument))
				if _, err = test.decode(legacy); err != nil {
					t.Errorf("decode legacy document %s: %v", legacyDocument, err)
				}
			}
		})
	}
}
