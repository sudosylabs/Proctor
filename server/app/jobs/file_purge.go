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
	"strings"
	"time"

	jobengine "github.com/sudosylabs/proctor/server/app/job"
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

type FilePurgeStore interface {
	ListPurgeCandidates(context.Context, *store.FilePurgeCandidateRequest) (*store.FilePurgeCandidatePage, error)
	ClaimPurgeCandidate(context.Context, *store.FilePurgeCandidate) (*store.FilePurgeClaim, error)
	CompletePurge(context.Context, *store.FilePurgeClaim) error
}

// FileRevisionContentPurger is the physical-content capability used after the
// file store has durably fenced a purge candidate.
type FileRevisionContentPurger interface {
	PurgeAbandonedFileRevision(context.Context, model.FileRevisionID) error
	RemoveFileRevisionRenditions(context.Context, model.FileRevisionID, []model.FileRenditionID) error
}

type StarterWorkspaceCleanupStore interface {
	ClaimObjectsForCleanup(context.Context, int, string) ([]model.StarterWorkspaceObject, error)
	CompleteObjectCleanup(context.Context, model.StarterWorkspaceObjectID, string) error
	ReleaseObjectCleanup(context.Context, model.StarterWorkspaceObjectID, string) error
}

type StarterWorkspaceObjectPurger interface {
	RemoveStarterWorkspaceObject(context.Context, model.StarterWorkspaceObjectID) error
}

type AttemptWorkspaceCleanupStore interface {
	ClaimObjectsForCleanup(context.Context, int, string) ([]model.AttemptWorkspaceObject, error)
	CompleteObjectCleanup(context.Context, model.AttemptWorkspaceObjectID, string) error
	ReleaseObjectCleanup(context.Context, model.AttemptWorkspaceObjectID, string) error
}

type AttemptWorkspaceObjectPurger interface {
	RemoveAttemptWorkspaceObject(context.Context, model.AttemptWorkspaceObjectID) error
}

type filePurgeExpiredContentHandler struct {
	files                   FilePurgeStore
	content                 FileRevisionContentPurger
	starterWorkspace        StarterWorkspaceCleanupStore
	starterWorkspaceContent StarterWorkspaceObjectPurger
	attemptWorkspace        AttemptWorkspaceCleanupStore
	attemptWorkspaceContent AttemptWorkspaceObjectPurger
}

func newFilePurgeExpiredContentHandler(files FilePurgeStore, content FileRevisionContentPurger,
	starterWorkspace StarterWorkspaceCleanupStore, starterWorkspaceContent StarterWorkspaceObjectPurger,
	attemptWorkspace AttemptWorkspaceCleanupStore, attemptWorkspaceContent AttemptWorkspaceObjectPurger,
) jobengine.Handler {
	return filePurgeExpiredContentHandler{files: files, content: content,
		starterWorkspace: starterWorkspace, starterWorkspaceContent: starterWorkspaceContent,
		attemptWorkspace: attemptWorkspace, attemptWorkspaceContent: attemptWorkspaceContent}
}

func (h filePurgeExpiredContentHandler) Run(ctx context.Context, execution jobengine.Execution) jobengine.Outcome {
	if execution.Job == nil || h.files == nil || h.content == nil ||
		(h.starterWorkspace == nil) != (h.starterWorkspaceContent == nil) ||
		(h.attemptWorkspace == nil) != (h.attemptWorkspaceContent == nil) ||
		((h.starterWorkspace != nil || h.attemptWorkspace != nil) &&
			(execution.Attempt == nil || !execution.Attempt.ClaimToken.IsValid())) {
		return jobengine.PermanentFailure("job.command.invalid", errors.New("invalid file purge dependencies"))
	}
	command, err := DecodeFilePurgeExpiredContentCommand(execution.Job.CommandVersion, execution.Job.Command)
	if err != nil {
		return jobengine.PermanentFailure("job.command.invalid", err)
	}
	checkpoint := FilePurgeExpiredContentCheckpointV1{}
	if len(execution.Job.Checkpoint) > 0 {
		checkpoint, err = DecodeFilePurgeExpiredContentCheckpoint(execution.Job.CheckpointVersion, execution.Job.Checkpoint)
		if err != nil {
			return jobengine.PermanentFailure("job.checkpoint.invalid", err)
		}
	}
	remaining := command.BatchSize - execution.Job.WorkReserved
	if checkpoint.Examined >= int64(command.BatchSize) || remaining <= 0 {
		result, marshalErr := json.Marshal(FilePurgeExpiredContentResultV1{Examined: checkpoint.Examined, Purged: checkpoint.Purged})
		return jobengine.Outcome{Kind: jobengine.OutcomeSucceeded, ResultVersion: 1, Result: result, Err: marshalErr}
	}
	if checkpointRemaining := command.BatchSize - int(checkpoint.Examined); checkpointRemaining < remaining {
		remaining = checkpointRemaining
	}
	page, err := h.files.ListPurgeCandidates(ctx, &store.FilePurgeCandidateRequest{After: checkpoint.Cursor, Limit: remaining})
	if err != nil {
		return jobengine.RetryableFailure("database.unavailable", err)
	}
	progressTotal := checkpoint.Examined + int64(len(page.Candidates))
	for index := range page.Candidates {
		candidate := &page.Candidates[index]
		reserved, reserveErr := execution.ReserveWork(ctx, 1, command.BatchSize)
		if reserveErr != nil {
			return jobengine.RetryableFailure("database.unavailable", reserveErr)
		}
		if !reserved {
			break
		}
		claim, claimErr := h.files.ClaimPurgeCandidate(ctx, candidate)
		if claimErr != nil {
			if !store.IsConflict(claimErr) && !store.IsNotFound(claimErr) {
				return jobengine.RetryableFailure("database.unavailable", claimErr)
			}
			checkpoint.Cursor = candidate.Cursor
			checkpoint.Examined++
			if err = checkpointFilePurge(ctx, execution, checkpoint, progressTotal); err != nil {
				return jobengine.RetryableFailure("database.unavailable", err)
			}
			continue
		}
		switch claim.Candidate.Kind {
		case store.FilePurgeCandidateExpiredLease:
			err = h.content.PurgeAbandonedFileRevision(ctx, claim.Candidate.RevisionID)
		case store.FilePurgeCandidateArchivedCustom:
			err = h.content.RemoveFileRevisionRenditions(ctx, claim.Candidate.RevisionID, claim.Candidate.RenditionIDs)
		default:
			return jobengine.PermanentFailure("job.command.invalid", errors.New("unknown file purge candidate kind"))
		}
		if err != nil {
			return jobengine.RetryableFailure("file.backend_unavailable", err)
		}
		if err = h.files.CompletePurge(ctx, claim); err != nil {
			return jobengine.RetryableFailure("database.unavailable", err)
		}
		checkpoint.Cursor = candidate.Cursor
		checkpoint.Examined++
		checkpoint.Purged++
		if err = checkpointFilePurge(ctx, execution, checkpoint, progressTotal); err != nil {
			return jobengine.RetryableFailure("database.unavailable", err)
		}
	}
	if h.starterWorkspace != nil {
		var failure *jobengine.Outcome
		checkpoint, failure = h.purgeStarterWorkspaceObjects(ctx, execution, command, checkpoint)
		if failure != nil {
			return *failure
		}
	}
	if h.attemptWorkspace != nil {
		var failure *jobengine.Outcome
		checkpoint, failure = h.purgeAttemptWorkspaceObjects(ctx, execution, command, checkpoint)
		if failure != nil {
			return *failure
		}
	}
	result, err := json.Marshal(FilePurgeExpiredContentResultV1{Examined: checkpoint.Examined, Purged: checkpoint.Purged})
	return jobengine.Outcome{Kind: jobengine.OutcomeSucceeded, ResultVersion: 1, Result: result, Err: err}
}

// purgeStarterWorkspaceObjects runs only after the generic FileRevision phase.
// On failure it returns the last durable checkpoint and a retryable outcome;
// otherwise it returns the updated aggregate checkpoint.
func (h filePurgeExpiredContentHandler) purgeStarterWorkspaceObjects(ctx context.Context, execution jobengine.Execution, command FilePurgeExpiredContentCommandV1, checkpoint FilePurgeExpiredContentCheckpointV1) (FilePurgeExpiredContentCheckpointV1, *jobengine.Outcome) {
	remaining := command.BatchSize - int(checkpoint.Examined)
	if reservedRemaining := command.BatchSize - execution.Job.WorkReserved; reservedRemaining < remaining {
		remaining = reservedRemaining
	}
	if remaining <= 0 {
		return checkpoint, nil
	}
	claimToken := string(execution.Attempt.ClaimToken)
	objects, err := h.starterWorkspace.ClaimObjectsForCleanup(ctx, remaining, claimToken)
	if err != nil {
		failure := jobengine.RetryableFailure("database.unavailable", err)
		return checkpoint, &failure
	}
	progressTotal := checkpoint.Examined + int64(len(objects))
	for index := range objects {
		object := objects[index]
		reserved, reserveErr := execution.ReserveWork(ctx, 1, command.BatchSize)
		if reserveErr != nil {
			if releaseErr := h.releaseStarterWorkspaceClaims(ctx, objects[index:], claimToken); releaseErr != nil {
				failure := jobengine.RetryableFailure("database.unavailable", releaseErr)
				return checkpoint, &failure
			}
			failure := jobengine.RetryableFailure("database.unavailable", reserveErr)
			return checkpoint, &failure
		}
		if !reserved {
			if releaseErr := h.releaseStarterWorkspaceClaims(ctx, objects[index:], claimToken); releaseErr != nil {
				failure := jobengine.RetryableFailure("database.unavailable", releaseErr)
				return checkpoint, &failure
			}
			return checkpoint, nil
		}
		if err = h.starterWorkspaceContent.RemoveStarterWorkspaceObject(ctx, object.ID); err != nil {
			// Removal errors have an unknown outcome. Keep this object fenced for
			// stale-claim retry, but release every object never sent to storage.
			if releaseErr := h.releaseStarterWorkspaceClaims(ctx, objects[index+1:], claimToken); releaseErr != nil {
				failure := jobengine.RetryableFailure("database.unavailable", releaseErr)
				return checkpoint, &failure
			}
			failure := jobengine.RetryableFailure("file.backend_unavailable", err)
			return checkpoint, &failure
		}
		if err = h.starterWorkspace.CompleteObjectCleanup(ctx, object.ID, claimToken); err != nil {
			// Physical removal is idempotent. Leave the current claim fenced so a
			// stale-claim retry can safely repeat it and complete metadata cleanup.
			if releaseErr := h.releaseStarterWorkspaceClaims(ctx, objects[index+1:], claimToken); releaseErr != nil {
				failure := jobengine.RetryableFailure("database.unavailable", releaseErr)
				return checkpoint, &failure
			}
			failure := jobengine.RetryableFailure("database.unavailable", err)
			return checkpoint, &failure
		}
		checkpoint.Examined++
		checkpoint.Purged++
		if err = checkpointFilePurge(ctx, execution, checkpoint, progressTotal); err != nil {
			if releaseErr := h.releaseStarterWorkspaceClaims(ctx, objects[index+1:], claimToken); releaseErr != nil {
				failure := jobengine.RetryableFailure("database.unavailable", releaseErr)
				return checkpoint, &failure
			}
			failure := jobengine.RetryableFailure("database.unavailable", err)
			return checkpoint, &failure
		}
	}
	return checkpoint, nil
}

// purgeAttemptWorkspaceObjects is the final physical-content phase. It shares
// the Job's one bounded work reservation, checkpoint and claim token with the
// generic and Starter Workspace phases.
func (h filePurgeExpiredContentHandler) purgeAttemptWorkspaceObjects(ctx context.Context, execution jobengine.Execution,
	command FilePurgeExpiredContentCommandV1, checkpoint FilePurgeExpiredContentCheckpointV1,
) (FilePurgeExpiredContentCheckpointV1, *jobengine.Outcome) {
	remaining := command.BatchSize - int(checkpoint.Examined)
	if reservedRemaining := command.BatchSize - execution.Job.WorkReserved; reservedRemaining < remaining {
		remaining = reservedRemaining
	}
	if remaining <= 0 {
		return checkpoint, nil
	}
	claimToken := string(execution.Attempt.ClaimToken)
	objects, err := h.attemptWorkspace.ClaimObjectsForCleanup(ctx, remaining, claimToken)
	if err != nil {
		failure := jobengine.RetryableFailure("database.unavailable", err)
		return checkpoint, &failure
	}
	progressTotal := checkpoint.Examined + int64(len(objects))
	for index := range objects {
		object := objects[index]
		reserved, reserveErr := execution.ReserveWork(ctx, 1, command.BatchSize)
		if reserveErr != nil {
			if releaseErr := h.releaseAttemptWorkspaceClaims(ctx, objects[index:], claimToken); releaseErr != nil {
				failure := jobengine.RetryableFailure("database.unavailable", releaseErr)
				return checkpoint, &failure
			}
			failure := jobengine.RetryableFailure("database.unavailable", reserveErr)
			return checkpoint, &failure
		}
		if !reserved {
			if releaseErr := h.releaseAttemptWorkspaceClaims(ctx, objects[index:], claimToken); releaseErr != nil {
				failure := jobengine.RetryableFailure("database.unavailable", releaseErr)
				return checkpoint, &failure
			}
			return checkpoint, nil
		}
		if err = h.attemptWorkspaceContent.RemoveAttemptWorkspaceObject(ctx, object.ID); err != nil {
			// Outcome-unknown removal remains claimed for stale retry; only objects
			// never sent to the backend are released.
			if releaseErr := h.releaseAttemptWorkspaceClaims(ctx, objects[index+1:], claimToken); releaseErr != nil {
				failure := jobengine.RetryableFailure("database.unavailable", releaseErr)
				return checkpoint, &failure
			}
			failure := jobengine.RetryableFailure("file.backend_unavailable", err)
			return checkpoint, &failure
		}
		if err = h.attemptWorkspace.CompleteObjectCleanup(ctx, object.ID, claimToken); err != nil {
			if releaseErr := h.releaseAttemptWorkspaceClaims(ctx, objects[index+1:], claimToken); releaseErr != nil {
				failure := jobengine.RetryableFailure("database.unavailable", releaseErr)
				return checkpoint, &failure
			}
			failure := jobengine.RetryableFailure("database.unavailable", err)
			return checkpoint, &failure
		}
		checkpoint.Examined++
		checkpoint.Purged++
		if err = checkpointFilePurge(ctx, execution, checkpoint, progressTotal); err != nil {
			if releaseErr := h.releaseAttemptWorkspaceClaims(ctx, objects[index+1:], claimToken); releaseErr != nil {
				failure := jobengine.RetryableFailure("database.unavailable", releaseErr)
				return checkpoint, &failure
			}
			failure := jobengine.RetryableFailure("database.unavailable", err)
			return checkpoint, &failure
		}
	}
	return checkpoint, nil
}

func (h filePurgeExpiredContentHandler) releaseStarterWorkspaceClaims(ctx context.Context, objects []model.StarterWorkspaceObject, claimToken string) error {
	for index := range objects {
		if err := h.starterWorkspace.ReleaseObjectCleanup(ctx, objects[index].ID, claimToken); err != nil {
			return err
		}
	}
	return nil
}

func (h filePurgeExpiredContentHandler) releaseAttemptWorkspaceClaims(ctx context.Context,
	objects []model.AttemptWorkspaceObject, claimToken string,
) error {
	for index := range objects {
		if err := h.attemptWorkspace.ReleaseObjectCleanup(ctx, objects[index].ID, claimToken); err != nil {
			return err
		}
	}
	return nil
}

func checkpointFilePurge(ctx context.Context, execution jobengine.Execution, checkpoint FilePurgeExpiredContentCheckpointV1, total int64) error {
	document, err := EncodeFilePurgeExpiredContentCheckpoint(checkpoint)
	if err != nil {
		return err
	}
	if total < 1 {
		total = 1
	}
	return execution.Checkpoint(ctx, jobengine.CheckpointValue{Version: 1, Progress: &model.JobProgress{Current: checkpoint.Examined, Total: total, Stage: "purging"}, Document: document})
}

func filePurgeExpiredContentDescriptor(handler jobengine.Handler) jobengine.Descriptor {
	return jobengine.Descriptor{Type: model.JobTypeFilePurgeExpiredContent, CommandVersions: []int{1}, CheckpointVersions: []int{1}, ResultVersions: []int{1}, ProgressStages: []string{"purging"}, PublicErrorCodes: []string{"database.unavailable", "file.backend_unavailable", "job.checkpoint.invalid", "job.command.invalid"}, Timeout: time.Minute, Concurrency: 1, MaximumAttempts: 5, LeaseDuration: time.Minute, HeartbeatInterval: 15 * time.Second, BaseRetryDelay: time.Second, MaximumRetryDelay: 30 * time.Second, Visibility: jobengine.VisibilityOperator, SuccessRetention: 30 * 24 * time.Hour, FailureRetention: 90 * 24 * time.Hour, Handler: handler}
}

type filePurgeExpiredContentProposer struct {
	jobs JobEnqueuer
	now  func() time.Time
}

func (p filePurgeExpiredContentProposer) Propose(ctx context.Context, occurrence time.Time) error {
	if p.jobs == nil || p.now == nil {
		return errors.New("invalid file purge proposer dependencies")
	}
	command, err := EncodeFilePurgeExpiredContentCommand(FilePurgeExpiredContentCommandV1{BatchSize: 50})
	if err != nil {
		return err
	}
	at := model.TimeUTC(p.now())
	key := "file-purge-expired-content:" + model.TimeUTC(occurrence).Format("2006-01-02")
	job, err := model.NewJobWithDedupePolicy(model.NewJobID(), model.JobTypeFilePurgeExpiredContent, 1,
		json.RawMessage(command), key, model.JobDedupePermanent, at, at, 5)
	if err != nil {
		return err
	}
	_, _, err = p.jobs.Enqueue(ctx, &store.JobEnqueue{Job: job})
	return err
}
