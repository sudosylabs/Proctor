// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"
	"unicode/utf8"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

const examIntegrityReviewCursorVersion = 1

type ExamIntegrityReviewApplication interface {
	GetExamIntegrityReview(context.Context, application.Invocation, model.SubmissionID) (*application.ExamSubmissionReviewSnapshot, error)
	ListExamIntegrityFlags(context.Context, application.Invocation, application.ListExamIntegrityFlagsQuery) (*application.ExamIntegrityFlagPage, error)
	ListExamIntegrityEvidence(context.Context, application.Invocation, application.ListExamIntegrityEvidenceQuery) (*application.ExamIntegrityEvidencePage, error)
	ListExamIntegrityDiscrepancies(context.Context, application.Invocation, application.ListExamIntegrityDiscrepanciesQuery) (*application.ExamIntegrityDiscrepancyPage, error)
	SaveExamIntegrityDecision(context.Context, application.Invocation, application.SaveExamIntegrityDecisionCommand) (application.ExamIntegrityReviewResult, error)
	UpdateExamIntegrityReview(context.Context, application.Invocation, application.UpdateExamIntegrityReviewCommand) (application.ExamIntegrityReviewResult, error)
	FinalizeExamIntegrityReview(context.Context, application.Invocation, application.FinalizeExamIntegrityReviewCommand) (application.ExamIntegrityReviewResult, error)
	ReleaseStudentExamResult(context.Context, application.Invocation, application.ReleaseStudentExamResultCommand) (application.ExamIntegrityReviewResult, error)
	GetStudentExamResult(context.Context, application.Invocation, model.ExamAttemptID) (*application.StudentExamResult, error)
}

type examIntegrityReviewHTTPModule struct {
	application ExamIntegrityReviewApplication
}

type examIntegrityReviewIDCursor struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

func encodeExamIntegrityReviewCursor(kind, id string) string {
	encoded, _ := json.Marshal(struct {
		Version int    `json:"version"`
		Kind    string `json:"kind"`
		ID      string `json:"id"`
	}{examIntegrityReviewCursorVersion, kind, id})
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func decodeExamIntegrityReviewCursor(raw, kind string) (string, error) {
	if raw == "" {
		return "", errors.New("cursor is required")
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if err != nil {
		return "", err
	}
	var wire struct {
		Version int    `json:"version"`
		Kind    string `json:"kind"`
		ID      string `json:"id"`
	}
	decoder := json.NewDecoder(bytes.NewReader(decoded))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&wire); err != nil || wire.Version != examIntegrityReviewCursorVersion || wire.Kind != kind || !model.IsValidId(wire.ID) {
		return "", errors.New("invalid Integrity Review cursor")
	}
	var trailing any
	if err = decoder.Decode(&trailing); err != io.EOF {
		return "", errors.New("Integrity Review cursor has trailing data")
	}
	return wire.ID, nil
}

type saveExamIntegrityDecisionRequest struct {
	ReviewID                 string `json:"submission_review_id,omitempty"`
	ExpectedReviewRevision   int64  `json:"expected_review_revision"`
	ExpectedDecisionRevision int64  `json:"expected_decision_revision"`
	Outcome                  string `json:"outcome"`
	PrivateRationale         string `json:"private_rationale"`
}

type updateExamIntegrityReviewRequest struct {
	ReviewID               string `json:"submission_review_id,omitempty"`
	ExpectedReviewRevision int64  `json:"expected_review_revision"`
	ManagerNotes           string `json:"manager_notes"`
	StudentRemarksMarkdown string `json:"student_remarks_markdown"`
}

type terminalExamIntegrityReviewRequest struct {
	ReviewID               string `json:"submission_review_id"`
	ExpectedReviewRevision int64  `json:"expected_review_revision"`
}

func (body *saveExamIntegrityDecisionRequest) UnmarshalJSON(encoded []byte) error {
	type wire saveExamIntegrityDecisionRequest
	var decoded wire
	if err := decodeDuplicateFreeExamIntegrityReviewObject(encoded, &decoded); err != nil {
		return err
	}
	*body = saveExamIntegrityDecisionRequest(decoded)
	return nil
}

func (body *updateExamIntegrityReviewRequest) UnmarshalJSON(encoded []byte) error {
	type wire updateExamIntegrityReviewRequest
	var decoded wire
	if err := decodeDuplicateFreeExamIntegrityReviewObject(encoded, &decoded); err != nil {
		return err
	}
	*body = updateExamIntegrityReviewRequest(decoded)
	return nil
}

func (body *terminalExamIntegrityReviewRequest) UnmarshalJSON(encoded []byte) error {
	type wire terminalExamIntegrityReviewRequest
	var decoded wire
	if err := decodeDuplicateFreeExamIntegrityReviewObject(encoded, &decoded); err != nil {
		return err
	}
	*body = terminalExamIntegrityReviewRequest(decoded)
	return nil
}

func decodeDuplicateFreeExamIntegrityReviewObject(encoded []byte, target any) error {
	if !utf8.Valid(encoded) {
		return errors.New("Integrity Review request must be valid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return errors.New("Integrity Review request must be a JSON object")
	}
	seen := make(map[string]struct{})
	for decoder.More() {
		keyToken, tokenErr := decoder.Token()
		if tokenErr != nil {
			return tokenErr
		}
		key, ok := keyToken.(string)
		if !ok {
			return errors.New("Integrity Review request member is invalid")
		}
		if _, duplicate := seen[key]; duplicate {
			return errors.New("Integrity Review request contains a duplicate member")
		}
		seen[key] = struct{}{}
		var value json.RawMessage
		if tokenErr = decoder.Decode(&value); tokenErr != nil {
			return tokenErr
		}
	}
	if _, err = decoder.Token(); err != nil {
		return err
	}
	if _, err = decoder.Token(); err != io.EOF {
		return errors.New("Integrity Review request contains trailing JSON")
	}
	strict := json.NewDecoder(bytes.NewReader(encoded))
	strict.DisallowUnknownFields()
	return strict.Decode(target)
}

type examIntegrityReviewDecisionResponse struct {
	ID               string `json:"id"`
	IntegrityFlagID  string `json:"integrity_flag_id"`
	Outcome          string `json:"outcome"`
	Revision         int64  `json:"revision"`
	ActorUserID      string `json:"actor_user_id"`
	PrivateRationale string `json:"private_rationale"`
	DecidedAt        string `json:"decided_at"`
}

type examSubmissionReviewResponse struct {
	ID                      string `json:"id"`
	SubmissionID            string `json:"submission_id"`
	State                   string `json:"state"`
	ReleaseState            string `json:"release_state"`
	Revision                int64  `json:"revision"`
	CreatedByUserID         string `json:"created_by_user_id"`
	ManagerNotes            string `json:"manager_notes"`
	StudentRemarksMarkdown  string `json:"student_remarks_markdown"`
	FlagCount               int    `json:"flag_count"`
	EvidenceCount           int    `json:"evidence_count"`
	DiscrepancyCount        int    `json:"discrepancy_count"`
	EvidenceInventoryDigest string `json:"evidence_inventory_digest,omitempty"`
	CreatedAt               string `json:"created_at"`
	UpdatedAt               string `json:"updated_at"`
	FinalizedAt             string `json:"finalized_at,omitempty"`
	FinalizedByUserID       string `json:"finalized_by_user_id,omitempty"`
	ReleasedAt              string `json:"released_at,omitempty"`
	ReleasedByUserID        string `json:"released_by_user_id,omitempty"`
}

type examIntegrityReviewResponse struct {
	SubmissionID             string                                `json:"submission_id"`
	ExamID                   string                                `json:"exam_id"`
	ExamSittingID            string                                `json:"exam_sitting_id"`
	ExamAttemptID            string                                `json:"exam_attempt_id"`
	CandidateUserID          string                                `json:"candidate_user_id"`
	IntegrityState           string                                `json:"integrity_state"`
	UnresolvedIntegrityCount int64                                 `json:"unresolved_integrity_count"`
	Review                   *examSubmissionReviewResponse         `json:"review,omitempty"`
	Decisions                []examIntegrityReviewDecisionResponse `json:"decisions"`
}

type examIntegrityReviewMutationResponse struct {
	SubmissionID    string                               `json:"submission_id"`
	ExamAttemptID   string                               `json:"exam_attempt_id"`
	CandidateUserID string                               `json:"candidate_user_id"`
	Review          examSubmissionReviewResponse         `json:"review"`
	Decision        *examIntegrityReviewDecisionResponse `json:"decision,omitempty"`
}

type examIntegrityFlagResponse struct {
	ID                     string `json:"id"`
	ExamAttemptID          string `json:"exam_attempt_id"`
	Generation             int64  `json:"generation"`
	PolicyKind             string `json:"policy_kind"`
	State                  string `json:"state"`
	CreatedAt              string `json:"created_at"`
	EvidenceCount          int    `json:"evidence_count"`
	EvidenceOverflowCount  int64  `json:"evidence_overflow_count"`
	UnresolvedMissingCount int64  `json:"unresolved_missing_count"`
}

type examIntegrityFlagListResponse struct {
	Items      []examIntegrityFlagResponse `json:"items"`
	NextCursor string                      `json:"next_cursor,omitempty"`
}

type examIntegrityEvidenceResponse struct {
	ID                   string `json:"id"`
	ExamAttemptID        string `json:"exam_attempt_id"`
	ParticipationID      string `json:"participation_id"`
	IntegrityFlagID      string `json:"integrity_flag_id"`
	Generation           int64  `json:"generation"`
	PolicyKind           string `json:"policy_kind"`
	SignalID             string `json:"signal_id,omitempty"`
	Sequence             int64  `json:"sequence"`
	DurationMilliseconds int64  `json:"duration_milliseconds"`
	Source               string `json:"source,omitempty"`
	MissingBefore        int64  `json:"missing_before"`
	ObservedAt           string `json:"observed_at"`
	RecordedAt           string `json:"recorded_at"`
}

type examIntegrityEvidenceListResponse struct {
	Items      []examIntegrityEvidenceResponse `json:"items"`
	NextCursor string                          `json:"next_cursor,omitempty"`
}

type examIntegrityDiscrepancyResponse struct {
	ID                   string `json:"id"`
	ExamAttemptID        string `json:"exam_attempt_id"`
	ParticipationID      string `json:"participation_id"`
	Generation           int64  `json:"generation"`
	Kind                 string `json:"kind"`
	SchemaVersion        int    `json:"schema_version"`
	SignalID             string `json:"focus_loss_signal_id"`
	Sequence             int64  `json:"sequence"`
	DurationMilliseconds int64  `json:"duration_milliseconds"`
	Source               string `json:"source,omitempty"`
	MissingBefore        int64  `json:"missing_before"`
	ReceivedAt           string `json:"received_at"`
}

type examIntegrityDiscrepancyListResponse struct {
	Items      []examIntegrityDiscrepancyResponse `json:"items"`
	NextCursor string                             `json:"next_cursor,omitempty"`
}

type studentExamResultResponse struct {
	SubmissionReviewID     string `json:"submission_review_id"`
	SubmissionID           string `json:"submission_id"`
	ExamAttemptID          string `json:"exam_attempt_id"`
	StudentRemarksMarkdown string `json:"student_remarks_markdown"`
	ReleasedAt             string `json:"released_at"`
}

func examIntegrityReviewResource(application ExamIntegrityReviewApplication) resource {
	module := examIntegrityReviewHTTPModule{application: application}
	base := apiPath(literal("submissions"), canonicalID("submission_id"))
	flags := appendRoutePath(base, literal("integrity-flags"))
	discrepancies := appendRoutePath(base, literal("integrity-discrepancies"))
	flag := appendRoutePath(flags, canonicalID("integrity_flag_id"))
	evidence := appendRoutePath(flag, literal("evidence"))
	review := appendRoutePath(base, literal("review"))
	decision := appendRoutePath(review, literal("decisions"), canonicalID("integrity_flag_id"))
	finalize := appendRoutePath(review, literal("finalize"))
	release := appendRoutePath(review, literal("release"))
	candidateResult := apiPath(literal("exam-attempts"), canonicalID("exam_attempt_id"), literal("result"))
	readErrors := academicReadErrorCodes("request.invalid", "resource.not_found", "exam.integrity_review.invalid",
		"exam.integrity_review.unavailable")
	mutationErrors := academicMutationErrorCodes("request.invalid", "resource.not_found", "exam.integrity_review.invalid",
		"exam.integrity_review.revision_conflict", "exam.integrity_review.state_conflict", "exam.integrity_review.incomplete",
		"exam.integrity_review.too_large", "exam.integrity_review.unavailable", "idempotency.key_required",
		"idempotency.invalid_key", "idempotency.conflict", "idempotency.in_progress")
	studentErrors := personalAccessTokenSessionCodes("request.invalid", "resource.not_found",
		"exam.integrity_review.invalid", "exam.integrity_review.unavailable")
	return newResource("exam-integrity-reviews",
		principalRoute(http.MethodGet, flags, readErrors, module.listFlags),
		principalRoute(http.MethodGet, evidence, readErrors, module.listEvidence),
		principalRoute(http.MethodGet, discrepancies, readErrors, module.listDiscrepancies),
		principalRoute(http.MethodGet, review, readErrors, module.get),
		idempotentPrincipalRoute(IdempotencyRequired, http.MethodPut, review, mutationErrors, module.update),
		idempotentPrincipalRoute(IdempotencyRequired, http.MethodPut, decision, mutationErrors, module.saveDecision),
		idempotentPrincipalRoute(IdempotencyRequired, http.MethodPost, finalize, mutationErrors, module.finalize),
		idempotentPrincipalRoute(IdempotencyRequired, http.MethodPost, release, mutationErrors, module.release),
		sessionRoute(http.MethodGet, candidateResult, studentErrors, module.studentResult),
	)
}

func (module examIntegrityReviewHTTPModule) get(request operationRequest) (operationResult, error) {
	submissionID, err := reviewSubmissionID(request)
	if err != nil {
		return operationResult{}, err
	}
	snapshot, err := module.application.GetExamIntegrityReview(request.context, request.invocation(), submissionID)
	if err != nil {
		return operationResult{}, err
	}
	response, err := examIntegrityReviewSnapshotResponse(snapshot)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, response).withHeaders(noStoreHeaders()), nil
}

func (module examIntegrityReviewHTTPModule) listFlags(request operationRequest) (operationResult, error) {
	submissionID, err := reviewSubmissionID(request)
	if err != nil {
		return operationResult{}, err
	}
	query := application.ListExamIntegrityFlagsQuery{SubmissionID: submissionID, Limit: 50}
	if err = applyReviewPageQuery(request, "flag", &query.Limit, func(id string) error {
		query.AfterFlagID, err = model.ParseIntegrityFlagID(id)
		return err
	}); err != nil {
		return operationResult{}, err
	}
	page, err := module.application.ListExamIntegrityFlags(request.context, request.invocation(), query)
	if err != nil {
		return operationResult{}, err
	}
	response := examIntegrityFlagListResponse{Items: make([]examIntegrityFlagResponse, 0, len(page.Items))}
	for _, item := range page.Items {
		response.Items = append(response.Items, examIntegrityFlagResponse{ID: item.Flag.ID.String(),
			ExamAttemptID: item.Flag.AttemptID.String(), Generation: item.Flag.Generation, PolicyKind: string(item.Flag.Kind),
			State: string(item.Flag.State), CreatedAt: model.TimeUTC(item.Flag.CreatedAt).Format(time.RFC3339Nano),
			EvidenceCount: item.EvidenceCount, EvidenceOverflowCount: item.OverflowCount,
			UnresolvedMissingCount: item.UnresolvedMissingCount})
	}
	if page.HasMore {
		if len(page.Items) == 0 {
			return operationResult{}, errors.New("Integrity Review application returned an invalid Flag page")
		}
		response.NextCursor = encodeExamIntegrityReviewCursor("flag", page.Items[len(page.Items)-1].Flag.ID.String())
	}
	return jsonResult(http.StatusOK, response).withHeaders(noStoreHeaders()), nil
}

func (module examIntegrityReviewHTTPModule) listEvidence(request operationRequest) (operationResult, error) {
	submissionID, err := reviewSubmissionID(request)
	if err != nil {
		return operationResult{}, err
	}
	flagID, err := reviewFlagID(request)
	if err != nil {
		return operationResult{}, err
	}
	query := application.ListExamIntegrityEvidenceQuery{SubmissionID: submissionID, FlagID: flagID, Limit: 50}
	if err = applyReviewPageQuery(request, "evidence", &query.Limit, func(id string) error {
		query.AfterEvidenceID, err = model.ParseIntegrityEvidenceID(id)
		return err
	}); err != nil {
		return operationResult{}, err
	}
	page, err := module.application.ListExamIntegrityEvidence(request.context, request.invocation(), query)
	if err != nil {
		return operationResult{}, err
	}
	response := examIntegrityEvidenceListResponse{Items: make([]examIntegrityEvidenceResponse, 0, len(page.Items))}
	for _, item := range page.Items {
		response.Items = append(response.Items, examIntegrityEvidenceResponse{ID: item.ID.String(),
			ExamAttemptID: item.AttemptID.String(), ParticipationID: item.ParticipationID.String(),
			IntegrityFlagID: item.FlagID.String(), Generation: item.Generation, PolicyKind: string(item.Kind),
			SignalID: item.SignalID.String(), Sequence: item.Sequence, DurationMilliseconds: item.DurationMilliseconds,
			Source: string(item.Source), MissingBefore: item.MissingBefore,
			ObservedAt: model.TimeUTC(item.ObservedAt).Format(time.RFC3339Nano),
			RecordedAt: model.TimeUTC(item.RecordedAt).Format(time.RFC3339Nano)})
	}
	if page.HasMore {
		if len(page.Items) == 0 {
			return operationResult{}, errors.New("Integrity Review application returned an invalid Evidence page")
		}
		response.NextCursor = encodeExamIntegrityReviewCursor("evidence", page.Items[len(page.Items)-1].ID.String())
	}
	return jsonResult(http.StatusOK, response).withHeaders(noStoreHeaders()), nil
}

func (module examIntegrityReviewHTTPModule) listDiscrepancies(request operationRequest) (operationResult, error) {
	submissionID, err := reviewSubmissionID(request)
	if err != nil {
		return operationResult{}, err
	}
	query := application.ListExamIntegrityDiscrepanciesQuery{SubmissionID: submissionID, Limit: 50}
	if err = applyReviewPageQuery(request, "discrepancy", &query.Limit, func(id string) error {
		query.AfterDiscrepancyID, err = model.ParseIntegrityDiscrepancyID(id)
		return err
	}); err != nil {
		return operationResult{}, err
	}
	page, err := module.application.ListExamIntegrityDiscrepancies(request.context, request.invocation(), query)
	if err != nil {
		return operationResult{}, err
	}
	response := examIntegrityDiscrepancyListResponse{Items: make([]examIntegrityDiscrepancyResponse, 0, len(page.Items))}
	for _, item := range page.Items {
		response.Items = append(response.Items, examIntegrityDiscrepancyResponse{ID: item.ID.String(),
			ExamAttemptID: item.AttemptID.String(), ParticipationID: item.ParticipationID.String(),
			Generation: item.Generation, Kind: string(item.Kind), SchemaVersion: item.SchemaVersion,
			SignalID: item.SignalID.String(), Sequence: item.Sequence, DurationMilliseconds: item.DurationMilliseconds,
			Source: string(item.Source), MissingBefore: item.MissingBefore,
			ReceivedAt: model.TimeUTC(item.ReceivedAt).Format(time.RFC3339Nano)})
	}
	if page.HasMore {
		if len(page.Items) == 0 {
			return operationResult{}, errors.New("Integrity Review application returned an invalid Discrepancy page")
		}
		response.NextCursor = encodeExamIntegrityReviewCursor("discrepancy", page.Items[len(page.Items)-1].ID.String())
	}
	return jsonResult(http.StatusOK, response).withHeaders(noStoreHeaders()), nil
}

func (module examIntegrityReviewHTTPModule) saveDecision(request operationRequest) (operationResult, error) {
	submissionID, err := reviewSubmissionID(request)
	if err != nil {
		return operationResult{}, err
	}
	flagID, err := reviewFlagID(request)
	if err != nil {
		return operationResult{}, err
	}
	var body saveExamIntegrityDecisionRequest
	if err = request.decodeJSON(&body, "saveExamIntegrityDecision"); err != nil {
		return operationResult{}, err
	}
	reviewID, err := optionalSubmissionReviewID(body.ReviewID)
	if err != nil {
		return operationResult{}, invalidRequestError("submission_review_id", err)
	}
	result, err := module.application.SaveExamIntegrityDecision(request.context, request.invocation(),
		application.SaveExamIntegrityDecisionCommand{SubmissionID: submissionID, ReviewID: reviewID, FlagID: flagID,
			ExpectedReviewRevision: body.ExpectedReviewRevision, ExpectedDecisionRevision: body.ExpectedDecisionRevision,
			Outcome: model.IntegrityReviewOutcome(body.Outcome), PrivateRationale: body.PrivateRationale,
			IdempotencyKey: request.idempotencyKey})
	if err != nil {
		return operationResult{}, err
	}
	return reviewMutationResult(result)
}

func (module examIntegrityReviewHTTPModule) update(request operationRequest) (operationResult, error) {
	submissionID, err := reviewSubmissionID(request)
	if err != nil {
		return operationResult{}, err
	}
	var body updateExamIntegrityReviewRequest
	if err = request.decodeJSON(&body, "updateExamIntegrityReview"); err != nil {
		return operationResult{}, err
	}
	reviewID, err := optionalSubmissionReviewID(body.ReviewID)
	if err != nil {
		return operationResult{}, invalidRequestError("submission_review_id", err)
	}
	result, err := module.application.UpdateExamIntegrityReview(request.context, request.invocation(),
		application.UpdateExamIntegrityReviewCommand{SubmissionID: submissionID, ReviewID: reviewID,
			ExpectedReviewRevision: body.ExpectedReviewRevision, ManagerNotes: body.ManagerNotes,
			StudentRemarksMarkdown: body.StudentRemarksMarkdown, IdempotencyKey: request.idempotencyKey})
	if err != nil {
		return operationResult{}, err
	}
	return reviewMutationResult(result)
}

type integrityReviewTerminalOperation uint8

const (
	integrityReviewFinalize integrityReviewTerminalOperation = iota + 1
	integrityReviewRelease
)

func (module examIntegrityReviewHTTPModule) finalize(request operationRequest) (operationResult, error) {
	return module.terminal(request, integrityReviewFinalize)
}

func (module examIntegrityReviewHTTPModule) release(request operationRequest) (operationResult, error) {
	return module.terminal(request, integrityReviewRelease)
}

func (module examIntegrityReviewHTTPModule) terminal(request operationRequest,
	operation integrityReviewTerminalOperation,
) (operationResult, error) {
	if operation != integrityReviewFinalize && operation != integrityReviewRelease {
		return operationResult{}, invalidRequestError("operation", errors.New("unsupported Review terminal operation"))
	}
	submissionID, err := reviewSubmissionID(request)
	if err != nil {
		return operationResult{}, err
	}
	var body terminalExamIntegrityReviewRequest
	if err = request.decodeJSON(&body, "terminalExamIntegrityReview"); err != nil {
		return operationResult{}, err
	}
	reviewID, err := model.ParseSubmissionReviewID(body.ReviewID)
	if err != nil {
		return operationResult{}, invalidRequestError("submission_review_id", err)
	}
	command := application.FinalizeExamIntegrityReviewCommand{SubmissionID: submissionID, ReviewID: reviewID,
		ExpectedReviewRevision: body.ExpectedReviewRevision, IdempotencyKey: request.idempotencyKey}
	var result application.ExamIntegrityReviewResult
	if operation == integrityReviewRelease {
		result, err = module.application.ReleaseStudentExamResult(request.context, request.invocation(), command)
	} else {
		result, err = module.application.FinalizeExamIntegrityReview(request.context, request.invocation(), command)
	}
	if err != nil {
		return operationResult{}, err
	}
	return reviewMutationResult(result)
}

func (module examIntegrityReviewHTTPModule) studentResult(request operationRequest) (operationResult, error) {
	raw, err := request.params.RequireExamAttemptId()
	if err != nil {
		return operationResult{}, err
	}
	attemptID, err := model.ParseExamAttemptID(raw)
	if err != nil {
		return operationResult{}, invalidRequestError("exam_attempt_id", err)
	}
	result, err := module.application.GetStudentExamResult(request.context, request.invocation(), attemptID)
	if err != nil {
		return operationResult{}, err
	}
	response := studentExamResultResponse{SubmissionReviewID: result.ReviewID.String(),
		SubmissionID: result.SubmissionID.String(), ExamAttemptID: result.AttemptID.String(),
		StudentRemarksMarkdown: result.StudentRemarksMarkdown,
		ReleasedAt:             model.TimeUTC(result.ReleasedAt).Format(time.RFC3339Nano)}
	return jsonResult(http.StatusOK, response).withHeaders(noStoreHeaders()), nil
}

func reviewMutationResult(result application.ExamIntegrityReviewResult) (operationResult, error) {
	if result.Review == nil {
		return operationResult{}, errors.New("Integrity Review application returned an empty Review")
	}
	response := examIntegrityReviewMutationResponse{SubmissionID: result.Authorization.SubmissionID.String(),
		ExamAttemptID: result.Authorization.AttemptID.String(), CandidateUserID: result.Authorization.CandidateUserID.String(),
		Review: examSubmissionReviewDTO(*result.Review)}
	if result.Decision != nil {
		decision := examIntegrityReviewDecisionDTO(*result.Decision)
		response.Decision = &decision
	}
	return jsonResult(http.StatusOK, response).withHeaders(noStoreHeaders()), nil
}

func examIntegrityReviewSnapshotResponse(snapshot *application.ExamSubmissionReviewSnapshot) (examIntegrityReviewResponse, error) {
	if snapshot == nil || snapshot.Submission == nil {
		return examIntegrityReviewResponse{}, errors.New("Integrity Review application returned an empty snapshot")
	}
	response := examIntegrityReviewResponse{SubmissionID: snapshot.Authorization.SubmissionID.String(),
		ExamID: snapshot.Authorization.ExamID.String(), ExamSittingID: snapshot.Authorization.SittingID.String(),
		ExamAttemptID: snapshot.Authorization.AttemptID.String(), CandidateUserID: snapshot.Authorization.CandidateUserID.String(),
		IntegrityState:           string(snapshot.Submission.IntegrityState),
		UnresolvedIntegrityCount: snapshot.Submission.UnresolvedIntegrityCount,
		Decisions:                make([]examIntegrityReviewDecisionResponse, 0, len(snapshot.Decisions))}
	if snapshot.Review != nil {
		review := examSubmissionReviewDTO(*snapshot.Review)
		response.Review = &review
	}
	for _, item := range snapshot.Decisions {
		response.Decisions = append(response.Decisions, examIntegrityReviewDecisionDTO(item))
	}
	return response, nil
}

func examSubmissionReviewDTO(review model.SubmissionReview) examSubmissionReviewResponse {
	response := examSubmissionReviewResponse{ID: review.ID.String(), SubmissionID: review.SubmissionID.String(),
		State: string(review.State), ReleaseState: string(review.ReleaseState), Revision: review.Revision,
		CreatedByUserID: review.CreatedByUserID.String(), ManagerNotes: review.ManagerNotes,
		StudentRemarksMarkdown: review.StudentRemarksMarkdown, FlagCount: review.FlagCount,
		EvidenceCount: review.EvidenceCount, DiscrepancyCount: review.DiscrepancyCount,
		EvidenceInventoryDigest: review.EvidenceInventoryDigest,
		CreatedAt:               model.TimeUTC(review.CreatedAt).Format(time.RFC3339Nano),
		UpdatedAt:               model.TimeUTC(review.UpdatedAt).Format(time.RFC3339Nano)}
	if review.FinalizedAt.Valid {
		response.FinalizedAt = model.TimeUTC(review.FinalizedAt.Time).Format(time.RFC3339Nano)
		response.FinalizedByUserID = review.FinalizedByUserID.String()
	}
	if review.ReleasedAt.Valid {
		response.ReleasedAt = model.TimeUTC(review.ReleasedAt.Time).Format(time.RFC3339Nano)
		response.ReleasedByUserID = review.ReleasedByUserID.String()
	}
	return response
}

func examIntegrityReviewDecisionDTO(decision model.IntegrityReviewDecision) examIntegrityReviewDecisionResponse {
	return examIntegrityReviewDecisionResponse{ID: decision.ID.String(), IntegrityFlagID: decision.FlagID.String(),
		Outcome: string(decision.Outcome), Revision: decision.Revision, ActorUserID: decision.ActorUserID.String(),
		PrivateRationale: decision.PrivateRationale,
		DecidedAt:        model.TimeUTC(decision.DecidedAt).Format(time.RFC3339Nano)}
}

func reviewSubmissionID(request operationRequest) (model.SubmissionID, error) {
	raw, err := request.params.RequireSubmissionId()
	if err != nil {
		return "", err
	}
	id, err := model.ParseSubmissionID(raw)
	if err != nil {
		return "", invalidRequestError("submission_id", err)
	}
	return id, nil
}

func reviewFlagID(request operationRequest) (model.IntegrityFlagID, error) {
	raw, err := request.params.RequireIntegrityFlagID()
	if err != nil {
		return "", err
	}
	id, err := model.ParseIntegrityFlagID(raw)
	if err != nil {
		return "", invalidRequestError("integrity_flag_id", err)
	}
	return id, nil
}

func optionalSubmissionReviewID(raw string) (model.SubmissionReviewID, error) {
	if raw == "" {
		return "", nil
	}
	return model.ParseSubmissionReviewID(raw)
}

func applyReviewPageQuery(request operationRequest, kind string, limit *int, setCursor func(string) error) error {
	values := request.request.URL.Query()
	if raw := values.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 200 {
			return invalidRequestError("limit", errors.New("must be between 1 and 200"))
		}
		*limit = parsed
	}
	if raw := values.Get("cursor"); raw != "" {
		id, err := decodeExamIntegrityReviewCursor(raw, kind)
		if err != nil {
			return invalidRequestError("cursor", err)
		}
		if err = setCursor(id); err != nil {
			return invalidRequestError("cursor", err)
		}
	}
	return nil
}
