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
	"github.com/sudosylabs/proctor/server/platform"
	"github.com/sudosylabs/proctor/server/store"
)

// hookStore is a persistence override whose per-model stores are unused by
// graph construction.
type hookStore struct {
	store.Store
	closed atomic.Bool
}

func (s *hookStore) Institution() store.InstitutionStore             { return nil }
func (s *hookStore) AcademicUnit() store.AcademicUnitStore           { return nil }
func (s *hookStore) Programme() store.ProgrammeStore                 { return nil }
func (s *hookStore) ProgrammeLevel() store.ProgrammeLevelStore       { return nil }
func (s *hookStore) AcademicPeriod() store.AcademicPeriodStore       { return nil }
func (s *hookStore) Ping(context.Context) error                      { return nil }
func (s *hookStore) GetDBSchemaVersion(context.Context) (int, error) { return 0, nil }
func (s *hookStore) GetLocalSchemaVersion() (int, error)             { return 0, nil }
func (s *hookStore) ValidateSchema(context.Context) error            { return nil }
func (s *hookStore) Close() error                                    { s.closed.Store(true); return nil }

type hookCache struct{ closed atomic.Bool }

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
	if runtime.Application.Platform() != runtime.Platform {
		t.Fatal("application was not constructed with the runtime platform")
	}
	if runtime.Platform.ConfigStore() != configuration {
		t.Fatal("platform does not own the provided configuration store")
	}
	if runtime.Platform.Store() != persistence {
		t.Fatal("platform does not own the provided persistence store")
	}
	if runtime.Platform.Cluster() == nil {
		t.Fatal("cluster was not selected from configuration")
	}

	if err := runtime.Server.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if !persistence.closed.Load() || !cache.closed.Load() || !mailer.closed.Load() {
		t.Fatal("Close() did not close the overridden capabilities")
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
