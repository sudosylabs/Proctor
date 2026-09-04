// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

// Package local implements the single-node degenerate cluster transport.
// Peer broadcasts succeed without local delivery so multi-node fan-out stays
// loop-free when only one process is present.
package local

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/sudosylabs/proctor/server/cluster"
)

type state uint8

const (
	stateCreated state = iota
	stateStarted
	stateStopped
)

// Transport is the in-process single-node cluster adapter.
type Transport struct {
	nodeID   string
	logger   cluster.Logger
	mu       sync.RWMutex
	state    state
	handlers map[cluster.Event]cluster.Handler
}

// New constructs an inert local cluster transport. Call Start before send.
func New(nodeID string, logger cluster.Logger) (*Transport, error) {
	if nodeID == "" {
		return nil, errors.New("cluster node ID is required")
	}
	if logger == nil {
		return nil, errors.New("cluster logger is required")
	}
	return &Transport{
		nodeID:   nodeID,
		logger:   logger,
		handlers: make(map[cluster.Event]cluster.Handler),
	}, nil
}

// NodeID returns this node's stable identity.
func (c *Transport) NodeID() string {
	return c.nodeID
}

func (c *Transport) PeerCount() int { return 0 }

// Start marks the transport ready. It is idempotent while running.
func (c *Transport) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	switch c.state {
	case stateCreated:
		c.state = stateStarted
		return nil
	case stateStarted:
		return nil
	default:
		return cluster.ErrStopped
	}
}

// Stop permanently stops the transport. It is idempotent.
func (c *Transport) Stop(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == stateStopped {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	c.state = stateStopped
	return nil
}

// Ping reports whether the transport is still usable.
func (c *Transport) Ping(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.state == stateStopped {
		return cluster.ErrStopped
	}
	return nil
}

// RegisterHandler registers the sole handler for an event on this node.
func (c *Transport) RegisterHandler(event cluster.Event, handler cluster.Handler) error {
	if handler == nil {
		return errors.New("cluster message handler is nil")
	}
	probe := &cluster.Message{Event: event}
	if err := probe.Validate(); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == stateStopped {
		return cluster.ErrStopped
	}
	if _, exists := c.handlers[event]; exists {
		return fmt.Errorf("%w: %s", cluster.ErrHandlerExists, event)
	}
	c.handlers[event] = handler
	return nil
}

// Broadcast is peer-only. A single-node transport has no peers, so this is a
// validated no-op that never re-enters local handlers.
func (c *Transport) Broadcast(ctx context.Context, message *cluster.Message) error {
	return c.validateSend(ctx, message)
}

// SendToNode delivers to this node when nodeID matches, otherwise fails.
func (c *Transport) SendToNode(
	ctx context.Context,
	nodeID string,
	message *cluster.Message,
) error {
	if err := c.validateSend(ctx, message); err != nil {
		return err
	}
	if nodeID != c.nodeID {
		return fmt.Errorf("%w: %s", cluster.ErrNodeUnavailable, nodeID)
	}

	c.mu.RLock()
	handler := c.handlers[message.Event]
	c.mu.RUnlock()
	if handler == nil {
		return nil
	}
	cloned := message.Clone()
	if err := callHandler(ctx, handler, cloned); err != nil {
		c.logger.ErrorContext(
			ctx,
			fmt.Sprintf("cluster message handler failed event=%s data_bytes=%d", message.Event, len(message.Data)),
			err,
		)
		return fmt.Errorf("handle local cluster event %s: %w", message.Event, err)
	}
	return nil
}

func (c *Transport) validateSend(ctx context.Context, message *cluster.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := message.Validate(); err != nil {
		return err
	}
	c.mu.RLock()
	current := c.state
	c.mu.RUnlock()
	switch current {
	case stateStarted:
		return nil
	case stateStopped:
		return cluster.ErrStopped
	default:
		return cluster.ErrNotStarted
	}
}

func callHandler(
	ctx context.Context,
	handler cluster.Handler,
	message *cluster.Message,
) (resultErr error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			resultErr = errors.New("cluster message handler panicked")
		}
	}()
	return handler(ctx, message)
}

var _ cluster.Transport = (*Transport)(nil)
