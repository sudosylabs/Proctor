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
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/platform/externalauth"
	"github.com/sudosylabs/proctor/server/store"
)

type ServiceConfig struct {
	Context                context.Context
	ConfigStore            *config.Store
	Logger                 *mlog.Logger
	Store                  store.Store
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

type constructionCleanupPolicy struct {
	logger        bool
	configuration bool
}

func New(serviceConfig ServiceConfig) (*Service, error) {
	if err := validateServiceConfig(serviceConfig); err != nil {
		return nil, err
	}
	return newService(serviceConfig, constructionCleanupPolicy{
		logger:        true,
		configuration: true,
	})
}

func validateServiceConfig(serviceConfig ServiceConfig) error {
	required := []struct {
		name    string
		missing bool
	}{
		{name: "configuration store", missing: serviceConfig.ConfigStore == nil},
		{name: "logger", missing: serviceConfig.Logger == nil},
		{name: "persistence store", missing: serviceConfig.Store == nil},
		{name: "cache", missing: serviceConfig.Cache == nil},
		{name: "cluster transport", missing: serviceConfig.Cluster == nil},
		{name: "mailer", missing: serviceConfig.Mailer == nil},
		{name: "VFS", missing: serviceConfig.VFS == nil},
		{name: "external authentication registry", missing: serviceConfig.ExternalAuthentication == nil},
	}
	for _, dependency := range required {
		if dependency.missing {
			return fmt.Errorf("%s is required", dependency.name)
		}
	}
	return nil
}

func newService(
	serviceConfig ServiceConfig,
	cleanupPolicy constructionCleanupPolicy,
) (*Service, error) {
	logger := serviceConfig.Logger
	if err := configureLogger(logger, serviceConfig.ConfigStore.Get().Log); err != nil &&
		!errors.Is(err, mlog.ErrConfigurationLocked) {
		return nil, errors.Join(
			fmt.Errorf("configure logger: %w", err),
			closeServiceConfig(serviceConfig, cleanupPolicy),
		)
	}

	constructionCtx := serviceConfig.Context
	if constructionCtx == nil {
		constructionCtx = context.Background()
	}
	persistence := serviceConfig.Store
	if err := persistence.ValidateSchema(constructionCtx); err != nil {
		return nil, errors.Join(
			fmt.Errorf("validate database schema: %w", err),
			closeServiceConfig(serviceConfig, cleanupPolicy),
		)
	}

	cacheStore := serviceConfig.Cache
	mailer := serviceConfig.Mailer
	filesystem := serviceConfig.VFS
	clusterTransport := serviceConfig.Cluster
	externalAuthentication := serviceConfig.ExternalAuthentication
	if err := externalAuthentication.Configure(
		serviceConfig.ConfigStore.Get().Authentication.External,
	); err != nil {
		return nil, errors.Join(err, closeServiceConfig(serviceConfig, cleanupPolicy))
	}

	service := &Service{
		configStore:            serviceConfig.ConfigStore,
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
		return nil, errors.Join(
			fmt.Errorf("check platform dependencies: %w", err),
			closeServiceConfig(serviceConfig, cleanupPolicy),
		)
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
	service.logger.Info(
		"platform initialized",
		mlog.String("go_version", runtime.Version()),
		mlog.String("config_source", service.configStore.Describe()),
		mlog.String("node_id", service.cluster.NodeID()),
		mlog.String("cluster_backend", service.Config().Cluster.Backend),
	)
	return service, nil
}

func closeServiceConfig(
	serviceConfig ServiceConfig,
	policy constructionCleanupPolicy,
) error {
	stopCtx, cancelStop := context.WithTimeout(
		context.Background(),
		serviceConfig.ConfigStore.Get().Server.ShutdownTimeout.Duration,
	)
	defer cancelStop()
	var vfsErr error
	if closer, ok := serviceConfig.VFS.(interface{ Close() error }); ok {
		vfsErr = closer.Close()
	}
	var loggerErr error
	if policy.logger {
		loggerErr = serviceConfig.Logger.Shutdown()
	}
	var configErr error
	if policy.configuration {
		configErr = serviceConfig.ConfigStore.Close()
	}
	return errors.Join(
		serviceConfig.Cluster.Stop(stopCtx),
		vfsErr,
		serviceConfig.Mailer.Close(),
		serviceConfig.Cache.Close(),
		serviceConfig.Store.Close(),
		loggerErr,
		configErr,
	)
}

func (s *Service) Config() config.Config {
	return s.configStore.Get()
}

func (s *Service) ConfigStore() *config.Store {
	return s.configStore
}

func (s *Service) Log() *mlog.Logger {
	return s.logger
}

func (s *Service) Store() store.Store {
	return s.store
}

func (s *Service) Cache() Cache {
	return s.cache
}

func (s *Service) Cluster() Cluster {
	return s.cluster
}

func (s *Service) Mailer() Mailer {
	return s.mailer
}

func (s *Service) VFS() vfspkg.FileSystem {
	return s.vfs
}

func (s *Service) ExternalAuthenticationProviders() []model.ExternalAuthenticationProvider {
	return s.externalAuthentication.Descriptors()
}

func (s *Service) ExternalAuthenticationProvider(
	id string,
) (externalauth.Provider, bool) {
	return s.externalAuthentication.Provider(id)
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
	if err := s.mailer.Test(ctx); err != nil {
		return fmt.Errorf("mail: %w", err)
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
			s.Config().Server.ShutdownTimeout.Duration,
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
