// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

// Package execution owns Proctor's exam-aware execution workflow. Its ports
// intentionally contain no execenv types: the reusable package remains
// exam-blind and the application remains transport- and adapter-independent.
package execution

import (
	"context"
	"errors"
	"io"

	"github.com/sudosylabs/proctor/server/model"
)

type Content interface {
	OpenStarterWorkspaceObject(context.Context, model.StarterWorkspaceObjectID) (io.ReadCloser, error)
	OpenAttemptWorkspaceObject(context.Context, model.AttemptWorkspaceObjectID) (io.ReadCloser, error)
}

var (
	ErrUnavailable     = errors.New("execution host unavailable")
	ErrCapacity        = errors.New("execution host has no capacity")
	ErrConflict        = errors.New("execution environment conflicts with its placement")
	ErrInvalid         = errors.New("execution request is invalid")
	ErrRevoked         = errors.New("execution environment is revoked")
	ErrNotFound        = errors.New("execution path not found")
	ErrObservationLost = errors.New("execution observation lost")
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
	Revoke(context.Context, string, string) error
}
