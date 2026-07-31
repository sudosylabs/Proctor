// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// Adapted from Mattermost server/channels/api4/audit_logging.go. Proctor keeps
// visible API preflight, authoritative application authorization, and an
// opaque keyset cursor.

package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/sudosylabs/proctor/server/model"
)

const defaultAuditPageSize = 50

type auditCursor struct {
	CreateAt int64  `json:"create_at"`
	Id       string `json:"id"`
}

type auditListResponse struct {
	Events     []*model.AuditEvent `json:"events"`
	NextCursor string              `json:"next_cursor,omitempty"`
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
	authorizedContext, allowed, appErr := a.application.PrincipalHasPermissionToSystem(
		request.Context(),
		principal,
		model.ActionAuditView,
		RequestMetadata(request.Context()),
	)
	if !a.requirePermission(writer, request, allowed, appErr) {
		return
	}
	request = request.WithContext(authorizedContext)
	query, err := auditQueryFromRequest(request)
	if err != nil {
		WriteError(writer, request, invalidRequestError("listAuditEvents", err))
		return
	}
	events, appErr := a.application.ListAuditEvents(
		request.Context(), principal, RequestMetadata(request.Context()), query,
	)
	if appErr != nil {
		writeApplicationError(writer, request, a.logger, appErr)
		return
	}
	response := auditListResponse{Events: events}
	if len(events) == query.Limit {
		last := events[len(events)-1]
		response.NextCursor = encodeAuditCursor(auditCursor{
			CreateAt: last.CreateAt, Id: last.Id,
		})
	}
	writeJSON(writer, http.StatusOK, response)
}

func auditQueryFromRequest(request *http.Request) (model.AuditQuery, error) {
	query := model.AuditQuery{Limit: defaultAuditPageSize}
	values := request.URL.Query()
	if raw := values.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 200 {
			return query, errors.New("limit must be between 1 and 200")
		}
		query.Limit = limit
	}
	if raw := values.Get("cursor"); raw != "" {
		cursor, err := decodeAuditCursor(raw)
		if err != nil {
			return query, err
		}
		query.BeforeTime = cursor.CreateAt
		query.BeforeId = cursor.Id
	}
	query.ActorId = values.Get("actor_id")
	query.Action = values.Get("action")
	resourceType, resourceID := values.Get("resource_type"), values.Get("resource_id")
	if (resourceType == "") != (resourceID == "") {
		return query, errors.New("resource_type and resource_id must be provided together")
	}
	if resourceType != "" {
		resource := model.Resource{Type: model.ResourceType(resourceType), Id: resourceID}
		if !resource.IsValid() {
			return query, errors.New("resource is invalid")
		}
		query.Resource = &resource
	}
	return query, nil
}

func encodeAuditCursor(cursor auditCursor) string {
	encoded, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func decodeAuditCursor(raw string) (auditCursor, error) {
	var cursor auditCursor
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return cursor, errors.New("cursor is invalid")
	}
	if err := json.Unmarshal(decoded, &cursor); err != nil ||
		cursor.CreateAt <= 0 || !model.IsValidId(cursor.Id) {
		return cursor, errors.New("cursor is invalid")
	}
	return cursor, nil
}
