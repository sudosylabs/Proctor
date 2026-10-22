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

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/exam/attempt"
)

func TestExamAttemptExpiryPeriodicRunnerUsesBoundedBatchAndPreservesFailure(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("database unavailable")
	fake := &recordingExamAttemptExpiryUseCases{err: wantErr}
	err := (examAttemptExpiryPeriodicRunner{attempts: fake}).Run(context.Background())
	require.ErrorIs(t, err, wantErr, "Run() error")
	require.Equal(t, examAttemptExpiryBatchLimit, fake.limit, "Run() error")
	require.Equal(t, 2*time.Second, examAttemptExpiryScanInterval, "Run() error")
	require.NotEmpty(t, examAttemptExpiryPeriodicTaskName, "Run() error")
}

type recordingExamAttemptExpiryUseCases struct {
	limit         int
	deliveryLimit int
	err           error
}

func (f *recordingExamAttemptExpiryUseCases) ScanExpiredParticipations(_ context.Context, limit int) (attempt.ExpiryScanResult, error) {
	f.limit = limit
	return attempt.ExpiryScanResult{}, f.err
}

func (f *recordingExamAttemptExpiryUseCases) ScanExpiredDeliveries(_ context.Context, limit int) (attempt.ExpiryScanResult, error) {
	f.deliveryLimit = limit
	return attempt.ExpiryScanResult{}, nil
}

func TestExpiryRunnerAlsoSettlesAbandonedDeliveries(t *testing.T) {
	fake := &recordingExamAttemptExpiryUseCases{}
	require.NoError(t, (examAttemptExpiryPeriodicRunner{attempts: fake}).Run(context.Background()), "delivery scan bound")
	require.Equal(t, 200, fake.deliveryLimit, "delivery scan bound")
}
