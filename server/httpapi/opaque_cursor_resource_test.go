// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

func assertHTTPProblem(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	var problem Problem
	err := json.Unmarshal(response.Body.Bytes(), &problem)
	require.NoError(t, err, "decode problem response: %v; body=%s", err, response.Body.String())
	require.Equal(t, status, response.Code, "problem response = %d/%q; want %d/%q; body=%s", response.Code, problem.Code, status, code, response.Body.String())
	require.Equal(t, code, problem.Code, "problem response = %d/%q; want %d/%q; body=%s", response.Code, problem.Code, status, code, response.Body.String())
}

func TestResourceCursorContracts(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 24, 12, 34, 56, 123456789, time.UTC)
	domainAt := time.Date(2026, time.August, 24, 12, 34, 56, 123456000, time.UTC)
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
		maximumRawLength int
	}{
		{
			name: "audit", encode: func() (string, error) {
				return encodeAuditCursor(auditCursor{CreatedAt: at.Format(time.RFC3339Nano), ID: auditID})
			}, decode: func(raw string) (any, error) { return decodeAuditCursor(raw) },
			want: auditCursor{CreatedAt: at.Format(time.RFC3339Nano), ID: auditID}, plainKeyset: auditID,
			malformed: fmt.Sprintf(`{"created_at":"invalid","id":%q}`, auditID),
		},
		{
			name: "mail", encode: func() (string, error) {
				return encodeMailDeliveryCursor(mailDeliveryCursor{CreatedAt: at.Format(time.RFC3339Nano), ID: mailID})
			}, decode: func(raw string) (any, error) { return decodeMailDeliveryCursor(raw) },
			want: mailDeliveryCursor{CreatedAt: at.Format(time.RFC3339Nano), ID: mailID}, plainKeyset: mailID,
			malformed: fmt.Sprintf(`{"created_at":"invalid","id":%q}`, mailID),
		},
		{
			name: "job", encode: func() (string, error) {
				return encodeJobCursor(jobCursor{CreatedAt: at.Format(time.RFC3339Nano), ID: jobID})
			}, decode: func(raw string) (any, error) { return decodeJobCursor(raw) },
			want: jobCursor{CreatedAt: at.Format(time.RFC3339Nano), ID: jobID}, plainKeyset: jobID,
			malformed: fmt.Sprintf(`{"created_at":"invalid","id":%q}`, jobID),
		},
		{
			name: "job attempt", encode: func() (string, error) { return encodeJobAttemptCursor(987654321) },
			decode: func(raw string) (any, error) { return decodeJobAttemptCursor(raw) },
			want:   987654321, plainKeyset: "987654321", malformed: `{"number":0}`,
		},
		{
			name: "Exam catalog", encode: func() (string, error) {
				return encodeExamCatalogCursor(examCatalogCursor{UpdatedAt: at.Format(time.RFC3339Nano), ExamID: examID})
			}, decode: func(raw string) (any, error) { return decodeExamCatalogCursor(raw) },
			want: examCatalogCursor{UpdatedAt: at.Format(time.RFC3339Nano), ExamID: examID}, plainKeyset: examID,
			malformed: fmt.Sprintf(`{"updated_at":"invalid","exam_id":%q}`, examID),
		},
		{
			name: "Exam Manager", encode: func() (string, error) {
				return encodeExamManagerCursor(examManagerCursor{GrantedAt: at.Format(time.RFC3339Nano), UserID: managerID})
			}, decode: func(raw string) (any, error) { return decodeExamManagerCursor(raw) },
			want: examManagerCursor{GrantedAt: at.Format(time.RFC3339Nano), UserID: managerID}, plainKeyset: managerID,
			malformed: fmt.Sprintf(`{"granted_at":"invalid","user_id":%q}`, managerID),
		},
		{
			name: "Invitation", encode: func() (string, error) {
				return encodeInvitationCursor(invitationCursor{CreatedAt: at.Format(time.RFC3339Nano), ID: invitationID})
			}, decode: func(raw string) (any, error) { return decodeInvitationCursor(raw) },
			want: invitationCursor{CreatedAt: at.Format(time.RFC3339Nano), ID: invitationID}, plainKeyset: invitationID,
			malformed: fmt.Sprintf(`{"created_at":"invalid","id":%q}`, invitationID),
		},
		{
			name: "Exam Revision", encode: func() (string, error) {
				return encodeExamRevisionCursor(examRevisionCursor{Number: 7, RevisionID: revisionID})
			}, decode: func(raw string) (any, error) { return decodeExamRevisionCursor(raw) },
			want: examRevisionCursor{Number: 7, RevisionID: revisionID}, plainKeyset: revisionID,
			malformed: fmt.Sprintf(`{"number":0,"revision_id":%q}`, revisionID),
		},
		{
			name: "Exam Sitting", encode: func() (string, error) {
				return encodeExamSittingCursor(examSittingCursor{StartAt: at, ID: sittingID})
			}, decode: func(raw string) (any, error) { return decodeExamSittingCursor(raw) },
			want: examSittingCursor{StartAt: domainAt, ID: sittingID}, plainKeyset: sittingID.String(),
			malformed: fmt.Sprintf(`{"start_at":"invalid","id":%q}`, sittingID),
		},
		{
			name: "no-show", encode: func() (string, error) { return encodeExamSittingNoShowCursor(noShowID) },
			decode: func(raw string) (any, error) { return decodeExamSittingNoShowCursor(raw) },
			want:   noShowID, plainKeyset: noShowID.String(),
			malformed: `{"candidate_user_id":"invalid"}`, maximumRawLength: 342,
		},
		{
			name: "Submission manifest", encode: func() (string, error) {
				return encodeSubmissionManifestCursor(submissionManifestCursor{EntryID: entryID})
			}, decode: func(raw string) (any, error) { return decodeSubmissionManifestCursor(raw) },
			want: submissionManifestCursor{EntryID: entryID}, plainKeyset: entryID.String(),
			malformed: `{"after_entry_id":"invalid"}`,
		},
		{
			name: "Exam Attempt", encode: func() (string, error) {
				return encodeExamAttemptManagerCursor(examAttemptManagerCursor{CreatedAt: at, ID: attemptID})
			}, decode: func(raw string) (any, error) { return decodeExamAttemptManagerCursor(raw) },
			want: examAttemptManagerCursor{CreatedAt: domainAt, ID: attemptID}, plainKeyset: attemptID.String(),
			malformed: fmt.Sprintf(`{"created_at":"invalid","id":%q}`, attemptID),
		},
		{
			name: "candidate Workspace", encode: func() (string, error) {
				return encodeCandidateWorkspaceCursor(candidateWorkspaceCursor{ExpectedCursor: 9, ID: entryID})
			}, decode: func(raw string) (any, error) { return decodeCandidateWorkspaceCursor(raw) },
			want: candidateWorkspaceCursor{ExpectedCursor: 9, ID: entryID}, plainKeyset: entryID.String(),
			malformed: fmt.Sprintf(`{"workspace_cursor":-1,"after_entry_id":%q}`, entryID),
		},
		{
			name: "Integrity Review", encode: func() (string, error) {
				return encodeExamIntegrityReviewCursor("evidence", reviewID)
			}, decode: func(raw string) (any, error) {
				return decodeExamIntegrityReviewCursor(raw, "evidence")
			},
			want: reviewID, plainKeyset: reviewID,
			malformed: fmt.Sprintf(`{"kind":"wrong","id":%q}`, reviewID),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			raw, err := test.encode()
			require.NoError(t, err)
			got, err := test.decode(raw)
			require.NoError(t, err)
			require.True(t, reflect.DeepEqual(got, test.want), "round trip = %#v; want %#v", got, test.want)
			if test.plainKeyset != "" {
				require.NotContains(t, raw, test.plainKeyset)
			}

			documentBytes, err := base64.RawURLEncoding.Strict().DecodeString(raw)
			require.NoError(t, err)
			var envelope map[string]any
			err = json.Unmarshal(documentBytes, &envelope)
			require.NoError(t, err)
			require.NotContains(t, envelope, "version", "cursors are unversioned")

			document := string(documentBytes)
			invalidDocuments := []string{
				strings.Replace(document, "{", `{"version":1,`, 1),
				strings.Replace(document, "{", `{"version":0,`, 1),
				strings.Replace(document, "{", `{"unknown":true,`, 1),
				document + `{}`,
				test.malformed,
			}
			for _, invalidDocument := range invalidDocuments {
				invalid := base64.RawURLEncoding.EncodeToString([]byte(invalidDocument))
				_, decodeErr := test.decode(invalid)
				assert.Error(t, decodeErr, "decode accepted invalid document %s", invalidDocument)
			}

			maximum := test.maximumRawLength
			if maximum == 0 {
				maximum = defaultOpaqueCursorMaximumEncodedLength
			}
			_, err = test.decode(strings.Repeat("A", maximum+1))
			assert.Error(t, err, "decode accepted overlong cursor")
		})
	}
}
