//go:build integration

// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	jobengine "github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/app/jobhandler"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/sqlstore"
)

type recurrenceIntegrationHandler struct{}

func (recurrenceIntegrationHandler) Run(context.Context, jobengine.Execution) jobengine.Outcome {
	return jobengine.Outcome{Kind: jobengine.OutcomeSucceeded, Result: json.RawMessage(`{}`)}
}

type signalingCleanupProposer struct {
	mu            sync.Mutex
	next          jobengine.OccurrenceProposer
	remainingFail int
	calls         chan recurrenceProposalResult
}

type recurrenceProposalResult struct {
	occurrence time.Time
	err        error
}

type fixedRealtimeClock struct{ at time.Time }

func (c fixedRealtimeClock) Now() time.Time { return c.at }
func (fixedRealtimeClock) NewTimer(delay time.Duration) jobengine.Timer {
	return realtimeTimer{timer: time.NewTimer(delay)}
}

type realtimeTimer struct{ timer *time.Timer }

func (t realtimeTimer) C() <-chan time.Time { return t.timer.C }
func (t realtimeTimer) Stop() bool          { return t.timer.Stop() }

func (p *signalingCleanupProposer) Propose(ctx context.Context, occurrence time.Time) error {
	p.mu.Lock()
	if p.remainingFail > 0 {
		p.remainingFail--
		p.mu.Unlock()
		err := errors.New("temporary proposal failure")
		p.calls <- recurrenceProposalResult{occurrence: occurrence, err: err}
		return err
	}
	p.mu.Unlock()
	err := p.next.Propose(ctx, occurrence)
	p.calls <- recurrenceProposalResult{occurrence: occurrence, err: err}
	return err
}

func TestRecurringMaintenanceUsesOnePermanentOccurrenceAcrossNodesAndRestart(t *testing.T) {
	persistence := openClusteredJobIntegrationStore(t)
	occurrence := time.Date(2026, time.August, 11, 8, 0, 0, 0, time.UTC)
	proposer := &signalingCleanupProposer{
		next: integrationRecurrenceProposer(t, jobhandler.NewCatalog(jobhandler.CatalogDependencies{
			JobStore: persistence.Job(), Now: time.Now,
		}), "job-history-cleanup"),
		calls: make(chan recurrenceProposalResult, 3),
	}
	first := newRecurrenceIntegrationEngine(t, persistence, "node-a", proposer, occurrence)
	second := newRecurrenceIntegrationEngine(t, persistence, "node-b", proposer, occurrence)
	require.NoError(t, first.Start(context.Background()))
	require.NoError(t, second.Start(context.Background()))
	results := []recurrenceProposalResult{<-proposer.calls, <-proposer.calls}
	for _, result := range results {
		require.NoError(t, result.err)
	}
	waitForCleanupOccurrenceSucceeded(t, persistence, results[0].occurrence)
	require.NoError(t, first.Close())
	require.NoError(t, second.Close())

	restarted := newRecurrenceIntegrationEngine(t, persistence, "node-a-restarted", proposer, occurrence)
	require.NoError(t, restarted.Start(context.Background()))
	restartResult := <-proposer.calls
	require.NoError(t, restartResult.err)
	require.NoError(t, restarted.Close())
	assertOneCleanupOccurrence(t, persistence, results[0].occurrence)
}

func TestRecurringMaintenanceRecoversTransientProposalFailureWithoutRepeatingCompletion(t *testing.T) {
	persistence := openClusteredJobIntegrationStore(t)
	occurrence := time.Date(2026, time.August, 11, 8, 0, 0, 0, time.UTC)
	proposer := &signalingCleanupProposer{
		next: integrationRecurrenceProposer(t, jobhandler.NewCatalog(jobhandler.CatalogDependencies{
			JobStore: persistence.Job(), Now: time.Now,
		}), "job-history-cleanup"),
		remainingFail: 1,
		calls:         make(chan recurrenceProposalResult, 3),
	}
	engine := newRecurrenceIntegrationEngine(t, persistence, "node-a", proposer, occurrence)
	require.NoError(t, engine.Start(context.Background()))
	failed, succeeded := <-proposer.calls, <-proposer.calls
	require.Error(t, failed.err, "proposal results = %#v, %#v", failed, succeeded)
	require.NoError(t, succeeded.err, "proposal results = %#v, %#v", failed, succeeded)
	require.True(t, failed.occurrence.Equal(succeeded.occurrence), "proposal results = %#v, %#v", failed, succeeded)
	waitForCleanupOccurrenceSucceeded(t, persistence, succeeded.occurrence)
	require.NoError(t, engine.Close())

	restarted := newRecurrenceIntegrationEngine(t, persistence, "node-b", proposer, occurrence)
	require.NoError(t, restarted.Start(context.Background()))
	restartResult := <-proposer.calls
	require.NoError(t, restartResult.err)
	require.True(t, restartResult.occurrence.Equal(succeeded.occurrence))
	require.NoError(t, restarted.Close())
	assertOneCleanupOccurrence(t, persistence, succeeded.occurrence)
}

func newRecurrenceIntegrationEngine(t *testing.T, persistence *sqlstore.SQLStore, nodeID string, proposer jobengine.OccurrenceProposer, occurrence time.Time) *jobengine.Engine {
	t.Helper()
	descriptor := integrationJobDescriptor(t, jobhandler.NewCatalog(jobhandler.CatalogDependencies{
		JobStore: persistence.Job(), Now: time.Now,
	}), model.JobTypeCleanup)
	descriptor.Handler = recurrenceIntegrationHandler{}
	engine, err := jobengine.New(jobengine.Config{
		Store: persistence.Job(), Descriptors: []jobengine.Descriptor{descriptor},
		NodeID: nodeID, Diagnostics: &integrationJobDiagnostics{},
		Policy: jobengine.Policy{PollInterval: 5 * time.Millisecond, ProposalRetryDelay: 5 * time.Millisecond},
		Clock:  fixedRealtimeClock{at: occurrence}, Recurrences: []jobengine.Recurrence{{Name: "job-history-cleanup", Proposer: proposer}},
	})
	require.NoError(t, err)
	return engine
}

func integrationJobDescriptor(t *testing.T, catalog jobhandler.Catalog, jobType model.JobType) jobengine.Descriptor {
	t.Helper()
	for _, descriptor := range catalog.Descriptors {
		if descriptor.Type == jobType {
			return descriptor
		}
	}
	require.FailNow(t, fmt.Sprintf("production Job catalog does not contain %q", jobType))
	return jobengine.Descriptor{}
}

func integrationRecurrenceProposer(t *testing.T, catalog jobhandler.Catalog, name string) jobengine.OccurrenceProposer {
	t.Helper()
	for _, recurrence := range catalog.Recurrences {
		if recurrence.Name == name {
			return recurrence.Proposer
		}
	}
	require.FailNow(t, fmt.Sprintf("production Job catalog does not contain recurrence %q", name))
	return nil
}

func waitForCleanupOccurrenceSucceeded(t *testing.T, persistence *sqlstore.SQLStore, occurrence time.Time) {
	t.Helper()
	key := "job-history-cleanup:" + occurrence.UTC().Format("2006-01-02")
	deadline := time.Now().Add(2 * time.Second)
	for {
		jobs, err := persistence.Job().List(context.Background(), store.JobListOptions{Types: []model.JobType{model.JobTypeCleanup}, Limit: 10})
		require.NoError(t, err)
		if len(jobs) == 1 && jobs[0].DedupeKey == key && jobs[0].Status == model.JobStatusSucceeded {
			return
		}
		require.False(t, time.Now().After(deadline), "cleanup occurrence did not succeed: %#v", jobs)
		time.Sleep(5 * time.Millisecond)
	}
}

func assertOneCleanupOccurrence(t *testing.T, persistence *sqlstore.SQLStore, occurrence time.Time) {
	t.Helper()
	key := "job-history-cleanup:" + occurrence.UTC().Format("2006-01-02")
	var occurrences, jobs, attempts int
	require.NoError(t, persistence.GetMaster().Get(context.Background(), &occurrences, `SELECT count(*) FROM job_permanent_occurrences WHERE type = $1 AND dedupe_key = $2`, model.JobTypeCleanup, key))
	require.NoError(t, persistence.GetMaster().Get(context.Background(), &jobs, `SELECT count(*) FROM jobs WHERE type = $1 AND dedupe_key = $2`, model.JobTypeCleanup, key))
	require.NoError(t, persistence.GetMaster().Get(context.Background(), &attempts, `SELECT count(*) FROM job_attempts a JOIN jobs j ON j.id = a.job_id WHERE j.type = $1 AND j.dedupe_key = $2`, model.JobTypeCleanup, key))
	require.Equal(t, 1, occurrences, "occurrences")
	require.Equal(t, 1, jobs, "occurrences")
	require.Equal(t, 1, attempts, "occurrences")
}
