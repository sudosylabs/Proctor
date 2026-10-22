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
	"hash/fnv"
	"sync"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type Engine struct {
	deps     Dependencies
	registry *Registry
	wake     chan struct{}

	mu      sync.Mutex
	started bool
	closed  bool
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

type Diagnostics interface {
	ErrorContext(context.Context, string, error)
}

// Recorder observes bounded Job runtime facts and never receives commands,
// results, errors, claims, identifiers, or actor data.
type Recorder interface {
	Started(model.JobType)
	Finished(model.JobType, ExecutionOutcome, time.Duration)
	Record(Activity)
}

// ExecutionOutcome is the closed telemetry vocabulary for one claimed Job
// execution. It includes runtime terminal states that are not handler outcomes.
type ExecutionOutcome string

const (
	ExecutionSucceeded        ExecutionOutcome = "succeeded"
	ExecutionRetryableFailure ExecutionOutcome = "retryable_failure"
	ExecutionPermanentFailure ExecutionOutcome = "permanent_failure"
	ExecutionCanceled         ExecutionOutcome = "canceled"
	ExecutionRelinquished     ExecutionOutcome = "relinquished"
	ExecutionAbandoned        ExecutionOutcome = "abandoned"
	ExecutionClaimLost        ExecutionOutcome = "claim_lost"
	ExecutionShutdown         ExecutionOutcome = "shutdown"
	ExecutionInvalid          ExecutionOutcome = "invalid"
)

func executionOutcomeFromHandler(outcome OutcomeKind) ExecutionOutcome {
	switch outcome {
	case OutcomeSucceeded:
		return ExecutionSucceeded
	case OutcomeRetryableFailure:
		return ExecutionRetryableFailure
	case OutcomePermanentFailure:
		return ExecutionPermanentFailure
	case OutcomeCanceled:
		return ExecutionCanceled
	case OutcomeRelinquished:
		return ExecutionRelinquished
	default:
		return ExecutionInvalid
	}
}

// Activity is a bounded operational fact emitted by the Job runtime. Name is
// either a registered Job type, recurrence, or periodic-task name; Operation
// and Outcome are selected only by this package.
type Activity struct {
	Kind         string
	Name         string
	Operation    string
	Outcome      string
	Duration     time.Duration
	QueueLatency time.Duration
}

type Policy struct {
	PollInterval       time.Duration
	ShutdownTimeout    time.Duration
	ProposalRetryDelay time.Duration
}

// Dependencies are the collaborators and policy of the Job engine; New
// validates them once, fills policy defaults, and the Engine keeps them in
// deps.
type Dependencies struct {
	Store         store.JobStore
	Descriptors   []Descriptor
	NodeID        string
	Diagnostics   Diagnostics
	Recorder      Recorder
	Policy        Policy
	Clock         Clock
	Recurrences   []Recurrence
	PeriodicTasks []PeriodicTask
}

// New validates deps once and constructs an idle Job engine; Start begins
// claiming work.
func New(deps Dependencies) (*Engine, error) {
	registry, err := NewRegistry(deps.Descriptors)
	if err != nil {
		return nil, err
	}
	if deps.Store == nil || deps.NodeID == "" || deps.Diagnostics == nil || deps.Policy.PollInterval <= 0 {
		return nil, errors.New("invalid job engine dependencies")
	}
	recurrences, err := cloneRecurrences(deps.Recurrences)
	if err != nil {
		return nil, err
	}
	periodicTasks, err := clonePeriodicTasks(deps.PeriodicTasks)
	if err != nil {
		return nil, err
	}
	if deps.Policy.ShutdownTimeout <= 0 {
		deps.Policy.ShutdownTimeout = registry.MaximumTimeout()
	}
	if deps.Policy.ProposalRetryDelay <= 0 {
		deps.Policy.ProposalRetryDelay = time.Minute
	}
	if deps.Clock == nil {
		deps.Clock = systemClock{}
	}
	deps.Recurrences, deps.PeriodicTasks = recurrences, periodicTasks
	return &Engine{deps: deps, registry: registry, wake: make(chan struct{}, 1)}, nil
}

// Descriptor returns the immutable execution contract for a registered type.
func (r *Engine) Descriptor(jobType model.JobType) (Descriptor, error) {
	if r == nil {
		return Descriptor{}, errors.New("job engine is nil")
	}
	descriptor, err := r.registry.Descriptor(jobType)
	descriptor.Handler = nil
	return descriptor, err
}

// Types returns the registered Job types in stable order.
func (r *Engine) Types() []model.JobType {
	if r == nil {
		return nil
	}
	return r.registry.Types()
}

func (r *Engine) Start(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return errors.New("job engine is closed")
	}
	if r.started {
		return errors.New("job engine is already started")
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
	for _, recurrence := range r.deps.Recurrences {
		r.wg.Add(1)
		go func(value Recurrence) {
			defer r.wg.Done()
			runRecurrenceProposal(runCtx, value, r.deps.Diagnostics, r.deps.Clock, r.deps.Policy.ProposalRetryDelay, r.Wake, r.deps.Recorder)
		}(recurrence)
	}
	for _, task := range r.deps.PeriodicTasks {
		r.wg.Add(1)
		go func(value PeriodicTask) {
			defer r.wg.Done()
			runPeriodicTask(runCtx, value, r.deps.Diagnostics, r.deps.Clock, r.deps.Recorder)
		}(task)
	}
	return nil
}

func (r *Engine) Close() error {
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
	case <-time.After(r.deps.Policy.ShutdownTimeout):
		return errors.New("durable job engine shutdown deadline exceeded")
	}
}

func (r *Engine) Wake() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *Engine) worker(ctx context.Context, descriptor Descriptor) {
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
			r.deps.Diagnostics.ErrorContext(ctx, "generate job claim token", err)
			resetTimer(timer, r.deps.Policy.PollInterval)
			continue
		}
		startedClaim := time.Now()
		claim, err := r.deps.Store.ClaimNext(ctx, &store.JobClaimRequest{Types: []model.JobType{descriptor.Type}, NodeID: r.deps.NodeID, ClaimToken: token, LeaseDuration: descriptor.LeaseDuration})
		claimDuration := time.Since(startedClaim)
		if err != nil {
			result := "error"
			if store.IsNotFound(err) {
				result = "empty"
			} else if errors.Is(err, context.Canceled) {
				result = "canceled"
			}
			r.record(Activity{Kind: "job", Name: string(descriptor.Type), Operation: "claim", Outcome: result, Duration: claimDuration})
			if !store.IsNotFound(err) && !errors.Is(err, context.Canceled) {
				r.deps.Diagnostics.ErrorContext(ctx, "claim durable job", err)
			}
			resetTimer(timer, r.deps.Policy.PollInterval)
			continue
		}
		queueLatency := time.Since(claim.Job.AvailableAt)
		if queueLatency < 0 {
			queueLatency = 0
		}
		r.record(Activity{Kind: "job", Name: string(descriptor.Type), Operation: "claim", Outcome: "success", Duration: claimDuration, QueueLatency: queueLatency})
		resolved, err := r.registry.Descriptor(claim.Job.Type)
		if err != nil {
			r.completeUnsupported(ctx, descriptor, claim, err)
		} else {
			r.execute(ctx, resolved, claim)
		}
		resetTimer(timer, 0)
	}
}

func (r *Engine) completeUnsupported(ctx context.Context, descriptor Descriptor, claim *store.JobClaim, resolutionErr error) {
	startedAt := time.Now()
	if r.deps.Recorder != nil {
		r.deps.Recorder.Started(descriptor.Type)
		defer func() {
			r.deps.Recorder.Finished(descriptor.Type, ExecutionPermanentFailure, time.Since(startedAt))
		}()
	}
	r.complete(ctx, descriptor, claim, PermanentFailure("job.command.unsupported", resolutionErr))
}

func (r *Engine) execute(parent context.Context, descriptor Descriptor, claim *store.JobClaim) {
	startedAt := time.Now()
	metricOutcome := ExecutionAbandoned
	if r.deps.Recorder != nil {
		r.deps.Recorder.Started(descriptor.Type)
		defer func() {
			r.deps.Recorder.Finished(descriptor.Type, metricOutcome, time.Since(startedAt))
		}()
	}
	handlerCtx, cancelHandler := context.WithTimeout(parent, descriptor.Timeout)
	defer cancelHandler()
	heartbeatDone := make(chan struct{})
	heartbeatStopped := make(chan struct{})
	claimLost := make(chan struct{}, 1)
	go func() {
		defer close(heartbeatStopped)
		ticker := time.NewTicker(descriptor.HeartbeatInterval)
		defer ticker.Stop()
		// The lease runs from the last renewal the store accepted. A failed
		// heartbeat abandons the attempt only when the store reports the claim
		// lost or when the next heartbeat could no longer land inside the lease.
		renewedAt := startedAt
		for {
			select {
			case <-heartbeatDone:
				return
			case <-handlerCtx.Done():
				return
			case <-ticker.C:
				heartbeatStarted := time.Now()
				if _, err := r.deps.Store.Heartbeat(handlerCtx, &store.JobHeartbeat{AttemptID: claim.Attempt.ID, ClaimToken: claim.Attempt.ClaimToken, LeaseDuration: descriptor.LeaseDuration}); err != nil {
					r.record(Activity{Kind: "job", Name: string(descriptor.Type), Operation: "heartbeat", Outcome: "error", Duration: time.Since(heartbeatStarted)})
					r.deps.Diagnostics.ErrorContext(parent, "heartbeat durable job", err)
					if !heartbeatClaimLost(err) && time.Since(renewedAt)+descriptor.HeartbeatInterval < descriptor.LeaseDuration {
						continue
					}
					select {
					case claimLost <- struct{}{}:
					default:
					}
					cancelHandler()
					return
				}
				renewedAt = heartbeatStarted
				r.record(Activity{Kind: "job", Name: string(descriptor.Type), Operation: "heartbeat", Outcome: "success", Duration: time.Since(heartbeatStarted)})
				if descriptor.Cancelable {
					cancellationStarted := time.Now()
					requested, err := r.deps.Store.CancellationRequested(handlerCtx, claim.Attempt.ID, claim.Attempt.ClaimToken)
					if err != nil {
						r.record(Activity{Kind: "job", Name: string(descriptor.Type), Operation: "cancellation_check", Outcome: "error", Duration: time.Since(cancellationStarted)})
						r.deps.Diagnostics.ErrorContext(parent, "observe durable job cancellation", err)
						if !heartbeatClaimLost(err) && time.Since(renewedAt)+descriptor.HeartbeatInterval < descriptor.LeaseDuration {
							continue
						}
						select {
						case claimLost <- struct{}{}:
						default:
						}
						cancelHandler()
						return
					}
					if requested {
						r.record(Activity{Kind: "job", Name: string(descriptor.Type), Operation: "cancellation_check", Outcome: "requested", Duration: time.Since(cancellationStarted)})
						cancelHandler()
						return
					}
					r.record(Activity{Kind: "job", Name: string(descriptor.Type), Operation: "cancellation_check", Outcome: "clear", Duration: time.Since(cancellationStarted)})
				}
			}
		}
	}()
	execution := NewExecution(claim.Job, claim.Attempt, nil, nil)
	execution.reserveWork = func(ctx context.Context, units, limit int) (bool, error) {
		started := time.Now()
		result, err := r.deps.Store.ReserveWork(ctx, &store.JobWorkReservation{AttemptID: claim.Attempt.ID, ClaimToken: claim.Attempt.ClaimToken, Units: units, Limit: limit})
		activityOutcome := "error"
		if err == nil && result.Reserved {
			activityOutcome = "reserved"
		} else if err == nil {
			activityOutcome = "limit"
		}
		r.record(Activity{Kind: "job", Name: string(descriptor.Type), Operation: "reserve_work", Outcome: activityOutcome, Duration: time.Since(started)})
		if err != nil {
			return false, err
		}
		return result.Reserved, nil
	}
	execution.checkpoint = func(ctx context.Context, value CheckpointValue) error {
		if !descriptor.Checkpoints || len(value.Document) == 0 || len(value.Document) > model.JobMaximumDocumentBytes || !json.Valid(value.Document) {
			return errors.New("job type does not declare checkpoints")
		}
		if value.Progress != nil && (value.Progress.Current < 0 || value.Progress.Total <= 0 || value.Progress.Current > value.Progress.Total || !descriptor.SupportsProgressStage(value.Progress.Stage)) {
			return errors.New("job progress stage is not declared")
		}
		started := time.Now()
		_, err := r.deps.Store.Checkpoint(ctx, &store.JobCheckpoint{AttemptID: claim.Attempt.ID, ClaimToken: claim.Attempt.ClaimToken, Progress: value.Progress, Checkpoint: value.Document})
		r.record(Activity{Kind: "job", Name: string(descriptor.Type), Operation: "checkpoint", Outcome: simpleJobOutcome(err), Duration: time.Since(started)})
		return err
	}
	outcome := runHandler(handlerCtx, descriptor.Handler, execution)
	close(heartbeatDone)
	<-heartbeatStopped
	select {
	case <-claimLost:
		r.record(Activity{Kind: "job", Name: string(descriptor.Type), Operation: "lease", Outcome: "lost"})
		metricOutcome = ExecutionClaimLost
		return
	default:
	}
	if errors.Is(parent.Err(), context.Canceled) {
		metricOutcome = ExecutionShutdown
		return
	}
	// A handler that returned success has committed its work, even when it
	// finished as its timeout elapsed; only an unfinished outcome is a timeout.
	if outcome.Kind != OutcomeSucceeded && errors.Is(handlerCtx.Err(), context.DeadlineExceeded) {
		outcome = RetryableFailure("job.timeout", handlerCtx.Err())
	}
	metricOutcome = executionOutcomeFromHandler(outcome.Kind)
	r.complete(parent, descriptor, claim, outcome)
}

// heartbeatClaimLost reports a heartbeat the store refused because another
// owner holds the attempt or its lease already expired. Any other failure is
// an unknown outcome that later heartbeats may still repair.
func heartbeatClaimLost(err error) bool {
	return store.IsConflict(err) || store.IsNotFound(err)
}

func runHandler(ctx context.Context, handler Handler, execution Execution) (outcome Outcome) {
	defer func() {
		if recovered := recover(); recovered != nil {
			outcome = RetryableFailure("job.handler_panic", fmt.Errorf("job handler panic: %T", recovered))
		}
	}()
	return handler.Run(ctx, execution)
}

func (r *Engine) complete(ctx context.Context, descriptor Descriptor, claim *store.JobClaim, outcome Outcome) {
	failureWithoutCode := (outcome.Kind == OutcomeRetryableFailure || outcome.Kind == OutcomeRelinquished || outcome.Kind == OutcomePermanentFailure) && outcome.PublicErrorCode == ""
	if failureWithoutCode || !descriptor.SupportsPublicErrorCode(outcome.PublicErrorCode) {
		outcome = PermanentFailure("job.outcome.invalid", errors.New("handler returned an undeclared public error code"))
	}
	completion := &store.JobCompletion{AttemptID: claim.Attempt.ID, ClaimToken: claim.Attempt.ClaimToken, Result: outcome.Result, PublicErrorCode: outcome.PublicErrorCode}
	switch outcome.Kind {
	case OutcomeSucceeded:
		if outcome.Err != nil || len(outcome.Result) == 0 {
			completion.Kind = store.JobCompletionPermanentFailure
			completion.PublicErrorCode = "job.result.invalid"
		} else {
			completion.Kind = store.JobCompletionSucceeded
		}
	case OutcomeRetryableFailure:
		completion.Kind = store.JobCompletionRetryableFailure
		completion.RetryDelay = retryDelay(descriptor, claim.Job.ID, claim.Attempt.Number)
	case OutcomeRelinquished:
		completion.Kind = store.JobCompletionRelinquished
		completion.RetryDelay = retryDelay(descriptor, claim.Job.ID, claim.Attempt.Number)
	case OutcomePermanentFailure:
		completion.Kind = store.JobCompletionPermanentFailure
	case OutcomeCanceled:
		completion.Kind = store.JobCompletionCanceled
	default:
		completion.Kind = store.JobCompletionPermanentFailure
		completion.PublicErrorCode = "job.outcome.invalid"
	}
	started := time.Now()
	_, err := r.deps.Store.Complete(ctx, completion)
	r.record(Activity{Kind: "job", Name: string(descriptor.Type), Operation: "complete", Outcome: simpleJobOutcome(err), Duration: time.Since(started)})
	if err != nil && !errors.Is(err, context.Canceled) {
		r.deps.Diagnostics.ErrorContext(ctx, "complete durable job", err)
	}
}

func (r *Engine) record(activity Activity) {
	if r.deps.Recorder != nil {
		r.deps.Recorder.Record(activity)
	}
}

func simpleJobOutcome(err error) string {
	switch {
	case err == nil:
		return "success"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	default:
		return "error"
	}
}

func retryDelay(descriptor Descriptor, jobID model.JobID, attempt int) time.Duration {
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
