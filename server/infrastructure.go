// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	vfspkg "github.com/sudosylabs/proctor/packages/vfs"
	localvfs "github.com/sudosylabs/proctor/packages/vfs/local"
	s3vfs "github.com/sudosylabs/proctor/packages/vfs/s3"
	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/app/api"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/mlog"
	"github.com/sudosylabs/proctor/server/platform"
	"github.com/sudosylabs/proctor/server/platform/externalauth"
	externalauthcas "github.com/sudosylabs/proctor/server/platform/externalauth/cas"
	externalauthoidc "github.com/sudosylabs/proctor/server/platform/externalauth/oidc"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/sqlstore"
)

type runtimeInfrastructure struct {
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
	application, err := app.New(applicationPlatform)
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("construct application: %w", err),
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
	httpAPI, err := api.New(api.Options{
		Logger:                  applicationPlatform.Log(),
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
		BuildInfo:               buildInfo,
		PublicURL:               cfg.Server.PublicURL,
		MaxBodyBytes:            cfg.Server.MaxBodyBytes,
		RecentAuthenticationTTL: cfg.Authentication.RecentAuthenticationTTL.Duration,
		NodeID:                  applicationPlatform.Cluster().NodeID(),
	})
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("construct HTTP API: %w", err),
			applicationPlatform.Close(),
		)
	}
	if err := application.AttachRealtimeSink(httpAPI); err != nil {
		return nil, errors.Join(
			fmt.Errorf("attach realtime sink: %w", err),
			httpAPI.Close(),
			applicationPlatform.Close(),
		)
	}
	return &assembledRuntime{
		components: runtimeComponents{
			platform:  applicationPlatform,
			transport: httpAPI,
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
) (result runtimeInfrastructure, resultErr error) {
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, result.close())
		}
	}()

	if overrides.Configuration != nil {
		result.configuration = overrides.Configuration
	} else {
		configuration, err := openConfiguration(ctx, configPath)
		if err != nil {
			return result, err
		}
		result.configuration = configuration
	}

	if overrides.Logger != nil {
		result.logger = overrides.Logger
	} else {
		logger, err := mlog.New()
		if err != nil {
			return result, fmt.Errorf("create logger: %w", err)
		}
		result.logger = logger
	}

	cfg := result.configuration.Get()
	if overrides.Persistence != nil {
		result.persistence = overrides.Persistence
	} else {
		persistence, err := sqlstore.New(ctx, sqlstore.SettingsFromConfig(cfg.Database))
		if err != nil {
			return result, fmt.Errorf("open database: %w", err)
		}
		result.persistence = persistence
	}
	if overrides.Cache != nil {
		result.cache = overrides.Cache
	} else {
		cache, err := newCache(cfg.Cache)
		if err != nil {
			return result, fmt.Errorf("open cache: %w", err)
		}
		result.cache = cache
	}
	if overrides.Mailer != nil {
		result.mailer = overrides.Mailer
	} else {
		mailer, err := newMailer(cfg.Mail)
		if err != nil {
			return result, fmt.Errorf("open mail transport: %w", err)
		}
		result.mailer = mailer
	}
	if overrides.Filesystem != nil {
		result.filesystem = overrides.Filesystem
	} else {
		filesystem, err := newVFS(cfg.VFS)
		if err != nil {
			return result, fmt.Errorf("open VFS: %w", err)
		}
		result.filesystem = filesystem
	}
	if overrides.Cluster != nil {
		result.cluster = overrides.Cluster
	} else {
		cluster, err := newCluster(cfg.Cluster, result.logger)
		if err != nil {
			return result, fmt.Errorf("open cluster transport: %w", err)
		}
		result.cluster = cluster
	}
	externalAuthentication, err := newExternalAuthenticationRegistry()
	if err != nil {
		return result, fmt.Errorf("construct external authentication registry: %w", err)
	}
	result.externalAuthentication = externalAuthentication
	return result, nil
}

func (i *runtimeInfrastructure) close() error {
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
		return platform.NewMemoryCache()
	case "redis":
		return platform.NewRedisCache(settings)
	default:
		return nil, fmt.Errorf("unsupported cache backend %q", settings.Backend)
	}
}

func newMailer(settings config.Mail) (platform.Mailer, error) {
	if !settings.Enabled {
		return platform.NewDisabledMailer(settings), nil
	}
	if settings.Backend != "smtp" {
		return nil, fmt.Errorf("unsupported mail backend %q", settings.Backend)
	}
	return platform.NewSMTPMailer(settings)
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

func newCluster(settings config.Cluster, logger *mlog.Logger) (platform.Cluster, error) {
	switch settings.Backend {
	case "local":
		return platform.NewLocalCluster(settings.NodeID, logger)
	case "redis":
		return platform.NewRedisCluster(settings, logger)
	default:
		return nil, fmt.Errorf("unsupported cluster backend %q", settings.Backend)
	}
}

func newExternalAuthenticationRegistry() (*externalauth.Registry, error) {
	return externalauth.NewRegistry(
		externalauthcas.NewFactory(),
		externalauthoidc.NewFactory(),
	)
}
