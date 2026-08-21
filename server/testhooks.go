// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"io"
	"net/http"

	vfspkg "github.com/sudosylabs/proctor/packages/vfs"
	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/httpapi"
	"github.com/sudosylabs/proctor/server/logging"
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
	Logger            *logging.Logger
	Persistence       store.Store
	StoreRetry        *retrylayer.Policy
	StoreMetrics      timerlayer.Recorder
	StoreLocalCache   localcachelayer.Cache
	StoreCachePolicy  *localcachelayer.Policy
	StoreCacheMetrics localcachelayer.Recorder
	// MailMetrics captures bounded mail outcome, queue, and health observations.
	// A nil value keeps the production bounded operational telemetry recorder.
	MailMetrics app.MailDeliveryRecorder
	Cache       platform.Cache
	Cluster     platform.Cluster
	Mailer      platform.Mailer
	Filesystem  vfspkg.FileSystem
	// AllowMissingJobs is an explicit lifecycle-only test policy. Production
	// construction always requires durable Job persistence and a Job runtime.
	AllowMissingJobs bool
	// BuildInfo replaces the served build information when any field is set.
	BuildInfo httpapi.BuildInfo
	// BootstrapSecretWriter captures the explicit loopback-development secret.
	// Production writes it directly to the controlling terminal.
	BootstrapSecretWriter io.Writer
}

// TestingRuntime is a non-owning behavioral projection. Server owns lifecycle
// exactly as in production and must be Closed.
type TestingRuntime struct {
	Server      *Server
	Application *app.App
	Handler     http.Handler
}

// NewForTesting constructs the production runtime graph with explicit
// capability overrides and returns the assembled handles alongside the
// lifecycle-owning Server. Construction uses the same private recipe as New,
// so startup, readiness, shutdown, and cleanup behavior is identical to
// production.
func NewForTesting(ctx context.Context, overrides TestingOverrides) (*TestingRuntime, error) {
	if overrides.BootstrapSecretWriter == nil {
		overrides.BootstrapSecretWriter = io.Discard
	}
	result, err := composeNode(ctx, compositionInput{
		overrides:        overrides,
		allowMissingJobs: overrides.AllowMissingJobs,
	})
	if err != nil {
		return nil, err
	}
	return &TestingRuntime{
		Server:      result.server,
		Application: result.test.application,
		Handler:     result.test.handler,
	}, nil
}
