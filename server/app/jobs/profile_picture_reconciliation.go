// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	jobengine "github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

const maximumDefaultProfilePictureReconciliationBatch = 100

type DefaultProfilePictureReconciliationCommandV1 struct {
	BatchSize int `json:"batch_size"`
}

type DefaultProfilePictureReconciliationCheckpointV1 struct {
	AfterUsername string       `json:"after_username,omitempty"`
	AfterUserID   model.UserID `json:"after_user_id,omitempty"`
	Processed     int64        `json:"processed"`
	Proposed      int64        `json:"proposed"`
}

type DefaultProfilePictureReconciliationResultV1 struct {
	Processed int64 `json:"processed"`
	Proposed  int64 `json:"proposed"`
}

func EncodeDefaultProfilePictureReconciliationCommand(value DefaultProfilePictureReconciliationCommandV1) (json.RawMessage, error) {
	if value.BatchSize < 1 || value.BatchSize > maximumDefaultProfilePictureReconciliationBatch {
		return nil, errors.New("default profile-picture reconciliation batch size is invalid")
	}
	return json.Marshal(value)
}

func DecodeDefaultProfilePictureReconciliationCommand(version int, document json.RawMessage) (DefaultProfilePictureReconciliationCommandV1, error) {
	var value DefaultProfilePictureReconciliationCommandV1
	if version != 1 {
		return value, fmt.Errorf("unsupported default profile-picture reconciliation command version %d", version)
	}
	if err := decodeStrictJobDocument(document, &value); err != nil {
		return value, err
	}
	if value.BatchSize < 1 || value.BatchSize > maximumDefaultProfilePictureReconciliationBatch {
		return value, errors.New("default profile-picture reconciliation batch size is invalid")
	}
	return value, nil
}

func EncodeDefaultProfilePictureReconciliationCheckpoint(value DefaultProfilePictureReconciliationCheckpointV1) (json.RawMessage, error) {
	if err := validateDefaultProfilePictureReconciliationCheckpoint(value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

func DecodeDefaultProfilePictureReconciliationCheckpoint(version int, document json.RawMessage) (DefaultProfilePictureReconciliationCheckpointV1, error) {
	var value DefaultProfilePictureReconciliationCheckpointV1
	if version != 1 {
		return value, fmt.Errorf("unsupported default profile-picture reconciliation checkpoint version %d", version)
	}
	if err := decodeStrictJobDocument(document, &value); err != nil {
		return value, err
	}
	return value, validateDefaultProfilePictureReconciliationCheckpoint(value)
}

func validateDefaultProfilePictureReconciliationCheckpoint(value DefaultProfilePictureReconciliationCheckpointV1) error {
	if value.Processed < 0 || value.Proposed < 0 || value.Proposed > value.Processed || (value.AfterUsername == "") != value.AfterUserID.IsZero() || (!value.AfterUserID.IsZero() && !value.AfterUserID.IsValid()) {
		return errors.New("default profile-picture reconciliation checkpoint is invalid")
	}
	return nil
}

func decodeStrictJobDocument(document json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("job document contains trailing JSON")
		}
		return fmt.Errorf("decode trailing job document: %w", err)
	}
	return nil
}

type DefaultProfilePictureReconciliationUserLister interface {
	List(context.Context, store.UserListOptions) ([]*model.User, error)
}

type ProfilePictureDefaultJobs interface {
	ProposeDefaultProfilePicture(context.Context, model.UserID, time.Time) error
}

type defaultProfilePictureReconciliationHandler struct {
	users    DefaultProfilePictureReconciliationUserLister
	defaults ProfilePictureDefaultJobs
	now      func() time.Time
}

func (h defaultProfilePictureReconciliationHandler) Run(ctx context.Context, execution jobengine.Execution) jobengine.Outcome {
	if execution.Job == nil {
		return jobengine.PermanentFailure("job.command.invalid", errors.New("job is missing"))
	}
	command, err := DecodeDefaultProfilePictureReconciliationCommand(execution.Job.CommandVersion, execution.Job.Command)
	if err != nil {
		return jobengine.PermanentFailure("job.command.invalid", err)
	}
	checkpoint := DefaultProfilePictureReconciliationCheckpointV1{}
	if len(execution.Job.Checkpoint) != 0 {
		checkpoint, err = DecodeDefaultProfilePictureReconciliationCheckpoint(execution.Job.CheckpointVersion, execution.Job.Checkpoint)
		if err != nil {
			return jobengine.PermanentFailure("job.checkpoint.invalid", err)
		}
		return defaultProfilePictureReconciliationSucceeded(checkpoint)
	}
	remaining := command.BatchSize - execution.Job.WorkReserved
	if remaining <= 0 {
		return defaultProfilePictureReconciliationSucceeded(checkpoint)
	}
	users, listErr := h.users.List(ctx, store.UserListOptions{Limit: remaining, IncludeDisabled: true, MissingDefaultProfilePicture: true, Visibility: store.UserVisibilityScope{InstitutionWide: true}})
	if listErr != nil {
		return jobengine.RetryableFailure("dependency.unavailable", listErr)
	}
	for _, user := range users {
		if user == nil || !user.ID.IsValid() {
			return jobengine.PermanentFailure("job.invariant_failed", errors.New("reconciliation returned an invalid user"))
		}
		if !user.DefaultProfilePictureFileID.IsZero() {
			continue
		}
		reserved, reserveErr := execution.ReserveWork(ctx, 1, command.BatchSize)
		if reserveErr != nil {
			return jobengine.RetryableFailure("dependency.unavailable", reserveErr)
		}
		if !reserved {
			break
		}
		checkpoint.Processed++
		checkpoint.AfterUsername = user.Username
		checkpoint.AfterUserID = user.ID
		if proposeErr := h.defaults.ProposeDefaultProfilePicture(ctx, user.ID, model.TimeUTC(h.now())); proposeErr != nil {
			return jobengine.RetryableFailure("dependency.unavailable", proposeErr)
		}
		checkpoint.Proposed++
	}
	if len(users) > 0 {
		document, encodeErr := EncodeDefaultProfilePictureReconciliationCheckpoint(checkpoint)
		if encodeErr != nil {
			return jobengine.PermanentFailure("job.invariant_failed", encodeErr)
		}
		if checkpointErr := execution.Checkpoint(ctx, jobengine.CheckpointValue{Version: 1, Progress: &model.JobProgress{Current: checkpoint.Processed, Total: checkpoint.Processed, Stage: "completed"}, Document: document}); checkpointErr != nil {
			return jobengine.RetryableFailure("dependency.unavailable", checkpointErr)
		}
	}
	return defaultProfilePictureReconciliationSucceeded(checkpoint)
}

func defaultProfilePictureReconciliationSucceeded(checkpoint DefaultProfilePictureReconciliationCheckpointV1) jobengine.Outcome {
	document, err := json.Marshal(DefaultProfilePictureReconciliationResultV1{Processed: checkpoint.Processed, Proposed: checkpoint.Proposed})
	return jobengine.Outcome{Kind: jobengine.OutcomeSucceeded, ResultVersion: 1, Result: document, Err: err}
}

type defaultProfilePictureReconciliationJobProposer struct {
	jobs JobEnqueuer
	now  func() time.Time
}

func (p defaultProfilePictureReconciliationJobProposer) Propose(ctx context.Context, occurrence time.Time) error {
	at := model.TimeUTC(p.now())
	command, err := EncodeDefaultProfilePictureReconciliationCommand(DefaultProfilePictureReconciliationCommandV1{BatchSize: 50})
	if err != nil {
		return err
	}
	key := "reconcile-default-profile-pictures:" + model.TimeUTC(occurrence).Format("2006-01-02")
	job, err := model.NewJobWithDedupePolicy(model.NewJobID(), model.JobTypeProfilePictureReconcile, 1, command, key, model.JobDedupePermanent, at, at, 5)
	if err != nil {
		return err
	}
	_, _, err = p.jobs.Enqueue(ctx, &store.JobEnqueue{Job: job})
	return err
}

func defaultProfilePictureReconciliationDescriptor(handler jobengine.Handler) jobengine.Descriptor {
	return jobengine.Descriptor{Type: model.JobTypeProfilePictureReconcile, CommandVersions: []int{1}, CheckpointVersions: []int{1}, ResultVersions: []int{1}, ProgressStages: []string{"completed"}, PublicErrorCodes: []string{"dependency.unavailable", "job.checkpoint.invalid", "job.command.invalid", "job.invariant_failed"}, Timeout: 10 * time.Minute, Concurrency: 1, MaximumAttempts: 5, LeaseDuration: time.Minute, HeartbeatInterval: 15 * time.Second, BaseRetryDelay: time.Second, MaximumRetryDelay: time.Minute, Visibility: jobengine.VisibilityOperator, SuccessRetention: 30 * 24 * time.Hour, FailureRetention: 90 * 24 * time.Hour, Handler: handler}
}
