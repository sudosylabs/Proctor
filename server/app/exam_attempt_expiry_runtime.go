// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"time"

	examattempt "github.com/sudosylabs/proctor/server/app/exam/attempt"
	appexecution "github.com/sudosylabs/proctor/server/app/execution"
)

const executionReconciliationPeriodicTaskName = "execution-reconciliation"
const executionReconciliationInterval = 10 * time.Second

const (
	examAttemptExpiryPeriodicTaskName = "exam-attempt-participation-expiry"
	examAttemptExpiryScanInterval     = 2 * time.Second
	examAttemptExpiryBatchLimit       = 200
)

type examAttemptExpiryUseCases interface {
	ScanExpiredParticipations(context.Context, int) (examattempt.ExpiryScanResult, error)
}

// examAttemptExpiryPeriodicRunner adapts the bounded application scan to the
// generic non-durable runtime loop. Cross-node claiming and idempotency stay in
// the named Store operation; this adapter creates no durable Job occurrence.
type examAttemptExpiryPeriodicRunner struct{ attempts examAttemptExpiryUseCases }

func (runner examAttemptExpiryPeriodicRunner) Run(ctx context.Context) error {
	_, err := runner.attempts.ScanExpiredParticipations(ctx, examAttemptExpiryBatchLimit)
	return err
}

type executionReconciliationPeriodicRunner struct{ execution *appexecution.Service }

func (runner executionReconciliationPeriodicRunner) Run(ctx context.Context) error {
	_, err := runner.execution.Reconcile(ctx)
	return err
}
