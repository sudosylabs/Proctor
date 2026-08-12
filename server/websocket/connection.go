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

type connection struct {
	hub       *Hub
	socket    connectionSocket
	clock     runtimeClock
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

// connectionRuntime names the private deep runtime exercised by the Hub and
// deterministic package tests. The temporary alias keeps this first migration
// slice behavior-preserving while ownership moves behind the runtime seam.
type connectionRuntime = connection

func (c *connection) pump(ctx context.Context) {
	c.run(ctx)
	c.hub.unregister(c)
}

func (c *connection) run(ctx context.Context) {
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
}
