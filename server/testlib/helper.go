// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

// Package testlib constructs the real Proctor application graph for tests.
package testlib

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"

	memoryvfs "github.com/sudosylabs/proctor/packages/vfs/memory"
	server "github.com/sudosylabs/proctor/server"
	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/app/api"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/mlog"
	"github.com/sudosylabs/proctor/server/platform"
	"github.com/sudosylabs/proctor/server/store"
)

type setupOptions struct {
	updateConfig func(*config.Config)
	persistence  store.Store
	cluster      platform.Cluster
	buildInfo    api.BuildInfo
}

// Option customizes the test graph. Each option replaces exactly one
// capability; everything else is constructed from configuration as in
// production.
type Option func(*setupOptions)

// WithConfig mutates the in-memory deployment configuration before the graph
// is constructed.
func WithConfig(update func(*config.Config)) Option {
	return func(options *setupOptions) {
		options.updateConfig = update
	}
}

// WithStore replaces the persistence capability, typically with a real
// PostgreSQL store in integration suites.
func WithStore(persistence store.Store) Option {
	return func(options *setupOptions) {
		options.persistence = persistence
	}
}

// WithCluster replaces the cluster transport selected from configuration.
func WithCluster(cluster platform.Cluster) Option {
	return func(options *setupOptions) {
		options.cluster = cluster
	}
}

// WithBuildInfo replaces the build information served by the HTTP API.
func WithBuildInfo(buildInfo api.BuildInfo) Option {
	return func(options *setupOptions) {
		options.buildInfo = buildInfo
	}
}

// Helper exposes the assembled production graph and the test doubles it was
// constructed with.
type Helper struct {
	Server      *server.Server
	App         *app.App
	Platform    *platform.Service
	API         *api.API
	Health      *app.Health
	ConfigStore *config.Store
	Logs        *mlog.Buffer
	Store       *Store
	Cache       *Cache
	Cluster     platform.Cluster
	Mailer      *Mailer
	VFS         *memoryvfs.FS
}

// Handler returns the HTTP transport of the assembled graph.
func (h *Helper) Handler() http.Handler {
	return h.API
}

// Setup constructs the production runtime graph through the module-root
// composition path with memory configuration, captured logs, and disposable
// memory capabilities. Environment variables never influence the test
// configuration.
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

	cache, err := newCache()
	if err != nil {
		tb.Fatalf("create test cache: %v", err)
	}
	mailer, err := newMailer()
	if err != nil {
		tb.Fatalf("create test mailer: %v", err)
	}
	filesystem := memoryvfs.New()

	persistenceOverride := settings.persistence
	var persistence *Store
	if persistenceOverride == nil {
		persistence = &Store{}
		persistenceOverride = persistence
	}
	runtime, err := server.NewForTesting(context.Background(), server.TestingOverrides{
		Configuration: store,
		Logger:        logger,
		Persistence:   persistenceOverride,
		Cache:         cache,
		Cluster:       settings.cluster,
		Mailer:        mailer,
		Filesystem:    filesystem,
		BuildInfo:     settings.buildInfo,
	})
	if err != nil {
		tb.Fatalf("create test server: %v", err)
	}
	helper := &Helper{
		Server:      runtime.Server,
		App:         runtime.Application,
		Platform:    runtime.Platform,
		API:         runtime.API,
		Health:      runtime.Health,
		ConfigStore: store,
		Logs:        logs,
		Store:       persistence,
		Cache:       cache,
		Cluster:     runtime.Platform.Cluster(),
		Mailer:      mailer,
		VFS:         filesystem,
	}
	tb.Cleanup(func() {
		if err := runtime.Server.Close(); err != nil {
			tb.Errorf("close test server: %v", err)
		}
	})
	return helper
}

// Store is the persistence dependency used by ordinary unit tests. SQL store
// tests use the real PostgreSQL adapter and its conformance suites.
type Store struct {
	closed atomic.Bool
}

func (s *Store) Institution() store.InstitutionStore           { return nil }
func (s *Store) AcademicUnit() store.AcademicUnitStore         { return nil }
func (s *Store) Programme() store.ProgrammeStore               { return nil }
func (s *Store) ProgrammeLevel() store.ProgrammeLevelStore     { return nil }
func (s *Store) AcademicPeriod() store.AcademicPeriodStore     { return nil }
func (s *Store) Class() store.ClassStore                       { return nil }
func (s *Store) User() store.UserStore                         { return nil }
func (s *Store) ExternalIdentity() store.ExternalIdentityStore { return nil }
func (s *Store) ExternalLoginState() store.ExternalLoginStateStore {
	return nil
}
func (s *Store) UserToken() store.UserTokenStore { return nil }
func (s *Store) PersonalAccessToken() store.PersonalAccessTokenStore {
	return nil
}
func (s *Store) MFA() store.MFAStore                 { return nil }
func (s *Store) Affiliation() store.AffiliationStore { return nil }
func (s *Store) AcademicUnitMember() store.AcademicUnitMemberStore {
	return nil
}
func (s *Store) ClassMember() store.ClassMemberStore { return nil }
func (s *Store) PasswordCredential() store.PasswordCredentialStore {
	return nil
}
func (s *Store) Session() store.SessionStore                     { return nil }
func (s *Store) SessionCredential() store.SessionCredentialStore { return nil }
func (s *Store) Role() store.RoleStore                           { return nil }
func (s *Store) RoleBinding() store.RoleBindingStore             { return nil }
func (s *Store) Audit() store.AuditStore                         { return nil }
func (s *Store) Installation() store.InstallationStore           { return nil }
func (s *Store) Ping(context.Context) error                      { return nil }
func (s *Store) GetDBSchemaVersion(context.Context) (int, error) { return 0, nil }
func (s *Store) GetLocalSchemaVersion() (int, error)             { return 0, nil }
func (s *Store) ValidateSchema(context.Context) error            { return nil }
func (s *Store) Close() error {
	s.closed.Store(true)
	return nil
}

// Closed reports whether the assembled runtime closed the persistence
// capability.
func (s *Store) Closed() bool { return s.closed.Load() }
