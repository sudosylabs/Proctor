// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"net/http"
	"strconv"

	"github.com/sudosylabs/proctor/server/model"
)

// InitWebSocket mounts the authenticated upgrade route. Protocol ownership
// lives in package websocket; this file only bridges session middleware.
func (a *API) InitWebSocket() error {
	return a.Register(
		a.BaseRoutes.WebSocket,
		"",
		http.MethodGet,
		a.APISessionRequired(http.HandlerFunc(a.connectWebSocket)),
	)
}

func (a *API) connectWebSocket(writer http.ResponseWriter, request *http.Request) {
	principal, ok := Principal(request.Context())
	if !ok {
		WriteError(writer, request, authenticationRequiredError())
		return
	}
	params, ok := RequestParams(request.Context())
	if !ok {
		WriteError(writer, request, invalidRequestError("route_params", nil))
		return
	}
	connectionID, sequence, err := parseWebSocketResume(params)
	if err != nil {
		WriteError(writer, request, err)
		return
	}
	allowMissingOrigin := credentialSourceFromContext(request.Context()) == credentialSourceBearer
	if acceptErr := a.webSocket.Accept(
		writer,
		request,
		principal,
		RequestMetadata(request.Context()),
		connectionID,
		sequence,
		allowMissingOrigin,
	); acceptErr != nil {
		WriteError(writer, request, acceptErr)
	}
}

func parseWebSocketResume(params Params) (string, int64, error) {
	if params.ConnectionId == "" && params.SequenceNumber == "" {
		return "", 0, nil
	}
	if !model.IsValidId(params.ConnectionId) || params.SequenceNumber == "" {
		return "", 0, invalidRequestError("connection_id", nil)
	}
	sequence, err := strconv.ParseInt(params.SequenceNumber, 10, 64)
	if err != nil || sequence < 0 {
		return "", 0, invalidRequestError("sequence_number", err)
	}
	return params.ConnectionId, sequence, nil
}
