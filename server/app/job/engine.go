// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

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
	jobs               store.JobStore
	registry           *Registry
	nodeID             string
	diagnostics        Diagnostics
	poll               time.Duration
	shutdownTimeout    time.Duration
	wake               chan struct{}
	dailyProposals     []dailyProposal
	proposalRetryDelay time.Duration
	clock              func() time.Time

	mu      sync.Mutex
	started bool
	closed  bool
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

func (r *Engine) AddDailyProposal(name string, proposer OccurrenceProposer) error {
	if name == "" || proposer == nil {
		return errors.New("invalid daily job proposal")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started || r.closed {
		return errors.New("daily job proposals must be registered before start")
	}
	r.dailyProposals = append(r.dailyProposals, dailyProposal{name: name, proposer: proposer})
	return nil
}

type Diagnostics interface {
	ErrorContext(context.Context, string, error)
}

type Policy struct {
	PollInterval       time.Duration
	ShutdownTimeout    time.Duration
	ProposalRetryDelay time.Duration
}

type Config struct {
	Store       store.JobStore
	Descriptors []Descriptor
	NodeID      string
	Diagnostics Diagnostics
	Policy      Policy
	Clock       func() time.Time
}

func New(config Config) (*Engine, error) {
	registry, err := NewRegistry(config.Descriptors)
	if err != nil {
		return nil, err
	}
	if config.Store == nil || config.NodeID == "" || config.Diagnostics == nil || config.Policy.PollInterval <= 0 {
		return nil, errors.New("invalid job engine dependencies")
	}
	if config.Policy.ShutdownTimeout <= 0 {
		config.Policy.ShutdownTimeout = registry.MaximumTimeout()
	}
	if config.Policy.ProposalRetryDelay <= 0 {
		config.Policy.ProposalRetryDelay = time.Minute
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}
	return &Engine{jobs: config.Store, registry: registry, nodeID: config.NodeID, diagnostics: config.Diagnostics, poll: config.Policy.PollInterval, shutdownTimeout: config.Policy.ShutdownTimeout, proposalRetryDelay: config.Policy.ProposalRetryDelay, clock: config.Clock, wake: make(chan struct{}, 1)}, nil
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
	for _, proposal := range r.dailyProposals {
		r.wg.Add(1)
		go func(value dailyProposal) {
			defer r.wg.Done()
			runDailyProposal(runCtx, value, r.diagnostics, r.clock, r.proposalRetryDelay)
		}(proposal)
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
	case <-time.After(r.shutdownTimeout):
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
			r.complete(ctx, descriptor, claim, PermanentFailure("job.command.unsupported", err))
		} else {
			r.execute(ctx, resolved, claim)
		}
		resetTimer(timer, 0)
	}
}

func (r *Engine) execute(parent context.Context, descriptor Descriptor, claim *store.JobClaim) {
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
				if descriptor.Cancelable {
					requested, err := r.jobs.CancellationRequested(handlerCtx, claim.Attempt.ID, claim.Attempt.ClaimToken)
					if err != nil {
						r.diagnostics.ErrorContext(parent, "observe durable job cancellation", err)
						select {
						case claimLost <- struct{}{}:
						default:
						}
						cancelHandler()
						return
					}
					if requested {
						cancelHandler()
						return
					}
				}
			}
		}
	}()
	execution := NewExecution(claim.Job, claim.Attempt, nil, nil)
	execution.reserveWork = func(ctx context.Context, units, limit int) (bool, error) {
		result, err := r.jobs.ReserveWork(ctx, &store.JobWorkReservation{AttemptID: claim.Attempt.ID, ClaimToken: claim.Attempt.ClaimToken, Units: units, Limit: limit})
		if err != nil {
			return false, err
		}
		return result.Reserved, nil
	}
	execution.checkpoint = func(ctx context.Context, value CheckpointValue) error {
		if !descriptor.SupportsCheckpointVersion(value.Version) || len(value.Document) == 0 || len(value.Document) > model.JobMaximumDocumentBytes || !json.Valid(value.Document) {
			return errors.New("job checkpoint version is not declared")
		}
		if value.Progress != nil && (value.Progress.Current < 0 || value.Progress.Total <= 0 || value.Progress.Current > value.Progress.Total || !descriptor.SupportsProgressStage(value.Progress.Stage)) {
			return errors.New("job progress stage is not declared")
		}
		_, err := r.jobs.Checkpoint(ctx, &store.JobCheckpoint{AttemptID: claim.Attempt.ID, ClaimToken: claim.Attempt.ClaimToken, Progress: value.Progress, CheckpointVersion: value.Version, Checkpoint: value.Document})
		return err
	}
	outcome := runHandler(handlerCtx, descriptor.Handler, execution)
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
		outcome = RetryableFailure("job.timeout", handlerCtx.Err())
	}
	r.complete(parent, descriptor, claim, outcome)
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
	failureWithoutCode := (outcome.Kind == OutcomeRetryableFailure || outcome.Kind == OutcomePermanentFailure) && outcome.PublicErrorCode == ""
	if failureWithoutCode || !descriptor.SupportsPublicErrorCode(outcome.PublicErrorCode) {
		outcome = PermanentFailure("job.outcome.invalid", errors.New("handler returned an undeclared public error code"))
	}
	completion := &store.JobCompletion{AttemptID: claim.Attempt.ID, ClaimToken: claim.Attempt.ClaimToken, ResultVersion: outcome.ResultVersion, Result: outcome.Result, PublicErrorCode: outcome.PublicErrorCode}
	switch outcome.Kind {
	case OutcomeSucceeded:
		if outcome.Err != nil || !descriptor.SupportsResultVersion(outcome.ResultVersion) || len(outcome.Result) == 0 {
			completion.Kind = store.JobCompletionPermanentFailure
			completion.PublicErrorCode = "job.result.invalid"
		} else {
			completion.Kind = store.JobCompletionSucceeded
		}
	case OutcomeRetryableFailure:
		completion.Kind = store.JobCompletionRetryableFailure
		completion.RetryDelay = retryDelay(descriptor, claim.Job.ID, claim.Attempt.Number)
	case OutcomePermanentFailure:
		completion.Kind = store.JobCompletionPermanentFailure
	case OutcomeCanceled:
		completion.Kind = store.JobCompletionCanceled
	default:
		completion.Kind = store.JobCompletionPermanentFailure
		completion.PublicErrorCode = "job.outcome.invalid"
	}
	if _, err := r.jobs.Complete(ctx, completion); err != nil && !errors.Is(err, context.Canceled) {
		r.diagnostics.ErrorContext(ctx, "complete durable job", err)
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
