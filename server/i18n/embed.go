// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package i18n

import "embed"

// catalogFiles contains the locale catalogs shipped with the server binary.
// Keeping the filesystem private prevents callers from depending on catalog
// storage rather than the Bundle interface.
//
//go:embed *.json
var catalogFiles embed.FS
