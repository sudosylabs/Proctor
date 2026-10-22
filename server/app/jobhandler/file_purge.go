// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package jobhandler

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

// FilePurgeExpiredContentCommand bounds one purge Job. Cursor is set only on
// a continuation and resumes the FileRevision phase after its predecessor.
type FilePurgeExpiredContentCommand struct {
	BatchSize int    `json:"batch_size"`
	Cursor    string `json:"cursor,omitempty"`
}

// FilePurgeExpiredContentCheckpoint counts examined objects for the whole
// batch and separately for each workspace phase; the FileRevision phase's
// count is the remainder.
type FilePurgeExpiredContentCheckpoint struct {
	Cursor          string `json:"cursor,omitempty"`
	Examined        int64  `json:"examined"`
	Purged          int64  `json:"purged"`
	StarterExamined int64  `json:"starter_examined,omitempty"`
	AttemptExamined int64  `json:"attempt_examined,omitempty"`
}

// FilePurgeExpiredContentResult is the safe result of the expired file-content purge Job.
type FilePurgeExpiredContentResult struct {
	Examined int64 `json:"examined"`
	Purged   int64 `json:"purged"`
}

// EncodeFilePurgeExpiredContentCommand validates and encodes an expired file-content purge Job command.
func EncodeFilePurgeExpiredContentCommand(value FilePurgeExpiredContentCommand) (json.RawMessage, error) {
	if err := validateFilePurgeCommand(value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

func validateFilePurgeCommand(value FilePurgeExpiredContentCommand) error {
	if value.BatchSize < 1 || value.BatchSize > filePurgeMaximumBatchSize {
		return errors.New("file purge batch size is out of range")
	}
	if value.Cursor != "" && !validFilePurgeCursor(value.Cursor) {
		return errors.New("invalid file purge cursor")
	}
	return nil
}

// DecodeFilePurgeExpiredContentCommand strictly decodes and validates an expired file-content purge Job command.
func DecodeFilePurgeExpiredContentCommand(document json.RawMessage) (FilePurgeExpiredContentCommand, error) {
	var value FilePurgeExpiredContentCommand
	if err := decodeStrictFilePurgeDocument(document, &value); err != nil {
		return value, fmt.Errorf("decode file purge command: %w", err)
	}
	if err := validateFilePurgeCommand(value); err != nil {
		return value, err
	}
	return value, nil
}

// EncodeFilePurgeExpiredContentCheckpoint validates and encodes an expired file-content purge Job checkpoint.
func EncodeFilePurgeExpiredContentCheckpoint(value FilePurgeExpiredContentCheckpoint) (json.RawMessage, error) {
	if (value.Cursor != "" && !validFilePurgeCursor(value.Cursor)) || value.Examined < 0 || value.Purged < 0 || value.Purged > value.Examined ||
		value.StarterExamined < 0 || value.AttemptExamined < 0 || value.StarterExamined+value.AttemptExamined > value.Examined {
		return nil, errors.New("invalid file purge checkpoint")
	}
	return json.Marshal(value)
}

func validFilePurgeCursor(value string) bool {
	prefix, id, ok := strings.Cut(value, ":")
	return ok && (prefix == "lease" || prefix == "archived") && model.IsValidOpaqueID(id)
}

// DecodeFilePurgeExpiredContentCheckpoint strictly decodes and validates an expired file-content purge Job checkpoint.
func DecodeFilePurgeExpiredContentCheckpoint(document json.RawMessage) (FilePurgeExpiredContentCheckpoint, error) {
	var value FilePurgeExpiredContentCheckpoint
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
	continuations           JobEnqueuer
	now                     func() time.Time
}

// filePurgeContinuationDelay spaces successors so a backlog drains steadily
// without a tight chain of Jobs.
const filePurgeContinuationDelay = time.Second

func (h filePurgeExpiredContentHandler) Run(ctx context.Context, execution jobengine.Execution) jobengine.Outcome {
	if execution.Job == nil || !execution.Job.ID.IsValid() || h.files == nil || h.content == nil ||
		h.continuations == nil || h.now == nil ||
		(h.starterWorkspace == nil) != (h.starterWorkspaceContent == nil) ||
		(h.attemptWorkspace == nil) != (h.attemptWorkspaceContent == nil) ||
		((h.starterWorkspace != nil || h.attemptWorkspace != nil) &&
			(execution.Attempt == nil || !execution.Attempt.ClaimToken.IsValid())) {
		return jobengine.PermanentFailure("job.command.invalid", errors.New("invalid file purge dependencies"))
	}
	command, err := DecodeFilePurgeExpiredContentCommand(execution.Job.Command)
	if err != nil {
		return jobengine.PermanentFailure("job.command.invalid", err)
	}
	checkpoint := FilePurgeExpiredContentCheckpoint{Cursor: command.Cursor}
	if len(execution.Job.Checkpoint) > 0 {
		checkpoint, err = DecodeFilePurgeExpiredContentCheckpoint(execution.Job.Checkpoint)
		if err != nil {
			return jobengine.PermanentFailure("job.checkpoint.invalid", err)
		}
	}
	allowances := h.phaseAllowances(command.BatchSize, checkpoint)
	if h.starterWorkspace != nil {
		var failure *jobengine.Outcome
		checkpoint, failure = h.purgeStarterWorkspaceObjects(ctx, execution, command, allowances.starter, checkpoint)
		if failure != nil {
			return *failure
		}
	}
	allowances = h.phaseAllowances(command.BatchSize, checkpoint)
	if h.attemptWorkspace != nil {
		var failure *jobengine.Outcome
		checkpoint, failure = h.purgeAttemptWorkspaceObjects(ctx, execution, command, allowances.attempt, checkpoint)
		if failure != nil {
			return *failure
		}
	}
	allowances = h.phaseAllowances(command.BatchSize, checkpoint)
	if failure := h.purgeFileRevisions(ctx, execution, command, allowances.generic, &checkpoint); failure != nil {
		return *failure
	}
	return h.finish(ctx, execution.Job, command, checkpoint)
}

// filePurgeAllowances is how many objects each phase may examine in one batch.
type filePurgeAllowances struct {
	starter, attempt, generic int
}

// phaseAllowances gives each enabled workspace phase an equal share of the
// batch, so a FileRevision backlog cannot starve workspace cleanup. The
// phases run in order (Starter Workspace, Attempt Workspace, FileRevision),
// and each may also spend what earlier phases left unused, so the
// FileRevision phase takes the rest of the batch. Allowances derive from the
// checkpoint counts alone, so a resumed attempt recomputes the same ones.
func (h filePurgeExpiredContentHandler) phaseAllowances(batchSize int, checkpoint FilePurgeExpiredContentCheckpoint) filePurgeAllowances {
	phases := 1
	if h.starterWorkspace != nil {
		phases++
	}
	if h.attemptWorkspace != nil {
		phases++
	}
	share := batchSize / phases
	var allowances filePurgeAllowances
	reserved := 0
	if h.starterWorkspace != nil {
		allowances.starter = share
		reserved += share
	}
	if h.attemptWorkspace != nil {
		reserved += share
		allowances.attempt = reserved - int(checkpoint.StarterExamined)
	}
	allowances.generic = batchSize - int(checkpoint.StarterExamined+checkpoint.AttemptExamined)
	return allowances
}

func genericExamined(checkpoint FilePurgeExpiredContentCheckpoint) int64 {
	return checkpoint.Examined - checkpoint.StarterExamined - checkpoint.AttemptExamined
}

// remainingFilePurgeWork bounds one phase by its allowance and by the Job's
// durable work reservation.
func remainingFilePurgeWork(execution jobengine.Execution, command FilePurgeExpiredContentCommand, allowance int, examined int64) int {
	return min(allowance-int(examined), command.BatchSize-execution.Job.WorkReserved)
}

func (h filePurgeExpiredContentHandler) purgeFileRevisions(ctx context.Context, execution jobengine.Execution,
	command FilePurgeExpiredContentCommand, allowance int, checkpoint *FilePurgeExpiredContentCheckpoint,
) *jobengine.Outcome {
	remaining := remainingFilePurgeWork(execution, command, allowance, genericExamined(*checkpoint))
	if remaining <= 0 {
		return nil
	}
	fail := func(outcome jobengine.Outcome) *jobengine.Outcome { return &outcome }
	page, err := h.files.ListPurgeCandidates(ctx, &store.FilePurgeCandidateRequest{After: checkpoint.Cursor, Limit: remaining})
	if err != nil {
		return fail(jobengine.RetryableFailure("database.unavailable", err))
	}
	progressTotal := checkpoint.Examined + int64(len(page.Candidates))
	for index := range page.Candidates {
		candidate := &page.Candidates[index]
		reserved, reserveErr := execution.ReserveWork(ctx, 1, command.BatchSize)
		if reserveErr != nil {
			return fail(jobengine.RetryableFailure("database.unavailable", reserveErr))
		}
		if !reserved {
			break
		}
		claim, claimErr := h.files.ClaimPurgeCandidate(ctx, candidate)
		if claimErr != nil {
			if !store.IsConflict(claimErr) && !store.IsNotFound(claimErr) {
				return fail(jobengine.RetryableFailure("database.unavailable", claimErr))
			}
			checkpoint.Cursor = candidate.Cursor
			checkpoint.Examined++
			if err = checkpointFilePurge(ctx, execution, *checkpoint, progressTotal); err != nil {
				return fail(jobengine.RetryableFailure("database.unavailable", err))
			}
			continue
		}
		switch claim.Candidate.Kind {
		case store.FilePurgeCandidateExpiredLease:
			err = h.content.PurgeAbandonedFileRevision(ctx, claim.Candidate.RevisionID)
		case store.FilePurgeCandidateArchivedCustom:
			err = h.content.RemoveFileRevisionRenditions(ctx, claim.Candidate.RevisionID, claim.Candidate.RenditionIDs)
		default:
			return fail(jobengine.PermanentFailure("job.command.invalid", errors.New("unknown file purge candidate kind")))
		}
		if err != nil {
			return fail(jobengine.RetryableFailure("file.backend_unavailable", err))
		}
		if err = h.files.CompletePurge(ctx, claim); err != nil {
			return fail(jobengine.RetryableFailure("database.unavailable", err))
		}
		checkpoint.Cursor = candidate.Cursor
		checkpoint.Examined++
		checkpoint.Purged++
		if err = checkpointFilePurge(ctx, execution, *checkpoint, progressTotal); err != nil {
			return fail(jobengine.RetryableFailure("database.unavailable", err))
		}
	}
	return nil
}

// finish succeeds the Job, first enqueueing one continuation when some phase
// examined its whole allowance and may hold more work. The continuation is
// permanently deduplicated on this Job, so a retry or an unknown enqueue
// outcome cannot fork the chain, and it starts with a fresh batch. Only
// examined work fills an allowance, so the chain ends once every phase drains;
// a reservation lost to an uncertain checkpoint never extends it.
func (h filePurgeExpiredContentHandler) finish(ctx context.Context, parent *model.Job,
	command FilePurgeExpiredContentCommand, checkpoint FilePurgeExpiredContentCheckpoint,
) jobengine.Outcome {
	if h.mayHaveMoreWork(command, checkpoint) {
		document, err := EncodeFilePurgeExpiredContentCommand(FilePurgeExpiredContentCommand{BatchSize: command.BatchSize, Cursor: checkpoint.Cursor})
		if err != nil {
			return jobengine.PermanentFailure("job.command.invalid", err)
		}
		at := model.TimeUTC(h.now())
		successor, err := model.NewJobWithDedupePolicy(model.NewJobID(), model.JobTypeFilePurgeExpiredContent, document,
			"file-purge-expired-content:after:"+parent.ID.String(), model.JobDedupePermanent, at, at.Add(filePurgeContinuationDelay), 5)
		if err != nil {
			return jobengine.PermanentFailure("job.command.invalid", err)
		}
		if _, _, err = h.continuations.Enqueue(ctx, &store.JobEnqueue{Job: successor}); err != nil {
			return jobengine.RetryableFailure("database.unavailable", err)
		}
	}
	result, err := json.Marshal(FilePurgeExpiredContentResult{Examined: checkpoint.Examined, Purged: checkpoint.Purged})
	return jobengine.Outcome{Kind: jobengine.OutcomeSucceeded, Result: result, Err: err}
}

// mayHaveMoreWork reports whether some phase examined its whole allowance.
func (h filePurgeExpiredContentHandler) mayHaveMoreWork(command FilePurgeExpiredContentCommand, checkpoint FilePurgeExpiredContentCheckpoint) bool {
	allowances := h.phaseAllowances(command.BatchSize, checkpoint)
	full := func(examined int64, allowance int) bool { return examined > 0 && examined >= int64(allowance) }
	return full(checkpoint.StarterExamined, allowances.starter) ||
		full(checkpoint.AttemptExamined, allowances.attempt) ||
		full(genericExamined(checkpoint), allowances.generic)
}

// purgeStarterWorkspaceObjects is the first physical-content phase. On failure it returns the last durable checkpoint and a retryable outcome;
// otherwise it returns the updated aggregate checkpoint.
func (h filePurgeExpiredContentHandler) purgeStarterWorkspaceObjects(ctx context.Context, execution jobengine.Execution, command FilePurgeExpiredContentCommand, allowance int, checkpoint FilePurgeExpiredContentCheckpoint) (FilePurgeExpiredContentCheckpoint, *jobengine.Outcome) {
	remaining := remainingFilePurgeWork(execution, command, allowance, checkpoint.StarterExamined)
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
		checkpoint.StarterExamined++
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

// purgeAttemptWorkspaceObjects follows the Starter Workspace phase. It shares
// the Job's one bounded work reservation, checkpoint and claim token with the
// Starter Workspace and FileRevision phases.
func (h filePurgeExpiredContentHandler) purgeAttemptWorkspaceObjects(ctx context.Context, execution jobengine.Execution,
	command FilePurgeExpiredContentCommand, allowance int, checkpoint FilePurgeExpiredContentCheckpoint,
) (FilePurgeExpiredContentCheckpoint, *jobengine.Outcome) {
	remaining := remainingFilePurgeWork(execution, command, allowance, checkpoint.AttemptExamined)
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
		checkpoint.AttemptExamined++
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

func checkpointFilePurge(ctx context.Context, execution jobengine.Execution, checkpoint FilePurgeExpiredContentCheckpoint, total int64) error {
	document, err := EncodeFilePurgeExpiredContentCheckpoint(checkpoint)
	if err != nil {
		return err
	}
	if total < 1 {
		total = 1
	}
	return execution.Checkpoint(ctx, jobengine.CheckpointValue{Progress: &model.JobProgress{Current: checkpoint.Examined, Total: total, Stage: "purging"}, Document: document})
}

func filePurgeExpiredContentDescriptor(handler jobengine.Handler) jobengine.Descriptor {
	return jobengine.Descriptor{Type: model.JobTypeFilePurgeExpiredContent, Checkpoints: true, ProgressStages: []string{"purging"}, PublicErrorCodes: []string{"database.unavailable", "file.backend_unavailable", "job.checkpoint.invalid", "job.command.invalid"}, Timeout: time.Minute, Concurrency: 1, MaximumAttempts: 5, LeaseDuration: time.Minute, HeartbeatInterval: 15 * time.Second, BaseRetryDelay: time.Second, MaximumRetryDelay: 30 * time.Second, Visibility: jobengine.VisibilityOperator, SuccessRetention: 30 * 24 * time.Hour, FailureRetention: 90 * 24 * time.Hour, Handler: handler}
}

type filePurgeExpiredContentProposer struct {
	jobs JobEnqueuer
	now  func() time.Time
}

func (p filePurgeExpiredContentProposer) Propose(ctx context.Context, occurrence time.Time) error {
	if p.jobs == nil || p.now == nil {
		return errors.New("invalid file purge proposer dependencies")
	}
	command, err := EncodeFilePurgeExpiredContentCommand(FilePurgeExpiredContentCommand{BatchSize: 50})
	if err != nil {
		return err
	}
	at := model.TimeUTC(p.now())
	key := "file-purge-expired-content:" + model.TimeUTC(occurrence).Format("2006-01-02")
	job, err := model.NewJobWithDedupePolicy(model.NewJobID(), model.JobTypeFilePurgeExpiredContent,
		json.RawMessage(command), key, model.JobDedupePermanent, at, at, 5)
	if err != nil {
		return err
	}
	_, _, err = p.jobs.Enqueue(ctx, &store.JobEnqueue{Job: job})
	return err
}
