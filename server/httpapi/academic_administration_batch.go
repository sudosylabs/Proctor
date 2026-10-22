// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"context"
	"net/http"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
)

type AcademicAdministrationBatchApplication interface {
	RunAcademicAdministrationBatch(context.Context, invocation.Invocation, app.RunAcademicAdministrationBatchCommand) (app.AcademicAdministrationBatchResult, error)
}

type unavailableAcademicAdministrationBatchApplication struct{}

func (unavailableAcademicAdministrationBatchApplication) RunAcademicAdministrationBatch(context.Context, invocation.Invocation, app.RunAcademicAdministrationBatchCommand) (app.AcademicAdministrationBatchResult, error) {
	return app.AcademicAdministrationBatchResult{}, errUnpresentableResult
}

type academicAdministrationBatchItemRequest struct {
	Key             string `json:"key"`
	UserID          string `json:"user_id,omitempty"`
	RelationshipID  string `json:"relationship_id,omitempty"`
	RoleID          string `json:"role_id,omitempty"`
	AffiliationKind string `json:"affiliation_kind,omitempty"`
	StartAt         string `json:"start_at,omitempty"`
	EndAt           string `json:"end_at,omitempty"`
}

type academicAdministrationBatchRequest struct {
	Operation string                                   `json:"operation"`
	ScopeType string                                   `json:"scope_type"`
	ScopeID   string                                   `json:"scope_id"`
	Items     []academicAdministrationBatchItemRequest `json:"items"`
}

type academicAdministrationBatchItemResponse struct {
	Index      int    `json:"index"`
	Status     string `json:"status"`
	ResourceID string `json:"resource_id,omitempty"`
	ErrorCode  string `json:"error_code,omitempty"`
}

type academicAdministrationBatchResponse struct {
	Operation string                                    `json:"operation"`
	Items     []academicAdministrationBatchItemResponse `json:"items"`
	Succeeded int                                       `json:"succeeded"`
	NoOp      int                                       `json:"no_op"`
	Failed    int                                       `json:"failed"`
}

type academicAdministrationBatchResourceModule struct {
	batches AcademicAdministrationBatchApplication
}

func academicAdministrationBatchResource(batches AcademicAdministrationBatchApplication) apiResource {
	module := academicAdministrationBatchResourceModule{batches: batches}
	return newResource("academic-administration-batches",
		idempotentPrincipalRoute(IdempotencyRequired, http.MethodPost, apiPath(literal("academic-administration-batches")),
			academicRelationshipMutationErrorCodes("authentication.strong_required", "authentication.reauthentication_required",
				"idempotency.key_required", "idempotency.key_invalid"), module.run),
	)
}

func (m academicAdministrationBatchResourceModule) run(request operationRequest) (operationResult, error) {
	var body academicAdministrationBatchRequest
	if err := request.decodeJSON(&body); err != nil {
		return operationResult{}, invalidRequestError(err)
	}
	items := make([]app.AcademicAdministrationBatchItemCommand, 0, len(body.Items))
	for _, item := range body.Items {
		startAt, err := wireTimeMillis("start_at", item.StartAt)
		if err != nil {
			return operationResult{}, err
		}
		endAt, err := wireTimeMillis("end_at", item.EndAt)
		if err != nil {
			return operationResult{}, err
		}
		items = append(items, app.AcademicAdministrationBatchItemCommand{IdempotencyKey: item.Key, UserID: model.UserID(item.UserID),
			RelationshipID: item.RelationshipID, RoleID: model.RoleID(item.RoleID), AffiliationKind: model.AffiliationKind(item.AffiliationKind),
			StartAt: startAt, EndAt: endAt})
	}
	result, err := m.batches.RunAcademicAdministrationBatch(request.context, request.invocation(), app.RunAcademicAdministrationBatchCommand{
		Operation: app.AcademicAdministrationBatchOperation(body.Operation), ScopeType: model.RoleScopeType(body.ScopeType),
		ScopeID: body.ScopeID, IdempotencyKey: request.idempotencyKey, Items: items,
	})
	if err != nil {
		return operationResult{}, err
	}
	response := academicAdministrationBatchResponse{Operation: string(result.Operation), Succeeded: result.Succeeded,
		NoOp: result.NoOp, Failed: result.Failed, Items: make([]academicAdministrationBatchItemResponse, 0, len(result.Items))}
	for _, item := range result.Items {
		response.Items = append(response.Items, academicAdministrationBatchItemResponse{Index: item.Index, Status: string(item.Status),
			ResourceID: item.ResourceID, ErrorCode: item.ErrorCode})
	}
	return jsonResult(http.StatusOK, response), nil
}
