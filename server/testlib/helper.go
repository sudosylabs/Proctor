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
	// PersistenceClose tracks close of the default lifecycle-only persistence
	// stub. It is nil when the graph was constructed with WithStore.
	PersistenceClose *LifecycleStore
	Cache            *Cache
	Cluster          platform.Cluster
	Mailer           *Mailer
	VFS              *memoryvfs.FS
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
	var lifecycle *LifecycleStore
	if persistenceOverride == nil {
		lifecycle = NewLifecycleStore()
		persistenceOverride = lifecycle
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
		PersistenceClose: lifecycle,
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

// LifecycleStore is the lifecycle-only persistence seam for ordinary unit
// tests that never exercise durable model stores. Accessors return nil so
// composition can construct the graph; capability tests must supply WithStore
// or a focused consumer-owned fake.
type LifecycleStore struct {
	closed atomic.Bool
}

// NewLifecycleStore constructs the lifecycle-only persistence stub.
func NewLifecycleStore() *LifecycleStore {
	return &LifecycleStore{}
}

func (s *LifecycleStore) Institution() store.InstitutionStore { return nil }
func (s *LifecycleStore) AcademicUnit() store.AcademicUnitStore { return nil }
func (s *LifecycleStore) Programme() store.ProgrammeStore { return nil }
func (s *LifecycleStore) ProgrammeLevel() store.ProgrammeLevelStore { return nil }
func (s *LifecycleStore) AcademicPeriod() store.AcademicPeriodStore { return nil }
func (s *LifecycleStore) Class() store.ClassStore { return nil }
func (s *LifecycleStore) User() store.UserStore { return nil }
func (s *LifecycleStore) ExternalIdentity() store.ExternalIdentityStore { return nil }
func (s *LifecycleStore) ExternalLoginState() store.ExternalLoginStateStore { return nil }
func (s *LifecycleStore) UserToken() store.UserTokenStore { return nil }
func (s *LifecycleStore) PersonalAccessToken() store.PersonalAccessTokenStore { return nil }
func (s *LifecycleStore) MFA() store.MFAStore { return nil }
func (s *LifecycleStore) Affiliation() store.AffiliationStore { return nil }
func (s *LifecycleStore) AcademicUnitMember() store.AcademicUnitMemberStore { return nil }
func (s *LifecycleStore) ClassMember() store.ClassMemberStore { return nil }
func (s *LifecycleStore) PasswordCredential() store.PasswordCredentialStore { return nil }
func (s *LifecycleStore) Session() store.SessionStore { return nil }
func (s *LifecycleStore) SessionCredential() store.SessionCredentialStore { return nil }
func (s *LifecycleStore) Role() store.RoleStore { return nil }
func (s *LifecycleStore) RoleBinding() store.RoleBindingStore { return nil }
func (s *LifecycleStore) Audit() store.AuditStore { return nil }
func (s *LifecycleStore) Installation() store.InstallationStore { return nil }
func (s *LifecycleStore) ClusterDiscovery() store.ClusterDiscoveryStore {
	// Composition always requests discovery while constructing the cluster
	// transport. Local-mode unit tests only need a no-op implementation.
	return noopClusterDiscovery{}
}

type noopClusterDiscovery struct{}

func (noopClusterDiscovery) Upsert(context.Context, *store.ClusterDiscoveryNode) error {
	return nil
}
func (noopClusterDiscovery) ListLive(context.Context, int64) ([]*store.ClusterDiscoveryNode, error) {
	return nil, nil
}
func (noopClusterDiscovery) Delete(context.Context, string) error { return nil }
func (noopClusterDiscovery) DeleteExpired(context.Context, int64) (int64, error) {
	return 0, nil
}
func (s *LifecycleStore) Ping(context.Context) error                      { return nil }
func (s *LifecycleStore) GetDBSchemaVersion(context.Context) (int, error) { return 0, nil }
func (s *LifecycleStore) GetLocalSchemaVersion() (int, error)             { return 0, nil }
func (s *LifecycleStore) ValidateSchema(context.Context) error            { return nil }
func (s *LifecycleStore) Close() error {
	s.closed.Store(true)
	return nil
}

// Closed reports whether the assembled runtime closed the persistence
// capability.
func (s *LifecycleStore) Closed() bool { return s.closed.Load() }

var _ store.Store = (*LifecycleStore)(nil)
