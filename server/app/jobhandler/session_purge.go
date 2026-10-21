// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package jobhandler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	jobengine "github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// maximumSessionPurgeBatches bounds the passes of each kind in one run; the
// next hourly occurrence continues any remainder.
const maximumSessionPurgeBatches = 40

// SessionPurgeCommandV1 is the purge pass size.
type SessionPurgeCommandV1 struct {
	BatchSize int `json:"batch_size"`
}

// SessionPurgeResultV1 counts what one run deleted.
type SessionPurgeResultV1 struct {
	Sessions    int64 `json:"sessions"`
	Credentials int64 `json:"credentials"`
}

// SessionPurgeStore deletes retired Sessions.
type SessionPurgeStore interface {
	PurgeRetired(context.Context, *store.SessionPurge) (*store.SessionPurgeResult, error)
}

// SessionCredentialPurgeStore deletes retired Session credentials.
type SessionCredentialPurgeStore interface {
	PurgeRetired(context.Context, *store.SessionCredentialPurge) (int64, error)
}

type sessionPurgeHandler struct {
	sessions    SessionPurgeStore
	credentials SessionCredentialPurgeStore
	retention   time.Duration
}

func (h sessionPurgeHandler) Run(ctx context.Context, execution jobengine.Execution) jobengine.Outcome {
	if execution.Job == nil {
		return jobengine.PermanentFailure("job.command.invalid", errors.New("job is missing"))
	}
	if execution.Job.CommandVersion != 1 {
		return jobengine.PermanentFailure("job.command.invalid", fmt.Errorf("unsupported command version %d", execution.Job.CommandVersion))
	}
	var command SessionPurgeCommandV1
	if err := decodeStrictJobDocument(execution.Job.Command, &command); err != nil ||
		command.BatchSize < 1 || command.BatchSize > store.MaximumSessionPurgeBatch {
		return jobengine.PermanentFailure("job.command.invalid", errors.New("session purge command is invalid"))
	}
	if h.retention <= 0 {
		return jobengine.PermanentFailure("job.command.invalid", errors.New("session retention is not configured"))
	}
	var result SessionPurgeResultV1
	for range maximumSessionPurgeBatches {
		deleted, err := h.credentials.PurgeRetired(ctx, &store.SessionCredentialPurge{Grace: h.retention, Limit: command.BatchSize})
		if err != nil {
			return jobengine.RetryableFailure("dependency.unavailable", err)
		}
		result.Credentials += deleted
		if deleted < int64(command.BatchSize) {
			break
		}
	}
	var after store.SessionPurgePosition
	for range maximumSessionPurgeBatches {
		pass, err := h.sessions.PurgeRetired(ctx, &store.SessionPurge{Grace: h.retention, Limit: command.BatchSize, After: after})
		if err != nil {
			return jobengine.RetryableFailure("dependency.unavailable", err)
		}
		result.Sessions += pass.Sessions
		result.Credentials += pass.Credentials
		if pass.Examined < command.BatchSize {
			break
		}
		after = pass.Next
	}
	document, err := json.Marshal(result)
	return jobengine.Outcome{Kind: jobengine.OutcomeSucceeded, ResultVersion: 1, Result: document, Err: err}
}

type sessionPurgeProposer struct {
	jobs JobEnqueuer
	now  func() time.Time
}

func (p sessionPurgeProposer) Propose(ctx context.Context, occurrence time.Time) error {
	command, err := json.Marshal(SessionPurgeCommandV1{BatchSize: store.MaximumSessionPurgeBatch})
	if err != nil {
		return err
	}
	at := model.TimeUTC(p.now())
	key := "session-purge-retired:" + model.TimeUTC(occurrence).Format("2006-01-02T15")
	job, err := model.NewJobWithDedupePolicy(model.NewJobID(), model.JobTypeSessionPurgeRetired, 1, command, key, model.JobDedupePermanent, at, at, 5)
	if err != nil {
		return err
	}
	_, _, err = p.jobs.Enqueue(ctx, &store.JobEnqueue{Job: job})
	return err
}

func sessionPurgeDescriptor(handler jobengine.Handler) jobengine.Descriptor {
	return jobengine.Descriptor{Type: model.JobTypeSessionPurgeRetired, CommandVersions: []int{1}, ResultVersions: []int{1}, PublicErrorCodes: []string{"dependency.unavailable", "job.command.invalid"}, Timeout: 10 * time.Minute, Concurrency: 1, MaximumAttempts: 5, LeaseDuration: time.Minute, HeartbeatInterval: 15 * time.Second, BaseRetryDelay: time.Second, MaximumRetryDelay: time.Minute, Visibility: jobengine.VisibilityOperator, SuccessRetention: 7 * 24 * time.Hour, FailureRetention: 90 * 24 * time.Hour, Handler: handler}
}
