// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// This file contains a substantially modified adaptation of Mattermost's
// public WebSocket connection flow. See server/NOTICE for exact provenance.

package websocket

import (
	"context"
	"sync"

	"github.com/sudosylabs/proctor/server/model"
)

type connectionRuntime struct {
	application Application
	nodeID      string
	socket      connectionSocket
	clock       runtimeClock
	principal   model.Principal
	metadata    model.RequestMetadata
	id          string

	mu            sync.Mutex
	nextSequence  int64
	history       []*Event
	subscriptions map[string]Subscription
	replayable    bool
	send          chan outboundMessage
	closeOnce     sync.Once
}

func newConnectionRuntime(
	application Application,
	nodeID string,
	socket connectionSocket,
	principal model.Principal,
	metadata model.RequestMetadata,
	id string,
	nextSequence int64,
	history []*Event,
	subscriptions map[string]Subscription,
	replayEvents []*Event,
) *connectionRuntime {
	runtime := &connectionRuntime{
		application:   application,
		nodeID:        nodeID,
		socket:        socket,
		clock:         systemRuntimeClock{},
		principal:     principal,
		metadata:      metadata,
		id:            id,
		nextSequence:  nextSequence,
		history:       history,
		subscriptions: subscriptions,
		replayable:    true,
		send:          make(chan outboundMessage, sendQueueSize),
	}
	for _, event := range replayEvents {
		runtime.send <- outboundMessage{event: event}
	}
	return runtime
}

func (c *connectionRuntime) run(ctx context.Context) connectionSnapshot {
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
	c.closeTransport()
	pumps.Wait()
	return c.finalSnapshot()
}

func (c *connectionRuntime) belongsToUser(userID string) bool {
	return c.principal.UserID.String() == userID
}

func (c *connectionRuntime) belongsToSession(sessionID string) bool {
	return c.principal.SessionID.String() == sessionID
}
