// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

// Package app owns Proctor's application use cases and orchestration.
package app

import (
	"context"
	"time"

	vfspkg "github.com/sudosylabs/proctor/packages/vfs"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/mlog"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/platform"
	"github.com/sudosylabs/proctor/server/store"
)

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
	audit                  *AuditService
	realtime               *RealtimeService
}

func New(applicationPlatform *platform.Service) (*App, error) {
	mfa, err := newMFAService(applicationPlatform)
	if err != nil {
		return nil, err
	}
	authentication, err := newAuthenticationService(applicationPlatform, mfa)
	if err != nil {
		return nil, err
	}
	audit := newAuditService(
		applicationPlatform.Store(),
		applicationPlatform.Cluster().NodeID(),
	)
	externalAuthentication := newExternalAuthenticationService(
		applicationPlatform,
		authentication,
		audit,
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
	return &App{
		platform: applicationPlatform, authentication: authentication,
		externalAuthentication: externalAuthentication, mfa: mfa,
		authorization: authorization, academicUnits: academicUnits,
		academicUnitCommands: academicUnitCommands,
		institutions:         institutions,
		programmes:           programmes,
		programmeLevels:      programmeLevels,
		academicPeriods:      academicPeriods,
		audit:                audit, realtime: realtime,
	}, nil
}

func (a *App) ListAuditEvents(
	ctx context.Context,
	principal model.Principal,
	metadata model.RequestMetadata,
	query model.AuditQuery,
) ([]*model.AuditEvent, *model.AppError) {
	if _, appErr := a.authorizePrincipalToSystem(
		ctx,
		principal,
		model.ActionAuditView,
		metadata,
	); appErr != nil {
		return nil, appErr
	}
	return a.audit.List(ctx, query)
}

func (a *App) Can(
	ctx context.Context,
	principal model.Principal,
	action model.Action,
	resource model.Resource,
) (bool, *model.AppError) {
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
