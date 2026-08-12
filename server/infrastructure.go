// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/redis/rueidis"

	cachepkg "github.com/sudosylabs/proctor/packages/cache"
	memorycache "github.com/sudosylabs/proctor/packages/cache/memory"
	rediscache "github.com/sudosylabs/proctor/packages/cache/redis"
	mailpkg "github.com/sudosylabs/proctor/packages/mail"
	smtpmail "github.com/sudosylabs/proctor/packages/mail/smtp"
	vfspkg "github.com/sudosylabs/proctor/packages/vfs"
	localvfs "github.com/sudosylabs/proctor/packages/vfs/local"
	s3vfs "github.com/sudosylabs/proctor/packages/vfs/s3"
	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/app/api"
	"github.com/sudosylabs/proctor/server/cluster"
	"github.com/sudosylabs/proctor/server/cluster/local"
	clustermemberlist "github.com/sudosylabs/proctor/server/cluster/memberlist"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/filecontent"
	"github.com/sudosylabs/proctor/server/mlog"
	"github.com/sudosylabs/proctor/server/platform"
	"github.com/sudosylabs/proctor/server/platform/externalauth"
	externalauthcas "github.com/sudosylabs/proctor/server/platform/externalauth/cas"
	externalauthoidc "github.com/sudosylabs/proctor/server/platform/externalauth/oidc"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/localcachelayer"
	"github.com/sudosylabs/proctor/server/store/retrylayer"
	"github.com/sudosylabs/proctor/server/store/sqlstore"
	"github.com/sudosylabs/proctor/server/store/timerlayer"
	"github.com/sudosylabs/proctor/server/websocket"
)

// ownedInfrastructure is the sole owner of infrastructure while the root is
// acquiring and decorating it. Ownership is transferred as one unit to the
// Platform boundary; before then, release is the only cleanup path.
type ownedInfrastructure struct {
	configuration          *config.Store
	logger                 *mlog.Logger
	persistence            store.Store
	cache                  platform.Cache
	cluster                platform.Cluster
	mailer                 platform.Mailer
	filesystem             vfspkg.FileSystem
	externalAuthentication *externalauth.Registry
}

// assembledRuntime retains the assembled graph handles beside the lifecycle
// components so test construction can inspect the same graph production runs.
type assembledRuntime struct {
	components  runtimeComponents
	platform    *platform.Service
	application *app.App
	transport   *api.API
	readiness   *app.Health
}

func constructRuntime(ctx context.Context, configPath string) (runtimeComponents, error) {
	assembled, err := assembleRuntime(ctx, configPath, TestingOverrides{})
	if err != nil {
		return runtimeComponents{}, err
	}
	return assembled.components, nil
}

func assembleRuntime(
	ctx context.Context,
	configPath string,
	overrides TestingOverrides,
) (*assembledRuntime, error) {
	infrastructure, err := openRuntimeInfrastructure(ctx, configPath, overrides)
	if err != nil {
		return nil, err
	}
	applicationPlatform, err := platform.New(platform.ServiceConfig{
		Context:                ctx,
		ConfigStore:            infrastructure.configuration,
		Logger:                 infrastructure.logger,
		Store:                  infrastructure.persistence,
		Cache:                  infrastructure.cache,
		Cluster:                infrastructure.cluster,
		Mailer:                 infrastructure.mailer,
		VFS:                    infrastructure.filesystem,
		ExternalAuthentication: infrastructure.externalAuthentication,
	})
	if err != nil {
		return nil, err
	}
	// Construct stateless domain-aware content mechanics over the VFS selected
	// by the sole composition root. platform.Service retains VFS lifecycle
	// ownership; File Content neither starts nor closes infrastructure.
	content, err := filecontent.New(applicationPlatform.VFS())
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("construct file content: %w", err),
			applicationPlatform.Close(),
		)
	}
	applicationDeps, err := applicationDependencies(applicationPlatform, content)
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("project application dependencies: %w", err),
			applicationPlatform.Close(),
		)
	}
	application, err := app.New(applicationDeps)
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("construct application: %w", err),
			applicationPlatform.Close(),
		)
	}
	clusterFanout, err := newRealtimeClusterAdapter(applicationPlatform.Cluster())
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("construct realtime cluster adapter: %w", err),
			applicationPlatform.Close(),
		)
	}
	if err := application.AttachRealtimeClusterFanout(clusterFanout); err != nil {
		return nil, errors.Join(
			fmt.Errorf("attach realtime cluster fan-out: %w", err),
			applicationPlatform.Close(),
		)
	}
	readiness := &app.Health{}
	cfg := applicationPlatform.Config()
	buildInfo := overrides.BuildInfo
	if buildInfo == (api.BuildInfo{}) {
		current := app.CurrentBuildInfo()
		buildInfo = api.BuildInfo{
			Version: current.Version, Commit: current.Commit,
			BuildTime: current.BuildTime, GoVersion: current.GoVersion,
		}
	}
	webSocketHub, err := websocket.NewHub(
		application,
		websocketLogger{log: applicationPlatform.Log()},
		cfg.Server.PublicURL,
		applicationPlatform.Cluster().NodeID(),
	)
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("construct WebSocket hub: %w", err),
			applicationPlatform.Close(),
		)
	}
	if err := application.AttachRealtimeSink(webSocketHub); err != nil {
		return nil, errors.Join(
			fmt.Errorf("attach realtime sink: %w", err),
			webSocketHub.Close(),
			applicationPlatform.Close(),
		)
	}
	httpAPI, err := api.New(api.Options{
		Logger:                  apiLogger{log: applicationPlatform.Log()},
		Health:                  readiness,
		Application:             application,
		AcademicUnits:           application,
		Institutions:            application,
		Programmes:              application,
		ProgrammeLevels:         application,
		AcademicPeriods:         application,
		Classes:                 application,
		Affiliations:            application,
		AcademicUnitMembers:     application,
		ClassMembers:            application,
		UserProfiles:            application,
		AccountStates:           application,
		SessionAdministrations:  application,
		Roles:                   application,
		RoleBindings:            application,
		AuditListings:           application,
		Bootstrap:               application,
		BuildInfo:               buildInfo,
		PublicURL:               cfg.Server.PublicURL,
		MaxBodyBytes:            cfg.Server.MaxBodyBytes,
		RecentAuthenticationTTL: cfg.Authentication.RecentAuthenticationTTL.Duration,
		NodeID:                  applicationPlatform.Cluster().NodeID(),
		WebSocket:               webSocketHub,
	})
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("construct HTTP API: %w", err),
			webSocketHub.Close(),
			applicationPlatform.Close(),
		)
	}
	var jobRuntime runtimeJobs
	if runner := application.Jobs(); runner != nil {
		jobRuntime = runner
	}
	return &assembledRuntime{
		components: runtimeComponents{
			platform:  applicationPlatform,
			jobs:      jobRuntime,
			transport: httpAPI,
			websocket: webSocketHub,
			readiness: readiness,
			listen:    net.Listen,
			newHTTP:   newHTTPServer,
		},
		platform:    applicationPlatform,
		application: application,
		transport:   httpAPI,
		readiness:   readiness,
	}, nil
}

func openRuntimeInfrastructure(
	ctx context.Context,
	configPath string,
	overrides TestingOverrides,
) (result ownedInfrastructure, resultErr error) {
	result = ownedInfrastructure{
		configuration: overrides.Configuration,
		logger:        overrides.Logger,
		persistence:   overrides.Persistence,
		cache:         overrides.Cache,
		cluster:       overrides.Cluster,
		mailer:        overrides.Mailer,
		filesystem:    overrides.Filesystem,
	}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, result.release())
		}
	}()

	if err := checkAcquisitionContext(ctx, "begin infrastructure acquisition"); err != nil {
		return result, err
	}
	if result.configuration == nil {
		configuration, err := openConfiguration(ctx, configPath)
		if err != nil {
			return result, fmt.Errorf("acquire configuration: %w", err)
		}
		result.configuration = configuration
	}
	if err := checkAcquisitionContext(ctx, "acquire configuration"); err != nil {
		return result, err
	}

	if result.logger == nil {
		logger, err := mlog.New()
		if err != nil {
			return result, fmt.Errorf("create logger: %w", err)
		}
		result.logger = logger
	}
	if err := checkAcquisitionContext(ctx, "acquire logger"); err != nil {
		return result, err
	}

	cfg := result.configuration.Get()
	if result.persistence == nil {
		persistence, err := sqlstore.New(ctx, sqlstore.SettingsFromConfig(cfg.Database))
		if err != nil {
			return result, fmt.Errorf("open database: %w", err)
		}
		result.persistence = persistence
	}
	if err := checkAcquisitionContext(ctx, "acquire persistence"); err != nil {
		return result, err
	}
	storeRetry := retrylayer.DefaultPolicy(sqlstore.IsTransientError)
	if overrides.StoreRetry != nil {
		storeRetry = *overrides.StoreRetry
	}
	retriedPersistence, err := retrylayer.New(result.persistence, storeRetry)
	if err != nil {
		return result, fmt.Errorf("construct store retry layer: %w", err)
	}
	result.replacePersistence(retriedPersistence)
	if err := checkAcquisitionContext(ctx, "decorate persistence with retry"); err != nil {
		return result, err
	}
	storeMetrics := overrides.StoreMetrics
	if storeMetrics == nil {
		storeMetrics = timerlayer.NopRecorder{}
	}
	timedPersistence, err := timerlayer.New(result.persistence, storeMetrics)
	if err != nil {
		return result, fmt.Errorf("construct store timing layer: %w", err)
	}
	result.replacePersistence(timedPersistence)
	if err := checkAcquisitionContext(ctx, "decorate persistence with timing"); err != nil {
		return result, err
	}
	if result.cache == nil {
		cache, err := newCache(cfg.Cache)
		if err != nil {
			return result, fmt.Errorf("open cache: %w", err)
		}
		result.cache = cache
	}
	if err := checkAcquisitionContext(ctx, "acquire cache"); err != nil {
		return result, err
	}
	if result.mailer == nil {
		mailer, err := newMailer(cfg.Mail)
		if err != nil {
			return result, fmt.Errorf("open mail transport: %w", err)
		}
		result.mailer = mailer
	}
	if err := checkAcquisitionContext(ctx, "acquire mail transport"); err != nil {
		return result, err
	}
	if result.filesystem == nil {
		filesystem, err := newVFS(cfg.VFS)
		if err != nil {
			return result, fmt.Errorf("open VFS: %w", err)
		}
		result.filesystem = filesystem
	}
	if err := checkAcquisitionContext(ctx, "acquire VFS"); err != nil {
		return result, err
	}
	if result.cluster == nil {
		var discovery store.ClusterDiscoveryStore
		if result.persistence != nil {
			discovery = result.persistence.ClusterDiscovery()
		}
		cluster, err := newCluster(cfg.Cluster, result.logger, discovery, app.Version)
		if err != nil {
			return result, fmt.Errorf("open cluster transport: %w", err)
		}
		result.cluster = cluster
	}
	if err := checkAcquisitionContext(ctx, "acquire cluster transport"); err != nil {
		return result, err
	}
	storeLocalCache := overrides.StoreLocalCache
	if storeLocalCache == nil {
		storeLocalCache, err = localcachelayer.NewMemoryCache(1_024)
		if err != nil {
			return result, fmt.Errorf("construct local store cache: %w", err)
		}
	}
	storeCachePolicy := localcachelayer.DefaultPolicy()
	if overrides.StoreCachePolicy != nil {
		storeCachePolicy = *overrides.StoreCachePolicy
	}
	storeCacheMetrics := overrides.StoreCacheMetrics
	if storeCacheMetrics == nil {
		storeCacheMetrics = localcachelayer.NopRecorder{}
	}
	cacheInvalidation, err := newLocalCacheClusterAdapter(result.cluster)
	if err != nil {
		return result, fmt.Errorf("construct local-cache invalidation adapter: %w", err)
	}
	cachedPersistence, err := localcachelayer.New(
		result.persistence,
		storeLocalCache,
		storeCachePolicy,
		storeCacheMetrics,
		cacheInvalidation,
	)
	if err != nil {
		return result, fmt.Errorf("construct local-cache store layer: %w", err)
	}
	result.replacePersistence(cachedPersistence)
	if err := checkAcquisitionContext(ctx, "decorate persistence with local cache"); err != nil {
		return result, err
	}
	externalAuthentication, err := newExternalAuthenticationRegistry()
	if err != nil {
		return result, fmt.Errorf("construct external authentication registry: %w", err)
	}
	result.externalAuthentication = externalAuthentication
	if err := checkAcquisitionContext(ctx, "acquire external authentication registry"); err != nil {
		return result, err
	}
	return result, nil
}

func checkAcquisitionContext(ctx context.Context, phase string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%s: %w", phase, err)
	}
	return nil
}

func (i *ownedInfrastructure) replacePersistence(next store.Store) {
	i.persistence = next
}

func (i *ownedInfrastructure) release() error {
	var clusterErr error
	if i.cluster != nil {
		shutdownTimeout := 15 * time.Second
		if i.configuration != nil {
			shutdownTimeout = i.configuration.Get().Server.ShutdownTimeout.Duration
		}
		stopCtx, cancelStop := context.WithTimeout(context.Background(), shutdownTimeout)
		clusterErr = i.cluster.Stop(stopCtx)
		cancelStop()
	}
	var vfsErr error
	if closer, ok := i.filesystem.(interface{ Close() error }); ok {
		vfsErr = closer.Close()
	}
	var mailErr error
	if i.mailer != nil {
		mailErr = i.mailer.Close()
	}
	var cacheErr error
	if i.cache != nil {
		cacheErr = i.cache.Close()
	}
	var storeErr error
	if i.persistence != nil {
		storeErr = i.persistence.Close()
	}
	var loggerErr error
	if i.logger != nil {
		loggerErr = i.logger.Shutdown()
	}
	var configErr error
	if i.configuration != nil {
		configErr = i.configuration.Close()
	}
	return errors.Join(
		clusterErr,
		vfsErr,
		mailErr,
		cacheErr,
		storeErr,
		loggerErr,
		configErr,
	)
}

func newCache(settings config.Cache) (platform.Cache, error) {
	switch settings.Backend {
	case "memory":
		store, err := memorycache.New(cachepkg.BytesCodec())
		if err != nil {
			return nil, err
		}
		return platform.NewCacheAdapter(memoryCacheBackend{store: store}), nil
	case "redis":
		clientOption := rueidis.ClientOption{
			InitAddress: append([]string(nil), settings.Redis.Addresses...),
			Username:    settings.Redis.Username,
			Password:    settings.Redis.Password,
			SelectDB:    settings.Redis.Database,
			ClientName:  "proctor",
			Dialer: net.Dialer{
				Timeout:   settings.Redis.ConnectTimeout.Duration,
				KeepAlive: 30 * time.Second,
			},
		}
		if settings.Redis.TLS {
			clientOption.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		}
		client, err := rueidis.NewClient(clientOption)
		if err != nil {
			return nil, fmt.Errorf("create Redis client: %w", err)
		}
		store, err := rediscache.New(
			client,
			cachepkg.BytesCodec(),
			rediscache.Config{Namespace: settings.Namespace},
		)
		if err != nil {
			client.Close()
			return nil, err
		}
		return platform.NewCacheAdapter(redisCacheBackend{store: store, client: client}), nil
	default:
		return nil, fmt.Errorf("unsupported cache backend %q", settings.Backend)
	}
}

type memoryCacheBackend struct {
	store *memorycache.Store[[]byte]
}

func (b memoryCacheBackend) Get(ctx context.Context, key string) ([]byte, error) {
	return b.store.Get(ctx, key)
}

func (b memoryCacheBackend) Set(ctx context.Context, key string, value []byte, options cachepkg.SetOptions) error {
	return b.store.Set(ctx, key, value, options)
}

func (b memoryCacheBackend) Delete(ctx context.Context, key string) error {
	return b.store.Delete(ctx, key)
}

func (b memoryCacheBackend) Add(ctx context.Context, key string, delta int64, options cachepkg.CounterOptions) (int64, error) {
	return b.store.Add(ctx, key, delta, options)
}

type redisCacheBackend struct {
	store  *rediscache.Store[[]byte]
	client rueidis.Client
}

func (b redisCacheBackend) Get(ctx context.Context, key string) ([]byte, error) {
	return b.store.Get(ctx, key)
}

func (b redisCacheBackend) Set(ctx context.Context, key string, value []byte, options cachepkg.SetOptions) error {
	return b.store.Set(ctx, key, value, options)
}

func (b redisCacheBackend) Delete(ctx context.Context, key string) error {
	return b.store.Delete(ctx, key)
}

func (b redisCacheBackend) Add(ctx context.Context, key string, delta int64, options cachepkg.CounterOptions) (int64, error) {
	return b.store.Add(ctx, key, delta, options)
}

func (b redisCacheBackend) Ping(ctx context.Context) error {
	return b.client.Do(ctx, b.client.B().Ping().Build()).Error()
}

func (b redisCacheBackend) Close() error {
	b.client.Close()
	return nil
}

func newMailer(settings config.Mail) (platform.Mailer, error) {
	from := mailpkg.Address{Name: settings.FromName, Address: settings.FromAddress}
	if !settings.Enabled {
		return platform.NewDisabledMailer(from), nil
	}
	if settings.Backend != "smtp" {
		return nil, fmt.Errorf("unsupported mail backend %q", settings.Backend)
	}
	sender, err := smtpmail.New(smtpmail.Config{
		Address:         settings.SMTP.Address,
		ServerName:      settings.SMTP.ServerName,
		LocalName:       settings.SMTP.LocalName,
		Security:        smtpmail.Security(settings.SMTP.Security),
		Username:        settings.SMTP.Username,
		Password:        settings.SMTP.Password,
		Authentication:  smtpmail.Authentication(settings.SMTP.Authentication),
		Timeout:         settings.SMTP.Timeout.Duration,
		MessageIDDomain: settings.SMTP.MessageIDDomain,
		MaxMessageBytes: settings.SMTP.MaxMessageBytes,
		MaxRecipients:   settings.SMTP.MaxRecipients,
	})
	if err != nil {
		return nil, err
	}
	return platform.NewMailAdapter(true, from, sender), nil
}

func newVFS(settings config.VFS) (vfspkg.FileSystem, error) {
	switch settings.Backend {
	case "local":
		return localvfs.New(settings.Local.Root)
	case "s3":
		return s3vfs.New(s3vfs.Config{
			Endpoint:     settings.S3.Endpoint,
			AccessKey:    settings.S3.AccessKey,
			SecretKey:    settings.S3.SecretKey,
			SessionToken: settings.S3.SessionToken,
			Bucket:       settings.S3.Bucket,
			Prefix:       settings.S3.Prefix,
			Region:       settings.S3.Region,
			Secure:       settings.S3.Secure,
		})
	default:
		return nil, fmt.Errorf("unsupported VFS backend %q", settings.Backend)
	}
}

func newCluster(
	settings config.Cluster,
	logger *mlog.Logger,
	discovery store.ClusterDiscoveryStore,
	serverVersion string,
) (cluster.Transport, error) {
	switch settings.Backend {
	case "local":
		return local.New(settings.NodeID, mlogClusterLogger{log: logger.With(
			mlog.String("component", "cluster"),
			mlog.String("node_id", settings.NodeID),
		)})
	case "memberlist":
		if discovery == nil {
			return nil, fmt.Errorf("cluster discovery store is required for memberlist")
		}
		key, err := clustermemberlist.DecodeEncryptionKey(settings.Memberlist.EncryptionKey)
		if err != nil {
			return nil, err
		}
		if serverVersion == "" {
			serverVersion = app.Version
		}
		return clustermemberlist.New(clustermemberlist.Config{
			NodeID:             settings.NodeID,
			BindAddress:        settings.Memberlist.BindAddress,
			AdvertiseAddress:   settings.Memberlist.AdvertiseAddress,
			EncryptionKey:      key,
			SeedAddresses:      append([]string(nil), settings.Memberlist.SeedAddresses...),
			Discovery:          storeClusterDiscovery{store: discovery},
			DiscoveryTTL:       settings.Memberlist.DiscoveryTTL.Duration,
			DiscoveryHeartbeat: settings.Memberlist.DiscoveryHeartbeat.Duration,
			ProtocolMin:        settings.Memberlist.ProtocolMin,
			ProtocolMax:        settings.Memberlist.ProtocolMax,
			ServerVersion:      serverVersion,
			AllowPublicBind:    settings.Memberlist.AllowPublicBind,
			Logger: mlogClusterLogger{log: logger.With(
				mlog.String("component", "cluster"),
				mlog.String("node_id", settings.NodeID),
				mlog.String("backend", "memberlist"),
			)},
		})
	default:
		return nil, fmt.Errorf("unsupported cluster backend %q", settings.Backend)
	}
}

// storeClusterDiscovery adapts store.ClusterDiscoveryStore to cluster.DiscoveryStore.
type storeClusterDiscovery struct {
	store store.ClusterDiscoveryStore
}

func (s storeClusterDiscovery) Upsert(ctx context.Context, node cluster.DiscoveryNode) error {
	return s.store.Upsert(ctx, &store.ClusterDiscoveryNode{
		NodeID:           node.NodeID,
		AdvertiseAddress: node.AdvertiseAddress,
		ServerVersion:    node.ServerVersion,
		ProtocolMin:      node.ProtocolMin,
		ProtocolMax:      node.ProtocolMax,
		ExpiresAt:        node.ExpiresAt.UTC().UnixMilli(),
		UpdatedAt:        node.UpdatedAt.UTC().UnixMilli(),
	})
}

func (s storeClusterDiscovery) ListLive(ctx context.Context, now time.Time) ([]cluster.DiscoveryNode, error) {
	rows, err := s.store.ListLive(ctx, now.UTC().UnixMilli())
	if err != nil {
		return nil, err
	}
	result := make([]cluster.DiscoveryNode, 0, len(rows))
	for _, row := range rows {
		if row == nil {
			continue
		}
		result = append(result, cluster.DiscoveryNode{
			NodeID:           row.NodeID,
			AdvertiseAddress: row.AdvertiseAddress,
			ServerVersion:    row.ServerVersion,
			ProtocolMin:      row.ProtocolMin,
			ProtocolMax:      row.ProtocolMax,
			ExpiresAt:        time.UnixMilli(row.ExpiresAt).UTC(),
			UpdatedAt:        time.UnixMilli(row.UpdatedAt).UTC(),
		})
	}
	return result, nil
}

func (s storeClusterDiscovery) Delete(ctx context.Context, nodeID string) error {
	return s.store.Delete(ctx, nodeID)
}

func (s storeClusterDiscovery) DeleteExpired(ctx context.Context, now time.Time) (int64, error) {
	return s.store.DeleteExpired(ctx, now.UTC().UnixMilli())
}

// mlogClusterLogger adapts mlog to cluster.Logger at the composition root.
type mlogClusterLogger struct {
	log *mlog.Logger
}

func (l mlogClusterLogger) ErrorContext(ctx context.Context, message string, err error) {
	if l.log == nil {
		return
	}
	fields := []mlog.Field{}
	if err != nil {
		fields = append(fields, mlog.Err(err))
	}
	l.log.ErrorContext(ctx, message, fields...)
}

func newExternalAuthenticationRegistry() (*externalauth.Registry, error) {
	return externalauth.NewRegistry(
		externalauthcas.NewFactory(),
		externalauthoidc.NewFactory(),
	)
}
