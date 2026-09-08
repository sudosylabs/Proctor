// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type ExamRecordsApplication interface {
	GetExamSittingRecords(context.Context, application.Invocation, model.ExamID, model.ExamSittingID) (*application.ExamSittingRecordsSnapshot, error)
	GetSubmissionReviewWaiver(context.Context, application.Invocation, model.RetentionHoldScope) (*model.SubmissionReviewWaiver, error)
	CompleteExamSittingRecords(context.Context, application.Invocation, application.CompleteExamSittingRecordsCommand) (*model.ExamSittingRecordsCompletion, error)
	WaiveSubmissionReview(context.Context, application.Invocation, application.WaiveSubmissionReviewCommand) (*model.SubmissionReviewWaiver, error)
	ListRetentionHolds(context.Context, application.Invocation, application.ListRetentionHoldsQuery) (*application.RetentionHoldPage, error)
	CreateRetentionHold(context.Context, application.Invocation, application.CreateRetentionHoldCommand) (*model.RetentionHold, error)
	ReleaseRetentionHold(context.Context, application.Invocation, application.ReleaseRetentionHoldCommand) (*model.RetentionHold, error)
}

type examRecordsHTTPModule struct{ application ExamRecordsApplication }

func examRecordsResource(application ExamRecordsApplication) resource {
	m := examRecordsHTTPModule{application: application}
	completion := apiPath(literal("exams"), canonicalID("exam_id"), literal("sittings"), canonicalID("exam_sitting_id"), literal("records-completion"))
	waiver := apiPath(literal("exams"), canonicalID("exam_id"), literal("sittings"), canonicalID("exam_sitting_id"), literal("submissions"), canonicalID("submission_id"), literal("review-waiver"))
	holds := apiPath(literal("exams"), canonicalID("exam_id"), literal("retention-holds"))
	release := apiPath(literal("exams"), canonicalID("exam_id"), literal("retention-holds"), canonicalID("retention_hold_id"), literal("release"))
	readErrors := academicReadErrorCodes("request.invalid", "resource.not_found", "exam.invalid", "exam.unavailable")
	mutationErrors := academicMutationErrorCodes("request.invalid", "resource.not_found", "exam.invalid", "exam.conflict", "exam.unavailable",
		"idempotency.key_required", "idempotency.invalid_key", "idempotency.conflict", "idempotency.in_progress")
	completeRoute := sessionRoute(http.MethodPost, completion, mutationErrors, m.complete)
	waiveRoute := sessionRoute(http.MethodPost, waiver, mutationErrors, m.waive)
	createRoute := sessionRoute(http.MethodPost, holds, mutationErrors, m.createHold)
	releaseRoute := strongRecentSessionRoute(http.MethodPost, release, append(append([]string(nil), mutationErrors...), "authentication.strong_required", "authentication.reauthentication_required"), m.releaseHold)
	completeRoute.idempotency, waiveRoute.idempotency, createRoute.idempotency, releaseRoute.idempotency = IdempotencyRequired, IdempotencyRequired, IdempotencyRequired, IdempotencyRequired
	return newResource("exam-records", sessionRoute(http.MethodGet, completion, readErrors, m.getCompletion), completeRoute,
		sessionRoute(http.MethodGet, waiver, readErrors, m.getWaiver), waiveRoute,
		sessionRoute(http.MethodGet, holds, readErrors, m.listHolds), createRoute, releaseRoute)
}

type completeExamRecordsRequest struct {
	ExpectedRevision             Optional[int64] `json:"expected_revision"`
	AcknowledgedEvidenceRevision Optional[int64] `json:"acknowledged_evidence_revision"`
}
type waiveSubmissionReviewRequest struct {
	ExpectedDeliveryInventoryRevision Optional[int64] `json:"expected_delivery_inventory_revision,omitempty"`
	ExpectedRevision                  Optional[int64] `json:"expected_revision"`
	ExpectedReviewRevision            Optional[int64] `json:"expected_review_revision"`
	ExpectedDiscrepancyCount          Optional[int64] `json:"expected_discrepancy_count"`
	ReasonCode                        string          `json:"reason_code"`
	PrivateReason                     string          `json:"private_reason"`
}
type createRetentionHoldRequest struct {
	SittingID     Optional[string] `json:"exam_sitting_id"`
	SubmissionID  Optional[string] `json:"submission_id"`
	ReasonCode    string           `json:"reason_code"`
	PrivateReason string           `json:"private_reason"`
}
type releaseRetentionHoldRequest struct {
	SittingID        Optional[string] `json:"exam_sitting_id"`
	SubmissionID     Optional[string] `json:"submission_id"`
	ExpectedRevision Optional[int64]  `json:"expected_revision"`
	ReasonCode       string           `json:"reason_code"`
	PrivateReason    string           `json:"private_reason"`
}

func (b *completeExamRecordsRequest) UnmarshalJSON(data []byte) error {
	type wire completeExamRecordsRequest
	var value wire
	if err := decodeDuplicateFreeExamIntegrityReviewObject(data, &value); err != nil {
		return err
	}
	*b = completeExamRecordsRequest(value)
	return nil
}
func (b *waiveSubmissionReviewRequest) UnmarshalJSON(data []byte) error {
	type wire waiveSubmissionReviewRequest
	var value wire
	if err := decodeDuplicateFreeExamIntegrityReviewObject(data, &value); err != nil {
		return err
	}
	*b = waiveSubmissionReviewRequest(value)
	return nil
}
func (b *createRetentionHoldRequest) UnmarshalJSON(data []byte) error {
	type wire createRetentionHoldRequest
	var value wire
	if err := decodeDuplicateFreeExamIntegrityReviewObject(data, &value); err != nil {
		return err
	}
	*b = createRetentionHoldRequest(value)
	return nil
}
func (b *releaseRetentionHoldRequest) UnmarshalJSON(data []byte) error {
	type wire releaseRetentionHoldRequest
	var value wire
	if err := decodeDuplicateFreeExamIntegrityReviewObject(data, &value); err != nil {
		return err
	}
	*b = releaseRetentionHoldRequest(value)
	return nil
}

type examRecordsCompletionResponse struct {
	SittingID                 string `json:"exam_sitting_id"`
	Revision                  int64  `json:"revision"`
	EvidenceRevision          int64  `json:"evidence_revision"`
	CompletedEvidenceRevision int64  `json:"completed_evidence_revision"`
	Current                   bool   `json:"current"`
	CompletedAt               string `json:"completed_at,omitempty"`
	CompletedByUserID         string `json:"completed_by_user_id,omitempty"`
	StaleAt                   string `json:"stale_at,omitempty"`
}
type examRecordsSnapshotResponse struct {
	Completion      examRecordsCompletionResponse `json:"completion"`
	SittingState    string                        `json:"sitting_state"`
	SubmissionCount int64                         `json:"submission_count"`
	PendingReviews  int64                         `json:"pending_reviews"`
}
type submissionReviewWaiverResponse struct {
	DeliveryInventoryRevision int64  `json:"delivery_inventory_revision"`
	InventoryInvalidated      bool   `json:"inventory_invalidated"`
	SubmissionID              string `json:"submission_id"`
	Revision                  int64  `json:"revision"`
	ReviewRevision            int64  `json:"review_revision"`
	DiscrepancyCount          int64  `json:"discrepancy_count"`
	ActorUserID               string `json:"actor_user_id"`
	RecordedAt                string `json:"recorded_at"`
	ReasonCode                string `json:"reason_code"`
	PrivateReason             string `json:"private_reason"`
}
type submissionReviewWaiverEnvelope struct {
	Waiver *submissionReviewWaiverResponse `json:"waiver"`
}
type retentionHoldResponse struct {
	ID                              string `json:"id"`
	ExamID                          string `json:"exam_id"`
	SittingID                       string `json:"exam_sitting_id,omitempty"`
	SubmissionID                    string `json:"submission_id,omitempty"`
	Revision                        int64  `json:"revision"`
	CreatedAt                       string `json:"created_at"`
	CreatedByUserID                 string `json:"created_by_user_id"`
	ReasonCode                      string `json:"reason_code"`
	PrivateReason                   string `json:"private_reason"`
	ReleasedAt                      string `json:"released_at,omitempty"`
	ReleasedByUserID                string `json:"released_by_user_id,omitempty"`
	ReleaseReasonCode               string `json:"release_reason_code,omitempty"`
	ReleasePrivateReason            string `json:"release_private_reason,omitempty"`
	WorkRetiredSubmissionCount      int64  `json:"work_retired_submission_count"`
	IntegrityRetiredSubmissionCount int64  `json:"integrity_retired_submission_count"`
}
type retentionHoldPageResponse struct {
	Items      []retentionHoldResponse `json:"items"`
	NextCursor string                  `json:"next_cursor,omitempty"`
}

func (m examRecordsHTTPModule) getCompletion(request operationRequest) (operationResult, error) {
	examID, sittingID, err := examSittingIDs(request)
	if err != nil {
		return operationResult{}, err
	}
	value, err := m.application.GetExamSittingRecords(request.context, request.invocation(), examID, sittingID)
	if err != nil {
		return operationResult{}, err
	}
	if value == nil {
		return operationResult{}, application.NewError("exam.unavailable")
	}
	return jsonResult(http.StatusOK, examRecordsSnapshotResponse{Completion: recordsCompletionResponse(value.Completion), SittingState: string(value.SittingState), SubmissionCount: value.SubmissionCount, PendingReviews: value.PendingReviews}).withHeaders(privateNoStoreHeaders()), nil
}
func (m examRecordsHTTPModule) complete(request operationRequest) (operationResult, error) {
	examID, sittingID, err := examSittingIDs(request)
	if err != nil {
		return operationResult{}, err
	}
	var body completeExamRecordsRequest
	if err = request.decodeJSON(&body, "completeExamSittingRecords"); err != nil {
		return operationResult{}, err
	}
	revision, err := requiredRecordsInteger("expected_revision", body.ExpectedRevision, 1)
	if err != nil {
		return operationResult{}, err
	}
	evidence, err := requiredRecordsInteger("acknowledged_evidence_revision", body.AcknowledgedEvidenceRevision, 0)
	if err != nil {
		return operationResult{}, err
	}
	value, err := m.application.CompleteExamSittingRecords(request.context, request.invocation(), application.CompleteExamSittingRecordsCommand{
		Scope: model.RetentionHoldScope{ExamID: examID, SittingID: sittingID}, ExpectedRevision: revision, AcknowledgedEvidenceRevision: evidence, IdempotencyKey: request.idempotencyKey})
	if err != nil {
		return operationResult{}, err
	}
	if value == nil {
		return operationResult{}, application.NewError("exam.unavailable")
	}
	return jsonResult(http.StatusOK, recordsCompletionResponse(*value)).withHeaders(privateNoStoreHeaders()), nil
}
func (m examRecordsHTTPModule) getWaiver(request operationRequest) (operationResult, error) {
	scope, err := recordsSubmissionScope(request)
	if err != nil {
		return operationResult{}, err
	}
	value, err := m.application.GetSubmissionReviewWaiver(request.context, request.invocation(), scope)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, submissionReviewWaiverEnvelope{Waiver: recordsWaiverResponse(value)}).withHeaders(privateNoStoreHeaders()), nil
}
func (m examRecordsHTTPModule) waive(request operationRequest) (operationResult, error) {
	scope, err := recordsSubmissionScope(request)
	if err != nil {
		return operationResult{}, err
	}
	var body waiveSubmissionReviewRequest
	if err = request.decodeJSON(&body, "waiveSubmissionReview"); err != nil {
		return operationResult{}, err
	}
	revision, err := requiredRecordsInteger("expected_revision", body.ExpectedRevision, 0)
	if err != nil {
		return operationResult{}, err
	}
	reviewRevision, err := requiredRecordsInteger("expected_review_revision", body.ExpectedReviewRevision, 0)
	if err != nil {
		return operationResult{}, err
	}
	discrepancies, err := requiredRecordsInteger("expected_discrepancy_count", body.ExpectedDiscrepancyCount, 0)
	if err != nil {
		return operationResult{}, err
	}
	if err = model.ValidateRecordsReason(body.ReasonCode, body.PrivateReason); err != nil {
		return operationResult{}, invalidRequestError("reason", err)
	}
	var deliveryRevision int64
	if body.ExpectedDeliveryInventoryRevision.IsSet() {
		deliveryRevision, err = requiredRecordsInteger("expected_delivery_inventory_revision", body.ExpectedDeliveryInventoryRevision, 0)
		if err != nil {
			return operationResult{}, err
		}
	}
	value, err := m.application.WaiveSubmissionReview(request.context, request.invocation(), application.WaiveSubmissionReviewCommand{Scope: scope, ExpectedRevision: revision,
		ExpectedDeliveryInventoryRevision: deliveryRevision, ExpectedReviewRevision: reviewRevision, ExpectedDiscrepancyCount: discrepancies, ReasonCode: body.ReasonCode, PrivateReason: body.PrivateReason, IdempotencyKey: request.idempotencyKey})
	if err != nil {
		return operationResult{}, err
	}
	if value == nil {
		return operationResult{}, application.NewError("exam.unavailable")
	}
	return jsonResult(http.StatusOK, submissionReviewWaiverEnvelope{Waiver: recordsWaiverResponse(value)}).withHeaders(privateNoStoreHeaders()), nil
}
func (m examRecordsHTTPModule) createHold(request operationRequest) (operationResult, error) {
	examID, err := examSittingExamID(request)
	if err != nil {
		return operationResult{}, err
	}
	var body createRetentionHoldRequest
	if err = request.decodeJSON(&body, "createRetentionHold"); err != nil {
		return operationResult{}, err
	}
	scope, err := recordsOptionalScope(examID, body.SittingID, body.SubmissionID)
	if err != nil {
		return operationResult{}, err
	}
	if err = model.ValidateRecordsReason(body.ReasonCode, body.PrivateReason); err != nil {
		return operationResult{}, invalidRequestError("reason", err)
	}
	value, err := m.application.CreateRetentionHold(request.context, request.invocation(), application.CreateRetentionHoldCommand{Scope: scope,
		ReasonCode: body.ReasonCode, PrivateReason: body.PrivateReason, IdempotencyKey: request.idempotencyKey})
	if err != nil {
		return operationResult{}, err
	}
	if value == nil {
		return operationResult{}, application.NewError("exam.unavailable")
	}
	return jsonResult(http.StatusCreated, recordsHoldResponse(*value)).withHeaders(privateNoStoreHeaders()), nil
}
func (m examRecordsHTTPModule) releaseHold(request operationRequest) (operationResult, error) {
	examID, err := examSittingExamID(request)
	if err != nil {
		return operationResult{}, err
	}
	raw, err := request.params.RequireRetentionHoldID()
	if err != nil {
		return operationResult{}, err
	}
	holdID, err := model.ParseRetentionHoldID(raw)
	if err != nil {
		return operationResult{}, invalidRequestError("retention_hold_id", err)
	}
	var body releaseRetentionHoldRequest
	if err = request.decodeJSON(&body, "releaseRetentionHold"); err != nil {
		return operationResult{}, err
	}
	scope, err := recordsOptionalScope(examID, body.SittingID, body.SubmissionID)
	if err != nil {
		return operationResult{}, err
	}
	revision, err := requiredRecordsInteger("expected_revision", body.ExpectedRevision, 1)
	if err != nil {
		return operationResult{}, err
	}
	if err = model.ValidateRecordsReason(body.ReasonCode, body.PrivateReason); err != nil {
		return operationResult{}, invalidRequestError("reason", err)
	}
	value, err := m.application.ReleaseRetentionHold(request.context, request.invocation(), application.ReleaseRetentionHoldCommand{Scope: scope, HoldID: holdID, ExpectedRevision: revision,
		ReasonCode: body.ReasonCode, PrivateReason: body.PrivateReason, IdempotencyKey: request.idempotencyKey})
	if err != nil {
		return operationResult{}, err
	}
	if value == nil {
		return operationResult{}, application.NewError("exam.unavailable")
	}
	return jsonResult(http.StatusOK, recordsHoldResponse(*value)).withHeaders(privateNoStoreHeaders()), nil
}

func (m examRecordsHTTPModule) listHolds(request operationRequest) (operationResult, error) {
	examID, err := examSittingExamID(request)
	if err != nil {
		return operationResult{}, err
	}
	values := request.request.URL.Query()
	for key, items := range values {
		if len(items) != 1 {
			return operationResult{}, invalidRequestError("query", nil)
		}
		switch key {
		case "exam_sitting_id", "submission_id", "include_released", "limit", "cursor":
		default:
			return operationResult{}, invalidRequestError("query", nil)
		}
	}
	scope := model.RetentionHoldScope{ExamID: examID, SittingID: model.ExamSittingID(values.Get("exam_sitting_id")), SubmissionID: model.SubmissionID(values.Get("submission_id"))}
	if err = scope.Validate(); err != nil {
		return operationResult{}, invalidRequestError("scope", err)
	}
	includeReleased := false
	if raw, exists := values["include_released"]; exists {
		if raw[0] != "true" && raw[0] != "false" {
			return operationResult{}, invalidRequestError("include_released", nil)
		}
		includeReleased = raw[0] == "true"
	}
	limit, err := request.queryLimit()
	if err != nil {
		return operationResult{}, err
	}
	query := application.ListRetentionHoldsQuery{Scope: scope, Limit: limit, IncludeReleased: includeReleased}
	if raw := values.Get("cursor"); raw != "" {
		cursor, decodeErr := decodeOpaqueCursor(raw, recordsHoldCursorSpec(scope, includeReleased))
		if decodeErr != nil {
			return operationResult{}, invalidRequestError("cursor", decodeErr)
		}
		query.After = model.RetentionHoldID(cursor.AfterID)
	}
	page, err := m.application.ListRetentionHolds(request.context, request.invocation(), query)
	if err != nil {
		return operationResult{}, err
	}
	if page == nil || len(page.Holds) > limit || page.HasMore && len(page.Holds) == 0 {
		return operationResult{}, application.NewError("exam.unavailable")
	}
	response := retentionHoldPageResponse{Items: make([]retentionHoldResponse, 0, len(page.Holds))}
	for _, hold := range page.Holds {
		response.Items = append(response.Items, recordsHoldResponse(hold))
	}
	if page.HasMore {
		response.NextCursor, err = encodeOpaqueCursor(recordsHoldCursor{ExamID: scope.ExamID.String(), SittingID: scope.SittingID.String(), SubmissionID: scope.SubmissionID.String(), IncludeReleased: includeReleased,
			AfterID: page.Holds[len(page.Holds)-1].ID.String()}, recordsHoldCursorSpec(scope, includeReleased))
		if err != nil {
			return operationResult{}, application.NewError("exam.unavailable").Wrap(err)
		}
	}
	return jsonResult(http.StatusOK, response).withHeaders(privateNoStoreHeaders()), nil
}

func requiredRecordsInteger(field string, value Optional[int64], minimum int64) (int64, error) {
	p := value.ValuePointer()
	if p == nil || *p < minimum {
		return 0, invalidRequestError(field, nil)
	}
	return *p, nil
}
func recordsSubmissionScope(request operationRequest) (model.RetentionHoldScope, error) {
	examID, sittingID, err := examSittingIDs(request)
	if err != nil {
		return model.RetentionHoldScope{}, err
	}
	raw, err := request.params.RequireSubmissionId()
	if err != nil {
		return model.RetentionHoldScope{}, err
	}
	id, err := model.ParseSubmissionID(raw)
	if err != nil {
		return model.RetentionHoldScope{}, invalidRequestError("submission_id", err)
	}
	return model.RetentionHoldScope{ExamID: examID, SittingID: sittingID, SubmissionID: id}, nil
}
func recordsOptionalScope(examID model.ExamID, sitting, submission Optional[string]) (model.RetentionHoldScope, error) {
	scope := model.RetentionHoldScope{ExamID: examID}
	if sitting.IsSet() {
		if p := sitting.ValuePointer(); p == nil || *p == "" {
			return scope, invalidRequestError("exam_sitting_id", nil)
		} else {
			scope.SittingID = model.ExamSittingID(*p)
		}
	}
	if submission.IsSet() {
		if p := submission.ValuePointer(); p == nil || *p == "" {
			return scope, invalidRequestError("submission_id", nil)
		} else {
			scope.SubmissionID = model.SubmissionID(*p)
		}
	}
	if err := scope.Validate(); err != nil {
		return scope, invalidRequestError("scope", err)
	}
	return scope, nil
}

func recordsCompletionResponse(value model.ExamSittingRecordsCompletion) examRecordsCompletionResponse {
	out := examRecordsCompletionResponse{SittingID: value.SittingID.String(), Revision: value.Revision, EvidenceRevision: value.EvidenceRevision, CompletedEvidenceRevision: value.CompletedEvidenceRevision, Current: value.IsCurrent(), CompletedByUserID: value.CompletedByUserID.String()}
	if value.CompletedAt.Valid {
		out.CompletedAt = model.TimeUTC(value.CompletedAt.Time).Format(time.RFC3339Nano)
	}
	if value.StaleAt.Valid {
		out.StaleAt = model.TimeUTC(value.StaleAt.Time).Format(time.RFC3339Nano)
	}
	return out
}
func recordsWaiverResponse(value *model.SubmissionReviewWaiver) *submissionReviewWaiverResponse {
	if value == nil {
		return nil
	}
	return &submissionReviewWaiverResponse{DeliveryInventoryRevision: value.DeliveryInventoryRevision, InventoryInvalidated: value.InventoryInvalidated, SubmissionID: value.SubmissionID.String(), Revision: value.Revision, ReviewRevision: value.ReviewRevision,
		DiscrepancyCount: value.DiscrepancyCount, ActorUserID: value.ActorUserID.String(), RecordedAt: model.TimeUTC(value.RecordedAt).Format(time.RFC3339Nano), ReasonCode: value.ReasonCode, PrivateReason: value.PrivateReason}
}
func recordsHoldResponse(value model.RetentionHold) retentionHoldResponse {
	out := retentionHoldResponse{ID: value.ID.String(), ExamID: value.Scope.ExamID.String(), SittingID: value.Scope.SittingID.String(), SubmissionID: value.Scope.SubmissionID.String(), Revision: value.Revision,
		CreatedAt: model.TimeUTC(value.CreatedAt).Format(time.RFC3339Nano), CreatedByUserID: value.CreatedByUserID.String(), ReasonCode: value.ReasonCode, PrivateReason: value.PrivateReason,
		ReleasedByUserID: value.ReleasedByUserID.String(), ReleaseReasonCode: value.ReleaseReasonCode, ReleasePrivateReason: value.ReleasePrivateReason,
		WorkRetiredSubmissionCount: value.WorkRetiredSubmissionCount, IntegrityRetiredSubmissionCount: value.IntegrityRetiredSubmissionCount}
	if value.ReleasedAt.Valid {
		out.ReleasedAt = model.TimeUTC(value.ReleasedAt.Time).Format(time.RFC3339Nano)
	}
	return out
}

type recordsHoldCursor struct {
	Version         int    `json:"version"`
	ExamID          string `json:"exam_id"`
	SittingID       string `json:"exam_sitting_id"`
	SubmissionID    string `json:"submission_id"`
	IncludeReleased bool   `json:"include_released"`
	AfterID         string `json:"after_id"`
}

func recordsHoldCursorSpec(scope model.RetentionHoldScope, includeReleased bool) opaqueCursorSpec[recordsHoldCursor] {
	return opaqueCursorSpec[recordsHoldCursor]{label: "retention holds", maximumEncodedLength: 512, currentVersion: 1, members: []string{"version", "exam_id", "exam_sitting_id", "submission_id", "include_released", "after_id"},
		version: func(c recordsHoldCursor) int { return c.Version }, setVersion: func(c *recordsHoldCursor, v int) { c.Version = v }, acceptsVersion: func(v int) bool { return v == 1 }, validate: func(c recordsHoldCursor) error {
			if scope.Validate() != nil || c.ExamID != scope.ExamID.String() || c.SittingID != scope.SittingID.String() || c.SubmissionID != scope.SubmissionID.String() || c.IncludeReleased != includeReleased || !model.RetentionHoldID(c.AfterID).IsValid() {
				return errors.New("retention hold cursor scope changed")
			}
			return nil
		}}
}
