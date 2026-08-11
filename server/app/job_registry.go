// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	jobengine "github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/model"
)

type JobExecution = jobengine.Execution
type JobCheckpointValue = jobengine.CheckpointValue
type JobOutcomeKind = jobengine.OutcomeKind
type JobOutcome = jobengine.Outcome
type JobHandler = jobengine.Handler
type JobVisibility = jobengine.Visibility
type JobDescriptor = jobengine.Descriptor
type JobRegistry = jobengine.Registry

const (
	JobOutcomeSucceeded        = jobengine.OutcomeSucceeded
	JobOutcomeRetryableFailure = jobengine.OutcomeRetryableFailure
	JobOutcomePermanentFailure = jobengine.OutcomePermanentFailure
	JobOutcomeCanceled         = jobengine.OutcomeCanceled
	JobVisibilityOperator      = jobengine.VisibilityOperator
	JobVisibilityDomain        = jobengine.VisibilityDomain
)

var (
	JobRetryableFailure = jobengine.RetryableFailure
	JobPermanentFailure = jobengine.PermanentFailure
	JobCanceled         = jobengine.Canceled
	NewJobRegistry      = jobengine.NewRegistry
)

func newJobExecution(record *model.Job, attempt *model.JobAttempt, checkpoint func(context.Context, JobCheckpointValue) error, reserveWork func(context.Context, int, int) (bool, error)) JobExecution {
	return jobengine.NewExecution(record, attempt, checkpoint, reserveWork)
}

type DefaultProfilePictureCommandV1 = model.DefaultProfilePictureCommandV1

type DefaultProfilePictureResultV1 struct {
	FileEntryID model.FileEntryID `json:"file_entry_id"`
}

func DefaultProfilePictureJobSucceeded(fileEntryID model.FileEntryID) JobOutcome {
	if !fileEntryID.IsValid() {
		return JobOutcome{Kind: JobOutcomeSucceeded, Err: errors.New("default profile-picture result has invalid file entry ID")}
	}
	document, err := json.Marshal(DefaultProfilePictureResultV1{FileEntryID: fileEntryID})
	return JobOutcome{Kind: JobOutcomeSucceeded, ResultVersion: 1, Result: document, Err: err}
}

func EncodeDefaultProfilePictureCommand(command DefaultProfilePictureCommandV1) (json.RawMessage, error) {
	return model.EncodeDefaultProfilePictureCommand(command)
}

func DecodeDefaultProfilePictureCommand(version int, document json.RawMessage) (DefaultProfilePictureCommandV1, error) {
	return model.DecodeDefaultProfilePictureCommand(version, document)
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("job command contains trailing JSON")
		}
		return fmt.Errorf("decode trailing job command: %w", err)
	}
	return nil
}
