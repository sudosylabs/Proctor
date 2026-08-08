// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// This file contains a substantially modified adaptation of Mattermost's
// public WebSocket connection, hub, replay-queue, and router flow. See
// server/NOTICE for exact provenance.

package websocket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/maphash"
	"net/http"
	"net/url"
	"runtime"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

const (
	sendQueueSize   = 256
	replayQueueSize = 128
	writeWait       = 30 * time.Second
	pongWait        = 100 * time.Second
	pingInterval    = 60 * time.Second
	sessionCheck    = 30 * time.Second
	replayRetention = 2 * time.Minute
	// Close codes are part of the public WebSocket protocol contract.
	CloseServer               = 4000
	CloseSessionRevoked       = 4001
	CloseBackpressure         = 4002
	CloseAuthorizationChanged = 4003
	CloseLimit                = 4004
	maximumPerSession         = 8
	maximumPerUser            = 64
	maximumSubscriptions      = 256
)

type outboundMessage struct {
	event    *Event
	response *Response
}

type replayState struct {
	userID        string
	sessionID     string
	nextSequence  int64
	history       []*Event
	subscriptions map[string]Subscription
	expiresAt     time.Time
}

type hubState uint8

const (
	hubCreated hubState = iota
	hubStarted
	hubStopped
)

// Hub owns only connections attached to this process. Application
// events arrive through PublishLocal after the application has performed the
// local-first/cluster-peer publication flow.
//
// Construction is inert: Start owns the replay reaper; Close is idempotent and
// drains connections when the hub was started.
type Hub struct {
	application Application
	logger      Logger
	publicURL   *url.URL
	nodeID      string
	hashSeed    maphash.Seed

	mu     sync.RWMutex
	state  hubState
	shards []*shard
	stop   chan struct{}
	done   chan struct{}
}

type shard struct {
	mu     sync.RWMutex
	conns  map[string]*connection
	replay map[string]*replayState
}

type connection struct {
	hub       *Hub
	socket    *websocket.Conn
	principal model.Principal
	metadata  model.RequestMetadata
	id        string

	mu            sync.Mutex
	nextSequence  int64
	history       []*Event
	subscriptions map[string]Subscription
	replayable    bool
	send          chan outboundMessage
	closeOnce     sync.Once
}

// Logger reports operational transport failures without depending on mlog.
type Logger interface {
	WarnContext(ctx context.Context, message string, err error)
}

// Application is the narrow application surface the hub needs for subscription
// authorization and session revalidation.
type Application interface {
	AuthorizeWebSocketSubscription(
		context.Context,
		model.Principal,
		model.RequestMetadata,
		model.Action,
		model.Resource,
	) error
	ValidateWebSocketPrincipal(context.Context, model.Principal) error
}

// NewHub constructs an inert hub. Call Start before Accept; Close stops any
// started background work and open connections.
func NewHub(
	application Application,
	logger Logger,
	publicURL string,
	nodeID string,
) (*Hub, error) {
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
	hub := &Hub{
		application: application,
		logger:      logger,
		publicURL:   parsed,
		nodeID:      nodeID,
		hashSeed:    maphash.MakeSeed(),
		state:       hubCreated,
		shards:      make([]*shard, shardCount),
	}
	for index := range hub.shards {
		hub.shards[index] = &shard{
			conns:  make(map[string]*connection),
			replay: make(map[string]*replayState),
		}
	}
	return hub, nil
}

// Start begins replay-state reaping. It is idempotent while the hub is running
// and fails after Close.
func (h *Hub) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	switch h.state {
	case hubCreated:
		h.stop = make(chan struct{})
		h.done = make(chan struct{})
		h.state = hubStarted
		go h.reapReplayStates()
		return nil
	case hubStarted:
		return nil
	default:
		return errors.New("websocket hub is closed")
	}
}

// Started reports whether Start has succeeded and Close has not completed.
func (h *Hub) Started() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.state == hubStarted
}

func (h *Hub) OriginAllowed(origin string, allowMissingOrigin bool) bool {
	if origin == "" {
		return allowMissingOrigin
	}
	parsed, err := url.Parse(origin)
	return err == nil &&
		parsed.Scheme == h.publicURL.Scheme &&
		parsed.Host == h.publicURL.Host
}

// Accept upgrades an already-authenticated HTTP request to a WebSocket
// connection. Callers must enforce session authentication before Accept.
// The hub must be Start'ed; construction alone does not accept connections.
func (h *Hub) Accept(
	writer http.ResponseWriter,
	request *http.Request,
	principal model.Principal,
	metadata model.RequestMetadata,
	connectionID string,
	sequence int64,
	allowMissingOrigin bool,
) error {
	if !h.Started() {
		return app.NewError("websocket.unavailable")
	}
	if !h.OriginAllowed(request.Header.Get("Origin"), allowMissingOrigin) {
		return app.NewError("websocket.origin.invalid")
	}
	upgrader := websocket.Upgrader{
		ReadBufferSize:  MaxMessageBytes,
		WriteBufferSize: MaxMessageBytes,
		CheckOrigin: func(*http.Request) bool {
			// Credential-aware origin decision was made immediately above.
			return true
		},
	}
	socket, err := upgrader.Upgrade(writer, request, nil)
	if err != nil {
		h.logger.WarnContext(request.Context(), "WebSocket upgrade failed", err)
		return nil
	}
	connection, resumed := h.register(
		socket,
		principal,
		metadata,
		connectionID,
		sequence,
	)
	if connection == nil {
		_ = socket.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(CloseLimit, "connection limit reached"),
			time.Now().Add(writeWait),
		)
		_ = socket.Close()
		return nil
	}
	connection.enqueueHello(resumed, connectionID != "" && !resumed)
	connection.pump(request.Context())
	return nil
}

func (h *Hub) register(
	socket *websocket.Conn,
	principal model.Principal,
	metadata model.RequestMetadata,
	requestedID string,
	requestedSequence int64,
) (*connection, bool) {
	h.mu.RLock()
	started := h.state == hubStarted
	h.mu.RUnlock()
	if !started {
		return nil, false
	}

	shard := h.shardForUser(principal.UserID.String())
	shard.mu.Lock()
	defer shard.mu.Unlock()
	h.mu.RLock()
	started = h.state == hubStarted
	h.mu.RUnlock()
	if !started {
		return nil, false
	}
	h.pruneReplayLocked(shard, time.Now())
	var userConnections, sessionConnections int
	for _, connection := range shard.conns {
		if connection.principal.UserID.String() == principal.UserID.String() {
			userConnections++
		}
		if connection.principal.SessionID.String() == principal.SessionID.String() {
			sessionConnections++
		}
	}
	if userConnections >= maximumPerUser ||
		sessionConnections >= maximumPerSession {
		return nil, false
	}
	connectionID := model.NewId()
	nextSequence := int64(0)
	var (
		history       []*Event
		subscriptions = make(map[string]Subscription)
		replayEvents  []*Event
		resumed       bool
	)
	if state := shard.replay[requestedID]; state != nil &&
		state.userID == principal.UserID.String() &&
		state.sessionID == principal.SessionID.String() &&
		requestedSequence <= state.nextSequence {
		oldest := state.nextSequence
		if len(state.history) != 0 {
			oldest = state.history[0].Sequence - 1
		}
		if requestedSequence >= oldest {
			connectionID = requestedID
			nextSequence = state.nextSequence
			history = cloneEvents(state.history)
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
	connection := &connection{
		hub: h, socket: socket, principal: principal, metadata: metadata,
		id: connectionID, nextSequence: nextSequence, history: history,
		subscriptions: subscriptions, replayable: true,
		send: make(chan outboundMessage, sendQueueSize),
	}
	shard.conns[connection.id] = connection
	for _, event := range replayEvents {
		connection.send <- outboundMessage{event: event}
	}
	return connection, resumed
}

func (h *Hub) unregister(connection *connection) {
	shard := h.shardForUser(connection.principal.UserID.String())
	shard.mu.Lock()
	defer shard.mu.Unlock()
	current, exists := shard.conns[connection.id]
	if !exists || current != connection {
		return
	}
	delete(shard.conns, connection.id)
	connection.mu.Lock()
	h.mu.RLock()
	started := h.state == hubStarted
	h.mu.RUnlock()
	if connection.replayable && started {
		shard.replay[connection.id] = &replayState{
			userID:        connection.principal.UserID.String(),
			sessionID:     connection.principal.SessionID.String(),
			nextSequence:  connection.nextSequence,
			history:       cloneEvents(connection.history),
			subscriptions: cloneSubscriptions(connection.subscriptions),
			expiresAt:     time.Now().Add(replayRetention),
		}
	}
	connection.mu.Unlock()
}

// PublishLocal implements app.RealtimeSink using transport-neutral events.
func (h *Hub) PublishLocal(_ context.Context, event app.RealtimeEvent) {
	h.publishWire(eventFromRealtime(event))
}

func (h *Hub) publishWire(event *Event) {
	if event == nil || event.ValidateForPublish() != nil {
		return
	}
	var shards []*shard
	if event.UserID != "" {
		shards = []*shard{h.shardForUser(event.UserID)}
	} else {
		shards = h.shards
	}
	var connections []*connection
	for _, shard := range shards {
		shard.mu.RLock()
		for _, connection := range shard.conns {
			connections = append(connections, connection)
		}
		shard.mu.RUnlock()
	}
	for _, connection := range connections {
		if event.UserID != "" && connection.principal.UserID.String() != event.UserID {
			continue
		}
		if event.Action != "" && !connection.hasSubscription(
			Subscription{Action: event.Action, Resource: event.Resource},
		) {
			continue
		}
		connection.enqueueEvent(event)
	}
}

// CloseSession implements app.RealtimeSink.
func (h *Hub) CloseSession(sessionID string, reason app.ConnectionCloseReason) {
	code, text := closeCodeForReason(reason)
	h.closeMatching(code, text, func(connection *connection) bool {
		return connection.principal.SessionID.String() == sessionID
	})
}

// CloseUser implements app.RealtimeSink.
func (h *Hub) CloseUser(userID string, reason app.ConnectionCloseReason) {
	code, text := closeCodeForReason(reason)
	h.closeMatching(code, text, func(connection *connection) bool {
		return connection.principal.UserID.String() == userID
	})
}

// CloseAll implements app.RealtimeSink.
func (h *Hub) CloseAll(reason app.ConnectionCloseReason) {
	code, text := closeCodeForReason(reason)
	h.closeMatching(code, text, func(*connection) bool { return true })
}

func eventFromRealtime(event app.RealtimeEvent) *Event {
	return &Event{
		Id:       event.ID,
		Event:    event.Name,
		UserID:   event.UserID,
		Action:   event.Action,
		Resource: resourceFromModel(event.Resource),
		Data:     append(json.RawMessage(nil), event.Data...),
	}
}

func closeCodeForReason(reason app.ConnectionCloseReason) (int, string) {
	switch reason {
	case app.ConnectionCloseSessionRevoked:
		return CloseSessionRevoked, "session revoked"
	case app.ConnectionCloseAuthorizationChanged:
		return CloseAuthorizationChanged, "authorization changed"
	default:
		return CloseServer, "connection closed"
	}
}

var _ app.RealtimeSink = (*Hub)(nil)

func (h *Hub) closeMatching(
	code int,
	reason string,
	matches func(*connection) bool,
) {
	var connections []*connection
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

// Close stops the reaper when running, drains connections, and is idempotent.
// Closing a never-started hub is a no-op success.
func (h *Hub) Close() error {
	h.mu.Lock()
	switch h.state {
	case hubCreated:
		h.state = hubStopped
		h.mu.Unlock()
		return nil
	case hubStopped:
		done := h.done
		h.mu.Unlock()
		if done != nil {
			<-done
		}
		return nil
	case hubStarted:
		h.state = hubStopped
		close(h.stop)
		done := h.done
		h.mu.Unlock()
		var connections []*connection
		for _, shard := range h.shards {
			shard.mu.Lock()
			for _, connection := range shard.conns {
				connections = append(connections, connection)
			}
			shard.replay = make(map[string]*replayState)
			shard.mu.Unlock()
		}
		for _, connection := range connections {
			connection.close(CloseServer, "server shutting down", false)
		}
		<-done
		return nil
	default:
		h.mu.Unlock()
		return nil
	}
}

func (h *Hub) reapReplayStates() {
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

func (h *Hub) pruneReplayLocked(
	shard *shard,
	now time.Time,
) {
	for id, state := range shard.replay {
		if !state.expiresAt.After(now) {
			delete(shard.replay, id)
		}
	}
}

func (h *Hub) shardForUser(userID string) *shard {
	var hash maphash.Hash
	hash.SetSeed(h.hashSeed)
	_, _ = hash.WriteString(userID)
	return h.shards[int(hash.Sum64()%uint64(len(h.shards)))]
}

func (c *connection) pump(ctx context.Context) {
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

func (c *connection) readPump(ctx context.Context) {
	c.socket.SetReadLimit(MaxMessageBytes)
	_ = c.socket.SetReadDeadline(time.Now().Add(pongWait))
	c.socket.SetPongHandler(func(string) error {
		return c.socket.SetReadDeadline(time.Now().Add(pongWait))
	})
	for {
		var request Request
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

func (c *connection) writePump(ctx context.Context) {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case message := <-c.send:
			_ = c.socket.SetWriteDeadline(time.Now().Add(writeWait))
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
			_ = c.socket.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.socket.WriteMessage(websocket.PingMessage, nil); err != nil {
				_ = c.socket.Close()
				return
			}
		}
	}
}

func (c *connection) sessionPump(ctx context.Context) {
	ticker := time.NewTicker(sessionCheck)
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
				c.close(CloseSessionRevoked, "session no longer valid", false)
				return
			}
		}
	}
}

func (c *connection) handleRequest(
	ctx context.Context,
	request *Request,
) {
	switch request.Action {
	case "ping":
		c.enqueueResponse(request.Sequence, json.RawMessage(`{"pong":true}`))
	case "subscribe":
		var subscription Subscription
		if err := json.Unmarshal(request.Data, &subscription); err != nil ||
			!subscription.IsValid() {
			c.enqueueError(request.Sequence, "websocket.subscription.invalid", "Invalid subscription.")
			return
		}
		metadata := c.metadata
		metadata.RequestID = fmt.Sprintf("%s:%d", c.id, request.Sequence)
		if err := c.hub.application.AuthorizeWebSocketSubscription(
			ctx,
			c.principal,
			metadata,
			subscription.Action,
			subscription.Resource.model(),
		); err != nil {
			code := "authorization.denied"
			message := "WebSocket subscription denied."
			if failure, ok := app.As(err); ok {
				code = failure.Code()
				if code != "authorization.denied" {
					message = "WebSocket subscription failed."
				}
			}
			c.enqueueError(request.Sequence, code, message)
			return
		}
		c.mu.Lock()
		if _, exists := c.subscriptions[subscription.Key()]; !exists &&
			len(c.subscriptions) >= maximumSubscriptions {
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
		var subscription Subscription
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

func (c *connection) enqueueHello(resumed, resyncRequired bool) {
	data, _ := json.Marshal(Hello{
		ConnectionId: c.id, NodeId: c.hub.nodeID, Resumed: resumed,
	})
	c.enqueueEvent(&Event{
		Id: model.NewId(), Event: string(EventHello),
		UserID: c.principal.UserID.String(), Data: data,
	})
	if resyncRequired {
		c.enqueueEvent(&Event{
			Id: model.NewId(), Event: string(EventResync),
			UserID: c.principal.UserID.String(),
		})
	}
}

func (c *connection) enqueueEvent(event *Event) {
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
	if len(c.history) > replayQueueSize {
		c.history = append([]*Event(nil), c.history[len(c.history)-replayQueueSize:]...)
	}
	c.mu.Unlock()
	select {
	case c.send <- outboundMessage{event: candidate}:
	default:
		c.close(CloseBackpressure, "client is too slow", false)
	}
}

func (c *connection) enqueueResponse(sequence int64, data json.RawMessage) {
	response := &Response{
		Status: "ok", Sequence: sequence, Data: append(json.RawMessage(nil), data...),
	}
	select {
	case c.send <- outboundMessage{response: response}:
	default:
		c.close(CloseBackpressure, "client is too slow", false)
	}
}

func (c *connection) enqueueError(sequence int64, code, message string) {
	response := &Response{
		Status: "error", Sequence: sequence,
		Error: &Error{Code: code, Message: message},
	}
	select {
	case c.send <- outboundMessage{response: response}:
	default:
		c.close(CloseBackpressure, "client is too slow", false)
	}
}

func (c *connection) hasSubscription(
	subscription Subscription,
) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, exists := c.subscriptions[subscription.Key()]
	return exists
}

func (c *connection) close(code int, reason string, replayable bool) {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.replayable = replayable
		c.mu.Unlock()
		message := websocket.FormatCloseMessage(code, reason)
		_ = c.socket.WriteControl(
			websocket.CloseMessage,
			message,
			time.Now().Add(writeWait),
		)
		_ = c.socket.Close()
	})
}

func cloneEvents(events []*Event) []*Event {
	cloned := make([]*Event, 0, len(events))
	for _, event := range events {
		cloned = append(cloned, event.Clone())
	}
	return cloned
}

func cloneSubscriptions(
	subscriptions map[string]Subscription,
) map[string]Subscription {
	cloned := make(map[string]Subscription, len(subscriptions))
	for key, subscription := range subscriptions {
		cloned[key] = subscription
	}
	return cloned
}
