// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type defaultProfilePictureJobProposer struct {
	jobs store.JobStore
	wake func()
}

func prepareUserDefaultProfilePictureJob(user *model.User, at time.Time) (*model.User, *model.Job, error) {
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
	command, err := EncodeDefaultProfilePictureCommand(DefaultProfilePictureCommandV1{UserID: candidate.ID})
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

func (p defaultProfilePictureJobProposer) ProposeDefaultProfilePicture(ctx context.Context, userID model.UserID, at time.Time) error {
	command, err := EncodeDefaultProfilePictureCommand(DefaultProfilePictureCommandV1{UserID: userID})
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
	generator interface {
		EnsureDefaultProfilePicture(context.Context, model.UserID) (model.FileEntryID, error)
	}
}

func (h defaultProfilePictureHandler) Run(ctx context.Context, execution JobExecution) JobOutcome {
	if execution.Job == nil {
		return JobPermanentFailure("job.command.invalid", errors.New("job is missing"))
	}
	command, err := DecodeDefaultProfilePictureCommand(execution.Job.CommandVersion, execution.Job.Command)
	if err != nil {
		return JobPermanentFailure("job.command.invalid", err)
	}
	entryID, err := h.generator.EnsureDefaultProfilePicture(ctx, command.UserID)
	if err != nil {
		if errors.Is(err, errDefaultProfilePictureUserNotFound) {
			return JobPermanentFailure("user.not_found", err)
		}
		if errors.Is(err, errDefaultProfilePictureInvariant) {
			return JobPermanentFailure("job.invariant_failed", err)
		}
		return JobRetryableFailure("dependency.unavailable", err)
	}
	return DefaultProfilePictureJobSucceeded(entryID)
}

func defaultProfilePictureDescriptor(handler JobHandler) JobDescriptor {
	return JobDescriptor{Type: model.JobTypeProfilePictureGenerateDefault, CommandVersions: []int{1}, ResultVersions: []int{1}, PublicErrorCodes: []string{"dependency.unavailable", "job.command.invalid", "job.invariant_failed", "user.not_found"}, Timeout: time.Minute, Concurrency: 1, MaximumAttempts: 8, LeaseDuration: time.Minute, HeartbeatInterval: 15 * time.Second, BaseRetryDelay: time.Second, MaximumRetryDelay: 30 * time.Second, Cancelable: true, ExplicitRetryStatuses: []model.JobStatus{model.JobStatusFailed}, Visibility: JobVisibilityOperator, SuccessRetention: 30 * 24 * time.Hour, FailureRetention: 90 * 24 * time.Hour, Handler: handler}
}
