// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

// Package app owns Proctor's process and application flow.
package app

import (
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/mlog"
	"github.com/sudosylabs/proctor/server/platform"
	"github.com/sudosylabs/proctor/server/store"
)

// App is the long-lived application facade. Product capabilities will be
// composed here as their contracts become concrete.
type App struct {
	platform *platform.Service
}

func New(applicationPlatform *platform.Service) *App {
	return &App{platform: applicationPlatform}
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
