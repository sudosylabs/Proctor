// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package platform

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/cluster"
)

func TestClusterErrorsAreSiblingSentinels(t *testing.T) {
	t.Parallel()

	require.Equal(t, cluster.ErrStopped, ErrClusterStopped, "platform cluster errors drifted from cluster package sentinels")
	require.Equal(t, cluster.ErrNotStarted, ErrClusterNotStarted, "platform cluster errors drifted from cluster package sentinels")
	require.Equal(t, cluster.ErrHandlerExists, ErrClusterHandlerExists, "platform cluster errors drifted from cluster package sentinels")
	require.Equal(t, cluster.ErrNodeUnavailable, ErrClusterNodeUnavailable, "platform cluster errors drifted from cluster package sentinels")
	require.Equal(t, cluster.ErrNodeIDInUse, ErrClusterNodeIDInUse, "platform cluster errors drifted from cluster package sentinels")
}
