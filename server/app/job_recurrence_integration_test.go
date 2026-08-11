//go:build integration

// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	jobengine "github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/sqlstore"
)

type recurrenceIntegrationHandler struct{}

func (recurrenceIntegrationHandler) Run(context.Context, jobengine.Execution) jobengine.Outcome {
	return jobengine.Outcome{Kind: jobengine.OutcomeSucceeded, ResultVersion: 1, Result: json.RawMessage(`{}`)}
}

type signalingCleanupProposer struct {
	mu            sync.Mutex
	next          jobHistoryCleanupProposer
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
		next:  jobHistoryCleanupProposer{jobs: persistence.Job(), now: time.Now},
		calls: make(chan recurrenceProposalResult, 3),
	}
	first := newRecurrenceIntegrationEngine(t, persistence, "node-a", proposer, occurrence)
	second := newRecurrenceIntegrationEngine(t, persistence, "node-b", proposer, occurrence)
	if err := first.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := second.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	results := []recurrenceProposalResult{<-proposer.calls, <-proposer.calls}
	for _, result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
	}
	waitForCleanupOccurrenceSucceeded(t, persistence, results[0].occurrence)
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}

	restarted := newRecurrenceIntegrationEngine(t, persistence, "node-a-restarted", proposer, occurrence)
	if err := restarted.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	restartResult := <-proposer.calls
	if restartResult.err != nil {
		t.Fatal(restartResult.err)
	}
	if err := restarted.Close(); err != nil {
		t.Fatal(err)
	}
	assertOneCleanupOccurrence(t, persistence, results[0].occurrence)
}

func TestRecurringMaintenanceRecoversTransientProposalFailureWithoutRepeatingCompletion(t *testing.T) {
	persistence := openClusteredJobIntegrationStore(t)
	occurrence := time.Date(2026, time.August, 11, 8, 0, 0, 0, time.UTC)
	proposer := &signalingCleanupProposer{
		next:          jobHistoryCleanupProposer{jobs: persistence.Job(), now: time.Now},
		remainingFail: 1,
		calls:         make(chan recurrenceProposalResult, 3),
	}
	engine := newRecurrenceIntegrationEngine(t, persistence, "node-a", proposer, occurrence)
	if err := engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	failed, succeeded := <-proposer.calls, <-proposer.calls
	if failed.err == nil || succeeded.err != nil || !failed.occurrence.Equal(succeeded.occurrence) {
		t.Fatalf("proposal results = %#v, %#v", failed, succeeded)
	}
	waitForCleanupOccurrenceSucceeded(t, persistence, succeeded.occurrence)
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}

	restarted := newRecurrenceIntegrationEngine(t, persistence, "node-b", proposer, occurrence)
	if err := restarted.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	restartResult := <-proposer.calls
	if restartResult.err != nil || !restartResult.occurrence.Equal(succeeded.occurrence) {
		t.Fatalf("restart proposal = %#v", restartResult)
	}
	if err := restarted.Close(); err != nil {
		t.Fatal(err)
	}
	assertOneCleanupOccurrence(t, persistence, succeeded.occurrence)
}

func newRecurrenceIntegrationEngine(t *testing.T, persistence *sqlstore.SQLStore, nodeID string, proposer jobengine.OccurrenceProposer, occurrence time.Time) *jobengine.Engine {
	t.Helper()
	engine, err := jobengine.New(jobengine.Config{
		Store: persistence.Job(), Descriptors: []jobengine.Descriptor{jobHistoryCleanupDescriptor(recurrenceIntegrationHandler{})},
		NodeID: nodeID, Diagnostics: &integrationJobDiagnostics{},
		Policy: jobengine.Policy{PollInterval: 5 * time.Millisecond, ProposalRetryDelay: 5 * time.Millisecond},
		Clock:  fixedRealtimeClock{at: occurrence}, Recurrences: []jobengine.Recurrence{{Name: "job-history-cleanup", Proposer: proposer}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

func waitForCleanupOccurrenceSucceeded(t *testing.T, persistence *sqlstore.SQLStore, occurrence time.Time) {
	t.Helper()
	key := "job-history-cleanup:" + occurrence.UTC().Format("2006-01-02")
	deadline := time.Now().Add(2 * time.Second)
	for {
		jobs, err := persistence.Job().List(context.Background(), store.JobListOptions{Types: []model.JobType{model.JobTypeCleanup}, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(jobs) == 1 && jobs[0].DedupeKey == key && jobs[0].Status == model.JobStatusSucceeded {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("cleanup occurrence did not succeed: %#v", jobs)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func assertOneCleanupOccurrence(t *testing.T, persistence *sqlstore.SQLStore, occurrence time.Time) {
	t.Helper()
	key := "job-history-cleanup:" + occurrence.UTC().Format("2006-01-02")
	var occurrences, jobs, attempts int
	if err := persistence.GetMaster().Get(context.Background(), &occurrences, `SELECT count(*) FROM job_permanent_occurrences WHERE type = $1 AND dedupe_key = $2`, model.JobTypeCleanup, key); err != nil {
		t.Fatal(err)
	}
	if err := persistence.GetMaster().Get(context.Background(), &jobs, `SELECT count(*) FROM jobs WHERE type = $1 AND dedupe_key = $2`, model.JobTypeCleanup, key); err != nil {
		t.Fatal(err)
	}
	if err := persistence.GetMaster().Get(context.Background(), &attempts, `SELECT count(*) FROM job_attempts a JOIN jobs j ON j.id = a.job_id WHERE j.type = $1 AND j.dedupe_key = $2`, model.JobTypeCleanup, key); err != nil {
		t.Fatal(err)
	}
	if occurrences != 1 || jobs != 1 || attempts != 1 {
		t.Fatalf("occurrences=%d jobs=%d attempts=%d, want 1 each", occurrences, jobs, attempts)
	}
}
