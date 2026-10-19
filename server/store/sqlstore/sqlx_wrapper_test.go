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
)

func TestSQLXDBWrapperQueryContext(t *testing.T) {
	assertQueryContextBoundsEveryStatement(t, (&sqlxDBWrapper{queryTimeout: time.Second}).queryContext)
}

func TestSQLXTxWrapperQueryContext(t *testing.T) {
	assertQueryContextBoundsEveryStatement(t, (&sqlxTxWrapper{queryTimeout: time.Second}).queryContext)
}

func assertQueryContextBoundsEveryStatement(
	t *testing.T,
	queryContext func(context.Context) (context.Context, context.CancelFunc),
) {
	t.Helper()
	assertRemaining := func(name string, ctx context.Context, upper time.Duration) {
		t.Helper()
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Fatalf("%s: query context has no deadline", name)
		}
		if remaining := time.Until(deadline); remaining <= 0 || remaining > upper {
			t.Fatalf("%s: query deadline remaining = %v, want (0, %v]", name, remaining, upper)
		}
	}

	ctx, cancel := queryContext(context.Background())
	defer cancel()
	assertRemaining("no caller deadline", ctx, time.Second)

	later, laterCancel := context.WithTimeout(context.Background(), time.Hour)
	defer laterCancel()
	ctx, cancel = queryContext(later)
	defer cancel()
	assertRemaining("later caller deadline", ctx, time.Second)

	earlierDeadline := time.Now().Add(100 * time.Millisecond)
	earlier, earlierCancel := context.WithDeadline(context.Background(), earlierDeadline)
	defer earlierCancel()
	ctx, cancel = queryContext(earlier)
	defer cancel()
	if deadline, ok := ctx.Deadline(); !ok || !deadline.Equal(earlierDeadline) {
		t.Fatalf("earlier caller deadline = %v, want %v", deadline, earlierDeadline)
	}
}
