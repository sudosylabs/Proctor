// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	vfspkg "github.com/sudosylabs/proctor/packages/vfs"
	"github.com/sudosylabs/proctor/server/app/api"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/mlog"
	"github.com/sudosylabs/proctor/server/platform"
	"github.com/sudosylabs/proctor/server/store"
)

type serverOptions struct {
	configPath  string
	configStore *config.Store
	logger      *mlog.Logger
	store       store.Store
	cache       platform.Cache
	cluster     platform.Cluster
	mailer      platform.Mailer
	vfs         vfspkg.FileSystem
	buildInfo   api.BuildInfo
}

type Option func(*serverOptions) error

func WithConfigPath(path string) Option {
	return func(options *serverOptions) error {
		if path == "" {
			return errors.New("configuration path is empty")
		}
		options.configPath = path
		return nil
	}
}

func WithConfigStore(store *config.Store) Option {
	return func(options *serverOptions) error {
		if store == nil {
			return errors.New("configuration store is nil")
		}
		options.configStore = store
		return nil
	}
}

func WithLogger(logger *mlog.Logger) Option {
	return func(options *serverOptions) error {
		if logger == nil {
			return errors.New("logger is nil")
		}
		options.logger = logger
		return nil
	}
}

func WithStore(persistence store.Store) Option {
	return func(options *serverOptions) error {
		if persistence == nil {
			return errors.New("store is nil")
		}
		options.store = persistence
		return nil
	}
}

func WithCache(cache platform.Cache) Option {
	return func(options *serverOptions) error {
		if cache == nil {
			return errors.New("cache is nil")
		}
		options.cache = cache
		return nil
	}
}

func WithCluster(cluster platform.Cluster) Option {
	return func(options *serverOptions) error {
		if cluster == nil {
			return errors.New("cluster transport is nil")
		}
		options.cluster = cluster
		return nil
	}
}

func WithMailer(mailer platform.Mailer) Option {
	return func(options *serverOptions) error {
		if mailer == nil {
			return errors.New("mailer is nil")
		}
		options.mailer = mailer
		return nil
	}
}

func WithVFS(filesystem vfspkg.FileSystem) Option {
	return func(options *serverOptions) error {
		if filesystem == nil {
			return errors.New("VFS is nil")
		}
		options.vfs = filesystem
		return nil
	}
}

func WithBuildInfo(buildInfo api.BuildInfo) Option {
	return func(options *serverOptions) error {
		options.buildInfo = buildInfo
		return nil
	}
}

type Server struct {
	platform *platform.Service
	app      *App
	api      *api.API
	health   *Health

	lifecycleMu sync.Mutex
	started     bool
	closed      bool
	runCancel   context.CancelFunc
	runDone     chan struct{}

	httpMu sync.Mutex
	http   *http.Server

	platformCloseOnce sync.Once
	platformCloseErr  error
}

func NewServer(ctx context.Context, options ...Option) (*Server, error) {
	settings := serverOptions{buildInfo: CurrentBuildInfo()}
	for _, option := range options {
		if err := option(&settings); err != nil {
			return nil, fmt.Errorf("apply server option: %w", err)
		}
	}
	if settings.configStore != nil && settings.configPath != "" {
		return nil, errors.New("configuration path and store cannot both be provided")
	}

	configStore := settings.configStore
	if configStore == nil {
		var backing config.BackingStore
		if settings.configPath == "" {
			backing = config.NewMemoryStore(nil)
		} else {
			fileStore, err := config.NewFileStore(settings.configPath)
			if err != nil {
				return nil, err
			}
			backing = fileStore
		}
		var err error
		configStore, err = config.NewStore(ctx, backing, config.StoreOptions{})
		if err != nil {
			return nil, err
		}
	}

	applicationPlatform, err := platform.New(platform.ServiceConfig{
		Context:     ctx,
		ConfigStore: configStore,
		Logger:      settings.logger,
		Store:       settings.store,
		Cache:       settings.cache,
		Cluster:     settings.cluster,
		Mailer:      settings.mailer,
		VFS:         settings.vfs,
	})
	if err != nil {
		_ = configStore.Close()
		if settings.logger != nil {
			_ = settings.logger.Shutdown()
		}
		return nil, err
	}
	application, err := New(applicationPlatform)
	if err != nil {
		_ = applicationPlatform.Close()
		return nil, fmt.Errorf("construct application: %w", err)
	}
	health := &Health{}
	httpAPI, err := api.New(api.Options{
		Logger:         applicationPlatform.Log(),
		Health:         health,
		Authentication: application,
		BuildInfo:      settings.buildInfo,
		MaxBodyBytes:   applicationPlatform.Config().Server.MaxBodyBytes,
	})
	if err != nil {
		_ = applicationPlatform.Close()
		return nil, fmt.Errorf("construct HTTP API: %w", err)
	}

	return &Server{
		platform: applicationPlatform,
		app:      application,
		api:      httpAPI,
		health:   health,
	}, nil
}

func (s *Server) Platform() *platform.Service {
	return s.platform
}

func (s *Server) App() *App {
	return s.app
}

func (s *Server) API() *api.API {
	return s.api
}

func (s *Server) Handler() http.Handler {
	return s.api
}

func (s *Server) Health() *Health {
	return s.health
}

func (s *Server) Start(ctx context.Context) (resultErr error) {
	s.lifecycleMu.Lock()
	if s.closed {
		s.lifecycleMu.Unlock()
		return errors.New("server is closed")
	}
	if s.started {
		s.lifecycleMu.Unlock()
		return errors.New("server has already been started")
	}
	runCtx, runCancel := context.WithCancel(ctx)
	s.started = true
	s.runCancel = runCancel
	s.runDone = make(chan struct{})
	runDone := s.runDone
	s.lifecycleMu.Unlock()

	defer func() {
		runCancel()
		resultErr = errors.Join(resultErr, s.closePlatform())
		s.lifecycleMu.Lock()
		s.closed = true
		close(runDone)
		s.lifecycleMu.Unlock()
	}()

	if err := s.platform.Start(runCtx); err != nil {
		if errors.Is(err, context.Canceled) && errors.Is(runCtx.Err(), context.Canceled) {
			return nil
		}
		return fmt.Errorf("start platform: %w", err)
	}

	cfg := s.platform.Config()
	listener, err := net.Listen("tcp", cfg.Server.ListenAddress)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.Server.ListenAddress, err)
	}

	httpServer := &http.Server{
		Handler:           s.api,
		ErrorLog:          s.platform.Log().StdLogger(slog.LevelError),
		ReadHeaderTimeout: cfg.Server.ReadHeaderTimeout.Duration,
		ReadTimeout:       cfg.Server.ReadTimeout.Duration,
		WriteTimeout:      cfg.Server.WriteTimeout.Duration,
		IdleTimeout:       cfg.Server.IdleTimeout.Duration,
		MaxHeaderBytes:    cfg.Server.MaxHeaderBytes,
	}
	s.httpMu.Lock()
	s.http = httpServer
	s.httpMu.Unlock()

	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- httpServer.Serve(listener)
	}()

	s.health.SetReady(true)
	s.platform.Log().InfoContext(
		runCtx,
		"server started",
		mlog.String("listen_address", listener.Addr().String()),
		mlog.String("public_url", cfg.Server.PublicURL),
		mlog.String("version", Version),
	)

	select {
	case serveErr := <-serveErrors:
		s.health.SetReady(false)
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP: %w", serveErr)
		}
		return nil
	case <-runCtx.Done():
		s.health.SetReady(false)
		s.platform.Log().Info("server shutdown started")
	}

	if err := s.shutdownHTTP(cfg.Server.ShutdownTimeout.Duration); err != nil {
		return err
	}
	select {
	case serveErr := <-serveErrors:
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP during shutdown: %w", serveErr)
		}
	case <-time.After(cfg.Server.ShutdownTimeout.Duration):
		return errors.New("HTTP server did not stop after graceful shutdown")
	}
	s.platform.Log().Info("server stopped")
	return nil
}

func (s *Server) Close() error {
	s.lifecycleMu.Lock()
	if s.closed {
		done := s.runDone
		s.lifecycleMu.Unlock()
		if done != nil {
			<-done
		}
		return s.closePlatform()
	}
	s.closed = true
	cancel := s.runCancel
	done := s.runDone
	started := s.started
	s.lifecycleMu.Unlock()

	s.health.SetReady(false)
	if !started {
		return s.closePlatform()
	}
	cancel()
	<-done
	return s.closePlatform()
}

func (s *Server) shutdownHTTP(timeout time.Duration) error {
	s.httpMu.Lock()
	httpServer := s.http
	s.httpMu.Unlock()
	if httpServer == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		return errors.Join(fmt.Errorf("graceful HTTP shutdown: %w", err), httpServer.Close())
	}
	return nil
}

func (s *Server) closePlatform() error {
	s.platformCloseOnce.Do(func() {
		s.platformCloseErr = s.platform.Close()
	})
	return s.platformCloseErr
}
