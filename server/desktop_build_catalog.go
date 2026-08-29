// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package server

import "github.com/sudosylabs/proctor/server/model"

// verifiedDesktopBuildCatalog is intentionally empty until coordinated
// activation supplies signed Desktop target artifacts and capability matrices.
// Server releases must embed exact verified tuples here; configuration and
// Institution policy are not permitted to invent compatible builds.
func verifiedDesktopBuildCatalog() []model.DesktopBuildTuple {
	return []model.DesktopBuildTuple{}
}
