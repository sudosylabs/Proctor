// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import "testing"

func TestDirectorySearchPatternTreatsSQLWildcardsLiterally(t *testing.T) {
	t.Parallel()
	if got, want := directorySearchPattern("  50%_done!  "), "%50!%!_done!!%"; got != want {
		t.Fatalf("directorySearchPattern() = %q, want %q", got, want)
	}
}
