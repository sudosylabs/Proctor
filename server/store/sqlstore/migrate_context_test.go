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
)

func TestMigrationLockContextDetachesAfterAcquisition(t *testing.T) {
	t.Parallel()

	parent, cancelParent := context.WithCancel(context.Background())
	lockCtx, finishAcquisition, release := newMigrationLockContext(parent, time.Minute)
	require.NoError(t, finishAcquisition())

	cancelParent()
	require.NoError(t, lockCtx.Err())
	release(context.Canceled)
	require.ErrorIs(t, lockCtx.Err(), context.Canceled, "released lock context error")
}

func TestMigrationLockContextCancelsWhileWaiting(t *testing.T) {
	t.Parallel()

	parent, cancelParent := context.WithCancel(context.Background())
	lockCtx, finishAcquisition, release := newMigrationLockContext(parent, time.Minute)
	cancelParent()
	<-lockCtx.Done()
	require.ErrorIs(t, finishAcquisition(), context.Canceled, "finish acquisition error")
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
		require.FailNow(t, "migration lock context did not enforce the acquisition timeout")
	}
	require.ErrorIs(t, finishAcquisition(), context.DeadlineExceeded, "finish acquisition error")
	release(context.Canceled)
}
