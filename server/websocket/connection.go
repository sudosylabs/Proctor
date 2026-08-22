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

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type connectionRuntime struct {
	application Application
	logger      Logger
	localizer   Localizer
	locale      string
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
	attemptClose  sync.Once
	attempt       *examAttemptBinding
	terminal      app.CandidateExamTerminal

	activityMu sync.Mutex
	activities sync.WaitGroup
	finalized  bool
}

type examAttemptBinding struct {
	attemptID       model.ExamAttemptID
	sittingID       model.ExamSittingID
	classID         model.ClassID
	connectionID    model.AttemptConnectionID
	participationID model.AttemptParticipationID
	generation      int64
	requestHash     [32]byte
}

func newConnectionRuntime(
	application Application,
	logger Logger,
	localizer Localizer,
	locale string,
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
		logger:        logger,
		localizer:     localizer,
		locale:        locale,
		nodeID:        nodeID,
		socket:        socket,
		clock:         systemRuntimeClock{},
		principal:     clonePrincipal(principal),
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

func (c *connectionRuntime) run(ctx context.Context) {
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
	c.finalizeExamAttempt(ctx)
}

// acquire retains the runtime for one Hub-selected operation. The Hub calls it
// while holding the shard read lock, so unregister cannot detach and finalize
// the runtime between selection and retention.
func (c *connectionRuntime) acquire() bool {
	c.activityMu.Lock()
	defer c.activityMu.Unlock()
	if c.finalized {
		return false
	}
	c.activities.Add(1)
	return true
}

func (c *connectionRuntime) release() {
	c.activities.Done()
}

func (c *connectionRuntime) belongsToUser(userID string) bool {
	return c.principal.UserID.String() == userID
}

func (c *connectionRuntime) belongsToSession(sessionID string) bool {
	return c.principal.SessionID.String() == sessionID
}

func (c *connectionRuntime) userID() string {
	return c.principal.UserID.String()
}

func (c *connectionRuntime) connectionID() string {
	return c.id
}
