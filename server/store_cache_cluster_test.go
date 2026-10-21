// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package server

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/cluster"
	"github.com/sudosylabs/proctor/server/cluster/local"
	"github.com/sudosylabs/proctor/server/model"
)

type cacheClusterTestLogger struct{}

func (cacheClusterTestLogger) ErrorContext(context.Context, string, error) {}

func TestLocalCacheClusterAdapterRegistersValidatedLoopSafeInvalidation(t *testing.T) {
	t.Parallel()

	transport, err := local.New("node-a", cacheClusterTestLogger{})
	require.NoError(t, err)
	adapter, err := newLocalCacheClusterAdapter(transport)
	require.NoError(t, err)
	id := model.NewAcademicPeriodID().String()
	var handled atomic.Int64
	require.NoError(t, adapter.RegisterAcademicPeriod(func(_ context.Context, got string) error {
		require.Equal(t, id, got, "invalidation ID")
		handled.Add(1)
		return nil
	}))
	require.NoError(t, transport.Start(context.Background()))

	require.NoError(t, adapter.BroadcastAcademicPeriod(context.Background(), id))
	require.Equal(t, int64(0), handled.Load(), "peer-only broadcast re-entered the local invalidation handler")
	require.NoError(t, transport.SendToNode(context.Background(), transport.NodeID(), &cluster.Message{
		Event: academicPeriodInvalidatedEvent,
		Data:  []byte(`{"id":"` + id + `"}`),
	}))
	require.Equal(t, int64(1), handled.Load(), "handled invalidations")
	require.Error(t, transport.SendToNode(context.Background(), transport.NodeID(), &cluster.Message{
		Event: academicPeriodInvalidatedEvent,
		Data:  []byte(`{"id":"invalid"}`),
	}), "invalid peer invalidation was accepted")
}
