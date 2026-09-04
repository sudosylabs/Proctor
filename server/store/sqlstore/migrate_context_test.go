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
)

func TestMigrationLockContextDetachesAfterAcquisition(t *testing.T) {
	t.Parallel()

	parent, cancelParent := context.WithCancel(context.Background())
	lockCtx, finishAcquisition, release := newMigrationLockContext(parent, time.Minute)
	if err := finishAcquisition(); err != nil {
		t.Fatalf("finish acquisition: %v", err)
	}

	cancelParent()
	if err := lockCtx.Err(); err != nil {
		t.Fatalf("acquired lock context followed caller cancellation: %v", err)
	}
	release(context.Canceled)
	if !errors.Is(lockCtx.Err(), context.Canceled) {
		t.Fatalf("released lock context error = %v, want context canceled", lockCtx.Err())
	}
}

func TestMigrationLockContextCancelsWhileWaiting(t *testing.T) {
	t.Parallel()

	parent, cancelParent := context.WithCancel(context.Background())
	lockCtx, finishAcquisition, release := newMigrationLockContext(parent, time.Minute)
	cancelParent()
	<-lockCtx.Done()
	if err := finishAcquisition(); !errors.Is(err, context.Canceled) {
		t.Fatalf("finish acquisition error = %v, want context canceled", err)
	}
	release(context.Canceled)
}

func TestMigrationLockContextTimesOutWhileWaiting(t *testing.T) {
	t.Parallel()

	lockCtx, finishAcquisition, release := newMigrationLockContext(
		context.Background(),
		time.Millisecond,
	)
	select {
	case <-lockCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("migration lock context did not enforce the acquisition timeout")
	}
	if err := finishAcquisition(); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("finish acquisition error = %v, want deadline exceeded", err)
	}
	release(context.Canceled)
}
