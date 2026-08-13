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

type sqlTransactionPolicy[T any] struct {
	beginError  func(error) error
	commit      bool
	commitError func(T, error) error
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
	return executeSQLTransaction(ctx, begin, sqlTransactionPolicy[T]{
		beginError: func(err error) error { return fmt.Errorf("begin %s: %w", operation, err) },
		commit:     true,
		commitError: func(_ T, err error) error {
			return fmt.Errorf("commit %s: %w", operation, err)
		},
	}, body)
}

// executeSQLTransaction is the single transaction lifecycle implementation.
// The policy exists for characterized legacy branches that deliberately expose
// raw errors, branch-specific commit labels, or rollback-only read completion.
func executeSQLTransaction[Tx transactionFinalizer, T any](
	ctx context.Context,
	begin func(context.Context) (Tx, error),
	policy sqlTransactionPolicy[T],
	body func(context.Context, Tx) (T, error),
) (T, error) {
	var zero T
	tx, err := begin(ctx)
	if err != nil {
		return zero, policy.beginError(err)
	}
	defer func() { _ = tx.Rollback() }()

	result, err := body(ctx, tx)
	if err != nil {
		return zero, err
	}
	if !policy.commit {
		return result, nil
	}
	if err := tx.Commit(); err != nil {
		return zero, policy.commitError(result, err)
	}
	return result, nil
}
