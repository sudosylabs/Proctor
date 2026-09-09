// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
package httpapi

import (
	"net/http"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

func deliveryBudgetResource(app ExamAttemptApplication) resource {
	m := examAttemptHTTPModule{application: app}
	return newResource("delivery-budget",
		sessionRoute(http.MethodGet, apiPath(literal("exam-attempts"), canonicalID("exam_attempt_id"), literal("delivery-limits")), nativeDeliveryErrorCodes(), m.deliveryBudget),
		boundedNativeDeliveryRoute(2048, idempotentSessionRoute(IdempotencyRequired, http.MethodPost, apiPath(literal("exam-attempts"), canonicalID("exam_attempt_id"), literal("delivery-limits"), literal("stop-details")), nativeDeliveryDeclarationErrorCodes(), m.stopDeliveryDetails)))
}
func (m examAttemptHTTPModule) deliveryBudget(request operationRequest) (operationResult, error) {
	query, err := browserSourceQuery(request, true)
	if err != nil {
		return operationResult{}, err
	}
	value, err := m.application.DeliveryBudget(request.context, request.invocation(), application.DeliveryBudgetQuery{Access: query.Access, ParticipationID: query.ParticipationID})
	if err != nil {
		return operationResult{}, err
	}
	if value == nil || value.Validate() != nil {
		return operationResult{}, application.NewError("exam.attempt.unavailable")
	}
	return jsonResult(http.StatusOK, value).withHeaders(noStoreHeaders()), nil
}
func (m examAttemptHTTPModule) stopDeliveryDetails(request operationRequest) (operationResult, error) {
	raw, err := request.params.RequireExamAttemptId()
	if err != nil {
		return operationResult{}, err
	}
	attempt, err := model.ParseExamAttemptID(raw)
	if err != nil {
		return operationResult{}, invalidRequestError("exam_attempt_id", err)
	}
	headers, err := candidateAttemptAccessHeaders(request.request)
	if err != nil {
		return operationResult{}, invalidRequestError("candidate_attempt_headers", err)
	}
	var body model.StopDeliveryDetails
	if err := request.decodeJSON(&body, "stopDeliveryDetails"); err != nil {
		return operationResult{}, err
	}
	if body.Validate() != nil {
		return operationResult{}, invalidRequestError("stop_delivery_details", nil)
	}
	value, err := m.application.StopDeliveryDetails(request.context, request.invocation(), application.StopDeliveryDetailsCommand{Access: application.CandidateExamAttemptAccess{AttemptID: attempt, ConnectionID: headers.ConnectionID, ContinuityCredential: headers.ContinuityCredential}, Request: body, IdempotencyKey: request.idempotencyKey})
	if err != nil {
		return operationResult{}, err
	}
	if value == nil || value.Validate(body.Family) != nil {
		return operationResult{}, application.NewError("exam.attempt.unavailable")
	}
	return jsonResult(http.StatusOK, value).withHeaders(noStoreHeaders()), nil
}
