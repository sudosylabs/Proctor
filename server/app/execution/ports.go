// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

// Package execution owns Proctor's exam-aware execution workflow. Its ports
// intentionally contain no execenv types: the reusable package remains
// exam-blind and the application remains transport- and adapter-independent.
package execution

import (
	"context"
	"errors"
	"io"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type Content interface {
	OpenStarterWorkspaceObject(context.Context, model.StarterWorkspaceObjectID) (io.ReadCloser, error)
	OpenAttemptWorkspaceObject(context.Context, model.AttemptWorkspaceObjectID) (io.ReadCloser, error)
}

var (
	ErrUnavailable        = errors.New("execution host unavailable")
	ErrCapacity           = errors.New("execution host has no capacity")
	ErrConflict           = errors.New("execution environment conflicts with its placement")
	ErrInvalid            = errors.New("execution request is invalid")
	ErrRevoked            = errors.New("execution environment is revoked")
	ErrNotFound           = errors.New("execution path not found")
	ErrProjectionPending  = errors.New("execution projection pending")
	ErrHostCursorConflict = errors.New("execution host has unprocessed observations")
	ErrInteractionBlocked = errors.New("execution interaction is blocked by current authority")
	ErrObservationLost    = errors.New("execution observation lost")
)

type Network string

const (
	NetworkNone      Network = "none"
	NetworkAllowlist Network = "allowlist"
)

type HostStatus struct {
	ID       string
	Usable   bool
	Isolated bool
	Freeze   bool
	Images   []string
	Networks []Network
	Slots    int
	Release  string
}

type Spec struct {
	ID      string
	Image   string
	Network Network
}

type NodeKind uint8

const (
	NodeFile NodeKind = iota
	NodeDirectory
)

type Node struct {
	Path    string
	Kind    NodeKind
	Version string
	Data    []byte
}

type Tree []Node

type Window struct {
	Cols uint16
	Rows uint16
}

type Terminal interface {
	io.ReadWriteCloser
	Resize(context.Context, Window) error
}

type Cursor string

type Operation uint8

const (
	OperationCreate Operation = iota + 1
	OperationReplace
	OperationMove
	OperationDelete
)

type Event struct {
	Semantic  *store.ExecutionObservation
	Cursor    Cursor
	Operation Operation
	Path      string
	From      string
}

type Mutation struct {
	Operation Operation
	Path      string
	From      string
	Kind      NodeKind
	Version   string
	Data      []byte
}

type Observation interface {
	Cursor() Cursor
	Next(context.Context) (Event, error)
	Close() error
}

type Environment interface {
	ReplaceTree(context.Context, Tree) error
	Apply(context.Context, []Mutation) error
	Watch(context.Context, Cursor) (Observation, error)
	Open(context.Context, string) (io.ReadCloser, error)
	Attach(context.Context, Window) (Terminal, error)
	Freeze(context.Context) error
	Thaw(context.Context) error
}

// HostDirectory is the complete consumer-owned port for configured execenv
// hosts. Implementations own connections and reconnection; callers address a
// host only by the stable operator ID persisted in an Execution Grant.
type HostDirectory interface {
	Catalog(context.Context) ([]HostStatus, error)
	Ensure(context.Context, string, Spec) (Environment, error)
	// Existing returns the original environment handle without creating a guest
	// or reconnecting it. Losing that handle requires a fresh grant/projection.
	Existing(context.Context, string, Spec) (Environment, error)
	Revoke(context.Context, string, string) error
}

// ControlledEnvironment is supported only by an adapter with acknowledged,
// monotonically fenced control of the actual guest. Legacy Freeze/Thaw cannot
// provide this contract. The epoch is immutable for this environment handle.
type ControlledEnvironment interface {
	Environment
	Epoch() string
	Control(context.Context, model.ExecutionFence, model.ExecutionControlState) (ControlReceipt, error)
}

type ControlReceipt struct {
	Fence     model.ExecutionFence
	State     model.ExecutionControlState
	Confirmed bool
}

// ProjectionEnvironment implements atomic, consecutive Workspace projection.
// Only adapters backed by the actual fenced host protocol may expose this port.
// ApplyProjection returns an exact receipt on retry without reapplying effects.
type ProjectionEnvironment interface {
	ControlledEnvironment
	UploadProjectionContent(context.Context, model.ExecutionFence, io.Reader) (store.ExecutionProjectionContent, error)
	ApplyProjection(context.Context, store.ExecutionProjectionRequest) (store.ExecutionProjectionReceipt, error)
}

// SemanticObservation keeps immutable event content and acknowledgement tied to
// the original environment handle. Confirm records the accepted Workspace echo
// before Acknowledge permits the host to release captured bytes.
type SemanticObservation interface {
	Observation
	OpenContent(context.Context, store.ExecutionObservation) (io.ReadCloser, error)
	Confirm(context.Context, store.ExecutionObservation, store.ExecutionProjectionMutation) error
	Acknowledge(context.Context, int64) error
}

// ObservationEnvironment starts recovery without requiring projection to have
// caught up: retained guest changes may be precisely what is blocking Apply.
type ObservationEnvironment interface {
	ProjectionEnvironment
	Observe(context.Context, model.ExecutionFence, int64) (SemanticObservation, error)
}

// PendingSemanticObservation supports bounded startup draining before a PTY is
// attached. Next remains the streaming operation for the active terminal.
type PendingSemanticObservation interface {
	SemanticObservation
	NextPending(context.Context) (Event, bool, error)
}
