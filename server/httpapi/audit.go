// ---------------------------------------------------------------------------------------------
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Modifications Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
//
// Adapted from Mattermost server/channels/api4/audit_logging.go. Proctor keeps
// authoritative application authorization and an opaque keyset cursor.

package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/model"
)

const defaultAuditPageSize = 50

type auditCursor struct {
	CreatedAt string `json:"created_at"`
	ID        string `json:"id"`
}

type auditEventResponse struct {
	ID         string                `json:"id"`
	CreatedAt  string                `json:"created_at"`
	UpdatedAt  string                `json:"updated_at"`
	ActorID    string                `json:"actor_id,omitempty"`
	SessionID  string                `json:"session_id,omitempty"`
	Action     string                `json:"action"`
	Resource   auditResourceResponse `json:"resource"`
	ScopeType  model.RoleScopeType   `json:"scope_type,omitempty"`
	ScopeID    string                `json:"scope_id,omitempty"`
	Status     model.AuditStatus     `json:"status"`
	RequestID  string                `json:"request_id,omitempty"`
	NodeID     string                `json:"node_id,omitempty"`
	ClientType string                `json:"client_type,omitempty"`
	AuthMethod string                `json:"authentication_method,omitempty"`
	IPAddress  string                `json:"ip_address,omitempty"`
	UserAgent  string                `json:"user_agent,omitempty"`
	ErrorCode  string                `json:"error_code,omitempty"`
	// Parameters, prior_state, and result remain application/operator-only and
	// are intentionally omitted from the public audit list projection.
}

type auditResourceResponse struct {
	Type model.ResourceType `json:"type"`
	ID   string             `json:"id"`
}

func auditEventResponseFromModel(event *model.AuditEvent) auditEventResponse {
	return auditEventResponse{
		ID: event.ID.String(), CreatedAt: model.FormatRFC3339(event.CreatedAt),
		UpdatedAt: model.FormatRFC3339(event.UpdatedAt),
		ActorID:   event.ActorID.String(), SessionID: event.SessionID.String(), Action: event.Action,
		Resource:  auditResourceResponse{Type: event.Resource.Type, ID: event.Resource.ID},
		ScopeType: event.ScopeType, ScopeID: event.ScopeID,
		Status: event.Status, RequestID: event.RequestID, NodeID: event.NodeID,
		ClientType: event.ClientType, AuthMethod: event.AuthMethod,
		IPAddress: event.IPAddress, UserAgent: event.UserAgent, ErrorCode: event.ErrorCode,
	}
}

type auditResourceModule struct {
	audits AuditListingApplication
}

func auditResource(audits AuditListingApplication) apiResource {
	module := auditResourceModule{audits: audits}
	return newResource(
		"audits",
		principalRoute(http.MethodGet, apiPath(literal("audits")),
			[]string{"authorization.denied", "authorization.request.invalid", "authorization.unavailable", "audit.query.invalid", "audit.unavailable", "request.invalid", "resource.not_found"}, module.list),
	)
}

func (m auditResourceModule) list(request operationRequest) (operationResult, error) {
	query, err := auditQueryFromRequest(request.request)
	if err != nil {
		return operationResult{}, apperror.Invalid("request.invalid", "query")
	}
	page, listErr := m.audits.ListAuditEvents(
		request.context,
		request.invocation(),
		query,
	)
	if listErr != nil {
		return operationResult{}, listErr
	}
	response, err := newListResponse(page.Items, page.HasMore, auditEventResponseFromModel, func(last *model.AuditEvent) (string, error) {
		return encodeAuditCursor(auditCursor{CreatedAt: model.FormatRFC3339(last.CreatedAt), ID: last.ID.String()})
	})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, response), nil
}

func auditQueryFromRequest(request *http.Request) (app.ListAuditEventsQuery, error) {
	query := app.ListAuditEventsQuery{Limit: defaultAuditPageSize}
	values := request.URL.Query()
	if raw := values.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 200 {
			return query, err
		}
		query.Limit = limit
	}
	if raw := values.Get("cursor"); raw != "" {
		cursor, err := decodeAuditCursor(raw)
		if err != nil {
			return query, err
		}
		createdAt, _ := model.ParseRFC3339(cursor.CreatedAt)
		query.BeforeCreatedAt = createdAt
		query.BeforeID = model.AuditEventID(cursor.ID)
	}
	query.ActorID = model.UserID(values.Get("actor_id"))
	query.Action = values.Get("action")
	resourceType, resourceID := values.Get("resource_type"), values.Get("resource_id")
	if (resourceType == "") != (resourceID == "") {
		return query, errInvalidAuditResource
	}
	if resourceType != "" {
		resource := model.Resource{Type: model.ResourceType(resourceType), ID: resourceID}
		if resource.Validate() != nil {
			return query, errInvalidAuditResource
		}
		query.Resource = &resource
	}
	return query, nil
}

var errInvalidAuditResource = errAuditQuery("resource")

type auditQueryError string

func (e auditQueryError) Error() string { return string(e) }

func errAuditQuery(field string) error { return auditQueryError(field) }

func encodeAuditCursor(cursor auditCursor) (string, error) {
	return encodeOpaqueCursor(cursor, auditCursorSpec())
}

func decodeAuditCursor(raw string) (auditCursor, error) {
	return decodeOpaqueCursor(raw, auditCursorSpec())
}

func auditCursorSpec() opaqueCursorSpec[auditCursor] {
	return opaqueCursorSpec[auditCursor]{
		label: "audit", maximumEncodedLength: defaultOpaqueCursorMaximumEncodedLength,
		members: []string{"created_at", "id"},
		validate: func(cursor auditCursor) error {
			createdAt, err := model.ParseRFC3339(cursor.CreatedAt)
			if err != nil || createdAt.IsZero() || !model.AuditEventID(cursor.ID).IsValid() {
				return errors.New("invalid audit keyset")
			}
			return nil
		},
	}
}
