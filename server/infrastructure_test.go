// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	vfspkg "github.com/sudosylabs/proctor/packages/vfs"
	"github.com/sudosylabs/proctor/server/cluster"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/mlog"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/platform"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/localcachelayer"
	"github.com/sudosylabs/proctor/server/store/timerlayer"
)

type releaseStore struct {
	store.Store
	events *[]string
}

func (s releaseStore) Close() error {
	if s.events != nil {
		*s.events = append(*s.events, "store")
	}
	return nil
}

type releaseCache struct {
	platform.Cache
	events *[]string
}

func (c releaseCache) Close() error {
	if c.events != nil {
		*c.events = append(*c.events, "cache")
	}
	return nil
}

type releaseMailer struct {
	platform.Mailer
	events *[]string
}

func (m releaseMailer) Close() error {
	if m.events != nil {
		*m.events = append(*m.events, "mailer")
	}
	return nil
}

type releaseCluster struct {
	platform.Cluster
	events *[]string
}

func (c releaseCluster) NodeID() string                                       { return "release-node" }
func (c releaseCluster) RegisterHandler(cluster.Event, cluster.Handler) error { return nil }
func (c releaseCluster) Broadcast(context.Context, *cluster.Message) error    { return nil }

func (c releaseCluster) Stop(context.Context) error {
	if c.events != nil {
		*c.events = append(*c.events, "cluster")
	}
	return nil
}

type releaseVFS struct {
	vfspkg.FileSystem
	events *[]string
}

func (f releaseVFS) Close() error {
	if f.events != nil {
		*f.events = append(*f.events, "vfs")
	}
	return nil
}

func TestOwnedInfrastructureReleasesInReverseDependencyOrder(t *testing.T) {
	t.Parallel()

	events := []string{}
	owned := ownedInfrastructure{
		persistence: releaseStore{events: &events},
		cache:       releaseCache{events: &events},
		mailer:      releaseMailer{events: &events},
		filesystem:  releaseVFS{events: &events},
		cluster:     releaseCluster{events: &events},
	}
	if err := owned.release(); err != nil {
		t.Fatalf("release() error = %v", err)
	}
	want := []string{"cluster", "vfs", "mailer", "cache", "store"}
	if len(events) != len(want) {
		t.Fatalf("release order = %v, want %v", events, want)
	}
	for index := range want {
		if events[index] != want[index] {
			t.Fatalf("release order = %v, want %v", events, want)
		}
	}
}

type layerOrderStore struct {
	store.Store
	periods store.AcademicPeriodStore
}

func (s layerOrderStore) AcademicPeriod() store.AcademicPeriodStore   { return s.periods }
func (layerOrderStore) ClusterDiscovery() store.ClusterDiscoveryStore { return nil }
func (layerOrderStore) Close() error                                  { return nil }

type layerOrderPeriods struct {
	store.AcademicPeriodStore
	period *model.AcademicPeriod
	reads  atomic.Int64
}

func (s *layerOrderPeriods) Get(context.Context, string) (*model.AcademicPeriod, error) {
	s.reads.Add(1)
	copy := *s.period
	return &copy, nil
}

func TestRootComposesLocalCacheOutsideTiming(t *testing.T) {
	t.Parallel()

	configuration, err := config.NewStore(context.Background(), config.NewMemoryStore(nil), config.StoreOptions{
		LookupEnv: func(string) (string, bool) { return "", false },
	})
	if err != nil {
		t.Fatal(err)
	}
	logger, err := mlog.New()
	if err != nil {
		t.Fatal(err)
	}
	period := &model.AcademicPeriod{
		ID: model.NewAcademicPeriodID(), Owner: model.NewInstitutionAcademicPeriodOwner(model.NewInstitutionID()),
		Name: "2026", DisplayName: "2026", StartsAt: time.Now().UTC(),
		EndsAt: time.Now().UTC().Add(time.Hour), CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(), Revision: 1,
	}
	periods := &layerOrderPeriods{period: period}
	cache, err := localcachelayer.NewMemoryCache(8)
	if err != nil {
		t.Fatal(err)
	}
	var timedReads atomic.Int64
	owner, err := openRuntimeInfrastructure(context.Background(), "", TestingOverrides{
		Configuration: configuration,
		Logger:        logger,
		Persistence:   layerOrderStore{periods: periods},
		Cache:         releaseCache{}, Mailer: releaseMailer{}, Filesystem: releaseVFS{}, Cluster: releaseCluster{},
		StoreLocalCache: cache,
		StoreMetrics: timerlayer.RecorderFunc(func(operation timerlayer.Operation, _ timerlayer.Outcome, _ time.Duration) {
			if operation.String() == "academic_period.get" {
				timedReads.Add(1)
			}
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.release() })
	for range 2 {
		if _, err := owner.persistence.AcademicPeriod().Get(context.Background(), period.ID.String()); err != nil {
			t.Fatal(err)
		}
	}
	if got := periods.reads.Load(); got != 1 {
		t.Fatalf("authoritative reads = %d, want 1", got)
	}
	if got := timedReads.Load(); got != 1 {
		t.Fatalf("timed reads = %d, want 1 because local cache is outermost", got)
	}
}

func TestRootSelectsLocalDevelopmentInfrastructure(t *testing.T) {
	t.Parallel()

	settings := config.Default()
	settings.VFS.Local.Root = t.TempDir()
	logger, err := mlog.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logger.Shutdown() })

	cache, err := newCache(settings.Cache)
	if err != nil {
		t.Fatalf("construct memory cache: %v", err)
	}
	t.Cleanup(func() { _ = cache.Close() })
	if err := cache.Ping(context.Background()); err != nil {
		t.Fatalf("memory cache health: %v", err)
	}

	mailer, err := newMailer(settings.Mail)
	if err != nil {
		t.Fatalf("construct disabled mailer: %v", err)
	}
	t.Cleanup(func() { _ = mailer.Close() })
	if mailer.Enabled() {
		t.Fatal("disabled mail configuration selected an enabled mailer")
	}

	filesystem, err := newVFS(settings.VFS)
	if err != nil {
		t.Fatalf("construct local VFS: %v", err)
	}
	if _, err := filesystem.List(context.Background(), vfspkg.ListOptions{Limit: 1}); err != nil {
		t.Fatalf("local VFS health: %v", err)
	}

	cluster, err := newCluster(settings.Cluster, logger, nil, "test")
	if err != nil {
		t.Fatalf("construct local cluster: %v", err)
	}
	t.Cleanup(func() { _ = cluster.Stop(context.Background()) })
	if cluster.NodeID() != settings.Cluster.NodeID {
		t.Fatalf("cluster node ID = %q, want %q", cluster.NodeID(), settings.Cluster.NodeID)
	}

	providers, err := newExternalAuthenticationRegistry()
	if err != nil {
		t.Fatalf("construct external authentication registry: %v", err)
	}
	if err := providers.Configure(settings.Authentication.External); err != nil {
		t.Fatalf("configure external authentication registry: %v", err)
	}
}

func TestRootSelectsConfiguredSMTPVFSAndIdentityAdapters(t *testing.T) {
	t.Parallel()

	settings := config.Default()

	settings.Mail.Enabled = true
	settings.Mail.Backend = "smtp"
	mailer, err := newMailer(settings.Mail)
	if err != nil {
		t.Fatalf("construct SMTP mailer: %v", err)
	}
	if !mailer.Enabled() {
		t.Fatal("enabled SMTP configuration selected a disabled mailer")
	}

	settings.VFS.Backend = "s3"
	settings.VFS.S3.Endpoint = "s3.example.test"
	settings.VFS.S3.Bucket = "proctor"
	if _, err := newVFS(settings.VFS); err != nil {
		t.Fatalf("construct S3 VFS: %v", err)
	}

	providers, err := newExternalAuthenticationRegistry()
	if err != nil {
		t.Fatal(err)
	}
	external := settings.Authentication.External
	external.Providers = []config.ExternalAuthenticationProvider{
		{
			ID: "campus-cas", Type: config.ExternalAuthenticationTypeCAS,
			DisplayName: "Campus CAS", Enabled: true,
			CAS: &config.CASProvider{
				BaseURL: "https://cas.example.test/cas", ValidationPath: "/p3/serviceValidate",
				Timeout: config.Duration{Duration: 5 * time.Second}, MaxResponseBytes: 64 << 10,
			},
			Claims: config.ExternalClaimMapping{Subject: "user"},
		},
		{
			ID: "campus-oidc", Type: config.ExternalAuthenticationTypeOIDC,
			DisplayName: "Campus OIDC", Enabled: true,
			OIDC: &config.OIDCProvider{
				Issuer: "https://oidc.example.test", ClientID: "proctor",
				Scopes: []string{"openid"}, Timeout: config.Duration{Duration: 5 * time.Second},
				MaxResponseBytes: 64 << 10,
			},
			Claims: config.ExternalClaimMapping{Subject: "sub"},
		},
	}
	if err := providers.Configure(external); err != nil {
		t.Fatalf("configure CAS and OIDC adapters: %v", err)
	}
	if descriptors := providers.Descriptors(); len(descriptors) != 2 {
		t.Fatalf("external authentication descriptors = %#v", descriptors)
	}
}
