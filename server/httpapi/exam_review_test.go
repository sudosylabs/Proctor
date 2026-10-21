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

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app"
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
	require.Equal(t, http.StatusOK, response.Code, "response=%d headers=%v command=%#v body=%s", response.Code, response.Header(), fake.decision, response.Body.String())
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"), "response=%d headers=%v command=%#v body=%s", response.Code, response.Header(), fake.decision, response.Body.String())
	require.Equal(t, fake.submission.ID, fake.decision.SubmissionID, "response=%d headers=%v command=%#v body=%s", response.Code, response.Header(), fake.decision, response.Body.String())
	require.Equal(t, fake.flag.ID, fake.decision.FlagID, "response=%d headers=%v command=%#v body=%s", response.Code, response.Header(), fake.decision, response.Body.String())
	require.Equal(t, int64(2), fake.decision.ExpectedReviewRevision, "response=%d headers=%v command=%#v body=%s", response.Code, response.Header(), fake.decision, response.Body.String())
	require.Equal(t, int64(1), fake.decision.ExpectedDecisionRevision, "response=%d headers=%v command=%#v body=%s", response.Code, response.Header(), fake.decision, response.Body.String())
	require.Equal(t, model.IntegrityReviewInconclusive, fake.decision.Outcome, "response=%d headers=%v command=%#v body=%s", response.Code, response.Header(), fake.decision, response.Body.String())
	require.Equal(t, "Manager-only rationale.", fake.decision.PrivateRationale, "response=%d headers=%v command=%#v body=%s", response.Code, response.Header(), fake.decision, response.Body.String())
	require.Equal(t, "decision-once", fake.decision.IdempotencyKey, "response=%d headers=%v command=%#v body=%s", response.Code, response.Header(), fake.decision, response.Body.String())
	require.Contains(t, response.Body.String(), `"private_rationale":"Manager-only rationale."`)

	duplicate := httptest.NewRequest(http.MethodPut, path,
		strings.NewReader(`{"expected_review_revision":2,"expected_review_revision":3,"expected_decision_revision":1,"outcome":"confirmed","private_rationale":"x"}`))
	duplicate.Header.Set("Authorization", "Bearer credential")
	duplicate.Header.Set("Content-Type", "application/json")
	duplicate.Header.Set("Idempotency-Key", "duplicate")
	response = httptest.NewRecorder()
	httpAPI.ServeHTTP(response, duplicate)
	require.Equal(t, http.StatusBadRequest, response.Code, "duplicate JSON response=%d command=%#v body=%s", response.Code, fake.decision, response.Body.String())
	require.Equal(t, "decision-once", fake.decision.IdempotencyKey, "duplicate JSON response=%d command=%#v body=%s", response.Code, fake.decision, response.Body.String())
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
	require.Equal(t, http.StatusOK, response.Code, "response=%d query=%#v body=%s", response.Code, fake.evidenceQuery, response.Body.String())
	require.Equal(t, 1, fake.evidenceQuery.Limit, "response=%d query=%#v body=%s", response.Code, fake.evidenceQuery, response.Body.String())
	require.Equal(t, fake.flag.ID, fake.evidenceQuery.FlagID, "response=%d query=%#v body=%s", response.Code, fake.evidenceQuery, response.Body.String())
	var page examIntegrityEvidenceListResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &page), "page=%#v error", page)
	require.Len(t, page.Items, 1, "page=%#v error", page)
	require.NotEmpty(t, page.NextCursor, "page=%#v error", page)
	id, err := decodeExamIntegrityReviewCursor(page.NextCursor, "evidence")
	require.NoError(t, err, "cursor=%q id=%q error", page.NextCursor, id)
	require.Equal(t, fake.evidence.ID.String(), id, "cursor=%q id=%q error", page.NextCursor, id)
	unknown := base64.RawURLEncoding.EncodeToString([]byte(`{"version":1,"kind":"evidence","id":"` + fake.evidence.ID.String() + `","path":"private"}`))
	_, err = decodeExamIntegrityReviewCursor(unknown, "evidence")
	require.Error(t, err, "cursor accepted an unknown private field")
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
				require.Equal(t, flagID, fake.flagQuery.AfterFlagID)
			},
		},
		{
			name: "evidence", kind: "evidence", id: evidenceID.String(),
			path: "/api/v1/submissions/" + fake.submission.ID.String() + "/integrity-flags/" + fake.flag.ID.String() + "/evidence",
			assert: func(t *testing.T) {
				require.Equal(t, evidenceID, fake.evidenceQuery.AfterEvidenceID)
			},
		},
		{
			name: "discrepancies", kind: "discrepancy", id: discrepancyID.String(),
			path: "/api/v1/submissions/" + fake.submission.ID.String() + "/integrity-discrepancies",
			assert: func(t *testing.T) {
				require.Equal(t, discrepancyID, fake.discrepancyQuery.AfterDiscrepancyID)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cursor, err := encodeExamIntegrityReviewCursor(test.kind, test.id)
			require.NoError(t, err)
			request := httptest.NewRequest(http.MethodGet, test.path+"?cursor="+url.QueryEscape(cursor), nil)
			request.Header.Set("Authorization", "Bearer credential")
			response := httptest.NewRecorder()
			httpAPI.ServeHTTP(response, request)
			require.Equal(t, http.StatusOK, response.Code, "cursor response = %d: %s", response.Code, response.Body.String())
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
	require.Equal(t, http.StatusOK, response.Code, "response=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"), "response=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	payload := response.Body.String()
	require.Contains(t, payload, "Approved **remarks**")
	for _, forbidden := range []string{"manager_notes", "private_rationale", "integrity_flag", "evidence", "workspace", "candidate_user_id"} {
		require.NotContains(t, payload, forbidden, "student result exposed")
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
	require.Equal(t, http.StatusOK, response.Code, "response=%d query=%#v body=%s", response.Code, fake.discrepancyQuery, response.Body.String())
	require.Equal(t, 1, fake.discrepancyQuery.Limit, "response=%d query=%#v body=%s", response.Code, fake.discrepancyQuery, response.Body.String())
	payload := response.Body.String()
	require.Contains(t, payload, fake.discrepancy.ID.String())
	require.Contains(t, payload, `"kind":"late_focus_loss"`)
	for _, forbidden := range []string{"connection_id", "session_id", "credential", "continuity"} {
		require.NotContains(t, payload, forbidden, "discrepancy response exposed")
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
	decision         app.SaveExamIntegrityDecisionCommand
	flagQuery        app.ListExamIntegrityFlagsQuery
	evidenceQuery    app.ListExamIntegrityEvidenceQuery
	evidencePage     store.ExamIntegrityEvidencePage
	discrepancyQuery app.ListExamIntegrityDiscrepanciesQuery
}

func newExamIntegrityReviewHTTPFake(t *testing.T) *examIntegrityReviewHTTPFake {
	t.Helper()
	at := time.Date(2026, 8, 15, 18, 0, 0, 0, time.UTC)
	attemptID, submissionID := model.NewExamAttemptID(), model.NewSubmissionID()
	manifest, err := model.NewExamSubmissionManifest(0, nil)
	require.NoError(t, err)
	submission, err := model.NewExamSubmission(model.ExamSubmissionSpecification{ID: submissionID, AttemptID: attemptID,
		ExamRevisionID: model.NewExamRevisionID(), WorkspaceID: model.NewExamAttemptWorkspaceID(), Manifest: manifest,
		BrowserActivity: model.BrowserSubmissionSettlement{State: "not_applicable", InventoryRevision: 1},
		Provenance:      model.ExamSubmissionCandidateSubmitted, SubmittedAt: at})
	require.NoError(t, err)
	managerID := model.NewUserID()
	review, err := model.NewSubmissionReview(model.NewSubmissionReviewID(), submissionID, managerID, at)
	require.NoError(t, err)
	err = review.UpdateDraft(1, "Manager-only note.", "Approved **remarks**", at.Add(time.Second))
	require.NoError(t, err)
	flag, err := model.NewIntegrityFlag(model.NewIntegrityFlagID(), attemptID, 1, model.IntegrityPolicyConnectionLoss, at)
	require.NoError(t, err)
	decision, err := model.NewIntegrityReviewDecision(model.NewIntegrityReviewDecisionID(), review.ID, flag.ID,
		model.IntegrityReviewInconclusive, managerID, "Manager-only rationale.", at.Add(time.Second))
	require.NoError(t, err)
	evidence, err := model.NewConnectionLossEvidence(model.NewIntegrityEvidenceID(), attemptID,
		model.NewAttemptParticipationID(), flag.ID, 1, at.Add(-time.Second), at)
	require.NoError(t, err)
	discrepancy, err := model.NewIntegrityDiscrepancy(model.IntegrityDiscrepancySpecification{
		ID: model.NewIntegrityDiscrepancyID(), SubmissionID: submissionID, AttemptID: attemptID,
		ParticipationID: model.NewAttemptParticipationID(), Generation: 1,
		Kind: model.IntegrityDiscrepancyLateFocusLoss, SchemaVersion: model.FocusLossSignalSchemaVersion,
		SignalID: model.NewFocusLossSignalID(), Sequence: 2, DurationMilliseconds: 900,
		Source: model.FocusLossSourceWindowBlur, ReceivedAt: at.Add(time.Second)})
	require.NoError(t, err)
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

func (f *examIntegrityReviewHTTPFake) AuthenticateAccess(context.Context, string) (*model.Principal, error) {
	principal := f.principal
	return &principal, nil
}

func (f *examIntegrityReviewHTTPFake) AuthenticateBearer(context.Context, string) (*model.Principal, error) {
	principal := f.principal
	return &principal, nil
}

func (f *examIntegrityReviewHTTPFake) GetExamIntegrityReview(context.Context, app.Invocation,
	model.SubmissionID,
) (*app.ExamSubmissionReviewSnapshot, error) {
	return &store.ExamSubmissionReviewSnapshot{NativeConditions: model.NewNativeConditionInventory(), Authorization: f.authorization, Submission: f.submission,
		Review: f.review, Decisions: []model.IntegrityReviewDecision{*f.decisionValue}}, nil
}

func (f *examIntegrityReviewHTTPFake) ListExamIntegrityFlags(_ context.Context, _ app.Invocation,
	query app.ListExamIntegrityFlagsQuery,
) (*app.ExamIntegrityFlagPage, error) {
	f.flagQuery = query
	return &store.ExamIntegrityFlagPage{Items: []store.ExamIntegrityFlagSummary{{Flag: *f.flag, EvidenceCount: 1}}}, nil
}

func (f *examIntegrityReviewHTTPFake) ListExamIntegrityEvidence(_ context.Context, _ app.Invocation,
	query app.ListExamIntegrityEvidenceQuery,
) (*app.ExamIntegrityEvidencePage, error) {
	f.evidenceQuery = query
	page := f.evidencePage
	return &page, nil
}

func (f *examIntegrityReviewHTTPFake) ListExamIntegrityDiscrepancies(_ context.Context, _ app.Invocation,
	query app.ListExamIntegrityDiscrepanciesQuery,
) (*app.ExamIntegrityDiscrepancyPage, error) {
	f.discrepancyQuery = query
	return &store.ExamIntegrityDiscrepancyPage{Items: []model.IntegrityDiscrepancy{*f.discrepancy}}, nil
}

func (f *examIntegrityReviewHTTPFake) SaveExamIntegrityDecision(_ context.Context, _ app.Invocation,
	command app.SaveExamIntegrityDecisionCommand,
) (app.ExamIntegrityReviewResult, error) {
	f.decision = command
	decision := *f.decisionValue
	decision.Outcome, decision.PrivateRationale = command.Outcome, command.PrivateRationale
	return app.ExamIntegrityReviewResult{Authorization: f.authorization, Review: f.review, Decision: &decision}, nil
}

func (f *examIntegrityReviewHTTPFake) UpdateExamIntegrityReview(context.Context, app.Invocation,
	app.UpdateExamIntegrityReviewCommand,
) (app.ExamIntegrityReviewResult, error) {
	return app.ExamIntegrityReviewResult{Authorization: f.authorization, Review: f.review}, nil
}

func (f *examIntegrityReviewHTTPFake) FinalizeExamIntegrityReview(context.Context, app.Invocation,
	app.FinalizeExamIntegrityReviewCommand,
) (app.ExamIntegrityReviewResult, error) {
	return app.ExamIntegrityReviewResult{Authorization: f.authorization, Review: f.review}, nil
}

func (f *examIntegrityReviewHTTPFake) ReleaseStudentExamResult(context.Context, app.Invocation,
	app.ReleaseStudentExamResultCommand,
) (app.ExamIntegrityReviewResult, error) {
	return app.ExamIntegrityReviewResult{Authorization: f.authorization, Review: f.review}, nil
}

func (f *examIntegrityReviewHTTPFake) GetStudentExamResult(context.Context, app.Invocation,
	model.ExamAttemptID,
) (*app.StudentExamResult, error) {
	return &model.StudentResult{ReviewID: f.review.ID, SubmissionID: f.submission.ID, AttemptID: f.attemptID,
		CandidateUserID: f.principal.UserID, StudentRemarksMarkdown: "Approved **remarks**", ReleasedAt: f.review.UpdatedAt}, nil
}

func (f *examIntegrityReviewHTTPFake) ListNativeConditions(context.Context, app.Invocation, app.NativeConditionListQuery) (*app.NativeConditionPage, error) {
	return &app.NativeConditionPage{Items: []model.NativeConditionEvidence{}}, nil
}
