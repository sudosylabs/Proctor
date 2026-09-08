// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"errors"
	"testing"
	"time"

	examattempt "github.com/sudosylabs/proctor/server/app/exam/attempt"
)

func TestExamAttemptExpiryPeriodicRunnerUsesBoundedBatchAndPreservesFailure(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("database unavailable")
	fake := &recordingExamAttemptExpiryUseCases{err: wantErr}
	err := (examAttemptExpiryPeriodicRunner{attempts: fake}).Run(context.Background())
	if !errors.Is(err, wantErr) || fake.limit != examAttemptExpiryBatchLimit ||
		examAttemptExpiryScanInterval != 2*time.Second || examAttemptExpiryPeriodicTaskName == "" {
		t.Fatalf("Run() error=%v limit=%d", err, fake.limit)
	}
}

type recordingExamAttemptExpiryUseCases struct {
	limit         int
	deliveryLimit int
	err           error
}

func (fake *recordingExamAttemptExpiryUseCases) ScanExpiredParticipations(_ context.Context, limit int) (examattempt.ExpiryScanResult, error) {
	fake.limit = limit
	return examattempt.ExpiryScanResult{}, fake.err
}

func (fake *recordingExamAttemptExpiryUseCases) ScanExpiredDeliveries(_ context.Context, limit int) (examattempt.ExpiryScanResult, error) {
	fake.deliveryLimit = limit
	return examattempt.ExpiryScanResult{}, nil
}

func TestExpiryRunnerAlsoSettlesAbandonedDeliveries(t *testing.T) {
	fake := &recordingExamAttemptExpiryUseCases{}
	if err := (examAttemptExpiryPeriodicRunner{attempts: fake}).Run(context.Background()); err != nil || fake.deliveryLimit != 200 {
		t.Fatalf("delivery scan bound=%d: %v", fake.deliveryLimit, err)
	}
}
