// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/sudosylabs/proctor/server/app/exam/attempt"
	"github.com/sudosylabs/proctor/server/model"
)

func browserDeliveryResource(application ExamAttemptApplication) apiResource {
	m := examAttemptHTTPModule{application: application}
	base := []pathPart{literal("exam-attempts"), canonicalID("exam_attempt_id"), literal("browser-activity"), literal("sources")}
	path := func(tail string) routePath {
		parts := append(append([]pathPart{}, base...), canonicalUUID("source_session_id"))
		if tail != "" {
			parts = append(parts, literal(tail))
		}
		return apiPath(parts...)
	}
	return newResource("browser-delivery",
		sessionRoute(http.MethodGet, apiPath(base...), []string{"exam.delivery.control_rate_limited", "request.invalid", "resource.not_found", "exam.attempt.invalid", "exam.attempt.registered_desktop_required", "exam.attempt.connection_closed", "exam.attempt.connection_lost", "exam.attempt.sitting_not_open", "exam.attempt.unavailable", "exam.delivery.declaration_conflict", "exam.delivery.sequence_limit", "exam.delivery.detail_budget_exhausted", "exam.delivery.metadata_capacity", "exam.delivery.summary_rate_limited", "idempotency.conflict", "idempotency.in_progress"}, m.browserSourceList),
		sessionRoute(http.MethodGet, path(""), []string{"exam.delivery.control_rate_limited", "request.invalid", "resource.not_found", "exam.attempt.invalid", "exam.attempt.registered_desktop_required", "exam.attempt.connection_closed", "exam.attempt.connection_lost", "exam.attempt.sitting_not_open", "exam.attempt.unavailable", "exam.delivery.declaration_conflict", "exam.delivery.sequence_limit", "exam.delivery.detail_budget_exhausted", "exam.delivery.metadata_capacity", "exam.delivery.summary_rate_limited", "idempotency.conflict", "idempotency.in_progress"}, m.browserSourceStatus),
		sessionRoute(http.MethodGet, path("receipts"), []string{"exam.delivery.control_rate_limited", "request.invalid", "resource.not_found", "exam.attempt.invalid", "exam.attempt.registered_desktop_required", "exam.attempt.connection_closed", "exam.attempt.connection_lost", "exam.attempt.sitting_not_open", "exam.attempt.unavailable", "exam.delivery.upload_expired", "exam.delivery.declaration_conflict", "exam.delivery.sequence_limit", "exam.delivery.detail_budget_exhausted", "exam.delivery.metadata_capacity", "exam.delivery.summary_rate_limited", "idempotency.conflict", "idempotency.in_progress"}, m.browserDeliveryReceipts),
		boundedNativeDeliveryRoute(8192, idempotentSessionRoute(IdempotencyRequired, http.MethodPost, path("gaps"), []string{"audit.unavailable", "exam.delivery.control_rate_limited", "request.invalid", "resource.not_found", "exam.attempt.invalid", "exam.attempt.registered_desktop_required", "exam.attempt.connection_closed", "exam.attempt.connection_lost", "exam.attempt.sitting_not_open", "exam.attempt.unavailable", "exam.delivery.upload_expired", "exam.delivery.declaration_conflict", "exam.delivery.sequence_limit", "exam.delivery.detail_budget_exhausted", "exam.delivery.metadata_capacity", "exam.delivery.summary_rate_limited", "idempotency.conflict", "idempotency.in_progress", "idempotency.key_required", "idempotency.key_invalid"}, m.declareBrowserDeliveryGaps)),
		boundedNativeDeliveryRoute(2048, idempotentSessionRoute(IdempotencyRequired, http.MethodPost, path("seal"), []string{"audit.unavailable", "exam.delivery.control_rate_limited", "request.invalid", "resource.not_found", "exam.attempt.invalid", "exam.attempt.registered_desktop_required", "exam.attempt.connection_closed", "exam.attempt.connection_lost", "exam.attempt.sitting_not_open", "exam.attempt.unavailable", "exam.delivery.upload_expired", "exam.delivery.declaration_conflict", "exam.delivery.sequence_limit", "exam.delivery.detail_budget_exhausted", "exam.delivery.metadata_capacity", "exam.delivery.summary_rate_limited", "idempotency.conflict", "idempotency.in_progress", "idempotency.key_required", "idempotency.key_invalid"}, m.sealBrowserDelivery)),
		boundedNativeDeliveryRoute(2048, idempotentSessionRoute(IdempotencyRequired, http.MethodPost, path("summary"), []string{"audit.unavailable", "exam.delivery.control_rate_limited", "request.invalid", "resource.not_found", "exam.attempt.invalid", "exam.attempt.registered_desktop_required", "exam.attempt.connection_closed", "exam.attempt.connection_lost", "exam.attempt.sitting_not_open", "exam.attempt.unavailable", "exam.delivery.upload_expired", "exam.delivery.declaration_conflict", "exam.delivery.sequence_limit", "exam.delivery.detail_budget_exhausted", "exam.delivery.metadata_capacity", "exam.delivery.summary_rate_limited", "idempotency.conflict", "idempotency.in_progress", "idempotency.key_required", "idempotency.key_invalid"}, m.updateBrowserDeliverySummary)),
		boundedNativeDeliveryRoute(262144, idempotentSessionRoute(IdempotencyRequired, http.MethodPost, path("events"), []string{"audit.unavailable", "request.invalid", "resource.not_found", "exam.attempt.invalid", "exam.attempt.registered_desktop_required", "exam.attempt.connection_closed", "exam.attempt.connection_lost", "exam.attempt.sitting_not_open", "exam.attempt.unavailable", "exam.delivery.upload_expired", "exam.delivery.declaration_conflict", "exam.delivery.sequence_limit", "exam.delivery.detail_budget_exhausted", "exam.delivery.metadata_capacity", "exam.delivery.summary_rate_limited", "idempotency.conflict", "idempotency.in_progress", "idempotency.key_required", "idempotency.key_invalid", "exam.delivery.batch_conflict", "exam.delivery.replay_window_exceeded", "exam.delivery.pending_capacity", "exam.delivery.append_rate_limited", "exam.attempt.browser_activity_conflict"}, m.appendHistoricalBrowserDelivery)))
}

func browserSourceQuery(request operationRequest, list bool) (attempt.BrowserSourceQuery, error) {
	attemptID, err := request.params.RequireExamAttemptID()
	if err != nil {
		return attempt.BrowserSourceQuery{}, err
	}
	query := attempt.BrowserSourceQuery{Access: attempt.CandidateAccess{AttemptID: attemptID}, SourceSessionID: model.BrowserSourceSessionID(request.params.BrowserSourceSessionID)}
	if list {
		values := request.request.URL.Query()["participation_id"]
		if len(values) != 1 || !model.AttemptParticipationID(values[0]).IsValid() {
			return query, invalidRequestError(nil)
		}
		query.ParticipationID = model.AttemptParticipationID(values[0])
	} else if !query.SourceSessionID.IsValid() {
		return query, invalidRequestError(nil)
	}
	if len(request.request.Header.Values(candidateAttemptCredentialHeader)) > 0 || len(request.request.Header.Values(candidateAttemptConnectionHeader)) > 0 {
		headers, err := candidateAttemptAccessHeaders(request.request)
		if err != nil {
			return query, invalidRequestError(err)
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
		return operationResult{}, errUnpresentableResult
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
		return operationResult{}, errUnpresentableResult
	}
	for _, value := range values {
		if value.Validate() != nil {
			return operationResult{}, errUnpresentableResult
		}
	}
	// A Participation holds at most BrowserSourceMaximumPerParticipation
	// sources, so the complete set is paged by source identity.
	response, err := boundedListResult(request, "Browser source", values,
		func(value model.BrowserSourceStatus) boundedPageKey {
			return boundedPageKey{Name: string(value.SourceSessionID), ID: string(value.SourceSessionID)}
		}, func(value model.BrowserSourceStatus) model.BrowserSourceStatus { return value })
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, response).withHeaders(noStoreHeaders()), nil
}

func (m examAttemptHTTPModule) browserDeliveryReceipts(request operationRequest) (operationResult, error) {
	query, err := browserSourceQuery(request, false)
	if err != nil {
		return operationResult{}, err
	}
	page, err := request.pageRequest()
	if err != nil {
		return operationResult{}, err
	}
	if !request.request.URL.Query().Has("limit") {
		page.Limit = browserReceiptPageLimit
	}
	if page.Limit > browserReceiptPageLimit {
		return operationResult{}, invalidRequestError(errors.New("limit must be between 1 and 64"))
	}
	cursor, continued, err := decodePageCursor(page, browserReceiptCursorSpec())
	if err != nil {
		return operationResult{}, err
	}
	first := int64(1)
	if continued {
		first = cursor.Sequence
	}
	value, err := m.application.BrowserDeliveryReceipts(request.context, request.invocation(), query, first, page.Limit)
	if err != nil {
		return operationResult{}, err
	}
	if value == nil {
		return operationResult{}, errUnpresentableResult
	}
	response := listResponse[model.BrowserEventReceipt]{Items: value.Receipts}
	if value.NextSequence != nil {
		if response.NextCursor, err = encodeOpaqueCursor(browserReceiptCursor{Sequence: *value.NextSequence}, browserReceiptCursorSpec()); err != nil {
			return operationResult{}, unpresentableResult(err)
		}
	}
	return jsonResult(http.StatusOK, response).withHeaders(noStoreHeaders()), nil
}

// browserReceiptPageLimit is the largest and default receipt page; the
// receipts of one page fit the 16 KiB response bound.
const browserReceiptPageLimit = 64

// browserReceiptCursor continues the receipt listing at the first retained
// sequence of the next page.
type browserReceiptCursor struct {
	Sequence int64 `json:"sequence"`
}

func browserReceiptCursorSpec() opaqueCursorSpec[browserReceiptCursor] {
	return opaqueCursorSpec[browserReceiptCursor]{
		label: "Browser receipt", maximumEncodedLength: defaultOpaqueCursorMaximumEncodedLength,
		members: []string{"sequence"},
		validate: func(cursor browserReceiptCursor) error {
			if cursor.Sequence < 1 || cursor.Sequence > model.BrowserParticipationPositionLimit {
				return errors.New("invalid Browser receipt keyset")
			}
			return nil
		},
	}
}
func (m examAttemptHTTPModule) declareBrowserDeliveryGaps(request operationRequest) (operationResult, error) {
	query, err := browserSourceQuery(request, false)
	if err != nil {
		return operationResult{}, err
	}
	var body model.DeclareDeliveryGaps
	if err = request.decodeJSON(&body); err != nil {
		return operationResult{}, err
	}
	value, err := m.application.DeclareBrowserDeliveryGaps(request.context, request.invocation(), attempt.BrowserDeliveryGapsCommand{Query: query, Declaration: body, IdempotencyKey: request.idempotencyKey})
	if err != nil {
		return operationResult{}, err
	}
	if value == nil || value.Status.Validate() != nil {
		return operationResult{}, errUnpresentableResult
	}
	return jsonResult(http.StatusOK, browserDeliveryGapResponse{Receipt: value.Receipt, Status: value.Status}).withHeaders(noStoreHeaders()), nil
}

// browserDeliveryGapResponse acknowledges a Browser delivery gap declaration
// with its receipt and the resulting source status.
type browserDeliveryGapResponse struct {
	Receipt model.DeliveryGapReceipt  `json:"receipt"`
	Status  model.BrowserSourceStatus `json:"status"`
}

func (m examAttemptHTTPModule) sealBrowserDelivery(request operationRequest) (operationResult, error) {
	query, err := browserSourceQuery(request, false)
	if err != nil {
		return operationResult{}, err
	}
	var body model.FinalDeliveryDeclaration
	if err = request.decodeJSON(&body); err != nil {
		return operationResult{}, err
	}
	value, err := m.application.SealBrowserDelivery(request.context, request.invocation(), attempt.BrowserDeliveryFinalCommand{Query: query, Declaration: body, IdempotencyKey: request.idempotencyKey})
	if err != nil {
		return operationResult{}, err
	}
	if value == nil || value.Validate() != nil {
		return operationResult{}, errUnpresentableResult
	}
	return jsonResult(http.StatusOK, value).withHeaders(noStoreHeaders()), nil
}
func (m examAttemptHTTPModule) updateBrowserDeliverySummary(request operationRequest) (operationResult, error) {
	query, err := browserSourceQuery(request, false)
	if err != nil {
		return operationResult{}, err
	}
	var body model.UnretainedDeliverySummary
	if err = request.decodeJSON(&body); err != nil {
		return operationResult{}, err
	}
	value, err := m.application.UpdateBrowserDeliverySummary(request.context, request.invocation(), attempt.BrowserDeliverySummaryCommand{Query: query, Summary: body, IdempotencyKey: request.idempotencyKey})
	if err != nil {
		return operationResult{}, err
	}
	if value == nil || value.Status.Validate() != nil || value.Summary.Validate() != nil {
		return operationResult{}, errUnpresentableResult
	}
	return jsonResult(http.StatusOK, value).withHeaders(noStoreHeaders()), nil
}
func (m examAttemptHTTPModule) appendHistoricalBrowserDelivery(request operationRequest) (operationResult, error) {
	query, err := browserSourceQuery(request, false)
	if err != nil {
		return operationResult{}, err
	}
	if query.Access.ConnectionID != "" || query.Access.ContinuityCredential != "" {
		return operationResult{}, invalidRequestError(nil)
	}
	var body model.BrowserActivityBatch
	if err = request.decodeJSON(&body); err != nil {
		return operationResult{}, err
	}
	value, err := m.application.AppendHistoricalBrowserDelivery(request.context, request.invocation(), attempt.BrowserDeliveryAppendCommand{Query: query, Batch: body, IdempotencyKey: request.idempotencyKey})
	if err != nil {
		return operationResult{}, err
	}
	if value == nil {
		return operationResult{}, errUnpresentableResult
	}
	progress := model.BrowserDeliveryProgress{HighestContiguous: value.HighestContiguous, SettledThrough: value.SettledThrough, HighestSeen: value.HighestSeen, AllocatedThrough: value.AllocatedThrough, TerminalMissingThrough: value.TerminalMissingThrough, MissingRanges: []model.SequenceRange{}, MissingRangesTruncated: value.MissingRangesTruncated}
	for _, r := range value.MissingRanges {
		progress.MissingRanges = append(progress.MissingRanges, model.SequenceRange{First: r.First, Last: r.Last})
	}
	if progress.Validate() != nil {
		return operationResult{}, errUnpresentableResult
	}
	return jsonResult(http.StatusOK, browserDeliveryAcknowledgementResponse{BrowserDeliveryProgress: progress, SourceSessionID: value.SourceSessionID, Receipts: value.Receipts, ServerTime: value.ServerTime}).withHeaders(noStoreHeaders()), nil
}

type browserDeliveryAcknowledgementResponse struct {
	model.BrowserDeliveryProgress
	SourceSessionID model.BrowserSourceSessionID `json:"source_session_id"`
	Receipts        []model.BrowserEventReceipt  `json:"receipts"`
	ServerTime      time.Time                    `json:"server_time"`
}
