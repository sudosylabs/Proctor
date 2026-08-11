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

type filePurgeExpiredContentProposer struct {
	jobs jobEnqueuer
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
	return err
}
