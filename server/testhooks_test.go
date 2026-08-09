// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	mailpkg "github.com/sudosylabs/proctor/packages/mail"
	memoryvfs "github.com/sudosylabs/proctor/packages/vfs/memory"
	server "github.com/sudosylabs/proctor/server"
	"github.com/sudosylabs/proctor/server/app/api"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/mlog"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/platform"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/localcachelayer"
	"github.com/sudosylabs/proctor/server/store/retrylayer"
	"github.com/sudosylabs/proctor/server/store/timerlayer"
)

// hookStore is a persistence override whose per-model stores are unused by
// graph construction.
type hookStore struct {
	store.Store
	closed         atomic.Bool
	pingAttempts   atomic.Int64
	ping           func(int64) error
	academicPeriod store.AcademicPeriodStore
}

func (s *hookStore) Institution() store.InstitutionStore               { return nil }
func (s *hookStore) AcademicUnit() store.AcademicUnitStore             { return nil }
func (s *hookStore) Programme() store.ProgrammeStore                   { return nil }
func (s *hookStore) ProgrammeLevel() store.ProgrammeLevelStore         { return nil }
func (s *hookStore) AcademicPeriod() store.AcademicPeriodStore         { return s.academicPeriod }
func (s *hookStore) Class() store.ClassStore                           { return nil }
func (s *hookStore) Affiliation() store.AffiliationStore               { return nil }
func (s *hookStore) User() store.UserStore                             { return nil }
func (s *hookStore) File() store.FileStore                             { return nil }
func (s *hookStore) Job() store.JobStore                               { return nil }
func (s *hookStore) Session() store.SessionStore                       { return nil }
func (s *hookStore) Role() store.RoleStore                             { return nil }
func (s *hookStore) RoleBinding() store.RoleBindingStore               { return nil }
func (s *hookStore) Audit() store.AuditStore                           { return nil }
func (s *hookStore) Installation() store.InstallationStore             { return nil }
func (s *hookStore) ClusterDiscovery() store.ClusterDiscoveryStore     { return nil }
func (s *hookStore) ClassMember() store.ClassMemberStore               { return nil }
func (s *hookStore) AcademicUnitMember() store.AcademicUnitMemberStore { return nil }
func (s *hookStore) Ping(context.Context) error {
	attempt := s.pingAttempts.Add(1)
	if s.ping != nil {
		return s.ping(attempt)
	}
	return nil
}
func (s *hookStore) GetDBSchemaVersion(context.Context) (int, error) { return 0, nil }
func (s *hookStore) GetLocalSchemaVersion() (int, error)             { return 0, nil }
func (s *hookStore) ValidateSchema(context.Context) error            { return nil }
func (s *hookStore) Close() error                                    { s.closed.Store(true); return nil }

type hookCache struct{ closed atomic.Bool }

type hookAcademicPeriodStore struct {
	store.AcademicPeriodStore
	period   *model.AcademicPeriod
	attempts atomic.Int64
}

func (s *hookAcademicPeriodStore) Get(context.Context, string) (*model.AcademicPeriod, error) {
	s.attempts.Add(1)
	return s.period, nil
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
func (c *hookCache) Close() error               { c.closed.Store(true); return nil }

type hookMailer struct{ closed atomic.Bool }

func (m *hookMailer) Enabled() bool { return true }
func (m *hookMailer) From() mailpkg.Address {
	return mailpkg.Address{Name: "Proctor Test", Address: "no-reply@test.proctor.invalid"}
}
func (m *hookMailer) Send(context.Context, mailpkg.Message) (mailpkg.Receipt, error) {
	return mailpkg.Receipt{}, nil
}
func (m *hookMailer) Test(context.Context) error { return nil }
func (m *hookMailer) Close() error               { m.closed.Store(true); return nil }

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
	logger, err := mlog.New()
	if err != nil {
		t.Fatal(err)
	}
	persistence := &hookStore{}
	cache := &hookCache{}
	mailer := &hookMailer{}
	return server.TestingOverrides{
		Configuration: configuration,
		Logger:        logger,
		Persistence:   persistence,
		Cache:         cache,
		Mailer:        mailer,
		Filesystem:    memoryvfs.New(),
	}, persistence, cache, mailer
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
	logs := &mlog.Buffer{}
	if err := overrides.Logger.Configure(mlog.Config{
		MaxFieldBytes: configuration.Get().Log.MaxFieldBytes,
		Targets:       []mlog.Target{{Name: "test", Type: "console", Level: "trace", Format: "json", Writer: logs}},
	}); err != nil {
		t.Fatal(err)
	}

	runtime, err := server.NewForTesting(context.Background(), overrides)
	if err != nil {
		t.Fatalf("NewForTesting() error = %v", err)
	}

	if runtime.Server == nil || runtime.Platform == nil || runtime.Application == nil ||
		runtime.API == nil || runtime.Health == nil {
		t.Fatalf("NewForTesting() runtime = %#v, want every handle populated", runtime)
	}
	if runtime.Server.Ready() {
		t.Fatal("server is ready before Start")
	}
	if runtime.Application.Store() == persistence {
		t.Fatal("application persistence bypassed the root store layers")
	}
	if _, ok := runtime.Application.Store().(*localcachelayer.Layer); !ok {
		t.Fatalf("outer store layer = %T, want *localcachelayer.Layer", runtime.Application.Store())
	}
	if runtime.Platform.ConfigStore() != configuration {
		t.Fatal("platform does not own the provided configuration store")
	}
	if runtime.Platform.Store() != runtime.Application.Store() {
		t.Fatal("platform and application do not share the root-decorated persistence store")
	}
	if runtime.Platform.Cluster() == nil {
		t.Fatal("cluster was not selected from configuration")
	}
	observationsBeforePing := timedOperations.Load()
	if err := runtime.Application.Store().Ping(context.Background()); err != nil {
		t.Fatalf("timed persistence Ping() error = %v", err)
	}
	if timedOperations.Load() != observationsBeforePing+1 {
		t.Fatalf(
			"timed persistence observations = %d, want %d",
			timedOperations.Load(),
			observationsBeforePing+1,
		)
	}

	if err := runtime.Server.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if !persistence.closed.Load() || !cache.closed.Load() || !mailer.closed.Load() {
		t.Fatal("Close() did not close the overridden capabilities")
	}
}

func TestRootComposesLocalCacheOutsideTimerAndRetry(t *testing.T) {
	t.Parallel()

	overrides, persistence, _, _ := newHookOverrides(t)
	now := time.Now().UTC()
	periods := &hookAcademicPeriodStore{period: &model.AcademicPeriod{
		ID:            model.NewAcademicPeriodID(),
		InstitutionID: model.NewInstitutionID(),
		Name:          "2026-2027",
		DisplayName:   "2026-2027",
		CreatedAt:     now,
		UpdatedAt:     now,
		StartsAt:      now,
		EndsAt:        now.Add(time.Hour),
		Revision:      1,
	}}
	persistence.academicPeriod = periods
	cache, err := localcachelayer.NewMemoryCache(8)
	if err != nil {
		t.Fatal(err)
	}
	overrides.StoreLocalCache = cache
	var timedReads atomic.Int64
	overrides.StoreMetrics = timerlayer.RecorderFunc(func(
		operation timerlayer.Operation,
		_ timerlayer.Outcome,
		_ time.Duration,
	) {
		if operation.String() == "academic_period.get" {
			timedReads.Add(1)
		}
	})

	runtime, err := server.NewForTesting(context.Background(), overrides)
	if err != nil {
		t.Fatalf("NewForTesting() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Server.Close() })

	for range 2 {
		if _, err := runtime.Application.Store().AcademicPeriod().Get(
			context.Background(),
			periods.period.ID.String(),
		); err != nil {
			t.Fatal(err)
		}
	}
	if got := periods.attempts.Load(); got != 1 {
		t.Fatalf("authoritative reads = %d, want 1", got)
	}
	if got := timedReads.Load(); got != 1 {
		t.Fatalf("timed reads = %d, want 1 because cache is outermost", got)
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
	buildInfo := api.BuildInfo{
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
	runtime.API.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("version status = %d, want 200", response.Code)
	}
	var served api.BuildInfo
	if err := json.Unmarshal(response.Body.Bytes(), &served); err != nil {
		t.Fatal(err)
	}
	if served != buildInfo {
		t.Fatalf("served build info = %#v, want %#v", served, buildInfo)
	}
}
