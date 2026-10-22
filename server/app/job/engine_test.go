// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package job

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type jobRunnerStoreFake struct {
	mu              sync.Mutex
	heartbeats      int
	heartbeatErrors []error
	checkpoints     int
	checkpoint      *store.JobCheckpoint
	completion      *store.JobCompletion
	cancelRequested bool
	reservation     *store.JobWorkReservation
	claim           *store.JobClaim
	claimed         bool
	claimRequests   chan struct{}
}

type jobMetricsFake struct {
	mu         sync.Mutex
	activities []Activity
	started    int
	finished   int
	outcomes   []ExecutionOutcome
}

func (r *jobMetricsFake) Started(model.JobType) {
	r.mu.Lock()
	r.started++
	r.mu.Unlock()
}
func (r *jobMetricsFake) Finished(_ model.JobType, outcome ExecutionOutcome, _ time.Duration) {
	r.mu.Lock()
	r.finished++
	r.outcomes = append(r.outcomes, outcome)
	r.mu.Unlock()
}
func (r *jobMetricsFake) Record(activity Activity) {
	r.mu.Lock()
	r.activities = append(r.activities, activity)
	r.mu.Unlock()
}

func allowJobWorkReservation() func(context.Context, int, int) (bool, error) {
	consumed := 0
	return func(_ context.Context, units, limit int) (bool, error) {
		if consumed+units > limit {
			return false, nil
		}
		consumed += units
		return true, nil
	}
}
func (s *jobRunnerStoreFake) ReserveWork(_ context.Context, reservation *store.JobWorkReservation) (*store.JobWorkReservationResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := *reservation
	s.reservation = &copy
	return &store.JobWorkReservationResult{Reserved: true, Consumed: reservation.Units}, nil
}

func (*jobRunnerStoreFake) Enqueue(context.Context, *store.JobEnqueue) (*model.Job, bool, error) {
	return nil, false, nil
}
func (s *jobRunnerStoreFake) ClaimNext(context.Context, *store.JobClaimRequest) (*store.JobClaim, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.claimRequests != nil {
		select {
		case s.claimRequests <- struct{}{}:
		default:
		}
	}
	if s.claim == nil || s.claimed {
		return nil, store.NewErrNotFound("job", "claimable")
	}
	s.claimed = true
	return s.claim, nil
}
func (s *jobRunnerStoreFake) Heartbeat(_ context.Context, _ *store.JobHeartbeat) (*model.JobAttempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.heartbeats++
	if len(s.heartbeatErrors) > 0 {
		err := s.heartbeatErrors[0]
		s.heartbeatErrors = s.heartbeatErrors[1:]
		if err != nil {
			return nil, err
		}
	}
	return &model.JobAttempt{}, nil
}
func (s *jobRunnerStoreFake) Checkpoint(_ context.Context, checkpoint *store.JobCheckpoint) (*model.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := *checkpoint
	s.checkpoint = &copy
	s.checkpoints++
	return &model.Job{}, nil
}
func (s *jobRunnerStoreFake) Complete(_ context.Context, completion *store.JobCompletion) (*model.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := *completion
	s.completion = &copy
	return &model.Job{}, nil
}
func (*jobRunnerStoreFake) Get(context.Context, model.JobID) (*model.Job, error) { return nil, nil }
func (*jobRunnerStoreFake) ListAttempts(context.Context, model.JobID) ([]model.JobAttempt, error) {
	return nil, nil
}
func (*jobRunnerStoreFake) List(context.Context, store.JobListOptions) ([]*model.Job, error) {
	return nil, nil
}
func (*jobRunnerStoreFake) ListAttemptsPage(context.Context, store.JobAttemptListOptions) ([]model.JobAttempt, error) {
	return nil, nil
}
func (s *jobRunnerStoreFake) CancellationRequested(context.Context, model.JobAttemptID, model.JobClaimToken) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cancelRequested, nil
}
func (*jobRunnerStoreFake) Cancel(context.Context, *store.JobMutation) (*model.Job, error) {
	return nil, nil
}
func (*jobRunnerStoreFake) Retry(context.Context, *store.JobMutation) (*model.Job, error) {
	return nil, nil
}
func (*jobRunnerStoreFake) DeleteTerminalHistory(context.Context, *store.JobHistoryCleanup) (*store.JobHistoryCleanupResult, error) {
	return nil, nil
}

type jobDiagnosticsFake struct{ errors []error }

func (d *jobDiagnosticsFake) ErrorContext(_ context.Context, _ string, err error) {
	d.errors = append(d.errors, err)
}

type noOpProposer struct{}

func (noOpProposer) Propose(context.Context, time.Time) error { return nil }

func TestEngineValidatesRecurrencesAtConstruction(t *testing.T) {
	t.Parallel()
	descriptor := testDescriptor(handlerFunc(func(context.Context, Execution) Outcome { return succeededOutcome() }))
	config := Config{
		Store: &jobRunnerStoreFake{}, Descriptors: []Descriptor{descriptor}, NodeID: "node-a",
		Diagnostics: &jobDiagnosticsFake{}, Policy: Policy{PollInterval: time.Second},
		Recurrences: []Recurrence{{Name: "daily", Proposer: noOpProposer{}}, {Name: "daily", Proposer: noOpProposer{}}},
	}
	_, err := New(config)
	require.Error(t, err, "New() accepted duplicate recurrence names")
	config.Recurrences = []Recurrence{{Name: "", Proposer: noOpProposer{}}}
	_, err = New(config)
	require.Error(t, err, "New() accepted an unnamed recurrence")
	config.Recurrences = []Recurrence{{Name: "unsafe\nname", Proposer: noOpProposer{}}}
	_, err = New(config)
	require.Error(t, err, "New() accepted an unsafe recurrence name")
	config.Recurrences = []Recurrence{{Name: "daily"}}
	_, err = New(config)
	require.Error(t, err, "New() accepted a recurrence without a proposer")
}

func TestEngineClaimsAndCompletesThroughItsPublicLifecycle(t *testing.T) {
	t.Parallel()
	claim := jobRunnerClaim(t)
	descriptor := testDescriptor(handlerFunc(func(context.Context, Execution) Outcome { return succeededOutcome() }))
	persistence := &jobRunnerStoreFake{claim: claim}
	recorder := &jobMetricsFake{}
	engine, err := New(Config{
		Store: persistence, Descriptors: []Descriptor{descriptor}, NodeID: "node-a",
		Diagnostics: &jobDiagnosticsFake{}, Recorder: recorder, Policy: Policy{PollInterval: time.Millisecond},
	})
	require.NoError(t, err)
	described, describeErr := engine.Descriptor(descriptor.Type)
	require.NoError(t, describeErr, "Descriptor() exposed executable handler")
	require.Nil(t, described.Handler, "Descriptor() exposed executable handler")
	err = engine.Start(context.Background())
	require.NoError(t, err)
	engine.Wake()
	deadline := time.Now().Add(time.Second)
	for {
		persistence.mu.Lock()
		completion := persistence.completion
		persistence.mu.Unlock()
		if completion != nil {
			break
		}
		require.False(t, time.Now().After(deadline), "Engine did not claim and complete queued work")
		time.Sleep(time.Millisecond)
	}
	err = engine.Close()
	require.NoError(t, err)
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	require.Equal(t, 1, recorder.started, "execution metrics = started")
	require.Equal(t, 1, recorder.finished, "execution metrics = started")
	seen := map[string]bool{}
	for _, activity := range recorder.activities {
		seen[activity.Operation+"/"+activity.Outcome] = true
	}
	require.True(t, seen["claim/success"])
	require.True(t, seen["complete/success"])
}

func TestEngineRecordsUnsupportedClaimAsAnExecution(t *testing.T) {
	t.Parallel()

	claim := jobRunnerClaim(t)
	claim.Job.Type = model.JobTypeMailCleanup
	descriptor := testDescriptor(handlerFunc(func(context.Context, Execution) Outcome { return succeededOutcome() }))
	persistence := &jobRunnerStoreFake{claim: claim}
	recorder := &jobMetricsFake{}
	engine, err := New(Config{
		Store: persistence, Descriptors: []Descriptor{descriptor}, NodeID: "node-a",
		Diagnostics: &jobDiagnosticsFake{}, Recorder: recorder, Policy: Policy{PollInterval: time.Millisecond},
	})
	require.NoError(t, err)
	err = engine.Start(context.Background())
	require.NoError(t, err)
	engine.Wake()
	deadline := time.Now().Add(time.Second)
	for {
		persistence.mu.Lock()
		completion := persistence.completion
		persistence.mu.Unlock()
		if completion != nil {
			break
		}
		require.False(t, time.Now().After(deadline), "Engine did not complete the unsupported claim")
		time.Sleep(time.Millisecond)
	}
	err = engine.Close()
	require.NoError(t, err)
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	require.Equal(t, 1, recorder.started, "execution metrics = started")
	require.Equal(t, 1, recorder.finished, "execution metrics = started")
	require.Len(t, recorder.outcomes, 1, "execution metrics = started")
	require.Equal(t, ExecutionPermanentFailure, recorder.outcomes[0], "execution metrics = started")
}

func TestEngineSchedulesRetryWithBoundedDeterministicBackoff(t *testing.T) {
	t.Parallel()
	descriptor := testDescriptor(handlerFunc(func(context.Context, Execution) Outcome {
		return RetryableFailure("dependency.unavailable", errors.New("temporary"))
	}))
	descriptor.BaseRetryDelay = 100 * time.Millisecond
	descriptor.MaximumRetryDelay = 450 * time.Millisecond

	for _, test := range []struct {
		attempt int
		want    time.Duration
	}{{attempt: 1, want: 119400 * time.Microsecond}, {attempt: 2, want: 238800 * time.Microsecond}, {attempt: 3, want: 450 * time.Millisecond}, {attempt: 4, want: 450 * time.Millisecond}} {
		t.Run(fmt.Sprintf("attempt=%v", test.attempt), func(t *testing.T) {
			claim := jobRunnerClaim(t)
			claim.Job.ID = model.JobID("job-fixed")
			claim.Attempt.Number = test.attempt
			persistence := &jobRunnerStoreFake{}
			registry, err := NewRegistry([]Descriptor{descriptor})
			require.NoError(t, err)
			engine, err := newTestEngine(persistence, registry, "node-a", &jobDiagnosticsFake{}, time.Second)
			require.NoError(t, err)
			engine.execute(context.Background(), descriptor, claim)
			persistence.mu.Lock()
			completion := persistence.completion
			persistence.mu.Unlock()
			require.NotNil(t, completion, "attempt %d retry completion", test.attempt)
			require.Equal(t, store.JobCompletionRetryableFailure, completion.Kind, "attempt %d retry completion", test.attempt)
			require.Equal(t, test.want, completion.RetryDelay, "attempt %d retry completion", test.attempt)
		})
	}
}

func TestEngineRelinquishesIncompatibleWorkWithoutConsumingItsRetryBudget(t *testing.T) {
	t.Parallel()
	claim := jobRunnerClaim(t)
	descriptor := testDescriptor(handlerFunc(func(context.Context, Execution) Outcome {
		return Relinquished("worker.capability_mismatch", errors.New("another node owns the required capability"))
	}))
	descriptor.PublicErrorCodes = append(descriptor.PublicErrorCodes, "worker.capability_mismatch")
	registry, err := NewRegistry([]Descriptor{descriptor})
	require.NoError(t, err)
	persistence := &jobRunnerStoreFake{}
	runner, err := newTestEngine(persistence, registry, "node-old", &jobDiagnosticsFake{}, time.Second)
	require.NoError(t, err)

	runner.execute(context.Background(), descriptor, claim)

	persistence.mu.Lock()
	defer persistence.mu.Unlock()
	require.NotNil(t, persistence.completion)
	require.Equal(t, store.JobCompletionRelinquished, persistence.completion.Kind)
	require.Equal(t, "worker.capability_mismatch", persistence.completion.PublicErrorCode)
	require.Positive(t, int64(persistence.completion.RetryDelay))
}

func TestEngineContainsPanicsAsRetryableOutcomes(t *testing.T) {
	t.Parallel()
	claim := jobRunnerClaim(t)
	descriptor := testDescriptor(handlerFunc(func(context.Context, Execution) Outcome { panic("secret payload") }))
	registry, err := NewRegistry([]Descriptor{descriptor})
	require.NoError(t, err)
	persistence := &jobRunnerStoreFake{}
	runner, err := newTestEngine(persistence, registry, "node-a", &jobDiagnosticsFake{}, time.Second)
	require.NoError(t, err)
	runner.execute(context.Background(), descriptor, claim)
	persistence.mu.Lock()
	defer persistence.mu.Unlock()
	require.NotNil(t, persistence.completion)
	require.Equal(t, store.JobCompletionRetryableFailure, persistence.completion.Kind)
	require.Equal(t, "job.handler_panic", persistence.completion.PublicErrorCode)
}

func TestEngineFencesOccurrenceWorkReservations(t *testing.T) {
	t.Parallel()
	claim := jobRunnerClaim(t)
	var reserved bool
	descriptor := testDescriptor(handlerFunc(func(ctx context.Context, execution Execution) Outcome {
		var err error
		reserved, err = execution.ReserveWork(ctx, 2, 5)
		if err != nil {
			return RetryableFailure("dependency.unavailable", err)
		}
		return succeededOutcome()
	}))
	registry, err := NewRegistry([]Descriptor{descriptor})
	require.NoError(t, err)
	persistence := &jobRunnerStoreFake{}
	runner, err := newTestEngine(persistence, registry, "node-a", &jobDiagnosticsFake{}, time.Second)
	require.NoError(t, err)
	runner.execute(context.Background(), descriptor, claim)
	require.True(t, reserved, "reservation")
	require.NotNil(t, persistence.reservation, "reservation")
	require.Equal(t, claim.Attempt.ID, persistence.reservation.AttemptID, "reservation")
	require.Equal(t, claim.Attempt.ClaimToken, persistence.reservation.ClaimToken, "reservation")
	require.Equal(t, 2, persistence.reservation.Units, "reservation")
	require.Equal(t, 5, persistence.reservation.Limit, "reservation")
}

func TestEngineHeartbeatsLongWorkAndCompletesWithItsFence(t *testing.T) {
	t.Parallel()
	claim := jobRunnerClaim(t)
	descriptor := testDescriptor(handlerFunc(func(ctx context.Context, _ Execution) Outcome {
		select {
		case <-time.After(20 * time.Millisecond):
			return succeededOutcome()
		case <-ctx.Done():
			return RetryableFailure("job.timeout", ctx.Err())
		}
	}))
	registry, err := NewRegistry([]Descriptor{descriptor})
	require.NoError(t, err)
	persistence := &jobRunnerStoreFake{}
	runner, err := newTestEngine(persistence, registry, "node-a", &jobDiagnosticsFake{}, time.Second)
	require.NoError(t, err)
	runner.execute(context.Background(), descriptor, claim)
	persistence.mu.Lock()
	defer persistence.mu.Unlock()
	require.NotEqual(t, 0, persistence.heartbeats, "execution: heartbeats")
	require.NotNil(t, persistence.completion, "execution: heartbeats")
	require.Equal(t, store.JobCompletionSucceeded, persistence.completion.Kind, "execution: heartbeats")
	require.Equal(t, claim.Attempt.ClaimToken, persistence.completion.ClaimToken, "execution: heartbeats")
}

func TestEngineToleratesHeartbeatFailuresWithinLeaseSlack(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		failure  error
		complete bool
	}{
		{"unknown outcome keeps the claim", errors.New("database unavailable"), true},
		{"lost claim abandons the attempt", store.NewErrConflict("job_attempt", "claim_lost", nil), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			claim := jobRunnerClaim(t)
			descriptor := testDescriptor(handlerFunc(func(ctx context.Context, _ Execution) Outcome {
				select {
				case <-time.After(60 * time.Millisecond):
					return succeededOutcome()
				case <-ctx.Done():
					return RetryableFailure("job.timeout", ctx.Err())
				}
			}))
			descriptor.HeartbeatInterval = 5 * time.Millisecond
			descriptor.LeaseDuration = time.Minute
			registry, err := NewRegistry([]Descriptor{descriptor})
			require.NoError(t, err)
			persistence := &jobRunnerStoreFake{heartbeatErrors: []error{test.failure}}
			runner, err := newTestEngine(persistence, registry, "node-a", &jobDiagnosticsFake{}, time.Second)
			require.NoError(t, err)
			runner.execute(context.Background(), descriptor, claim)
			persistence.mu.Lock()
			defer persistence.mu.Unlock()
			if !test.complete {
				require.Nil(t, persistence.completion, "lost claim completed")
				return
			}
			require.NotNil(t, persistence.completion, "one failed heartbeat abandoned the attempt")
			require.Equal(t, store.JobCompletionSucceeded, persistence.completion.Kind)
			require.Greater(t, persistence.heartbeats, 1, "heartbeats after the failure")
		})
	}
}

func TestEngineCompletesHandlerThatSucceedsAtItsTimeout(t *testing.T) {
	t.Parallel()
	claim := jobRunnerClaim(t)
	descriptor := testDescriptor(handlerFunc(func(ctx context.Context, _ Execution) Outcome {
		<-ctx.Done()
		return succeededOutcome()
	}))
	descriptor.Timeout = 10 * time.Millisecond
	registry, err := NewRegistry([]Descriptor{descriptor})
	require.NoError(t, err)
	persistence := &jobRunnerStoreFake{}
	runner, err := newTestEngine(persistence, registry, "node-a", &jobDiagnosticsFake{}, time.Second)
	require.NoError(t, err)
	runner.execute(context.Background(), descriptor, claim)
	persistence.mu.Lock()
	defer persistence.mu.Unlock()
	require.NotNil(t, persistence.completion)
	require.Equal(t, store.JobCompletionSucceeded, persistence.completion.Kind, "committed success was re-run as a timeout")
}

func TestEngineObservesDurableCancellationCooperatively(t *testing.T) {
	t.Parallel()
	claim := jobRunnerClaim(t)
	descriptor := testDescriptor(handlerFunc(func(ctx context.Context, _ Execution) Outcome {
		<-ctx.Done()
		return Canceled("job.canceled")
	}))
	descriptor.HeartbeatInterval = 5 * time.Millisecond
	descriptor.Cancelable = true
	persistence := &jobRunnerStoreFake{cancelRequested: true}
	registry, err := NewRegistry([]Descriptor{descriptor})
	require.NoError(t, err)
	runner, err := newTestEngine(persistence, registry, "node-a", &jobDiagnosticsFake{}, time.Second)
	require.NoError(t, err)
	runner.execute(context.Background(), descriptor, claim)
	persistence.mu.Lock()
	defer persistence.mu.Unlock()
	require.NotNil(t, persistence.completion)
	require.Equal(t, store.JobCompletionCanceled, persistence.completion.Kind)
}

func TestEngineRejectsASucceededOutcomeWithoutResult(t *testing.T) {
	t.Parallel()
	claim := jobRunnerClaim(t)
	descriptor := testDescriptor(handlerFunc(func(context.Context, Execution) Outcome {
		return Outcome{Kind: OutcomeSucceeded}
	}))
	registry, err := NewRegistry([]Descriptor{descriptor})
	require.NoError(t, err)
	persistence := &jobRunnerStoreFake{}
	runner, err := newTestEngine(persistence, registry, "node-a", &jobDiagnosticsFake{}, time.Second)
	require.NoError(t, err)
	runner.execute(context.Background(), descriptor, claim)
	persistence.mu.Lock()
	defer persistence.mu.Unlock()
	require.NotNil(t, persistence.completion)
	require.Equal(t, store.JobCompletionPermanentFailure, persistence.completion.Kind)
	require.Equal(t, "job.result.invalid", persistence.completion.PublicErrorCode)
}

func TestEngineShutdownIsBounded(t *testing.T) {
	t.Parallel()
	descriptor := testDescriptor(handlerFunc(func(context.Context, Execution) Outcome {
		return succeededOutcome()
	}))
	descriptor.Timeout = 20 * time.Millisecond
	registry, err := NewRegistry([]Descriptor{descriptor})
	require.NoError(t, err)
	runner, err := newTestEngine(&jobRunnerStoreFake{}, registry, "node-a", &jobDiagnosticsFake{}, time.Second)
	require.NoError(t, err)
	blocked := make(chan struct{})
	runner.wg.Add(1)
	go func() {
		defer runner.wg.Done()
		<-blocked
	}()
	started := time.Now()
	err = runner.Close()
	require.Error(t, err, "Close() waited without a shutdown deadline")
	close(blocked)
	elapsed := time.Since(started)
	require.GreaterOrEqual(t, elapsed, descriptor.Timeout)
	require.LessOrEqual(t, elapsed, time.Second)
}

func TestEngineEnforcesTypedCheckpointContract(t *testing.T) {
	t.Parallel()

	claim := jobRunnerClaim(t)
	var checkpointErr error
	descriptor := testDescriptor(handlerFunc(func(ctx context.Context, execution Execution) Outcome {
		checkpointErr = execution.Checkpoint(ctx, CheckpointValue{Progress: &model.JobProgress{Current: 1, Total: 3, Stage: "rendering"}, Document: json.RawMessage(`{"cursor":1}`)})
		return succeededOutcome()
	}))
	descriptor.Checkpoints = true
	descriptor.ProgressStages = []string{"rendering"}
	registry, err := NewRegistry([]Descriptor{descriptor})
	require.NoError(t, err)
	persistence := &jobRunnerStoreFake{}
	runner, err := newTestEngine(persistence, registry, "node-a", &jobDiagnosticsFake{}, time.Second)
	require.NoError(t, err)
	runner.execute(context.Background(), descriptor, claim)
	require.NoError(t, checkpointErr)
	persistence.mu.Lock()
	require.Equal(t, 1, persistence.checkpoints, "checkpoint = %#v count", persistence.checkpoint)
	require.Equal(t, "rendering", persistence.checkpoint.Progress.Stage, "checkpoint = %#v count", persistence.checkpoint)
	persistence.mu.Unlock()

	undeclared := testDescriptor(handlerFunc(func(ctx context.Context, execution Execution) Outcome {
		checkpointErr = execution.Checkpoint(ctx, CheckpointValue{Progress: &model.JobProgress{Current: 1, Total: 1, Stage: "secret"}, Document: json.RawMessage(`{}`)})
		return succeededOutcome()
	}))
	runner.execute(context.Background(), undeclared, jobRunnerClaim(t))
	require.Error(t, checkpointErr, "Checkpoint() accepted undeclared version and stage")
	persistence.mu.Lock()
	defer persistence.mu.Unlock()
	require.Equal(t, 1, persistence.checkpoints, "undeclared checkpoint reached store: count")
}

func testDescriptor(handler Handler) Descriptor {
	return Descriptor{Type: model.JobTypeProfilePictureGenerateDefault, PublicErrorCodes: []string{"dependency.unavailable", "job.command.invalid", "job.invariant_failed", "user.not_found"}, Timeout: 100 * time.Millisecond, Concurrency: 1, MaximumAttempts: 8, LeaseDuration: 50 * time.Millisecond, HeartbeatInterval: 5 * time.Millisecond, BaseRetryDelay: time.Millisecond, MaximumRetryDelay: time.Second, Visibility: VisibilityOperator, SuccessRetention: 24 * time.Hour, FailureRetention: 48 * time.Hour, Handler: handler}
}

func jobRunnerClaim(t *testing.T) *store.JobClaim {
	t.Helper()
	at := time.Now()
	job, err := model.NewJob(model.NewJobID(), model.JobTypeProfilePictureGenerateDefault, []byte(`{}`), "test", at, at, 8)
	require.NoError(t, err)
	running, err := job.Start(at)
	require.NoError(t, err)
	token, err := model.NewJobClaimToken()
	require.NoError(t, err)
	attempt, err := model.NewJobAttempt(model.NewJobAttemptID(), job.ID, 1, "node-a", token, at, at.Add(time.Minute))
	require.NoError(t, err)
	return &store.JobClaim{Job: running, Attempt: attempt}
}

func succeededOutcome() Outcome {
	return Outcome{Kind: OutcomeSucceeded, Result: json.RawMessage(`{}`)}
}

func newTestEngine(persistence store.JobStore, registry *Registry, nodeID string, diagnostics Diagnostics, poll time.Duration) (*Engine, error) {
	descriptors := make([]Descriptor, 0, len(registry.types))
	for _, jobType := range registry.types {
		descriptors = append(descriptors, registry.descriptors[jobType])
	}
	return New(Config{Store: persistence, Descriptors: descriptors, NodeID: nodeID, Diagnostics: diagnostics, Policy: Policy{PollInterval: poll}})
}
