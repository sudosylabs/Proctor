// ---------------------------------------------------------------------------------------------
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Modifications Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
//
// This file contains substantially modified code adapted from Mattermost's
// public WebSocket connection flow. See server/NOTICE for exact provenance.

package websocket

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

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
	recorder    Recorder

	mu                 sync.Mutex
	nextSequence       int64
	history            []*Event
	subscriptions      map[string]Subscription
	replayable         bool
	send               chan outboundMessage
	closeStarted       atomic.Bool
	transportCloseOnce sync.Once
	attemptClose       sync.Once
	attempt            *examAttemptBinding
	terminal           app.CandidateExamTerminal
	terminalReaders    sync.WaitGroup

	activityMu sync.Mutex
	activities sync.WaitGroup
	finalized  bool

	lifecycleMu      sync.Mutex
	runStarted       bool
	stopping         bool
	runDone          chan struct{}
	runCancel        context.CancelFunc
	finalizeCancel   context.CancelFunc
	shutdownDeadline time.Time
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
		runDone:       make(chan struct{}),
	}
	for _, event := range replayEvents {
		runtime.send <- outboundMessage{event: event}
	}
	return runtime
}

func (c *connectionRuntime) run(ctx context.Context) {
	c.lifecycleMu.Lock()
	if c.stopping || c.runStarted {
		c.lifecycleMu.Unlock()
		return
	}
	if c.runDone == nil {
		c.runDone = make(chan struct{})
	}
	pumpCtx, cancel := context.WithCancel(ctx)
	c.runStarted, c.runCancel = true, cancel
	c.lifecycleMu.Unlock()
	defer close(c.runDone)
	defer cancel()
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
	c.terminalReaders.Wait()
}

// beginShutdown stops new connection work while retaining the shared deadline
// for durable finalization. A registered socket whose pumps have not begun can
// be disposed immediately and can never start afterward.
func (c *connectionRuntime) beginShutdown(deadline time.Time) {
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()
	if c.runDone == nil {
		c.runDone = make(chan struct{})
	}
	if !c.stopping {
		c.stopping = true
		if !c.runStarted {
			close(c.runDone)
		}
	}
	c.shutdownDeadline = deadline
	if c.runCancel != nil {
		c.runCancel()
	}
}

func (c *connectionRuntime) finalizationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()
	deadline := time.Now().Add(5 * time.Second)
	if !c.shutdownDeadline.IsZero() {
		deadline = minTime(deadline, c.shutdownDeadline)
	}
	finalizeCtx, cancel := context.WithDeadline(context.WithoutCancel(ctx), deadline)
	c.finalizeCancel = cancel
	return finalizeCtx, cancel
}

func (c *connectionRuntime) cancelFinalization() {
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()
	if c.finalizeCancel != nil {
		c.finalizeCancel()
	}
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
