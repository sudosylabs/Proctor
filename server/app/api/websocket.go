// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"net/http"
	"strconv"

	"github.com/sudosylabs/proctor/server/model"
)

type webSocketModule struct {
	transport WebSocketTransport
}

const (
	webSocketResourceName = "websocket"
	webSocketProtocolName = "websocket-upgrade"
	webSocketPathLiteral  = "websocket"
)

func webSocketResource(transport WebSocketTransport) resource {
	module := webSocketModule{transport: transport}
	return newResource(webSocketResourceName,
		upgradeRoute(
			webSocketProtocolName,
			AuthSessionRequired,
			http.MethodGet,
			apiPath(literal(webSocketPathLiteral)),
			[]string{
				"authentication.required",
				"authentication.invalid_token",
				"authentication.credential_ambiguous",
				"request.invalid",
				"websocket.origin.invalid",
				"websocket.unavailable",
			},
			module.connect,
		),
	)
}

func (module webSocketModule) connect(writer http.ResponseWriter, request operationRequest) error {
	connectionID, sequence, err := parseWebSocketResume(request.params)
	if err != nil {
		return err
	}
	allowMissingOrigin := credentialSourceFromContext(request.context) == credentialSourceBearer
	return module.transport.Accept(
		writer,
		request.request,
		request.principal,
		request.metadata,
		connectionID,
		sequence,
		allowMissingOrigin,
	)
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
