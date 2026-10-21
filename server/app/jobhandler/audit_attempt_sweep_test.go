// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package jobhandler

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	jobengine "github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type auditAttemptSweeperFake struct {
	results []int64
	err     error
	inputs  []store.AuditAttemptAbandonment
}

func (f *auditAttemptSweeperFake) FailAbandonedAttempts(_ context.Context, input *store.AuditAttemptAbandonment) (int64, error) {
	f.inputs = append(f.inputs, *input)
	if f.err != nil {
		return 0, f.err
	}
	if len(f.results) == 0 {
		return 0, nil
	}
	changed := f.results[0]
	f.results = f.results[1:]
	return changed, nil
}

func runAuditAttemptSweep(t *testing.T, sweeper *auditAttemptSweeperFake, batch int) jobengine.Outcome {
	t.Helper()
	command, err := json.Marshal(AuditAttemptSweepCommandV1{BatchSize: batch})
	require.NoError(t, err)
	at := time.Now().UTC()
	record, err := model.NewJob(model.NewJobID(), model.JobTypeAuditAttemptSweep, 1, command, "sweep", at, at, 3)
	require.NoError(t, err)
	return (auditAttemptSweepHandler{audits: sweeper}).Run(context.Background(), jobengine.NewExecution(record, nil, nil, nil))
}

func TestAuditAttemptSweepContinuesWhileBatchesAreFull(t *testing.T) {
	t.Parallel()

	sweeper := &auditAttemptSweeperFake{results: []int64{10, 10, 3}}
	outcome := runAuditAttemptSweep(t, sweeper, 10)
	require.Equal(t, jobengine.OutcomeSucceeded, outcome.Kind, "outcome = %#v", outcome)
	require.Len(t, sweeper.inputs, 3)
	require.Equal(t, store.AuditAttemptAbandonment{Age: 24 * time.Hour, Limit: 10}, sweeper.inputs[0])
	var result AuditAttemptSweepResultV1
	require.NoError(t, json.Unmarshal(outcome.Result, &result))
	require.Equal(t, int64(23), result.Failed)
}

func TestAuditAttemptSweepBoundsOneRun(t *testing.T) {
	t.Parallel()

	results := make([]int64, 100)
	for index := range results {
		results[index] = 5
	}
	sweeper := &auditAttemptSweeperFake{results: results}
	outcome := runAuditAttemptSweep(t, sweeper, 5)
	require.Equal(t, jobengine.OutcomeSucceeded, outcome.Kind)
	require.Len(t, sweeper.inputs, maximumAuditAttemptSweepBatches)
}

func TestAuditAttemptSweepRetriesAStoreFailure(t *testing.T) {
	t.Parallel()

	outcome := runAuditAttemptSweep(t, &auditAttemptSweeperFake{err: errors.New("down")}, 5)
	require.Equal(t, jobengine.OutcomeRetryableFailure, outcome.Kind)
}
