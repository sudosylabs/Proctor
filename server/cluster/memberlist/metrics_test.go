// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package memberlist

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/cluster"
)

func TestMetricEventLabelsOnlyRegisteredHandlers(t *testing.T) {
	t.Parallel()

	transport := &Transport{handlers: map[cluster.Event]cluster.Handler{
		"registered.event": func(context.Context, *cluster.Message) error { return nil },
	}}
	require.Equal(t, "registered.event", transport.metricEvent("registered.event"))
	require.Equal(t, "unregistered", transport.metricEvent("peer.supplied"))
}
