// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

// Package platform owns infrastructure shared by the Proctor application.
package platform

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"

	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/mlog"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/sqlstore"
)

type ServiceConfig struct {
	Context     context.Context
	ConfigStore *config.Store
	Logger      *mlog.Logger
	Store       store.Store
}

type Service struct {
	configStore    *config.Store
	logger         *mlog.Logger
	store          store.Store
	configListener string
	shutdownOnce   sync.Once
	shutdownErr    error
}

func New(serviceConfig ServiceConfig) (*Service, error) {
	if serviceConfig.ConfigStore == nil {
		return nil, errors.New("configuration store is required")
	}

	logger := serviceConfig.Logger
	if logger == nil {
		var err error
		logger, err = mlog.New()
		if err != nil {
			return nil, fmt.Errorf("create logger: %w", err)
		}
	}
	if err := configureLogger(logger, serviceConfig.ConfigStore.Get().Log); err != nil &&
		!errors.Is(err, mlog.ErrConfigurationLocked) {
		if serviceConfig.Logger == nil {
			_ = logger.Shutdown()
		}
		return nil, fmt.Errorf("configure logger: %w", err)
	}

	constructionCtx := serviceConfig.Context
	if constructionCtx == nil {
		constructionCtx = context.Background()
	}
	persistence := serviceConfig.Store
	if persistence == nil {
		var err error
		persistence, err = sqlstore.New(
			constructionCtx,
			sqlstore.SettingsFromConfig(serviceConfig.ConfigStore.Get().Database),
		)
		if err != nil {
			if serviceConfig.Logger == nil {
				_ = logger.Shutdown()
			}
			return nil, fmt.Errorf("open database: %w", err)
		}
	}
	if err := persistence.ValidateSchema(constructionCtx); err != nil {
		_ = persistence.Close()
		if serviceConfig.Logger == nil {
			_ = logger.Shutdown()
		}
		return nil, fmt.Errorf("validate database schema: %w", err)
	}

	service := &Service{
		configStore: serviceConfig.ConfigStore,
		logger:      logger,
		store:       persistence,
	}
	service.configListener = service.configStore.AddListener(func(old, current config.Config) {
		if !logConfigurationChanged(old, current) {
			return
		}
		if err := configureLogger(service.logger, current.Log); err != nil &&
			!errors.Is(err, mlog.ErrConfigurationLocked) {
			service.logger.Error("failed to reconfigure logger", mlog.Err(err))
		}
	})
	service.logger.Info(
		"platform initialized",
		mlog.String("go_version", runtime.Version()),
		mlog.String("config_source", service.configStore.Describe()),
	)
	return service, nil
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

func (s *Service) Close() error {
	s.shutdownOnce.Do(func() {
		s.configStore.RemoveListener(s.configListener)
		s.shutdownErr = errors.Join(
			s.store.Close(),
			s.logger.Flush(),
			s.logger.Shutdown(),
			s.configStore.Close(),
		)
	})
	return s.shutdownErr
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
