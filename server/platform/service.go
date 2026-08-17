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
	"github.com/sudosylabs/proctor/server/mlog"
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
	Logger                 *mlog.Logger
	Persistence            store.Store
	Cache                  Cache
	Cluster                Cluster
	Mailer                 Mailer
	VFS                    vfspkg.FileSystem
	ExternalAuthentication *externalauth.Registry
}

type Service struct {
	configStore            *config.Store
	logger                 *mlog.Logger
	store                  store.Store
	cache                  Cache
	cluster                Cluster
	mailer                 Mailer
	vfs                    vfspkg.FileSystem
	externalAuthentication *externalauth.Registry
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
		!errors.Is(err, mlog.ErrConfigurationLocked) {
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
	}
	checkCtx, cancelCheck := context.WithTimeout(constructionCtx, 15*time.Second)
	defer cancelCheck()
	if err := service.CheckDependencies(checkCtx); err != nil {
		return nil, fmt.Errorf("check platform dependencies: %w", err)
	}
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
					mlog.Err(err),
				)
			}
		}
		if logConfigurationChanged(old, current) {
			if err := configureLogger(service.logger, current.Log); err != nil &&
				!errors.Is(err, mlog.ErrConfigurationLocked) {
				service.logger.Error(
					"failed to reconfigure logger",
					mlog.Err(err),
				)
			}
		}
	})
	if service.configListener == "" {
		return nil, errors.New("register dynamic configuration listener: configuration store is closed")
	}
	service.logger.Info(
		"platform initialized",
		mlog.String("go_version", runtime.Version()),
		mlog.String("config_source", service.configStore.Describe()),
		mlog.String("node_id", service.cluster.NodeID()),
		mlog.String("cluster_backend", snapshot.Cluster.Backend),
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
	var loggerErr error
	if resources.Logger != nil {
		loggerErr = resources.Logger.Shutdown()
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
		loggerErr,
		configurationErr,
	)
}

func (s *Service) Start(ctx context.Context) error {
	if err := s.cluster.Start(ctx); err != nil {
		return fmt.Errorf("start cluster transport: %w", err)
	}
	s.logger.Info("cluster transport started", mlog.String("node_id", s.cluster.NodeID()))
	return nil
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
			s.logger.Flush(),
			s.logger.Shutdown(),
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
	)
}

func configureLogger(logger *mlog.Logger, settings config.Log) error {
	targets := make([]mlog.Target, 0, len(settings.Targets))
	for _, target := range settings.Targets {
		targets = append(targets, mlog.Target{
			Name:      target.Name,
			Type:      target.Type,
			Level:     target.Level,
			Format:    target.Format,
			File:      target.File,
			AddSource: true,
		})
	}
	return logger.Configure(mlog.Config{
		MaxFieldBytes: settings.MaxFieldBytes,
		Targets:       targets,
	})
}

func logConfigurationChanged(old, current config.Config) bool {
	for _, change := range config.Diff(old, current) {
		if change.Path == "log" || strings.HasPrefix(change.Path, "log.") {
			return true
		}
	}
	return false
}
