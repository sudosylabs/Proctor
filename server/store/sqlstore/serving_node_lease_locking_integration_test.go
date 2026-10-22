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
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestServingNodeLeaseWritesSerializeOnlyTheSameNode(t *testing.T) {
	StoreTest(t, func(t *testing.T, persistence store.Store) {
		sqlStore := persistence.(*SQLStore)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// An open lease transaction for node-a holds exactly what Upsert holds.
		blocker, err := sqlStore.GetMaster().Begin(ctx)
		require.NoError(t, err)
		released := false
		release := func() {
			if !released {
				released = true
				_ = blocker.Rollback()
			}
		}
		defer release()
		require.NoError(t, lockServingNodeLease(ctx, blocker, "node-a"))
		var blockerPID int
		require.NoError(t, blocker.Get(ctx, &blockerPID, `SELECT pg_backend_pid()`))

		otherCtx, cancelOther := context.WithTimeout(ctx, 5*time.Second)
		otherLeaseID := model.NewID()
		_, err = sqlStore.ServingNodeLease().Upsert(otherCtx, &store.ServingNodeLeaseClaim{NodeID: "node-b", LeaseID: otherLeaseID, Lifetime: 30 * time.Second})
		cancelOther()
		require.NoError(t, err)

		sameLeaseID := model.NewID()
		sameDone := make(chan error, 1)
		go func() {
			_, upsertErr := sqlStore.ServingNodeLease().Upsert(ctx, &store.ServingNodeLeaseClaim{NodeID: "node-a", LeaseID: sameLeaseID, Lifetime: 30 * time.Second})
			sameDone <- upsertErr
		}()
		waitForBlockedSystemAdministratorAuthenticationPathTransactions(t, ctx, sqlStore, blockerPID, 1)
		select {
		case err := <-sameDone:
			require.FailNow(t, fmt.Sprintf("same-node renewal did not wait for the open node-a transaction: %v", err))
		default:
		}
		release()
		require.NoError(t, (<-sameDone))
		require.NoError(t, sqlStore.ServingNodeLease().Delete(ctx, &store.ServingNodeLeaseDeletion{NodeID: "node-a", LeaseID: sameLeaseID}))
		require.NoError(t, sqlStore.ServingNodeLease().Delete(ctx, &store.ServingNodeLeaseDeletion{NodeID: "node-b", LeaseID: otherLeaseID}))
	})
}
