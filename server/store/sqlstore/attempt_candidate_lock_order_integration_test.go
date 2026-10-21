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
	"fmt"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store/storetest"
)

// candidateLockProbe replays Invitation acceptance's lock sequence (the User
// row FOR UPDATE, then a candidate-scoped fence or lifecycle lock) around a
// real Store operation. The operation must queue on the User row without
// holding the fence; holding it would make PostgreSQL abort one side as a
// deadlock.
func candidateLockProbe(persistence *SQLStore) storetest.CandidateLockProbe {
	return func(t *testing.T, ctx context.Context, candidateID model.UserID, fence storetest.CandidateFence, operation func() error) error {
		t.Helper()
		var key string
		switch fence.Kind {
		case storetest.CandidateFenceClassEnrollment:
			key = advisoryLockKey("class-enrollment", candidateID.String(), fence.AcademicPeriodID.String())
		case storetest.CandidateFenceAttemptAdmission:
			key = advisoryLockKey("exam-attempt-admission", candidateID.String())
		case storetest.CandidateFenceClassLifecycle:
			key = classLifecycleLock
		default:
			return fmt.Errorf("unknown candidate fence %q", fence.Kind)
		}
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		controller, err := persistence.GetMaster().DB().Conn(ctx)
		if err != nil {
			return err
		}
		defer controller.Close()
		acceptance, err := controller.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = acceptance.Rollback() }()
		var acceptancePID int
		if err = acceptance.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&acceptancePID); err != nil {
			return err
		}
		if _, err = acceptance.ExecContext(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, candidateID.String()); err != nil {
			return err
		}

		operationResult := make(chan error, 1)
		go func() { operationResult <- operation() }()
		// The operation must queue behind the acceptance; finishing first means
		// it never needed the User row this test fences.
		for blocked := false; !blocked; {
			select {
			case err = <-operationResult:
				return fmt.Errorf("operation finished without waiting for the candidate User: %w", err)
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Millisecond):
			}
			if err = persistence.GetMaster().DB().QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity
				WHERE pid <> pg_backend_pid() AND $1 = ANY(pg_blocking_pids(pid)))`, acceptancePID).Scan(&blocked); err != nil {
				return err
			}
		}

		if _, err = acceptance.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, key); err != nil {
			return fmt.Errorf("acceptance %s lock after the operation queued: %w", fence.Kind, err)
		}
		if err = acceptance.Commit(); err != nil {
			return err
		}
		return <-operationResult
	}
}

func TestExamAttemptConnectCandidateLockOrder(t *testing.T) {
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	storetest.TestExamAttemptConnectCandidateLockOrder(t, persistence, candidateLockProbe(persistence))
}

func TestExamManagerAddCandidateLockOrder(t *testing.T) {
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	storetest.TestExamManagerAddCandidateLockOrder(t, persistence, candidateLockProbe(persistence))
}

func TestRoleBindingCandidateLockOrder(t *testing.T) {
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	storetest.TestRoleBindingCandidateLockOrder(t, persistence, candidateLockProbe(persistence))
}

func TestClassEnrollmentCandidateLockOrder(t *testing.T) {
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	storetest.TestClassEnrollmentCandidateLockOrder(t, persistence, candidateLockProbe(persistence))
}
