// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package server

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProductionDesktopCatalogDoesNotInventCertification(t *testing.T) {
	builds, err := verifiedDesktopBuildCatalog()
	require.NoError(t, err, "production native certification requires real admitted artifacts")
	require.Empty(t, builds, "production native certification requires real admitted artifacts")
}
