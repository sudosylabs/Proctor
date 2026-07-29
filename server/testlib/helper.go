// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

// Package testlib constructs the real Proctor application graph for tests.
package testlib

import (
	"context"
	"sync/atomic"
	"testing"

	memoryvfs "github.com/sudosylabs/proctor/packages/vfs/memory"
	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/mlog"
	"github.com/sudosylabs/proctor/server/platform"
	"github.com/sudosylabs/proctor/server/store"
)

type setupOptions struct {
	updateConfig  func(*config.Config)
	serverOptions []app.Option
}

type Option func(*setupOptions)

func WithConfig(update func(*config.Config)) Option {
	return func(options *setupOptions) {
		options.updateConfig = update
	}
}

func WithServerOptions(options ...app.Option) Option {
	return func(settings *setupOptions) {
		settings.serverOptions = append(settings.serverOptions, options...)
	}
}

type Helper struct {
	Server      *app.Server
	App         *app.App
	Platform    *platform.Service
	ConfigStore *config.Store
	Logs        *mlog.Buffer
	Store       *Store
	Cache       *Cache
	Mailer      *Mailer
	VFS         *memoryvfs.FS
}

// Store is the persistence dependency used by ordinary unit tests. SQL store
// tests use the real PostgreSQL adapter and its conformance suites.
type Store struct {
	closed atomic.Bool
}

func (s *Store) Institution() store.InstitutionStore {
	return nil
}

func (s *Store) AcademicUnit() store.AcademicUnitStore {
	return nil
}

func (s *Store) Programme() store.ProgrammeStore {
	return nil
}

func (s *Store) ProgrammeLevel() store.ProgrammeLevelStore {
	return nil
}

func (s *Store) AcademicPeriod() store.AcademicPeriodStore {
	return nil
}

func (s *Store) Class() store.ClassStore {
	return nil
}

func (s *Store) User() store.UserStore {
	return nil
}

func (s *Store) PasswordCredential() store.PasswordCredentialStore {
	return nil
}

func (s *Store) Session() store.SessionStore {
	return nil
}

func (s *Store) SessionCredential() store.SessionCredentialStore {
	return nil
}

func (s *Store) Ping(context.Context) error {
	return nil
}

func (s *Store) GetDBSchemaVersion(context.Context) (int, error) {
	return 0, nil
}

func (s *Store) GetLocalSchemaVersion() (int, error) {
	return 0, nil
}

func (s *Store) ValidateSchema(context.Context) error {
	return nil
}

func (s *Store) Close() error {
	s.closed.Store(true)
	return nil
}

func (s *Store) Closed() bool {
	return s.closed.Load()
}

func Setup(tb testing.TB, options ...Option) *Helper {
	tb.Helper()

	settings := setupOptions{}
	for _, option := range options {
		option(&settings)
	}
	store, err := config.NewStore(
		context.Background(),
		config.NewMemoryStore(nil),
		config.StoreOptions{LookupEnv: func(string) (string, bool) { return "", false }},
	)
	if err != nil {
		tb.Fatalf("create test configuration: %v", err)
	}
	if settings.updateConfig != nil {
		cfg := store.Get()
		settings.updateConfig(&cfg)
		if _, _, err := store.Set(context.Background(), cfg); err != nil {
			tb.Fatalf("update test configuration: %v", err)
		}
	}

	logs := &mlog.Buffer{}
	logger, err := mlog.New()
	if err != nil {
		tb.Fatalf("create test logger: %v", err)
	}
	if err := logger.Configure(mlog.Config{
		MaxFieldBytes: store.Get().Log.MaxFieldBytes,
		Targets: []mlog.Target{{
			Name: "test", Type: "console", Level: "trace", Format: "json", Writer: logs,
		}},
	}); err != nil {
		tb.Fatalf("configure test logger: %v", err)
	}
	logger.LockConfiguration()

	persistence := &Store{}
	cache, err := newCache()
	if err != nil {
		tb.Fatalf("create test cache: %v", err)
	}
	mailer, err := newMailer()
	if err != nil {
		tb.Fatalf("create test mailer: %v", err)
	}
	filesystem := memoryvfs.New()
	serverOptions := append([]app.Option{
		app.WithConfigStore(store),
		app.WithLogger(logger),
		app.WithStore(persistence),
		app.WithCache(cache),
		app.WithMailer(mailer),
		app.WithVFS(filesystem),
	}, settings.serverOptions...)
	server, err := app.NewServer(context.Background(), serverOptions...)
	if err != nil {
		tb.Fatalf("create test server: %v", err)
	}
	helper := &Helper{
		Server:      server,
		App:         server.App(),
		Platform:    server.Platform(),
		ConfigStore: store,
		Logs:        logs,
		Store:       persistence,
		Cache:       cache,
		Mailer:      mailer,
		VFS:         filesystem,
	}
	tb.Cleanup(func() {
		if err := server.Close(); err != nil {
			tb.Errorf("close test server: %v", err)
		}
	})
	return helper
}
