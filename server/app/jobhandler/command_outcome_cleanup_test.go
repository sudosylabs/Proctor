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
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	jobengine "github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/model"
)

type commandOutcomeCleanerFake struct {
	limit   int
	deleted int64
	err     error
}

func (f *commandOutcomeCleanerFake) DeleteExpired(_ context.Context, limit int) (int64, error) {
	f.limit = limit
	return f.deleted, f.err
}

func TestCommandOutcomeCleanupDeletesOneBoundedPage(t *testing.T) {
	cleaner := &commandOutcomeCleanerFake{deleted: 17}
	command, _ := json.Marshal(CommandOutcomeCleanupCommand{BatchSize: 500})
	at := time.Now().UTC()
	record, err := model.NewJob(model.NewJobID(), model.JobTypeCommandOutcomeCleanup, command, "cleanup", at, at, 3)
	require.NoError(t, err)
	outcome := (commandOutcomeCleanupHandler{outcomes: cleaner}).Run(context.Background(), jobengine.NewExecution(record, nil, nil, nil))
	require.Equal(t, jobengine.OutcomeSucceeded, outcome.Kind, "outcome = %#v, limit", outcome)
	require.Equal(t, 500, cleaner.limit, "outcome = %#v, limit", outcome)
	var result CommandOutcomeCleanupResult
	err = json.Unmarshal(outcome.Result, &result)
	require.NoError(t, err, "result = %#v, err", result)
	require.Equal(t, int64(17), result.Deleted, "result = %#v, err", result)
}
