// ---------------------------------------------------------------------------------------------
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Modifications Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
//
// Adapted from Mattermost server/channels/store/sqlstore/sqlx_wrapper.go.
// Proctor keeps the centralized builder execution, PostgreSQL rebinding, query
// timeout, and transaction-executor concepts while using request contexts and
// omitting Mattermost's tracing and online-replica state.

package sqlstore

import (
	"context"
	"database/sql"
	"time"

	"github.com/jmoiron/sqlx"
)

type builder interface {
	ToSql() (string, []any, error)
}

type sqlxExecutor interface {
	Get(context.Context, any, string, ...any) error
	GetBuilder(context.Context, any, builder) error
	Select(context.Context, any, string, ...any) error
	SelectBuilder(context.Context, any, builder) error
	NamedExec(context.Context, string, any) (sql.Result, error)
	Exec(context.Context, string, ...any) (sql.Result, error)
	ExecBuilder(context.Context, builder) (sql.Result, error)
}

type sqlxDBWrapper struct {
	db           *sqlx.DB
	queryTimeout time.Duration
}

func newSqlxDBWrapper(db *sqlx.DB, queryTimeout time.Duration) *sqlxDBWrapper {
	return &sqlxDBWrapper{db: db, queryTimeout: queryTimeout}
}

func (w *sqlxDBWrapper) DB() *sqlx.DB {
	return w.db
}

func (w *sqlxDBWrapper) Close() error {
	return w.db.Close()
}

func (w *sqlxDBWrapper) Ping(ctx context.Context) error {
	queryCtx, cancel := w.queryContext(ctx)
	defer cancel()
	return w.db.PingContext(queryCtx)
}

func (w *sqlxDBWrapper) Stats() sql.DBStats {
	return w.db.Stats()
}

func (w *sqlxDBWrapper) Get(ctx context.Context, dest any, query string, args ...any) error {
	queryCtx, cancel := w.queryContext(ctx)
	defer cancel()
	return w.db.GetContext(queryCtx, dest, w.db.Rebind(query), args...)
}

func (w *sqlxDBWrapper) GetBuilder(ctx context.Context, dest any, query builder) error {
	sqlQuery, args, err := query.ToSql()
	if err != nil {
		return err
	}
	return w.Get(ctx, dest, sqlQuery, args...)
}

func (w *sqlxDBWrapper) Select(ctx context.Context, dest any, query string, args ...any) error {
	queryCtx, cancel := w.queryContext(ctx)
	defer cancel()
	return w.db.SelectContext(queryCtx, dest, w.db.Rebind(query), args...)
}

func (w *sqlxDBWrapper) SelectBuilder(ctx context.Context, dest any, query builder) error {
	sqlQuery, args, err := query.ToSql()
	if err != nil {
		return err
	}
	return w.Select(ctx, dest, sqlQuery, args...)
}

func (w *sqlxDBWrapper) NamedExec(ctx context.Context, query string, arg any) (sql.Result, error) {
	queryCtx, cancel := w.queryContext(ctx)
	defer cancel()
	return w.db.NamedExecContext(queryCtx, query, arg)
}

func (w *sqlxDBWrapper) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	queryCtx, cancel := w.queryContext(ctx)
	defer cancel()
	return w.db.ExecContext(queryCtx, w.db.Rebind(query), args...)
}

func (w *sqlxDBWrapper) ExecBuilder(ctx context.Context, query builder) (sql.Result, error) {
	sqlQuery, args, err := query.ToSql()
	if err != nil {
		return nil, err
	}
	return w.Exec(ctx, sqlQuery, args...)
}

func (w *sqlxDBWrapper) Begin(ctx context.Context) (*sqlxTxWrapper, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	tx, err := w.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	return &sqlxTxWrapper{
		tx:           tx,
		queryTimeout: w.queryTimeout,
		rebind:       w.db.Rebind,
	}, nil
}

func (w *sqlxDBWrapper) queryContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, w.queryTimeout)
}

type sqlxTxWrapper struct {
	tx           *sqlx.Tx
	queryTimeout time.Duration
	rebind       func(string) string
}

func (w *sqlxTxWrapper) Get(ctx context.Context, dest any, query string, args ...any) error {
	queryCtx, cancel := w.queryContext(ctx)
	defer cancel()
	return w.tx.GetContext(queryCtx, dest, w.rebind(query), args...)
}

func (w *sqlxTxWrapper) GetBuilder(ctx context.Context, dest any, query builder) error {
	sqlQuery, args, err := query.ToSql()
	if err != nil {
		return err
	}
	return w.Get(ctx, dest, sqlQuery, args...)
}

func (w *sqlxTxWrapper) Select(ctx context.Context, dest any, query string, args ...any) error {
	queryCtx, cancel := w.queryContext(ctx)
	defer cancel()
	return w.tx.SelectContext(queryCtx, dest, w.rebind(query), args...)
}

func (w *sqlxTxWrapper) SelectBuilder(ctx context.Context, dest any, query builder) error {
	sqlQuery, args, err := query.ToSql()
	if err != nil {
		return err
	}
	return w.Select(ctx, dest, sqlQuery, args...)
}

func (w *sqlxTxWrapper) NamedExec(ctx context.Context, query string, arg any) (sql.Result, error) {
	queryCtx, cancel := w.queryContext(ctx)
	defer cancel()
	return w.tx.NamedExecContext(queryCtx, query, arg)
}

func (w *sqlxTxWrapper) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	queryCtx, cancel := w.queryContext(ctx)
	defer cancel()
	return w.tx.ExecContext(queryCtx, w.rebind(query), args...)
}

func (w *sqlxTxWrapper) ExecBuilder(ctx context.Context, query builder) (sql.Result, error) {
	sqlQuery, args, err := query.ToSql()
	if err != nil {
		return nil, err
	}
	return w.Exec(ctx, sqlQuery, args...)
}

func (w *sqlxTxWrapper) Commit() error {
	return w.tx.Commit()
}

func (w *sqlxTxWrapper) Rollback() error {
	return w.tx.Rollback()
}

func (w *sqlxTxWrapper) queryContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, w.queryTimeout)
}

var (
	_ sqlxExecutor         = (*sqlxDBWrapper)(nil)
	_ sqlxExecutor         = (*sqlxTxWrapper)(nil)
	_ transactionFinalizer = (*sqlxTxWrapper)(nil)
)
