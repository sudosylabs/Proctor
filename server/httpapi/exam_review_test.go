// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestIntegrityReviewHTTPMapsStrictDecisionAndPrivateManagerProjection(t *testing.T) {
	t.Parallel()
	fake := newExamIntegrityReviewHTTPFake(t)
	httpAPI := newExamIntegrityReviewFocusedAPI(t, fake)
	path := "/api/v1/submissions/" + fake.submission.ID.String() + "/review/decisions/" + fake.flag.ID.String()
	body := `{"expected_review_revision":2,"expected_decision_revision":1,"outcome":"inconclusive","private_rationale":"Manager-only rationale."}`
	request := httptest.NewRequest(http.MethodPut, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "decision-once")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" ||
		fake.decision.SubmissionID != fake.submission.ID || fake.decision.FlagID != fake.flag.ID ||
		fake.decision.ExpectedReviewRevision != 2 || fake.decision.ExpectedDecisionRevision != 1 ||
		fake.decision.Outcome != model.IntegrityReviewInconclusive ||
		fake.decision.PrivateRationale != "Manager-only rationale." || fake.decision.IdempotencyKey != "decision-once" {
		t.Fatalf("response=%d headers=%v command=%#v body=%s", response.Code, response.Header(), fake.decision, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"private_rationale":"Manager-only rationale."`) {
		t.Fatalf("manager mutation response omitted authorized private rationale: %s", response.Body.String())
	}

	duplicate := httptest.NewRequest(http.MethodPut, path,
		strings.NewReader(`{"expected_review_revision":2,"expected_review_revision":3,"expected_decision_revision":1,"outcome":"confirmed","private_rationale":"x"}`))
	duplicate.Header.Set("Authorization", "Bearer credential")
	duplicate.Header.Set("Content-Type", "application/json")
	duplicate.Header.Set("Idempotency-Key", "duplicate")
	response = httptest.NewRecorder()
	httpAPI.ServeHTTP(response, duplicate)
	if response.Code != http.StatusBadRequest || fake.decision.IdempotencyKey != "decision-once" {
		t.Fatalf("duplicate JSON response=%d command=%#v body=%s", response.Code, fake.decision, response.Body.String())
	}
}

func TestIntegrityReviewHTTPUsesBoundedOpaqueEvidenceCursor(t *testing.T) {
	t.Parallel()
	fake := newExamIntegrityReviewHTTPFake(t)
	fake.evidencePage.HasMore = true
	httpAPI := newExamIntegrityReviewFocusedAPI(t, fake)
	path := "/api/v1/submissions/" + fake.submission.ID.String() + "/integrity-flags/" + fake.flag.ID.String() + "/evidence?limit=1"
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("Authorization", "Bearer credential")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusOK || fake.evidenceQuery.Limit != 1 || fake.evidenceQuery.FlagID != fake.flag.ID {
		t.Fatalf("response=%d query=%#v body=%s", response.Code, fake.evidenceQuery, response.Body.String())
	}
	var page examIntegrityEvidenceListResponse
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil || len(page.Items) != 1 || page.NextCursor == "" {
		t.Fatalf("page=%#v error=%v", page, err)
	}
	id, err := decodeExamIntegrityReviewCursor(page.NextCursor, "evidence")
	if err != nil || id != fake.evidence.ID.String() {
		t.Fatalf("cursor=%q id=%q error=%v", page.NextCursor, id, err)
	}
	unknown := base64.RawURLEncoding.EncodeToString([]byte(`{"version":1,"kind":"evidence","id":"` + fake.evidence.ID.String() + `","path":"private"}`))
	if _, err = decodeExamIntegrityReviewCursor(unknown, "evidence"); err == nil {
		t.Fatal("cursor accepted an unknown private field")
	}
}

func TestIntegrityReviewEndpointsForwardExactCursorsAndMapMalformedCursors(t *testing.T) {
	t.Parallel()
	fake := newExamIntegrityReviewHTTPFake(t)
	httpAPI := newExamIntegrityReviewFocusedAPI(t, fake)
	flagID := model.NewIntegrityFlagID()
	evidenceID := model.NewIntegrityEvidenceID()
	discrepancyID := model.NewIntegrityDiscrepancyID()
	tests := []struct {
		name   string
		kind   string
		id     string
		path   string
		assert func(*testing.T)
	}{
		{
			name: "flags", kind: "flag", id: flagID.String(),
			path: "/api/v1/submissions/" + fake.submission.ID.String() + "/integrity-flags",
			assert: func(t *testing.T) {
				if fake.flagQuery.AfterFlagID != flagID {
					t.Fatalf("flag query = %#v", fake.flagQuery)
				}
			},
		},
		{
			name: "evidence", kind: "evidence", id: evidenceID.String(),
			path: "/api/v1/submissions/" + fake.submission.ID.String() + "/integrity-flags/" + fake.flag.ID.String() + "/evidence",
			assert: func(t *testing.T) {
				if fake.evidenceQuery.AfterEvidenceID != evidenceID {
					t.Fatalf("evidence query = %#v", fake.evidenceQuery)
				}
			},
		},
		{
			name: "discrepancies", kind: "discrepancy", id: discrepancyID.String(),
			path: "/api/v1/submissions/" + fake.submission.ID.String() + "/integrity-discrepancies",
			assert: func(t *testing.T) {
				if fake.discrepancyQuery.AfterDiscrepancyID != discrepancyID {
					t.Fatalf("discrepancy query = %#v", fake.discrepancyQuery)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cursor, err := encodeExamIntegrityReviewCursor(test.kind, test.id)
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodGet, test.path+"?cursor="+url.QueryEscape(cursor), nil)
			request.Header.Set("Authorization", "Bearer credential")
			response := httptest.NewRecorder()
			httpAPI.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("cursor response = %d: %s", response.Code, response.Body.String())
			}
			test.assert(t)

			malformed := httptest.NewRequest(http.MethodGet, test.path+"?cursor=not-a-cursor", nil)
			malformed.Header.Set("Authorization", "Bearer credential")
			malformedResponse := httptest.NewRecorder()
			httpAPI.ServeHTTP(malformedResponse, malformed)
			assertHTTPProblem(t, malformedResponse, http.StatusBadRequest, "request.invalid")
		})
	}
}

func TestStudentResultHTTPExposesOnlyReleasedSanitizedProjection(t *testing.T) {
	t.Parallel()
	fake := newExamIntegrityReviewHTTPFake(t)
	httpAPI := newExamIntegrityReviewFocusedAPI(t, fake)
	path := "/api/v1/exam-attempts/" + fake.attemptID.String() + "/result"
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("Authorization", "Bearer credential")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("response=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
	payload := response.Body.String()
	if !strings.Contains(payload, "Approved **remarks**") {
		t.Fatalf("student result omitted approved remarks: %s", payload)
	}
	for _, forbidden := range []string{"manager_notes", "private_rationale", "integrity_flag", "evidence", "workspace", "candidate_user_id"} {
		if strings.Contains(payload, forbidden) {
			t.Fatalf("student result exposed %q: %s", forbidden, payload)
		}
	}
}

func TestIntegrityReviewHTTPListsBoundedLateDiscrepanciesWithoutConnectionSelectors(t *testing.T) {
	t.Parallel()
	fake := newExamIntegrityReviewHTTPFake(t)
	httpAPI := newExamIntegrityReviewFocusedAPI(t, fake)
	path := "/api/v1/submissions/" + fake.submission.ID.String() + "/integrity-discrepancies?limit=1"
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("Authorization", "Bearer credential")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusOK || fake.discrepancyQuery.Limit != 1 {
		t.Fatalf("response=%d query=%#v body=%s", response.Code, fake.discrepancyQuery, response.Body.String())
	}
	payload := response.Body.String()
	if !strings.Contains(payload, fake.discrepancy.ID.String()) || !strings.Contains(payload, `"kind":"late_focus_loss"`) {
		t.Fatalf("discrepancy response=%s", payload)
	}
	for _, forbidden := range []string{"connection_id", "session_id", "credential", "continuity"} {
		if strings.Contains(payload, forbidden) {
			t.Fatalf("discrepancy response exposed %q: %s", forbidden, payload)
		}
	}
}

type examIntegrityReviewHTTPFake struct {
	principal        model.Principal
	submission       *model.ExamSubmission
	review           *model.SubmissionReview
	decisionValue    *model.IntegrityReviewDecision
	flag             *model.IntegrityFlag
	evidence         *model.IntegrityEvidence
	discrepancy      *model.IntegrityDiscrepancy
	attemptID        model.ExamAttemptID
	authorization    store.ExamIntegrityReviewAuthorization
	decision         application.SaveExamIntegrityDecisionCommand
	flagQuery        application.ListExamIntegrityFlagsQuery
	evidenceQuery    application.ListExamIntegrityEvidenceQuery
	evidencePage     store.ExamIntegrityEvidencePage
	discrepancyQuery application.ListExamIntegrityDiscrepanciesQuery
}

func newExamIntegrityReviewHTTPFake(t *testing.T) *examIntegrityReviewHTTPFake {
	t.Helper()
	at := time.Date(2026, 8, 15, 18, 0, 0, 0, time.UTC)
	attemptID, submissionID := model.NewExamAttemptID(), model.NewSubmissionID()
	manifest, err := model.NewExamSubmissionManifest(0, nil)
	if err != nil {
		t.Fatal(err)
	}
	submission, err := model.NewExamSubmission(model.ExamSubmissionSpecification{ID: submissionID, AttemptID: attemptID,
		ExamRevisionID: model.NewExamRevisionID(), WorkspaceID: model.NewExamAttemptWorkspaceID(), Manifest: manifest,
		BrowserActivity: model.BrowserActivitySubmission{State: model.BrowserActivitySubmissionNotApplicable},
		Provenance:      model.ExamSubmissionCandidateSubmitted, SubmittedAt: at})
	if err != nil {
		t.Fatal(err)
	}
	managerID := model.NewUserID()
	review, err := model.NewSubmissionReview(model.NewSubmissionReviewID(), submissionID, managerID, at)
	if err != nil {
		t.Fatal(err)
	}
	if err = review.UpdateDraft(1, "Manager-only note.", "Approved **remarks**", at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	flag, err := model.NewIntegrityFlag(model.NewIntegrityFlagID(), attemptID, 1, model.IntegrityPolicyConnectionLoss, at)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := model.NewIntegrityReviewDecision(model.NewIntegrityReviewDecisionID(), review.ID, flag.ID,
		model.IntegrityReviewInconclusive, managerID, "Manager-only rationale.", at.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := model.NewConnectionLossEvidence(model.NewIntegrityEvidenceID(), attemptID,
		model.NewAttemptParticipationID(), flag.ID, 1, at.Add(-time.Second), at)
	if err != nil {
		t.Fatal(err)
	}
	discrepancy, err := model.NewIntegrityDiscrepancy(model.IntegrityDiscrepancySpecification{
		ID: model.NewIntegrityDiscrepancyID(), SubmissionID: submissionID, AttemptID: attemptID,
		ParticipationID: model.NewAttemptParticipationID(), Generation: 1,
		Kind: model.IntegrityDiscrepancyLateFocusLoss, SchemaVersion: model.FocusLossSignalSchemaVersion,
		SignalID: model.NewFocusLossSignalID(), Sequence: 2, DurationMilliseconds: 900,
		Source: model.FocusLossSourceWindowBlur, ReceivedAt: at.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	principal := testExamHTTPPrincipal()
	authorization := store.ExamIntegrityReviewAuthorization{SubmissionID: submissionID, ExamID: model.NewExamID(),
		SittingID: model.NewExamSittingID(), AttemptID: attemptID, CandidateUserID: principal.UserID,
		AcademicUnitID: model.NewAcademicUnitID()}
	return &examIntegrityReviewHTTPFake{principal: principal, submission: submission, review: review,
		decisionValue: decision, flag: flag, evidence: evidence, discrepancy: discrepancy, attemptID: attemptID, authorization: authorization,
		evidencePage: store.ExamIntegrityEvidencePage{Items: []model.IntegrityEvidence{*evidence}}}
}

func newExamIntegrityReviewFocusedAPI(t *testing.T, fake *examIntegrityReviewHTTPFake) *API {
	t.Helper()
	logger, _ := newTestLogger(t)
	return newFocusedResourceAPI(t, logger, fake, examIntegrityReviewResource(fake))
}

func (fake *examIntegrityReviewHTTPFake) AuthenticateAccess(context.Context, string) (*model.Principal, error) {
	principal := fake.principal
	return &principal, nil
}

func (fake *examIntegrityReviewHTTPFake) AuthenticateBearer(context.Context, string) (*model.Principal, error) {
	principal := fake.principal
	return &principal, nil
}

func (fake *examIntegrityReviewHTTPFake) GetExamIntegrityReview(context.Context, application.Invocation,
	model.SubmissionID,
) (*application.ExamSubmissionReviewSnapshot, error) {
	return &store.ExamSubmissionReviewSnapshot{Authorization: fake.authorization, Submission: fake.submission,
		Review: fake.review, Decisions: []model.IntegrityReviewDecision{*fake.decisionValue}}, nil
}

func (fake *examIntegrityReviewHTTPFake) ListExamIntegrityFlags(_ context.Context, _ application.Invocation,
	query application.ListExamIntegrityFlagsQuery,
) (*application.ExamIntegrityFlagPage, error) {
	fake.flagQuery = query
	return &store.ExamIntegrityFlagPage{Items: []store.ExamIntegrityFlagSummary{{Flag: *fake.flag, EvidenceCount: 1}}}, nil
}

func (fake *examIntegrityReviewHTTPFake) ListExamIntegrityEvidence(_ context.Context, _ application.Invocation,
	query application.ListExamIntegrityEvidenceQuery,
) (*application.ExamIntegrityEvidencePage, error) {
	fake.evidenceQuery = query
	page := fake.evidencePage
	return &page, nil
}

func (fake *examIntegrityReviewHTTPFake) ListExamIntegrityDiscrepancies(_ context.Context, _ application.Invocation,
	query application.ListExamIntegrityDiscrepanciesQuery,
) (*application.ExamIntegrityDiscrepancyPage, error) {
	fake.discrepancyQuery = query
	return &store.ExamIntegrityDiscrepancyPage{Items: []model.IntegrityDiscrepancy{*fake.discrepancy}}, nil
}

func (fake *examIntegrityReviewHTTPFake) SaveExamIntegrityDecision(_ context.Context, _ application.Invocation,
	command application.SaveExamIntegrityDecisionCommand,
) (application.ExamIntegrityReviewResult, error) {
	fake.decision = command
	decision := *fake.decisionValue
	decision.Outcome, decision.PrivateRationale = command.Outcome, command.PrivateRationale
	return application.ExamIntegrityReviewResult{Authorization: fake.authorization, Review: fake.review, Decision: &decision}, nil
}

func (fake *examIntegrityReviewHTTPFake) UpdateExamIntegrityReview(context.Context, application.Invocation,
	application.UpdateExamIntegrityReviewCommand,
) (application.ExamIntegrityReviewResult, error) {
	return application.ExamIntegrityReviewResult{Authorization: fake.authorization, Review: fake.review}, nil
}

func (fake *examIntegrityReviewHTTPFake) FinalizeExamIntegrityReview(context.Context, application.Invocation,
	application.FinalizeExamIntegrityReviewCommand,
) (application.ExamIntegrityReviewResult, error) {
	return application.ExamIntegrityReviewResult{Authorization: fake.authorization, Review: fake.review}, nil
}

func (fake *examIntegrityReviewHTTPFake) ReleaseStudentExamResult(context.Context, application.Invocation,
	application.ReleaseStudentExamResultCommand,
) (application.ExamIntegrityReviewResult, error) {
	return application.ExamIntegrityReviewResult{Authorization: fake.authorization, Review: fake.review}, nil
}

func (fake *examIntegrityReviewHTTPFake) GetStudentExamResult(context.Context, application.Invocation,
	model.ExamAttemptID,
) (*application.StudentExamResult, error) {
	return &model.StudentResult{ReviewID: fake.review.ID, SubmissionID: fake.submission.ID, AttemptID: fake.attemptID,
		CandidateUserID: fake.principal.UserID, StudentRemarksMarkdown: "Approved **remarks**", ReleasedAt: fake.review.UpdatedAt}, nil
}
