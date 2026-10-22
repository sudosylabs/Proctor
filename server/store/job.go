// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

type JobEnqueue struct {
	Job *model.Job
}

type JobClaimRequest struct {
	Types         []model.JobType
	NodeID        string
	ClaimToken    model.JobClaimToken
	LeaseDuration time.Duration
}

type JobClaim struct {
	Job     *model.Job
	Attempt *model.JobAttempt
}

type JobHeartbeat struct {
	AttemptID     model.JobAttemptID
	ClaimToken    model.JobClaimToken
	LeaseDuration time.Duration
}

type JobCheckpoint struct {
	AttemptID  model.JobAttemptID
	ClaimToken model.JobClaimToken
	Progress   *model.JobProgress
	Checkpoint json.RawMessage
}

type JobWorkReservation struct {
	AttemptID  model.JobAttemptID
	ClaimToken model.JobClaimToken
	Units      int
	Limit      int
}

type JobWorkReservationResult struct {
	Reserved bool
	Consumed int
}

type JobCompletionKind string

const (
	JobCompletionSucceeded        JobCompletionKind = "succeeded"
	JobCompletionRetryableFailure JobCompletionKind = "retryable_failure"
	JobCompletionRelinquished     JobCompletionKind = "relinquished"
	JobCompletionPermanentFailure JobCompletionKind = "permanent_failure"
	JobCompletionCanceled         JobCompletionKind = "canceled"
)

type JobCompletion struct {
	AttemptID       model.JobAttemptID
	ClaimToken      model.JobClaimToken
	Kind            JobCompletionKind
	RetryDelay      time.Duration
	Result          json.RawMessage
	PublicErrorCode string
}

type JobListOptions struct {
	Types           []model.JobType
	Statuses        []model.JobStatus
	BeforeCreatedAt time.Time
	BeforeID        model.JobID
	Limit           int
}

type JobAttemptListOptions struct {
	JobID        model.JobID
	BeforeNumber int
	Limit        int
}

type JobMutation struct {
	ID               model.JobID
	ExpectedRevision int64
	AuditEventID     model.AuditEventID
	AuditAt          time.Time
}

type JobRetentionPolicy struct {
	Type                 model.JobType
	SucceededCanceledAge time.Duration
	FailedAge            time.Duration
}

type JobHistoryCleanup struct {
	ExcludeJobID     model.JobID
	Policies         []JobRetentionPolicy
	AfterCompletedAt time.Time
	AfterJobID       model.JobID
	Limit            int
}

type JobHistoryCleanupResult struct {
	Deleted         int64
	LastCompletedAt time.Time
	LastJobID       model.JobID
	Done            bool
}

// JobStore owns durable, at-least-once work claiming and its fencing rules.
type JobStore interface {
	// Enqueue atomically deduplicates one logical occurrence by type, key, and
	// the Job's persisted policy. Active dedupe admits a replacement after a
	// terminal outcome; permanent dedupe reserves the key even after retention
	// removes its Job history. The returned boolean reports whether the supplied
	// Job was inserted; a retained winner is returned when one still exists.
	Enqueue(context.Context, *JobEnqueue) (*model.Job, bool, error)
	// ClaimNext expires lost attempts, requeues their Jobs, then atomically claims
	// one eligible Job using SKIP LOCKED and creates its next append-only Attempt.
	// Availability, expiry, and lease timestamps use the primary database clock;
	// an exhausted expired attempt atomically fails its Job instead of stranding it.
	ClaimNext(context.Context, *JobClaimRequest) (*JobClaim, error)
	// Heartbeat extends only the live Attempt owning the exact claim token, with
	// both fence validation and the new expiry based on the database clock.
	Heartbeat(context.Context, *JobHeartbeat) (*model.JobAttempt, error)
	// Checkpoint atomically updates bounded Job progress only for the live token
	// before its database-clock lease expiry.
	Checkpoint(context.Context, *JobCheckpoint) (*model.Job, error)
	// ReserveWork atomically consumes an occurrence-wide unit budget for the
	// live fenced Attempt. Reservations remain consumed across retries so a
	// crashed worker can under-process, but can never repeat beyond the limit.
	ReserveWork(context.Context, *JobWorkReservation) (*JobWorkReservationResult, error)
	// Complete atomically closes the fenced Attempt and transitions its Job to a
	// terminal state or a future queued retry. An expired token cannot complete.
	Complete(context.Context, *JobCompletion) (*model.Job, error)
	Get(context.Context, model.JobID) (*model.Job, error)
	ListAttempts(context.Context, model.JobID) ([]model.JobAttempt, error)
	List(context.Context, JobListOptions) ([]*model.Job, error)
	ListAttemptsPage(context.Context, JobAttemptListOptions) ([]model.JobAttempt, error)
	// CancellationRequested observes the durable cancellation bit only while the
	// supplied Attempt still owns the live fence.
	CancellationRequested(context.Context, model.JobAttemptID, model.JobClaimToken) (bool, error)
	// Cancel and Retry atomically mutate the Job and complete
	// an already-durable operator audit attempt.
	Cancel(context.Context, *JobMutation) (*model.Job, error)
	Retry(context.Context, *JobMutation) (*model.Job, error)
	// DeleteTerminalHistory atomically selects and removes one bounded,
	// oldest-first page of retention-eligible terminal Jobs and their Attempts.
	// It revalidates per-type ages against the primary database clock, never
	// selects active work, and always excludes the caller's cleanup Job.
	DeleteTerminalHistory(context.Context, *JobHistoryCleanup) (*JobHistoryCleanupResult, error)
}
