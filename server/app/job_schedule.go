// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type jobEnqueuer interface {
	Enqueue(context.Context, *store.JobEnqueue) (*model.Job, bool, error)
}

type jobOccurrenceProposer interface {
	Propose(context.Context, time.Time) error
}

type dailyJobProposal struct {
	name     string
	proposer jobOccurrenceProposer
}

type filePurgeExpiredContentProposer struct {
	jobs jobEnqueuer
	wake func()
	now  func() time.Time
}

func (p filePurgeExpiredContentProposer) Propose(ctx context.Context, occurrence time.Time) error {
	if p.jobs == nil || p.now == nil {
		return errors.New("invalid file purge proposer dependencies")
	}
	command, err := EncodeFilePurgeExpiredContentCommand(FilePurgeExpiredContentCommandV1{BatchSize: 50})
	if err != nil {
		return err
	}
	at := model.TimeUTC(p.now())
	key := "file-purge-expired-content:" + model.TimeUTC(occurrence).Format("2006-01-02")
	job, err := model.NewJobWithDedupePolicy(model.NewJobID(), model.JobTypeFilePurgeExpiredContent, 1, json.RawMessage(command), key, model.JobDedupePermanent, at, at, 5)
	if err != nil {
		return err
	}
	_, _, err = p.jobs.Enqueue(ctx, &store.JobEnqueue{Job: job})
	if err == nil && p.wake != nil {
		p.wake()
	}
	return err
}

func nextDailyJobOccurrence(now time.Time) time.Time {
	now = now.UTC()
	return time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
}

func runDailyJobProposal(ctx context.Context, proposal dailyJobProposal, diagnostics recoveryDiagnostics, now func() time.Time, retryDelay time.Duration) {
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
			delay = nextDailyJobOccurrence(occurrence).Sub(now().UTC())
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
