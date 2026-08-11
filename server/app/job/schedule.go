// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package job

import (
	"context"
	"errors"
	"time"
)

type OccurrenceProposer interface {
	Propose(context.Context, time.Time) error
}

type dailyProposal struct {
	name     string
	proposer OccurrenceProposer
}

func nextDailyOccurrence(now time.Time) time.Time {
	now = now.UTC()
	return time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
}

func runDailyProposal(ctx context.Context, proposal dailyProposal, diagnostics Diagnostics, now func() time.Time, retryDelay time.Duration) {
	if proposal.name == "" || proposal.proposer == nil || diagnostics == nil || now == nil || retryDelay <= 0 {
		return
	}
	occurrence := now().UTC()
	for {
		err := proposal.proposer.Propose(ctx, occurrence)
		if err != nil && !errors.Is(err, context.Canceled) {
			diagnostics.ErrorContext(ctx, "propose daily durable job "+proposal.name, err)
		}
		delay := retryDelay
		if err == nil {
			delay = nextDailyOccurrence(occurrence).Sub(now().UTC())
		}
		if delay <= 0 {
			occurrence = now().UTC()
			continue
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-timer.C:
			if err == nil {
				occurrence = now().UTC()
			}
		}
	}
}
