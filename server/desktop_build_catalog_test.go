// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package server

import "testing"

func TestProductionDesktopCatalogDoesNotInventCertification(t *testing.T) {
	builds, err := verifiedDesktopBuildCatalog()
	if err != nil || len(builds) != 0 {
		t.Fatal("production native certification requires real admitted artifacts", err)
	}
}
