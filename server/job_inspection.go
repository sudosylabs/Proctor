// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package server

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/sqlstore"
)

const (
	// JobListDefaultLimit is the page size ListJobs uses when none is given.
	JobListDefaultLimit = 50
	// JobListMaximumLimit bounds one ListJobs page.
	JobListMaximumLimit = 200
	// JobAttemptDisplayLimit bounds the most recent Attempts ShowJob returns.
	JobAttemptDisplayLimit = 20
)

// ErrJobNotFound reports that no Job has the requested ID.
var ErrJobNotFound = errors.New("job not found")

// JobQuery filters a host Job listing. Empty Types or States match every
// value; Limit zero means JobListDefaultLimit.
type JobQuery struct {
	Types  []string
	States []string
	Limit  int
}

// JobList is one bounded page of Jobs, newest first. Its JSON field names are
// part of the public CLI contract.
type JobList struct {
	Jobs    []JobSummary `json:"jobs"`
	HasMore bool         `json:"has_more"`
}

// JobSummary is the safe host projection of a Job. It deliberately omits the
// command, checkpoint, result, and deduplication key, which can carry user
// identifiers or content.
type JobSummary struct {
	ID              string       `json:"id"`
	Type            string       `json:"type"`
	State           string       `json:"state"`
	AttemptCount    int          `json:"attempt_count"`
	MaximumAttempts int          `json:"maximum_attempts"`
	LastErrorCode   string       `json:"last_error_code,omitempty"`
	CreatedAt       time.Time    `json:"created_at"`
	UpdatedAt       time.Time    `json:"updated_at"`
	AvailableAt     time.Time    `json:"available_at"`
	StartedAt       *time.Time   `json:"started_at,omitempty"`
	CompletedAt     *time.Time   `json:"completed_at,omitempty"`
	Progress        *JobProgress `json:"progress,omitempty"`
}

// JobProgress is a Job's bounded progress report.
type JobProgress struct {
	Current int64  `json:"current"`
	Total   int64  `json:"total"`
	Stage   string `json:"stage"`
}

// JobDetail is one Job with its most recent Attempts, newest first.
type JobDetail struct {
	JobSummary
	Attempts     []JobAttemptSummary `json:"attempts"`
	MoreAttempts bool                `json:"more_attempts"`
}

// JobAttemptSummary is the safe host projection of a Job Attempt. It omits
// the claim token.
type JobAttemptSummary struct {
	Number         int        `json:"number"`
	State          string     `json:"state"`
	NodeID         string     `json:"node_id"`
	StartedAt      time.Time  `json:"started_at"`
	HeartbeatAt    time.Time  `json:"heartbeat_at"`
	LeaseExpiresAt time.Time  `json:"lease_expires_at"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
	ErrorCode      string     `json:"error_code,omitempty"`
}

// JobTypes returns every Job type a JobQuery accepts.
func JobTypes() []string {
	types := model.JobTypes()
	result := make([]string, 0, len(types))
	for _, jobType := range types {
		result = append(result, string(jobType))
	}
	return result
}

// JobStates returns every Job state a JobQuery accepts, in lifecycle order.
func JobStates() []string {
	statuses := model.JobStatuses()
	result := make([]string, 0, len(statuses))
	for _, status := range statuses {
		result = append(result, string(status))
	}
	return result
}

// ListJobs lists Jobs of every type, operator-visible or internal, newest
// first, through a read-only database session. It never migrates, takes a
// lock, or writes.
func ListJobs(ctx context.Context, configPath string, query JobQuery) (_ JobList, resultErr error) {
	options, err := jobListOptions(query)
	if err != nil {
		return JobList{}, err
	}
	database, closeDatabase, err := openJobDatabase(ctx, configPath)
	if err != nil {
		return JobList{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, closeDatabase()) }()

	limit := options.Limit
	options.Limit++
	records, err := database.Job().List(ctx, options)
	if err != nil {
		return JobList{}, err
	}
	list := JobList{Jobs: []JobSummary{}, HasMore: len(records) > limit}
	for _, record := range records[:min(len(records), limit)] {
		list.Jobs = append(list.Jobs, summarizeJob(record))
	}
	return list, nil
}

// ShowJob returns one Job and its most recent Attempts through a read-only
// database session. It returns ErrJobNotFound for an unknown ID.
func ShowJob(ctx context.Context, configPath, id string) (_ JobDetail, resultErr error) {
	jobID := model.JobID(id)
	if !jobID.IsValid() {
		return JobDetail{}, fmt.Errorf("job ID %q is invalid", id)
	}
	database, closeDatabase, err := openJobDatabase(ctx, configPath)
	if err != nil {
		return JobDetail{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, closeDatabase()) }()

	record, err := database.Job().Get(ctx, jobID)
	if store.IsNotFound(err) {
		return JobDetail{}, fmt.Errorf("%w: %s", ErrJobNotFound, id)
	}
	if err != nil {
		return JobDetail{}, err
	}
	attempts, err := database.Job().ListAttemptsPage(ctx, store.JobAttemptListOptions{JobID: jobID, Limit: JobAttemptDisplayLimit + 1})
	if err != nil {
		return JobDetail{}, err
	}
	detail := JobDetail{
		JobSummary:   summarizeJob(record),
		Attempts:     []JobAttemptSummary{},
		MoreAttempts: len(attempts) > JobAttemptDisplayLimit,
	}
	for _, attempt := range attempts[:min(len(attempts), JobAttemptDisplayLimit)] {
		detail.Attempts = append(detail.Attempts, JobAttemptSummary{
			Number: attempt.Number, State: string(attempt.Status), NodeID: attempt.NodeID,
			StartedAt: attempt.StartedAt, HeartbeatAt: attempt.HeartbeatAt, LeaseExpiresAt: attempt.LeaseExpiresAt,
			CompletedAt: attempt.CompletedAt.Ptr(), ErrorCode: attempt.PublicErrorCode,
		})
	}
	return detail, nil
}

func jobListOptions(query JobQuery) (store.JobListOptions, error) {
	limit := query.Limit
	if limit == 0 {
		limit = JobListDefaultLimit
	}
	if limit < 1 || limit > JobListMaximumLimit {
		return store.JobListOptions{}, fmt.Errorf("job list limit must be between 1 and %d", JobListMaximumLimit)
	}
	options := store.JobListOptions{Types: model.JobTypes(), Limit: limit}
	if len(query.Types) > 0 {
		options.Types = make([]model.JobType, 0, len(query.Types))
		for _, value := range query.Types {
			jobType := model.JobType(value)
			if !jobType.IsValid() {
				return store.JobListOptions{}, fmt.Errorf("job type %q is unknown", value)
			}
			options.Types = append(options.Types, jobType)
		}
	}
	for _, value := range query.States {
		status := model.JobStatus(value)
		if !status.IsValid() {
			return store.JobListOptions{}, fmt.Errorf("job state %q is unknown", value)
		}
		options.Statuses = append(options.Statuses, status)
	}
	return options, nil
}

// openJobDatabase opens a read-only store and refuses a database that has no
// schema yet, which has no Jobs table to read.
func openJobDatabase(ctx context.Context, configPath string) (*sqlstore.SQLStore, func() error, error) {
	database, closeDatabase, err := openReadOnlyDatabase(ctx, configPath)
	if err != nil {
		return nil, nil, err
	}
	version, err := database.GetDBSchemaVersion(ctx)
	if err == nil && version == 0 {
		err = errors.New("database has no schema yet; run `proctor migrate up` or start a node first")
	}
	if err != nil {
		return nil, nil, errors.Join(err, closeDatabase())
	}
	return database, closeDatabase, nil
}

func summarizeJob(record *model.Job) JobSummary {
	summary := JobSummary{
		ID: record.ID.String(), Type: string(record.Type), State: string(record.Status),
		AttemptCount: record.AttemptCount, MaximumAttempts: record.MaximumAttempts, LastErrorCode: record.PublicErrorCode,
		CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt, AvailableAt: record.AvailableAt,
		StartedAt: record.StartedAt.Ptr(), CompletedAt: record.CompletedAt.Ptr(),
	}
	if record.Progress != nil {
		summary.Progress = &JobProgress{Current: record.Progress.Current, Total: record.Progress.Total, Stage: record.Progress.Stage}
	}
	return summary
}
