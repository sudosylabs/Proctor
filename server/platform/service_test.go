// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package platform

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	mailpkg "github.com/sudosylabs/proctor/packages/mail"
	memoryvfs "github.com/sudosylabs/proctor/packages/vfs/memory"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type testStore struct{}
type testCache struct{}
type testMailer struct{}

type trackedStore struct {
	testStore
	closed atomic.Bool
}

type unhealthyCache struct {
	testCache
	closed atomic.Bool
}

type trackedMailer struct {
	testMailer
	closed atomic.Bool
}

type trackedCluster struct {
	started atomic.Bool
	stopped atomic.Bool
}

func (testStore) Institution() store.InstitutionStore               { return nil }
func (testStore) AcademicUnit() store.AcademicUnitStore             { return nil }
func (testStore) Programme() store.ProgrammeStore                   { return nil }
func (testStore) ProgrammeLevel() store.ProgrammeLevelStore         { return nil }
func (testStore) AcademicPeriod() store.AcademicPeriodStore         { return nil }
func (testStore) Class() store.ClassStore                           { return nil }
func (testStore) User() store.UserStore                             { return nil }
func (testStore) PasswordCredential() store.PasswordCredentialStore { return nil }
func (testStore) Session() store.SessionStore                       { return nil }
func (testStore) SessionCredential() store.SessionCredentialStore   { return nil }
func (testStore) Role() store.RoleStore                             { return nil }
func (testStore) RoleBinding() store.RoleBindingStore               { return nil }
func (testStore) Audit() store.AuditStore                           { return nil }
func (testStore) Installation() store.InstallationStore             { return nil }
func (testStore) Ping(context.Context) error                        { return nil }
func (testStore) GetDBSchemaVersion(context.Context) (int, error)   { return 0, nil }
func (testStore) GetLocalSchemaVersion() (int, error)               { return 0, nil }
func (testStore) ValidateSchema(context.Context) error              { return nil }
func (testStore) Close() error                                      { return nil }

func (testCache) Get(context.Context, string) ([]byte, error) {
	return nil, ErrCacheMiss
}
func (testCache) Set(context.Context, string, []byte, time.Duration, CacheCondition) error {
	return nil
}
func (testCache) Delete(context.Context, string) error { return nil }
func (testCache) Add(context.Context, string, int64, time.Duration) (int64, error) {
	return 0, nil
}
func (testCache) Ping(context.Context) error { return nil }
func (testCache) Close() error               { return nil }

func (testMailer) Enabled() bool         { return false }
func (testMailer) From() mailpkg.Address { return mailpkg.Address{} }
func (testMailer) Send(context.Context, mailpkg.Message) (mailpkg.Receipt, error) {
	return mailpkg.Receipt{}, ErrMailDisabled
}
func (testMailer) Test(context.Context) error { return nil }
func (testMailer) Close() error               { return nil }

func (s *trackedStore) Close() error {
	s.closed.Store(true)
	return nil
}

func (c *unhealthyCache) Ping(context.Context) error {
	return context.DeadlineExceeded
}

func (c *unhealthyCache) Close() error {
	c.closed.Store(true)
	return nil
}

func (m *trackedMailer) Close() error {
	m.closed.Store(true)
	return nil
}

func (c *trackedCluster) NodeID() string {
	return "test-node"
}

func (c *trackedCluster) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.started.Store(true)
	return nil
}

func (c *trackedCluster) Stop(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.stopped.Store(true)
	return nil
}

func (c *trackedCluster) Ping(ctx context.Context) error {
	return ctx.Err()
}

func (c *trackedCluster) RegisterMessageHandler(model.ClusterEvent, ClusterMessageHandler) error {
	return nil
}

func (c *trackedCluster) Broadcast(context.Context, *model.ClusterMessage) error {
	return nil
}

func (c *trackedCluster) SendToNode(context.Context, string, *model.ClusterMessage) error {
	return nil
}

func TestServiceReconfiguresLoggerFromSharedConfiguration(t *testing.T) {
	t.Parallel()

	firstPath := filepath.Join(t.TempDir(), "first.log")
	secondPath := filepath.Join(t.TempDir(), "second.log")
	store, err := config.NewStore(
		context.Background(),
		config.NewMemoryStore(nil),
		config.StoreOptions{LookupEnv: func(string) (string, bool) { return "", false }},
	)
	if err != nil {
		t.Fatal(err)
	}
	initial := store.Get()
	initial.Log.Targets = []config.LogTarget{{
		Name: "file", Type: "file", Level: "info", Format: "json", File: firstPath,
	}}
	if _, _, err := store.Set(context.Background(), initial); err != nil {
		t.Fatal(err)
	}

	service, err := New(ServiceConfig{
		ConfigStore: store,
		Store:       testStore{},
		Cache:       testCache{},
		Mailer:      testMailer{},
		VFS:         memoryvfs.New(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	service.Log().Info("first target")
	if err := service.Log().Flush(); err != nil {
		t.Fatal(err)
	}

	updated := store.Get()
	updated.Log.Targets[0].File = secondPath
	if _, _, err := store.Set(context.Background(), updated); err != nil {
		t.Fatal(err)
	}
	service.Log().Info("second target")
	if err := service.Log().Flush(); err != nil {
		t.Fatal(err)
	}

	firstData, err := os.ReadFile(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	secondData, err := os.ReadFile(secondPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(firstData), "first target") || strings.Contains(string(firstData), "second target") {
		t.Fatalf("first target = %q", firstData)
	}
	if !strings.Contains(string(secondData), "second target") {
		t.Fatalf("second target = %q", secondData)
	}
}

func TestServiceConstructionFailureClosesOwnedInfrastructure(t *testing.T) {
	t.Parallel()

	configuration, err := config.NewStore(
		context.Background(),
		config.NewMemoryStore(nil),
		config.StoreOptions{LookupEnv: func(string) (string, bool) { return "", false }},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = configuration.Close() })

	persistence := &trackedStore{}
	cache := &unhealthyCache{}
	mailer := &trackedMailer{}
	cluster := &trackedCluster{}
	_, err = New(ServiceConfig{
		ConfigStore: configuration,
		Store:       persistence,
		Cache:       cache,
		Cluster:     cluster,
		Mailer:      mailer,
		VFS:         memoryvfs.New(),
	})
	if err == nil || !strings.Contains(err.Error(), "cache") {
		t.Fatalf("New() error = %v", err)
	}
	if !persistence.closed.Load() {
		t.Error("store was not closed after dependency failure")
	}
	if !cache.closed.Load() {
		t.Error("cache was not closed after dependency failure")
	}
	if !mailer.closed.Load() {
		t.Error("mailer was not closed after dependency failure")
	}
	if !cluster.stopped.Load() {
		t.Error("cluster transport was not stopped after dependency failure")
	}
}

func TestServiceOwnsClusterLifecycle(t *testing.T) {
	t.Parallel()

	configuration, err := config.NewStore(
		context.Background(),
		config.NewMemoryStore(nil),
		config.StoreOptions{LookupEnv: func(string) (string, bool) { return "", false }},
	)
	if err != nil {
		t.Fatal(err)
	}
	cluster := &trackedCluster{}
	service, err := New(ServiceConfig{
		ConfigStore: configuration,
		Store:       testStore{},
		Cache:       testCache{},
		Cluster:     cluster,
		Mailer:      testMailer{},
		VFS:         memoryvfs.New(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if cluster.started.Load() {
		t.Fatal("cluster transport started during construction before handlers can be registered")
	}
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !cluster.started.Load() {
		t.Fatal("platform did not start cluster transport")
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	if !cluster.stopped.Load() {
		t.Fatal("platform did not stop cluster transport")
	}
}

var _ Cluster = (*trackedCluster)(nil)
