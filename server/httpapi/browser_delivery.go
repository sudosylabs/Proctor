// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
	"net/http"
	"strconv"
	"time"
)

func browserDeliveryResource(app ExamAttemptApplication) resource {
	m := examAttemptHTTPModule{application: app}
	base := []pathPart{literal("exam-attempts"), canonicalID("exam_attempt_id"), literal("browser-activity"), literal("sources")}
	path := func(tail string) routePath {
		parts := append(append([]pathPart{}, base...), canonicalUUID("source_session_id"))
		if tail != "" {
			parts = append(parts, literal(tail))
		}
		return apiPath(parts...)
	}
	return newResource("browser-delivery",
		sessionRoute(http.MethodGet, apiPath(base...), nativeDeliveryErrorCodes(), m.browserSourceList),
		sessionRoute(http.MethodGet, path(""), nativeDeliveryErrorCodes(), m.browserSourceStatus),
		sessionRoute(http.MethodGet, path("receipts"), nativeDeliveryErrorCodes(), m.browserDeliveryReceipts),
		boundedNativeDeliveryRoute(8192, idempotentSessionRoute(IdempotencyRequired, http.MethodPost, path("gaps"), nativeDeliveryDeclarationErrorCodes(), m.declareBrowserDeliveryGaps)),
		boundedNativeDeliveryRoute(2048, idempotentSessionRoute(IdempotencyRequired, http.MethodPost, path("seal"), nativeDeliveryDeclarationErrorCodes(), m.sealBrowserDelivery)),
		boundedNativeDeliveryRoute(2048, idempotentSessionRoute(IdempotencyRequired, http.MethodPost, path("summary"), nativeDeliveryDeclarationErrorCodes(), m.updateBrowserDeliverySummary)),
		boundedNativeDeliveryRoute(262144, idempotentSessionRoute(IdempotencyRequired, http.MethodPost, path("events"), browserDeliveryAppendErrorCodes(), m.appendHistoricalBrowserDelivery)))
}

func browserDeliveryAppendErrorCodes() []string {
	return append(nativeDeliveryAppendErrorCodes(), "exam.attempt.browser_activity_conflict")
}
func browserSourceQuery(request operationRequest, list bool) (application.BrowserSourceQuery, error) {
	raw, err := request.params.RequireExamAttemptId()
	if err != nil {
		return application.BrowserSourceQuery{}, err
	}
	attemptID, err := model.ParseExamAttemptID(raw)
	if err != nil {
		return application.BrowserSourceQuery{}, invalidRequestError("exam_attempt_id", err)
	}
	query := application.BrowserSourceQuery{Access: application.CandidateExamAttemptAccess{AttemptID: attemptID}, SourceSessionID: model.BrowserSourceSessionID(request.params.BrowserSourceSessionID)}
	if list {
		values := request.request.URL.Query()["participation_id"]
		if len(values) != 1 || !model.AttemptParticipationID(values[0]).IsValid() {
			return query, invalidRequestError("participation_id", nil)
		}
		query.ParticipationID = model.AttemptParticipationID(values[0])
	} else if !query.SourceSessionID.IsValid() {
		return query, invalidRequestError("source_session_id", nil)
	}
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
func (m examAttemptHTTPModule) browserSourceStatus(request operationRequest) (operationResult, error) {
	query, err := browserSourceQuery(request, false)
	if err != nil {
		return operationResult{}, err
	}
	value, err := m.application.BrowserSourceStatus(request.context, request.invocation(), query)
	if err != nil {
		return operationResult{}, err
	}
	if value == nil || value.Validate() != nil {
		return operationResult{}, application.NewError("exam.attempt.unavailable")
	}
	return jsonResult(http.StatusOK, value).withHeaders(noStoreHeaders()), nil
}
func (m examAttemptHTTPModule) browserSourceList(request operationRequest) (operationResult, error) {
	query, err := browserSourceQuery(request, true)
	if err != nil {
		return operationResult{}, err
	}
	values, err := m.application.BrowserSourceList(request.context, request.invocation(), query)
	if err != nil {
		return operationResult{}, err
	}
	if values == nil || len(values) > model.BrowserSourceMaximumPerParticipation {
		return operationResult{}, application.NewError("exam.attempt.unavailable")
	}
	for _, value := range values {
		if value.Validate() != nil {
			return operationResult{}, application.NewError("exam.attempt.unavailable")
		}
	}
	return jsonResult(http.StatusOK, browserSourceListResponse{Sources: values}).withHeaders(noStoreHeaders()), nil
}

type browserSourceListResponse struct {
	Sources []model.BrowserSourceStatus `json:"sources"`
}

func (m examAttemptHTTPModule) browserDeliveryReceipts(request operationRequest) (operationResult, error) {
	query, err := browserSourceQuery(request, false)
	if err != nil {
		return operationResult{}, err
	}
	values := request.request.URL.Query()
	if len(values["first_sequence"]) != 1 || len(values["limit"]) != 1 {
		return operationResult{}, invalidRequestError("receipt_page", nil)
	}
	first, err := strconv.ParseInt(values.Get("first_sequence"), 10, 64)
	if err != nil || strconv.FormatInt(first, 10) != values.Get("first_sequence") {
		return operationResult{}, invalidRequestError("first_sequence", err)
	}
	limit, err := strconv.Atoi(values.Get("limit"))
	if err != nil || strconv.Itoa(limit) != values.Get("limit") {
		return operationResult{}, invalidRequestError("limit", err)
	}
	value, err := m.application.BrowserDeliveryReceipts(request.context, request.invocation(), query, first, limit)
	if err != nil {
		return operationResult{}, err
	}
	if value == nil {
		return operationResult{}, application.NewError("exam.attempt.unavailable")
	}
	return jsonResult(http.StatusOK, value).withHeaders(noStoreHeaders()), nil
}
func (m examAttemptHTTPModule) declareBrowserDeliveryGaps(request operationRequest) (operationResult, error) {
	query, err := browserSourceQuery(request, false)
	if err != nil {
		return operationResult{}, err
	}
	var body model.DeclareDeliveryGaps
	if err := request.decodeJSON(&body, "declareDeliveryGaps"); err != nil {
		return operationResult{}, err
	}
	value, err := m.application.DeclareBrowserDeliveryGaps(request.context, request.invocation(), application.BrowserDeliveryGapsCommand{Query: query, Declaration: body, IdempotencyKey: request.idempotencyKey})
	if err != nil {
		return operationResult{}, err
	}
	if value == nil || value.Status.Validate() != nil {
		return operationResult{}, application.NewError("exam.attempt.unavailable")
	}
	return jsonResult(http.StatusOK, value).withHeaders(noStoreHeaders()), nil
}
func (m examAttemptHTTPModule) sealBrowserDelivery(request operationRequest) (operationResult, error) {
	query, err := browserSourceQuery(request, false)
	if err != nil {
		return operationResult{}, err
	}
	var body model.FinalDeliveryDeclaration
	if err := request.decodeJSON(&body, "finalDeliveryDeclaration"); err != nil {
		return operationResult{}, err
	}
	value, err := m.application.SealBrowserDelivery(request.context, request.invocation(), application.BrowserDeliveryFinalCommand{Query: query, Declaration: body, IdempotencyKey: request.idempotencyKey})
	if err != nil {
		return operationResult{}, err
	}
	if value == nil || value.Validate() != nil {
		return operationResult{}, application.NewError("exam.attempt.unavailable")
	}
	return jsonResult(http.StatusOK, value).withHeaders(noStoreHeaders()), nil
}
func (m examAttemptHTTPModule) updateBrowserDeliverySummary(request operationRequest) (operationResult, error) {
	query, err := browserSourceQuery(request, false)
	if err != nil {
		return operationResult{}, err
	}
	var body model.UnretainedDeliverySummary
	if err := request.decodeJSON(&body, "unretainedDeliverySummary"); err != nil {
		return operationResult{}, err
	}
	value, err := m.application.UpdateBrowserDeliverySummary(request.context, request.invocation(), application.BrowserDeliverySummaryCommand{Query: query, Summary: body, IdempotencyKey: request.idempotencyKey})
	if err != nil {
		return operationResult{}, err
	}
	if value == nil || value.Status.Validate() != nil || value.Summary.Validate() != nil {
		return operationResult{}, application.NewError("exam.attempt.unavailable")
	}
	return jsonResult(http.StatusOK, value).withHeaders(noStoreHeaders()), nil
}
func (m examAttemptHTTPModule) appendHistoricalBrowserDelivery(request operationRequest) (operationResult, error) {
	query, err := browserSourceQuery(request, false)
	if err != nil {
		return operationResult{}, err
	}
	if query.Access.ConnectionID != "" || query.Access.ContinuityCredential != "" {
		return operationResult{}, invalidRequestError("historical_browser_headers", nil)
	}
	var body model.BrowserActivityBatch
	if err := request.decodeJSON(&body, "browserActivityBatch"); err != nil {
		return operationResult{}, err
	}
	value, err := m.application.AppendHistoricalBrowserDelivery(request.context, request.invocation(), application.BrowserDeliveryAppendCommand{Query: query, Batch: body, IdempotencyKey: request.idempotencyKey})
	if err != nil {
		return operationResult{}, err
	}
	if value == nil {
		return operationResult{}, application.NewError("exam.attempt.unavailable")
	}
	progress := model.BrowserDeliveryProgress{HighestContiguous: value.HighestContiguous, SettledThrough: value.SettledThrough, HighestSeen: value.HighestSeen, AllocatedThrough: value.AllocatedThrough, TerminalMissingThrough: value.TerminalMissingThrough, MissingRanges: []model.SequenceRange{}, MissingRangesTruncated: value.MissingRangesTruncated}
	for _, r := range value.MissingRanges {
		progress.MissingRanges = append(progress.MissingRanges, model.SequenceRange{First: r.First, Last: r.Last})
	}
	if progress.Validate() != nil {
		return operationResult{}, application.NewError("exam.attempt.unavailable")
	}
	return jsonResult(http.StatusOK, browserDeliveryAcknowledgementResponse{BrowserDeliveryProgress: progress, SourceSessionID: value.SourceSessionID, Receipts: value.Receipts, ServerTime: value.ServerTime}).withHeaders(noStoreHeaders()), nil
}

type browserDeliveryAcknowledgementResponse struct {
	model.BrowserDeliveryProgress
	SourceSessionID model.BrowserSourceSessionID `json:"source_session_id"`
	Receipts        []model.BrowserEventReceipt  `json:"receipts"`
	ServerTime      time.Time                    `json:"server_time"`
}
