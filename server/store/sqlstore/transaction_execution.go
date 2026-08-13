// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"context"
	"fmt"
)

type transactionFinalizer interface {
	Commit() error
	Rollback() error
}

// runSQLTransaction owns the ordinary transaction lifecycle inside the
// PostgreSQL adapter. Operation bodies retain domain locks, queries, error
// translation, and result construction; application code never receives this
// generic transaction mechanism.
func runSQLTransaction[Tx transactionFinalizer, T any](
	ctx context.Context,
	begin func(context.Context) (Tx, error),
	operation string,
	body func(context.Context, Tx) (T, error),
) (T, error) {
	var zero T
	tx, err := begin(ctx)
	if err != nil {
		return zero, fmt.Errorf("begin %s: %w", operation, err)
	}
	defer func() { _ = tx.Rollback() }()

	result, err := body(ctx, tx)
	if err != nil {
		return zero, err
	}
	if err := tx.Commit(); err != nil {
		return zero, fmt.Errorf("commit %s: %w", operation, err)
	}
	return result, nil
}
