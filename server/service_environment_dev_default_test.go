//go:build !production

// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package server

import "testing"

func TestServiceEnvironmentBuildDefault(t *testing.T) {
	t.Setenv("PROCTOR_SERVICE_ENVIRONMENT", "")
	got, err := readServiceEnvironment()
	if err != nil || got != ServiceEnvironmentDev {
		t.Fatalf("build default = %q, %v", got, err)
	}
}
