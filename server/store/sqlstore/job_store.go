// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

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
	AttemptCount      int            `db:"attempt_count"`
	MaximumAttempts   int            `db:"maximum_attempts"`
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

const jobColumns = `id, type, status, created_at, updated_at, available_at, started_at, completed_at, command_version, command, checkpoint_version, checkpoint, result_version, result, public_error_code, dedupe_key, attempt_count, maximum_attempts, progress_current, progress_total, progress_stage, revision`
const jobAttemptColumns = `id, job_id, number, status, node_id, claim_token, started_at, heartbeat_at, lease_expires_at, completed_at, public_error_code`

func newSQLJobStore(sqlStore *SQLStore) store.JobStore { return &SQLJobStore{SQLStore: sqlStore} }

func (s SQLJobStore) Enqueue(ctx context.Context, input *store.JobEnqueue) (*model.Job, bool, error) {
	if input == nil || input.Job == nil {
		return nil, false, store.NewErrInvalidInput("job", "enqueue", nil)
	}
	if err := input.Job.Validate(); err != nil || input.Job.Status != model.JobStatusQueued || input.Job.AttemptCount != 0 {
		return nil, false, store.NewErrInvalidInput("job", "enqueue", err)
	}
	result, err := s.GetMaster().Exec(ctx, `INSERT INTO jobs (`+jobColumns+`) VALUES (?, ?, ?, ?, ?, ?, NULL, NULL, ?, ?, NULL, NULL, NULL, NULL, '', ?, 0, ?, NULL, NULL, NULL, ?) ON CONFLICT (type, dedupe_key) DO NOTHING`, input.Job.ID.String(), string(input.Job.Type), string(input.Job.Status), input.Job.CreatedAt, input.Job.UpdatedAt, input.Job.AvailableAt, input.Job.CommandVersion, input.Job.Command, input.Job.DedupeKey, input.Job.MaximumAttempts, input.Job.Revision)
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
	if err = s.GetMaster().Get(ctx, &row, `SELECT `+jobColumns+` FROM jobs WHERE type = ? AND dedupe_key = ?`, string(input.Job.Type), input.Job.DedupeKey); err != nil {
		return nil, false, translateError("job", input.Job.DedupeKey, err)
	}
	job, err := row.model()
	return job, false, err
}

func (s SQLJobStore) ClaimNext(ctx context.Context, request *store.JobClaimRequest) (*store.JobClaim, error) {
	if request == nil || len(request.Types) == 0 || strings.TrimSpace(request.NodeID) == "" || !request.ClaimToken.IsValid() || request.LeaseDuration <= 0 || request.LeaseDuration > time.Hour {
		return nil, store.NewErrInvalidInput("job", "claim", nil)
	}
	types := make([]string, len(request.Types))
	for index, jobType := range request.Types {
		types[index] = string(jobType)
	}
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin job claim: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
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
	if err = tx.Get(ctx, &row, `SELECT `+jobColumns+` FROM jobs WHERE status = 'queued' AND available_at <= ? AND attempt_count < maximum_attempts AND type = ANY(?) ORDER BY available_at, created_at, id FOR UPDATE SKIP LOCKED LIMIT 1`, databaseNow, pq.Array(types)); err != nil {
		if errors.Is(err, sql.ErrNoRows) && len(expiredJobIDs) > 0 {
			if commitErr := tx.Commit(); commitErr != nil {
				return nil, fmt.Errorf("commit expired job recovery: %w", commitErr)
			}
		}
		return nil, translateError("job", "claimable", err)
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
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit job claim: %w", err)
	}
	return &store.JobClaim{Job: running, Attempt: attempt}, nil
}

func (s SQLJobStore) Heartbeat(ctx context.Context, input *store.JobHeartbeat) (*model.JobAttempt, error) {
	if input == nil || !input.AttemptID.IsValid() || !input.ClaimToken.IsValid() || input.LeaseDuration <= 0 || input.LeaseDuration > time.Hour {
		return nil, store.NewErrInvalidInput("job_attempt", "heartbeat", nil)
	}
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
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
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return heartbeat, nil
}

func (s SQLJobStore) Checkpoint(ctx context.Context, input *store.JobCheckpoint) (*model.Job, error) {
	if input == nil || !input.AttemptID.IsValid() || !input.ClaimToken.IsValid() {
		return nil, store.NewErrInvalidInput("job", "checkpoint", nil)
	}
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
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
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return updated, nil
}

func (s SQLJobStore) Complete(ctx context.Context, input *store.JobCompletion) (*model.Job, error) {
	if input == nil || !input.AttemptID.IsValid() || !input.ClaimToken.IsValid() || input.RetryDelay < 0 || input.RetryDelay > 24*time.Hour {
		return nil, store.NewErrInvalidInput("job", "completion", nil)
	}
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
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
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return updated, nil
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
	result, err := tx.Exec(ctx, `UPDATE jobs SET status = ?, updated_at = ?, available_at = ?, started_at = ?, completed_at = ?, checkpoint_version = ?, checkpoint = ?, result_version = ?, result = ?, public_error_code = ?, attempt_count = ?, progress_current = ?, progress_total = ?, progress_stage = ?, revision = ? WHERE id = ? AND revision = ?`, string(job.Status), job.UpdatedAt, job.AvailableAt, startedAt, completedAt, checkpointVersion, nullableJSON(job.Checkpoint), resultVersion, nullableJSON(job.Result), job.PublicErrorCode, job.AttemptCount, progressCurrent, progressTotal, progressStage, job.Revision, job.ID.String(), job.Revision-1)
	if err != nil {
		return fmt.Errorf("update job: %w", err)
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
	job := &model.Job{ID: model.JobID(r.ID), Type: model.JobType(r.Type), Status: model.JobStatus(r.Status), CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(), AvailableAt: r.AvailableAt.UTC(), StartedAt: OptionalTimeFromNullTime(r.StartedAt), CompletedAt: OptionalTimeFromNullTime(r.CompletedAt), CommandVersion: r.CommandVersion, Command: append(json.RawMessage(nil), r.Command...), Checkpoint: append(json.RawMessage(nil), r.Checkpoint...), PublicErrorCode: r.PublicErrorCode, DedupeKey: r.DedupeKey, AttemptCount: r.AttemptCount, MaximumAttempts: r.MaximumAttempts, Revision: r.Revision}
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
	if err := job.Validate(); err != nil {
		return nil, fmt.Errorf("rehydrate job %q: %w", r.ID, err)
	}
	return job, nil
}

func (r jobAttemptRow) model() (*model.JobAttempt, error) {
	attempt := &model.JobAttempt{ID: model.JobAttemptID(r.ID), JobID: model.JobID(r.JobID), Number: r.Number, Status: model.JobAttemptStatus(r.Status), NodeID: r.NodeID, ClaimToken: model.JobClaimToken(r.ClaimToken), StartedAt: r.StartedAt.UTC(), HeartbeatAt: r.HeartbeatAt.UTC(), LeaseExpiresAt: r.LeaseExpiresAt.UTC(), CompletedAt: OptionalTimeFromNullTime(r.CompletedAt), PublicErrorCode: r.PublicErrorCode}
	if err := attempt.Validate(); err != nil {
		return nil, fmt.Errorf("rehydrate job attempt %q: %w", r.ID, err)
	}
	return attempt, nil
}

var _ store.JobStore = (*SQLJobStore)(nil)
