// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	jobengine "github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type DefaultProfilePictureJobs struct {
	jobs store.JobStore
	wake func()
}

func NewDefaultProfilePictureJobs(jobs store.JobStore) *DefaultProfilePictureJobs {
	return &DefaultProfilePictureJobs{jobs: jobs}
}

func (p *DefaultProfilePictureJobs) SetWake(wake func()) { p.wake = wake }

func PrepareUserDefaultProfilePictureJob(user *model.User, at time.Time) (*model.User, *model.Job, error) {
	if user == nil {
		return nil, nil, errors.New("user is missing")
	}
	candidate := *user
	candidate.ID = ""
	candidate.CreatedAt = time.Time{}
	candidate.UpdatedAt = time.Time{}
	candidate.ArchivedAt = model.OptionalTime{}
	candidate.Revision = 0
	candidate.LastLoginAt = model.OptionalTime{}
	candidate.LastActivityAt = model.OptionalTime{}
	candidate.DisabledAt = model.OptionalTime{}
	candidate.DefaultProfilePictureFileID = ""
	candidate.CustomProfilePictureFileID = ""
	candidate.ProfilePictureChangedAt = model.OptionalTime{}
	candidate.PrepareCreate(model.NewUserID(), at)
	if err := candidate.Validate(); err != nil {
		return nil, nil, err
	}
	command, err := model.EncodeDefaultProfilePictureCommand(model.DefaultProfilePictureCommandV1{UserID: candidate.ID})
	if err != nil {
		return nil, nil, err
	}
	job, err := model.NewJob(
		model.NewJobID(), model.JobTypeProfilePictureGenerateDefault, 1,
		command, candidate.ID.String(), candidate.CreatedAt, candidate.CreatedAt, 8,
	)
	if err != nil {
		return nil, nil, err
	}
	return &candidate, job, nil
}

func (p DefaultProfilePictureJobs) ProposeDefaultProfilePicture(ctx context.Context, userID model.UserID, at time.Time) error {
	command, err := model.EncodeDefaultProfilePictureCommand(model.DefaultProfilePictureCommandV1{UserID: userID})
	if err != nil {
		return err
	}
	job, err := model.NewJob(model.NewJobID(), model.JobTypeProfilePictureGenerateDefault, 1, command, userID.String(), at, at, 8)
	if err != nil {
		return err
	}
	_, _, err = p.jobs.Enqueue(ctx, &store.JobEnqueue{Job: job})
	if err == nil && p.wake != nil {
		p.wake()
	}
	return err
}

type defaultProfilePictureHandler struct {
	generator DefaultProfilePictureGenerator
}

func (h defaultProfilePictureHandler) Run(ctx context.Context, execution jobengine.Execution) jobengine.Outcome {
	if execution.Job == nil {
		return jobengine.PermanentFailure("job.command.invalid", errors.New("job is missing"))
	}
	command, err := model.DecodeDefaultProfilePictureCommand(execution.Job.CommandVersion, execution.Job.Command)
	if err != nil {
		return jobengine.PermanentFailure("job.command.invalid", err)
	}
	entryID, err := h.generator.EnsureDefaultProfilePicture(ctx, command.UserID)
	if err != nil {
		switch h.generator.ClassifyDefaultProfilePictureError(err) {
		case "user.not_found":
			return jobengine.PermanentFailure("user.not_found", err)
		case "job.invariant_failed":
			return jobengine.PermanentFailure("job.invariant_failed", err)
		}
		return jobengine.RetryableFailure("dependency.unavailable", err)
	}
	return defaultProfilePictureJobSucceeded(entryID)
}

type defaultProfilePictureResultV1 struct {
	FileEntryID model.FileEntryID `json:"file_entry_id"`
}

func defaultProfilePictureJobSucceeded(fileEntryID model.FileEntryID) jobengine.Outcome {
	if !fileEntryID.IsValid() {
		return jobengine.Outcome{Kind: jobengine.OutcomeSucceeded, Err: errors.New("default profile-picture result has invalid file entry ID")}
	}
	document, err := json.Marshal(defaultProfilePictureResultV1{FileEntryID: fileEntryID})
	return jobengine.Outcome{Kind: jobengine.OutcomeSucceeded, ResultVersion: 1, Result: document, Err: err}
}

func defaultProfilePictureDescriptor(handler jobengine.Handler) jobengine.Descriptor {
	return jobengine.Descriptor{Type: model.JobTypeProfilePictureGenerateDefault, CommandVersions: []int{1}, ResultVersions: []int{1}, PublicErrorCodes: []string{"dependency.unavailable", "job.command.invalid", "job.invariant_failed", "user.not_found"}, Timeout: time.Minute, Concurrency: 1, MaximumAttempts: 8, LeaseDuration: time.Minute, HeartbeatInterval: 15 * time.Second, BaseRetryDelay: time.Second, MaximumRetryDelay: 30 * time.Second, Cancelable: true, ExplicitRetryStatuses: []model.JobStatus{model.JobStatusFailed}, Visibility: jobengine.VisibilityOperator, SuccessRetention: 30 * 24 * time.Hour, FailureRetention: 90 * 24 * time.Hour, Handler: handler}
}

func NewDefaultProfilePictureDescriptor(generator DefaultProfilePictureGenerator) jobengine.Descriptor {
	return defaultProfilePictureDescriptor(defaultProfilePictureHandler{generator: generator})
}
