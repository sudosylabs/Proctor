// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDirectorySearchPatternTreatsSQLWildcardsLiterally(t *testing.T) {
	t.Parallel()
	require.Equal(t, "%50!%!_done!!%", directorySearchPattern("  50%_done!  "), "directorySearchPattern()")
}
