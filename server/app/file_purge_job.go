// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

const (
	filePurgeMaximumBatchSize = 100
)

type FilePurgeExpiredContentCommandV1 struct {
	BatchSize int `json:"batch_size"`
}
type FilePurgeExpiredContentCheckpointV1 struct {
	Cursor   string `json:"cursor,omitempty"`
	Examined int64  `json:"examined"`
	Purged   int64  `json:"purged"`
}
type FilePurgeExpiredContentResultV1 struct {
	Examined int64 `json:"examined"`
	Purged   int64 `json:"purged"`
}

func EncodeFilePurgeExpiredContentCommand(value FilePurgeExpiredContentCommandV1) (json.RawMessage, error) {
	if value.BatchSize < 1 || value.BatchSize > filePurgeMaximumBatchSize {
		return nil, errors.New("file purge batch size is out of range")
	}
	return json.Marshal(value)
}
func DecodeFilePurgeExpiredContentCommand(version int, document json.RawMessage) (FilePurgeExpiredContentCommandV1, error) {
	var value FilePurgeExpiredContentCommandV1
	if version != 1 {
		return value, fmt.Errorf("unsupported file purge command version %d", version)
	}
	if err := decodeStrictFilePurgeDocument(document, &value); err != nil {
		return value, fmt.Errorf("decode file purge command: %w", err)
	}
	if value.BatchSize < 1 || value.BatchSize > filePurgeMaximumBatchSize {
		return value, errors.New("file purge batch size is out of range")
	}
	return value, nil
}
func EncodeFilePurgeExpiredContentCheckpoint(value FilePurgeExpiredContentCheckpointV1) (json.RawMessage, error) {
	if (value.Cursor != "" && !validFilePurgeCursor(value.Cursor)) || value.Examined < 0 || value.Purged < 0 || value.Purged > value.Examined {
		return nil, errors.New("invalid file purge checkpoint")
	}
	return json.Marshal(value)
}

func validFilePurgeCursor(value string) bool {
	prefix, id, ok := strings.Cut(value, ":")
	return ok && (prefix == "lease" || prefix == "archived") && model.IsValidId(id)
}
func DecodeFilePurgeExpiredContentCheckpoint(version int, document json.RawMessage) (FilePurgeExpiredContentCheckpointV1, error) {
	var value FilePurgeExpiredContentCheckpointV1
	if version != 1 {
		return value, fmt.Errorf("unsupported file purge checkpoint version %d", version)
	}
	if err := decodeStrictFilePurgeDocument(document, &value); err != nil {
		return value, fmt.Errorf("decode file purge checkpoint: %w", err)
	}
	if _, err := EncodeFilePurgeExpiredContentCheckpoint(value); err != nil {
		return value, err
	}
	return value, nil
}
func decodeStrictFilePurgeDocument(document json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing JSON")
		}
		return err
	}
	return nil
}

type filePurgeStore interface {
	ListPurgeCandidates(context.Context, *store.FilePurgeCandidateRequest) (*store.FilePurgeCandidatePage, error)
	ClaimPurgeCandidate(context.Context, *store.FilePurgeCandidate) (*store.FilePurgeClaim, error)
	CompletePurge(context.Context, *store.FilePurgeClaim) error
}
type filePurgeContent interface {
	RemoveFileRevisionContent(context.Context, model.FileRevisionID, []model.FileRenditionID) error
}
type filePurgeExpiredContentHandler struct {
	files   filePurgeStore
	content filePurgeContent
}

func newFilePurgeExpiredContentHandler(files filePurgeStore, content filePurgeContent) JobHandler {
	return filePurgeExpiredContentHandler{files: files, content: content}
}

func (h filePurgeExpiredContentHandler) Run(ctx context.Context, execution JobExecution) JobOutcome {
	if execution.Job == nil || h.files == nil || h.content == nil {
		return JobPermanentFailure("job.command.invalid", errors.New("invalid file purge dependencies"))
	}
	command, err := DecodeFilePurgeExpiredContentCommand(execution.Job.CommandVersion, execution.Job.Command)
	if err != nil {
		return JobPermanentFailure("job.command.invalid", err)
	}
	checkpoint := FilePurgeExpiredContentCheckpointV1{}
	if len(execution.Job.Checkpoint) > 0 {
		checkpoint, err = DecodeFilePurgeExpiredContentCheckpoint(execution.Job.CheckpointVersion, execution.Job.Checkpoint)
		if err != nil {
			return JobPermanentFailure("job.checkpoint.invalid", err)
		}
	}
	remaining := command.BatchSize - execution.Job.WorkReserved
	if checkpoint.Examined >= int64(command.BatchSize) || remaining <= 0 {
		result, marshalErr := json.Marshal(FilePurgeExpiredContentResultV1{Examined: checkpoint.Examined, Purged: checkpoint.Purged})
		return JobOutcome{Kind: JobOutcomeSucceeded, ResultVersion: 1, Result: result, Err: marshalErr}
	}
	if checkpointRemaining := command.BatchSize - int(checkpoint.Examined); checkpointRemaining < remaining {
		remaining = checkpointRemaining
	}
	page, err := h.files.ListPurgeCandidates(ctx, &store.FilePurgeCandidateRequest{After: checkpoint.Cursor, Limit: remaining})
	if err != nil {
		return JobRetryableFailure("database.unavailable", err)
	}
	progressTotal := checkpoint.Examined + int64(len(page.Candidates))
	for index := range page.Candidates {
		candidate := &page.Candidates[index]
		reserved, reserveErr := execution.ReserveWork(ctx, 1, command.BatchSize)
		if reserveErr != nil {
			return JobRetryableFailure("database.unavailable", reserveErr)
		}
		if !reserved {
			break
		}
		claim, claimErr := h.files.ClaimPurgeCandidate(ctx, candidate)
		if claimErr != nil {
			if !store.IsConflict(claimErr) && !store.IsNotFound(claimErr) {
				return JobRetryableFailure("database.unavailable", claimErr)
			}
			checkpoint.Cursor = candidate.Cursor
			checkpoint.Examined++
			if err = checkpointFilePurge(ctx, execution, checkpoint, progressTotal); err != nil {
				return JobRetryableFailure("database.unavailable", err)
			}
			continue
		}
		if err = h.content.RemoveFileRevisionContent(ctx, claim.Candidate.RevisionID, claim.Candidate.RenditionIDs); err != nil {
			return JobRetryableFailure("file.backend_unavailable", err)
		}
		if err = h.files.CompletePurge(ctx, claim); err != nil {
			return JobRetryableFailure("database.unavailable", err)
		}
		checkpoint.Cursor = candidate.Cursor
		checkpoint.Examined++
		checkpoint.Purged++
		if err = checkpointFilePurge(ctx, execution, checkpoint, progressTotal); err != nil {
			return JobRetryableFailure("database.unavailable", err)
		}
	}
	result, err := json.Marshal(FilePurgeExpiredContentResultV1{Examined: checkpoint.Examined, Purged: checkpoint.Purged})
	return JobOutcome{Kind: JobOutcomeSucceeded, ResultVersion: 1, Result: result, Err: err}
}

func checkpointFilePurge(ctx context.Context, execution JobExecution, checkpoint FilePurgeExpiredContentCheckpointV1, total int64) error {
	document, err := EncodeFilePurgeExpiredContentCheckpoint(checkpoint)
	if err != nil {
		return err
	}
	if total < 1 {
		total = 1
	}
	return execution.Checkpoint(ctx, JobCheckpointValue{Version: 1, Progress: &model.JobProgress{Current: checkpoint.Examined, Total: total, Stage: "purging"}, Document: document})
}

func filePurgeExpiredContentDescriptor(handler JobHandler) JobDescriptor {
	return JobDescriptor{Type: model.JobTypeFilePurgeExpiredContent, CommandVersions: []int{1}, CheckpointVersions: []int{1}, ResultVersions: []int{1}, ProgressStages: []string{"purging"}, PublicErrorCodes: []string{"database.unavailable", "file.backend_unavailable", "job.checkpoint.invalid", "job.command.invalid"}, Timeout: time.Minute, Concurrency: 1, MaximumAttempts: 5, LeaseDuration: time.Minute, HeartbeatInterval: 15 * time.Second, BaseRetryDelay: time.Second, MaximumRetryDelay: 30 * time.Second, Visibility: JobVisibilityOperator, SuccessRetention: 30 * 24 * time.Hour, FailureRetention: 90 * 24 * time.Hour, Handler: handler}
}
