//go:build integration

// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
package sqlstore

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/store"
)

func TestDeliveryAppendAdmissionReservesDatabaseCapacity(t *testing.T) {
	s := openTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.GreaterOrEqual(t, cap(s.deliveryAppendSlots), 1, "append admission leaves no reserved pool capacity")
	require.LessOrEqual(t, cap(s.deliveryAppendSlots)*2, s.GetMaster().DB().Stats().MaxOpenConnections, "append admission leaves no reserved pool capacity")
	for range cap(s.deliveryAppendSlots) {
		release, err := s.enterDeliveryAppend(ctx, "native")
		require.NoError(t, err)
		t.Cleanup(release)
		conn, err := s.GetMaster().DB().Connx(ctx)
		require.NoError(t, err)
		t.Cleanup(func() { _ = conn.Close() })
	}
	// Browser and native intake share admission; a second transport is no escape.
	for _, family := range []string{"browser", "native"} {
		_, err := s.enterDeliveryAppend(ctx, family)
		require.ErrorIs(t, err, store.ErrDeliveryAdmissionFull, "saturated %s intake", family)
	}
	readCtx, stopRead := context.WithTimeout(ctx, time.Second)
	defer stopRead()
	var one int
	require.NoError(t, s.GetMaster().Get(readCtx, &one, "SELECT 1"))
	require.Equal(t, 1, one)
}
