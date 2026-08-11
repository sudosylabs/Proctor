// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package job

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type jobRunnerStoreFake struct {
	mu              sync.Mutex
	heartbeats      int
	checkpoints     int
	checkpoint      *store.JobCheckpoint
	completion      *store.JobCompletion
	cancelRequested bool
	reservation     *store.JobWorkReservation
	claim           *store.JobClaim
	claimed         bool
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
func (*jobRunnerStoreFake) CancelWithAudit(context.Context, *store.JobMutation) (*model.Job, error) {
	return nil, nil
}
func (*jobRunnerStoreFake) RetryWithAudit(context.Context, *store.JobMutation) (*model.Job, error) {
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

func TestEngineClosesRegistrationBeforeStart(t *testing.T) {
	t.Parallel()
	descriptor := testDescriptor(handlerFunc(func(context.Context, Execution) Outcome { return succeededOutcome() }))
	engine, err := New(Config{
		Store: &jobRunnerStoreFake{}, Descriptors: []Descriptor{descriptor}, NodeID: "node-a",
		Diagnostics: &jobDiagnosticsFake{}, Policy: Policy{PollInterval: time.Second},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = engine.AddDailyProposal("before-start", noOpProposer{}); err != nil {
		t.Fatalf("AddDailyProposal() before Start = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err = engine.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err = engine.AddDailyProposal("late", noOpProposer{}); err == nil {
		t.Fatal("AddDailyProposal() accepted runtime registration")
	}
	if err = engine.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestEngineClaimsAndCompletesThroughItsPublicLifecycle(t *testing.T) {
	t.Parallel()
	claim := jobRunnerClaim(t)
	descriptor := testDescriptor(handlerFunc(func(context.Context, Execution) Outcome { return succeededOutcome() }))
	persistence := &jobRunnerStoreFake{claim: claim}
	engine, err := New(Config{
		Store: persistence, Descriptors: []Descriptor{descriptor}, NodeID: "node-a",
		Diagnostics: &jobDiagnosticsFake{}, Policy: Policy{PollInterval: time.Millisecond},
	})
	if err != nil {
		t.Fatal(err)
	}
	if described, describeErr := engine.Descriptor(descriptor.Type); describeErr != nil || described.Handler != nil {
		t.Fatalf("Descriptor() exposed executable handler: %#v, %v", described.Handler, describeErr)
	}
	if err = engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	engine.Wake()
	deadline := time.Now().Add(time.Second)
	for {
		persistence.mu.Lock()
		completion := persistence.completion
		persistence.mu.Unlock()
		if completion != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Engine did not claim and complete queued work")
		}
		time.Sleep(time.Millisecond)
	}
	if err = engine.Close(); err != nil {
		t.Fatal(err)
	}
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
		claim := jobRunnerClaim(t)
		claim.Job.ID = model.JobID("job-fixed")
		claim.Attempt.Number = test.attempt
		persistence := &jobRunnerStoreFake{}
		registry, err := NewRegistry([]Descriptor{descriptor})
		if err != nil {
			t.Fatal(err)
		}
		engine, err := newTestEngine(persistence, registry, "node-a", &jobDiagnosticsFake{}, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		engine.execute(context.Background(), descriptor, claim)
		persistence.mu.Lock()
		completion := persistence.completion
		persistence.mu.Unlock()
		if completion == nil || completion.Kind != store.JobCompletionRetryableFailure || completion.RetryDelay != test.want {
			t.Fatalf("attempt %d retry completion = %#v, want delay %v", test.attempt, completion, test.want)
		}
	}
}

func TestEngineContainsPanicsAsRetryableOutcomes(t *testing.T) {
	t.Parallel()
	claim := jobRunnerClaim(t)
	descriptor := testDescriptor(handlerFunc(func(context.Context, Execution) Outcome { panic("secret payload") }))
	registry, err := NewRegistry([]Descriptor{descriptor})
	if err != nil {
		t.Fatal(err)
	}
	persistence := &jobRunnerStoreFake{}
	runner, err := newTestEngine(persistence, registry, "node-a", &jobDiagnosticsFake{}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	runner.execute(context.Background(), descriptor, claim)
	persistence.mu.Lock()
	defer persistence.mu.Unlock()
	if persistence.completion == nil || persistence.completion.Kind != store.JobCompletionRetryableFailure || persistence.completion.PublicErrorCode != "job.handler_panic" {
		t.Fatalf("panic completion = %#v", persistence.completion)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	persistence := &jobRunnerStoreFake{}
	runner, err := newTestEngine(persistence, registry, "node-a", &jobDiagnosticsFake{}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	runner.execute(context.Background(), descriptor, claim)
	if !reserved || persistence.reservation == nil || persistence.reservation.AttemptID != claim.Attempt.ID || persistence.reservation.ClaimToken != claim.Attempt.ClaimToken || persistence.reservation.Units != 2 || persistence.reservation.Limit != 5 {
		t.Fatalf("reservation = %#v, reserved = %v", persistence.reservation, reserved)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	persistence := &jobRunnerStoreFake{}
	runner, err := newTestEngine(persistence, registry, "node-a", &jobDiagnosticsFake{}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	runner.execute(context.Background(), descriptor, claim)
	persistence.mu.Lock()
	defer persistence.mu.Unlock()
	if persistence.heartbeats == 0 || persistence.completion == nil || persistence.completion.Kind != store.JobCompletionSucceeded || persistence.completion.ClaimToken != claim.Attempt.ClaimToken {
		t.Fatalf("execution: heartbeats=%d completion=%#v", persistence.heartbeats, persistence.completion)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	runner, err := newTestEngine(persistence, registry, "node-a", &jobDiagnosticsFake{}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	runner.execute(context.Background(), descriptor, claim)
	persistence.mu.Lock()
	defer persistence.mu.Unlock()
	if persistence.completion == nil || persistence.completion.Kind != store.JobCompletionCanceled {
		t.Fatalf("cancellation completion = %#v", persistence.completion)
	}
}

func TestEngineRejectsAnUnregisteredResultVersion(t *testing.T) {
	t.Parallel()
	claim := jobRunnerClaim(t)
	descriptor := testDescriptor(handlerFunc(func(context.Context, Execution) Outcome {
		return Outcome{Kind: OutcomeSucceeded, ResultVersion: 2, Result: json.RawMessage(`{}`)}
	}))
	registry, err := NewRegistry([]Descriptor{descriptor})
	if err != nil {
		t.Fatal(err)
	}
	persistence := &jobRunnerStoreFake{}
	runner, err := newTestEngine(persistence, registry, "node-a", &jobDiagnosticsFake{}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	runner.execute(context.Background(), descriptor, claim)
	persistence.mu.Lock()
	defer persistence.mu.Unlock()
	if persistence.completion == nil || persistence.completion.Kind != store.JobCompletionPermanentFailure || persistence.completion.PublicErrorCode != "job.result.invalid" {
		t.Fatalf("completion = %#v", persistence.completion)
	}
}

func TestEngineShutdownIsBounded(t *testing.T) {
	t.Parallel()
	descriptor := testDescriptor(handlerFunc(func(context.Context, Execution) Outcome {
		return succeededOutcome()
	}))
	descriptor.Timeout = 20 * time.Millisecond
	registry, err := NewRegistry([]Descriptor{descriptor})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := newTestEngine(&jobRunnerStoreFake{}, registry, "node-a", &jobDiagnosticsFake{}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	blocked := make(chan struct{})
	runner.wg.Add(1)
	go func() {
		defer runner.wg.Done()
		<-blocked
	}()
	started := time.Now()
	if err = runner.Close(); err == nil {
		t.Fatal("Close() waited without a shutdown deadline")
	}
	close(blocked)
	if elapsed := time.Since(started); elapsed < descriptor.Timeout || elapsed > time.Second {
		t.Fatalf("Close() duration = %v", elapsed)
	}
}

func TestEngineEnforcesTypedCheckpointContract(t *testing.T) {
	t.Parallel()

	claim := jobRunnerClaim(t)
	var checkpointErr error
	descriptor := testDescriptor(handlerFunc(func(ctx context.Context, execution Execution) Outcome {
		checkpointErr = execution.Checkpoint(ctx, CheckpointValue{Version: 1, Progress: &model.JobProgress{Current: 1, Total: 3, Stage: "rendering"}, Document: json.RawMessage(`{"cursor":1}`)})
		return succeededOutcome()
	}))
	descriptor.CheckpointVersions = []int{1}
	descriptor.ProgressStages = []string{"rendering"}
	registry, err := NewRegistry([]Descriptor{descriptor})
	if err != nil {
		t.Fatal(err)
	}
	persistence := &jobRunnerStoreFake{}
	runner, err := newTestEngine(persistence, registry, "node-a", &jobDiagnosticsFake{}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	runner.execute(context.Background(), descriptor, claim)
	if checkpointErr != nil {
		t.Fatalf("Checkpoint() error = %v", checkpointErr)
	}
	persistence.mu.Lock()
	if persistence.checkpoints != 1 || persistence.checkpoint.CheckpointVersion != 1 || persistence.checkpoint.Progress.Stage != "rendering" {
		t.Fatalf("checkpoint = %#v count=%d", persistence.checkpoint, persistence.checkpoints)
	}
	persistence.mu.Unlock()

	undeclared := testDescriptor(handlerFunc(func(ctx context.Context, execution Execution) Outcome {
		checkpointErr = execution.Checkpoint(ctx, CheckpointValue{Version: 2, Progress: &model.JobProgress{Current: 1, Total: 1, Stage: "secret"}, Document: json.RawMessage(`{}`)})
		return succeededOutcome()
	}))
	runner.execute(context.Background(), undeclared, jobRunnerClaim(t))
	if checkpointErr == nil {
		t.Fatal("Checkpoint() accepted undeclared version and stage")
	}
	persistence.mu.Lock()
	defer persistence.mu.Unlock()
	if persistence.checkpoints != 1 {
		t.Fatalf("undeclared checkpoint reached store: count=%d", persistence.checkpoints)
	}
}

func testDescriptor(handler Handler) Descriptor {
	return Descriptor{Type: model.JobTypeProfilePictureGenerateDefault, CommandVersions: []int{1}, ResultVersions: []int{1}, PublicErrorCodes: []string{"dependency.unavailable", "job.command.invalid", "job.invariant_failed", "user.not_found"}, Timeout: 100 * time.Millisecond, Concurrency: 1, MaximumAttempts: 8, LeaseDuration: 50 * time.Millisecond, HeartbeatInterval: 5 * time.Millisecond, BaseRetryDelay: time.Millisecond, MaximumRetryDelay: time.Second, Visibility: VisibilityOperator, SuccessRetention: 24 * time.Hour, FailureRetention: 48 * time.Hour, Handler: handler}
}

func jobRunnerClaim(t *testing.T) *store.JobClaim {
	t.Helper()
	at := time.Now()
	job, err := model.NewJob(model.NewJobID(), model.JobTypeProfilePictureGenerateDefault, 1, []byte(`{}`), "test", at, at, 8)
	if err != nil {
		t.Fatal(err)
	}
	running, err := job.Start(at)
	if err != nil {
		t.Fatal(err)
	}
	token, err := model.NewJobClaimToken()
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := model.NewJobAttempt(model.NewJobAttemptID(), job.ID, 1, "node-a", token, at, at.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	return &store.JobClaim{Job: running, Attempt: attempt}
}

func succeededOutcome() Outcome {
	return Outcome{Kind: OutcomeSucceeded, ResultVersion: 1, Result: json.RawMessage(`{}`)}
}

func newTestEngine(persistence store.JobStore, registry *Registry, nodeID string, diagnostics Diagnostics, poll time.Duration) (*Engine, error) {
	descriptors := make([]Descriptor, 0, len(registry.types))
	for _, jobType := range registry.types {
		descriptors = append(descriptors, registry.descriptors[jobType])
	}
	return New(Config{Store: persistence, Descriptors: descriptors, NodeID: nodeID, Diagnostics: diagnostics, Policy: Policy{PollInterval: poll}})
}
