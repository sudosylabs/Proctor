// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import "testing"

func TestDirectorySearchPatternTreatsSQLWildcardsLiterally(t *testing.T) {
	t.Parallel()
	if got, want := directorySearchPattern("  50%_done!  "), "%50!%!_done!!%"; got != want {
		t.Fatalf("directorySearchPattern() = %q, want %q", got, want)
	}
}
