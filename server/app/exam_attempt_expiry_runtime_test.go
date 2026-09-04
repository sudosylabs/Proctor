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
	limit int
	err   error
}

func (fake *recordingExamAttemptExpiryUseCases) ScanExpiredParticipations(_ context.Context, limit int) (examattempt.ExpiryScanResult, error) {
	fake.limit = limit
	return examattempt.ExpiryScanResult{}, fake.err
}
