// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package retrylayer

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/sudosylabs/proctor/server/store"
)

const maxAllowedAttempts = 10

// Policy bounds retry behavior and delegates transient-failure knowledge to
// the concrete adapter at root composition.
type Policy struct {
	MaxAttempts    int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	IsTransient    func(error) bool
}

// DefaultPolicy returns the conservative production retry policy.
func DefaultPolicy(classifier func(error) bool) Policy {
	return Policy{
		MaxAttempts:    3,
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     100 * time.Millisecond,
		IsTransient:    classifier,
	}
}

// Layer is a safe-by-default store decorator. Embedded methods are forwarded
// exactly once; only handwritten overrides opt an operation into retries.
type Layer struct {
	store.Store
	policy Policy
	stores retryStores
}

// New constructs a constrained retry layer around next.
func New(next store.Store, policy Policy) (*Layer, error) {
	if next == nil {
		return nil, errors.New("retry store is nil")
	}
	if policy.MaxAttempts < 1 || policy.MaxAttempts > maxAllowedAttempts {
		return nil, fmt.Errorf("retry max attempts must be between 1 and %d", maxAllowedAttempts)
	}
	if policy.InitialBackoff <= 0 {
		return nil, errors.New("retry initial backoff must be positive")
	}
	if policy.MaxBackoff < policy.InitialBackoff {
		return nil, errors.New("retry max backoff must not be less than initial backoff")
	}
	if policy.IsTransient == nil {
		return nil, errors.New("retry transient classifier is nil")
	}
	return &Layer{Store: next, policy: policy}, nil
}

// Ping retries the idempotent database health query.
func (l *Layer) Ping(ctx context.Context) error {
	return retryCall0(ctx, l, func() error { return l.Store.Ping(ctx) })
}

// GetDBSchemaVersion retries the idempotent persisted-version query.
func (l *Layer) GetDBSchemaVersion(ctx context.Context) (int, error) {
	return retryCall1(ctx, l, func() (int, error) { return l.Store.GetDBSchemaVersion(ctx) })
}

// ValidateSchema retries the idempotent schema inspection.
func (l *Layer) ValidateSchema(ctx context.Context) error {
	return retryCall0(ctx, l, func() error { return l.Store.ValidateSchema(ctx) })
}

func retryCall1[T any](ctx context.Context, layer *Layer, call func() (T, error)) (T, error) {
	var zero T
	delay := layer.policy.InitialBackoff
	for attempt := 1; ; attempt++ {
		result, err := call()
		if err == nil || attempt >= layer.policy.MaxAttempts || !layer.policy.IsTransient(err) {
			return result, err
		}
		if err := wait(ctx, delay); err != nil {
			return zero, err
		}
		delay = nextBackoff(delay, layer.policy.MaxBackoff)
	}
}

func retryCall0(ctx context.Context, layer *Layer, call func() error) error {
	_, err := retryCall1(ctx, layer, func() (struct{}, error) {
		return struct{}{}, call()
	})
	return err
}

func retryCall2[T, U any](
	ctx context.Context,
	layer *Layer,
	call func() (T, U, error),
) (T, U, error) {
	type pair struct {
		first  T
		second U
	}
	result, err := retryCall1(ctx, layer, func() (pair, error) {
		first, second, err := call()
		return pair{first: first, second: second}, err
	})
	return result.first, result.second, err
}

func wait(ctx context.Context, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return ctx.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
}

func nextBackoff(current, maximum time.Duration) time.Duration {
	if current >= maximum || current > maximum/2 {
		return maximum
	}
	return current * 2
}
