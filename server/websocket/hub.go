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
	"hash/maphash"
	"net/http"
	"net/url"
	"runtime"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/app/realtime"
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
	conns  map[string]*connectionRuntime
	replay map[string]*replayState
}

// Logger reports operational transport failures without depending on mlog.
type Logger interface {
	WarnContext(ctx context.Context, message string, err error)
}

// Application is the narrow application surface each connection runtime needs
// for subscription authorization and principal revalidation.
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
			conns:  make(map[string]*connectionRuntime),
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
	runtimeSocket := newGorillaConnectionSocket(socket)
	connection, resumed := h.register(
		runtimeSocket,
		principal,
		metadata,
		connectionID,
		sequence,
	)
	if connection == nil {
		_ = runtimeSocket.WriteControl(
			websocketCloseMessage,
			websocket.FormatCloseMessage(CloseLimit, "connection limit reached"),
			time.Now().Add(writeWait),
		)
		_ = runtimeSocket.Close()
		return nil
	}
	connection.enqueueHello(resumed, connectionID != "" && !resumed)
	connection.run(request.Context())
	h.unregister(connection)
	return nil
}

func (h *Hub) register(
	socket connectionSocket,
	principal model.Principal,
	metadata model.RequestMetadata,
	requestedID string,
	requestedSequence int64,
) (*connectionRuntime, bool) {
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
		if connection.belongsToUser(principal.UserID.String()) {
			userConnections++
		}
		if connection.belongsToSession(principal.SessionID.String()) {
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
	connection := newConnectionRuntime(
		h.application,
		h.logger,
		h.nodeID,
		socket,
		principal,
		metadata,
		connectionID,
		nextSequence,
		history,
		subscriptions,
		replayEvents,
	)
	shard.conns[connectionID] = connection
	return connection, resumed
}

func (h *Hub) unregister(connection *connectionRuntime) {
	shard := h.shardForUser(connection.userID())
	shard.mu.Lock()
	defer shard.mu.Unlock()
	connectionID := connection.connectionID()
	current, exists := shard.conns[connectionID]
	if !exists || current != connection {
		return
	}
	delete(shard.conns, connectionID)
	snapshot := connection.finalSnapshot()
	h.mu.RLock()
	started := h.state == hubStarted
	h.mu.RUnlock()
	if snapshot.replayable && started {
		shard.replay[snapshot.id] = &replayState{
			userID:        snapshot.principal.UserID.String(),
			sessionID:     snapshot.principal.SessionID.String(),
			nextSequence:  snapshot.nextSequence,
			history:       cloneEvents(snapshot.history),
			subscriptions: cloneSubscriptions(snapshot.subscriptions),
			expiresAt:     time.Now().Add(replayRetention),
		}
	}
}

// PublishLocal implements realtime.Sink using transport-neutral events.
func (h *Hub) PublishLocal(_ context.Context, event realtime.RealtimeEvent) {
	h.publishWire(eventFromRealtime(event))
}

// UnbindExamAttemptConnection implements realtime.Sink. It clears only the
// matching durable Attempt Connection binding and its protected subscription;
// the generic WebSocket remains available for unrelated actions.
func (h *Hub) UnbindExamAttemptConnection(connectionID model.AttemptConnectionID) {
	if !connectionID.IsValid() {
		return
	}
	var connections []*connectionRuntime
	for _, shard := range h.shards {
		shard.mu.RLock()
		for _, connection := range shard.conns {
			if connection.acquire() {
				connections = append(connections, connection)
			}
		}
		shard.mu.RUnlock()
	}
	for _, connection := range connections {
		connection.unbindExamAttemptConnection(connectionID)
		connection.release()
	}
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
	var connections []*connectionRuntime
	for _, shard := range shards {
		shard.mu.RLock()
		for _, connection := range shard.conns {
			if event.UserID != "" && !connection.belongsToUser(event.UserID) {
				continue
			}
			if event.Action != "" && !connection.hasSubscription(
				Subscription{Action: event.Action, Resource: event.Resource},
			) {
				continue
			}
			if connection.acquire() {
				connections = append(connections, connection)
			}
		}
		shard.mu.RUnlock()
	}
	for _, connection := range connections {
		connection.enqueueEvent(event)
		connection.release()
	}
}

// CloseSession implements realtime.Sink.
func (h *Hub) CloseSession(sessionID string, reason realtime.ConnectionCloseReason) {
	code, text := closeCodeForReason(reason)
	h.closeMatching(code, text, func(connection *connectionRuntime) bool {
		return connection.belongsToSession(sessionID)
	})
}

// CloseUser implements realtime.Sink.
func (h *Hub) CloseUser(userID string, reason realtime.ConnectionCloseReason) {
	code, text := closeCodeForReason(reason)
	h.closeMatching(code, text, func(connection *connectionRuntime) bool {
		return connection.belongsToUser(userID)
	})
}

// CloseAll implements realtime.Sink.
func (h *Hub) CloseAll(reason realtime.ConnectionCloseReason) {
	code, text := closeCodeForReason(reason)
	h.closeMatching(code, text, func(*connectionRuntime) bool { return true })
}

func eventFromRealtime(event realtime.RealtimeEvent) *Event {
	return &Event{
		Id:       event.ID,
		Event:    event.Name,
		UserID:   event.UserID,
		Action:   event.Action,
		Resource: resourceFromModel(event.Resource),
		Data:     append(json.RawMessage(nil), event.Data...),
	}
}

func closeCodeForReason(reason realtime.ConnectionCloseReason) (int, string) {
	switch reason {
	case realtime.ConnectionCloseSessionRevoked:
		return CloseSessionRevoked, "session revoked"
	case realtime.ConnectionCloseAuthorizationChanged:
		return CloseAuthorizationChanged, "authorization changed"
	default:
		return CloseServer, "connection closed"
	}
}

var _ realtime.Sink = (*Hub)(nil)

func (h *Hub) closeMatching(
	code int,
	reason string,
	matches func(*connectionRuntime) bool,
) {
	var connections []*connectionRuntime
	for _, shard := range h.shards {
		shard.mu.RLock()
		for _, connection := range shard.conns {
			if matches(connection) && connection.acquire() {
				connections = append(connections, connection)
			}
		}
		shard.mu.RUnlock()
	}
	for _, connection := range connections {
		connection.close(code, reason, false)
		connection.release()
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
		var connections []*connectionRuntime
		for _, shard := range h.shards {
			shard.mu.Lock()
			for _, connection := range shard.conns {
				if connection.acquire() {
					connections = append(connections, connection)
				}
			}
			shard.replay = make(map[string]*replayState)
			shard.mu.Unlock()
		}
		for _, connection := range connections {
			connection.close(CloseServer, "server shutting down", false)
			connection.release()
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
