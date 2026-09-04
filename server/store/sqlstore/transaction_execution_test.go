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
	"reflect"
	"testing"
)

func TestRunSQLTransactionPreservesLifecycleAndPrimaryErrors(t *testing.T) {
	t.Parallel()

	ctx := context.WithValue(context.Background(), transactionTestContextKey{}, "exact")
	var events []string
	beginFailure := errors.New("begin failed")
	beginFailed := func(got context.Context) (*transactionTestTx, error) {
		events = append(events, "begin")
		if got != ctx {
			t.Fatal("begin received a replaced context")
		}
		return nil, beginFailure
	}
	result, err := runSQLTransaction(ctx, beginFailed, "affiliation save", func(context.Context, *transactionTestTx) (string, error) {
		t.Fatal("body called after begin failure")
		return "", nil
	})
	if !errors.Is(err, beginFailure) || err.Error() != "begin affiliation save: begin failed" || result != "" || !reflect.DeepEqual(events, []string{"begin"}) {
		t.Fatalf("begin result=%q error=%v events=%v", result, err, events)
	}

	events = nil
	bodyFailure := errors.New("body failed")
	bodyTx := &transactionTestTx{events: &events, rollbackErr: errors.New("rollback failed")}
	result, err = runSQLTransaction(ctx, transactionTestBegin(bodyTx, &events), "affiliation save", func(context.Context, *transactionTestTx) (string, error) {
		events = append(events, "body")
		return "discarded", bodyFailure
	})
	if !errors.Is(err, bodyFailure) || result != "" || !reflect.DeepEqual(events, []string{"begin", "body", "rollback"}) {
		t.Fatalf("body outcome result=%q error=%v events=%v", result, err, events)
	}

	events = nil
	commitFailure := errors.New("commit failed")
	commitTx := &transactionTestTx{events: &events, commitErr: commitFailure, rollbackErr: errors.New("rollback failed")}
	result, err = runSQLTransaction(ctx, transactionTestBegin(commitTx, &events), "affiliation save", func(context.Context, *transactionTestTx) (string, error) {
		events = append(events, "body")
		return "discarded", nil
	})
	if !errors.Is(err, commitFailure) || err.Error() != "commit affiliation save: commit failed" || result != "" || !reflect.DeepEqual(events, []string{"begin", "body", "commit", "rollback"}) {
		t.Fatalf("commit outcome result=%q error=%v events=%v", result, err, events)
	}

	events = nil
	successTx := &transactionTestTx{events: &events, rollbackErr: errors.New("rollback after commit failed")}
	result, err = runSQLTransaction(ctx, transactionTestBegin(successTx, &events), "affiliation save", func(got context.Context, tx *transactionTestTx) (string, error) {
		events = append(events, "body")
		if got != ctx || tx != successTx {
			t.Fatal("body received replaced transaction inputs")
		}
		return "result", nil
	})
	if err != nil || result != "result" || !reflect.DeepEqual(events, []string{"begin", "body", "commit", "rollback"}) {
		t.Fatalf("success outcome result=%q error=%v events=%v", result, err, events)
	}
}

func TestRunSQLTransactionRollsBackPanic(t *testing.T) {
	t.Parallel()

	var events []string
	tx := &transactionTestTx{events: &events}
	defer func() {
		if recovered := recover(); recovered != "boom" {
			t.Fatalf("recovered = %v, want boom", recovered)
		}
		if !reflect.DeepEqual(events, []string{"begin", "body", "rollback"}) {
			t.Fatalf("events = %v", events)
		}
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
	if err != beginFailure || result != "" {
		t.Fatalf("raw begin result=%q error=%v", result, err)
	}

	var events []string
	rollbackOnly := &transactionTestTx{events: &events}
	result, err = executeSQLTransaction(context.Background(), transactionTestBegin(rollbackOnly, &events),
		sqlTransactionPolicy[string]{beginError: func(err error) error { return err }},
		func(context.Context, *transactionTestTx) (string, error) {
			events = append(events, "body")
			return "observed", nil
		},
	)
	if err != nil || result != "observed" || !reflect.DeepEqual(events, []string{"begin", "body", "rollback"}) {
		t.Fatalf("rollback-only result=%q error=%v events=%v", result, err, events)
	}

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
	if result != "" || err == nil || err.Error() != "deduplicated: commit" || !reflect.DeepEqual(events, []string{"begin", "body", "commit", "rollback"}) {
		t.Fatalf("branch commit result=%q error=%v events=%v", result, err, events)
	}
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
