// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package cluster

import (
	"context"
	"errors"
)

var (
	// ErrStopped reports that the transport has been permanently stopped.
	ErrStopped = errors.New("cluster transport is stopped")
	// ErrNotStarted reports that Start has not completed successfully.
	ErrNotStarted = errors.New("cluster transport is not started")
	// ErrHandlerExists reports duplicate handler registration for one event.
	ErrHandlerExists = errors.New("cluster message handler is already registered")
	// ErrNodeUnavailable reports a targeted send to an unknown node.
	ErrNodeUnavailable = errors.New("cluster node is unavailable")
	// ErrNodeIDInUse reports duplicate node identity during multi-node join.
	ErrNodeIDInUse = errors.New("cluster node ID is already in use")
)

// Handler processes one cloned cluster message. Panics are recovered at the
// transport boundary. Handlers must be idempotent under best-effort delivery.
type Handler func(context.Context, *Message) error

// Logger reports operational transport failures without depending on logging.
type Logger interface {
	ErrorContext(ctx context.Context, message string, err error)
}

// Metrics receives only bounded transport facts selected inside cluster
// implementations. It never receives node identities, addresses, payloads, or
// error text.
type Metrics interface {
	ObserveClusterMessage(direction, event, outcome string, bytes int)
	ObserveClusterMembership(event string)
	ObserveClusterDiscovery(operation, outcome string)
	ObserveClusterAdmission(reason string)
	ObserveClusterFanout(recipients int)
}

// Transport is the server-owned inter-node messaging contract.
//
// Broadcast sends to peer nodes only and never invokes this node's handlers.
// SendToNode may target the current node and is used by the local transport to
// exercise registered handlers. Delivery is best-effort and non-durable.
type Transport interface {
	NodeID() string
	Start(context.Context) error
	Stop(context.Context) error
	Ping(context.Context) error
	RegisterHandler(Event, Handler) error
	Broadcast(context.Context, *Message) error
	SendToNode(context.Context, string, *Message) error
}
