// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type SQLJobStore struct{ *SQLStore }

type permanentJobEnqueueOutcome struct {
	job     *model.Job
	row     *jobRow
	created bool
}

type jobClaimTransactionOutcome struct {
	claim         *store.JobClaim
	postCommitErr error
}

type jobHistoryCleanupOutcome struct {
	result *store.JobHistoryCleanupResult
	empty  bool
}

func permanentJobTransactionPolicy() sqlTransactionPolicy[*permanentJobEnqueueOutcome] {
	return sqlTransactionPolicy[*permanentJobEnqueueOutcome]{
		beginError: func(err error) error { return fmt.Errorf("begin permanent job enqueue: %w", err) },
		commit:     true,
		commitError: func(outcome *permanentJobEnqueueOutcome, err error) error {
			if outcome != nil && outcome.created {
				return fmt.Errorf("commit permanent job enqueue: %w", err)
			}
			return fmt.Errorf("commit deduplicated permanent job enqueue: %w", err)
		},
	}
}

func jobClaimSQLTransactionPolicy() sqlTransactionPolicy[*jobClaimTransactionOutcome] {
	return sqlTransactionPolicy[*jobClaimTransactionOutcome]{
		beginError: func(err error) error { return fmt.Errorf("begin job claim: %w", err) },
		commit:     true,
		commitError: func(outcome *jobClaimTransactionOutcome, err error) error {
			if outcome != nil && outcome.postCommitErr != nil {
				return fmt.Errorf("commit expired job recovery: %w", err)
			}
			return fmt.Errorf("commit job claim: %w", err)
		},
	}
}

func jobHistorySQLTransactionPolicy() sqlTransactionPolicy[*jobHistoryCleanupOutcome] {
	return sqlTransactionPolicy[*jobHistoryCleanupOutcome]{
		beginError: func(err error) error { return fmt.Errorf("begin job history cleanup: %w", err) },
		commit:     true,
		commitError: func(outcome *jobHistoryCleanupOutcome, err error) error {
			if outcome != nil && outcome.empty {
				return fmt.Errorf("commit empty job history cleanup: %w", err)
			}
			return fmt.Errorf("commit job history cleanup: %w", err)
		},
	}
}

type jobRow struct {
	ID                string         `db:"id"`
	Type              string         `db:"type"`
	Status            string         `db:"status"`
	CreatedAt         time.Time      `db:"created_at"`
	UpdatedAt         time.Time      `db:"updated_at"`
	AvailableAt       time.Time      `db:"available_at"`
	StartedAt         sql.NullTime   `db:"started_at"`
	CompletedAt       sql.NullTime   `db:"completed_at"`
	CommandVersion    int            `db:"command_version"`
	Command           jsonValue      `db:"command"`
	CheckpointVersion sql.NullInt64  `db:"checkpoint_version"`
	Checkpoint        jsonValue      `db:"checkpoint"`
	ResultVersion     sql.NullInt64  `db:"result_version"`
	Result            jsonValue      `db:"result"`
	PublicErrorCode   string         `db:"public_error_code"`
	DedupeKey         string         `db:"dedupe_key"`
	DedupePolicy      string         `db:"dedupe_policy"`
	AttemptCount      int            `db:"attempt_count"`
	MaximumAttempts   int            `db:"maximum_attempts"`
	WorkReserved      int            `db:"work_reserved"`
	ProgressCurrent   sql.NullInt64  `db:"progress_current"`
	ProgressTotal     sql.NullInt64  `db:"progress_total"`
	ProgressStage     sql.NullString `db:"progress_stage"`
	Revision          int64          `db:"revision"`
}

type jobAttemptRow struct {
	ID              string       `db:"id"`
	JobID           string       `db:"job_id"`
	Number          int          `db:"number"`
	Status          string       `db:"status"`
	NodeID          string       `db:"node_id"`
	ClaimToken      string       `db:"claim_token"`
	StartedAt       time.Time    `db:"started_at"`
	HeartbeatAt     time.Time    `db:"heartbeat_at"`
	LeaseExpiresAt  time.Time    `db:"lease_expires_at"`
	CompletedAt     sql.NullTime `db:"completed_at"`
	PublicErrorCode string       `db:"public_error_code"`
}

const jobColumns = `id, type, status, created_at, updated_at, available_at, started_at, completed_at, command_version, command, checkpoint_version, checkpoint, result_version, result, public_error_code, dedupe_key, dedupe_policy, attempt_count, maximum_attempts, work_reserved, progress_current, progress_total, progress_stage, revision`
const jobAttemptColumns = `id, job_id, number, status, node_id, claim_token, started_at, heartbeat_at, lease_expires_at, completed_at, public_error_code`

func newSQLJobStore(sqlStore *SQLStore) store.JobStore { return &SQLJobStore{SQLStore: sqlStore} }

func (s SQLJobStore) Enqueue(ctx context.Context, input *store.JobEnqueue) (*model.Job, bool, error) {
	if input == nil || input.Job == nil {
		return nil, false, store.NewErrInvalidInput("job", "enqueue", nil)
	}
	if err := input.Job.Validate(); err != nil || input.Job.Status != model.JobStatusQueued || input.Job.AttemptCount != 0 {
		return nil, false, store.NewErrInvalidInput("job", "enqueue", err)
	}
	if input.Job.DedupePolicy == model.JobDedupePermanent {
		return s.enqueuePermanent(ctx, input.Job)
	}
	result, err := insertQueuedJob(ctx, s.GetMaster(), input.Job, true)
	if err != nil {
		return nil, false, fmt.Errorf("enqueue job: %w", translateError("job", input.Job.ID.String(), err))
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return nil, false, fmt.Errorf("read enqueued job rows: %w", err)
	}
	if affected == 1 {
		copy := *input.Job
		return &copy, true, nil
	}
	var row jobRow
	query := `SELECT ` + jobColumns + ` FROM jobs WHERE type = ? AND dedupe_key = ? AND dedupe_policy = ?`
	if input.Job.DedupePolicy == model.JobDedupeActive {
		query += ` AND status IN ('queued', 'running', 'cancel_requested')`
	}
	if err = s.GetMaster().Get(ctx, &row, query, string(input.Job.Type), input.Job.DedupeKey, string(input.Job.DedupePolicy)); err != nil {
		return nil, false, translateError("job", input.Job.DedupeKey, err)
	}
	job, err := row.model()
	return job, false, err
}

func (s SQLJobStore) enqueuePermanent(ctx context.Context, proposed *model.Job) (*model.Job, bool, error) {
	outcome, err := executeSQLTransaction(ctx, s.GetMaster().Begin, permanentJobTransactionPolicy(), func(ctx context.Context, tx *sqlxTxWrapper) (*permanentJobEnqueueOutcome, error) {
		result, err := tx.Exec(ctx, `INSERT INTO job_permanent_occurrences (type, dedupe_key, job_id, created_at) VALUES (?, ?, ?, ?) ON CONFLICT (type, dedupe_key) DO NOTHING`, string(proposed.Type), proposed.DedupeKey, proposed.ID.String(), proposed.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("reserve permanent job occurrence: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return nil, fmt.Errorf("read permanent job occurrence rows: %w", err)
		}
		if affected == 1 {
			if _, err = insertQueuedJob(ctx, tx, proposed, false); err != nil {
				return nil, fmt.Errorf("enqueue permanent job: %w", translateError("job", proposed.ID.String(), err))
			}
			copy := *proposed
			return &permanentJobEnqueueOutcome{job: &copy, created: true}, nil
		}
		var row jobRow
		err = tx.Get(ctx, &row, `SELECT `+jobColumns+` FROM jobs WHERE id = (SELECT job_id FROM job_permanent_occurrences WHERE type = ? AND dedupe_key = ?)`, string(proposed.Type), proposed.DedupeKey)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("read permanent job occurrence: %w", err)
		}
		if errors.Is(err, sql.ErrNoRows) {
			return &permanentJobEnqueueOutcome{}, nil
		}
		return &permanentJobEnqueueOutcome{row: &row}, nil
	})
	if err != nil {
		return nil, false, err
	}
	if outcome.job != nil {
		return outcome.job, outcome.created, nil
	}
	if outcome.row == nil {
		return nil, false, nil
	}
	job, err := outcome.row.model()
	return job, false, err
}

func insertQueuedJob(ctx context.Context, executor sqlxExecutor, job *model.Job, deduplicate bool) (sql.Result, error) {
	conflict := ""
	if deduplicate {
		switch job.DedupePolicy {
		case model.JobDedupeActive:
			conflict = " ON CONFLICT (type, dedupe_key) WHERE dedupe_policy = 'active' AND status IN ('queued', 'running', 'cancel_requested') DO NOTHING"
		case model.JobDedupePermanent:
			conflict = " ON CONFLICT (type, dedupe_key) WHERE dedupe_policy = 'permanent' DO NOTHING"
		}
	}
	return executor.Exec(ctx, `INSERT INTO jobs (`+jobColumns+`) VALUES (?, ?, ?, ?, ?, ?, NULL, NULL, ?, ?, NULL, NULL, NULL, NULL, '', ?, ?, 0, ?, 0, NULL, NULL, NULL, ?)`+conflict, job.ID.String(), string(job.Type), string(job.Status), job.CreatedAt, job.UpdatedAt, job.AvailableAt, job.CommandVersion, job.Command, job.DedupeKey, string(job.DedupePolicy), job.MaximumAttempts, job.Revision)
}

func (s SQLJobStore) ClaimNext(ctx context.Context, request *store.JobClaimRequest) (*store.JobClaim, error) {
	if request == nil || len(request.Types) == 0 || strings.TrimSpace(request.NodeID) == "" || !request.ClaimToken.IsValid() || request.LeaseDuration <= 0 || request.LeaseDuration > time.Hour {
		return nil, store.NewErrInvalidInput("job", "claim", nil)
	}
	types := make([]string, len(request.Types))
	for index, jobType := range request.Types {
		types[index] = string(jobType)
	}
	outcome, err := executeSQLTransaction(ctx, s.GetMaster().Begin, jobClaimSQLTransactionPolicy(), func(ctx context.Context, tx *sqlxTxWrapper) (*jobClaimTransactionOutcome, error) {
		databaseNow, err := jobDatabaseNow(ctx, tx)
		if err != nil {
			return nil, err
		}
		leaseExpiresAt := databaseNow.Add(request.LeaseDuration)
		var expiredJobIDs []string
		if err = tx.Select(ctx, &expiredJobIDs, `UPDATE job_attempts SET status = 'lease_expired', completed_at = ? WHERE status = 'running' AND lease_expires_at <= ? RETURNING job_id`, databaseNow, databaseNow); err != nil {
			return nil, fmt.Errorf("expire lost job attempts: %w", err)
		}
		for _, jobID := range expiredJobIDs {
			if _, err = tx.Exec(ctx, `UPDATE jobs SET status = CASE WHEN status = 'cancel_requested' THEN 'canceled' WHEN attempt_count >= maximum_attempts THEN 'failed' ELSE 'queued' END, completed_at = CASE WHEN status = 'cancel_requested' OR attempt_count >= maximum_attempts THEN CAST(? AS timestamptz) ELSE NULL END, public_error_code = CASE WHEN status = 'cancel_requested' THEN 'job.canceled' WHEN attempt_count >= maximum_attempts THEN 'job.lease_expired' ELSE public_error_code END, available_at = ?, updated_at = GREATEST(updated_at, ?), revision = revision + 1 WHERE id = ? AND status IN ('running', 'cancel_requested')`, databaseNow, databaseNow, databaseNow, jobID); err != nil {
				return nil, fmt.Errorf("recover expired job %s: %w", jobID, err)
			}
		}
		var row jobRow
		if err = tx.Get(ctx, &row, `SELECT `+jobColumns+` FROM jobs WHERE status = 'queued' AND available_at <= ? AND attempt_count < maximum_attempts AND type = ANY(?)
			AND NOT EXISTS (SELECT 1 FROM job_attempts WHERE job_attempts.job_id = jobs.id AND job_attempts.node_id = ? AND job_attempts.status = 'incompatible')
			ORDER BY available_at, created_at, id FOR UPDATE SKIP LOCKED LIMIT 1`, databaseNow, pq.Array(types), strings.TrimSpace(request.NodeID)); err != nil {
			claimableErr := translateError("job", "claimable", err)
			if errors.Is(err, sql.ErrNoRows) && len(expiredJobIDs) > 0 {
				return &jobClaimTransactionOutcome{postCommitErr: claimableErr}, nil
			}
			return nil, claimableErr
		}
		job, err := row.model()
		if err != nil {
			return nil, err
		}
		running, err := job.Start(databaseNow)
		if err != nil {
			return nil, store.NewErrConflict("job", "not_claimable", err)
		}
		if err = updateJob(ctx, tx, running); err != nil {
			return nil, err
		}
		attempt, err := model.NewJobAttempt(model.NewJobAttemptID(), running.ID, running.AttemptCount, request.NodeID, request.ClaimToken, databaseNow, leaseExpiresAt)
		if err != nil {
			return nil, store.NewErrInvalidInput("job_attempt", "claim", err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO job_attempts (`+jobAttemptColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, '')`, attempt.ID.String(), attempt.JobID.String(), attempt.Number, string(attempt.Status), attempt.NodeID, string(attempt.ClaimToken), attempt.StartedAt, attempt.HeartbeatAt, attempt.LeaseExpiresAt); err != nil {
			return nil, fmt.Errorf("create job attempt: %w", translateError("job_attempt", attempt.ID.String(), err))
		}
		return &jobClaimTransactionOutcome{claim: &store.JobClaim{Job: running, Attempt: attempt}}, nil
	})
	if err != nil {
		return nil, err
	}
	if outcome.postCommitErr != nil {
		return nil, outcome.postCommitErr
	}
	return outcome.claim, nil
}

func (s SQLJobStore) Heartbeat(ctx context.Context, input *store.JobHeartbeat) (*model.JobAttempt, error) {
	if input == nil || !input.AttemptID.IsValid() || !input.ClaimToken.IsValid() || input.LeaseDuration <= 0 || input.LeaseDuration > time.Hour {
		return nil, store.NewErrInvalidInput("job_attempt", "heartbeat", nil)
	}
	return executeSQLTransaction(ctx, s.GetMaster().Begin, rawSQLTransactionPolicy[*model.JobAttempt](true, func(_ *model.JobAttempt, err error) error { return err }), func(ctx context.Context, tx *sqlxTxWrapper) (*model.JobAttempt, error) {
		databaseNow, err := jobDatabaseNow(ctx, tx)
		if err != nil {
			return nil, err
		}
		attempt, err := getFencedAttempt(ctx, tx, input.AttemptID, input.ClaimToken, databaseNow)
		if err != nil {
			return nil, err
		}
		heartbeat, err := attempt.Heartbeat(databaseNow, databaseNow.Add(input.LeaseDuration))
		if err != nil {
			return nil, store.NewErrConflict("job_attempt", "not_running", err)
		}
		if _, err = tx.Exec(ctx, `UPDATE job_attempts SET heartbeat_at = ?, lease_expires_at = ? WHERE id = ? AND claim_token = ? AND status = 'running'`, heartbeat.HeartbeatAt, heartbeat.LeaseExpiresAt, heartbeat.ID.String(), string(heartbeat.ClaimToken)); err != nil {
			return nil, err
		}
		return heartbeat, nil
	})
}

func (s SQLJobStore) Checkpoint(ctx context.Context, input *store.JobCheckpoint) (*model.Job, error) {
	if input == nil || !input.AttemptID.IsValid() || !input.ClaimToken.IsValid() {
		return nil, store.NewErrInvalidInput("job", "checkpoint", nil)
	}
	return executeSQLTransaction(ctx, s.GetMaster().Begin, rawSQLTransactionPolicy[*model.Job](true, func(_ *model.Job, err error) error { return err }), func(ctx context.Context, tx *sqlxTxWrapper) (*model.Job, error) {
		databaseNow, err := jobDatabaseNow(ctx, tx)
		if err != nil {
			return nil, err
		}
		attempt, err := getFencedAttempt(ctx, tx, input.AttemptID, input.ClaimToken, databaseNow)
		if err != nil {
			return nil, err
		}
		job, err := getJob(ctx, tx, attempt.JobID, true)
		if err != nil {
			return nil, err
		}
		updated, err := job.UpdateProgress(input.Progress, input.CheckpointVersion, input.Checkpoint, databaseNow)
		if err != nil {
			return nil, store.NewErrInvalidInput("job", "checkpoint", err)
		}
		if err = updateJob(ctx, tx, updated); err != nil {
			return nil, err
		}
		return updated, nil
	})
}

func (s SQLJobStore) ReserveWork(ctx context.Context, input *store.JobWorkReservation) (*store.JobWorkReservationResult, error) {
	if input == nil || !input.AttemptID.IsValid() || !input.ClaimToken.IsValid() || input.Units <= 0 || input.Limit <= 0 || input.Units > input.Limit || input.Limit > 1_000_000 {
		return nil, store.NewErrInvalidInput("job", "reserve_work", nil)
	}
	return executeSQLTransaction(ctx, s.GetMaster().Begin, rawSQLTransactionPolicy[*store.JobWorkReservationResult](true, func(_ *store.JobWorkReservationResult, err error) error {
		return fmt.Errorf("commit job work reservation: %w", err)
	}), func(ctx context.Context, tx *sqlxTxWrapper) (*store.JobWorkReservationResult, error) {
		databaseNow, err := jobDatabaseNow(ctx, tx)
		if err != nil {
			return nil, err
		}
		attempt, err := getFencedAttempt(ctx, tx, input.AttemptID, input.ClaimToken, databaseNow)
		if err != nil {
			return nil, err
		}
		var consumed int
		err = tx.Get(ctx, &consumed, `UPDATE jobs SET work_reserved = work_reserved + ?, updated_at = GREATEST(updated_at, ?), revision = revision + 1 WHERE id = ? AND work_reserved + ? <= ? RETURNING work_reserved`, input.Units, databaseNow, attempt.JobID.String(), input.Units, input.Limit)
		reserved := err == nil
		if errors.Is(err, sql.ErrNoRows) {
			err = tx.Get(ctx, &consumed, `SELECT work_reserved FROM jobs WHERE id = ?`, attempt.JobID.String())
		}
		if err != nil {
			return nil, fmt.Errorf("reserve job work: %w", translateError("job", attempt.JobID.String(), err))
		}
		return &store.JobWorkReservationResult{Reserved: reserved, Consumed: consumed}, nil
	})
}

func (s SQLJobStore) Complete(ctx context.Context, input *store.JobCompletion) (*model.Job, error) {
	if input == nil || !input.AttemptID.IsValid() || !input.ClaimToken.IsValid() || input.RetryDelay < 0 || input.RetryDelay > 24*time.Hour {
		return nil, store.NewErrInvalidInput("job", "completion", nil)
	}
	return executeSQLTransaction(ctx, s.GetMaster().Begin, rawSQLTransactionPolicy[*model.Job](true, func(_ *model.Job, err error) error { return err }), func(ctx context.Context, tx *sqlxTxWrapper) (*model.Job, error) {
		databaseNow, err := jobDatabaseNow(ctx, tx)
		if err != nil {
			return nil, err
		}
		attempt, err := getFencedAttempt(ctx, tx, input.AttemptID, input.ClaimToken, databaseNow)
		if err != nil {
			return nil, err
		}
		job, err := getJob(ctx, tx, attempt.JobID, true)
		if err != nil {
			return nil, err
		}
		var attemptStatus model.JobAttemptStatus
		var updated *model.Job
		switch input.Kind {
		case store.JobCompletionSucceeded:
			attemptStatus = model.JobAttemptStatusSucceeded
			updated, err = job.Succeed(input.ResultVersion, input.Result, databaseNow)
		case store.JobCompletionRetryableFailure:
			attemptStatus = model.JobAttemptStatusFailed
			if job.AttemptCount < job.MaximumAttempts {
				updated, err = job.Retry(input.PublicErrorCode, databaseNow.Add(input.RetryDelay), databaseNow)
			} else {
				updated, err = job.Fail(input.PublicErrorCode, databaseNow)
			}
		case store.JobCompletionRelinquished:
			attemptStatus = model.JobAttemptStatusIncompatible
			updated, err = job.Relinquish(input.PublicErrorCode, databaseNow.Add(input.RetryDelay), databaseNow)
		case store.JobCompletionPermanentFailure:
			attemptStatus = model.JobAttemptStatusFailed
			updated, err = job.Fail(input.PublicErrorCode, databaseNow)
		case store.JobCompletionCanceled:
			attemptStatus = model.JobAttemptStatusCanceled
			updated, err = job.Cancel(input.PublicErrorCode, databaseNow)
		default:
			err = fmt.Errorf("unknown completion kind %q", input.Kind)
		}
		if err != nil {
			return nil, store.NewErrInvalidInput("job", "completion", err)
		}
		completedAttempt, err := attempt.Complete(attemptStatus, input.PublicErrorCode, databaseNow)
		if err != nil {
			return nil, store.NewErrInvalidInput("job_attempt", "completion", err)
		}
		if _, err = tx.Exec(ctx, `UPDATE job_attempts SET status = ?, completed_at = ?, public_error_code = ? WHERE id = ? AND claim_token = ? AND status = 'running'`, string(completedAttempt.Status), completedAttempt.CompletedAt.Time, completedAttempt.PublicErrorCode, completedAttempt.ID.String(), string(completedAttempt.ClaimToken)); err != nil {
			return nil, err
		}
		if err = updateJob(ctx, tx, updated); err != nil {
			return nil, err
		}
		return updated, nil
	})
}

func (s SQLJobStore) Get(ctx context.Context, id model.JobID) (*model.Job, error) {
	return getJob(ctx, s.GetMaster(), id, false)
}

func (s SQLJobStore) ListAttempts(ctx context.Context, jobID model.JobID) ([]model.JobAttempt, error) {
	if !jobID.IsValid() {
		return nil, store.NewErrInvalidInput("job", "id", nil)
	}
	var rows []jobAttemptRow
	if err := s.GetMaster().Select(ctx, &rows, `SELECT `+jobAttemptColumns+` FROM job_attempts WHERE job_id = ? ORDER BY number`, jobID.String()); err != nil {
		return nil, err
	}
	result := make([]model.JobAttempt, 0, len(rows))
	for _, row := range rows {
		attempt, err := row.model()
		if err != nil {
			return nil, err
		}
		result = append(result, *attempt)
	}
	return result, nil
}

func (s SQLJobStore) List(ctx context.Context, options store.JobListOptions) ([]*model.Job, error) {
	if options.Limit < 1 || options.Limit > 200 || len(options.Types) == 0 ||
		(!options.BeforeID.IsZero() && (!options.BeforeID.IsValid() || options.BeforeCreatedAt.IsZero())) {
		return nil, store.NewErrInvalidInput("job", "list", nil)
	}
	types := make([]string, len(options.Types))
	for index, jobType := range options.Types {
		types[index] = string(jobType)
	}
	arguments := []any{pq.Array(types)}
	query := `SELECT ` + jobColumns + ` FROM jobs WHERE type = ANY(?)`
	if len(options.Statuses) > 0 {
		statuses := make([]string, len(options.Statuses))
		for index, status := range options.Statuses {
			statuses[index] = string(status)
		}
		query += ` AND status = ANY(?)`
		arguments = append(arguments, pq.Array(statuses))
	}
	if !options.BeforeID.IsZero() {
		query += ` AND (created_at, id) < (?, ?)`
		arguments = append(arguments, options.BeforeCreatedAt, options.BeforeID.String())
	}
	query += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	arguments = append(arguments, options.Limit)
	var rows []jobRow
	if err := s.GetMaster().Select(ctx, &rows, query, arguments...); err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	result := make([]*model.Job, 0, len(rows))
	for _, row := range rows {
		job, err := row.model()
		if err != nil {
			return nil, err
		}
		result = append(result, job)
	}
	return result, nil
}

func (s SQLJobStore) ListAttemptsPage(ctx context.Context, options store.JobAttemptListOptions) ([]model.JobAttempt, error) {
	if !options.JobID.IsValid() || options.BeforeNumber < 0 || options.Limit < 1 || options.Limit > 200 {
		return nil, store.NewErrInvalidInput("job_attempt", "list", nil)
	}
	query := `SELECT ` + jobAttemptColumns + ` FROM job_attempts WHERE job_id = ?`
	arguments := []any{options.JobID.String()}
	if options.BeforeNumber > 0 {
		query += ` AND number < ?`
		arguments = append(arguments, options.BeforeNumber)
	}
	query += ` ORDER BY number DESC LIMIT ?`
	arguments = append(arguments, options.Limit)
	var rows []jobAttemptRow
	if err := s.GetMaster().Select(ctx, &rows, query, arguments...); err != nil {
		return nil, fmt.Errorf("list job attempts: %w", err)
	}
	result := make([]model.JobAttempt, 0, len(rows))
	for _, row := range rows {
		attempt, err := row.model()
		if err != nil {
			return nil, err
		}
		result = append(result, *attempt)
	}
	return result, nil
}

func (s SQLJobStore) CancellationRequested(ctx context.Context, attemptID model.JobAttemptID, token model.JobClaimToken) (bool, error) {
	if !attemptID.IsValid() || !token.IsValid() {
		return false, store.NewErrInvalidInput("job_attempt", "observe_cancellation", nil)
	}
	return executeSQLTransaction(ctx, s.GetMaster().Begin, rawSQLTransactionPolicy[bool](false, nil), func(ctx context.Context, tx *sqlxTxWrapper) (bool, error) {
		now, err := jobDatabaseNow(ctx, tx)
		if err != nil {
			return false, err
		}
		attempt, err := getFencedAttempt(ctx, tx, attemptID, token, now)
		if err != nil {
			return false, err
		}
		job, err := getJob(ctx, tx, attempt.JobID, false)
		if err != nil {
			return false, err
		}
		return job.Status == model.JobStatusCancelRequested, nil
	})
}

func (s SQLJobStore) CancelWithAudit(ctx context.Context, input *store.JobMutation) (*model.Job, error) {
	return s.mutateOperatorJob(ctx, input, "cancel", func(job *model.Job, at time.Time) (*model.Job, error) { return job.RequestCancellation(at) })
}

func (s SQLJobStore) RetryWithAudit(ctx context.Context, input *store.JobMutation) (*model.Job, error) {
	return s.mutateOperatorJob(ctx, input, "retry", func(job *model.Job, at time.Time) (*model.Job, error) { return job.ExplicitRetry(at) })
}

func (s SQLJobStore) mutateOperatorJob(ctx context.Context, input *store.JobMutation, operation string, mutate func(*model.Job, time.Time) (*model.Job, error)) (*model.Job, error) {
	if input == nil || !input.ID.IsValid() || input.ExpectedRevision <= 0 || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("job", operation, nil)
	}
	return executeSQLTransaction(ctx, s.GetMaster().Begin, rawSQLTransactionPolicy[*model.Job](true, func(_ *model.Job, err error) error { return err }), func(ctx context.Context, tx *sqlxTxWrapper) (*model.Job, error) {
		databaseNow, err := jobDatabaseNow(ctx, tx)
		if err != nil {
			return nil, err
		}
		job, err := getJob(ctx, tx, input.ID, true)
		if err != nil {
			return nil, err
		}
		if job.Revision != input.ExpectedRevision {
			return nil, store.NewErrConflict("job", "job_changed", nil)
		}
		updated, err := mutate(job, databaseNow)
		if err != nil {
			return nil, store.NewErrConflict("job", operation+"_unsupported", err)
		}
		if err = updateJob(ctx, tx, updated); err != nil {
			return nil, err
		}
		result, err := model.EncodeAuditData(map[string]any{"job_id": updated.ID.String(), "type": string(updated.Type), "status": string(updated.Status)})
		if err != nil {
			return nil, err
		}
		if _, err = completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", result, input.AuditAt); err != nil {
			return nil, err
		}
		return updated, nil
	})
}

func (s SQLJobStore) DeleteTerminalHistory(ctx context.Context, input *store.JobHistoryCleanup) (*store.JobHistoryCleanupResult, error) {
	if input == nil || !input.ExcludeJobID.IsValid() || input.Limit < 1 || input.Limit > 200 || len(input.Policies) == 0 || (input.AfterCompletedAt.IsZero() != input.AfterJobID.IsZero()) || (!input.AfterJobID.IsZero() && !input.AfterJobID.IsValid()) {
		return nil, store.NewErrInvalidInput("job", "history_cleanup", nil)
	}
	outcome, err := executeSQLTransaction(ctx, s.GetMaster().Begin, jobHistorySQLTransactionPolicy(), func(ctx context.Context, tx *sqlxTxWrapper) (*jobHistoryCleanupOutcome, error) {
		databaseNow, err := jobDatabaseNow(ctx, tx)
		if err != nil {
			return nil, err
		}
		conditions := make([]string, 0, len(input.Policies))
		arguments := []any{input.ExcludeJobID.String()}
		if !input.AfterCompletedAt.IsZero() {
			arguments = append(arguments, model.TimeUTC(input.AfterCompletedAt), model.TimeUTC(input.AfterCompletedAt), input.AfterJobID.String())
		}
		seenTypes := make(map[model.JobType]struct{}, len(input.Policies))
		for _, policy := range input.Policies {
			if _, exists := seenTypes[policy.Type]; exists || policy.SucceededCanceledAge <= 0 || policy.FailedAge < policy.SucceededCanceledAge {
				return nil, store.NewErrInvalidInput("job", "retention_policy", nil)
			}
			seenTypes[policy.Type] = struct{}{}
			conditions = append(conditions, `(type = ? AND ((status IN ('succeeded', 'canceled') AND completed_at <= ?) OR (status = 'failed' AND completed_at <= ?)))`)
			arguments = append(arguments, string(policy.Type), databaseNow.Add(-policy.SucceededCanceledAge), databaseNow.Add(-policy.FailedAge))
		}
		query := `SELECT id, completed_at FROM jobs WHERE id <> ? AND status IN ('succeeded', 'failed', 'canceled') AND completed_at IS NOT NULL
			AND id <> COALESCE((SELECT active_rekey_job_id FROM mail_key_state WHERE singleton = TRUE), '')`
		if !input.AfterCompletedAt.IsZero() {
			query += ` AND (completed_at > ? OR (completed_at = ? AND id > ?))`
		}
		query += ` AND (` + strings.Join(conditions, ` OR `) + `) ORDER BY completed_at, id FOR UPDATE SKIP LOCKED LIMIT ?`
		arguments = append(arguments, input.Limit)
		var rows []struct {
			ID          string    `db:"id"`
			CompletedAt time.Time `db:"completed_at"`
		}
		if err = tx.Select(ctx, &rows, query, arguments...); err != nil {
			return nil, fmt.Errorf("select job history cleanup page: %w", err)
		}
		result := &store.JobHistoryCleanupResult{Done: len(rows) < input.Limit}
		if len(rows) == 0 {
			return &jobHistoryCleanupOutcome{result: result, empty: true}, nil
		}
		ids := make([]string, len(rows))
		for index, row := range rows {
			ids[index] = row.ID
		}
		if _, err = tx.Exec(ctx, `DELETE FROM job_attempts WHERE job_id = ANY(?)`, pq.Array(ids)); err != nil {
			return nil, fmt.Errorf("delete retained job attempts: %w", err)
		}
		deleted, err := tx.Exec(ctx, `DELETE FROM jobs WHERE id = ANY(?) AND id <> ? AND status IN ('succeeded', 'failed', 'canceled')
			AND id <> COALESCE((SELECT active_rekey_job_id FROM mail_key_state WHERE singleton = TRUE), '')`, pq.Array(ids), input.ExcludeJobID.String())
		if err != nil {
			return nil, fmt.Errorf("delete retained jobs: %w", err)
		}
		result.Deleted, err = deleted.RowsAffected()
		if err != nil {
			return nil, fmt.Errorf("read deleted job count: %w", err)
		}
		last := rows[len(rows)-1]
		lastID, err := parsePersistedID("job", "id", last.ID, parseJobID)
		if err != nil {
			return nil, err
		}
		result.LastCompletedAt = last.CompletedAt.UTC()
		result.LastJobID = lastID
		return &jobHistoryCleanupOutcome{result: result}, nil
	})
	if err != nil {
		return nil, err
	}
	return outcome.result, nil
}

func getFencedAttempt(ctx context.Context, tx *sqlxTxWrapper, id model.JobAttemptID, token model.JobClaimToken, at time.Time) (*model.JobAttempt, error) {
	var row jobAttemptRow
	if err := tx.Get(ctx, &row, `SELECT `+jobAttemptColumns+` FROM job_attempts WHERE id = ? FOR UPDATE`, id.String()); err != nil {
		return nil, translateError("job_attempt", id.String(), err)
	}
	attempt, err := row.model()
	if err != nil {
		return nil, err
	}
	if attempt.ClaimToken != token || attempt.Status != model.JobAttemptStatusRunning || !model.TimeUTC(at).Before(attempt.LeaseExpiresAt) {
		return nil, store.NewErrConflict("job_attempt", "claim_lost", nil)
	}
	return attempt, nil
}

func jobDatabaseNow(ctx context.Context, tx *sqlxTxWrapper) (time.Time, error) {
	var databaseNow time.Time
	if err := tx.Get(ctx, &databaseNow, `SELECT CURRENT_TIMESTAMP`); err != nil {
		return time.Time{}, fmt.Errorf("read job database time: %w", err)
	}
	return databaseNow.UTC(), nil
}

func getJob(ctx context.Context, executor sqlxExecutor, id model.JobID, lock bool) (*model.Job, error) {
	if !id.IsValid() {
		return nil, store.NewErrInvalidInput("job", "id", nil)
	}
	query := `SELECT ` + jobColumns + ` FROM jobs WHERE id = ?`
	if lock {
		query += ` FOR UPDATE`
	}
	var row jobRow
	if err := executor.Get(ctx, &row, query, id.String()); err != nil {
		return nil, translateError("job", id.String(), err)
	}
	return row.model()
}

func updateJob(ctx context.Context, tx *sqlxTxWrapper, job *model.Job) error {
	var startedAt any
	if job.StartedAt.Valid {
		startedAt = job.StartedAt.Time
	}
	var completedAt any
	if job.CompletedAt.Valid {
		completedAt = job.CompletedAt.Time
	}
	var resultVersion any
	if job.ResultVersion > 0 {
		resultVersion = job.ResultVersion
	}
	var checkpointVersion any
	if job.CheckpointVersion > 0 {
		checkpointVersion = job.CheckpointVersion
	}
	var progressCurrent, progressTotal, progressStage any
	if job.Progress != nil {
		progressCurrent, progressTotal, progressStage = job.Progress.Current, job.Progress.Total, job.Progress.Stage
	}
	result, err := tx.Exec(ctx, `UPDATE jobs SET status = ?, updated_at = ?, available_at = ?, started_at = ?, completed_at = ?, checkpoint_version = ?, checkpoint = ?, result_version = ?, result = ?, public_error_code = ?, attempt_count = ?, maximum_attempts = ?, work_reserved = ?, progress_current = ?, progress_total = ?, progress_stage = ?, revision = ? WHERE id = ? AND revision = ?`, string(job.Status), job.UpdatedAt, job.AvailableAt, startedAt, completedAt, checkpointVersion, nullableJSON(job.Checkpoint), resultVersion, nullableJSON(job.Result), job.PublicErrorCode, job.AttemptCount, job.MaximumAttempts, job.WorkReserved, progressCurrent, progressTotal, progressStage, job.Revision, job.ID.String(), job.Revision-1)
	if err != nil {
		return fmt.Errorf("update job: %w", translateError("job", job.ID.String(), err))
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read job affected rows: %w", err)
	}
	if affected == 0 {
		return store.NewErrConflict("job", "job_changed", nil)
	}
	return nil
}

func (r jobRow) model() (*model.Job, error) {
	id, err := parsePersistedID("job", "id", r.ID, parseJobID)
	if err != nil {
		return nil, err
	}
	job := &model.Job{ID: id, Type: model.JobType(r.Type), Status: model.JobStatus(r.Status), CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(), AvailableAt: r.AvailableAt.UTC(), StartedAt: OptionalTimeFromNullTime(r.StartedAt), CompletedAt: OptionalTimeFromNullTime(r.CompletedAt), CommandVersion: r.CommandVersion, Command: append(json.RawMessage(nil), r.Command...), Checkpoint: append(json.RawMessage(nil), r.Checkpoint...), PublicErrorCode: r.PublicErrorCode, DedupeKey: r.DedupeKey, DedupePolicy: model.JobDedupePolicy(r.DedupePolicy), AttemptCount: r.AttemptCount, MaximumAttempts: r.MaximumAttempts, WorkReserved: r.WorkReserved, Revision: r.Revision}
	if r.CheckpointVersion.Valid {
		job.CheckpointVersion = int(r.CheckpointVersion.Int64)
	}
	if r.ResultVersion.Valid {
		job.ResultVersion = int(r.ResultVersion.Int64)
		job.Result = append(json.RawMessage(nil), r.Result...)
	}
	if r.ProgressCurrent.Valid && r.ProgressTotal.Valid && r.ProgressStage.Valid {
		job.Progress = &model.JobProgress{Current: r.ProgressCurrent.Int64, Total: r.ProgressTotal.Int64, Stage: r.ProgressStage.String}
	}
	if err := validatePersistedModel("job", job); err != nil {
		return nil, err
	}
	return job, nil
}

func (r jobAttemptRow) model() (*model.JobAttempt, error) {
	id, err := parsePersistedID("job_attempt", "id", r.ID, parseJobAttemptID)
	if err != nil {
		return nil, err
	}
	jobID, err := parsePersistedID("job_attempt", "job_id", r.JobID, parseJobID)
	if err != nil {
		return nil, err
	}
	attempt := &model.JobAttempt{ID: id, JobID: jobID, Number: r.Number, Status: model.JobAttemptStatus(r.Status), NodeID: r.NodeID, ClaimToken: model.JobClaimToken(r.ClaimToken), StartedAt: r.StartedAt.UTC(), HeartbeatAt: r.HeartbeatAt.UTC(), LeaseExpiresAt: r.LeaseExpiresAt.UTC(), CompletedAt: OptionalTimeFromNullTime(r.CompletedAt), PublicErrorCode: r.PublicErrorCode}
	if err := validatePersistedModel("job_attempt", attempt); err != nil {
		return nil, err
	}
	return attempt, nil
}

func parseJobID(value string) (model.JobID, error) {
	id := model.JobID(value)
	if !id.IsValid() {
		return "", fmt.Errorf("invalid job id")
	}
	return id, nil
}

func parseJobAttemptID(value string) (model.JobAttemptID, error) {
	id := model.JobAttemptID(value)
	if !id.IsValid() {
		return "", fmt.Errorf("invalid job attempt id")
	}
	return id, nil
}

var _ store.JobStore = (*SQLJobStore)(nil)
