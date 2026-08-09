// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type jobRunnerStoreFake struct {
	mu          sync.Mutex
	heartbeats  int
	checkpoints int
	checkpoint  *store.JobCheckpoint
	completion  *store.JobCompletion
}

func (*jobRunnerStoreFake) Enqueue(context.Context, *store.JobEnqueue) (*model.Job, bool, error) {
	return nil, false, nil
}
func (*jobRunnerStoreFake) ClaimNext(context.Context, *store.JobClaimRequest) (*store.JobClaim, error) {
	return nil, store.NewErrNotFound("job", "claimable")
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

type jobDiagnosticsFake struct{ errors []error }

func (d *jobDiagnosticsFake) ErrorContext(_ context.Context, _ string, err error) {
	d.errors = append(d.errors, err)
}

func TestJobRunnerContainsPanicsAsRetryableOutcomes(t *testing.T) {
	t.Parallel()
	claim := jobRunnerClaim(t)
	descriptor := testJobDescriptor(jobHandlerFunc(func(context.Context, JobExecution) JobOutcome { panic("secret payload") }))
	registry, err := NewJobRegistry([]JobDescriptor{descriptor})
	if err != nil {
		t.Fatal(err)
	}
	persistence := &jobRunnerStoreFake{}
	runner, err := newJobRunner(persistence, registry, "node-a", &jobDiagnosticsFake{}, time.Second)
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

func TestJobRunnerHeartbeatsLongWorkAndCompletesWithItsFence(t *testing.T) {
	t.Parallel()
	claim := jobRunnerClaim(t)
	descriptor := testJobDescriptor(jobHandlerFunc(func(ctx context.Context, _ JobExecution) JobOutcome {
		select {
		case <-time.After(20 * time.Millisecond):
			return DefaultProfilePictureJobSucceeded(model.NewFileEntryID())
		case <-ctx.Done():
			return JobRetryableFailure("job.timeout", ctx.Err())
		}
	}))
	registry, err := NewJobRegistry([]JobDescriptor{descriptor})
	if err != nil {
		t.Fatal(err)
	}
	persistence := &jobRunnerStoreFake{}
	runner, err := newJobRunner(persistence, registry, "node-a", &jobDiagnosticsFake{}, time.Second)
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

func TestJobRunnerRejectsAnUnregisteredResultVersion(t *testing.T) {
	t.Parallel()
	claim := jobRunnerClaim(t)
	descriptor := testJobDescriptor(jobHandlerFunc(func(context.Context, JobExecution) JobOutcome {
		return JobOutcome{Kind: JobOutcomeSucceeded, ResultVersion: 2, Result: json.RawMessage(`{}`)}
	}))
	registry, err := NewJobRegistry([]JobDescriptor{descriptor})
	if err != nil {
		t.Fatal(err)
	}
	persistence := &jobRunnerStoreFake{}
	runner, err := newJobRunner(persistence, registry, "node-a", &jobDiagnosticsFake{}, time.Second)
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

func TestJobRunnerShutdownIsBounded(t *testing.T) {
	t.Parallel()
	descriptor := testJobDescriptor(jobHandlerFunc(func(context.Context, JobExecution) JobOutcome {
		return DefaultProfilePictureJobSucceeded(model.NewFileEntryID())
	}))
	descriptor.Timeout = 20 * time.Millisecond
	registry, err := NewJobRegistry([]JobDescriptor{descriptor})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := newJobRunner(&jobRunnerStoreFake{}, registry, "node-a", &jobDiagnosticsFake{}, time.Second)
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

func TestJobRunnerEnforcesTypedCheckpointContract(t *testing.T) {
	t.Parallel()

	claim := jobRunnerClaim(t)
	var checkpointErr error
	descriptor := testJobDescriptor(jobHandlerFunc(func(ctx context.Context, execution JobExecution) JobOutcome {
		checkpointErr = execution.Checkpoint(ctx, JobCheckpointValue{Version: 1, Progress: &model.JobProgress{Current: 1, Total: 3, Stage: "rendering"}, Document: json.RawMessage(`{"cursor":1}`)})
		return DefaultProfilePictureJobSucceeded(model.NewFileEntryID())
	}))
	descriptor.CheckpointVersions = []int{1}
	descriptor.ProgressStages = []string{"rendering"}
	registry, err := NewJobRegistry([]JobDescriptor{descriptor})
	if err != nil {
		t.Fatal(err)
	}
	persistence := &jobRunnerStoreFake{}
	runner, err := newJobRunner(persistence, registry, "node-a", &jobDiagnosticsFake{}, time.Second)
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

	undeclared := testJobDescriptor(jobHandlerFunc(func(ctx context.Context, execution JobExecution) JobOutcome {
		checkpointErr = execution.Checkpoint(ctx, JobCheckpointValue{Version: 2, Progress: &model.JobProgress{Current: 1, Total: 1, Stage: "secret"}, Document: json.RawMessage(`{}`)})
		return DefaultProfilePictureJobSucceeded(model.NewFileEntryID())
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

func testJobDescriptor(handler JobHandler) JobDescriptor {
	return JobDescriptor{Type: model.JobTypeProfilePictureGenerateDefault, CommandVersions: []int{1}, ResultVersions: []int{1}, PublicErrorCodes: []string{"dependency.unavailable", "job.command.invalid", "job.invariant_failed", "user.not_found"}, Timeout: 100 * time.Millisecond, Concurrency: 1, MaximumAttempts: 8, LeaseDuration: 50 * time.Millisecond, HeartbeatInterval: 5 * time.Millisecond, BaseRetryDelay: time.Millisecond, MaximumRetryDelay: time.Second, Visibility: JobVisibilityOperator, SuccessRetention: 24 * time.Hour, FailureRetention: 48 * time.Hour, Handler: handler}
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
