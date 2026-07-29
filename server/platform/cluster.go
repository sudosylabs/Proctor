// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package platform

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/mlog"
	"github.com/sudosylabs/proctor/server/model"
)

var (
	ErrClusterStopped         = errors.New("cluster transport is stopped")
	ErrClusterNotStarted      = errors.New("cluster transport is not started")
	ErrClusterHandlerExists   = errors.New("cluster message handler is already registered")
	ErrClusterNodeUnavailable = errors.New("cluster node is unavailable")
)

type ClusterMessageHandler func(context.Context, *model.ClusterMessage) error

// Cluster is the server-owned application messaging port. Broadcast sends to
// peer nodes only; it never invokes this node's handler. SendToNode may target
// this node and is used by the local transport to exercise the same handlers.
type Cluster interface {
	NodeID() string
	Start(context.Context) error
	Stop(context.Context) error
	Ping(context.Context) error
	RegisterMessageHandler(model.ClusterEvent, ClusterMessageHandler) error
	Broadcast(context.Context, *model.ClusterMessage) error
	SendToNode(context.Context, string, *model.ClusterMessage) error
}

func newCluster(settings config.Cluster, logger *mlog.Logger) (Cluster, error) {
	switch settings.Backend {
	case "local":
		return newLocalCluster(settings.NodeID, logger)
	default:
		return nil, fmt.Errorf("unsupported cluster backend %q", settings.Backend)
	}
}

type localClusterState uint8

const (
	localClusterCreated localClusterState = iota
	localClusterStarted
	localClusterStopped
)

// localCluster is the single-node degenerate form of the cluster architecture.
// Peer broadcasts are deliberately no-ops, preventing local rebroadcast loops.
type localCluster struct {
	nodeID   string
	logger   *mlog.Logger
	mu       sync.RWMutex
	state    localClusterState
	handlers map[model.ClusterEvent]ClusterMessageHandler
}

func newLocalCluster(nodeID string, logger *mlog.Logger) (*localCluster, error) {
	if nodeID == "" {
		return nil, errors.New("cluster node ID is required")
	}
	if logger == nil {
		return nil, errors.New("cluster logger is required")
	}
	return &localCluster{
		nodeID:   nodeID,
		logger:   logger.With(mlog.String("component", "cluster"), mlog.String("node_id", nodeID)),
		handlers: make(map[model.ClusterEvent]ClusterMessageHandler),
	}, nil
}

func (c *localCluster) NodeID() string {
	return c.nodeID
}

func (c *localCluster) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	switch c.state {
	case localClusterCreated:
		c.state = localClusterStarted
		return nil
	case localClusterStarted:
		return nil
	default:
		return ErrClusterStopped
	}
}

func (c *localCluster) Stop(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == localClusterStopped {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	c.state = localClusterStopped
	return nil
}

func (c *localCluster) Ping(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.state == localClusterStopped {
		return ErrClusterStopped
	}
	return nil
}

func (c *localCluster) RegisterMessageHandler(
	event model.ClusterEvent,
	handler ClusterMessageHandler,
) error {
	if handler == nil {
		return errors.New("cluster message handler is nil")
	}
	probe := &model.ClusterMessage{Event: event, SendType: model.ClusterSendBestEffort}
	if err := probe.Validate(); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == localClusterStopped {
		return ErrClusterStopped
	}
	if _, exists := c.handlers[event]; exists {
		return fmt.Errorf("%w: %s", ErrClusterHandlerExists, event)
	}
	c.handlers[event] = handler
	return nil
}

func (c *localCluster) Broadcast(ctx context.Context, message *model.ClusterMessage) error {
	if err := c.validateSend(ctx, message); err != nil {
		return err
	}
	// Broadcast is peer-only. A single-node transport has no peers.
	return nil
}

func (c *localCluster) SendToNode(
	ctx context.Context,
	nodeID string,
	message *model.ClusterMessage,
) error {
	if err := c.validateSend(ctx, message); err != nil {
		return err
	}
	if nodeID != c.nodeID {
		return fmt.Errorf("%w: %s", ErrClusterNodeUnavailable, nodeID)
	}

	c.mu.RLock()
	handler := c.handlers[message.Event]
	c.mu.RUnlock()
	if handler == nil {
		return nil
	}
	cloned := message.Clone()
	if err := callClusterHandler(ctx, handler, cloned); err != nil {
		c.logger.ErrorContext(
			ctx,
			"cluster message handler failed",
			mlog.String("event", string(message.Event)),
			mlog.String("send_type", string(message.SendType)),
			mlog.Int("data_bytes", len(message.Data)),
			mlog.Err(err),
		)
		return fmt.Errorf("handle local cluster event %s: %w", message.Event, err)
	}
	return nil
}

func (c *localCluster) validateSend(ctx context.Context, message *model.ClusterMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := message.Validate(); err != nil {
		return err
	}
	c.mu.RLock()
	state := c.state
	c.mu.RUnlock()
	switch state {
	case localClusterStarted:
		return nil
	case localClusterStopped:
		return ErrClusterStopped
	default:
		return ErrClusterNotStarted
	}
}

func callClusterHandler(
	ctx context.Context,
	handler ClusterMessageHandler,
	message *model.ClusterMessage,
) (resultErr error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			resultErr = errors.New("cluster message handler panicked")
		}
	}()
	return handler(ctx, message)
}

var _ Cluster = (*localCluster)(nil)
