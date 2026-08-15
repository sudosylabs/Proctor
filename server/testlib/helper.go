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
	"github.com/sudosylabs/proctor/server/cluster/local"
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

// Helper exposes the runtime's public behaviors and retains the test adapters
// it supplied for lifecycle assertions.
type Helper struct {
	Server      *server.Server
	App         *app.App
	ConfigStore *config.Store
	Logs        *mlog.Buffer
	// Persistence and Cluster are the exact adapters supplied by testlib. A nil
	// Cluster means the production selector constructed a non-local configured
	// adapter such as Memberlist.
	Persistence store.Store
	Cluster     platform.Cluster
	// PersistenceClose tracks close of the default lifecycle-only persistence
	// stub. It is nil when the graph was constructed with WithStore.
	PersistenceClose *LifecycleStore
	Cache            *Cache
	Mailer           *Mailer
	VFS              *memoryvfs.FS
	handler          http.Handler
}

// Handler returns the HTTP transport of the assembled graph.
func (h *Helper) Handler() http.Handler {
	return h.handler
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
	if settings.cluster == nil && store.Get().Cluster.Backend == "local" {
		settings.cluster, err = local.New(store.Get().Cluster.NodeID, testClusterLogger{})
		if err != nil {
			tb.Fatalf("create local test cluster: %v", err)
		}
	}

	persistenceOverride := settings.persistence
	var lifecycle *LifecycleStore
	if persistenceOverride == nil {
		lifecycle = NewLifecycleStore()
		persistenceOverride = lifecycle
	}
	runtime, err := server.NewForTesting(context.Background(), server.TestingOverrides{
		Configuration:    store,
		Logger:           logger,
		Persistence:      persistenceOverride,
		Cache:            cache,
		Cluster:          settings.cluster,
		Mailer:           mailer,
		Filesystem:       filesystem,
		AllowMissingJobs: lifecycle != nil,
		BuildInfo:        settings.buildInfo,
	})
	if err != nil {
		tb.Fatalf("create test server: %v", err)
	}
	helper := &Helper{
		Server:           runtime.Server,
		App:              runtime.Application,
		handler:          runtime.Handler,
		ConfigStore:      store,
		Logs:             logs,
		Persistence:      persistenceOverride,
		Cluster:          settings.cluster,
		PersistenceClose: lifecycle,
		Cache:            cache,
		Mailer:           mailer,
		VFS:              filesystem,
	}
	tb.Cleanup(func() {
		if err := runtime.Server.Close(); err != nil {
			tb.Errorf("close test server: %v", err)
		}
	})
	return helper
}

type testClusterLogger struct{}

func (testClusterLogger) ErrorContext(context.Context, string, error) {}

// LifecycleStore is the lifecycle-only persistence seam for ordinary unit
// tests that never exercise durable model stores. Identity accessors return
// non-nil panic-on-use contracts so focused constructors can validate the real
// graph; capability tests must supply WithStore or a focused consumer-owned
// fake.
type LifecycleStore struct {
	closed atomic.Bool
}

// NewLifecycleStore constructs the lifecycle-only persistence stub.
func NewLifecycleStore() *LifecycleStore {
	return &LifecycleStore{}
}

func (s *LifecycleStore) Institution() store.InstitutionStore       { return lifecycleInstitutionStore{} }
func (s *LifecycleStore) AcademicUnit() store.AcademicUnitStore     { return lifecycleAcademicUnitStore{} }
func (s *LifecycleStore) Programme() store.ProgrammeStore           { return nil }
func (s *LifecycleStore) ProgrammeLevel() store.ProgrammeLevelStore { return nil }
func (s *LifecycleStore) AcademicPeriod() store.AcademicPeriodStore { return nil }
func (s *LifecycleStore) ExamAuthoring() store.ExamAuthoringStore {
	return lifecycleExamAuthoringStore{}
}
func (s *LifecycleStore) ExamResource() store.ExamResourceStore {
	return lifecycleExamResourceStore{}
}
func (s *LifecycleStore) ExamCorrection() store.ExamCorrectionStore {
	return lifecycleExamCorrectionStore{}
}

func (s *LifecycleStore) ExamAttempt() store.ExamAttemptStore {
	return lifecycleExamAttemptStore{}
}
func (s *LifecycleStore) ExamAttemptWorkspace() store.ExamAttemptWorkspaceStore {
	return lifecycleExamAttemptWorkspaceStore{}
}
func (s *LifecycleStore) ExamRevision() store.ExamRevisionStore {
	return lifecycleExamRevisionStore{}
}
func (s *LifecycleStore) ExamSitting() store.ExamSittingStore {
	return lifecycleExamSittingStore{}
}
func (s *LifecycleStore) ExamStarterWorkspace() store.ExamStarterWorkspaceStore {
	return lifecycleExamStarterWorkspaceStore{}
}
func (s *LifecycleStore) Class() store.ClassStore { return lifecycleClassStore{} }
func (s *LifecycleStore) User() store.UserStore   { return lifecycleUserStore{} }
func (s *LifecycleStore) File() store.FileStore   { return nil }
func (s *LifecycleStore) Job() store.JobStore     { return nil }
func (s *LifecycleStore) ExternalIdentity() store.ExternalIdentityStore {
	return lifecycleExternalIdentityStore{}
}
func (s *LifecycleStore) ExternalLoginState() store.ExternalLoginStateStore {
	return lifecycleExternalLoginStateStore{}
}
func (s *LifecycleStore) UserToken() store.UserTokenStore { return lifecycleUserTokenStore{} }
func (s *LifecycleStore) PersonalAccessToken() store.PersonalAccessTokenStore {
	return lifecyclePersonalAccessTokenStore{}
}
func (s *LifecycleStore) MFA() store.MFAStore                 { return lifecycleMFAStore{} }
func (s *LifecycleStore) Affiliation() store.AffiliationStore { return nil }
func (s *LifecycleStore) AcademicUnitMember() store.AcademicUnitMemberStore {
	return lifecycleAcademicUnitMemberStore{}
}
func (s *LifecycleStore) ClassMember() store.ClassMemberStore {
	return lifecycleClassMemberStore{}
}
func (s *LifecycleStore) PasswordCredential() store.PasswordCredentialStore {
	return lifecyclePasswordCredentialStore{}
}
func (s *LifecycleStore) Session() store.SessionStore { return lifecycleSessionStore{} }
func (s *LifecycleStore) SessionCredential() store.SessionCredentialStore {
	return lifecycleSessionCredentialStore{}
}
func (s *LifecycleStore) Role() store.RoleStore                 { return lifecycleRoleStore{} }
func (s *LifecycleStore) RoleBinding() store.RoleBindingStore   { return lifecycleRoleBindingStore{} }
func (s *LifecycleStore) Audit() store.AuditStore               { return lifecycleAuditStore{} }
func (s *LifecycleStore) Installation() store.InstallationStore { return nil }
func (s *LifecycleStore) ClusterDiscovery() store.ClusterDiscoveryStore {
	// Composition always requests discovery while constructing the cluster
	// transport. Local-mode unit tests only need a no-op implementation.
	return noopClusterDiscovery{}
}
func (s *LifecycleStore) CommandOutcome() store.CommandOutcomeStore { return noopCommandOutcomeStore{} }

type noopCommandOutcomeStore struct{}

func (noopCommandOutcomeStore) DeleteExpired(context.Context, int) (int64, error) { return 0, nil }

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

// These non-nil lifecycle-only contracts intentionally panic if a capability
// test invokes persistence without supplying WithStore. They allow the real
// composition graph to validate focused service dependencies while keeping
// lifecycle tests free of unrelated persistence behavior.
type lifecycleUserStore struct{ store.UserStore }
type lifecycleInstitutionStore struct{ store.InstitutionStore }
type lifecycleAcademicUnitStore struct{ store.AcademicUnitStore }
type lifecycleAcademicUnitMemberStore struct{ store.AcademicUnitMemberStore }
type lifecycleExamAuthoringStore struct{ store.ExamAuthoringStore }
type lifecycleExamResourceStore struct{ store.ExamResourceStore }
type lifecycleExamCorrectionStore struct{ store.ExamCorrectionStore }
type lifecycleExamAttemptStore struct{ store.ExamAttemptStore }
type lifecycleExamAttemptWorkspaceStore struct {
	store.ExamAttemptWorkspaceStore
}
type lifecycleExamRevisionStore struct{ store.ExamRevisionStore }
type lifecycleExamSittingStore struct{ store.ExamSittingStore }
type lifecycleExamStarterWorkspaceStore struct {
	store.ExamStarterWorkspaceStore
}
type lifecycleClassStore struct{ store.ClassStore }
type lifecycleClassMemberStore struct{ store.ClassMemberStore }
type lifecycleExternalIdentityStore struct{ store.ExternalIdentityStore }
type lifecycleExternalLoginStateStore struct{ store.ExternalLoginStateStore }
type lifecycleUserTokenStore struct{ store.UserTokenStore }
type lifecyclePasswordCredentialStore struct{ store.PasswordCredentialStore }
type lifecycleSessionStore struct{ store.SessionStore }
type lifecycleSessionCredentialStore struct{ store.SessionCredentialStore }
type lifecycleMFAStore struct{ store.MFAStore }
type lifecycleAuditStore struct{ store.AuditStore }
type lifecyclePersonalAccessTokenStore struct{ store.PersonalAccessTokenStore }
type lifecycleRoleStore struct{ store.RoleStore }
type lifecycleRoleBindingStore struct{ store.RoleBindingStore }
