// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package timerlayer

import (
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

// Layer is a semantics-preserving timing decorator for the complete root
// persistence store and every per-model store it exposes.
type Layer struct {
	next     store.Store
	recorder Recorder
	now      func() time.Time
	stores   timedStores
}

// New constructs a transparent timing layer around next.
func New(next store.Store, recorder Recorder) (*Layer, error) {
	if next == nil {
		return nil, errors.New("timed store is nil")
	}
	if recorder == nil {
		return nil, errors.New("store timing recorder is nil")
	}
	return newLayer(next, recorder, time.Now), nil
}

func newLayer(next store.Store, recorder Recorder, now func() time.Time) *Layer {
	return &Layer{next: next, recorder: recorder, now: now}
}

func timeStoreCall0(layer *Layer, operation Operation, call func() error) error {
	startedAt := layer.now()
	err := call()
	layer.observe(operation, startedAt, err)
	return err
}

func timeStoreCall1[T any](layer *Layer, operation Operation, call func() (T, error)) (T, error) {
	startedAt := layer.now()
	result, err := call()
	layer.observe(operation, startedAt, err)
	return result, err
}

func timeStoreCall2[T, U any](
	layer *Layer,
	operation Operation,
	call func() (T, U, error),
) (T, U, error) {
	startedAt := layer.now()
	first, second, err := call()
	layer.observe(operation, startedAt, err)
	return first, second, err
}

func (l *Layer) observe(operation Operation, startedAt time.Time, err error) {
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
}
