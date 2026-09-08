// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"net/http"
	"strconv"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

func nativeDeliveryErrorCodes() []string {
	return academicMutationErrorCodes("exam.delivery.control_rate_limited", "request.invalid", "resource.not_found", "exam.attempt.invalid", "exam.attempt.not_found", "exam.attempt.registered_desktop_required", "exam.attempt.continuity_invalid", "exam.attempt.connection_closed", "exam.attempt.connection_lost", "exam.attempt.sitting_unavailable", "exam.attempt.unavailable", "exam.delivery.upload_expired", "exam.delivery.declaration_conflict", "exam.delivery.sequence_limit", "exam.delivery.missing_interval_limit", "exam.delivery.detail_budget_exhausted", "exam.delivery.metadata_capacity", "exam.delivery.summary_rate_limited", "idempotency.conflict", "idempotency.in_progress")
}

func nativeDeliveryDeclarationErrorCodes() []string {
	return append(nativeDeliveryErrorCodes(), "idempotency.key_required", "idempotency.invalid_key")
}

func nativeDeliveryResource(app ExamAttemptApplication) resource {
	m := examAttemptHTTPModule{application: app}
	path := func(tail ...pathPart) routePath {
		parts := []pathPart{literal("exam-attempts"), canonicalID("exam_attempt_id"), literal("security-streams"), canonicalID("stream_id")}
		return apiPath(append(parts, tail...)...)
	}
	return newResource("native-delivery",
		boundedNativeDeliveryRoute(262144, idempotentSessionRoute(IdempotencyRequired, http.MethodPost, apiPath(literal("exam-attempts"), canonicalID("exam_attempt_id"), literal("security-batches")), nativeDeliveryAppendErrorCodes(), m.appendNativeDelivery)),
		sessionRoute(http.MethodGet, path(), nativeDeliveryErrorCodes(), m.nativeDeliveryStatus),
		sessionRoute(http.MethodGet, path(literal("receipts"), positiveSequence("batch_sequence")), nativeDeliveryErrorCodes(), m.nativeDeliveryReceipt),
		boundedNativeDeliveryRoute(8192, idempotentSessionRoute(IdempotencyRequired, http.MethodPost, path(literal("gaps")), nativeDeliveryDeclarationErrorCodes(), m.declareNativeDeliveryGaps)),
		boundedNativeDeliveryRoute(2048, idempotentSessionRoute(IdempotencyRequired, http.MethodPost, path(literal("seal")), nativeDeliveryDeclarationErrorCodes(), m.sealNativeDelivery)),
		boundedNativeDeliveryRoute(2048, idempotentSessionRoute(IdempotencyRequired, http.MethodPost, path(literal("summary")), nativeDeliveryDeclarationErrorCodes(), m.updateNativeDeliverySummary)))
}

func nativeDeliveryQuery(request operationRequest) (application.NativeDeliveryQuery, error) {
	raw, err := request.params.RequireExamAttemptId()
	if err != nil {
		return application.NativeDeliveryQuery{}, err
	}
	attemptID, err := model.ParseExamAttemptID(raw)
	if err != nil {
		return application.NativeDeliveryQuery{}, invalidRequestError("exam_attempt_id", err)
	}
	streamID, err := requirePathId("stream_id", request.params.NativeStreamID)
	if err != nil {
		return application.NativeDeliveryQuery{}, err
	}
	query := application.NativeDeliveryQuery{Access: application.CandidateExamAttemptAccess{AttemptID: attemptID}, StreamID: streamID}
	// Omission does not select historical authority. The Store decides from the
	// recorded source closure, and rejects omission for a live source.
	if len(request.request.Header.Values(candidateAttemptCredentialHeader)) > 0 || len(request.request.Header.Values(candidateAttemptConnectionHeader)) > 0 {
		headers, err := candidateAttemptAccessHeaders(request.request)
		if err != nil {
			return query, invalidRequestError("candidate_attempt_headers", err)
		}
		query.Access.ConnectionID = headers.ConnectionID
		query.Access.ContinuityCredential = headers.ContinuityCredential
	}
	return query, nil
}
func (m examAttemptHTTPModule) nativeDeliveryStatus(request operationRequest) (operationResult, error) {
	query, err := nativeDeliveryQuery(request)
	if err != nil {
		return operationResult{}, err
	}
	value, err := m.application.NativeDeliveryStatus(request.context, request.invocation(), query)
	if err != nil {
		return operationResult{}, err
	}
	return nativeDeliveryStatusResult(value)
}
func (m examAttemptHTTPModule) nativeDeliveryReceipt(request operationRequest) (operationResult, error) {
	query, err := nativeDeliveryQuery(request)
	if err != nil {
		return operationResult{}, err
	}
	sequence, err := strconv.ParseInt(request.params.NativeBatchSequence, 10, 64)
	if err != nil || sequence < 1 || sequence > model.NativeParticipationPositionLimit {
		return operationResult{}, invalidRequestError("batch_sequence", err)
	}
	value, err := m.application.NativeDeliveryReceipt(request.context, request.invocation(), query, sequence)
	if err != nil {
		return operationResult{}, err
	}
	if value == nil || value.Validate() != nil {
		return operationResult{}, application.NewError("exam.attempt.unavailable")
	}
	return jsonResult(http.StatusOK, value).withHeaders(noStoreHeaders()), nil
}
func (m examAttemptHTTPModule) declareNativeDeliveryGaps(request operationRequest) (operationResult, error) {
	query, err := nativeDeliveryQuery(request)
	if err != nil {
		return operationResult{}, err
	}
	var body model.DeclareDeliveryGaps
	if err := request.decodeJSON(&body, "nativeDeliveryGaps"); err != nil {
		return operationResult{}, err
	}
	value, err := m.application.DeclareNativeDeliveryGaps(request.context, request.invocation(), application.NativeDeliveryGapsCommand{Query: query, Declaration: body, IdempotencyKey: request.idempotencyKey})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, value).withHeaders(noStoreHeaders()), nil
}
func (m examAttemptHTTPModule) sealNativeDelivery(request operationRequest) (operationResult, error) {
	query, err := nativeDeliveryQuery(request)
	if err != nil {
		return operationResult{}, err
	}
	var body model.FinalDeliveryDeclaration
	if err := request.decodeJSON(&body, "nativeDeliveryFinal"); err != nil {
		return operationResult{}, err
	}
	value, err := m.application.SealNativeDelivery(request.context, request.invocation(), application.NativeDeliveryFinalCommand{Query: query, Declaration: body, IdempotencyKey: request.idempotencyKey})
	if err != nil {
		return operationResult{}, err
	}
	return nativeDeliveryStatusResult(value)
}
func (m examAttemptHTTPModule) updateNativeDeliverySummary(request operationRequest) (operationResult, error) {
	query, err := nativeDeliveryQuery(request)
	if err != nil {
		return operationResult{}, err
	}
	var body model.UnretainedDeliverySummary
	if err := request.decodeJSON(&body, "nativeDeliverySummary"); err != nil {
		return operationResult{}, err
	}
	value, err := m.application.UpdateNativeDeliverySummary(request.context, request.invocation(), application.NativeDeliverySummaryCommand{Query: query, Summary: body, IdempotencyKey: request.idempotencyKey})
	if err != nil {
		return operationResult{}, err
	}
	return nativeDeliveryStatusResult(value)
}
func nativeDeliveryStatusResult(value *model.NativeSecurityStreamStatus) (operationResult, error) {
	if value == nil || value.Validate() != nil {
		return operationResult{}, application.NewError("exam.attempt.unavailable")
	}
	return jsonResult(http.StatusOK, value).withHeaders(noStoreHeaders()), nil
}

func nativeDeliveryAppendErrorCodes() []string {
	return append(nativeDeliveryDeclarationErrorCodes(), "exam.delivery.batch_conflict", "exam.delivery.replay_window_exceeded", "exam.delivery.pending_capacity", "exam.delivery.append_rate_limited")
}
func (m examAttemptHTTPModule) appendNativeDelivery(request operationRequest) (operationResult, error) {
	var body model.NativeSecurityBatch
	if err := request.decodeJSON(&body, "nativeSecurityBatch"); err != nil {
		return operationResult{}, err
	}
	// The stream selector is in the closed batch envelope on this operation.
	request.params.NativeStreamID = body.StreamID
	query, err := nativeDeliveryQuery(request)
	if err != nil {
		return operationResult{}, err
	}
	value, err := m.application.AppendNativeDelivery(request.context, request.invocation(), application.NativeDeliveryAppendCommand{Query: query, Batch: body, IdempotencyKey: request.idempotencyKey})
	if err != nil {
		return operationResult{}, err
	}
	if value == nil || value.Receipt.Validate() != nil {
		return operationResult{}, application.NewError("exam.attempt.unavailable")
	}
	return jsonResult(http.StatusOK, value).withHeaders(noStoreHeaders()), nil
}

func boundedNativeDeliveryRoute(limit int64, definition routeDefinition) routeDefinition {
	definition.maxBodyBytes = limit
	return definition
}
