//go:build integration

// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"context"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestFilePurgeCandidatesRespectPurposeReferencesAndLegalHolds(t *testing.T) {
	ss := openTestStore(t)
	resetTestStore(t, ss)
	ctx := context.Background()
	now := model.NowUTC()
	old := now.Add(-60 * 24 * time.Hour)

	eligible := insertArchivedPurgeFixture(t, ss, model.FilePurposeProfilePictureCustom, old)
	held := insertArchivedPurgeFixture(t, ss, model.FilePurposeProfilePictureCustom, old)
	if _, err := ss.GetMaster().Exec(ctx, `INSERT INTO file_legal_holds (file_entry_id, created_at, reason_code) VALUES (?, ?, 'litigation')`, held.entryID.String(), now); err != nil {
		t.Fatal(err)
	}
	defaultPicture := insertArchivedPurgeFixture(t, ss, model.FilePurposeProfilePictureDefault, old)
	submission := insertArchivedPurgeFixture(t, ss, model.FilePurposeSubmission, old)

	page, err := ss.File().ListPurgeCandidates(ctx, &store.FilePurgeCandidateRequest{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[model.FileRevisionID]bool{}
	var candidate *store.FilePurgeCandidate
	for index := range page.Candidates {
		seen[page.Candidates[index].RevisionID] = true
		if page.Candidates[index].RevisionID == eligible.revisionID {
			candidate = &page.Candidates[index]
		}
	}
	if candidate == nil {
		t.Fatal("eligible archived custom content was not selected")
	}
	for _, excluded := range []model.FileRevisionID{held.revisionID, defaultPicture.revisionID, submission.revisionID} {
		if seen[excluded] {
			t.Fatalf("retained revision %s became purge eligible", excluded)
		}
	}
	claim, err := ss.File().ClaimPurgeCandidate(ctx, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if !model.IsValidId(claim.ID) || claim.Candidate.RevisionID != candidate.RevisionID {
		t.Fatalf("purge claim = %#v", claim)
	}
	if _, holdErr := ss.GetMaster().Exec(ctx, `INSERT INTO file_legal_holds (file_entry_id, created_at, reason_code) VALUES (?, CURRENT_TIMESTAMP, 'too_late')`, eligible.entryID.String()); holdErr == nil {
		t.Fatal("legal hold was added after the durable purge tombstone")
	}
	relisted, err := ss.File().ListPurgeCandidates(ctx, &store.FilePurgeCandidateRequest{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	claimedStillVisible := false
	for _, item := range relisted.Candidates {
		claimedStillVisible = claimedStillVisible || item.RevisionID == candidate.RevisionID
	}
	if !claimedStillVisible {
		t.Fatal("durably claimed purge disappeared before physical deletion")
	}
	if err = ss.File().CompletePurge(ctx, claim); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = ss.GetMaster().Get(ctx, &count, `SELECT COUNT(*) FROM file_revisions WHERE id = ?`, eligible.revisionID.String()); err != nil || count != 0 {
		t.Fatalf("purged revision count = %d, %v", count, err)
	}
}

func TestFilePurgeClaimRevalidatesLegalHoldBeforeBytesCanBeRemoved(t *testing.T) {
	ss := openTestStore(t)
	resetTestStore(t, ss)
	ctx := context.Background()
	old := model.NowUTC().Add(-60 * 24 * time.Hour)
	fixture := insertArchivedPurgeFixture(t, ss, model.FilePurposeProfilePictureCustom, old)
	page, err := ss.File().ListPurgeCandidates(ctx, &store.FilePurgeCandidateRequest{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var candidate *store.FilePurgeCandidate
	for index := range page.Candidates {
		if page.Candidates[index].RevisionID == fixture.revisionID {
			candidate = &page.Candidates[index]
		}
	}
	if candidate == nil {
		t.Fatal("eligible fixture was not listed")
	}
	if _, err = ss.GetMaster().Exec(ctx, `INSERT INTO file_legal_holds (file_entry_id, created_at, reason_code) VALUES (?, CURRENT_TIMESTAMP, 'review')`, fixture.entryID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err = ss.File().ClaimPurgeCandidate(ctx, candidate); !store.IsConflict(err) {
		t.Fatalf("ClaimPurgeCandidate(held after listing) error = %v", err)
	}
	var claimID *string
	if err = ss.GetMaster().Get(ctx, &claimID, `SELECT purge_claim_id FROM file_revisions WHERE id = ?`, fixture.revisionID.String()); err != nil || claimID != nil {
		t.Fatalf("purge claim persisted despite hold: %v, %v", claimID, err)
	}
}

type archivedPurgeFixture struct {
	entryID    model.FileEntryID
	revisionID model.FileRevisionID
}

func insertArchivedPurgeFixture(t *testing.T, ss *SQLStore, purpose model.FilePurpose, at time.Time) archivedPurgeFixture {
	t.Helper()
	ctx := context.Background()
	entryID, revisionID := model.NewFileEntryID(), model.NewFileRevisionID()
	if _, err := ss.GetMaster().Exec(ctx, `INSERT INTO file_entries (id, created_at, updated_at, archived_at, revision, current_revision_id, indexing_policy, purpose) VALUES (?, ?, ?, ?, 1, NULL, 'none', ?)`, entryID.String(), at.Add(-time.Hour), at, at, string(purpose)); err != nil {
		t.Fatal(err)
	}
	if _, err := ss.GetMaster().Exec(ctx, `INSERT INTO file_revisions (id, file_entry_id, created_at, availability, indexing_state) VALUES (?, ?, ?, 'available', 'not_required')`, revisionID.String(), entryID.String(), at.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := ss.GetMaster().Exec(ctx, `UPDATE file_entries SET current_revision_id = ? WHERE id = ?`, revisionID.String(), entryID.String()); err != nil {
		t.Fatal(err)
	}
	return archivedPurgeFixture{entryID: entryID, revisionID: revisionID}
}
