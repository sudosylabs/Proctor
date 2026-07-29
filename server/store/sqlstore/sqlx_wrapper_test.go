// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"context"
	"testing"
	"time"
)

func TestSQLXDBWrapperQueryContext(t *testing.T) {
	wrapper := &sqlxDBWrapper{queryTimeout: time.Second}

	ctx, cancel := wrapper.queryContext(context.Background())
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("query context has no deadline")
	}
	remaining := time.Until(deadline)
	if remaining <= 0 || remaining > time.Second {
		t.Fatalf("query deadline remaining = %v", remaining)
	}

	parentDeadline := time.Now().Add(2 * time.Second)
	parent, parentCancel := context.WithDeadline(context.Background(), parentDeadline)
	defer parentCancel()
	got, gotCancel := wrapper.queryContext(parent)
	defer gotCancel()
	gotDeadline, ok := got.Deadline()
	if !ok || !gotDeadline.Equal(parentDeadline) {
		t.Fatalf("existing deadline = %v, want %v", gotDeadline, parentDeadline)
	}
}

func TestSQLXTxWrapperQueryContext(t *testing.T) {
	wrapper := &sqlxTxWrapper{queryTimeout: time.Second}
	ctx, cancel := wrapper.queryContext(context.Background())
	defer cancel()
	if _, ok := ctx.Deadline(); !ok {
		t.Fatal("transaction query context has no deadline")
	}
}
