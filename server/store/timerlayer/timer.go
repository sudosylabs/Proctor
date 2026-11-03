// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package timerlayer

import (
	"context"
	"errors"
	"time"

	"github.com/sudosylabs/proctor/server/store"
)

// Operation is a closed, low-cardinality store operation label. The layer
// creates every value internally; callers can inspect only its stable name.
type Operation struct {
	name string
}

// String returns the stable aggregate.operation metric label.
func (o Operation) String() string { return o.name }

// Outcome is the bounded result category recorded for an operation.
type Outcome string

const (
	OutcomeSuccess Outcome = "success"
	OutcomeError   Outcome = "error"
)

// Recorder consumes bounded store timing observations. It deliberately
// receives no store arguments, results, context values, or error details.
// Implementations must return quickly; a panic is isolated from store callers.
type Recorder interface {
	Observe(Operation, Outcome, time.Duration)
}

// RecorderFunc adapts a function to Recorder.
type RecorderFunc func(Operation, Outcome, time.Duration)

func (f RecorderFunc) Observe(operation Operation, outcome Outcome, duration time.Duration) {
	f(operation, outcome, duration)
}

// NopRecorder discards observations when no metrics backend is configured.
type NopRecorder struct{}

func (NopRecorder) Observe(Operation, Outcome, time.Duration) {}

// SlowRecorder receives the operations that took at least the configured slow
// threshold. Like Recorder it receives only the closed operation name and its
// duration; the context is the caller's, for log correlation only, and must
// not be inspected for store arguments. A panic is isolated from store callers.
type SlowRecorder interface {
	ObserveSlow(context.Context, Operation, time.Duration)
}

// Tracer starts one span per store operation. It receives only the closed
// operation name, and the returned context is handed to the forwarded call so
// nested work joins the span. finish receives the bounded outcome; a panic
// in either is isolated from store callers.
type Tracer interface {
	StartOperation(ctx context.Context, operation Operation) (spanCtx context.Context, finish func(Outcome))
}

// Options selects the optional observers of a Layer beyond its Recorder.
type Options struct {
	// Tracer, when set, encloses every operation in a span.
	Tracer Tracer
	// SlowThreshold, when positive, reports every operation lasting at least
	// this long to Slow. Zero disables slow-operation reporting.
	SlowThreshold time.Duration
	Slow          SlowRecorder
}

// Layer is a semantics-preserving timing decorator for the complete root
// persistence store and every per-model store it exposes.
type Layer struct {
	next     store.Store
	recorder Recorder
	options  Options
	now      func() time.Time
	stores   timedStores
}

// New constructs a transparent timing layer around next.
func New(next store.Store, recorder Recorder, options Options) (*Layer, error) {
	if next == nil {
		return nil, errors.New("timed store is nil")
	}
	if recorder == nil {
		return nil, errors.New("store timing recorder is nil")
	}
	if options.SlowThreshold < 0 {
		return nil, errors.New("store slow-operation threshold is negative")
	}
	if options.SlowThreshold > 0 && options.Slow == nil {
		return nil, errors.New("store slow-operation recorder is nil")
	}
	return newLayer(next, recorder, options, time.Now), nil
}

func newLayer(next store.Store, recorder Recorder, options Options, now func() time.Time) *Layer {
	return &Layer{next: next, recorder: recorder, options: options, now: now}
}

func timeStoreCall0(ctx context.Context, layer *Layer, operation Operation, call func(context.Context) error) error {
	ctx, finish := layer.begin(ctx, operation)
	err := call(ctx)
	finish(err)
	return err
}

func timeStoreCall1[T any](
	ctx context.Context,
	layer *Layer,
	operation Operation,
	call func(context.Context) (T, error),
) (T, error) {
	ctx, finish := layer.begin(ctx, operation)
	result, err := call(ctx)
	finish(err)
	return result, err
}

func timeStoreCall2[T, U any](
	ctx context.Context,
	layer *Layer,
	operation Operation,
	call func(context.Context) (T, U, error),
) (T, U, error) {
	ctx, finish := layer.begin(ctx, operation)
	first, second, err := call(ctx)
	finish(err)
	return first, second, err
}

// begin starts the operation's optional span and clock. The returned context
// is the one the forwarded call receives; finish records every observation.
func (l *Layer) begin(ctx context.Context, operation Operation) (context.Context, func(error)) {
	endSpan := func(Outcome) {}
	if l.options.Tracer != nil {
		if spanCtx, finish := l.startSpan(ctx, operation); spanCtx != nil && finish != nil {
			ctx, endSpan = spanCtx, finish
		}
	}
	startedAt := l.now()
	return ctx, func(err error) {
		outcome := l.observe(ctx, operation, startedAt, err)
		func() {
			defer func() { _ = recover() }()
			endSpan(outcome)
		}()
	}
}

// startSpan asks the tracer for the operation's span. A panicking tracer
// yields no span: the recovered call never assigns the results, so the
// operation proceeds untraced on the caller's context.
func (l *Layer) startSpan(ctx context.Context, operation Operation) (spanCtx context.Context, finish func(Outcome)) {
	defer func() { _ = recover() }()
	return l.options.Tracer.StartOperation(ctx, operation)
}

func (l *Layer) observe(ctx context.Context, operation Operation, startedAt time.Time, err error) Outcome {
	outcome := OutcomeSuccess
	if err != nil {
		outcome = OutcomeError
	}
	duration := l.now().Sub(startedAt)
	if duration < 0 {
		duration = 0
	}
	func() {
		defer func() { _ = recover() }()
		l.recorder.Observe(operation, outcome, duration)
	}()
	if l.options.SlowThreshold > 0 && duration >= l.options.SlowThreshold {
		func() {
			defer func() { _ = recover() }()
			l.options.Slow.ObserveSlow(ctx, operation, duration)
		}()
	}
	return outcome
}
