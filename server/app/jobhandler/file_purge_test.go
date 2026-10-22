// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package jobhandler

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestFilePurgeCommandAndCheckpointAreTypedAndBounded(t *testing.T) {
	t.Parallel()
	command, err := EncodeFilePurgeExpiredContentCommand(FilePurgeExpiredContentCommand{BatchSize: 50})
	require.NoError(t, err)
	decoded, err := DecodeFilePurgeExpiredContentCommand(command)
	require.NoError(t, err, "decode command = %#v", decoded)
	require.Equal(t, 50, decoded.BatchSize, "decode command = %#v", decoded)
	_, err = EncodeFilePurgeExpiredContentCommand(FilePurgeExpiredContentCommand{BatchSize: 101})
	require.Error(t, err, "accepted oversized batch")
	_, err = DecodeFilePurgeExpiredContentCommand(json.RawMessage(`{"batch_size":1,"path":"secret"}`))
	require.Error(t, err, "accepted unknown field")

	checkpoint, err := EncodeFilePurgeExpiredContentCheckpoint(FilePurgeExpiredContentCheckpoint{Cursor: "lease:" + model.NewID(), Examined: 2, Purged: 1})
	require.NoError(t, err)
	_, err = DecodeFilePurgeExpiredContentCheckpoint(checkpoint)
	require.NoError(t, err)
	_, err = EncodeFilePurgeExpiredContentCheckpoint(FilePurgeExpiredContentCheckpoint{Cursor: string(make([]byte, 300))})
	require.Error(t, err, "accepted oversized cursor")
	_, err = EncodeFilePurgeExpiredContentCheckpoint(FilePurgeExpiredContentCheckpoint{Examined: 2, StarterExamined: 2, AttemptExamined: 1})
	require.Error(t, err, "accepted phase counts beyond the examined total")

	continuation, err := EncodeFilePurgeExpiredContentCommand(FilePurgeExpiredContentCommand{BatchSize: 50, Cursor: "archived:" + model.NewID()})
	require.NoError(t, err)
	_, err = DecodeFilePurgeExpiredContentCommand(continuation)
	require.NoError(t, err)
	_, err = EncodeFilePurgeExpiredContentCommand(FilePurgeExpiredContentCommand{BatchSize: 50, Cursor: "path:secret"})
	require.Error(t, err, "accepted an invalid continuation cursor")
}

type purgeStoreFake struct {
	candidates []store.FilePurgeCandidate
	claimed    []store.FilePurgeCandidate
	completed  []store.FilePurgeClaim
	claimErrAt int
	order      *[]string
	drain      bool
}

func (f *purgeStoreFake) ListPurgeCandidates(_ context.Context, request *store.FilePurgeCandidateRequest) (*store.FilePurgeCandidatePage, error) {
	result := make([]store.FilePurgeCandidate, 0, request.Limit)
	for _, candidate := range f.candidates {
		if candidate.Cursor > request.After {
			result = append(result, candidate)
		}
		if len(result) == request.Limit {
			break
		}
	}
	return &store.FilePurgeCandidatePage{Candidates: result}, nil
}
func (f *purgeStoreFake) ClaimPurgeCandidate(_ context.Context, candidate *store.FilePurgeCandidate) (*store.FilePurgeClaim, error) {
	f.claimed = append(f.claimed, *candidate)
	if f.claimErrAt > 0 && len(f.claimed) == f.claimErrAt {
		return nil, store.NewErrConflict("file_revision", "retained", nil)
	}
	return &store.FilePurgeClaim{ID: model.NewID(), Candidate: *candidate}, nil
}
func (f *purgeStoreFake) CompletePurge(_ context.Context, claim *store.FilePurgeClaim) error {
	f.completed = append(f.completed, *claim)
	if f.order != nil {
		*f.order = append(*f.order, "generic-complete")
	}
	if f.drain {
		f.candidates = slices.DeleteFunc(f.candidates, func(candidate store.FilePurgeCandidate) bool {
			return candidate.Cursor == claim.Candidate.Cursor
		})
	}
	return nil
}

type purgeContentFake struct {
	removed   []model.FileRevisionID
	abandoned []model.FileRevisionID
	manifests []model.FileRevisionID
	failAt    int
}

func (f *purgeContentFake) remove(revisionID model.FileRevisionID) error {
	f.removed = append(f.removed, revisionID)
	if f.failAt > 0 && len(f.removed) == f.failAt {
		return errors.New("backend unavailable")
	}
	return nil
}

type starterWorkspaceCleanupStoreFake struct {
	objects     []model.StarterWorkspaceObject
	claimLimits []int
	claimTokens []string
	completed   []model.StarterWorkspaceObjectID
	released    []model.StarterWorkspaceObjectID
	completeErr error
	releaseErr  error
	order       *[]string
	drain       bool
}

func (f *starterWorkspaceCleanupStoreFake) ClaimObjectsForCleanup(_ context.Context, limit int, token string) ([]model.StarterWorkspaceObject, error) {
	f.claimLimits = append(f.claimLimits, limit)
	f.claimTokens = append(f.claimTokens, token)
	if len(f.objects) < limit {
		limit = len(f.objects)
	}
	return append([]model.StarterWorkspaceObject(nil), f.objects[:limit]...), nil
}
func (f *starterWorkspaceCleanupStoreFake) CompleteObjectCleanup(_ context.Context, objectID model.StarterWorkspaceObjectID, _ string) error {
	f.completed = append(f.completed, objectID)
	if f.order != nil {
		*f.order = append(*f.order, "workspace-complete")
	}
	if f.drain && f.completeErr == nil {
		f.objects = slices.DeleteFunc(f.objects, func(object model.StarterWorkspaceObject) bool { return object.ID == objectID })
	}
	return f.completeErr
}
func (f *starterWorkspaceCleanupStoreFake) ReleaseObjectCleanup(_ context.Context, objectID model.StarterWorkspaceObjectID, _ string) error {
	f.released = append(f.released, objectID)
	return f.releaseErr
}

type starterWorkspaceContentPurgerFake struct {
	removed []model.StarterWorkspaceObjectID
	err     error
	order   *[]string
}

type attemptWorkspaceCleanupStoreFake struct {
	objects     []model.AttemptWorkspaceObject
	claimLimits []int
	claimTokens []string
	completed   []model.AttemptWorkspaceObjectID
	released    []model.AttemptWorkspaceObjectID
	order       *[]string
	drain       bool
}

func (f *attemptWorkspaceCleanupStoreFake) ClaimObjectsForCleanup(_ context.Context, limit int, token string) ([]model.AttemptWorkspaceObject, error) {
	f.claimLimits = append(f.claimLimits, limit)
	f.claimTokens = append(f.claimTokens, token)
	if len(f.objects) < limit {
		limit = len(f.objects)
	}
	return append([]model.AttemptWorkspaceObject(nil), f.objects[:limit]...), nil
}

func (f *attemptWorkspaceCleanupStoreFake) CompleteObjectCleanup(_ context.Context, objectID model.AttemptWorkspaceObjectID, _ string) error {
	f.completed = append(f.completed, objectID)
	if f.order != nil {
		*f.order = append(*f.order, "attempt-workspace-complete")
	}
	if f.drain {
		f.objects = slices.DeleteFunc(f.objects, func(object model.AttemptWorkspaceObject) bool { return object.ID == objectID })
	}
	return nil
}

func (f *attemptWorkspaceCleanupStoreFake) ReleaseObjectCleanup(_ context.Context, objectID model.AttemptWorkspaceObjectID, _ string) error {
	f.released = append(f.released, objectID)
	return nil
}

type attemptWorkspaceContentPurgerFake struct {
	removed []model.AttemptWorkspaceObjectID
	order   *[]string
	errAt   int
}

func (f *attemptWorkspaceContentPurgerFake) RemoveAttemptWorkspaceObject(_ context.Context, objectID model.AttemptWorkspaceObjectID) error {
	f.removed = append(f.removed, objectID)
	if f.order != nil {
		*f.order = append(*f.order, "attempt-workspace-remove")
	}
	if f.errAt > 0 && len(f.removed) == f.errAt {
		return errors.New("Attempt Workspace backend unavailable")
	}
	return nil
}

func (f *starterWorkspaceContentPurgerFake) RemoveStarterWorkspaceObject(_ context.Context, objectID model.StarterWorkspaceObjectID) error {
	f.removed = append(f.removed, objectID)
	if f.order != nil {
		*f.order = append(*f.order, "workspace-remove")
	}
	return f.err
}

func (f *purgeContentFake) PurgeAbandonedFileRevision(_ context.Context, revisionID model.FileRevisionID) error {
	f.abandoned = append(f.abandoned, revisionID)
	return f.remove(revisionID)
}

func (f *purgeContentFake) RemoveFileRevisionRenditions(_ context.Context, revisionID model.FileRevisionID, _ []model.FileRenditionID) error {
	f.manifests = append(f.manifests, revisionID)
	return f.remove(revisionID)
}

func TestFilePurgeHandlerCheckpointsOnlyAfterContentAndMetadataArePurged(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	firstCursor, secondCursor := "lease:"+model.NewID(), "lease:"+model.NewID()
	if secondCursor < firstCursor {
		firstCursor, secondCursor = secondCursor, firstCursor
	}
	first := store.FilePurgeCandidate{Cursor: firstCursor, Kind: store.FilePurgeCandidateExpiredLease, LeaseID: model.NewUploadLeaseID(), RevisionID: model.NewFileRevisionID()}
	second := store.FilePurgeCandidate{Cursor: secondCursor, Kind: store.FilePurgeCandidateExpiredLease, LeaseID: model.NewUploadLeaseID(), RevisionID: model.NewFileRevisionID()}
	persistence := &purgeStoreFake{candidates: []store.FilePurgeCandidate{first, second}}
	content := &purgeContentFake{failAt: 2}
	handler := testFilePurgeHandler(persistence, content, nil, nil, nil, nil)
	record, err := model.NewJob(model.NewJobID(), model.JobTypeFilePurgeExpiredContent, mustPurgeCommand(t, 2), "daily", now, now, 5)
	require.NoError(t, err)
	var checkpoints []job.CheckpointValue
	outcome := handler.Run(context.Background(), testJobExecution(record, allowJobWorkReservation(), func(_ context.Context, value job.CheckpointValue) error {
		checkpoints = append(checkpoints, value)
		return nil
	}))
	require.Equal(t, job.OutcomeRetryableFailure, outcome.Kind)
	require.Equal(t, "file.backend_unavailable", outcome.PublicErrorCode)
	require.Len(t, persistence.completed, 1)
	require.Equal(t, first.Cursor, persistence.completed[0].Candidate.Cursor)
	require.Len(t, checkpoints, 1, "checkpoints")
	got, err := DecodeFilePurgeExpiredContentCheckpoint(checkpoints[0].Document)
	require.NoError(t, err, "checkpoint = %#v", got)
	require.Equal(t, first.Cursor, got.Cursor, "checkpoint = %#v", got)
	require.Equal(t, int64(1), got.Purged, "checkpoint = %#v", got)
	require.Equal(t, int64(1), got.Examined, "checkpoint = %#v", got)
	require.NotNil(t, checkpoints[0].Progress)
	require.Equal(t, int64(1), checkpoints[0].Progress.Current)
	require.Equal(t, int64(2), checkpoints[0].Progress.Total)
}

func TestFilePurgeHandlerSelectsDeletionByAuthoritativeCandidateKind(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	expired := store.FilePurgeCandidate{Cursor: "lease:" + model.NewID(), Kind: store.FilePurgeCandidateExpiredLease, LeaseID: model.NewUploadLeaseID(), RevisionID: model.NewFileRevisionID()}
	archived := store.FilePurgeCandidate{Cursor: "archived:" + model.NewID(), Kind: store.FilePurgeCandidateArchivedCustom, EntryID: model.NewFileEntryID(), RevisionID: model.NewFileRevisionID(), RenditionIDs: []model.FileRenditionID{model.NewFileRenditionID()}}
	persistence := &purgeStoreFake{candidates: []store.FilePurgeCandidate{archived, expired}}
	content := &purgeContentFake{}
	record, err := model.NewJob(model.NewJobID(), model.JobTypeFilePurgeExpiredContent, mustPurgeCommand(t, 2), "daily", at, at, 5)
	require.NoError(t, err)
	outcome := testFilePurgeHandler(persistence, content, nil, nil, nil, nil).Run(context.Background(), testJobExecution(record, allowJobWorkReservation(), func(context.Context, job.CheckpointValue) error { return nil }))
	require.Equal(t, job.OutcomeSucceeded, outcome.Kind, "outcome=%#v err=%v abandoned=%#v manifests", outcome, outcome.Err, content.abandoned)
	require.Len(t, content.abandoned, 1, "outcome=%#v err=%v abandoned=%#v manifests", outcome, outcome.Err, content.abandoned)
	require.Len(t, content.manifests, 1, "outcome=%#v err=%v abandoned=%#v manifests", outcome, outcome.Err, content.abandoned)
}

func TestFilePurgeHandlerCapsWorkByDurableReservationAndCheckpoint(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	candidate := store.FilePurgeCandidate{Cursor: "lease:" + model.NewID(), Kind: store.FilePurgeCandidateExpiredLease, LeaseID: model.NewUploadLeaseID(), RevisionID: model.NewFileRevisionID()}
	persistence := &purgeStoreFake{candidates: []store.FilePurgeCandidate{candidate}}
	content := &purgeContentFake{}
	record, err := model.NewJob(model.NewJobID(), model.JobTypeFilePurgeExpiredContent, mustPurgeCommand(t, 1), "daily", now, now, 5)
	require.NoError(t, err)
	record.WorkReserved = 1
	outcome := testFilePurgeHandler(persistence, content, nil, nil, nil, nil).Run(context.Background(), testJobExecution(record, allowJobWorkReservation(), nil))
	require.Equal(t, job.OutcomeSucceeded, outcome.Kind, "outcome=%#v claimed=%#v removed", outcome, persistence.claimed)
	require.Empty(t, persistence.claimed, "outcome=%#v claimed=%#v removed", outcome, persistence.claimed)
	require.Empty(t, content.removed, "outcome=%#v claimed=%#v removed", outcome, persistence.claimed)

	record.WorkReserved = 0
	record.Checkpoint, err = EncodeFilePurgeExpiredContentCheckpoint(FilePurgeExpiredContentCheckpoint{Cursor: candidate.Cursor, Examined: 1, Purged: 1})
	require.NoError(t, err)
	outcome = testFilePurgeHandler(persistence, content, nil, nil, nil, nil).Run(context.Background(), testJobExecution(record, allowJobWorkReservation(), nil))
	require.Equal(t, job.OutcomeSucceeded, outcome.Kind, "checkpoint cap outcome=%#v claimed=%#v removed", outcome, persistence.claimed)
	require.Empty(t, persistence.claimed, "checkpoint cap outcome=%#v claimed=%#v removed", outcome, persistence.claimed)
	require.Empty(t, content.removed, "checkpoint cap outcome=%#v claimed=%#v removed", outcome, persistence.claimed)
}

func TestFilePurgeHandlerClaimsBeforeDeletingAndDoesNotCountClaimConflicts(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	firstCursor, secondCursor := "lease:"+model.NewID(), "lease:"+model.NewID()
	if secondCursor < firstCursor {
		firstCursor, secondCursor = secondCursor, firstCursor
	}
	first := store.FilePurgeCandidate{Cursor: firstCursor, Kind: store.FilePurgeCandidateExpiredLease, LeaseID: model.NewUploadLeaseID(), EntryID: model.NewFileEntryID(), RevisionID: model.NewFileRevisionID()}
	second := store.FilePurgeCandidate{Cursor: secondCursor, Kind: store.FilePurgeCandidateExpiredLease, LeaseID: model.NewUploadLeaseID(), EntryID: model.NewFileEntryID(), RevisionID: model.NewFileRevisionID()}
	persistence := &purgeStoreFake{candidates: []store.FilePurgeCandidate{first, second}, claimErrAt: 1}
	content := &purgeContentFake{}
	handler := testFilePurgeHandler(persistence, content, nil, nil, nil, nil)
	record, err := model.NewJob(model.NewJobID(), model.JobTypeFilePurgeExpiredContent, mustPurgeCommand(t, 2), "daily", now, now, 5)
	require.NoError(t, err)
	var checkpoints []job.CheckpointValue
	outcome := handler.Run(context.Background(), testJobExecution(record, allowJobWorkReservation(), func(_ context.Context, value job.CheckpointValue) error {
		checkpoints = append(checkpoints, value)
		return nil
	}))
	require.Equal(t, job.OutcomeSucceeded, outcome.Kind)
	require.Len(t, content.removed, 1)
	require.Equal(t, second.RevisionID, content.removed[0])
	require.Len(t, persistence.completed, 1)
	require.Equal(t, second.RevisionID, persistence.completed[0].Candidate.RevisionID)
	require.Len(t, checkpoints, 2, "checkpoints")
	got, err := DecodeFilePurgeExpiredContentCheckpoint(checkpoints[1].Document)
	require.NoError(t, err, "checkpoint = %#v", got)
	require.Equal(t, second.Cursor, got.Cursor, "checkpoint = %#v", got)
	require.Equal(t, int64(2), got.Examined, "checkpoint = %#v", got)
	require.Equal(t, int64(1), got.Purged, "checkpoint = %#v", got)
	require.NotNil(t, checkpoints[1].Progress)
	require.Equal(t, int64(2), checkpoints[1].Progress.Current)
	require.Equal(t, int64(2), checkpoints[1].Progress.Total)
}

func TestFilePurgeHandlerResumesWithCumulativeProgress(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	candidate := store.FilePurgeCandidate{Cursor: "lease:" + model.NewID(), Kind: store.FilePurgeCandidateExpiredLease, LeaseID: model.NewUploadLeaseID(), EntryID: model.NewFileEntryID(), RevisionID: model.NewFileRevisionID()}
	persistence := &purgeStoreFake{candidates: []store.FilePurgeCandidate{candidate}}
	handler := testFilePurgeHandler(persistence, &purgeContentFake{}, nil, nil, nil, nil)
	record, err := model.NewJob(model.NewJobID(), model.JobTypeFilePurgeExpiredContent, mustPurgeCommand(t, 10), "daily", now, now, 5)
	require.NoError(t, err)
	record.WorkReserved = 7
	record.Checkpoint, err = EncodeFilePurgeExpiredContentCheckpoint(FilePurgeExpiredContentCheckpoint{Cursor: "archived:" + model.NewID(), Examined: 7, Purged: 5})
	require.NoError(t, err)
	var checkpoint job.CheckpointValue
	outcome := handler.Run(context.Background(), testJobExecution(record, allowJobWorkReservation(), func(_ context.Context, value job.CheckpointValue) error {
		checkpoint = value
		return nil
	}))
	require.Equal(t, job.OutcomeSucceeded, outcome.Kind)
	got, err := DecodeFilePurgeExpiredContentCheckpoint(checkpoint.Document)
	require.NoError(t, err, "checkpoint = %#v", got)
	require.Equal(t, int64(8), got.Examined, "checkpoint = %#v", got)
	require.Equal(t, int64(6), got.Purged, "checkpoint = %#v", got)
	require.NotNil(t, checkpoint.Progress)
	require.Equal(t, int64(8), checkpoint.Progress.Current)
	require.Equal(t, int64(8), checkpoint.Progress.Total)
}

func TestFilePurgeHandlerRunsStarterWorkspaceCleanupAndGenericPurgeWithinOneBatch(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	var order []string
	candidate := store.FilePurgeCandidate{Cursor: "lease:" + model.NewID(), Kind: store.FilePurgeCandidateExpiredLease, LeaseID: model.NewUploadLeaseID(), RevisionID: model.NewFileRevisionID()}
	files := &purgeStoreFake{candidates: []store.FilePurgeCandidate{candidate}, order: &order}
	genericContent := &purgeContentFake{}
	workspaceObject := cleanupWorkspaceObject(at)
	workspace := &starterWorkspaceCleanupStoreFake{objects: []model.StarterWorkspaceObject{workspaceObject}, order: &order}
	workspaceContent := &starterWorkspaceContentPurgerFake{order: &order}
	record, err := model.NewJob(model.NewJobID(), model.JobTypeFilePurgeExpiredContent, mustPurgeCommand(t, 2), "daily", at, at, 5)
	require.NoError(t, err)
	var checkpoints []job.CheckpointValue
	execution := testWorkspaceCleanupExecution(t, record, at, allowJobWorkReservation(), func(_ context.Context, value job.CheckpointValue) error {
		checkpoints = append(checkpoints, value)
		return nil
	})
	outcome := testFilePurgeHandler(files, genericContent, workspace, workspaceContent, nil, nil).Run(context.Background(), execution)
	require.Equal(t, job.OutcomeSucceeded, outcome.Kind, "outcome=%#v claim limits", outcome)
	require.Len(t, workspace.claimLimits, 1, "outcome=%#v claim limits", outcome)
	require.Equal(t, 1, workspace.claimLimits[0], "outcome=%#v claim limits", outcome)
	require.Len(t, workspace.claimTokens, 1)
	require.Equal(t, string(execution.Attempt.ClaimToken), workspace.claimTokens[0])
	// Contract: Starter Workspace bytes are removed before their metadata
	// completes, so a failed removal leaves the claim fenced.
	require.Equal(t, []string{"workspace-remove", "workspace-complete", "generic-complete"}, order)
	require.Len(t, checkpoints, 2, "checkpoints")
	last, err := DecodeFilePurgeExpiredContentCheckpoint(checkpoints[1].Document)
	require.NoError(t, err, "checkpoint=%#v err", last)
	require.Equal(t, int64(2), last.Examined, "checkpoint=%#v err", last)
	require.Equal(t, int64(2), last.Purged, "checkpoint=%#v err", last)
	require.Equal(t, candidate.Cursor, last.Cursor, "checkpoint=%#v err", last)
}

func TestFilePurgeHandlerFencesUnknownWorkspaceRemovalAndReleasesUnprocessedClaims(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	first, second := cleanupWorkspaceObject(at), cleanupWorkspaceObject(at)
	workspace := &starterWorkspaceCleanupStoreFake{objects: []model.StarterWorkspaceObject{first, second}}
	workspaceContent := &starterWorkspaceContentPurgerFake{err: errors.New("opaque backend failure")}
	record, err := model.NewJob(model.NewJobID(), model.JobTypeFilePurgeExpiredContent, mustPurgeCommand(t, 4), "daily", at, at, 5)
	require.NoError(t, err)
	outcome := testFilePurgeHandler(&purgeStoreFake{}, &purgeContentFake{}, workspace, workspaceContent, nil, nil).Run(
		context.Background(), testWorkspaceCleanupExecution(t, record, at, allowJobWorkReservation(), func(context.Context, job.CheckpointValue) error { return nil }))
	require.Equal(t, job.OutcomeRetryableFailure, outcome.Kind)
	require.Equal(t, "file.backend_unavailable", outcome.PublicErrorCode)
	require.Empty(t, workspace.completed, "completed")
	require.Len(t, workspace.released, 1, "completed")
	require.Equal(t, second.ID, workspace.released[0], "completed")
}

func TestFilePurgeHandlerLeavesRemovedWorkspaceClaimFencedWhenMetadataCommitFails(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	first, second := cleanupWorkspaceObject(at), cleanupWorkspaceObject(at)
	workspace := &starterWorkspaceCleanupStoreFake{objects: []model.StarterWorkspaceObject{first, second}, completeErr: errors.New("database unavailable")}
	workspaceContent := &starterWorkspaceContentPurgerFake{}
	record, err := model.NewJob(model.NewJobID(), model.JobTypeFilePurgeExpiredContent, mustPurgeCommand(t, 4), "daily", at, at, 5)
	require.NoError(t, err)
	outcome := testFilePurgeHandler(&purgeStoreFake{}, &purgeContentFake{}, workspace, workspaceContent, nil, nil).Run(
		context.Background(), testWorkspaceCleanupExecution(t, record, at, allowJobWorkReservation(), func(context.Context, job.CheckpointValue) error { return nil }))
	require.Equal(t, job.OutcomeRetryableFailure, outcome.Kind)
	require.Equal(t, "database.unavailable", outcome.PublicErrorCode)
	require.Len(t, workspaceContent.removed, 1, "removed")
	require.Equal(t, first.ID, workspaceContent.removed[0], "removed")
	require.Len(t, workspace.released, 1, "removed")
	require.Equal(t, second.ID, workspace.released[0], "removed")
}

func TestFilePurgeHandlerRunsEveryPhaseWithinOneBatch(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	var order []string
	candidate := store.FilePurgeCandidate{Cursor: "lease:" + model.NewID(), Kind: store.FilePurgeCandidateExpiredLease,
		LeaseID: model.NewUploadLeaseID(), RevisionID: model.NewFileRevisionID()}
	files := &purgeStoreFake{candidates: []store.FilePurgeCandidate{candidate}, order: &order}
	starterObject := cleanupWorkspaceObject(at)
	starter := &starterWorkspaceCleanupStoreFake{objects: []model.StarterWorkspaceObject{starterObject}, order: &order}
	starterContent := &starterWorkspaceContentPurgerFake{order: &order}
	attemptObject := cleanupAttemptWorkspaceObject(at)
	attempts := &attemptWorkspaceCleanupStoreFake{objects: []model.AttemptWorkspaceObject{attemptObject}, order: &order}
	attemptContent := &attemptWorkspaceContentPurgerFake{order: &order}
	record, err := model.NewJob(model.NewJobID(), model.JobTypeFilePurgeExpiredContent, mustPurgeCommand(t, 3), "daily", at, at, 5)
	require.NoError(t, err)
	var checkpoints []job.CheckpointValue
	execution := testWorkspaceCleanupExecution(t, record, at, allowJobWorkReservation(), func(_ context.Context, value job.CheckpointValue) error {
		checkpoints = append(checkpoints, value)
		return nil
	})
	outcome := testFilePurgeHandler(files, &purgeContentFake{}, starter, starterContent, attempts, attemptContent).
		Run(context.Background(), execution)
	require.Equal(t, job.OutcomeSucceeded, outcome.Kind, "outcome=%#v claim limits", outcome)
	require.Len(t, attempts.claimLimits, 1, "outcome=%#v claim limits", outcome)
	require.Equal(t, 1, attempts.claimLimits[0], "outcome=%#v claim limits", outcome)
	require.Len(t, attempts.claimTokens, 1)
	require.Equal(t, string(execution.Attempt.ClaimToken), attempts.claimTokens[0])
	// Contract: each workspace family removes bytes before its metadata
	// completes, so a failed removal leaves the claim fenced.
	require.Equal(t, []string{"workspace-remove", "workspace-complete", "attempt-workspace-remove", "attempt-workspace-complete", "generic-complete"}, order)
	require.Len(t, checkpoints, 3, "checkpoints")
	last, err := DecodeFilePurgeExpiredContentCheckpoint(checkpoints[2].Document)
	require.NoError(t, err, "checkpoint=%#v err", last)
	require.Equal(t, int64(3), last.Examined, "checkpoint=%#v err", last)
	require.Equal(t, int64(3), last.Purged, "checkpoint=%#v err", last)
	require.Equal(t, candidate.Cursor, last.Cursor, "checkpoint=%#v err", last)
}

func TestFilePurgeHandlerFencesUnknownAttemptWorkspaceRemovalAndReleasesUntouchedClaims(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	first, second := cleanupAttemptWorkspaceObject(at), cleanupAttemptWorkspaceObject(at)
	attempts := &attemptWorkspaceCleanupStoreFake{objects: []model.AttemptWorkspaceObject{first, second}}
	content := &attemptWorkspaceContentPurgerFake{errAt: 1}
	record, err := model.NewJob(model.NewJobID(), model.JobTypeFilePurgeExpiredContent, mustPurgeCommand(t, 6), "daily", at, at, 5)
	require.NoError(t, err)
	outcome := testFilePurgeHandler(&purgeStoreFake{}, &purgeContentFake{}, &starterWorkspaceCleanupStoreFake{},
		&starterWorkspaceContentPurgerFake{}, attempts, content).Run(context.Background(),
		testWorkspaceCleanupExecution(t, record, at, allowJobWorkReservation(), func(context.Context, job.CheckpointValue) error { return nil }))
	require.Equal(t, job.OutcomeRetryableFailure, outcome.Kind, "outcome=%#v removed=%v completed=%v released", outcome, content.removed, attempts.completed)
	require.Len(t, content.removed, 1, "outcome=%#v removed=%v completed=%v released", outcome, content.removed, attempts.completed)
	require.Equal(t, first.ID, content.removed[0], "outcome=%#v removed=%v completed=%v released", outcome, content.removed, attempts.completed)
	require.Empty(t, attempts.completed, "outcome=%#v removed=%v completed=%v released", outcome, content.removed, attempts.completed)
	require.Len(t, attempts.released, 1, "outcome=%#v removed=%v completed=%v released", outcome, content.removed, attempts.completed)
	require.Equal(t, second.ID, attempts.released[0], "outcome=%#v removed=%v completed=%v released", outcome, content.removed, attempts.completed)
}

func cleanupWorkspaceObject(at time.Time) model.StarterWorkspaceObject {
	return model.StarterWorkspaceObject{ID: model.NewStarterWorkspaceObjectID(), ExamID: model.NewExamID(), CreatedByUserID: model.NewUserID(),
		CreatedAt: at, UpdatedAt: at, ExpiresAt: at.Add(-time.Hour), State: model.StarterWorkspaceObjectClaimed,
		ReclaimAfter: model.OptionalTimeFrom(at.Add(-time.Hour)), ClaimToken: strings.Repeat("a", 64), ClaimedAt: model.OptionalTimeFrom(at)}
}

func cleanupAttemptWorkspaceObject(at time.Time) model.AttemptWorkspaceObject {
	return model.AttemptWorkspaceObject{ID: model.NewAttemptWorkspaceObjectID(), WorkspaceID: model.NewExamAttemptWorkspaceID(),
		StorageOrigin: model.AttemptWorkspaceStorageAttempt, State: model.AttemptWorkspaceObjectClaimed,
		CreatedAt: at.Add(-2 * time.Hour), UpdatedAt: at, ReclaimAfter: model.OptionalTimeFrom(at.Add(-time.Hour)),
		ClaimToken: strings.Repeat("a", 64), ClaimedAt: model.OptionalTimeFrom(at)}
}

func testWorkspaceCleanupExecution(t *testing.T, record *model.Job, at time.Time, reserve func(context.Context, int, int) (bool, error), checkpoint func(context.Context, job.CheckpointValue) error) job.Execution {
	t.Helper()
	attempt, err := model.NewJobAttempt(model.NewJobAttemptID(), record.ID, 1, "test-node", model.JobClaimToken(strings.Repeat("a", 64)), at, at.Add(time.Minute))
	require.NoError(t, err)
	return job.NewExecution(record, attempt, checkpoint, reserve)
}

func mustPurgeCommand(t *testing.T, size int) json.RawMessage {
	t.Helper()
	value, err := EncodeFilePurgeExpiredContentCommand(FilePurgeExpiredContentCommand{BatchSize: size})
	require.NoError(t, err)
	return value
}

func testFilePurgeHandler(files FilePurgeStore, content FileRevisionContentPurger,
	starterWorkspace StarterWorkspaceCleanupStore, starterWorkspaceContent StarterWorkspaceObjectPurger,
	attemptWorkspace AttemptWorkspaceCleanupStore, attemptWorkspaceContent AttemptWorkspaceObjectPurger,
) filePurgeExpiredContentHandler {
	return filePurgeExpiredContentHandler{files: files, content: content,
		starterWorkspace: starterWorkspace, starterWorkspaceContent: starterWorkspaceContent,
		attemptWorkspace: attemptWorkspace, attemptWorkspaceContent: attemptWorkspaceContent,
		continuations: &deduplicatingJobEnqueuerFake{jobs: map[string]*model.Job{}},
		now:           func() time.Time { return time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC) }}
}

// A FileRevision backlog larger than the batch used to consume every daily
// batch, so workspace cleanup never ran.
func TestFilePurgeHandlerSharesTheBatchSoWorkspacePhasesCannotStarve(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	files := &purgeStoreFake{candidates: filePurgeCandidates(100)}
	starter := &starterWorkspaceCleanupStoreFake{objects: []model.StarterWorkspaceObject{cleanupWorkspaceObject(at)}}
	attempts := &attemptWorkspaceCleanupStoreFake{objects: []model.AttemptWorkspaceObject{cleanupAttemptWorkspaceObject(at)}}
	handler := testFilePurgeHandler(files, &purgeContentFake{}, starter, &starterWorkspaceContentPurgerFake{},
		attempts, &attemptWorkspaceContentPurgerFake{})
	record, err := model.NewJob(model.NewJobID(), model.JobTypeFilePurgeExpiredContent, mustPurgeCommand(t, 50), "daily", at, at, 5)
	require.NoError(t, err)

	outcome := handler.Run(context.Background(), testWorkspaceCleanupExecution(t, record, at, allowJobWorkReservation(),
		func(context.Context, job.CheckpointValue) error { return nil }))
	require.Equal(t, job.OutcomeSucceeded, outcome.Kind, "outcome=%#v", outcome)
	require.Len(t, starter.completed, 1, "Starter Workspace objects purged")
	require.Len(t, attempts.completed, 1, "Attempt Workspace objects purged")
	// The FileRevision phase takes what the workspace phases left unused, so
	// the batch is still fully used.
	require.Len(t, files.completed, 48, "FileRevisions purged")
}

func TestFilePurgeHandlerContinuesUntilEveryPhaseDrains(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	files := &purgeStoreFake{candidates: filePurgeCandidates(70), drain: true}
	starter := &starterWorkspaceCleanupStoreFake{drain: true}
	for range 40 {
		starter.objects = append(starter.objects, cleanupWorkspaceObject(at))
	}
	attempts := &attemptWorkspaceCleanupStoreFake{drain: true}
	for range 5 {
		attempts.objects = append(attempts.objects, cleanupAttemptWorkspaceObject(at))
	}
	jobs := &deduplicatingJobEnqueuerFake{jobs: map[string]*model.Job{}}
	handler := testFilePurgeHandler(files, &purgeContentFake{}, starter, &starterWorkspaceContentPurgerFake{},
		attempts, &attemptWorkspaceContentPurgerFake{})
	handler.continuations = jobs
	handler.now = func() time.Time { return at }
	record, err := model.NewJob(model.NewJobID(), model.JobTypeFilePurgeExpiredContent, mustPurgeCommand(t, 30), "daily", at, at, 5)
	require.NoError(t, err)

	runs := 0
	for record != nil {
		runs++
		require.LessOrEqual(t, runs, 10, "the continuation chain did not end")
		outcome := handler.Run(context.Background(), testWorkspaceCleanupExecution(t, record, at, allowJobWorkReservation(),
			func(context.Context, job.CheckpointValue) error { return nil }))
		require.Equal(t, job.OutcomeSucceeded, outcome.Kind, "run %d outcome=%#v", runs, outcome)
		successor := jobs.jobs[string(model.JobTypeFilePurgeExpiredContent)+":file-purge-expired-content:after:"+record.ID.String()]
		if successor != nil {
			require.Equal(t, model.JobDedupePermanent, successor.DedupePolicy)
			require.True(t, successor.AvailableAt.After(at), "successor available at %v", successor.AvailableAt)
		}
		record = successor
	}
	require.Empty(t, files.candidates, "FileRevision candidates left")
	require.Empty(t, starter.objects, "Starter Workspace objects left")
	require.Empty(t, attempts.objects, "Attempt Workspace objects left")
	// 115 objects at 30 per batch need four full batches and one that finds
	// the phases drained and ends the chain.
	require.Equal(t, 5, runs)
}

func TestFilePurgeHandlerDoesNotContinueADrainedBatch(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	jobs := &deduplicatingJobEnqueuerFake{jobs: map[string]*model.Job{}}
	handler := testFilePurgeHandler(&purgeStoreFake{candidates: filePurgeCandidates(3)}, &purgeContentFake{},
		&starterWorkspaceCleanupStoreFake{}, &starterWorkspaceContentPurgerFake{}, nil, nil)
	handler.continuations = jobs
	record, err := model.NewJob(model.NewJobID(), model.JobTypeFilePurgeExpiredContent, mustPurgeCommand(t, 50), "daily", at, at, 5)
	require.NoError(t, err)
	outcome := handler.Run(context.Background(), testWorkspaceCleanupExecution(t, record, at, allowJobWorkReservation(),
		func(context.Context, job.CheckpointValue) error { return nil }))
	require.Equal(t, job.OutcomeSucceeded, outcome.Kind, "outcome=%#v", outcome)
	require.Empty(t, jobs.jobs, "continuations")
}

// A continuation resumes the FileRevision phase after its predecessor's cursor
// instead of re-examining candidates it skipped.
func TestFilePurgeContinuationCarriesTheFileRevisionCursor(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	candidates := filePurgeCandidates(4)
	jobs := &deduplicatingJobEnqueuerFake{jobs: map[string]*model.Job{}}
	handler := testFilePurgeHandler(&purgeStoreFake{candidates: candidates}, &purgeContentFake{}, nil, nil, nil, nil)
	handler.continuations = jobs
	record, err := model.NewJob(model.NewJobID(), model.JobTypeFilePurgeExpiredContent, mustPurgeCommand(t, 2), "daily", at, at, 5)
	require.NoError(t, err)
	outcome := handler.Run(context.Background(), testJobExecution(record, allowJobWorkReservation(),
		func(context.Context, job.CheckpointValue) error { return nil }))
	require.Equal(t, job.OutcomeSucceeded, outcome.Kind, "outcome=%#v", outcome)
	require.Len(t, jobs.jobs, 1, "continuations")
	for _, successor := range jobs.jobs {
		command, err := DecodeFilePurgeExpiredContentCommand(successor.Command)
		require.NoError(t, err)
		require.Equal(t, FilePurgeExpiredContentCommand{BatchSize: 2, Cursor: candidates[1].Cursor}, command)
	}
}

func TestFilePurgeHandlerRetriesWhenTheContinuationCannotBeEnqueued(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	handler := testFilePurgeHandler(&purgeStoreFake{candidates: filePurgeCandidates(2)}, &purgeContentFake{}, nil, nil, nil, nil)
	handler.continuations = failingJobEnqueuer{}
	record, err := model.NewJob(model.NewJobID(), model.JobTypeFilePurgeExpiredContent, mustPurgeCommand(t, 1), "daily", at, at, 5)
	require.NoError(t, err)
	outcome := handler.Run(context.Background(), testJobExecution(record, allowJobWorkReservation(),
		func(context.Context, job.CheckpointValue) error { return nil }))
	require.Equal(t, job.OutcomeRetryableFailure, outcome.Kind)
	require.Equal(t, "database.unavailable", outcome.PublicErrorCode)
}

type failingJobEnqueuer struct{}

func (failingJobEnqueuer) Enqueue(context.Context, *store.JobEnqueue) (*model.Job, bool, error) {
	return nil, false, errors.New("database unavailable")
}

// filePurgeCandidates returns count expired-lease candidates in cursor order.
func filePurgeCandidates(count int) []store.FilePurgeCandidate {
	candidates := make([]store.FilePurgeCandidate, 0, count)
	for range count {
		candidates = append(candidates, store.FilePurgeCandidate{
			Cursor: "lease:" + model.NewID(), Kind: store.FilePurgeCandidateExpiredLease,
			LeaseID: model.NewUploadLeaseID(), RevisionID: model.NewFileRevisionID(),
		})
	}
	slices.SortFunc(candidates, func(a, b store.FilePurgeCandidate) int { return strings.Compare(a.Cursor, b.Cursor) })
	return candidates
}
