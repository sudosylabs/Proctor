// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"sync"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type JobRunner struct {
	jobs            store.JobStore
	registry        *JobRegistry
	nodeID          string
	diagnostics     recoveryDiagnostics
	poll            time.Duration
	shutdownTimeout time.Duration
	wake            chan struct{}

	mu      sync.Mutex
	started bool
	closed  bool
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

func newJobRunner(jobs store.JobStore, registry *JobRegistry, nodeID string, diagnostics recoveryDiagnostics, poll time.Duration) (*JobRunner, error) {
	if jobs == nil || registry == nil || nodeID == "" || diagnostics == nil || poll <= 0 {
		return nil, errors.New("invalid job runner dependencies")
	}
	return &JobRunner{jobs: jobs, registry: registry, nodeID: nodeID, diagnostics: diagnostics, poll: poll, shutdownTimeout: registry.MaximumTimeout(), wake: make(chan struct{}, 1)}, nil
}

func (r *JobRunner) Start(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return errors.New("job runner is closed")
	}
	if r.started {
		return errors.New("job runner is already started")
	}
	runCtx, cancel := context.WithCancel(ctx)
	r.cancel = cancel
	r.started = true
	for _, jobType := range r.registry.Types() {
		descriptor, err := r.registry.Descriptor(jobType)
		if err != nil {
			cancel()
			return err
		}
		for range descriptor.Concurrency {
			r.wg.Add(1)
			go r.worker(runCtx, descriptor)
		}
	}
	return nil
}

func (r *JobRunner) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	cancel := r.cancel
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	drained := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(drained)
	}()
	select {
	case <-drained:
		return nil
	case <-time.After(r.shutdownTimeout):
		return errors.New("durable job runner shutdown deadline exceeded")
	}
}

func (r *JobRunner) Wake() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *JobRunner) worker(ctx context.Context, descriptor JobDescriptor) {
	defer r.wg.Done()
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.wake:
		case <-timer.C:
		}
		token, err := model.NewJobClaimToken()
		if err != nil {
			r.diagnostics.ErrorContext(ctx, "generate job claim token", err)
			resetTimer(timer, r.poll)
			continue
		}
		claim, err := r.jobs.ClaimNext(ctx, &store.JobClaimRequest{Types: []model.JobType{descriptor.Type}, NodeID: r.nodeID, ClaimToken: token, LeaseDuration: descriptor.LeaseDuration})
		if err != nil {
			if !store.IsNotFound(err) && !errors.Is(err, context.Canceled) {
				r.diagnostics.ErrorContext(ctx, "claim durable job", err)
			}
			resetTimer(timer, r.poll)
			continue
		}
		resolved, err := r.registry.Resolve(claim.Job.Type, claim.Job.CommandVersion)
		if err != nil {
			r.complete(ctx, descriptor, claim, JobPermanentFailure("job.command.unsupported", err))
		} else {
			r.execute(ctx, resolved, claim)
		}
		resetTimer(timer, 0)
	}
}

func (r *JobRunner) execute(parent context.Context, descriptor JobDescriptor, claim *store.JobClaim) {
	handlerCtx, cancelHandler := context.WithTimeout(parent, descriptor.Timeout)
	defer cancelHandler()
	heartbeatDone := make(chan struct{})
	heartbeatStopped := make(chan struct{})
	claimLost := make(chan struct{}, 1)
	go func() {
		defer close(heartbeatStopped)
		ticker := time.NewTicker(descriptor.HeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatDone:
				return
			case <-handlerCtx.Done():
				return
			case <-ticker.C:
				if _, err := r.jobs.Heartbeat(handlerCtx, &store.JobHeartbeat{AttemptID: claim.Attempt.ID, ClaimToken: claim.Attempt.ClaimToken, LeaseDuration: descriptor.LeaseDuration}); err != nil {
					r.diagnostics.ErrorContext(parent, "heartbeat durable job", err)
					select {
					case claimLost <- struct{}{}:
					default:
					}
					cancelHandler()
					return
				}
			}
		}
	}()
	execution := JobExecution{Job: claim.Job, Attempt: claim.Attempt}
	execution.checkpoint = func(ctx context.Context, value JobCheckpointValue) error {
		if !descriptor.SupportsCheckpointVersion(value.Version) || len(value.Document) == 0 || len(value.Document) > model.JobMaximumDocumentBytes || !json.Valid(value.Document) {
			return errors.New("job checkpoint version is not declared")
		}
		if value.Progress != nil && (value.Progress.Current < 0 || value.Progress.Total <= 0 || value.Progress.Current > value.Progress.Total || !descriptor.SupportsProgressStage(value.Progress.Stage)) {
			return errors.New("job progress stage is not declared")
		}
		_, err := r.jobs.Checkpoint(ctx, &store.JobCheckpoint{AttemptID: claim.Attempt.ID, ClaimToken: claim.Attempt.ClaimToken, Progress: value.Progress, CheckpointVersion: value.Version, Checkpoint: value.Document})
		return err
	}
	outcome := runJobHandler(handlerCtx, descriptor.Handler, execution)
	close(heartbeatDone)
	<-heartbeatStopped
	select {
	case <-claimLost:
		return
	default:
	}
	if errors.Is(parent.Err(), context.Canceled) {
		return
	}
	if errors.Is(handlerCtx.Err(), context.DeadlineExceeded) {
		outcome = JobRetryableFailure("job.timeout", handlerCtx.Err())
	}
	r.complete(parent, descriptor, claim, outcome)
}

func runJobHandler(ctx context.Context, handler JobHandler, execution JobExecution) (outcome JobOutcome) {
	defer func() {
		if recovered := recover(); recovered != nil {
			outcome = JobRetryableFailure("job.handler_panic", fmt.Errorf("job handler panic: %T", recovered))
		}
	}()
	return handler.Run(ctx, execution)
}

func (r *JobRunner) complete(ctx context.Context, descriptor JobDescriptor, claim *store.JobClaim, outcome JobOutcome) {
	failureWithoutCode := (outcome.Kind == JobOutcomeRetryableFailure || outcome.Kind == JobOutcomePermanentFailure) && outcome.PublicErrorCode == ""
	if failureWithoutCode || !descriptor.SupportsPublicErrorCode(outcome.PublicErrorCode) {
		outcome = JobPermanentFailure("job.outcome.invalid", errors.New("handler returned an undeclared public error code"))
	}
	completion := &store.JobCompletion{AttemptID: claim.Attempt.ID, ClaimToken: claim.Attempt.ClaimToken, ResultVersion: outcome.ResultVersion, Result: outcome.Result, PublicErrorCode: outcome.PublicErrorCode}
	switch outcome.Kind {
	case JobOutcomeSucceeded:
		if outcome.Err != nil || !descriptor.SupportsResultVersion(outcome.ResultVersion) || len(outcome.Result) == 0 {
			completion.Kind = store.JobCompletionPermanentFailure
			completion.PublicErrorCode = "job.result.invalid"
		} else {
			completion.Kind = store.JobCompletionSucceeded
		}
	case JobOutcomeRetryableFailure:
		completion.Kind = store.JobCompletionRetryableFailure
		completion.RetryDelay = jobRetryDelay(descriptor, claim.Job.ID, claim.Attempt.Number)
	case JobOutcomePermanentFailure:
		completion.Kind = store.JobCompletionPermanentFailure
	case JobOutcomeCanceled:
		completion.Kind = store.JobCompletionCanceled
	default:
		completion.Kind = store.JobCompletionPermanentFailure
		completion.PublicErrorCode = "job.outcome.invalid"
	}
	if _, err := r.jobs.Complete(ctx, completion); err != nil && !errors.Is(err, context.Canceled) {
		r.diagnostics.ErrorContext(ctx, "complete durable job", err)
	}
}

func jobRetryDelay(descriptor JobDescriptor, jobID model.JobID, attempt int) time.Duration {
	delay := descriptor.BaseRetryDelay
	for index := 1; index < attempt && delay < descriptor.MaximumRetryDelay/2; index++ {
		delay *= 2
	}
	if delay > descriptor.MaximumRetryDelay {
		delay = descriptor.MaximumRetryDelay
	}
	hasher := fnv.New32a()
	_, _ = hasher.Write([]byte(jobID.String()))
	jitter := time.Duration(hasher.Sum32()%251) * delay / 1000
	return min(descriptor.MaximumRetryDelay, delay+jitter)
}

func resetTimer(timer *time.Timer, delay time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(delay)
}
