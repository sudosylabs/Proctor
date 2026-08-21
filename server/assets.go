// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"embed"
	"io/fs"
)

// Runtime assets stay embedded without turning their source directories into
// Go packages.
//
//go:embed i18n/*.json templates/*.html templates/*.txt
var runtimeAssets embed.FS

func runtimeAssetDirectory(name string) (fs.FS, error) {
	return fs.Sub(runtimeAssets, name)
}
