// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

// Package platform owns infrastructure shared by the Proctor application.
package platform

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"time"

	vfspkg "github.com/sudosylabs/proctor/packages/vfs"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/logging"
	"github.com/sudosylabs/proctor/server/platform/externalauth"
	"github.com/sudosylabs/proctor/server/store"
)

// OwnedResources contains infrastructure whose lifecycle transfers to
// Platform when Accept is called. Accept closes every non-nil resource on any
// unsuccessful outcome; callers must never close supplied resources after the
// call. Construction may retain explicitly non-owning aliases, but those
// aliases do not carry lifecycle authority.
type OwnedResources struct {
	Configuration          *config.Store
	Logger                 *logging.Logger
	Persistence            store.Store
	Cache                  Cache
	Cluster                Cluster
	Mailer                 Mailer
	VFS                    vfspkg.FileSystem
	ExternalAuthentication *externalauth.Registry
	ExecutionHosts         ExecutionHosts
}

// ExecutionHosts is the lifecycle-only platform view of the configured
// outbound execution-host directory. Application behavior is projected at the
// composition root and does not turn Platform into a service locator.
type ExecutionHosts interface {
	Check(context.Context) error
	Close() error
}

type Service struct {
	configStore            *config.Store
	logger                 *logging.Logger
	store                  store.Store
	cache                  Cache
	cluster                Cluster
	mailer                 Mailer
	vfs                    vfspkg.FileSystem
	externalAuthentication *externalauth.Registry
	executionHosts         ExecutionHosts
	clusterBackend         string
	mailBackend            string
	mailTestTimeout        time.Duration
	configListener         string
	shutdownOnce           sync.Once
	shutdownErr            error
}

// Accept takes ownership of resources at call entry. On success, the returned
// Service owns them and snapshot is the immutable configuration view used for
// this construction attempt. On failure, Accept closes every supplied non-nil
// resource and joins cleanup failures with the primary error.
func Accept(
	ctx context.Context,
	resources OwnedResources,
) (_ *Service, snapshot config.Config, resultErr error) {
	accepted := false
	defer func() {
		if !accepted {
			resultErr = errors.Join(resultErr, closeOwnedResources(resources))
		}
	}()
	if err := validateOwnedResources(resources); err != nil {
		return nil, config.Config{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	// config.Store validates both persisted and effective configuration before
	// making a snapshot observable. Acceptance therefore consumes that
	// established invariant instead of adding a second, unreachable validation
	// branch.
	snapshot = resources.Configuration.Get()
	service, err := newService(ctx, resources, snapshot)
	if err != nil {
		return nil, config.Config{}, err
	}
	accepted = true
	return service, snapshot, nil
}

func validateOwnedResources(resources OwnedResources) error {
	required := []struct {
		name    string
		missing bool
	}{
		{name: "configuration store", missing: resources.Configuration == nil},
		{name: "logger", missing: resources.Logger == nil},
		{name: "persistence store", missing: resources.Persistence == nil},
		{name: "cache", missing: resources.Cache == nil},
		{name: "cluster transport", missing: resources.Cluster == nil},
		{name: "mailer", missing: resources.Mailer == nil},
		{name: "VFS", missing: resources.VFS == nil},
		{name: "external authentication registry", missing: resources.ExternalAuthentication == nil},
	}
	for _, dependency := range required {
		if dependency.missing {
			return fmt.Errorf("%s is required", dependency.name)
		}
	}
	return nil
}

func newService(
	constructionCtx context.Context,
	resources OwnedResources,
	snapshot config.Config,
) (*Service, error) {
	logger := resources.Logger
	if err := configureLogger(logger, snapshot.Log); err != nil &&
		!errors.Is(err, logging.ErrConfigurationLocked) {
		return nil, fmt.Errorf("configure logger: %w", err)
	}

	persistence := resources.Persistence
	if err := persistence.ValidateSchema(constructionCtx); err != nil {
		return nil, fmt.Errorf("validate database schema: %w", err)
	}

	cacheStore := resources.Cache
	mailer := resources.Mailer
	filesystem := resources.VFS
	clusterTransport := resources.Cluster
	externalAuthentication := resources.ExternalAuthentication
	executionHosts := resources.ExecutionHosts
	if err := externalAuthentication.Configure(
		snapshot.Authentication.External,
	); err != nil {
		return nil, err
	}

	service := &Service{
		configStore:            resources.Configuration,
		logger:                 logger,
		store:                  persistence,
		cache:                  cacheStore,
		cluster:                clusterTransport,
		mailer:                 mailer,
		vfs:                    filesystem,
		externalAuthentication: externalAuthentication,
		executionHosts:         executionHosts,
		clusterBackend:         snapshot.Cluster.Backend,
		mailBackend:            snapshot.Mail.Backend,
		mailTestTimeout:        snapshot.Mail.SMTP.Timeout.Duration,
	}
	checkCtx, cancelCheck := context.WithTimeout(constructionCtx, 15*time.Second)
	defer cancelCheck()
	if err := service.CheckDependencies(checkCtx); err != nil {
		return nil, fmt.Errorf("check platform dependencies: %w", err)
	}
	service.logger.Info("database ready", logging.String("backend", "postgresql"))
	cacheFields := []logging.Field{logging.String("backend", snapshot.Cache.Backend)}
	if snapshot.Cache.Backend == "memory" {
		cacheFields = append(cacheFields,
			logging.String("scope", "process_local"),
			logging.Int("max_entries", snapshot.Cache.Memory.MaxEntries),
			logging.Int64("max_bytes", snapshot.Cache.Memory.MaxBytes),
		)
	} else {
		cacheFields = append(cacheFields, logging.String("scope", "installation_shared"))
	}
	service.logger.Info("application cache ready", cacheFields...)
	service.logger.Info("filesystem ready", logging.String("backend", snapshot.VFS.Backend))
	service.logger.Info(
		"execution hosts ready",
		logging.Bool("enabled", snapshot.Execution.Enabled),
		logging.Int("configured_hosts", len(snapshot.Execution.Hosts)),
	)
	service.logger.Info(
		"external authentication ready",
		logging.Int("enabled_providers", enabledExternalAuthenticationProviders(snapshot)),
	)
	service.configListener = service.configStore.AddListener(func(old, current config.Config) {
		if !reflect.DeepEqual(
			old.Authentication.External,
			current.Authentication.External,
		) {
			if err := service.externalAuthentication.Configure(
				current.Authentication.External,
			); err != nil {
				service.logger.Error(
					"failed to reconfigure external authentication providers",
					logging.Err(err),
				)
			}
		}
		if logConfigurationChanged(old, current) {
			if err := configureLogger(service.logger, current.Log); err != nil &&
				!errors.Is(err, logging.ErrConfigurationLocked) {
				service.logger.Error(
					"failed to reconfigure logger",
					logging.Err(err),
				)
			}
		}
	})
	if service.configListener == "" {
		return nil, errors.New("register dynamic configuration listener: configuration store is closed")
	}
	service.logger.Info(
		"platform initialized",
		logging.String("go_version", runtime.Version()),
		logging.String("config_source", service.configStore.Describe()),
		logging.String("node_id", service.cluster.NodeID()),
		logging.String("cluster_backend", snapshot.Cluster.Backend),
	)
	return service, nil
}

func closeOwnedResources(resources OwnedResources) error {
	shutdownTimeout := 15 * time.Second
	if resources.Configuration != nil {
		shutdownTimeout = resources.Configuration.Get().Server.ShutdownTimeout.Duration
	}
	stopCtx, cancelStop := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancelStop()
	var clusterErr error
	if resources.Cluster != nil {
		clusterErr = resources.Cluster.Stop(stopCtx)
	}
	var vfsErr error
	if closer, ok := resources.VFS.(interface{ Close() error }); ok {
		vfsErr = closer.Close()
	}
	var mailerErr error
	if resources.Mailer != nil {
		mailerErr = resources.Mailer.Close()
	}
	var cacheErr error
	if resources.Cache != nil {
		cacheErr = resources.Cache.Close()
	}
	var persistenceErr error
	if resources.Persistence != nil {
		persistenceErr = resources.Persistence.Close()
	}
	var executionErr error
	if resources.ExecutionHosts != nil {
		executionErr = resources.ExecutionHosts.Close()
	}
	var loggerErr error
	if resources.Logger != nil {
		loggerErr = resources.Logger.Shutdown(stopCtx)
	}
	var configurationErr error
	if resources.Configuration != nil {
		configurationErr = resources.Configuration.Close()
	}
	return errors.Join(
		clusterErr,
		vfsErr,
		mailerErr,
		cacheErr,
		persistenceErr,
		executionErr,
		loggerErr,
		configurationErr,
	)
}

func (s *Service) Start(ctx context.Context) error {
	if err := s.cluster.Start(ctx); err != nil {
		return fmt.Errorf("start cluster transport: %w", err)
	}
	s.logger.Info(
		"cluster transport started",
		logging.String("node_id", s.cluster.NodeID()),
		logging.String("backend", s.clusterBackend),
	)
	if !s.mailer.Enabled() {
		s.logger.Info("mail service disabled", logging.String("backend", s.mailBackend))
		return nil
	}
	testCtx, cancelTest := context.WithTimeout(ctx, s.mailTestTimeout)
	defer cancelTest()
	if err := s.mailer.Test(testCtx); err != nil {
		s.logger.Warn(
			"mail connection test failed; mail delivery may be unavailable",
			logging.String("backend", s.mailBackend),
			logging.String("reason", mailTestFailureReason(err)),
		)
		return nil
	}
	s.logger.Info("mail service ready", logging.String("backend", s.mailBackend))
	return nil
}

func mailTestFailureReason(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	default:
		return "connection_test_failed"
	}
}

func (s *Service) CheckDependencies(ctx context.Context) error {
	if err := s.store.Ping(ctx); err != nil {
		return fmt.Errorf("database: %w", err)
	}
	if err := s.cache.Ping(ctx); err != nil {
		return fmt.Errorf("cache: %w", err)
	}
	if err := s.cluster.Ping(ctx); err != nil {
		return fmt.Errorf("cluster: %w", err)
	}
	if err := checkVFS(ctx, s.vfs); err != nil {
		return fmt.Errorf("vfs: %w", err)
	}
	if s.executionHosts != nil {
		if err := s.executionHosts.Check(ctx); err != nil {
			return fmt.Errorf("execution hosts: %w", err)
		}
	}
	return nil
}

func (s *Service) Close() error {
	s.shutdownOnce.Do(func() {
		s.configStore.RemoveListener(s.configListener)
		stopCtx, cancelStop := context.WithTimeout(
			context.Background(),
			s.configStore.Get().Server.ShutdownTimeout.Duration,
		)
		defer cancelStop()
		s.shutdownErr = errors.Join(
			s.cluster.Stop(stopCtx),
			s.closeInfrastructure(),
			s.store.Close(),
			s.logger.Shutdown(stopCtx),
			s.configStore.Close(),
		)
	})
	return s.shutdownErr
}

func (s *Service) closeInfrastructure() error {
	var vfsErr error
	if closer, ok := s.vfs.(interface{ Close() error }); ok {
		vfsErr = closer.Close()
	}
	return errors.Join(
		vfsErr,
		s.mailer.Close(),
		s.cache.Close(),
		closeExecutionHosts(s.executionHosts),
	)
}

func closeExecutionHosts(hosts ExecutionHosts) error {
	if hosts == nil {
		return nil
	}
	return hosts.Close()
}

func configureLogger(logger *logging.Logger, settings config.Log) error {
	targets := make([]logging.Target, 0, len(settings.Targets))
	for _, target := range settings.Targets {
		targets = append(targets, logging.Target{
			Name: target.Name, Type: target.Type, Level: target.Level, Format: target.Format,
			File: target.File, AddSource: true, QueueSize: target.QueueSize,
			MaxSizeMB: target.MaxSizeMB, MaxAgeDays: target.MaxAgeDays,
			MaxBackups: target.MaxBackups, Compress: target.Compress,
		})
	}
	return logger.Configure(logging.Config{
		MaxFieldBytes: settings.MaxFieldBytes, QueueSize: settings.QueueSize,
		EnqueueTimeout: settings.EnqueueTimeout.Duration, FlushTimeout: settings.FlushTimeout.Duration,
		ShutdownTimeout: settings.ShutdownTimeout.Duration, Targets: targets,
	})
}

func logConfigurationChanged(old, current config.Config) bool {
	for _, change := range config.Diff(old, current) {
		if change.Path == "Log" || strings.HasPrefix(change.Path, "Log.") {
			return true
		}
	}
	return false
}

func enabledExternalAuthenticationProviders(settings config.Config) int {
	providers := 0
	for _, provider := range settings.Authentication.External.Providers {
		if provider.Enabled {
			providers++
		}
	}
	return providers
}
