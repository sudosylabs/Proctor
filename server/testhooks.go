// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"fmt"

	vfspkg "github.com/sudosylabs/proctor/packages/vfs"
	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/app/api"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/mlog"
	"github.com/sudosylabs/proctor/server/platform"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/localcachelayer"
	"github.com/sudosylabs/proctor/server/store/retrylayer"
	"github.com/sudosylabs/proctor/server/store/timerlayer"
)

// TestingOverrides replaces individual runtime capabilities for tests. A nil
// field keeps the production configuration-driven selection. Provided
// capabilities must be fully constructed by the caller; ownership transfers to
// the assembled runtime, which closes them on construction failure and when
// the Server closes, exactly as in production.
//
// This surface exists for testlib and focused tests. Production composition
// uses New with WithConfigPath only.
type TestingOverrides struct {
	Configuration     *config.Store
	Logger            *mlog.Logger
	Persistence       store.Store
	StoreRetry        *retrylayer.Policy
	StoreMetrics      timerlayer.Recorder
	StoreLocalCache   localcachelayer.Cache
	StoreCachePolicy  *localcachelayer.Policy
	StoreCacheMetrics localcachelayer.Recorder
	Cache             platform.Cache
	Cluster           platform.Cluster
	Mailer            platform.Mailer
	Filesystem        vfspkg.FileSystem
	// BuildInfo replaces the served build information when any field is set.
	BuildInfo api.BuildInfo
}

// TestingRuntime exposes the assembled graph for test inspection. The Server
// owns lifecycle exactly as in production and must be Closed.
type TestingRuntime struct {
	Server      *Server
	Platform    *platform.Service
	Application *app.App
	API         *api.API
	Health      *app.Health
}

// NewForTesting constructs the production runtime graph with explicit
// capability overrides and returns the assembled handles alongside the
// lifecycle-owning Server. Construction flows through New, so startup,
// readiness, shutdown, and cleanup behavior is identical to production.
func NewForTesting(ctx context.Context, overrides TestingOverrides) (*TestingRuntime, error) {
	var assembled *assembledRuntime
	node, err := New(ctx, func(settings *options) error {
		settings.runtimeFactory = func(ctx context.Context, configPath string) (runtimeComponents, error) {
			graph, err := assembleRuntime(ctx, configPath, overrides)
			if err != nil {
				return runtimeComponents{}, err
			}
			assembled = graph
			return graph.components, nil
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Tests that serve the HTTP handler without Server.Start still need the
	// WebSocket hub running so upgrade and realtime fan-out work. Production
	// Start owns this step for real processes.
	if assembled.components.websocket != nil {
		if err := assembled.components.websocket.Start(ctx); err != nil {
			_ = node.Close()
			return nil, fmt.Errorf("start WebSocket for testing: %w", err)
		}
	}
	return &TestingRuntime{
		Server:      node,
		Platform:    assembled.platform,
		Application: assembled.application,
		API:         assembled.transport,
		Health:      assembled.readiness,
	}, nil
}
