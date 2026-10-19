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
		if err != nil {
			t.Fatal(err)
		}
		released := false
		release := func() {
			if !released {
				released = true
				_ = blocker.Rollback()
			}
		}
		defer release()
		if err := lockServingNodeLease(ctx, blocker, "node-a"); err != nil {
			t.Fatal(err)
		}
		var blockerPID int
		if err := blocker.Get(ctx, &blockerPID, `SELECT pg_backend_pid()`); err != nil {
			t.Fatal(err)
		}

		otherCtx, cancelOther := context.WithTimeout(ctx, 5*time.Second)
		otherLeaseID := model.NewId()
		_, err = sqlStore.ServingNodeLease().Upsert(otherCtx, &store.ServingNodeLeaseClaim{NodeID: "node-b", LeaseID: otherLeaseID, Lifetime: 30 * time.Second})
		cancelOther()
		if err != nil {
			t.Fatalf("another node's renewal waited behind node-a: %v", err)
		}

		sameLeaseID := model.NewId()
		sameDone := make(chan error, 1)
		go func() {
			_, upsertErr := sqlStore.ServingNodeLease().Upsert(ctx, &store.ServingNodeLeaseClaim{NodeID: "node-a", LeaseID: sameLeaseID, Lifetime: 30 * time.Second})
			sameDone <- upsertErr
		}()
		waitForBlockedSystemAdministratorAuthenticationPathTransactions(t, ctx, sqlStore, blockerPID, 1)
		select {
		case err := <-sameDone:
			t.Fatalf("same-node renewal did not wait for the open node-a transaction: %v", err)
		default:
		}
		release()
		if err := <-sameDone; err != nil {
			t.Fatal(err)
		}
		if err := sqlStore.ServingNodeLease().Delete(ctx, "node-a", sameLeaseID); err != nil {
			t.Fatal(err)
		}
		if err := sqlStore.ServingNodeLease().Delete(ctx, "node-b", otherLeaseID); err != nil {
			t.Fatal(err)
		}
	})
}
