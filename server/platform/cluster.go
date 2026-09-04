// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package platform

import (
	"github.com/sudosylabs/proctor/server/cluster"
)

// Cluster is the platform lifecycle alias for the sibling cluster.Transport
// contract. Composition selects the concrete adapter (local or memberlist).
type Cluster = cluster.Transport

// Re-export sentinel errors so existing platform tests and callers can still
// use platform.ErrCluster* names during the migration.
var (
	ErrClusterStopped         = cluster.ErrStopped
	ErrClusterNotStarted      = cluster.ErrNotStarted
	ErrClusterHandlerExists   = cluster.ErrHandlerExists
	ErrClusterNodeUnavailable = cluster.ErrNodeUnavailable
	ErrClusterNodeIDInUse     = cluster.ErrNodeIDInUse
)
