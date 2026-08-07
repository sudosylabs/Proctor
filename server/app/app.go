// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

// Package app owns Proctor's application use cases and orchestration.
package app

import (
	"context"
	"errors"
	"time"

	vfspkg "github.com/sudosylabs/proctor/packages/vfs"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/mlog"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/platform"
	"github.com/sudosylabs/proctor/server/store"
)

// Composition-root adapters for authentication live here so authentication.go
// stays free of platform and mlog imports while app.go already carries that
// temporary dependency debt (ADR-0004, ticket #29).

// App is the long-lived application facade. Product capabilities will be
// composed here as their contracts become concrete.
type App struct {
	platform               *platform.Service
	authentication         *AuthenticationService
	externalAuthentication *ExternalAuthenticationService
	mfa                    *MFAService
	authorization          *AuthorizationService
	academicUnits          *academicUnitQueryService
	academicUnitCommands   *academicUnitCommandService
	institutions           *institutionService
	programmes             *programmeService
	programmeLevels        *programmeLevelService
	academicPeriods        *academicPeriodService
	classes                *classService
	affiliations           *affiliationService
	academicUnitMembers    *academicUnitMemberService
	classMembers           *classMemberService
	userProfiles           *userProfileService
	accountStates          *accountStateService
	sessionAdministrations *sessionAdministrationService
	roles                  *roleService
	roleBindings           *roleBindingService
	auditListings          *auditListingService
	bootstrap              *bootstrapService
	audit                  *AuditService
	realtime               *RealtimeService
}

func New(applicationPlatform *platform.Service) (*App, error) {
	mfa, err := newMFAService(applicationPlatform)
	if err != nil {
		return nil, err
	}
	authSettings := applicationPlatform.Config().Authentication
	hasher, err := newPasswordHasher(authSettings.Password)
	if err != nil {
		return nil, err
	}
	authentication := newAuthenticationService(
		applicationPlatform.Store(),
		platformAuthenticationCache{cache: applicationPlatform.Cache()},
		hasher,
		mfa,
		SessionPolicy{
			AccessTTL:              authSettings.Sessions.AccessTTL.Duration,
			RefreshTTL:             authSettings.Sessions.RefreshTTL.Duration,
			IdleTTL:                authSettings.Sessions.IdleTTL.Duration,
			AbsoluteTTL:            authSettings.Sessions.AbsoluteTTL.Duration,
			ActivityUpdateInterval: authSettings.Sessions.ActivityUpdateInterval.Duration,
			MaximumPerUser:         authSettings.Sessions.MaximumPerUser,
		},
		LoginRateLimitPolicy{
			Window:                authSettings.LoginRateLimit.Window.Duration,
			MaximumAttempts:       authSettings.LoginRateLimit.MaximumAttempts,
			MaximumSourceAttempts: authSettings.LoginRateLimit.MaximumSourceAttempts,
		},
		PersonalAccessTokenPolicy{
			LastUsedUpdateInterval: authSettings.PersonalAccessTokens.LastUsedUpdateInterval.Duration,
		},
		mlogAuthenticationDiagnostics{log: applicationPlatform.Log()},
		time.Now,
	)
	audit := newAuditService(
		applicationPlatform.Store(),
		applicationPlatform.Cluster().NodeID(),
	)
	externalAuthentication := newExternalAuthenticationService(
		platformExternalProviders{service: applicationPlatform},
		applicationPlatform.Store(),
		platformAuthenticationCache{cache: applicationPlatform.Cache()},
		authentication,
		audit,
		ExternalAuthenticationPolicy{
			PublicURL:     applicationPlatform.Config().Server.PublicURL,
			LoginStateTTL: authSettings.External.LoginStateTTL.Duration,
			LoginRateLimit: LoginRateLimitPolicy{
				Window:                authSettings.LoginRateLimit.Window.Duration,
				MaximumAttempts:       authSettings.LoginRateLimit.MaximumAttempts,
				MaximumSourceAttempts: authSettings.LoginRateLimit.MaximumSourceAttempts,
			},
			NodeID: applicationPlatform.Cluster().NodeID(),
		},
		mlogAuthenticationDiagnostics{log: applicationPlatform.Log()},
		time.Now,
	)
	authorization := newAuthorizationService(applicationPlatform.Store(), audit)
	academicAuthorization := academicUnitAuthorization{
		authorization: authorization,
		institutions:  applicationPlatform.Store().Institution(),
	}
	academicUnits := newAcademicUnitQueryService(
		applicationPlatform.Store().AcademicUnit(), academicAuthorization,
	)
	realtime, err := newRealtimeService(applicationPlatform, authentication)
	if err != nil {
		return nil, err
	}
	authentication.propagateAuthenticationCacheInvalidation =
		realtime.PropagateAuthenticationCacheInvalidation
	authentication.propagateSessionRevocation =
		realtime.PropagateSessionRevocation
	academicUnitCommands := newAcademicUnitCommandService(
		applicationPlatform.Store().AcademicUnit(), academicAuthorization,
		mutationAuditAdapter{audit: audit},
		academicUnitRealtimeEffects{realtime: realtime},
		academicUnitEffectReporter{realtime: realtime},
		time.Now, model.NewId,
	)
	institutions := newInstitutionService(
		applicationPlatform.Store().Institution(), academicAuthorization,
		mutationAuditAdapter{audit: audit}, time.Now,
	)
	programmes := newProgrammeService(
		applicationPlatform.Store().Programme(), academicAuthorization,
		mutationAuditAdapter{audit: audit}, time.Now, model.NewId,
	)
	programmeLevels := newProgrammeLevelService(
		applicationPlatform.Store().ProgrammeLevel(), applicationPlatform.Store().Programme(),
		academicAuthorization, mutationAuditAdapter{audit: audit}, time.Now, model.NewId,
	)
	academicPeriods := newAcademicPeriodService(
		applicationPlatform.Store().AcademicPeriod(), academicAuthorization,
		mutationAuditAdapter{audit: audit}, time.Now, model.NewId,
	)
	classes := newClassService(
		applicationPlatform.Store().Class(), applicationPlatform.Store().ProgrammeLevel(),
		applicationPlatform.Store().Programme(), academicAuthorization,
		mutationAuditAdapter{audit: audit}, time.Now, model.NewId,
	)
	affiliations := newAffiliationService(
		applicationPlatform.Store().Affiliation(), applicationPlatform.Store().ClassMember(),
		academicAuthorization, mutationAuditAdapter{audit: audit}, time.Now, model.NewId,
	)
	academicUnitMembers := newAcademicUnitMemberService(
		applicationPlatform.Store().AcademicUnitMember(), academicAuthorization,
		mutationAuditAdapter{audit: audit}, time.Now, model.NewId,
	)
	classMembers := newClassMemberService(
		applicationPlatform.Store().ClassMember(), applicationPlatform.Store().Class(),
		academicAuthorization, mutationAuditAdapter{audit: audit}, time.Now, model.NewId,
	)
	userProfiles := newUserProfileService(
		applicationPlatform.Store().User(),
		userProfileAuthorization{authorization: authorization, institutions: applicationPlatform.Store().Institution(), classMembers: applicationPlatform.Store().ClassMember(), now: time.Now},
		mutationAuditAdapter{audit: audit}, time.Now,
	)
	accountStates := newAccountStateService(
		applicationPlatform.Store().User(),
		userProfileAuthorization{authorization: authorization, institutions: applicationPlatform.Store().Institution(), classMembers: applicationPlatform.Store().ClassMember(), now: time.Now},
		mutationAuditAdapter{audit: audit},
		accountStateRealtimeEffects{realtime: realtime},
		time.Now,
	)
	sessionAdministrations := newSessionAdministrationService(
		applicationPlatform.Store().Session(),
		sessionAdministrationAuthorization{authorization: authorization},
		mutationAuditAdapter{audit: audit},
		sessionAdministrationRealtimeEffects{realtime: realtime},
		time.Now,
	)
	roles := newRoleService(
		applicationPlatform.Store().Role(),
		roleAuthorization{authorization: authorization, institutions: applicationPlatform.Store().Institution()},
		mutationAuditAdapter{audit: audit},
		roleRealtimeEffects{realtime: realtime},
		time.Now,
	)
	roleBindings := newRoleBindingService(
		applicationPlatform.Store().RoleBinding(),
		applicationPlatform.Store().Role(),
		roleAuthorization{authorization: authorization, institutions: applicationPlatform.Store().Institution()},
		mutationAuditAdapter{audit: audit},
		roleBindingRealtimeEffects{realtime: realtime},
		time.Now,
	)
	auditListings := newAuditListingService(
		applicationPlatform.Store().Audit(),
		auditListingAuthorization{authorization: authorization, institutions: applicationPlatform.Store().Institution()},
	)
	bootstrap := newBootstrapService(
		applicationPlatform.Store().Installation(),
		authentication.hasher,
		bootstrapRateLimit{
			cache:                 applicationPlatform.Cache(),
			window:                authSettings.LoginRateLimit.Window.Duration,
			maximumSourceAttempts: authSettings.LoginRateLimit.MaximumSourceAttempts,
		},
		applicationPlatform.Cluster().NodeID(),
		time.Now,
	)
	return &App{
		platform: applicationPlatform, authentication: authentication,
		externalAuthentication: externalAuthentication, mfa: mfa,
		authorization: authorization, academicUnits: academicUnits,
		academicUnitCommands:   academicUnitCommands,
		institutions:           institutions,
		programmes:             programmes,
		programmeLevels:        programmeLevels,
		academicPeriods:        academicPeriods,
		classes:                classes,
		affiliations:           affiliations,
		academicUnitMembers:    academicUnitMembers,
		classMembers:           classMembers,
		userProfiles:           userProfiles,
		accountStates:          accountStates,
		sessionAdministrations: sessionAdministrations,
		roles:                  roles,
		roleBindings:           roleBindings,
		auditListings:          auditListings,
		bootstrap:              bootstrap,
		audit:                  audit, realtime: realtime,
	}, nil
}

func (a *App) Can(
	ctx context.Context,
	principal model.Principal,
	action model.Action,
	resource model.Resource,
) (bool, error) {
	return a.PrincipalHasPermissionTo(ctx, principal, action, resource)
}

func (a *App) Platform() *platform.Service {
	return a.platform
}

func (a *App) Config() config.Config {
	return a.platform.Config()
}

func (a *App) Log() *mlog.Logger {
	return a.platform.Log()
}

func (a *App) Store() store.Store {
	return a.platform.Store()
}

func (a *App) Cache() platform.Cache {
	return a.platform.Cache()
}

func (a *App) Cluster() platform.Cluster {
	return a.platform.Cluster()
}

func (a *App) Mailer() platform.Mailer {
	return a.platform.Mailer()
}

func (a *App) VFS() vfspkg.FileSystem {
	return a.platform.VFS()
}

// errAuthenticationCacheMiss and errAuthenticationCacheNotStored are the
// transport-neutral sentinels authentication uses for disposable cache
// outcomes. Adapters translate platform cache errors into these values so the
// authentication service never imports platform.
var (
	errAuthenticationCacheMiss      = errors.New("authentication cache: key not found")
	errAuthenticationCacheNotStored = errors.New("authentication cache: conditional write not applied")
)

// platformAuthenticationCache adapts platform.Cache to the narrow
// authenticationCache port used by AuthenticationService.
type platformAuthenticationCache struct {
	cache platform.Cache
}

func (c platformAuthenticationCache) Get(ctx context.Context, key string) ([]byte, error) {
	data, err := c.cache.Get(ctx, key)
	if errors.Is(err, platform.ErrCacheMiss) {
		return nil, errAuthenticationCacheMiss
	}
	return data, err
}

func (c platformAuthenticationCache) SetAlways(
	ctx context.Context,
	key string,
	value []byte,
	ttl time.Duration,
) error {
	return c.cache.Set(ctx, key, value, ttl, platform.CacheSetAlways)
}

func (c platformAuthenticationCache) SetIfAbsent(
	ctx context.Context,
	key string,
	value []byte,
	ttl time.Duration,
) error {
	err := c.cache.Set(ctx, key, value, ttl, platform.CacheSetIfAbsent)
	if errors.Is(err, platform.ErrCacheNotStored) {
		return errAuthenticationCacheNotStored
	}
	return err
}

func (c platformAuthenticationCache) Delete(ctx context.Context, key string) error {
	return c.cache.Delete(ctx, key)
}

func (c platformAuthenticationCache) Add(
	ctx context.Context,
	key string,
	delta int64,
	ttl time.Duration,
) (int64, error) {
	return c.cache.Add(ctx, key, delta, ttl)
}

// mlogAuthenticationDiagnostics reports non-fatal authentication operational
// events without making the authentication service depend on mlog directly.
type mlogAuthenticationDiagnostics struct {
	log *mlog.Logger
}

func (d mlogAuthenticationDiagnostics) WarnContext(ctx context.Context, message string, err error) {
	if d.log == nil {
		return
	}
	fields := []mlog.Field{}
	if err != nil {
		fields = append(fields, mlog.Err(err))
	}
	d.log.WarnContext(ctx, message, fields...)
}
