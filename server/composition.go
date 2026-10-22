// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package server

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"

	"time"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/app/mail"
	"github.com/sudosylabs/proctor/server/app/realtime"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/filecontent"
	"github.com/sudosylabs/proctor/server/httpapi"
	"github.com/sudosylabs/proctor/server/localization"
	"github.com/sudosylabs/proctor/server/logging"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/websocket"
	"github.com/sudosylabs/proctor/server/webui"
)

type compositionInput struct {
	serviceEnvironment ServiceEnvironment
	configPath         string
	// requireCurrentSchema replaces startup migration with a strict schema
	// version check for offline host commands.
	requireCurrentSchema bool
	overrides            TestingOverrides
	allowMissingJobs     bool
	constructors         *consumerConstructors
}

var errDurableJobRuntimeUnavailable = errors.New("application Job runtime is unavailable")

type composedWebSocket interface {
	runtimeComponent
	httpapi.WebSocketTransport
	realtime.Sink
}

var (
	_ httpapi.Application                            = (*app.App)(nil)
	_ httpapi.AcademicUnitApplication                = (*app.App)(nil)
	_ httpapi.InstitutionApplication                 = (*app.App)(nil)
	_ httpapi.ProgrammeApplication                   = (*app.App)(nil)
	_ httpapi.ProgrammeLevelApplication              = (*app.App)(nil)
	_ httpapi.AcademicPeriodApplication              = (*app.App)(nil)
	_ httpapi.ClassApplication                       = (*app.App)(nil)
	_ httpapi.AffiliationApplication                 = (*app.App)(nil)
	_ httpapi.AcademicUnitMemberApplication          = (*app.App)(nil)
	_ httpapi.ClassMemberApplication                 = (*app.App)(nil)
	_ httpapi.InvitationApplication                  = (*app.App)(nil)
	_ httpapi.BrowserInvitationApplication           = (*app.App)(nil)
	_ httpapi.OnboardingImportApplication            = (*app.App)(nil)
	_ httpapi.StudentProgressionApplication          = (*app.App)(nil)
	_ httpapi.AcademicAdministrationBatchApplication = (*app.App)(nil)
	_ httpapi.UserProfileApplication                 = (*app.App)(nil)
	_ httpapi.UserSettingsApplication                = (*app.App)(nil)
	_ httpapi.AccountStateApplication                = (*app.App)(nil)
	_ httpapi.SessionAdministrationApplication       = (*app.App)(nil)
	_ httpapi.RoleApplication                        = (*app.App)(nil)
	_ httpapi.RoleBindingApplication                 = (*app.App)(nil)
	_ httpapi.AuditListingApplication                = (*app.App)(nil)
	_ httpapi.BootstrapApplication                   = (*app.App)(nil)
	_ httpapi.AccessPolicyApplication                = (*app.App)(nil)
	_ httpapi.MailApplication                        = (*app.App)(nil)
)

// consumerConstructors is a package-private failure seam used by composition
// tests. Production and NewForTesting always use the same default constructor
// set; no constructor substitution is exported.
type consumerConstructors struct {
	fileContent    func(constructionCapabilities) (app.FileContent, error)
	dependencies   func(constructionCapabilities, app.FileContent) (app.Dependencies, error)
	application    func(app.Dependencies) (*app.App, error)
	realtime       func(borrowedCluster) (realtime.ClusterFanout, error)
	attachRealtime func(*app.App, realtime.ClusterFanout) error
	websocket      func(*app.App, runtimeLogger, string, string) (composedWebSocket, error)
	attachSink     func(*app.App, realtime.Sink) error
	http           func(httpapi.Options) (runtimeTransport, http.Handler, error)
	webapp         func(webui.Options) (http.Handler, error)
	jobs           func(*app.App) runtimeComponent
}

func defaultConsumerConstructors(
	snapshot config.Config,
	overrideWebappFiles fs.FS,
	overrideDesktopBuildCatalog []model.DesktopBuildTuple,
	environment ServiceEnvironment,
) (consumerConstructors, error) {
	catalogFiles, err := runtimeAssetDirectory("i18n")
	if err != nil {
		return consumerConstructors{}, fmt.Errorf("open localization catalogs: %w", err)
	}
	localizer, err := localization.New(catalogFiles, snapshot.Localization.DefaultLocale)
	if err != nil {
		return consumerConstructors{}, fmt.Errorf("construct localizer: %w", err)
	}
	templateFiles, err := runtimeAssetDirectory("templates")
	if err != nil {
		return consumerConstructors{}, fmt.Errorf("open mail templates: %w", err)
	}
	mailRenderer, err := mail.NewRendererWithPolicy(templateFiles, localizer, mail.RendererPolicy{
		AllowLoopbackHTTPDevelopment: explicitLoopbackHTTPDevelopment(snapshot.Server.PublicURL),
	})
	if err != nil {
		return consumerConstructors{}, fmt.Errorf("construct mail renderer: %w", err)
	}
	webappFiles := overrideWebappFiles
	if webappFiles == nil {
		webappFiles = os.DirFS(snapshot.Server.WebappDirectory)
	}
	return consumerConstructors{
		fileContent: func(capabilities constructionCapabilities) (app.FileContent, error) {
			var recorder filecontent.WorkRecorder
			if capabilities.metrics != nil && capabilities.metrics.Enabled() {
				recorder = workMetricsRecorder{metrics: capabilities.metrics, pool: "file_content"}
			}
			return filecontent.New(capabilities.filesystem,
				filecontent.Policy{MaximumConcurrentOperations: snapshot.FileContent.MaximumConcurrentOperations}, recorder)
		},
		dependencies: func(capabilities constructionCapabilities, content app.FileContent) (app.Dependencies, error) {
			dependencies, err := applicationDependencies(capabilities, snapshot, content, mailRenderer)
			dependencies.SkipDesktopCompatibility = environment == ServiceEnvironmentDev
			if err == nil && overrideDesktopBuildCatalog != nil {
				dependencies.DesktopBuildCatalog = append([]model.DesktopBuildTuple(nil), overrideDesktopBuildCatalog...)
			}
			return dependencies, err
		},
		application: app.New,
		realtime: func(cluster borrowedCluster) (realtime.ClusterFanout, error) {
			return newRealtimeClusterAdapter(cluster)
		},
		attachRealtime: func(application *app.App, fanout realtime.ClusterFanout) error {
			return application.AttachRealtimeClusterFanout(fanout)
		},
		websocket: func(application *app.App, logger runtimeLogger, publicURL, nodeID string) (composedWebSocket, error) {
			return websocket.NewHub(application, websocketLogger{log: logger}, publicURL, nodeID, localizer, snapshot.Server.ShutdownTimeout.Duration)
		},
		attachSink: func(application *app.App, sink realtime.Sink) error {
			return application.AttachRealtimeSink(sink)
		},
		http: func(options httpapi.Options) (runtimeTransport, http.Handler, error) {
			options.Localizer = localizer
			transport, err := httpapi.New(options)
			return transport, transport, err
		},
		webapp: func(options webui.Options) (http.Handler, error) {
			return webui.New(webappFiles, options)
		},
		jobs: func(application *app.App) runtimeComponent {
			if runner := application.Jobs(); runner != nil {
				return runner
			}
			return nil
		},
	}, nil
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
	if !input.serviceEnvironment.valid() {
		return nil, errors.New("invalid service environment")
	}
	// 1. Acquire and decorate infrastructure under one temporary owner.
	infrastructure, err := openRuntimeInfrastructure(ctx, input.configPath, input.overrides, schemaPolicyFor(input.requireCurrentSchema))
	if err != nil {
		return nil, fmt.Errorf("acquire infrastructure: %w", err)
	}

	// 2. Atomically transfer ownership to Platform and capture one immutable
	// construction snapshot plus a lifecycle-free capability projection.
	applicationPlatform, snapshot, capabilities, err := infrastructure.acceptPlatform(ctx)
	if err != nil {
		return nil, fmt.Errorf("accept platform ownership: %w", err)
	}
	logStartupInfrastructure(snapshot, capabilities.logger, capabilities.migration)
	capabilities.logger.Info("service environment selected",
		logging.String("environment", string(input.serviceEnvironment)),
		logging.Bool("desktop_compatibility_enforced", input.serviceEnvironment != ServiceEnvironmentDev),
	)

	constructors, constructorsErr := defaultConsumerConstructors(
		snapshot,
		input.overrides.WebappFiles,
		input.overrides.DesktopBuildCatalog,
		input.serviceEnvironment,
	)
	if constructorsErr != nil {
		return nil, compositionRelease{platform: applicationPlatform, metrics: capabilities.metrics}.fail(constructorsErr)
	}
	if input.constructors != nil {
		constructors = *input.constructors
	}
	return composeConsumers(ctx, applicationPlatform, snapshot, capabilities, input, constructors)
}

func composeConsumers(
	ctx context.Context,
	applicationPlatform runtimeComponent,
	snapshot config.Config,
	capabilities constructionCapabilities,
	input compositionInput,
	constructors consumerConstructors,
) (*compositionResult, error) {
	// 3. Construct consumers in dependency order. Every failure releases only
	// the consumers already built, followed by Platform.
	release := compositionRelease{platform: applicationPlatform, metrics: capabilities.metrics}
	content, err := constructors.fileContent(capabilities)
	if err != nil {
		return nil, release.fail(fmt.Errorf("construct file content: %w", err))
	}
	applicationDeps, err := constructors.dependencies(capabilities, content)
	if err != nil {
		return nil, release.fail(fmt.Errorf("project application dependencies: %w", err))
	}
	if capabilities.metrics != nil && capabilities.metrics.Enabled() {
		applicationDeps.JobRecorder = jobMetricsRecorder{metrics: capabilities.metrics}
		applicationDeps.OperationalRecorder = applicationMetricsRecorder{metrics: capabilities.metrics}
		applicationDeps.PasswordWorkRecorder = workMetricsRecorder{metrics: capabilities.metrics, pool: "password"}
	}
	mailMetrics := input.overrides.MailMetrics
	if capabilities.metrics != nil && capabilities.metrics.Enabled() {
		prometheusMetrics := newPrometheusMailRecorder(capabilities.metrics)
		if mailMetrics == nil {
			mailMetrics = prometheusMetrics
		} else {
			mailMetrics = fanoutMailDeliveryRecorder{first: mailMetrics, second: prometheusMetrics}
		}
	}
	if mailMetrics != nil {
		mailDeliveryRecorder, mailMetricsReader := newMailTelemetry(capabilities.logger, mailMetrics)
		applicationDeps.MailDeliveryRecorder = mailDeliveryRecorder
		applicationDeps.MailMetricsReader = mailMetricsReader
	}
	var installations bootstrapStatusStore
	if capabilities.persistence != nil {
		if current := capabilities.persistence.Installation(); current != nil {
			installations = current
		}
	}
	bootstrapOutput := input.overrides.BootstrapSecretWriter
	if bootstrapOutput == nil {
		bootstrapOutput = os.Stderr
	}
	applicationDeps.BootstrapProtection, err = resolveBootstrapProtection(ctx, snapshot, installations, bootstrapOutput)
	if err != nil {
		return nil, release.fail(fmt.Errorf("protect installation bootstrap: %w", err))
	}
	application, err := constructors.application(applicationDeps)
	if err != nil {
		return nil, release.fail(fmt.Errorf("construct application: %w", err))
	}
	clusterFanout, err := constructors.realtime(capabilities.cluster)
	if err != nil {
		return nil, release.fail(fmt.Errorf("construct realtime cluster adapter: %w", err))
	}
	if err := constructors.attachRealtime(application, clusterFanout); err != nil {
		return nil, release.fail(fmt.Errorf("attach realtime cluster fan-out: %w", err))
	}

	// Readiness requires both the lifecycle flag and a live, bounded probe of
	// the mandatory platform dependencies (PostgreSQL, cache, cluster, VFS).
	var dependencyProbe app.DependencyProbe
	if checker, ok := applicationPlatform.(interface{ CheckDependencies(context.Context) error }); ok {
		dependencyProbe = checker.CheckDependencies
	}
	readiness, err := app.NewHealth(app.HealthDependencies{Probe: dependencyProbe, Timeout: app.DefaultReadinessProbeTimeout, Interval: time.Second})
	if err != nil {
		return nil, release.fail(fmt.Errorf("construct readiness: %w", err))
	}
	buildInfo := input.overrides.BuildInfo
	if buildInfo == (httpapi.BuildInfo{}) {
		current := app.CurrentBuildInfo()
		buildInfo = httpapi.BuildInfo{
			Version: current.Version, Commit: current.Commit,
			BuildTime: current.BuildTime, GoVersion: current.GoVersion,
		}
	}
	webSocketHub, err := constructors.websocket(
		application, capabilities.logger,
		snapshot.Server.PublicURL, capabilities.nodeID,
	)
	if err != nil {
		return nil, release.fail(fmt.Errorf("construct WebSocket hub: %w", err))
	}
	release.websocket = webSocketHub.Close
	if attachable, ok := webSocketHub.(interface {
		AttachRecorder(websocket.Recorder) error
	}); ok && capabilities.metrics != nil && capabilities.metrics.Enabled() {
		if err := attachable.AttachRecorder(capabilities.metrics); err != nil {
			return nil, release.fail(fmt.Errorf("attach WebSocket metrics: %w", err))
		}
	}
	if err := constructors.attachSink(application, webSocketHub); err != nil {
		return nil, release.fail(fmt.Errorf("attach realtime sink: %w", err))
	}
	var httpMetrics httpapi.Metrics
	if capabilities.metrics != nil && capabilities.metrics.Enabled() {
		httpMetrics = capabilities.metrics
	}
	trustedProxies, err := config.ParseTrustedProxies(snapshot.Server.TrustedProxies)
	if err != nil {
		return nil, release.fail(fmt.Errorf("parse trusted proxies: %w", err))
	}
	httpTransport, httpAPI, err := constructors.http(httpapi.Options{
		Logger: apiLogger{log: capabilities.logger}, Health: readiness,
		Metrics:     httpMetrics,
		Application: application, AcademicUnits: application, Institutions: application,
		Programmes: application, ProgrammeLevels: application, AcademicPeriods: application,
		Classes: application, Affiliations: application, AcademicUnitMembers: application,
		ClassMembers: application, UserProfiles: application, UserSettings: application, AccountStates: application,
		Invitations:                   application,
		BrowserInvitations:            application,
		OnboardingImports:             application,
		StudentProgressions:           application,
		AcademicAdministrationBatches: application,
		SessionAdministrations:        application, Roles: application, RoleBindings: application,
		AuditListings: application, Bootstrap: application, AccessPolicy: application, BuildInfo: buildInfo,
		Mail:      application,
		PublicURL: snapshot.Server.PublicURL, MaxBodyBytes: snapshot.Server.MaxBodyBytes,
		ReadTimeout: snapshot.Server.ReadTimeout.Duration, WriteTimeout: snapshot.Server.WriteTimeout.Duration,
		TrustedProxies:          trustedProxies,
		RecentAuthenticationTTL: snapshot.Authentication.RecentAuthenticationTTL.Duration,
		NodeID:                  capabilities.nodeID, WebSocket: webSocketHub,
	})
	if err != nil {
		return nil, release.fail(fmt.Errorf("construct HTTP API: %w", err))
	}
	release.transport = httpTransport.Close
	webappOptions := webui.Options{
		BuildVersion: buildInfo.Version,
		BuildCommit:  buildInfo.Commit,
	}
	if problems, ok := httpAPI.(browserRouteProblems); ok {
		webappOptions.NotFound = problems.NotFoundHandler()
		webappOptions.MethodNotAllowed = problems.MethodNotAllowedHandler(http.MethodGet, http.MethodHead)
	}
	webappHandler, err := constructors.webapp(webappOptions)
	if err != nil {
		return nil, release.fail(fmt.Errorf("construct hosted webapp: %w", err))
	}
	rootTransport, err := newRootHTTPTransport(httpTransport, httpAPI, webappHandler, snapshot.Server.StrictTransportSecurity())
	if err != nil {
		return nil, release.fail(fmt.Errorf("compose HTTP transports: %w", err))
	}
	release.transport = rootTransport.Close
	jobRuntime := constructors.jobs(application)
	if jobRuntime == nil && !input.allowMissingJobs {
		return nil, release.fail(fmt.Errorf("require durable Job runtime: %w", errDurableJobRuntimeUnavailable))
	}
	servingLease, err := newServingNodeLeaseRuntime(capabilities.persistence.ServingNodeLease(), capabilities.nodeID)
	if err != nil {
		return nil, release.fail(fmt.Errorf("construct serving node lease: %w", err))
	}

	// 4. Assemble one inert lifecycle owner. No listener, goroutine, readiness,
	// or transport lifecycle operation runs during composition.
	node := &Server{components: runtimeComponents{
		platform: applicationPlatform, reconciler: application, administratorRecovery: application,
		settings: runtimeSettingsFromConfig(snapshot.Server),
		logger:   capabilities.logger, jobs: jobRuntime, servingLease: servingLease, transport: rootTransport,
		websocket: webSocketHub, metrics: capabilities.metrics, readiness: readiness, listen: net.Listen, newHTTP: newHTTPServer,
	}}
	return &compositionResult{
		server: node,
		test: testingProjection{
			application: application, handler: rootTransport,
		},
	}, nil
}

// compositionRelease records the owners a failed composition must release:
// the HTTP transport and WebSocket hub once built, then metrics and Platform.
// A later transport, such as the root HTTP transport that owns the API
// transport, replaces the one it wraps.
type compositionRelease struct {
	platform  runtimeComponent
	metrics   runtimeMetrics
	transport func() error
	websocket func() error
}

// fail joins cause with the release of every owner built so far, closing the
// transport, the WebSocket hub, metrics, and Platform in that order.
func (r compositionRelease) fail(cause error) error {
	var transportErr, websocketErr, metricsErr error
	if r.transport != nil {
		transportErr = r.transport()
	}
	if r.websocket != nil {
		websocketErr = r.websocket()
	}
	if r.metrics != nil {
		metricsErr = r.metrics.Close()
	}
	return errors.Join(cause, transportErr, websocketErr, errors.Join(metricsErr, r.platform.Close()))
}
