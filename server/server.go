// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

// Package server owns Proctor runtime composition and lifecycle. Business
// policy remains in the application layer and is not implemented here.
//
// As the composition root, this package may depend on the components it wires;
// those components must not depend back on the module-root package.
//
// During the architecture migration, New delegates construction to the
// existing app server. The public facade remains stable while runtime ownership
// moves here in subsequent migration tickets. That legacy constructor starts
// bounded WebSocket replay maintenance during construction; callers must call
// Close even when Start is never called.
package server

import (
	"context"
	"errors"
	"fmt"

	"github.com/sudosylabs/proctor/server/app"
)

type options struct {
	configPath     string
	runtimeFactory func(context.Context, string) (runtime, error)
}

// Option configures construction without exposing infrastructure or transport
// implementation types.
type Option func(*options) error

// WithConfigPath selects the deployment configuration file used to construct
// the server. Applying the option fails when path is empty; loading and
// validating the file occurs in New.
func WithConfigPath(path string) Option {
	return func(settings *options) error {
		if path == "" {
			return errors.New("configuration path is empty")
		}
		settings.configPath = path
		return nil
	}
}

type runtime interface {
	Start(context.Context) error
	Close() error
	Ready() bool
}

type legacyRuntime struct {
	server *app.Server
}

func (r *legacyRuntime) Start(ctx context.Context) error {
	return r.server.Start(ctx)
}

func (r *legacyRuntime) Close() error {
	return r.server.Close()
}

func (r *legacyRuntime) Ready() bool {
	return r.server.Health().Ready()
}

// Server is the narrow construction and lifecycle facade for one Proctor node.
type Server struct {
	runtime runtime
}

// New constructs one Proctor server. It validates options and returns
// configuration or infrastructure construction failures without a partially
// usable Server.
//
// New does not start network listeners. During the architecture migration its
// legacy delegate does start bounded WebSocket replay maintenance, so callers
// must call Close on every successfully constructed Server even if Start is
// never called.
func New(ctx context.Context, optionValues ...Option) (*Server, error) {
	settings := options{runtimeFactory: newLegacyRuntime}
	for _, option := range optionValues {
		if option == nil {
			return nil, errors.New("server option is nil")
		}
		if err := option(&settings); err != nil {
			return nil, fmt.Errorf("apply server option: %w", err)
		}
	}

	constructedRuntime, err := settings.runtimeFactory(ctx, settings.configPath)
	if err != nil {
		return nil, fmt.Errorf("construct server: %w", err)
	}
	return &Server{runtime: constructedRuntime}, nil
}

func newLegacyRuntime(ctx context.Context, configPath string) (runtime, error) {
	var legacyOptions []app.Option
	if configPath != "" {
		legacyOptions = append(legacyOptions, app.WithConfigPath(configPath))
	}
	legacy, err := app.NewServer(ctx, legacyOptions...)
	if err != nil {
		return nil, err
	}
	return &legacyRuntime{server: legacy}, nil
}

// Start runs the server until the context is canceled, the listener stops, or
// startup fails. It returns an error when the server is closed, was already
// started, cannot start a dependency or listener, or stops unexpectedly.
func (s *Server) Start(ctx context.Context) error {
	return s.runtime.Start(ctx)
}

// Close stops the server and releases its resources. It is safe to call more
// than once and returns any error encountered while shutting resources down.
func (s *Server) Close() error {
	return s.runtime.Close()
}

// Ready reports whether the server can currently accept traffic.
func (s *Server) Ready() bool {
	return s.runtime.Ready()
}
