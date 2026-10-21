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

	"github.com/stretchr/testify/require"
)

func TestRunSQLTransactionPreservesLifecycleAndPrimaryErrors(t *testing.T) {
	t.Parallel()

	ctx := context.WithValue(context.Background(), transactionTestContextKey{}, "exact")
	// The transaction lifecycle order (begin, body, commit, deferred rollback)
	// is the contract under test, so these assertions compare exact sequences.
	var events []string
	beginFailure := errors.New("begin failed")
	beginFailed := func(got context.Context) (*transactionTestTx, error) {
		events = append(events, "begin")
		require.Equal(t, ctx, got, "begin received a replaced context")
		return nil, beginFailure
	}
	result, err := runSQLTransaction(ctx, beginFailed, "affiliation save", func(context.Context, *transactionTestTx) (string, error) {
		require.FailNow(t, "body called after begin failure")
		return "", nil
	})
	require.ErrorIs(t, err, beginFailure, "begin result")
	require.Equal(t, "begin affiliation save: begin failed", err.Error(), "begin result")
	require.Empty(t, result, "begin result")
	require.Equal(t, []string{"begin"}, events, "begin result")

	events = nil
	bodyFailure := errors.New("body failed")
	bodyTx := &transactionTestTx{events: &events, rollbackErr: errors.New("rollback failed")}
	result, err = runSQLTransaction(ctx, transactionTestBegin(bodyTx, &events), "affiliation save", func(context.Context, *transactionTestTx) (string, error) {
		events = append(events, "body")
		return "discarded", bodyFailure
	})
	require.ErrorIs(t, err, bodyFailure, "body outcome result")
	require.Empty(t, result, "body outcome result")
	require.Equal(t, []string{"begin", "body", "rollback"}, events, "body outcome result")

	events = nil
	commitFailure := errors.New("commit failed")
	commitTx := &transactionTestTx{events: &events, commitErr: commitFailure, rollbackErr: errors.New("rollback failed")}
	result, err = runSQLTransaction(ctx, transactionTestBegin(commitTx, &events), "affiliation save", func(context.Context, *transactionTestTx) (string, error) {
		events = append(events, "body")
		return "discarded", nil
	})
	require.ErrorIs(t, err, commitFailure, "commit outcome result")
	require.Equal(t, "commit affiliation save: commit failed", err.Error(), "commit outcome result")
	require.Empty(t, result, "commit outcome result")
	require.Equal(t, []string{"begin", "body", "commit", "rollback"}, events, "commit outcome result")

	events = nil
	successTx := &transactionTestTx{events: &events, rollbackErr: errors.New("rollback after commit failed")}
	result, err = runSQLTransaction(ctx, transactionTestBegin(successTx, &events), "affiliation save", func(got context.Context, tx *transactionTestTx) (string, error) {
		events = append(events, "body")
		require.Equal(t, ctx, got, "body received replaced transaction inputs")
		require.Same(t, successTx, tx, "body received replaced transaction inputs")
		return "result", nil
	})
	require.NoError(t, err, "success outcome result")
	require.Equal(t, "result", result, "success outcome result")
	require.Equal(t, []string{"begin", "body", "commit", "rollback"}, events, "success outcome result")
}

func TestRunSQLTransactionRollsBackPanic(t *testing.T) {
	t.Parallel()

	// The transaction lifecycle order is the contract under test.
	var events []string
	tx := &transactionTestTx{events: &events}
	defer func() {
		recovered := recover()
		require.Equal(t, "boom", recovered, "recovered")
		require.Equal(t, []string{"begin", "body", "rollback"}, events)
	}()
	_, _ = runSQLTransaction(context.Background(), transactionTestBegin(tx, &events), "affiliation save", func(context.Context, *transactionTestTx) (string, error) {
		events = append(events, "body")
		panic("boom")
	})
}

func TestExecuteSQLTransactionSupportsCharacterizedCompletionPolicies(t *testing.T) {
	t.Parallel()

	beginFailure := errors.New("raw begin")
	result, err := executeSQLTransaction(context.Background(),
		func(context.Context) (*transactionTestTx, error) { return nil, beginFailure },
		sqlTransactionPolicy[string]{beginError: func(err error) error { return err }},
		func(context.Context, *transactionTestTx) (string, error) { return "unexpected", nil },
	)
	require.Equal(t, beginFailure, err, "raw begin result")
	require.Empty(t, result, "raw begin result")

	// The transaction lifecycle order is the contract under test.
	var events []string
	rollbackOnly := &transactionTestTx{events: &events}
	result, err = executeSQLTransaction(context.Background(), transactionTestBegin(rollbackOnly, &events),
		sqlTransactionPolicy[string]{beginError: func(err error) error { return err }},
		func(context.Context, *transactionTestTx) (string, error) {
			events = append(events, "body")
			return "observed", nil
		},
	)
	require.NoError(t, err, "rollback-only result")
	require.Equal(t, "observed", result, "rollback-only result")
	require.Equal(t, []string{"begin", "body", "rollback"}, events, "rollback-only result")

	events = nil
	commitFailure := errors.New("commit")
	branchTx := &transactionTestTx{events: &events, commitErr: commitFailure}
	result, err = executeSQLTransaction(context.Background(), transactionTestBegin(branchTx, &events),
		sqlTransactionPolicy[string]{
			beginError: func(err error) error { return err }, commit: true,
			commitError: func(result string, err error) error { return errors.New(result + ": " + err.Error()) },
		},
		func(context.Context, *transactionTestTx) (string, error) {
			events = append(events, "body")
			return "deduplicated", nil
		},
	)
	require.Empty(t, result, "branch commit result")
	require.Error(t, err, "branch commit result")
	require.Equal(t, "deduplicated: commit", err.Error(), "branch commit result")
	require.Equal(t, []string{"begin", "body", "commit", "rollback"}, events, "branch commit result")
}

type transactionTestContextKey struct{}

func transactionTestBegin(tx *transactionTestTx, events *[]string) func(context.Context) (*transactionTestTx, error) {
	return func(context.Context) (*transactionTestTx, error) {
		*events = append(*events, "begin")
		return tx, nil
	}
}

type transactionTestTx struct {
	events      *[]string
	commitErr   error
	rollbackErr error
}

func (t *transactionTestTx) Commit() error {
	*t.events = append(*t.events, "commit")
	return t.commitErr
}

func (t *transactionTestTx) Rollback() error {
	*t.events = append(*t.events, "rollback")
	return t.rollbackErr
}
