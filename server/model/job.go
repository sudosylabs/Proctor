// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

const JobMaximumDocumentBytes = 64 << 10

type JobID string
type JobAttemptID string
type JobClaimToken string
type JobType string
type JobStatus string
type JobAttemptStatus string
type JobDedupePolicy string

const (
	JobTypeProfilePictureGenerateDefault JobType = "profile_picture.generate_default"
	JobTypeProfilePictureReconcile       JobType = "profile_picture.reconcile_defaults"
	JobTypeFilePurgeExpiredContent       JobType = "file.purge_expired_content"
	JobTypeCleanup                       JobType = "job.cleanup"
	JobTypeCommandOutcomeCleanup         JobType = "command_outcome.cleanup"
	JobTypeExamSittingLifecycle          JobType = "exam_sitting.lifecycle"
	JobTypeExamSittingLifecycleRecovery  JobType = "exam_sitting.lifecycle_recovery"
	JobTypeExamSittingSealing            JobType = "exam_sitting.sealing"
	JobTypeMailDeliver                   JobType = "mail.deliver"

	JobStatusQueued          JobStatus = "queued"
	JobStatusRunning         JobStatus = "running"
	JobStatusCancelRequested JobStatus = "cancel_requested"
	JobStatusSucceeded       JobStatus = "succeeded"
	JobStatusFailed          JobStatus = "failed"
	JobStatusCanceled        JobStatus = "canceled"

	JobAttemptStatusRunning      JobAttemptStatus = "running"
	JobAttemptStatusSucceeded    JobAttemptStatus = "succeeded"
	JobAttemptStatusFailed       JobAttemptStatus = "failed"
	JobAttemptStatusCanceled     JobAttemptStatus = "canceled"
	JobAttemptStatusLeaseExpired JobAttemptStatus = "lease_expired"

	// JobDedupeActive permits a new logical occurrence after the prior Job is
	// terminal. JobDedupePermanent reserves the key across every lifecycle state.
	JobDedupeActive    JobDedupePolicy = "active"
	JobDedupePermanent JobDedupePolicy = "permanent"
)

type JobProgress struct {
	Current int64
	Total   int64
	Stage   string
}

type Job struct {
	ID                JobID
	Type              JobType
	Status            JobStatus
	CreatedAt         time.Time
	UpdatedAt         time.Time
	AvailableAt       time.Time
	StartedAt         OptionalTime
	CompletedAt       OptionalTime
	CommandVersion    int
	Command           json.RawMessage
	CheckpointVersion int
	Checkpoint        json.RawMessage
	ResultVersion     int
	Result            json.RawMessage
	PublicErrorCode   string
	DedupeKey         string
	DedupePolicy      JobDedupePolicy
	AttemptCount      int
	MaximumAttempts   int
	WorkReserved      int
	Progress          *JobProgress
	Revision          int64
}

type JobAttempt struct {
	ID              JobAttemptID
	JobID           JobID
	Number          int
	Status          JobAttemptStatus
	NodeID          string
	ClaimToken      JobClaimToken
	StartedAt       time.Time
	HeartbeatAt     time.Time
	LeaseExpiresAt  time.Time
	CompletedAt     OptionalTime
	PublicErrorCode string
}

func NewJob(id JobID, jobType JobType, commandVersion int, command json.RawMessage, dedupeKey string, createdAt, availableAt time.Time, maximumAttempts int) (*Job, error) {
	return NewJobWithDedupePolicy(id, jobType, commandVersion, command, dedupeKey, JobDedupeActive, createdAt, availableAt, maximumAttempts)
}

func NewJobWithDedupePolicy(id JobID, jobType JobType, commandVersion int, command json.RawMessage, dedupeKey string, dedupePolicy JobDedupePolicy, createdAt, availableAt time.Time, maximumAttempts int) (*Job, error) {
	job := &Job{ID: id, Type: jobType, Status: JobStatusQueued, CreatedAt: TimeUTC(createdAt), UpdatedAt: TimeUTC(createdAt), AvailableAt: TimeUTC(availableAt), CommandVersion: commandVersion, Command: cloneJSON(command), DedupeKey: strings.TrimSpace(dedupeKey), DedupePolicy: dedupePolicy, MaximumAttempts: maximumAttempts, Revision: 1}
	if err := job.Validate(); err != nil {
		return nil, err
	}
	return job, nil
}

func (j *Job) Validate() error {
	if j == nil || !j.ID.IsValid() || !validJobType(j.Type) || !validJobStatus(j.Status) || j.CreatedAt.IsZero() || j.UpdatedAt.Before(j.CreatedAt) || j.AvailableAt.IsZero() || j.CommandVersion <= 0 || !validJobDocument(j.Command, false) || len(j.DedupeKey) == 0 || len(j.DedupeKey) > 255 || !validJobDedupePolicy(j.DedupePolicy) || j.AttemptCount < 0 || j.MaximumAttempts <= 0 || j.AttemptCount > j.MaximumAttempts || j.WorkReserved < 0 || j.Revision <= 0 {
		return fmt.Errorf("model: invalid job")
	}
	if !validJobDocument(j.Checkpoint, true) || (len(j.Checkpoint) > 0 && j.CheckpointVersion <= 0) || (len(j.Checkpoint) == 0 && j.CheckpointVersion != 0) || !validJobDocument(j.Result, true) || (len(j.Result) > 0 && j.ResultVersion <= 0) || (len(j.Result) == 0 && j.ResultVersion != 0) || !validPublicJobCode(j.PublicErrorCode) || !validJobProgress(j.Progress) {
		return fmt.Errorf("model: invalid job result state")
	}
	terminal := j.Status == JobStatusSucceeded || j.Status == JobStatusFailed || j.Status == JobStatusCanceled
	if terminal != j.CompletedAt.Valid || (j.Status == JobStatusRunning && j.AttemptCount == 0) {
		return fmt.Errorf("model: invalid job lifecycle")
	}
	return nil
}

func validJobDedupePolicy(policy JobDedupePolicy) bool {
	return policy == JobDedupeActive || policy == JobDedupePermanent
}

func (j *Job) Start(at time.Time) (*Job, error) {
	if j == nil || j.Status != JobStatusQueued || j.AttemptCount >= j.MaximumAttempts || TimeUTC(at).Before(j.AvailableAt) {
		return nil, fmt.Errorf("model: job cannot start")
	}
	result := *j
	result.Status = JobStatusRunning
	result.UpdatedAt = TimeUTC(at)
	if !result.StartedAt.Valid {
		result.StartedAt = OptionalTimeFrom(at)
	}
	result.AttemptCount++
	result.Revision++
	return &result, result.Validate()
}

func (j *Job) UpdateProgress(progress *JobProgress, checkpointVersion int, checkpoint json.RawMessage, at time.Time) (*Job, error) {
	if j == nil || (j.Status != JobStatusRunning && j.Status != JobStatusCancelRequested) || !validJobProgress(progress) || !validJobDocument(checkpoint, true) || (len(checkpoint) > 0) != (checkpointVersion > 0) || TimeUTC(at).Before(j.UpdatedAt) {
		return nil, fmt.Errorf("model: job progress cannot be updated")
	}
	result := *j
	if progress != nil {
		copyProgress := *progress
		result.Progress = &copyProgress
	} else {
		result.Progress = nil
	}
	result.Checkpoint = cloneJSON(checkpoint)
	result.CheckpointVersion = checkpointVersion
	result.UpdatedAt = TimeUTC(at)
	result.Revision++
	return &result, result.Validate()
}

func (j *Job) Succeed(resultVersion int, document json.RawMessage, at time.Time) (*Job, error) {
	if j == nil || (j.Status != JobStatusRunning && j.Status != JobStatusCancelRequested) || resultVersion <= 0 || !validJobDocument(document, false) || TimeUTC(at).Before(j.UpdatedAt) {
		return nil, fmt.Errorf("model: job cannot succeed")
	}
	result := *j
	result.Status = JobStatusSucceeded
	result.ResultVersion = resultVersion
	result.Result = cloneJSON(document)
	result.PublicErrorCode = ""
	result.CompletedAt = OptionalTimeFrom(at)
	result.UpdatedAt = TimeUTC(at)
	result.Revision++
	return &result, result.Validate()
}

func (j *Job) Retry(publicErrorCode string, availableAt, at time.Time) (*Job, error) {
	if j == nil || j.Status != JobStatusRunning || j.AttemptCount >= j.MaximumAttempts || !validPublicJobCode(publicErrorCode) || !TimeUTC(availableAt).After(TimeUTC(at)) || TimeUTC(at).Before(j.UpdatedAt) {
		return nil, fmt.Errorf("model: job cannot retry")
	}
	result := *j
	result.Status = JobStatusQueued
	result.AvailableAt = TimeUTC(availableAt)
	result.UpdatedAt = TimeUTC(at)
	result.PublicErrorCode = strings.TrimSpace(publicErrorCode)
	result.Revision++
	return &result, result.Validate()
}

func (j *Job) Fail(publicErrorCode string, at time.Time) (*Job, error) {
	if j == nil || (j.Status != JobStatusRunning && j.Status != JobStatusCancelRequested) || !validPublicJobCode(publicErrorCode) || publicErrorCode == "" || TimeUTC(at).Before(j.UpdatedAt) {
		return nil, fmt.Errorf("model: job cannot fail")
	}
	result := *j
	result.Status = JobStatusFailed
	result.UpdatedAt = TimeUTC(at)
	result.CompletedAt = OptionalTimeFrom(at)
	result.PublicErrorCode = strings.TrimSpace(publicErrorCode)
	result.Revision++
	return &result, result.Validate()
}

func (j *Job) Cancel(publicErrorCode string, at time.Time) (*Job, error) {
	if j == nil || (j.Status != JobStatusRunning && j.Status != JobStatusCancelRequested && j.Status != JobStatusQueued) || !validPublicJobCode(publicErrorCode) || TimeUTC(at).Before(j.UpdatedAt) {
		return nil, fmt.Errorf("model: job cannot cancel")
	}
	result := *j
	result.Status = JobStatusCanceled
	result.UpdatedAt = TimeUTC(at)
	result.CompletedAt = OptionalTimeFrom(at)
	result.PublicErrorCode = strings.TrimSpace(publicErrorCode)
	result.Revision++
	return &result, result.Validate()
}

// RequestCancellation immediately cancels queued work and durably marks
// running work for cooperative cancellation by its fenced worker.
func (j *Job) RequestCancellation(at time.Time) (*Job, error) {
	if j == nil || (j.Status != JobStatusQueued && j.Status != JobStatusRunning) || TimeUTC(at).Before(j.UpdatedAt) {
		return nil, fmt.Errorf("model: job cancellation cannot be requested")
	}
	if j.Status == JobStatusQueued {
		return j.Cancel("job.canceled", at)
	}
	result := *j
	result.Status = JobStatusCancelRequested
	result.UpdatedAt = TimeUTC(at)
	result.Revision++
	return &result, result.Validate()
}

// ExplicitRetry requeues a failed Job without altering its prior Attempts. One
// additional claim is admitted; policy decides which terminal Jobs may call it.
func (j *Job) ExplicitRetry(at time.Time) (*Job, error) {
	if j == nil || (j.Status != JobStatusFailed && j.Status != JobStatusCanceled) || TimeUTC(at).Before(j.UpdatedAt) {
		return nil, fmt.Errorf("model: job cannot be explicitly retried")
	}
	result := *j
	result.Status = JobStatusQueued
	result.AvailableAt = TimeUTC(at)
	result.UpdatedAt = TimeUTC(at)
	result.CompletedAt = OptionalTime{}
	result.MaximumAttempts = result.AttemptCount + 1
	result.Revision++
	return &result, result.Validate()
}

func NewJobAttempt(id JobAttemptID, jobID JobID, number int, nodeID string, token JobClaimToken, startedAt, leaseExpiresAt time.Time) (*JobAttempt, error) {
	attempt := &JobAttempt{ID: id, JobID: jobID, Number: number, Status: JobAttemptStatusRunning, NodeID: strings.TrimSpace(nodeID), ClaimToken: token, StartedAt: TimeUTC(startedAt), HeartbeatAt: TimeUTC(startedAt), LeaseExpiresAt: TimeUTC(leaseExpiresAt)}
	if err := attempt.Validate(); err != nil {
		return nil, err
	}
	return attempt, nil
}

func (a *JobAttempt) Validate() error {
	if a == nil || !a.ID.IsValid() || !a.JobID.IsValid() || a.Number <= 0 || !validJobAttemptStatus(a.Status) || a.NodeID == "" || len(a.NodeID) > 255 || !a.ClaimToken.IsValid() || a.StartedAt.IsZero() || a.HeartbeatAt.Before(a.StartedAt) || !a.LeaseExpiresAt.After(a.HeartbeatAt) || !validPublicJobCode(a.PublicErrorCode) {
		return fmt.Errorf("model: invalid job attempt")
	}
	if (a.Status == JobAttemptStatusRunning) == a.CompletedAt.Valid {
		return fmt.Errorf("model: invalid job attempt lifecycle")
	}
	return nil
}

func (a *JobAttempt) Heartbeat(at, leaseExpiresAt time.Time) (*JobAttempt, error) {
	if a == nil || a.Status != JobAttemptStatusRunning || TimeUTC(at).Before(a.HeartbeatAt) || !TimeUTC(leaseExpiresAt).After(TimeUTC(at)) {
		return nil, fmt.Errorf("model: job attempt cannot heartbeat")
	}
	result := *a
	result.HeartbeatAt = TimeUTC(at)
	result.LeaseExpiresAt = TimeUTC(leaseExpiresAt)
	return &result, result.Validate()
}

func (a *JobAttempt) Complete(status JobAttemptStatus, publicErrorCode string, at time.Time) (*JobAttempt, error) {
	if a == nil || a.Status != JobAttemptStatusRunning || status == JobAttemptStatusRunning || !validJobAttemptStatus(status) || TimeUTC(at).Before(a.HeartbeatAt) {
		return nil, fmt.Errorf("model: job attempt cannot complete")
	}
	result := *a
	result.Status = status
	result.PublicErrorCode = strings.TrimSpace(publicErrorCode)
	result.CompletedAt = OptionalTimeFrom(at)
	return &result, result.Validate()
}

func NewJobClaimToken() (JobClaimToken, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate job claim token: %w", err)
	}
	return JobClaimToken(hex.EncodeToString(value)), nil
}

func NewJobID() JobID                  { return JobID(NewId()) }
func NewJobAttemptID() JobAttemptID    { return JobAttemptID(NewId()) }
func (id JobID) IsZero() bool          { return id == "" }
func (id JobAttemptID) IsZero() bool   { return id == "" }
func (id JobID) IsValid() bool         { return IsValidId(string(id)) }
func (id JobAttemptID) IsValid() bool  { return IsValidId(string(id)) }
func (id JobID) String() string        { return string(id) }
func (id JobAttemptID) String() string { return string(id) }
func (t JobClaimToken) IsValid() bool {
	if len(t) != 64 {
		return false
	}
	_, err := hex.DecodeString(string(t))
	return err == nil
}

var jobSafeCode = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,127}$`)
var jobSafeStage = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)

func validJobType(value JobType) bool {
	return value == JobTypeProfilePictureGenerateDefault || value == JobTypeProfilePictureReconcile || value == JobTypeFilePurgeExpiredContent || value == JobTypeCleanup || value == JobTypeCommandOutcomeCleanup || value == JobTypeExamSittingLifecycle || value == JobTypeExamSittingLifecycleRecovery || value == JobTypeExamSittingSealing || value == JobTypeMailDeliver
}
func validJobStatus(value JobStatus) bool {
	return value == JobStatusQueued || value == JobStatusRunning || value == JobStatusCancelRequested || value == JobStatusSucceeded || value == JobStatusFailed || value == JobStatusCanceled
}
func validJobAttemptStatus(value JobAttemptStatus) bool {
	return value == JobAttemptStatusRunning || value == JobAttemptStatusSucceeded || value == JobAttemptStatusFailed || value == JobAttemptStatusCanceled || value == JobAttemptStatusLeaseExpired
}
func validJobDocument(value json.RawMessage, optional bool) bool {
	if len(value) == 0 {
		return optional
	}
	return len(value) <= JobMaximumDocumentBytes && json.Valid(value)
}
func validPublicJobCode(value string) bool {
	return value == "" || jobSafeCode.MatchString(value)
}
func validJobProgress(value *JobProgress) bool {
	return value == nil || (value.Current >= 0 && value.Total > 0 && value.Current <= value.Total && jobSafeStage.MatchString(value.Stage))
}
func cloneJSON(value json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), value...)
}
