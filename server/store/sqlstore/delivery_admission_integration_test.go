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
	"errors"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/store"
)

func TestDeliveryAppendAdmissionReservesDatabaseCapacity(t *testing.T) {
	s := openTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if cap(s.deliveryAppendSlots) < 1 || cap(s.deliveryAppendSlots)*2 > s.GetMaster().DB().Stats().MaxOpenConnections {
		t.Fatal("append admission leaves no reserved pool capacity")
	}
	for range cap(s.deliveryAppendSlots) {
		release, err := s.enterDeliveryAppend(ctx, "native")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(release)
		conn, err := s.GetMaster().DB().Connx(ctx)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close() })
	}
	// Browser and native intake share admission; a second transport is no escape.
	for _, family := range []string{"browser", "native"} {
		if _, err := s.enterDeliveryAppend(ctx, family); !errors.Is(err, store.ErrDeliveryAdmissionFull) {
			t.Fatalf("saturated %s intake = %v", family, err)
		}
	}
	readCtx, stopRead := context.WithTimeout(ctx, time.Second)
	defer stopRead()
	var one int
	if err := s.GetMaster().Get(readCtx, &one, "SELECT 1"); err != nil || one != 1 {
		t.Fatalf("append intake monopolized pool: %v", err)
	}
}
