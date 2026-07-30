// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

// Package app owns Proctor's process and application flow.
package app

import (
	"context"

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
	platform       *platform.Service
	authentication *AuthenticationService
	authorization  *AuthorizationService
	audit          *AuditService
}

func New(applicationPlatform *platform.Service) (*App, error) {
	authentication, err := newAuthenticationService(applicationPlatform)
	if err != nil {
		return nil, err
	}
	audit := newAuditService(
		applicationPlatform.Store(),
		applicationPlatform.Cluster().NodeID(),
	)
	authorization := newAuthorizationService(applicationPlatform.Store(), audit)
	return &App{
		platform: applicationPlatform, authentication: authentication,
		authorization: authorization, audit: audit,
	}, nil
}

func (a *App) ListAuditEvents(
	ctx context.Context,
	principal model.Principal,
	metadata model.RequestMetadata,
	query model.AuditQuery,
) ([]*model.AuditEvent, *model.AppError) {
	institution, err := a.Store().Institution().GetSingleton(ctx)
	if err != nil {
		return nil, authorizationResourceError("institution", err)
	}
	if appErr := a.AuthorizePrincipalToInstitution(
		ctx,
		principal,
		institution.Id,
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
