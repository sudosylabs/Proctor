// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package job

import (
	"context"
	"errors"
	"time"
)

type OccurrenceProposer interface {
	Propose(context.Context, time.Time) error
}

// Recurrence defines application-owned work proposed at UTC interval boundaries.
// A zero Interval retains the daily schedule. Explicit intervals must divide a
// day and range from one minute to one day. Each proposer durably deduplicates
// its occurrence so restarts and multiple nodes can propose the same work.
type Recurrence struct {
	Name     string
	Proposer OccurrenceProposer
	Interval time.Duration
}

// Clock controls recurrence time and waiting. Now must be safe for concurrent
// use. NewTimer returns a fresh one-shot Timer for each positive duration.
// Production uses the system clock; tests can advance occurrences without
// sleeping.
type Clock interface {
	Now() time.Time
	NewTimer(time.Duration) Timer
}

// Timer is the bounded waiting capability used by recurrence workers. C
// delivers at most one expiration value. Stop prevents a future delivery when
// it returns true and is safe to call while the engine is shutting down.
type Timer interface {
	C() <-chan time.Time
	Stop() bool
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

func (systemClock) NewTimer(delay time.Duration) Timer {
	return systemTimer{timer: time.NewTimer(delay)}
}

type systemTimer struct {
	timer *time.Timer
}

func (t systemTimer) C() <-chan time.Time { return t.timer.C }
func (t systemTimer) Stop() bool          { return t.timer.Stop() }

func cloneRecurrences(values []Recurrence) ([]Recurrence, error) {
	cloned := append([]Recurrence(nil), values...)
	names := make(map[string]struct{}, len(cloned))
	for _, recurrence := range cloned {
		if !jobContractCode.MatchString(recurrence.Name) || recurrence.Proposer == nil {
			return nil, errors.New("invalid job recurrence")
		}
		if recurrence.Interval != 0 && (recurrence.Interval < time.Minute || recurrence.Interval > 24*time.Hour || 24*time.Hour%recurrence.Interval != 0) {
			return nil, errors.New("invalid job recurrence interval")
		}
		if _, exists := names[recurrence.Name]; exists {
			return nil, errors.New("duplicate job recurrence")
		}
		names[recurrence.Name] = struct{}{}
	}
	return cloned, nil
}

func nextDailyOccurrence(now time.Time) time.Time {
	now = now.UTC()
	return time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
}

func nextRecurrenceOccurrence(now time.Time, interval time.Duration) time.Time {
	if interval == 0 {
		return nextDailyOccurrence(now)
	}
	return now.UTC().Truncate(interval).Add(interval)
}

func runRecurrenceProposal(ctx context.Context, recurrence Recurrence, diagnostics Diagnostics, clock Clock, retryDelay time.Duration, wake func(), recorder Recorder) {
	if recurrence.Name == "" || recurrence.Proposer == nil || diagnostics == nil || clock == nil || retryDelay <= 0 {
		return
	}
	occurrence := clock.Now().UTC()
	for {
		started := time.Now()
		err := recurrence.Proposer.Propose(ctx, occurrence)
		if recorder != nil {
			recorder.Record(Activity{Kind: "recurrence", Name: recurrence.Name, Operation: "propose", Outcome: simpleJobOutcome(err), Duration: time.Since(started)})
		}
		if err != nil && !errors.Is(err, context.Canceled) {
			diagnostics.ErrorContext(ctx, "propose durable job "+recurrence.Name, err)
		}
		delay := retryDelay
		if err == nil {
			if wake != nil {
				wake()
			}
			delay = nextRecurrenceOccurrence(occurrence, recurrence.Interval).Sub(clock.Now().UTC())
		}
		if delay <= 0 {
			occurrence = clock.Now().UTC()
			continue
		}
		timer := clock.NewTimer(delay)
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
			if err == nil {
				occurrence = clock.Now().UTC()
			}
		}
	}
}
