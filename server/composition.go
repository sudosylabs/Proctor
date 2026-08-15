// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/app/api"
	apprealtime "github.com/sudosylabs/proctor/server/app/realtime"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/filecontent"
	"github.com/sudosylabs/proctor/server/websocket"
)

type compositionInput struct {
	configPath       string
	overrides        TestingOverrides
	allowMissingJobs bool
	constructors     *consumerConstructors
}

var errDurableJobRuntimeUnavailable = errors.New("application Job runtime is unavailable")

type composedWebSocket interface {
	runtimeWebSocket
	api.WebSocketTransport
	apprealtime.Sink
}

// consumerConstructors is a package-private failure seam used by composition
// tests. Production and NewForTesting always use the same default constructor
// set; no constructor substitution is exported.
type consumerConstructors struct {
	fileContent    func(constructionCapabilities) (app.FileContent, error)
	dependencies   func(constructionCapabilities, app.FileContent) (app.Dependencies, error)
	application    func(app.Dependencies) (*app.App, error)
	realtime       func(borrowedCluster) (apprealtime.ClusterFanout, error)
	attachRealtime func(*app.App, apprealtime.ClusterFanout) error
	websocket      func(*app.App, runtimeLogger, string, string) (composedWebSocket, error)
	attachSink     func(*app.App, apprealtime.Sink) error
	http           func(api.Options) (runtimeTransport, http.Handler, error)
	jobs           func(*app.App) runtimeJobs
}

func defaultConsumerConstructors(snapshot config.Config) consumerConstructors {
	return consumerConstructors{
		fileContent: func(capabilities constructionCapabilities) (app.FileContent, error) {
			return filecontent.New(capabilities.filesystem)
		},
		dependencies: func(capabilities constructionCapabilities, content app.FileContent) (app.Dependencies, error) {
			return applicationDependencies(capabilities, snapshot, content)
		},
		application: app.New,
		realtime: func(cluster borrowedCluster) (apprealtime.ClusterFanout, error) {
			return newRealtimeClusterAdapter(cluster)
		},
		attachRealtime: func(application *app.App, fanout apprealtime.ClusterFanout) error {
			return application.AttachRealtimeClusterFanout(fanout)
		},
		websocket: func(application *app.App, logger runtimeLogger, publicURL, nodeID string) (composedWebSocket, error) {
			return websocket.NewHub(application, websocketLogger{log: logger}, publicURL, nodeID)
		},
		attachSink: func(application *app.App, sink apprealtime.Sink) error {
			return application.AttachRealtimeSink(sink)
		},
		http: func(options api.Options) (runtimeTransport, http.Handler, error) {
			transport, err := api.New(options)
			return transport, transport, err
		},
		jobs: func(application *app.App) runtimeJobs {
			if runner := application.Jobs(); runner != nil {
				return runner
			}
			return nil
		},
	}
}

// testingProjection borrows behavioral handles from the completed graph. It
// owns nothing and is discarded for production construction.
type testingProjection struct {
	application *app.App
	handler     http.Handler
}

type compositionResult struct {
	server *Server
	test   testingProjection
}

// composeNode is the sole construction recipe for production and tests. Each
// phase completes before the next becomes observable; construction remains
// inert and transfers all lifecycle authority into the returned Server.
func composeNode(ctx context.Context, input compositionInput) (*compositionResult, error) {
	// 1. Acquire and decorate infrastructure under one temporary owner.
	infrastructure, err := openRuntimeInfrastructure(ctx, input.configPath, input.overrides)
	if err != nil {
		return nil, fmt.Errorf("acquire infrastructure: %w", err)
	}

	// 2. Atomically transfer ownership to Platform and capture one immutable
	// construction snapshot plus a lifecycle-free capability projection.
	applicationPlatform, snapshot, capabilities, err := infrastructure.acceptPlatform(ctx)
	if err != nil {
		return nil, fmt.Errorf("accept platform ownership: %w", err)
	}

	constructors := defaultConsumerConstructors(snapshot)
	if input.constructors != nil {
		constructors = *input.constructors
	}
	return composeConsumers(applicationPlatform, snapshot, capabilities, input, constructors)
}

func composeConsumers(
	applicationPlatform runtimePlatform,
	snapshot config.Config,
	capabilities constructionCapabilities,
	input compositionInput,
	constructors consumerConstructors,
) (*compositionResult, error) {
	// 3. Construct consumers in dependency order. Every failure explicitly
	// releases only the consumers already built, followed by Platform.
	content, err := constructors.fileContent(capabilities)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("construct file content: %w", err), applicationPlatform.Close())
	}
	applicationDeps, err := constructors.dependencies(capabilities, content)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("project application dependencies: %w", err), applicationPlatform.Close())
	}
	application, err := constructors.application(applicationDeps)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("construct application: %w", err), applicationPlatform.Close())
	}
	clusterFanout, err := constructors.realtime(capabilities.cluster)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("construct realtime cluster adapter: %w", err), applicationPlatform.Close())
	}
	if err := constructors.attachRealtime(application, clusterFanout); err != nil {
		return nil, errors.Join(fmt.Errorf("attach realtime cluster fan-out: %w", err), applicationPlatform.Close())
	}

	readiness := &app.Health{}
	buildInfo := input.overrides.BuildInfo
	if buildInfo == (api.BuildInfo{}) {
		current := app.CurrentBuildInfo()
		buildInfo = api.BuildInfo{
			Version: current.Version, Commit: current.Commit,
			BuildTime: current.BuildTime, GoVersion: current.GoVersion,
		}
	}
	webSocketHub, err := constructors.websocket(
		application, capabilities.logger,
		snapshot.Server.PublicURL, capabilities.nodeID,
	)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("construct WebSocket hub: %w", err), applicationPlatform.Close())
	}
	if err := constructors.attachSink(application, webSocketHub); err != nil {
		return nil, errors.Join(
			fmt.Errorf("attach realtime sink: %w", err),
			webSocketHub.Close(), applicationPlatform.Close(),
		)
	}
	httpTransport, httpAPI, err := constructors.http(api.Options{
		Logger: apiLogger{log: capabilities.logger}, Health: readiness,
		Application: application, AcademicUnits: application, Institutions: application,
		Programmes: application, ProgrammeLevels: application, AcademicPeriods: application,
		Classes: application, Affiliations: application, AcademicUnitMembers: application,
		ClassMembers: application, UserProfiles: application, UserSettings: application, AccountStates: application,
		SessionAdministrations: application, Roles: application, RoleBindings: application,
		AuditListings: application, Bootstrap: application, BuildInfo: buildInfo,
		PublicURL: snapshot.Server.PublicURL, MaxBodyBytes: snapshot.Server.MaxBodyBytes,
		RecentAuthenticationTTL: snapshot.Authentication.RecentAuthenticationTTL.Duration,
		NodeID:                  capabilities.nodeID, WebSocket: webSocketHub,
	})
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("construct HTTP API: %w", err),
			webSocketHub.Close(), applicationPlatform.Close(),
		)
	}
	jobRuntime := constructors.jobs(application)
	if jobRuntime == nil && !input.allowMissingJobs {
		return nil, errors.Join(
			fmt.Errorf("require durable Job runtime: %w", errDurableJobRuntimeUnavailable),
			httpTransport.Close(), webSocketHub.Close(), applicationPlatform.Close(),
		)
	}

	// 4. Assemble one inert lifecycle owner. No listener, goroutine, readiness,
	// or transport lifecycle operation runs during composition.
	node := &Server{components: runtimeComponents{
		platform: applicationPlatform, settings: runtimeSettingsFromConfig(snapshot.Server),
		logger: capabilities.logger, jobs: jobRuntime, transport: httpTransport,
		websocket: webSocketHub, readiness: readiness, listen: net.Listen, newHTTP: newHTTPServer,
	}}
	return &compositionResult{
		server: node,
		test: testingProjection{
			application: application, handler: httpAPI,
		},
	}, nil
}
