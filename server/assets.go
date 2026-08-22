// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"embed"
	"io/fs"

	"github.com/sudosylabs/proctor/server/localization"
)

// Runtime assets stay embedded without turning their source directories into
// Go packages.
//
//go:embed i18n/*.json templates/*.html templates/*.txt
var runtimeAssets embed.FS

func runtimeAssetDirectory(name string) (fs.FS, error) {
	return fs.Sub(runtimeAssets, name)
}

// NewEmbeddedLocalizer opens the server-owned catalogs for presentation-only
// binaries such as the operator CLI. Runtime server composition uses the same
// assets through its private construction recipe.
func NewEmbeddedLocalizer() (*localization.Localizer, error) {
	catalogs, err := runtimeAssetDirectory("i18n")
	if err != nil {
		return nil, err
	}
	return localization.New(catalogs, localization.EnglishLocale)
}
