// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package platform

import (
	"testing"

	"github.com/sudosylabs/proctor/server/cluster"
)

func TestClusterErrorsAreSiblingSentinels(t *testing.T) {
	t.Parallel()

	if ErrClusterStopped != cluster.ErrStopped ||
		ErrClusterNotStarted != cluster.ErrNotStarted ||
		ErrClusterHandlerExists != cluster.ErrHandlerExists ||
		ErrClusterNodeUnavailable != cluster.ErrNodeUnavailable ||
		ErrClusterNodeIDInUse != cluster.ErrNodeIDInUse {
		t.Fatal("platform cluster errors drifted from cluster package sentinels")
	}
}
