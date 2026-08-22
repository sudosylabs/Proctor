// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mailpkg "github.com/sudosylabs/proctor/packages/mail"
	vfspkg "github.com/sudosylabs/proctor/packages/vfs"
	memoryvfs "github.com/sudosylabs/proctor/packages/vfs/memory"
	server "github.com/sudosylabs/proctor/server"
	"github.com/sudosylabs/proctor/server/cluster"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/httpapi"
	"github.com/sudosylabs/proctor/server/logging"
	"github.com/sudosylabs/proctor/server/platform"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/retrylayer"
	"github.com/sudosylabs/proctor/server/store/timerlayer"
)

// hookStore is a persistence override whose per-model stores are unused by
// graph construction.
type hookStore struct {
	store.Store
	closed       atomic.Bool
	pingAttempts atomic.Int64
	ping         func(int64) error
	validateErr  error
	closedCount  atomic.Int64
	closeEvents  *hookCloseEvents
	closeErr     error
}

func (s *hookStore) Institution() store.InstitutionStore       { return hookInstitutionStore{} }
func (s *hookStore) AccessPolicy() store.AccessPolicyStore     { return hookAccessPolicyStore{} }
func (s *hookStore) AcademicUnit() store.AcademicUnitStore     { return hookAcademicUnitStore{} }
func (s *hookStore) Programme() store.ProgrammeStore           { return nil }
func (s *hookStore) ProgrammeLevel() store.ProgrammeLevelStore { return nil }
func (s *hookStore) AcademicPeriod() store.AcademicPeriodStore { return hookAcademicPeriodStore{} }
func (s *hookStore) ExamAuthoring() store.ExamAuthoringStore {
	return hookExamAuthoringStore{}
}
func (s *hookStore) ExamAttempt() store.ExamAttemptStore {
	return hookExamAttemptStore{}
}
func (s *hookStore) ExecutionGrant() store.ExecutionGrantStore {
	return hookExecutionGrantStore{}
}
func (s *hookStore) ExamAttemptWorkspace() store.ExamAttemptWorkspaceStore {
	return hookExamAttemptWorkspaceStore{}
}
func (s *hookStore) ExamSubmission() store.ExamSubmissionStore {
	return hookExamSubmissionStore{}
}
func (s *hookStore) ExamIntegrityReview() store.ExamIntegrityReviewStore {
	return hookExamIntegrityReviewStore{}
}
func (s *hookStore) ExamCorrection() store.ExamCorrectionStore {
	return hookExamCorrectionStore{}
}
func (s *hookStore) ExamResource() store.ExamResourceStore {
	return hookExamResourceStore{}
}
func (s *hookStore) ExamRevision() store.ExamRevisionStore {
	return hookExamRevisionStore{}
}
func (s *hookStore) ExamSitting() store.ExamSittingStore {
	return hookExamSittingStore{}
}
func (s *hookStore) ExamStarterWorkspace() store.ExamStarterWorkspaceStore {
	return hookExamStarterWorkspaceStore{}
}
func (s *hookStore) Class() store.ClassStore             { return hookClassStore{} }
func (s *hookStore) Affiliation() store.AffiliationStore { return nil }
func (s *hookStore) User() store.UserStore               { return hookUserStore{} }
func (s *hookStore) UserSettings() store.UserSettingsStore {
	return hookUserSettingsStore{}
}
func (s *hookStore) File() store.FileStore       { return nil }
func (s *hookStore) Job() store.JobStore         { return nil }
func (s *hookStore) Session() store.SessionStore { return hookSessionStore{} }
func (s *hookStore) SessionCredential() store.SessionCredentialStore {
	return hookSessionCredentialStore{}
}
func (s *hookStore) PasswordCredential() store.PasswordCredentialStore {
	return hookPasswordCredentialStore{}
}
func (s *hookStore) MFA() store.MFAStore { return hookMFAStore{} }
func (s *hookStore) PersonalAccessToken() store.PersonalAccessTokenStore {
	return hookPersonalAccessTokenStore{}
}
func (s *hookStore) ExternalIdentity() store.ExternalIdentityStore {
	return hookExternalIdentityStore{}
}
func (s *hookStore) ExternalLoginState() store.ExternalLoginStateStore {
	return hookExternalLoginStateStore{}
}
func (s *hookStore) DesktopAuthorization() store.DesktopAuthorizationStore {
	return hookDesktopAuthorizationStore{}
}
func (s *hookStore) UserToken() store.UserTokenStore   { return hookUserTokenStore{} }
func (s *hookStore) Invitation() store.InvitationStore { return hookInvitationStore{} }
func (s *hookStore) CommandOutcome() store.CommandOutcomeStore {
	return hookCommandOutcomeStore{}
}
func (s *hookStore) OnboardingImport() store.OnboardingImportStore {
	return hookOnboardingImportStore{}
}
func (s *hookStore) Role() store.RoleStore                         { return hookRoleStore{} }
func (s *hookStore) RoleBinding() store.RoleBindingStore           { return hookRoleBindingStore{} }
func (s *hookStore) Audit() store.AuditStore                       { return hookAuditStore{} }
func (s *hookStore) Installation() store.InstallationStore         { return nil }
func (s *hookStore) ClusterDiscovery() store.ClusterDiscoveryStore { return nil }
func (s *hookStore) ServingNodeLease() store.ServingNodeLeaseStore {
	return hookServingNodeLeaseStore{}
}
func (s *hookStore) ClassMember() store.ClassMemberStore { return hookClassMemberStore{} }
func (s *hookStore) AcademicUnitMember() store.AcademicUnitMemberStore {
	return hookAcademicUnitMemberStore{}
}

type hookInvitationStore struct{ store.InvitationStore }
type hookOnboardingImportStore struct{ store.OnboardingImportStore }

type hookServingNodeLeaseStore struct{}

func (hookServingNodeLeaseStore) Upsert(_ context.Context, claim *store.ServingNodeLeaseClaim) (*store.ServingNodeLease, error) {
	at := time.Now().UTC()
	return &store.ServingNodeLease{NodeID: claim.NodeID, LeaseID: claim.LeaseID, UpdatedAt: at, ExpiresAt: at.Add(claim.Lifetime)}, nil
}
func (hookServingNodeLeaseStore) Delete(context.Context, string, string) error { return nil }

func (s *hookStore) Ping(context.Context) error {
	attempt := s.pingAttempts.Add(1)
	if s.ping != nil {
		return s.ping(attempt)
	}
	return nil
}
func (s *hookStore) GetDBSchemaVersion(context.Context) (int, error) { return 0, nil }
func (s *hookStore) GetLocalSchemaVersion() (int, error)             { return 0, nil }
func (s *hookStore) ValidateSchema(context.Context) error            { return s.validateErr }
func (s *hookStore) Close() error {
	s.closed.Store(true)
	s.closedCount.Add(1)
	if s.closeEvents != nil {
		s.closeEvents.record("store")
	}
	return s.closeErr
}

type hookExamAuthoringStore struct{ store.ExamAuthoringStore }
type hookExamAttemptStore struct{ store.ExamAttemptStore }
type hookExecutionGrantStore struct{ store.ExecutionGrantStore }
type hookExamAttemptWorkspaceStore struct {
	store.ExamAttemptWorkspaceStore
}
type hookExamSubmissionStore struct{ store.ExamSubmissionStore }
type hookExamIntegrityReviewStore struct{ store.ExamIntegrityReviewStore }
type hookExamCorrectionStore struct{ store.ExamCorrectionStore }
type hookExamResourceStore struct{ store.ExamResourceStore }
type hookExamRevisionStore struct{ store.ExamRevisionStore }
type hookExamSittingStore struct{ store.ExamSittingStore }
type hookExamStarterWorkspaceStore struct {
	store.ExamStarterWorkspaceStore
}
type hookAcademicUnitMemberStore struct{ store.AcademicUnitMemberStore }

type hookUserStore struct{ store.UserStore }
type hookAccessPolicyStore struct{ store.AccessPolicyStore }
type hookUserSettingsStore struct{ store.UserSettingsStore }
type hookInstitutionStore struct{ store.InstitutionStore }
type hookAcademicUnitStore struct{ store.AcademicUnitStore }
type hookAcademicPeriodStore struct{ store.AcademicPeriodStore }
type hookClassStore struct{ store.ClassStore }
type hookClassMemberStore struct{ store.ClassMemberStore }
type hookSessionStore struct{ store.SessionStore }
type hookSessionCredentialStore struct{ store.SessionCredentialStore }
type hookPasswordCredentialStore struct{ store.PasswordCredentialStore }
type hookMFAStore struct{ store.MFAStore }
type hookAuditStore struct{ store.AuditStore }
type hookPersonalAccessTokenStore struct{ store.PersonalAccessTokenStore }
type hookExternalIdentityStore struct{ store.ExternalIdentityStore }
type hookExternalLoginStateStore struct{ store.ExternalLoginStateStore }
type hookDesktopAuthorizationStore struct {
	store.DesktopAuthorizationStore
}
type hookUserTokenStore struct{ store.UserTokenStore }
type hookRoleStore struct{ store.RoleStore }
type hookCommandOutcomeStore struct{}

func (hookCommandOutcomeStore) Has(context.Context, *store.CommandIdempotency) (bool, error) {
	return false, nil
}
func (hookCommandOutcomeStore) DeleteExpired(context.Context, int) (int64, error) { return 0, nil }

type hookRoleBindingStore struct{ store.RoleBindingStore }

type hookCache struct {
	closed      atomic.Bool
	closedCount atomic.Int64
	closeEvents *hookCloseEvents
	closeErr    error
}

func (c *hookCache) Get(context.Context, string) ([]byte, error) {
	return nil, errors.New("no values stored")
}
func (c *hookCache) Set(context.Context, string, []byte, time.Duration, platform.CacheCondition) error {
	return nil
}
func (c *hookCache) Delete(context.Context, string) error { return nil }
func (c *hookCache) Add(context.Context, string, int64, time.Duration) (int64, error) {
	return 0, nil
}
func (c *hookCache) Ping(context.Context) error { return nil }
func (c *hookCache) Close() error {
	c.closed.Store(true)
	c.closedCount.Add(1)
	if c.closeEvents != nil {
		c.closeEvents.record("cache")
	}
	return c.closeErr
}

type hookMailer struct {
	closed      atomic.Bool
	closedCount atomic.Int64
	closeEvents *hookCloseEvents
	closeErr    error
}

// The hook configuration uses the production default with mail disabled. Keep
// the injected transport's capability state aligned with that configuration;
// an enabled transport must also provide the configured payload-key ring.
func (m *hookMailer) Enabled() bool { return false }
func (m *hookMailer) From() mailpkg.Address {
	return mailpkg.Address{Name: "Proctor Test", Address: "no-reply@test.proctor.invalid"}
}
func (m *hookMailer) Send(context.Context, mailpkg.Message) (mailpkg.Receipt, error) {
	return mailpkg.Receipt{}, nil
}
func (m *hookMailer) Test(context.Context) error { return nil }
func (m *hookMailer) Close() error {
	m.closed.Store(true)
	m.closedCount.Add(1)
	if m.closeEvents != nil {
		m.closeEvents.record("mailer")
	}
	return m.closeErr
}

type hookCloseEvents struct {
	mu     sync.Mutex
	values []string
}

func (e *hookCloseEvents) record(value string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.values = append(e.values, value)
}

func (e *hookCloseEvents) snapshot() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.values...)
}

type hookCluster struct {
	platform.Cluster
	cancel       context.CancelFunc
	startedCount atomic.Int64
	closedCount  atomic.Int64
	closeEvents  *hookCloseEvents
}

func (c *hookCluster) NodeID() string { return "hook-node" }
func (c *hookCluster) Start(context.Context) error {
	c.startedCount.Add(1)
	return nil
}
func (c *hookCluster) Ping(context.Context) error { return nil }
func (c *hookCluster) RegisterHandler(cluster.Event, cluster.Handler) error {
	if c.cancel != nil {
		c.cancel()
	}
	return nil
}
func (c *hookCluster) Stop(context.Context) error {
	c.closedCount.Add(1)
	if c.closeEvents != nil {
		c.closeEvents.record("cluster")
	}
	return nil
}

type hookFilesystem struct {
	vfspkg.FileSystem
	closedCount atomic.Int64
	closeEvents *hookCloseEvents
}

func (f *hookFilesystem) Close() error {
	f.closedCount.Add(1)
	if f.closeEvents != nil {
		f.closeEvents.record("vfs")
	}
	return nil
}

type hookConfigurationBacking struct {
	data        []byte
	closedCount atomic.Int64
	closeEvents *hookCloseEvents
}

func (b *hookConfigurationBacking) Load(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return append([]byte(nil), b.data...), nil
}
func (b *hookConfigurationBacking) Save(context.Context, []byte) error { return nil }
func (b *hookConfigurationBacking) String() string                     { return "hook" }
func (b *hookConfigurationBacking) Close() error {
	b.closedCount.Add(1)
	if b.closeEvents != nil {
		b.closeEvents.record("configuration")
	}
	return nil
}

func newHookOverrides(t *testing.T) (server.TestingOverrides, *hookStore, *hookCache, *hookMailer) {
	t.Helper()

	configuration, err := config.NewStore(
		context.Background(),
		config.NewMemoryStore(nil),
		config.StoreOptions{LookupEnv: func(string) (string, bool) { return "", false }},
	)
	if err != nil {
		t.Fatal(err)
	}
	logger, err := logging.New()
	if err != nil {
		t.Fatal(err)
	}
	persistence := &hookStore{}
	cache := &hookCache{}
	mailer := &hookMailer{}
	return server.TestingOverrides{
		Configuration:    configuration,
		Logger:           logger,
		Persistence:      persistence,
		Cache:            cache,
		Mailer:           mailer,
		Filesystem:       memoryvfs.New(),
		AllowMissingJobs: true,
	}, persistence, cache, mailer
}

func newHookConfiguration(t *testing.T, events *hookCloseEvents) (*config.Store, *hookConfigurationBacking) {
	t.Helper()

	backing := &hookConfigurationBacking{closeEvents: events}
	configuration, err := config.NewStore(
		context.Background(),
		backing,
		config.StoreOptions{LookupEnv: func(string) (string, bool) { return "", false }},
	)
	if err != nil {
		t.Fatal(err)
	}
	return configuration, backing
}

func TestNewForTestingAssemblesTheProductionGraphWithOverrides(t *testing.T) {
	t.Parallel()

	overrides, persistence, cache, mailer := newHookOverrides(t)
	var timedOperations atomic.Int64
	overrides.StoreMetrics = timerlayer.RecorderFunc(func(
		operation timerlayer.Operation,
		outcome timerlayer.Outcome,
		duration time.Duration,
	) {
		if operation.String() == "store.ping" && outcome == timerlayer.OutcomeSuccess && duration >= 0 {
			timedOperations.Add(1)
		}
	})
	configuration := overrides.Configuration
	logs := &logging.Buffer{}
	if err := overrides.Logger.Configure(logging.Config{
		MaxFieldBytes: configuration.Get().Log.MaxFieldBytes,
		Targets:       []logging.Target{{Name: "test", Type: "console", Level: "trace", Format: "json", Writer: logs}},
	}); err != nil {
		t.Fatal(err)
	}

	runtime, err := server.NewForTesting(context.Background(), overrides)
	if err != nil {
		t.Fatalf("NewForTesting() error = %v", err)
	}

	if runtime.Server == nil || runtime.Application == nil || runtime.Handler == nil {
		t.Fatalf("NewForTesting() runtime = %#v, want every handle populated", runtime)
	}
	if runtime.Server.Ready() {
		t.Fatal("server is ready before Run")
	}
	observationsBeforePing := timedOperations.Load()
	if observationsBeforePing == 0 {
		t.Fatal("root store timing layer did not observe construction dependency checks")
	}

	if err := runtime.Server.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if !persistence.closed.Load() || !cache.closed.Load() || !mailer.closed.Load() {
		t.Fatal("Close() did not close the overridden capabilities")
	}
}

func TestCompositionIsInertUntilServerRun(t *testing.T) {
	t.Parallel()

	overrides, _, _, _ := newHookOverrides(t)
	clusterTransport := &hookCluster{}
	overrides.Cluster = clusterTransport
	runtime, err := server.NewForTesting(context.Background(), overrides)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.Server.Ready() {
		t.Fatal("composed node is ready before Run")
	}
	if got := clusterTransport.startedCount.Load(); got != 0 {
		t.Fatalf("cluster Start calls during composition = %d, want 0", got)
	}
	if err := runtime.Server.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCompositionRequiresDurableJobsUnlessExplicitlyDisabled(t *testing.T) {
	t.Parallel()

	overrides, persistence, cache, mailer := newHookOverrides(t)
	overrides.AllowMissingJobs = false
	runtime, err := server.NewForTesting(context.Background(), overrides)
	if runtime != nil {
		t.Fatalf("NewForTesting(missing Jobs) runtime = %#v, want nil", runtime)
	}
	if err == nil || !strings.Contains(err.Error(), "require durable Job runtime") {
		t.Fatalf("NewForTesting(missing Jobs) error = %v", err)
	}
	if !persistence.closed.Load() || !cache.closed.Load() || !mailer.closed.Load() {
		t.Fatal("missing Job runtime did not release composed infrastructure")
	}
}

func TestNewForTestingCanceledConstructionClosesOwnedOverrides(t *testing.T) {
	t.Parallel()

	overrides, persistence, cache, mailer := newHookOverrides(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	runtime, err := server.NewForTesting(ctx, overrides)
	if runtime != nil {
		t.Fatalf("NewForTesting(canceled) runtime = %#v, want nil", runtime)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("NewForTesting(canceled) error = %v, want context.Canceled", err)
	}
	if !persistence.closed.Load() || !cache.closed.Load() || !mailer.closed.Load() {
		t.Fatal("canceled construction did not close every owned override")
	}
}

func TestFailedStoreDecoratorLeavesInputOwnedByAcquisition(t *testing.T) {
	t.Parallel()

	overrides, persistence, cache, mailer := newHookOverrides(t)
	overrides.StoreRetry = &retrylayer.Policy{}

	runtime, err := server.NewForTesting(context.Background(), overrides)
	if runtime != nil {
		t.Fatalf("NewForTesting(invalid retry) runtime = %#v, want nil", runtime)
	}
	if err == nil || !strings.Contains(err.Error(), "construct store retry layer") {
		t.Fatalf("NewForTesting(invalid retry) error = %v, want retry-layer construction failure", err)
	}
	if !persistence.closed.Load() || !cache.closed.Load() || !mailer.closed.Load() {
		t.Fatal("failed Store decorator did not release every owned override")
	}
}

func TestCanceledAcquisitionReleasesOverridesOnceInReverseOrder(t *testing.T) {
	t.Parallel()

	overrides, persistence, cache, mailer := newHookOverrides(t)
	events := &hookCloseEvents{}
	configuration, configurationBacking := newHookConfiguration(t, events)
	overrides.Configuration = configuration
	persistence.closeEvents = events
	cache.closeEvents = events
	mailer.closeEvents = events
	clusterTransport := &hookCluster{closeEvents: events}
	filesystem := &hookFilesystem{
		FileSystem:  memoryvfs.New(),
		closeEvents: events,
	}
	overrides.Cluster = clusterTransport
	overrides.Filesystem = filesystem
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	runtime, err := server.NewForTesting(ctx, overrides)
	if runtime != nil {
		t.Fatalf("NewForTesting(canceled) runtime = %#v, want nil", runtime)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("NewForTesting(canceled) error = %v, want context.Canceled", err)
	}
	wantOrder := []string{"cluster", "vfs", "mailer", "cache", "store", "configuration"}
	if got := events.snapshot(); !slices.Equal(got, wantOrder) {
		t.Fatalf("release order = %v, want %v", got, wantOrder)
	}
	for name, count := range map[string]int64{
		"cluster":       clusterTransport.closedCount.Load(),
		"vfs":           filesystem.closedCount.Load(),
		"mailer":        mailer.closedCount.Load(),
		"cache":         cache.closedCount.Load(),
		"store":         persistence.closedCount.Load(),
		"configuration": configurationBacking.closedCount.Load(),
	} {
		if count != 1 {
			t.Fatalf("%s close count = %d, want 1", name, count)
		}
	}
	if err := overrides.Logger.Configure(logging.Config{}); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("logger Configure() after release error = %v, want closed", err)
	}
}

func TestCancellationBetweenAcquisitionPhasesUnwindsCurrentOwner(t *testing.T) {
	t.Parallel()

	overrides, persistence, cache, mailer := newHookOverrides(t)
	events := &hookCloseEvents{}
	configuration, configurationBacking := newHookConfiguration(t, events)
	overrides.Configuration = configuration
	persistence.closeEvents = events
	cache.closeEvents = events
	mailer.closeEvents = events
	ctx, cancel := context.WithCancel(context.Background())
	clusterTransport := &hookCluster{cancel: cancel, closeEvents: events}
	filesystem := &hookFilesystem{
		FileSystem:  memoryvfs.New(),
		closeEvents: events,
	}
	overrides.Cluster = clusterTransport
	overrides.Filesystem = filesystem

	runtime, err := server.NewForTesting(ctx, overrides)
	if runtime != nil {
		t.Fatalf("NewForTesting(mid-acquisition cancellation) runtime = %#v, want nil", runtime)
	}
	if !errors.Is(err, context.Canceled) ||
		!strings.Contains(err.Error(), "decorate persistence with local cache") {
		t.Fatalf("NewForTesting(mid-acquisition cancellation) error = %v", err)
	}
	wantOrder := []string{"cluster", "vfs", "mailer", "cache", "store", "configuration"}
	if got := events.snapshot(); !slices.Equal(got, wantOrder) {
		t.Fatalf("release order = %v, want %v", got, wantOrder)
	}
	if configurationBacking.closedCount.Load() != 1 {
		t.Fatalf("configuration close count = %d, want 1", configurationBacking.closedCount.Load())
	}
}

func TestPlatformAcceptanceFailureReleasesTransferredResourcesOnce(t *testing.T) {
	t.Parallel()

	events := &hookCloseEvents{}
	configuration, configurationBacking := newHookConfiguration(t, events)
	logger, err := logging.New()
	if err != nil {
		t.Fatal(err)
	}
	schemaErr := errors.New("schema validation failed")
	persistence := &hookStore{validateErr: schemaErr, closeEvents: events}
	cache := &hookCache{closeEvents: events}
	mailer := &hookMailer{closeEvents: events}
	clusterTransport := &hookCluster{closeEvents: events}
	filesystem := &hookFilesystem{
		FileSystem:  memoryvfs.New(),
		closeEvents: events,
	}

	runtime, err := server.NewForTesting(context.Background(), server.TestingOverrides{
		Configuration: configuration,
		Logger:        logger,
		Persistence:   persistence,
		Cache:         cache,
		Mailer:        mailer,
		Filesystem:    filesystem,
		Cluster:       clusterTransport,
	})
	if runtime != nil {
		t.Fatalf("NewForTesting(schema failure) runtime = %#v, want nil", runtime)
	}
	if !errors.Is(err, schemaErr) {
		t.Fatalf("NewForTesting(schema failure) error = %v, want %v", err, schemaErr)
	}

	wantOrder := []string{"cluster", "vfs", "mailer", "cache", "store", "configuration"}
	if got := events.snapshot(); !slices.Equal(got, wantOrder) {
		t.Fatalf("acceptance cleanup order = %v, want %v", got, wantOrder)
	}
	for name, count := range map[string]int64{
		"cluster":       clusterTransport.closedCount.Load(),
		"vfs":           filesystem.closedCount.Load(),
		"mailer":        mailer.closedCount.Load(),
		"cache":         cache.closedCount.Load(),
		"store":         persistence.closedCount.Load(),
		"configuration": configurationBacking.closedCount.Load(),
	} {
		if count != 1 {
			t.Fatalf("%s close count = %d, want 1", name, count)
		}
	}
	if err := logger.Configure(logging.Config{}); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("logger Configure() after acceptance failure error = %v, want closed", err)
	}
}

func TestAcquisitionPreservesPrimaryAndCleanupFailures(t *testing.T) {
	t.Parallel()

	overrides, persistence, cache, mailer := newHookOverrides(t)
	storeCleanupErr := errors.New("store cleanup failed")
	cacheCleanupErr := errors.New("cache cleanup failed")
	persistence.closeErr = storeCleanupErr
	cache.closeErr = cacheCleanupErr
	overrides.StoreRetry = &retrylayer.Policy{
		MaxAttempts:    0,
		InitialBackoff: time.Nanosecond,
		MaxBackoff:     time.Nanosecond,
		IsTransient:    func(error) bool { return false },
	}

	runtime, err := server.NewForTesting(context.Background(), overrides)
	if runtime != nil {
		t.Fatalf("NewForTesting(invalid retry) runtime = %#v, want nil", runtime)
	}
	if err == nil || !strings.Contains(err.Error(), "construct store retry layer") {
		t.Fatalf("NewForTesting(invalid retry) error = %v, want primary retry failure", err)
	}
	for _, expected := range []error{storeCleanupErr, cacheCleanupErr} {
		if !errors.Is(err, expected) {
			t.Fatalf("NewForTesting(invalid retry) error = %v, want joined %v", err, expected)
		}
	}
	if !mailer.closed.Load() {
		t.Fatal("cleanup stopped before closing remaining owned resources")
	}
}

func TestRootComposesTimerOutsideRetry(t *testing.T) {
	t.Parallel()

	overrides, persistence, _, _ := newHookOverrides(t)
	transientErr := errors.New("serialization failure")
	const transientFailuresBeforeSuccess int64 = 2
	persistence.ping = func(attempt int64) error {
		if attempt <= transientFailuresBeforeSuccess {
			return transientErr
		}
		return nil
	}
	overrides.StoreRetry = &retrylayer.Policy{
		MaxAttempts:    3,
		InitialBackoff: time.Nanosecond,
		MaxBackoff:     time.Nanosecond,
		IsTransient:    func(err error) bool { return err == transientErr },
	}
	var observations atomic.Int64
	overrides.StoreMetrics = timerlayer.RecorderFunc(func(
		operation timerlayer.Operation,
		_ timerlayer.Outcome,
		_ time.Duration,
	) {
		if operation.String() == "store.ping" {
			observations.Add(1)
		}
	})

	runtime, err := server.NewForTesting(context.Background(), overrides)
	if err != nil {
		t.Fatalf("NewForTesting() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Server.Close() })

	attempts := persistence.pingAttempts.Load()
	if attempts < 3 {
		t.Fatalf("underlying Ping() attempts = %d, want at least 3", attempts)
	}
	if got, want := observations.Load(), attempts-transientFailuresBeforeSuccess; got != want {
		t.Fatalf("timer observations = %d, want %d logical calls outside retries", got, want)
	}
}

func TestNewForTestingServesTheProvidedBuildInfo(t *testing.T) {
	t.Parallel()

	overrides, _, _, _ := newHookOverrides(t)
	buildInfo := httpapi.BuildInfo{
		Version:   "test-version",
		Commit:    "test-commit",
		BuildTime: "test-time",
		GoVersion: "test-go",
	}
	overrides.BuildInfo = buildInfo

	runtime, err := server.NewForTesting(context.Background(), overrides)
	if err != nil {
		t.Fatalf("NewForTesting() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Server.Close() })

	request := httptest.NewRequest(http.MethodGet, "/api/v1/system/version", nil)
	response := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("version status = %d, want 200", response.Code)
	}
	var served httpapi.BuildInfo
	if err := json.Unmarshal(response.Body.Bytes(), &served); err != nil {
		t.Fatal(err)
	}
	if served != buildInfo {
		t.Fatalf("served build info = %#v, want %#v", served, buildInfo)
	}
}
