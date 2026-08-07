// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// This file contains a substantially modified adaptation of Mattermost's
// public WebSocket connection, hub, replay-queue, and router flow. See
// server/NOTICE for exact provenance.

package api

import (
	application "github.com/sudosylabs/proctor/server/app"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/maphash"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/sudosylabs/proctor/server/mlog"
	"github.com/sudosylabs/proctor/server/model"
)

const (
	webSocketSendQueueSize        = 256
	webSocketReplayQueueSize      = 128
	webSocketWriteWait            = 30 * time.Second
	webSocketPongWait             = 100 * time.Second
	webSocketPingInterval         = 60 * time.Second
	webSocketSessionCheck         = 30 * time.Second
	webSocketReplayRetention      = 2 * time.Minute
	webSocketCloseServer          = 4000
	webSocketCloseBackpressure    = 4002
	webSocketCloseLimit           = 4004
	webSocketMaximumPerSession    = 8
	webSocketMaximumPerUser       = 64
	webSocketMaximumSubscriptions = 256
)

type webSocketApplication interface {
	AuthorizeWebSocketSubscription(
		context.Context,
		model.Principal,
		model.RequestMetadata,
		model.WebSocketSubscription,
	) error
	ValidateWebSocketPrincipal(context.Context, model.Principal) error
}

type outboundWebSocketMessage struct {
	event    *model.WebSocketEvent
	response *model.WebSocketResponse
}

type replayWebSocketState struct {
	userID        string
	sessionID     string
	nextSequence  int64
	history       []*model.WebSocketEvent
	subscriptions map[string]model.WebSocketSubscription
	expiresAt     time.Time
}

// WebSocketHub owns only connections attached to this process. Application
// events arrive through PublishLocal after the application has performed the
// local-first/cluster-peer publication flow.
type WebSocketHub struct {
	application webSocketApplication
	logger      *mlog.Logger
	publicURL   *url.URL
	nodeID      string
	hashSeed    maphash.Seed

	mu     sync.RWMutex
	closed bool
	shards []*webSocketShard
	stop   chan struct{}
	done   chan struct{}
}

type webSocketShard struct {
	mu     sync.RWMutex
	conns  map[string]*webSocketConnection
	replay map[string]*replayWebSocketState
}

type webSocketConnection struct {
	hub       *WebSocketHub
	socket    *websocket.Conn
	principal model.Principal
	metadata  model.RequestMetadata
	id        string

	mu            sync.Mutex
	nextSequence  int64
	history       []*model.WebSocketEvent
	subscriptions map[string]model.WebSocketSubscription
	replayable    bool
	send          chan outboundWebSocketMessage
	closeOnce     sync.Once
}

func NewWebSocketHub(
	application webSocketApplication,
	logger *mlog.Logger,
	publicURL string,
	nodeID string,
) (*WebSocketHub, error) {
	if application == nil {
		return nil, errors.New("WebSocket application is required")
	}
	if logger == nil {
		return nil, errors.New("WebSocket logger is required")
	}
	parsed, err := url.Parse(publicURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, errors.New("WebSocket public URL is invalid")
	}
	shardCount := max(runtime.NumCPU(), 1)
	hub := &WebSocketHub{
		application: application,
		logger:      logger.With(mlog.String("component", "websocket")),
		publicURL:   parsed,
		nodeID:      nodeID,
		hashSeed:    maphash.MakeSeed(),
		shards:      make([]*webSocketShard, shardCount),
		stop:        make(chan struct{}),
		done:        make(chan struct{}),
	}
	for index := range hub.shards {
		hub.shards[index] = &webSocketShard{
			conns:  make(map[string]*webSocketConnection),
			replay: make(map[string]*replayWebSocketState),
		}
	}
	go hub.reapReplayStates()
	return hub, nil
}

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
	connectionID, sequence, appErr := parseWebSocketResume(params)
	if appErr != nil {
		WriteError(writer, request, appErr)
		return
	}
	if !a.webSocketHub.originAllowed(
		request.Header.Get("Origin"),
		credentialSourceFromContext(request.Context()),
	) {
		WriteError(
			writer,
			request,
			application.NewError("websocket.origin.invalid"),
		)
		return
	}
	upgrader := websocket.Upgrader{
		ReadBufferSize:  model.MaxWebSocketMessageBytes,
		WriteBufferSize: model.MaxWebSocketMessageBytes,
		CheckOrigin: func(*http.Request) bool {
			// The credential-aware origin decision was made immediately above.
			return true
		},
	}
	socket, err := upgrader.Upgrade(writer, request, nil)
	if err != nil {
		a.logger.WarnContext(request.Context(), "WebSocket upgrade failed", mlog.Err(err))
		return
	}
	connection, resumed := a.webSocketHub.register(
		socket,
		principal,
		RequestMetadata(request.Context()),
		connectionID,
		sequence,
	)
	if connection == nil {
		_ = socket.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(
				webSocketCloseLimit,
				"connection limit reached",
			),
			time.Now().Add(webSocketWriteWait),
		)
		_ = socket.Close()
		return
	}
	connection.enqueueHello(resumed, connectionID != "" && !resumed)
	connection.pump(request.Context())
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

func (h *WebSocketHub) originAllowed(origin string, source credentialSource) bool {
	if origin == "" {
		return source == credentialSourceBearer
	}
	parsed, err := url.Parse(origin)
	return err == nil &&
		parsed.Scheme == h.publicURL.Scheme &&
		parsed.Host == h.publicURL.Host
}

func (h *WebSocketHub) register(
	socket *websocket.Conn,
	principal model.Principal,
	metadata model.RequestMetadata,
	requestedID string,
	requestedSequence int64,
) (*webSocketConnection, bool) {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil, false
	}
	h.mu.Unlock()

	shard := h.shardForUser(principal.UserId)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	h.mu.RLock()
	closed := h.closed
	h.mu.RUnlock()
	if closed {
		return nil, false
	}
	h.pruneReplayLocked(shard, time.Now())
	var userConnections, sessionConnections int
	for _, connection := range shard.conns {
		if connection.principal.UserId == principal.UserId {
			userConnections++
		}
		if connection.principal.SessionId == principal.SessionId {
			sessionConnections++
		}
	}
	if userConnections >= webSocketMaximumPerUser ||
		sessionConnections >= webSocketMaximumPerSession {
		return nil, false
	}
	connectionID := model.NewId()
	nextSequence := int64(0)
	var (
		history       []*model.WebSocketEvent
		subscriptions = make(map[string]model.WebSocketSubscription)
		replayEvents  []*model.WebSocketEvent
		resumed       bool
	)
	if state := shard.replay[requestedID]; state != nil &&
		state.userID == principal.UserId &&
		state.sessionID == principal.SessionId &&
		requestedSequence <= state.nextSequence {
		oldest := state.nextSequence
		if len(state.history) != 0 {
			oldest = state.history[0].Sequence - 1
		}
		if requestedSequence >= oldest {
			connectionID = requestedID
			nextSequence = state.nextSequence
			history = cloneWebSocketEvents(state.history)
			subscriptions = cloneSubscriptions(state.subscriptions)
			for _, event := range state.history {
				if event.Sequence > requestedSequence {
					replayEvents = append(replayEvents, event.Clone())
				}
			}
			resumed = true
			delete(shard.replay, requestedID)
		}
	}
	connection := &webSocketConnection{
		hub: h, socket: socket, principal: principal, metadata: metadata,
		id: connectionID, nextSequence: nextSequence, history: history,
		subscriptions: subscriptions, replayable: true,
		send: make(chan outboundWebSocketMessage, webSocketSendQueueSize),
	}
	shard.conns[connection.id] = connection
	for _, event := range replayEvents {
		connection.send <- outboundWebSocketMessage{event: event}
	}
	return connection, resumed
}

func (h *WebSocketHub) unregister(connection *webSocketConnection) {
	shard := h.shardForUser(connection.principal.UserId)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	current, exists := shard.conns[connection.id]
	if !exists || current != connection {
		return
	}
	delete(shard.conns, connection.id)
	connection.mu.Lock()
	h.mu.RLock()
	closed := h.closed
	h.mu.RUnlock()
	if connection.replayable && !closed {
		shard.replay[connection.id] = &replayWebSocketState{
			userID:        connection.principal.UserId,
			sessionID:     connection.principal.SessionId,
			nextSequence:  connection.nextSequence,
			history:       cloneWebSocketEvents(connection.history),
			subscriptions: cloneSubscriptions(connection.subscriptions),
			expiresAt:     time.Now().Add(webSocketReplayRetention),
		}
	}
	connection.mu.Unlock()
}

func (h *WebSocketHub) PublishLocal(
	_ context.Context,
	event *model.WebSocketEvent,
) {
	if event == nil || event.ValidateForPublish() != nil {
		return
	}
	var shards []*webSocketShard
	if event.UserId != "" {
		shards = []*webSocketShard{h.shardForUser(event.UserId)}
	} else {
		shards = h.shards
	}
	var connections []*webSocketConnection
	for _, shard := range shards {
		shard.mu.RLock()
		for _, connection := range shard.conns {
			connections = append(connections, connection)
		}
		shard.mu.RUnlock()
	}
	for _, connection := range connections {
		if event.UserId != "" && connection.principal.UserId != event.UserId {
			continue
		}
		if event.Action != "" && !connection.hasSubscription(
			model.WebSocketSubscription{Action: event.Action, Resource: event.Resource},
		) {
			continue
		}
		connection.enqueueEvent(event)
	}
}

func (h *WebSocketHub) CloseSession(sessionID string, code int, reason string) {
	h.closeMatching(code, reason, func(connection *webSocketConnection) bool {
		return connection.principal.SessionId == sessionID
	})
}

func (h *WebSocketHub) CloseUser(userID string, code int, reason string) {
	h.closeMatching(code, reason, func(connection *webSocketConnection) bool {
		return connection.principal.UserId == userID
	})
}

func (h *WebSocketHub) CloseAll(code int, reason string) {
	h.closeMatching(code, reason, func(*webSocketConnection) bool { return true })
}

func (h *WebSocketHub) closeMatching(
	code int,
	reason string,
	matches func(*webSocketConnection) bool,
) {
	var connections []*webSocketConnection
	for _, shard := range h.shards {
		shard.mu.RLock()
		for _, connection := range shard.conns {
			if matches(connection) {
				connections = append(connections, connection)
			}
		}
		shard.mu.RUnlock()
	}
	for _, connection := range connections {
		connection.close(code, reason, false)
	}
}

func (h *WebSocketHub) Close() error {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		<-h.done
		return nil
	}
	h.closed = true
	close(h.stop)
	var connections []*webSocketConnection
	h.mu.Unlock()
	for _, shard := range h.shards {
		shard.mu.Lock()
		for _, connection := range shard.conns {
			connections = append(connections, connection)
		}
		shard.replay = make(map[string]*replayWebSocketState)
		shard.mu.Unlock()
	}
	for _, connection := range connections {
		connection.close(webSocketCloseServer, "server shutting down", false)
	}
	<-h.done
	return nil
}

func (h *WebSocketHub) reapReplayStates() {
	ticker := time.NewTicker(time.Minute)
	defer func() {
		ticker.Stop()
		close(h.done)
	}()
	for {
		select {
		case <-h.stop:
			return
		case now := <-ticker.C:
			for _, shard := range h.shards {
				shard.mu.Lock()
				h.pruneReplayLocked(shard, now)
				shard.mu.Unlock()
			}
		}
	}
}

func (h *WebSocketHub) pruneReplayLocked(
	shard *webSocketShard,
	now time.Time,
) {
	for id, state := range shard.replay {
		if !state.expiresAt.After(now) {
			delete(shard.replay, id)
		}
	}
}

func (h *WebSocketHub) shardForUser(userID string) *webSocketShard {
	var hash maphash.Hash
	hash.SetSeed(h.hashSeed)
	_, _ = hash.WriteString(userID)
	return h.shards[int(hash.Sum64()%uint64(len(h.shards)))]
}

func (c *webSocketConnection) pump(ctx context.Context) {
	pumpCtx, cancel := context.WithCancel(ctx)
	var pumps sync.WaitGroup
	pumps.Add(2)
	go func() {
		defer pumps.Done()
		c.writePump(pumpCtx)
	}()
	go func() {
		defer pumps.Done()
		c.sessionPump(pumpCtx)
	}()
	c.readPump(pumpCtx)
	cancel()
	_ = c.socket.Close()
	pumps.Wait()
	c.hub.unregister(c)
}

func (c *webSocketConnection) readPump(ctx context.Context) {
	c.socket.SetReadLimit(model.MaxWebSocketMessageBytes)
	_ = c.socket.SetReadDeadline(time.Now().Add(webSocketPongWait))
	c.socket.SetPongHandler(func(string) error {
		return c.socket.SetReadDeadline(time.Now().Add(webSocketPongWait))
	})
	for {
		var request model.WebSocketRequest
		if err := c.socket.ReadJSON(&request); err != nil {
			return
		}
		if err := request.Validate(); err != nil {
			c.enqueueError(request.Sequence, "websocket.request.invalid", "Invalid WebSocket request.")
			continue
		}
		c.handleRequest(ctx, &request)
	}
}

func (c *webSocketConnection) writePump(ctx context.Context) {
	ticker := time.NewTicker(webSocketPingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case message := <-c.send:
			_ = c.socket.SetWriteDeadline(time.Now().Add(webSocketWriteWait))
			var err error
			if message.event != nil {
				err = c.socket.WriteJSON(message.event)
			} else {
				err = c.socket.WriteJSON(message.response)
			}
			if err != nil {
				_ = c.socket.Close()
				return
			}
		case <-ticker.C:
			_ = c.socket.SetWriteDeadline(time.Now().Add(webSocketWriteWait))
			if err := c.socket.WriteMessage(websocket.PingMessage, nil); err != nil {
				_ = c.socket.Close()
				return
			}
		}
	}
}

func (c *webSocketConnection) sessionPump(ctx context.Context) {
	ticker := time.NewTicker(webSocketSessionCheck)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if appErr := c.hub.application.ValidateWebSocketPrincipal(
				ctx,
				c.principal,
			); appErr != nil {
				c.close(4001, "session no longer valid", false)
				return
			}
		}
	}
}

func (c *webSocketConnection) handleRequest(
	ctx context.Context,
	request *model.WebSocketRequest,
) {
	switch request.Action {
	case "ping":
		c.enqueueResponse(request.Sequence, json.RawMessage(`{"pong":true}`))
	case "subscribe":
		var subscription model.WebSocketSubscription
		if err := json.Unmarshal(request.Data, &subscription); err != nil ||
			!subscription.IsValid() {
			c.enqueueError(request.Sequence, "websocket.subscription.invalid", "Invalid subscription.")
			return
		}
		metadata := c.metadata
		metadata.RequestId = fmt.Sprintf("%s:%d", c.id, request.Sequence)
		if err := c.hub.application.AuthorizeWebSocketSubscription(
			ctx,
			c.principal,
			metadata,
			subscription,
		); err != nil {
			code := "authorization.denied"
			if failure, ok := application.As(err); ok {
				code = failure.Code()
			}
			c.enqueueError(request.Sequence, code, "WebSocket subscription denied.")
			return
		}
		c.mu.Lock()
		if _, exists := c.subscriptions[subscription.Key()]; !exists &&
			len(c.subscriptions) >= webSocketMaximumSubscriptions {
			c.mu.Unlock()
			c.enqueueError(
				request.Sequence,
				"websocket.subscription.limit",
				"WebSocket subscription limit reached.",
			)
			return
		}
		c.subscriptions[subscription.Key()] = subscription
		c.mu.Unlock()
		c.enqueueResponse(request.Sequence, nil)
	case "unsubscribe":
		var subscription model.WebSocketSubscription
		if err := json.Unmarshal(request.Data, &subscription); err != nil ||
			!subscription.IsValid() {
			c.enqueueError(request.Sequence, "websocket.subscription.invalid", "Invalid subscription.")
			return
		}
		c.mu.Lock()
		delete(c.subscriptions, subscription.Key())
		c.mu.Unlock()
		c.enqueueResponse(request.Sequence, nil)
	default:
		c.enqueueError(request.Sequence, "websocket.action.unknown", "Unknown WebSocket action.")
	}
}

func (c *webSocketConnection) enqueueHello(resumed, resyncRequired bool) {
	data, _ := json.Marshal(model.WebSocketHello{
		ConnectionId: c.id, NodeId: c.hub.nodeID, Resumed: resumed,
	})
	c.enqueueEvent(&model.WebSocketEvent{
		Id: model.NewId(), Event: string(model.WebSocketEventHello),
		UserId: c.principal.UserId, Data: data,
	})
	if resyncRequired {
		c.enqueueEvent(&model.WebSocketEvent{
			Id: model.NewId(), Event: string(model.WebSocketEventResync),
			UserId: c.principal.UserId,
		})
	}
}

func (c *webSocketConnection) enqueueEvent(event *model.WebSocketEvent) {
	c.mu.Lock()
	for _, sent := range c.history {
		if sent.Id == event.Id {
			c.mu.Unlock()
			return
		}
	}
	c.nextSequence++
	candidate := event.Clone()
	candidate.Sequence = c.nextSequence
	c.history = append(c.history, candidate.Clone())
	if len(c.history) > webSocketReplayQueueSize {
		c.history = append([]*model.WebSocketEvent(nil), c.history[len(c.history)-webSocketReplayQueueSize:]...)
	}
	c.mu.Unlock()
	select {
	case c.send <- outboundWebSocketMessage{event: candidate}:
	default:
		c.close(webSocketCloseBackpressure, "client is too slow", false)
	}
}

func (c *webSocketConnection) enqueueResponse(sequence int64, data json.RawMessage) {
	response := &model.WebSocketResponse{
		Status: "ok", Sequence: sequence, Data: append(json.RawMessage(nil), data...),
	}
	select {
	case c.send <- outboundWebSocketMessage{response: response}:
	default:
		c.close(webSocketCloseBackpressure, "client is too slow", false)
	}
}

func (c *webSocketConnection) enqueueError(sequence int64, code, message string) {
	response := &model.WebSocketResponse{
		Status: "error", Sequence: sequence,
		Error: &model.WebSocketError{Code: code, Message: message},
	}
	select {
	case c.send <- outboundWebSocketMessage{response: response}:
	default:
		c.close(webSocketCloseBackpressure, "client is too slow", false)
	}
}

func (c *webSocketConnection) hasSubscription(
	subscription model.WebSocketSubscription,
) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, exists := c.subscriptions[subscription.Key()]
	return exists
}

func (c *webSocketConnection) close(code int, reason string, replayable bool) {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.replayable = replayable
		c.mu.Unlock()
		message := websocket.FormatCloseMessage(code, reason)
		_ = c.socket.WriteControl(
			websocket.CloseMessage,
			message,
			time.Now().Add(webSocketWriteWait),
		)
		_ = c.socket.Close()
	})
}

func cloneWebSocketEvents(events []*model.WebSocketEvent) []*model.WebSocketEvent {
	cloned := make([]*model.WebSocketEvent, 0, len(events))
	for _, event := range events {
		cloned = append(cloned, event.Clone())
	}
	return cloned
}

func cloneSubscriptions(
	subscriptions map[string]model.WebSocketSubscription,
) map[string]model.WebSocketSubscription {
	cloned := make(map[string]model.WebSocketSubscription, len(subscriptions))
	for key, subscription := range subscriptions {
		cloned[key] = subscription
	}
	return cloned
}
