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
	appexecution "github.com/sudosylabs/proctor/server/app/execution"
	"github.com/sudosylabs/proctor/server/cluster"
	"github.com/sudosylabs/proctor/server/cluster/local"
	clustermemberlist "github.com/sudosylabs/proctor/server/cluster/memberlist"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/executionhost"
	"github.com/sudosylabs/proctor/server/logging"
	"github.com/sudosylabs/proctor/server/platform"
	"github.com/sudosylabs/proctor/server/platform/externalauth"
	externalauthcas "github.com/sudosylabs/proctor/server/platform/externalauth/cas"
	externalauthoidc "github.com/sudosylabs/proctor/server/platform/externalauth/oidc"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/localcachelayer"
	"github.com/sudosylabs/proctor/server/store/retrylayer"
	"github.com/sudosylabs/proctor/server/store/sqlstore"
	"github.com/sudosylabs/proctor/server/store/timerlayer"
)

const (
	storeLocalCacheMaxEntries = 1_024
	storeLocalCacheMaxBytes   = 64 << 20
)

// ownedInfrastructure is the sole owner of infrastructure while the root is
// acquiring and decorating it. Ownership is transferred as one unit to the
// Platform boundary; before then, release is the only cleanup path.
type ownedInfrastructure struct {
	configuration          *config.Store
	logger                 *logging.Logger
	persistence            store.Store
	cache                  platform.Cache
	cluster                platform.Cluster
	mailer                 platform.Mailer
	filesystem             vfspkg.FileSystem
	externalAuthentication *externalauth.Registry
	executionHosts         executionHostDirectory
	migration              *sqlstore.MigrationResult
}

type executionHostDirectory interface {
	appexecution.HostDirectory
	platform.ExecutionHosts
}

// constructionCapabilities is a short-lived, non-owning projection used only
// while the root assembles consumers. It has no lifecycle operations and must
// not escape composeNode; Platform owns every referenced capability after
// acceptance.
type constructionCapabilities struct {
	logger                 runtimeLogger
	persistence            store.Catalog
	cache                  borrowedCache
	cluster                borrowedCluster
	mailer                 borrowedMailer
	filesystem             vfspkg.FileSystem
	externalAuthentication *externalauth.Registry
	executionHosts         appexecution.HostDirectory
	nodeID                 string
	migration              *sqlstore.MigrationResult
}

type borrowedCache interface {
	Get(context.Context, string) ([]byte, error)
	Set(context.Context, string, []byte, time.Duration, platform.CacheCondition) error
	Delete(context.Context, string) error
	Add(context.Context, string, int64, time.Duration) (int64, error)
}

type borrowedMailer interface {
	Enabled() bool
	From() mailpkg.Address
	Send(context.Context, mailpkg.Message) (mailpkg.Receipt, error)
	Test(context.Context) error
}

type borrowedCluster interface {
	NodeID() string
	RegisterHandler(cluster.Event, cluster.Handler) error
	Broadcast(context.Context, *cluster.Message) error
}

func openRuntimeInfrastructure(
	ctx context.Context,
	configPath string,
	overrides TestingOverrides,
) (result ownedInfrastructure, resultErr error) {
	result = ownedInfrastructure{
		configuration:  overrides.Configuration,
		logger:         overrides.Logger,
		persistence:    overrides.Persistence,
		cache:          overrides.Cache,
		cluster:        overrides.Cluster,
		mailer:         overrides.Mailer,
		filesystem:     overrides.Filesystem,
		executionHosts: overrides.ExecutionHosts,
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
		logger, err := logging.New()
		if err != nil {
			return result, fmt.Errorf("create logger: %w", err)
		}
		result.logger = logger
	}
	if err := checkAcquisitionContext(ctx, "acquire logger"); err != nil {
		return result, err
	}

	cfg := result.configuration.Get()
	if result.executionHosts == nil {
		directory, err := newExecutionHostDirectory(cfg.Execution)
		if err != nil {
			return result, fmt.Errorf("open execution hosts: %w", err)
		}
		result.executionHosts = directory
	}
	if err := checkAcquisitionContext(ctx, "acquire execution hosts"); err != nil {
		return result, err
	}
	if result.persistence == nil {
		persistence, err := sqlstore.New(ctx, sqlstore.SettingsFromConfig(cfg.Database))
		if err != nil {
			return result, fmt.Errorf("open database: %w", err)
		}
		migration, err := persistence.ApplyPendingMigrations(ctx)
		if err != nil {
			return result, errors.Join(
				fmt.Errorf("migrate database: %w", err),
				persistence.Close(),
			)
		}
		result.migration = &migration
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
		storeLocalCache, err = newStoreLocalMemoryCache(storeLocalCacheMaxEntries)
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

func newStoreLocalMemoryCache(maxEntries int) (localcachelayer.Cache, error) {
	store, err := memorycache.New(cachepkg.BytesCodec(), memorycache.Config{
		MaxEntries: maxEntries,
		MaxBytes:   storeLocalCacheMaxBytes,
	})
	if err != nil {
		return nil, err
	}
	return localcachelayer.NewCacheAdapter(store)
}

func checkAcquisitionContext(ctx context.Context, phase string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%s: %w", phase, err)
	}
	return nil
}

func logStartupInfrastructure(
	snapshot config.Config,
	logger runtimeLogger,
	migration *sqlstore.MigrationResult,
) {
	if logger == nil {
		return
	}
	if migration != nil {
		logger.Info(
			"database migrations complete",
			logging.Int("applied", migration.Applied),
			logging.Int("schema_version", migration.SchemaVersion),
		)
	}
	logger.Info(
		"store cache ready",
		logging.String("backend", "memory_lru"),
		logging.Int("max_entries", storeLocalCacheMaxEntries),
		logging.Int64("max_bytes", storeLocalCacheMaxBytes),
		logging.String("invalidation_backend", snapshot.Cluster.Backend),
	)
}

func (i *ownedInfrastructure) acceptPlatform(
	ctx context.Context,
) (*platform.Service, config.Config, constructionCapabilities, error) {
	capabilities := constructionCapabilities{
		logger:                 i.logger,
		persistence:            i.persistence,
		cache:                  i.cache,
		cluster:                i.cluster,
		mailer:                 i.mailer,
		filesystem:             i.filesystem,
		externalAuthentication: i.externalAuthentication,
		executionHosts:         i.executionHosts,
		migration:              i.migration,
	}
	if i.cluster != nil {
		capabilities.nodeID = i.cluster.NodeID()
	}
	resources := platform.OwnedResources{
		Configuration:          i.configuration,
		Logger:                 i.logger,
		Persistence:            i.persistence,
		Cache:                  i.cache,
		Cluster:                i.cluster,
		Mailer:                 i.mailer,
		VFS:                    i.filesystem,
		ExternalAuthentication: i.externalAuthentication,
		ExecutionHosts:         i.executionHosts,
	}
	*i = ownedInfrastructure{}
	service, snapshot, err := platform.Accept(ctx, resources)
	if err != nil {
		return nil, config.Config{}, constructionCapabilities{}, err
	}
	return service, snapshot, capabilities, nil
}

func (i *ownedInfrastructure) replacePersistence(next store.Store) {
	i.persistence = next
}

func (i *ownedInfrastructure) release() error {
	shutdownTimeout := 15 * time.Second
	if i.configuration != nil {
		shutdownTimeout = i.configuration.Get().Server.ShutdownTimeout.Duration
	}
	stopCtx, cancelStop := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancelStop()
	var clusterErr error
	if i.cluster != nil {
		clusterErr = i.cluster.Stop(stopCtx)
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
	var executionErr error
	if i.executionHosts != nil {
		executionErr = i.executionHosts.Close()
	}
	var loggerErr error
	if i.logger != nil {
		loggerErr = i.logger.Shutdown(stopCtx)
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
		executionErr,
		loggerErr,
		configErr,
	)
}

func newExecutionHostDirectory(settings config.Execution) (*executionhost.Directory, error) {
	hosts := make([]executionhost.HostConfig, 0, len(settings.Hosts))
	for _, host := range settings.Hosts {
		hosts = append(hosts, executionhost.HostConfig{
			ID: host.ID, Address: host.Address, Security: host.Security, Token: host.Token,
			ServerName: host.ServerName, CAFile: host.CAFile,
			ClientCertificateFile: host.ClientCertificateFile, ClientKeyFile: host.ClientKeyFile,
		})
	}
	return executionhost.New(executionhost.Settings{Enabled: settings.Enabled,
		DialTimeout: settings.DialTimeout.Duration, OperationTimeout: settings.OperationTimeout.Duration, Hosts: hosts})
}

func newCache(settings config.Cache) (platform.Cache, error) {
	switch settings.Backend {
	case "memory":
		store, err := memorycache.New(cachepkg.BytesCodec(), memorycache.Config{
			MaxEntries: settings.Memory.MaxEntries,
			MaxBytes:   settings.Memory.MaxBytes,
		})
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
	logger *logging.Logger,
	discovery store.ClusterDiscoveryStore,
	serverVersion string,
) (cluster.Transport, error) {
	switch settings.Backend {
	case "local":
		return local.New(settings.NodeID, loggingClusterLogger{log: logger.With(
			logging.String("component", "cluster"),
			logging.String("node_id", settings.NodeID),
		)})
	case "memberlist":
		if discovery == nil {
			return nil, fmt.Errorf("cluster discovery store is required for memberlist")
		}
		key, decryptionKeys, err := clustermemberlist.DecodeEncryptionKeyring(
			settings.Memberlist.EncryptionKey,
			settings.Memberlist.DecryptionKeys,
		)
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
			DecryptionKeys:     decryptionKeys,
			SeedAddresses:      append([]string(nil), settings.Memberlist.SeedAddresses...),
			Discovery:          storeClusterDiscovery{store: discovery},
			DiscoveryTTL:       settings.Memberlist.DiscoveryTTL.Duration,
			DiscoveryHeartbeat: settings.Memberlist.DiscoveryHeartbeat.Duration,
			ServerVersion:      serverVersion,
			AllowPublicBind:    settings.Memberlist.AllowPublicBind,
			Logger: loggingClusterLogger{log: logger.With(
				logging.String("component", "cluster"),
				logging.String("node_id", settings.NodeID),
				logging.String("backend", "memberlist"),
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

// loggingClusterLogger adapts logging to cluster.Logger at the composition root.
type loggingClusterLogger struct {
	log *logging.Logger
}

func (l loggingClusterLogger) ErrorContext(ctx context.Context, message string, err error) {
	if l.log == nil {
		return
	}
	fields := []logging.Field{}
	if err != nil {
		fields = append(fields, logging.Err(err))
	}
	l.log.ErrorContext(ctx, message, fields...)
}

func newExternalAuthenticationRegistry() (*externalauth.Registry, error) {
	return externalauth.NewRegistry(
		externalauthcas.NewFactory(),
		externalauthoidc.NewFactory(),
	)
}
