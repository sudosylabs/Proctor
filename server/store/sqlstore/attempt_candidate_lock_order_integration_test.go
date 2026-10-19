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

// TestExamAttemptConnectCandidateLockOrder replays Invitation acceptance's
// lock sequence (User row FOR UPDATE, then the Class-enrollment fence) around
// a real Connect. Connect must queue on the User row without holding the
// fence; holding it would make PostgreSQL abort one side as a deadlock.
func TestExamAttemptConnectCandidateLockOrder(t *testing.T) {
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	storetest.TestExamAttemptConnectCandidateLockOrder(t, persistence, func(
		t *testing.T, ctx context.Context, candidateID model.UserID, periodID model.AcademicPeriodID, operation func() error,
	) error {
		t.Helper()
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
		_ = waitForBlockedMailQuery(t, ctx, persistence, acceptancePID, "users")

		if _, err = acceptance.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`,
			"proctor:class-enrollment:"+candidateID.String()+":"+periodID.String()); err != nil {
			return fmt.Errorf("acceptance Class-enrollment fence after Connect queued: %w", err)
		}
		if err = acceptance.Commit(); err != nil {
			return err
		}
		return <-operationResult
	})
}
