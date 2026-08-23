// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package job

import (
	"context"
	"errors"
	"time"
)

// PeriodicRunner performs one bounded, idempotent runtime maintenance pass.
// Unlike a durable Job, a pass has no persisted occurrence or execution
// history. Cross-node convergence remains the responsibility of the named
// application/Store operation invoked by the runner.
type PeriodicRunner interface {
	Run(context.Context) error
}

// PeriodicTask defines a non-overlapping runtime maintenance loop owned by the
// Engine lifecycle. Each node runs one loop immediately at startup and then at
// Interval after the preceding pass completes.
type PeriodicTask struct {
	Name     string
	Interval time.Duration
	Runner   PeriodicRunner
}

func clonePeriodicTasks(values []PeriodicTask) ([]PeriodicTask, error) {
	cloned := append([]PeriodicTask(nil), values...)
	names := make(map[string]struct{}, len(cloned))
	for _, task := range cloned {
		if !jobContractCode.MatchString(task.Name) || task.Interval <= 0 || task.Runner == nil {
			return nil, errors.New("invalid periodic runtime task")
		}
		if _, exists := names[task.Name]; exists {
			return nil, errors.New("duplicate periodic runtime task")
		}
		names[task.Name] = struct{}{}
	}
	return cloned, nil
}

func runPeriodicTask(ctx context.Context, task PeriodicTask, diagnostics Diagnostics, clock Clock, recorder Recorder) {
	if task.Name == "" || task.Interval <= 0 || task.Runner == nil || diagnostics == nil || clock == nil {
		return
	}
	for {
		started := time.Now()
		err := task.Runner.Run(ctx)
		if recorder != nil {
			recorder.Record(Activity{Kind: "periodic", Name: task.Name, Operation: "run", Outcome: simpleJobOutcome(err), Duration: time.Since(started)})
		}
		if err != nil && !errors.Is(err, context.Canceled) {
			diagnostics.ErrorContext(ctx, "run periodic runtime task "+task.Name, err)
		}
		if ctx.Err() != nil {
			return
		}
		timer := clock.NewTimer(task.Interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C():
				default:
				}
			}
			return
		case <-timer.C():
		}
	}
}
