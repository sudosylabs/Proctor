// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package server

import (
	"crypto/ed25519"
	"github.com/sudosylabs/proctor/server/desktoprelease"
	"github.com/sudosylabs/proctor/server/model"
)

// verifiedDesktopBuildCatalog is intentionally empty until coordinated
// activation supplies signed Desktop target artifacts and capability matrices.
// Server releases must embed exact verified tuples here; configuration and
// Institution policy are not permitted to invent compatible released builds.
// Development skips login compatibility checks without modifying this catalog.
func verifiedDesktopBuildCatalog() ([]model.DesktopBuildTuple, error) {
	// Real activation must add verified target artifacts and the admitted release
	// public keys together. Synthetic certification belongs only in tests.
	return verifyDesktopReleases([]admittedDesktopRelease{}, map[string]ed25519.PublicKey{})
}

type admittedDesktopRelease struct {
	expectation desktoprelease.Expectation
	artifacts   desktoprelease.Artifacts
}

func verifyDesktopReleases(releases []admittedDesktopRelease, keys map[string]ed25519.PublicKey) ([]model.DesktopBuildTuple, error) {
	result := make([]model.DesktopBuildTuple, 0, len(releases))
	for _, release := range releases {
		build, err := desktoprelease.Verify(release.expectation, release.artifacts, keys)
		if err != nil {
			return nil, err
		}
		result = append(result, build)
	}
	return result, nil
}
