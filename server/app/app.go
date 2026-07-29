// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

// Package app owns Proctor's process and application flow.
package app

import (
	vfspkg "github.com/sudosylabs/proctor/packages/vfs"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/mlog"
	"github.com/sudosylabs/proctor/server/platform"
	"github.com/sudosylabs/proctor/server/store"
)

// App is the long-lived application facade. Product capabilities will be
// composed here as their contracts become concrete.
type App struct {
	platform       *platform.Service
	authentication *AuthenticationService
}

func New(applicationPlatform *platform.Service) (*App, error) {
	authentication, err := newAuthenticationService(applicationPlatform)
	if err != nil {
		return nil, err
	}
	return &App{platform: applicationPlatform, authentication: authentication}, nil
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

func (a *App) Mailer() platform.Mailer {
	return a.platform.Mailer()
}

func (a *App) VFS() vfspkg.FileSystem {
	return a.platform.VFS()
}
