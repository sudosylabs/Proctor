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
	"time"

	vfspkg "github.com/sudosylabs/proctor/packages/vfs"
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
	Cache       Cache
	Mailer      Mailer
	VFS         vfspkg.FileSystem
}

type Service struct {
	configStore    *config.Store
	logger         *mlog.Logger
	store          store.Store
	cache          Cache
	mailer         Mailer
	vfs            vfspkg.FileSystem
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

	cacheStore := serviceConfig.Cache
	if cacheStore == nil {
		var err error
		cacheStore, err = newCache(serviceConfig.ConfigStore.Get().Cache)
		if err != nil {
			_ = persistence.Close()
			if serviceConfig.Logger == nil {
				_ = logger.Shutdown()
			}
			return nil, fmt.Errorf("open cache: %w", err)
		}
	}
	mailer := serviceConfig.Mailer
	if mailer == nil {
		var err error
		mailer, err = newMailer(serviceConfig.ConfigStore.Get().Mail)
		if err != nil {
			_ = cacheStore.Close()
			_ = persistence.Close()
			if serviceConfig.Logger == nil {
				_ = logger.Shutdown()
			}
			return nil, fmt.Errorf("open mail transport: %w", err)
		}
	}
	filesystem := serviceConfig.VFS
	if filesystem == nil {
		var err error
		filesystem, err = newVFS(serviceConfig.ConfigStore.Get().VFS)
		if err != nil {
			_ = mailer.Close()
			_ = cacheStore.Close()
			_ = persistence.Close()
			if serviceConfig.Logger == nil {
				_ = logger.Shutdown()
			}
			return nil, fmt.Errorf("open VFS: %w", err)
		}
	}

	service := &Service{
		configStore: serviceConfig.ConfigStore,
		logger:      logger,
		store:       persistence,
		cache:       cacheStore,
		mailer:      mailer,
		vfs:         filesystem,
	}
	checkCtx, cancelCheck := context.WithTimeout(constructionCtx, 15*time.Second)
	defer cancelCheck()
	if err := service.CheckDependencies(checkCtx); err != nil {
		_ = service.closeInfrastructure()
		_ = persistence.Close()
		if serviceConfig.Logger == nil {
			_ = logger.Shutdown()
		}
		return nil, fmt.Errorf("check platform dependencies: %w", err)
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

func (s *Service) Cache() Cache {
	return s.cache
}

func (s *Service) Mailer() Mailer {
	return s.mailer
}

func (s *Service) VFS() vfspkg.FileSystem {
	return s.vfs
}

func (s *Service) CheckDependencies(ctx context.Context) error {
	if err := s.store.Ping(ctx); err != nil {
		return fmt.Errorf("database: %w", err)
	}
	if err := s.cache.Ping(ctx); err != nil {
		return fmt.Errorf("cache: %w", err)
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
		s.shutdownErr = errors.Join(
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
