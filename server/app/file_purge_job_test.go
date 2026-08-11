// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"encoding/json"
	"errors"
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
	return nil
}

type purgeContentFake struct {
	removed []model.FileRevisionID
	failAt  int
}

func (f *purgeContentFake) RemoveFileRevisionContent(_ context.Context, revisionID model.FileRevisionID, _ []model.FileRenditionID) error {
	f.removed = append(f.removed, revisionID)
	if f.failAt > 0 && len(f.removed) == f.failAt {
		return errors.New("backend unavailable")
	}
	return nil
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
	handler := newFilePurgeExpiredContentHandler(persistence, content)
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
	outcome := newFilePurgeExpiredContentHandler(persistence, content).Run(context.Background(), testJobExecution(job, allowJobWorkReservation(), nil))
	if outcome.Kind != jobengine.OutcomeSucceeded || len(persistence.claimed) != 0 || len(content.removed) != 0 {
		t.Fatalf("outcome=%#v claimed=%#v removed=%#v", outcome, persistence.claimed, content.removed)
	}

	job.WorkReserved = 0
	job.CheckpointVersion = 1
	job.Checkpoint, err = EncodeFilePurgeExpiredContentCheckpoint(FilePurgeExpiredContentCheckpointV1{Cursor: candidate.Cursor, Examined: 1, Purged: 1})
	if err != nil {
		t.Fatal(err)
	}
	outcome = newFilePurgeExpiredContentHandler(persistence, content).Run(context.Background(), testJobExecution(job, allowJobWorkReservation(), nil))
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
	handler := newFilePurgeExpiredContentHandler(persistence, content)
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
	handler := newFilePurgeExpiredContentHandler(persistence, &purgeContentFake{})
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

func mustPurgeCommand(t *testing.T, size int) json.RawMessage {
	t.Helper()
	value, err := EncodeFilePurgeExpiredContentCommand(FilePurgeExpiredContentCommandV1{BatchSize: size})
	if err != nil {
		t.Fatal(err)
	}
	return value
}
