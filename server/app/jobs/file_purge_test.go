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
	"strings"
	"testing"
	"time"

	jobengine "github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestFilePurgeCommandAndCheckpointAreVersionedTypedAndBounded(t *testing.T) {
	t.Parallel()
	command, err := EncodeFilePurgeExpiredContentCommand(FilePurgeExpiredContentCommandV1{BatchSize: 50})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeFilePurgeExpiredContentCommand(1, command)
	if err != nil || decoded.BatchSize != 50 {
		t.Fatalf("decode command = %#v, %v", decoded, err)
	}
	if _, err = EncodeFilePurgeExpiredContentCommand(FilePurgeExpiredContentCommandV1{BatchSize: 101}); err == nil {
		t.Fatal("accepted oversized batch")
	}
	if _, err = DecodeFilePurgeExpiredContentCommand(2, command); err == nil {
		t.Fatal("accepted unsupported command version")
	}
	if _, err = DecodeFilePurgeExpiredContentCommand(1, json.RawMessage(`{"batch_size":1,"path":"secret"}`)); err == nil {
		t.Fatal("accepted unknown field")
	}

	checkpoint, err := EncodeFilePurgeExpiredContentCheckpoint(FilePurgeExpiredContentCheckpointV1{Cursor: "lease:" + model.NewId(), Examined: 2, Purged: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = DecodeFilePurgeExpiredContentCheckpoint(1, checkpoint); err != nil {
		t.Fatal(err)
	}
	if _, err = EncodeFilePurgeExpiredContentCheckpoint(FilePurgeExpiredContentCheckpointV1{Cursor: string(make([]byte, 300))}); err == nil {
		t.Fatal("accepted oversized cursor")
	}
}

type purgeStoreFake struct {
	candidates []store.FilePurgeCandidate
	claimed    []store.FilePurgeCandidate
	completed  []store.FilePurgeClaim
	claimErrAt int
	order      *[]string
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
	return &store.FilePurgeClaim{ID: model.NewId(), Candidate: *candidate}, nil
}
func (f *purgeStoreFake) CompletePurge(_ context.Context, claim *store.FilePurgeClaim) error {
	f.completed = append(f.completed, *claim)
	if f.order != nil {
		*f.order = append(*f.order, "generic-complete")
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
	firstCursor, secondCursor := "lease:"+model.NewId(), "lease:"+model.NewId()
	if secondCursor < firstCursor {
		firstCursor, secondCursor = secondCursor, firstCursor
	}
	first := store.FilePurgeCandidate{Cursor: firstCursor, Kind: store.FilePurgeCandidateExpiredLease, LeaseID: model.NewUploadLeaseID(), RevisionID: model.NewFileRevisionID()}
	second := store.FilePurgeCandidate{Cursor: secondCursor, Kind: store.FilePurgeCandidateExpiredLease, LeaseID: model.NewUploadLeaseID(), RevisionID: model.NewFileRevisionID()}
	persistence := &purgeStoreFake{candidates: []store.FilePurgeCandidate{first, second}}
	content := &purgeContentFake{failAt: 2}
	handler := newFilePurgeExpiredContentHandler(persistence, content, nil, nil, nil, nil)
	job, err := model.NewJob(model.NewJobID(), model.JobTypeFilePurgeExpiredContent, 1, mustPurgeCommand(t, 2), "daily", now, now, 5)
	if err != nil {
		t.Fatal(err)
	}
	var checkpoints []jobengine.CheckpointValue
	outcome := handler.Run(context.Background(), testJobExecution(job, allowJobWorkReservation(), func(_ context.Context, value jobengine.CheckpointValue) error {
		checkpoints = append(checkpoints, value)
		return nil
	}))
	if outcome.Kind != jobengine.OutcomeRetryableFailure || outcome.PublicErrorCode != "file.backend_unavailable" {
		t.Fatalf("outcome = %#v", outcome)
	}
	if len(persistence.completed) != 1 || persistence.completed[0].Candidate.Cursor != first.Cursor {
		t.Fatalf("completed = %#v", persistence.completed)
	}
	if len(checkpoints) != 1 {
		t.Fatalf("checkpoints = %d", len(checkpoints))
	}
	got, err := DecodeFilePurgeExpiredContentCheckpoint(checkpoints[0].Version, checkpoints[0].Document)
	if err != nil || got.Cursor != first.Cursor || got.Purged != 1 || got.Examined != 1 {
		t.Fatalf("checkpoint = %#v, %v", got, err)
	}
	if checkpoints[0].Progress == nil || checkpoints[0].Progress.Current != 1 || checkpoints[0].Progress.Total != 2 {
		t.Fatalf("progress = %#v", checkpoints[0].Progress)
	}
}

func TestFilePurgeHandlerSelectsDeletionByAuthoritativeCandidateKind(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	expired := store.FilePurgeCandidate{Cursor: "lease:" + model.NewId(), Kind: store.FilePurgeCandidateExpiredLease, LeaseID: model.NewUploadLeaseID(), RevisionID: model.NewFileRevisionID()}
	archived := store.FilePurgeCandidate{Cursor: "archived:" + model.NewId(), Kind: store.FilePurgeCandidateArchivedCustom, EntryID: model.NewFileEntryID(), RevisionID: model.NewFileRevisionID(), RenditionIDs: []model.FileRenditionID{model.NewFileRenditionID()}}
	persistence := &purgeStoreFake{candidates: []store.FilePurgeCandidate{archived, expired}}
	content := &purgeContentFake{}
	job, err := model.NewJob(model.NewJobID(), model.JobTypeFilePurgeExpiredContent, 1, mustPurgeCommand(t, 2), "daily", at, at, 5)
	if err != nil {
		t.Fatal(err)
	}
	outcome := newFilePurgeExpiredContentHandler(persistence, content, nil, nil, nil, nil).Run(context.Background(), testJobExecution(job, allowJobWorkReservation(), func(context.Context, jobengine.CheckpointValue) error { return nil }))
	if outcome.Kind != jobengine.OutcomeSucceeded || len(content.abandoned) != 1 || len(content.manifests) != 1 {
		t.Fatalf("outcome=%#v err=%v abandoned=%#v manifests=%#v", outcome, outcome.Err, content.abandoned, content.manifests)
	}
}

func TestFilePurgeHandlerCapsWorkByDurableReservationAndCheckpoint(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	candidate := store.FilePurgeCandidate{Cursor: "lease:" + model.NewId(), Kind: store.FilePurgeCandidateExpiredLease, LeaseID: model.NewUploadLeaseID(), RevisionID: model.NewFileRevisionID()}
	persistence := &purgeStoreFake{candidates: []store.FilePurgeCandidate{candidate}}
	content := &purgeContentFake{}
	job, err := model.NewJob(model.NewJobID(), model.JobTypeFilePurgeExpiredContent, 1, mustPurgeCommand(t, 1), "daily", now, now, 5)
	if err != nil {
		t.Fatal(err)
	}
	job.WorkReserved = 1
	outcome := newFilePurgeExpiredContentHandler(persistence, content, nil, nil, nil, nil).Run(context.Background(), testJobExecution(job, allowJobWorkReservation(), nil))
	if outcome.Kind != jobengine.OutcomeSucceeded || len(persistence.claimed) != 0 || len(content.removed) != 0 {
		t.Fatalf("outcome=%#v claimed=%#v removed=%#v", outcome, persistence.claimed, content.removed)
	}

	job.WorkReserved = 0
	job.CheckpointVersion = 1
	job.Checkpoint, err = EncodeFilePurgeExpiredContentCheckpoint(FilePurgeExpiredContentCheckpointV1{Cursor: candidate.Cursor, Examined: 1, Purged: 1})
	if err != nil {
		t.Fatal(err)
	}
	outcome = newFilePurgeExpiredContentHandler(persistence, content, nil, nil, nil, nil).Run(context.Background(), testJobExecution(job, allowJobWorkReservation(), nil))
	if outcome.Kind != jobengine.OutcomeSucceeded || len(persistence.claimed) != 0 || len(content.removed) != 0 {
		t.Fatalf("checkpoint cap outcome=%#v claimed=%#v removed=%#v", outcome, persistence.claimed, content.removed)
	}
}

func TestFilePurgeHandlerClaimsBeforeDeletingAndDoesNotCountClaimConflicts(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	firstCursor, secondCursor := "lease:"+model.NewId(), "lease:"+model.NewId()
	if secondCursor < firstCursor {
		firstCursor, secondCursor = secondCursor, firstCursor
	}
	first := store.FilePurgeCandidate{Cursor: firstCursor, Kind: store.FilePurgeCandidateExpiredLease, LeaseID: model.NewUploadLeaseID(), EntryID: model.NewFileEntryID(), RevisionID: model.NewFileRevisionID()}
	second := store.FilePurgeCandidate{Cursor: secondCursor, Kind: store.FilePurgeCandidateExpiredLease, LeaseID: model.NewUploadLeaseID(), EntryID: model.NewFileEntryID(), RevisionID: model.NewFileRevisionID()}
	persistence := &purgeStoreFake{candidates: []store.FilePurgeCandidate{first, second}, claimErrAt: 1}
	content := &purgeContentFake{}
	handler := newFilePurgeExpiredContentHandler(persistence, content, nil, nil, nil, nil)
	job, err := model.NewJob(model.NewJobID(), model.JobTypeFilePurgeExpiredContent, 1, mustPurgeCommand(t, 2), "daily", now, now, 5)
	if err != nil {
		t.Fatal(err)
	}
	var checkpoints []jobengine.CheckpointValue
	outcome := handler.Run(context.Background(), testJobExecution(job, allowJobWorkReservation(), func(_ context.Context, value jobengine.CheckpointValue) error {
		checkpoints = append(checkpoints, value)
		return nil
	}))
	if outcome.Kind != jobengine.OutcomeSucceeded {
		t.Fatalf("outcome = %#v", outcome)
	}
	if len(content.removed) != 1 || content.removed[0] != second.RevisionID {
		t.Fatalf("removed = %#v", content.removed)
	}
	if len(persistence.completed) != 1 || persistence.completed[0].Candidate.RevisionID != second.RevisionID {
		t.Fatalf("completed = %#v", persistence.completed)
	}
	if len(checkpoints) != 2 {
		t.Fatalf("checkpoints = %d", len(checkpoints))
	}
	got, err := DecodeFilePurgeExpiredContentCheckpoint(checkpoints[1].Version, checkpoints[1].Document)
	if err != nil || got.Cursor != second.Cursor || got.Examined != 2 || got.Purged != 1 {
		t.Fatalf("checkpoint = %#v, %v", got, err)
	}
	if checkpoints[1].Progress == nil || checkpoints[1].Progress.Current != 2 || checkpoints[1].Progress.Total != 2 {
		t.Fatalf("progress = %#v", checkpoints[1].Progress)
	}
}

func TestFilePurgeHandlerResumesWithCumulativeProgress(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	candidate := store.FilePurgeCandidate{Cursor: "lease:" + model.NewId(), Kind: store.FilePurgeCandidateExpiredLease, LeaseID: model.NewUploadLeaseID(), EntryID: model.NewFileEntryID(), RevisionID: model.NewFileRevisionID()}
	persistence := &purgeStoreFake{candidates: []store.FilePurgeCandidate{candidate}}
	handler := newFilePurgeExpiredContentHandler(persistence, &purgeContentFake{}, nil, nil, nil, nil)
	job, err := model.NewJob(model.NewJobID(), model.JobTypeFilePurgeExpiredContent, 1, mustPurgeCommand(t, 10), "daily", now, now, 5)
	if err != nil {
		t.Fatal(err)
	}
	job.WorkReserved = 7
	job.CheckpointVersion = 1
	job.Checkpoint, err = EncodeFilePurgeExpiredContentCheckpoint(FilePurgeExpiredContentCheckpointV1{Cursor: "archived:" + model.NewId(), Examined: 7, Purged: 5})
	if err != nil {
		t.Fatal(err)
	}
	var checkpoint jobengine.CheckpointValue
	outcome := handler.Run(context.Background(), testJobExecution(job, allowJobWorkReservation(), func(_ context.Context, value jobengine.CheckpointValue) error {
		checkpoint = value
		return nil
	}))
	if outcome.Kind != jobengine.OutcomeSucceeded {
		t.Fatalf("outcome = %#v", outcome)
	}
	got, err := DecodeFilePurgeExpiredContentCheckpoint(checkpoint.Version, checkpoint.Document)
	if err != nil || got.Examined != 8 || got.Purged != 6 {
		t.Fatalf("checkpoint = %#v, %v", got, err)
	}
	if checkpoint.Progress == nil || checkpoint.Progress.Current != 8 || checkpoint.Progress.Total != 8 {
		t.Fatalf("progress = %#v", checkpoint.Progress)
	}
}

func TestFilePurgeHandlerRunsStarterWorkspaceCleanupAfterGenericPurgeWithinOneBatch(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	var order []string
	candidate := store.FilePurgeCandidate{Cursor: "lease:" + model.NewId(), Kind: store.FilePurgeCandidateExpiredLease, LeaseID: model.NewUploadLeaseID(), RevisionID: model.NewFileRevisionID()}
	files := &purgeStoreFake{candidates: []store.FilePurgeCandidate{candidate}, order: &order}
	genericContent := &purgeContentFake{}
	workspaceObject := cleanupWorkspaceObject(at)
	workspace := &starterWorkspaceCleanupStoreFake{objects: []model.StarterWorkspaceObject{workspaceObject}, order: &order}
	workspaceContent := &starterWorkspaceContentPurgerFake{order: &order}
	job, err := model.NewJob(model.NewJobID(), model.JobTypeFilePurgeExpiredContent, 1, mustPurgeCommand(t, 2), "daily", at, at, 5)
	if err != nil {
		t.Fatal(err)
	}
	var checkpoints []jobengine.CheckpointValue
	execution := testWorkspaceCleanupExecution(t, job, at, allowJobWorkReservation(), func(_ context.Context, value jobengine.CheckpointValue) error {
		checkpoints = append(checkpoints, value)
		return nil
	})
	outcome := newFilePurgeExpiredContentHandler(files, genericContent, workspace, workspaceContent, nil, nil).Run(context.Background(), execution)
	if outcome.Kind != jobengine.OutcomeSucceeded || len(workspace.claimLimits) != 1 || workspace.claimLimits[0] != 1 {
		t.Fatalf("outcome=%#v claim limits=%v", outcome, workspace.claimLimits)
	}
	if len(workspace.claimTokens) != 1 || workspace.claimTokens[0] != string(execution.Attempt.ClaimToken) {
		t.Fatalf("claim tokens=%v", workspace.claimTokens)
	}
	if got := strings.Join(order, ","); got != "generic-complete,workspace-remove,workspace-complete" {
		t.Fatalf("cleanup order=%q", got)
	}
	if len(checkpoints) != 2 {
		t.Fatalf("checkpoints=%d", len(checkpoints))
	}
	last, err := DecodeFilePurgeExpiredContentCheckpoint(checkpoints[1].Version, checkpoints[1].Document)
	if err != nil || last.Examined != 2 || last.Purged != 2 || last.Cursor != candidate.Cursor {
		t.Fatalf("checkpoint=%#v err=%v", last, err)
	}
}

func TestFilePurgeHandlerFencesUnknownWorkspaceRemovalAndReleasesUnprocessedClaims(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	first, second := cleanupWorkspaceObject(at), cleanupWorkspaceObject(at)
	workspace := &starterWorkspaceCleanupStoreFake{objects: []model.StarterWorkspaceObject{first, second}}
	workspaceContent := &starterWorkspaceContentPurgerFake{err: errors.New("opaque backend failure")}
	job, err := model.NewJob(model.NewJobID(), model.JobTypeFilePurgeExpiredContent, 1, mustPurgeCommand(t, 2), "daily", at, at, 5)
	if err != nil {
		t.Fatal(err)
	}
	outcome := newFilePurgeExpiredContentHandler(&purgeStoreFake{}, &purgeContentFake{}, workspace, workspaceContent, nil, nil).Run(
		context.Background(), testWorkspaceCleanupExecution(t, job, at, allowJobWorkReservation(), func(context.Context, jobengine.CheckpointValue) error { return nil }))
	if outcome.Kind != jobengine.OutcomeRetryableFailure || outcome.PublicErrorCode != "file.backend_unavailable" {
		t.Fatalf("outcome=%#v", outcome)
	}
	if len(workspace.completed) != 0 || len(workspace.released) != 1 || workspace.released[0] != second.ID {
		t.Fatalf("completed=%v released=%v", workspace.completed, workspace.released)
	}
}

func TestFilePurgeHandlerLeavesRemovedWorkspaceClaimFencedWhenMetadataCommitFails(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	first, second := cleanupWorkspaceObject(at), cleanupWorkspaceObject(at)
	workspace := &starterWorkspaceCleanupStoreFake{objects: []model.StarterWorkspaceObject{first, second}, completeErr: errors.New("database unavailable")}
	workspaceContent := &starterWorkspaceContentPurgerFake{}
	job, err := model.NewJob(model.NewJobID(), model.JobTypeFilePurgeExpiredContent, 1, mustPurgeCommand(t, 2), "daily", at, at, 5)
	if err != nil {
		t.Fatal(err)
	}
	outcome := newFilePurgeExpiredContentHandler(&purgeStoreFake{}, &purgeContentFake{}, workspace, workspaceContent, nil, nil).Run(
		context.Background(), testWorkspaceCleanupExecution(t, job, at, allowJobWorkReservation(), func(context.Context, jobengine.CheckpointValue) error { return nil }))
	if outcome.Kind != jobengine.OutcomeRetryableFailure || outcome.PublicErrorCode != "database.unavailable" {
		t.Fatalf("outcome=%#v", outcome)
	}
	if len(workspaceContent.removed) != 1 || workspaceContent.removed[0] != first.ID || len(workspace.released) != 1 || workspace.released[0] != second.ID {
		t.Fatalf("removed=%v released=%v", workspaceContent.removed, workspace.released)
	}
}

func TestFilePurgeHandlerRunsAttemptWorkspaceCleanupAfterStarterWorkspaceWithinOneBatch(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	var order []string
	candidate := store.FilePurgeCandidate{Cursor: "lease:" + model.NewId(), Kind: store.FilePurgeCandidateExpiredLease,
		LeaseID: model.NewUploadLeaseID(), RevisionID: model.NewFileRevisionID()}
	files := &purgeStoreFake{candidates: []store.FilePurgeCandidate{candidate}, order: &order}
	starterObject := cleanupWorkspaceObject(at)
	starter := &starterWorkspaceCleanupStoreFake{objects: []model.StarterWorkspaceObject{starterObject}, order: &order}
	starterContent := &starterWorkspaceContentPurgerFake{order: &order}
	attemptObject := cleanupAttemptWorkspaceObject(at)
	attempts := &attemptWorkspaceCleanupStoreFake{objects: []model.AttemptWorkspaceObject{attemptObject}, order: &order}
	attemptContent := &attemptWorkspaceContentPurgerFake{order: &order}
	job, err := model.NewJob(model.NewJobID(), model.JobTypeFilePurgeExpiredContent, 1, mustPurgeCommand(t, 3), "daily", at, at, 5)
	if err != nil {
		t.Fatal(err)
	}
	var checkpoints []jobengine.CheckpointValue
	execution := testWorkspaceCleanupExecution(t, job, at, allowJobWorkReservation(), func(_ context.Context, value jobengine.CheckpointValue) error {
		checkpoints = append(checkpoints, value)
		return nil
	})
	outcome := newFilePurgeExpiredContentHandler(files, &purgeContentFake{}, starter, starterContent, attempts, attemptContent).
		Run(context.Background(), execution)
	if outcome.Kind != jobengine.OutcomeSucceeded || len(attempts.claimLimits) != 1 || attempts.claimLimits[0] != 1 {
		t.Fatalf("outcome=%#v claim limits=%v", outcome, attempts.claimLimits)
	}
	if len(attempts.claimTokens) != 1 || attempts.claimTokens[0] != string(execution.Attempt.ClaimToken) {
		t.Fatalf("claim tokens=%v", attempts.claimTokens)
	}
	if got := strings.Join(order, ","); got != "generic-complete,workspace-remove,workspace-complete,attempt-workspace-remove,attempt-workspace-complete" {
		t.Fatalf("cleanup order=%q", got)
	}
	if len(checkpoints) != 3 {
		t.Fatalf("checkpoints=%d", len(checkpoints))
	}
	last, err := DecodeFilePurgeExpiredContentCheckpoint(checkpoints[2].Version, checkpoints[2].Document)
	if err != nil || last.Examined != 3 || last.Purged != 3 || last.Cursor != candidate.Cursor {
		t.Fatalf("checkpoint=%#v err=%v", last, err)
	}
}

func TestFilePurgeHandlerFencesUnknownAttemptWorkspaceRemovalAndReleasesUntouchedClaims(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	first, second := cleanupAttemptWorkspaceObject(at), cleanupAttemptWorkspaceObject(at)
	attempts := &attemptWorkspaceCleanupStoreFake{objects: []model.AttemptWorkspaceObject{first, second}}
	content := &attemptWorkspaceContentPurgerFake{errAt: 1}
	job, err := model.NewJob(model.NewJobID(), model.JobTypeFilePurgeExpiredContent, 1, mustPurgeCommand(t, 2), "daily", at, at, 5)
	if err != nil {
		t.Fatal(err)
	}
	outcome := newFilePurgeExpiredContentHandler(&purgeStoreFake{}, &purgeContentFake{}, &starterWorkspaceCleanupStoreFake{},
		&starterWorkspaceContentPurgerFake{}, attempts, content).Run(context.Background(),
		testWorkspaceCleanupExecution(t, job, at, allowJobWorkReservation(), func(context.Context, jobengine.CheckpointValue) error { return nil }))
	if outcome.Kind != jobengine.OutcomeRetryableFailure || len(content.removed) != 1 || content.removed[0] != first.ID ||
		len(attempts.completed) != 0 || len(attempts.released) != 1 || attempts.released[0] != second.ID {
		t.Fatalf("outcome=%#v removed=%v completed=%v released=%v", outcome, content.removed, attempts.completed, attempts.released)
	}
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

func testWorkspaceCleanupExecution(t *testing.T, job *model.Job, at time.Time, reserve func(context.Context, int, int) (bool, error), checkpoint func(context.Context, jobengine.CheckpointValue) error) jobengine.Execution {
	t.Helper()
	attempt, err := model.NewJobAttempt(model.NewJobAttemptID(), job.ID, 1, "test-node", model.JobClaimToken(strings.Repeat("a", 64)), at, at.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	return jobengine.NewExecution(job, attempt, checkpoint, reserve)
}

func mustPurgeCommand(t *testing.T, size int) json.RawMessage {
	t.Helper()
	value, err := EncodeFilePurgeExpiredContentCommand(FilePurgeExpiredContentCommandV1{BatchSize: size})
	if err != nil {
		t.Fatal(err)
	}
	return value
}
