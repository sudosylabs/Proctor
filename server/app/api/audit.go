// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// Adapted from Mattermost server/channels/api4/audit_logging.go. Proctor keeps
// authoritative application authorization and an opaque keyset cursor.

package api

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

const defaultAuditPageSize = 50

type auditCursor struct {
	CreateAt int64  `json:"create_at"`
	Id       string `json:"id"`
}

type auditEventResponse struct {
	ID         string              `json:"id"`
	CreateAt   int64               `json:"create_at"`
	UpdateAt   int64               `json:"update_at"`
	ActorID    string              `json:"actor_id,omitempty"`
	SessionID  string              `json:"session_id,omitempty"`
	Action     string              `json:"action"`
	Resource   model.Resource      `json:"resource"`
	ScopeType  model.RoleScopeType `json:"scope_type,omitempty"`
	ScopeID    string              `json:"scope_id,omitempty"`
	Status     model.AuditStatus   `json:"status"`
	RequestID  string              `json:"request_id,omitempty"`
	NodeID     string              `json:"node_id,omitempty"`
	ClientType string              `json:"client_type,omitempty"`
	AuthMethod string              `json:"authentication_method,omitempty"`
	IPAddress  string              `json:"ip_address,omitempty"`
	UserAgent  string              `json:"user_agent,omitempty"`
	ErrorCode  string              `json:"error_code,omitempty"`
	// Parameters, prior_state, and result remain application/operator-only and
	// are intentionally omitted from the public audit list projection.
}

type auditListResponse struct {
	Events     []auditEventResponse `json:"events"`
	NextCursor string               `json:"next_cursor,omitempty"`
}

func auditEventResponseFromModel(event *model.AuditEvent) auditEventResponse {
	return auditEventResponse{
		ID: event.Id, CreateAt: event.CreateAt, UpdateAt: event.UpdateAt,
		ActorID: event.ActorId, SessionID: event.SessionId, Action: event.Action,
		Resource: event.Resource, ScopeType: event.ScopeType, ScopeID: event.ScopeId,
		Status: event.Status, RequestID: event.RequestId, NodeID: event.NodeId,
		ClientType: event.ClientType, AuthMethod: event.AuthMethod,
		IPAddress: event.IPAddress, UserAgent: event.UserAgent, ErrorCode: event.ErrorCode,
	}
}

func (a *API) InitAudits() error {
	return a.Register(
		a.BaseRoutes.Audits,
		"",
		http.MethodGet,
		a.APIPrincipalRequired(http.HandlerFunc(a.listAuditEvents)),
	)
}

func (a *API) listAuditEvents(writer http.ResponseWriter, request *http.Request) {
	principal, ok := Principal(request.Context())
	if !ok {
		WriteError(writer, request, authenticationRequiredError())
		return
	}
	query, err := auditQueryFromRequest(request)
	if err != nil {
		writeApplicationError(writer, request, a.logger, application.NewError("audit.query.invalid"))
		return
	}
	events, listErr := a.auditListings.ListAuditEvents(
		request.Context(),
		application.NewInvocation(principal, RequestMetadata(request.Context())),
		query,
	)
	if listErr != nil {
		writeApplicationError(writer, request, a.logger, listErr)
		return
	}
	response := auditListResponse{Events: make([]auditEventResponse, 0, len(events))}
	for _, event := range events {
		response.Events = append(response.Events, auditEventResponseFromModel(event))
	}
	if len(events) == query.Limit {
		last := events[len(events)-1]
		response.NextCursor = encodeAuditCursor(auditCursor{CreateAt: last.CreateAt, Id: last.Id})
	}
	writeJSON(writer, http.StatusOK, response)
}

func auditQueryFromRequest(request *http.Request) (application.ListAuditEventsQuery, error) {
	query := application.ListAuditEventsQuery{Limit: defaultAuditPageSize}
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
		query.BeforeTime = cursor.CreateAt
		query.BeforeID = cursor.Id
	}
	query.ActorID = values.Get("actor_id")
	query.Action = values.Get("action")
	resourceType, resourceID := values.Get("resource_type"), values.Get("resource_id")
	if (resourceType == "") != (resourceID == "") {
		return query, errInvalidAuditResource
	}
	if resourceType != "" {
		resource := model.Resource{Type: model.ResourceType(resourceType), Id: resourceID}
		if !resource.IsValid() {
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

func encodeAuditCursor(cursor auditCursor) string {
	encoded, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func decodeAuditCursor(raw string) (auditCursor, error) {
	var cursor auditCursor
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return cursor, err
	}
	if err := json.Unmarshal(decoded, &cursor); err != nil ||
		cursor.CreateAt <= 0 || !model.IsValidId(cursor.Id) {
		return cursor, errInvalidAuditResource
	}
	return cursor, nil
}
