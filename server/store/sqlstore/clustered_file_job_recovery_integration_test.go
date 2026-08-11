//go:build integration

// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestClusteredProfilePicturePublicationHasOneVisibleWinner(t *testing.T) {
	ctx := context.Background()
	nodeA := openTestStore(t)
	resetTestStore(t, nodeA)
	nodeB := openPeerTestStore(t)
	user := saveIntegrationUser(t, ctx, nodeA, &model.User{Username: "picture-race", Email: "picture-race@example.edu"})

	first := prepareConcurrentProfilePicturePublication(t, ctx, nodeA, user, "a", "node-a")
	second := prepareConcurrentProfilePicturePublication(t, ctx, nodeB, user, "b", "node-b")
	start := make(chan struct{})
	results := make(chan profilePictureRaceResult, 2)
	for _, candidate := range []struct {
		files store.FileStore
		input *store.ProfilePicturePublication
	}{{nodeA.File(), first}, {nodeB.File(), second}} {
		go func() {
			<-start
			result, err := candidate.files.PublishProfilePicture(ctx, candidate.input)
			results <- profilePictureRaceResult{result: result, err: err}
		}()
	}
	close(start)

	var winner *store.ProfilePicturePublicationResult
	conflicts := 0
	for range 2 {
		result := <-results
		switch {
		case result.err == nil:
			if winner != nil {
				t.Fatal("more than one concurrent profile-picture publication succeeded")
			}
			winner = result.result
		case store.IsConflict(result.err):
			conflicts++
		default:
			t.Fatalf("concurrent publication error = %v", result.err)
		}
	}
	if winner == nil || conflicts != 1 {
		t.Fatalf("winner/conflicts = %#v/%d", winner, conflicts)
	}

	state, err := nodeB.File().GetProfilePictureState(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state.EntryID != winner.User.CustomProfilePictureFileID || state.RevisionID != winner.Revision.ID || len(state.Renditions) != 3 {
		t.Fatalf("visible state = %#v, winner = %#v", state, winner)
	}
	for _, rendition := range state.Renditions {
		if rendition.RevisionID != state.RevisionID {
			t.Fatalf("visible state mixed revisions: %#v", state)
		}
	}
}

func TestClusteredJobsDeduplicateOccurrencesAndClaimDistinctWork(t *testing.T) {
	ctx := context.Background()
	nodeA := openTestStore(t)
	resetTestStore(t, nodeA)
	nodeB := openPeerTestStore(t)
	at := model.NowUTC().Add(-time.Second)

	var inserted atomic.Int64
	errorsByNode := make(chan error, 16)
	var enqueueGroup sync.WaitGroup
	for index := range 16 {
		enqueueGroup.Add(1)
		go func() {
			defer enqueueGroup.Done()
			candidate, err := model.NewJobWithDedupePolicy(
				model.NewJobID(), model.JobTypeCleanup, 1, json.RawMessage(`{"batch_size":50}`),
				"job-cleanup:2026-08-11", model.JobDedupePermanent, at, at, 5,
			)
			if err != nil {
				errorsByNode <- err
				return
			}
			jobs := nodeA.Job()
			if index%2 == 1 {
				jobs = nodeB.Job()
			}
			_, created, err := jobs.Enqueue(ctx, &store.JobEnqueue{Job: candidate})
			if err != nil {
				errorsByNode <- err
				return
			}
			if created {
				inserted.Add(1)
			}
		}()
	}
	enqueueGroup.Wait()
	close(errorsByNode)
	for err := range errorsByNode {
		t.Fatal(err)
	}
	if inserted.Load() != 1 {
		t.Fatalf("permanent occurrence insertions = %d, want 1", inserted.Load())
	}

	const workCount = 12
	jobIDs := make(map[model.JobID]struct{}, workCount)
	for index := range workCount {
		job, err := model.NewJob(
			model.NewJobID(), model.JobTypeFilePurgeExpiredContent, 1,
			json.RawMessage(`{"batch_size":1}`), fmt.Sprintf("parallel:%02d", index), at, at, 3,
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, created, err := nodeA.Job().Enqueue(ctx, &store.JobEnqueue{Job: job}); err != nil || !created {
			t.Fatalf("enqueue parallel job %d = %v, %v", index, created, err)
		}
		jobIDs[job.ID] = struct{}{}
	}

	claimed := make(map[model.JobID]string, workCount)
	var claimedMu sync.Mutex
	claimErrors := make(chan error, 6)
	var claimGroup sync.WaitGroup
	for index := range 6 {
		claimGroup.Add(1)
		go func() {
			defer claimGroup.Done()
			jobs, nodeID := nodeA.Job(), fmt.Sprintf("node-%d", index)
			if index%2 == 1 {
				jobs = nodeB.Job()
			}
			for {
				token, err := model.NewJobClaimToken()
				if err != nil {
					claimErrors <- err
					return
				}
				claim, err := jobs.ClaimNext(ctx, &store.JobClaimRequest{Types: []model.JobType{model.JobTypeFilePurgeExpiredContent}, NodeID: nodeID, ClaimToken: token, LeaseDuration: time.Minute})
				if store.IsNotFound(err) {
					return
				}
				if err != nil {
					claimErrors <- err
					return
				}
				claimedMu.Lock()
				if prior, duplicate := claimed[claim.Job.ID]; duplicate {
					claimedMu.Unlock()
					claimErrors <- fmt.Errorf("job %s claimed by both %s and %s", claim.Job.ID, prior, nodeID)
					return
				}
				claimed[claim.Job.ID] = nodeID
				claimedMu.Unlock()
				if _, err = jobs.Complete(ctx, &store.JobCompletion{AttemptID: claim.Attempt.ID, ClaimToken: token, Kind: store.JobCompletionSucceeded, ResultVersion: 1, Result: json.RawMessage(`{}`)}); err != nil {
					claimErrors <- err
					return
				}
			}
		}()
	}
	claimGroup.Wait()
	close(claimErrors)
	for err := range claimErrors {
		t.Fatal(err)
	}
	if len(claimed) != workCount {
		t.Fatalf("distinct claimed jobs = %d, want %d", len(claimed), workCount)
	}
	for id := range jobIDs {
		attempts, err := nodeB.Job().ListAttempts(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if len(attempts) != 1 || attempts[0].Status != model.JobAttemptStatusSucceeded {
			t.Fatalf("job %s attempts = %#v", id, attempts)
		}
	}
}

func TestClusteredJobLeaseRecoveryFencesTheDeadNodeAndPreservesAttempts(t *testing.T) {
	ctx := context.Background()
	nodeA := openTestStore(t)
	resetTestStore(t, nodeA)
	nodeB := openPeerTestStore(t)
	at := model.NowUTC().Add(-time.Second)
	job, err := model.NewJob(model.NewJobID(), model.JobTypeFilePurgeExpiredContent, 1, json.RawMessage(`{"batch_size":1}`), "recover-across-nodes", at, at, 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, inserted, err := nodeA.Job().Enqueue(ctx, &store.JobEnqueue{Job: job}); err != nil || !inserted {
		t.Fatalf("Enqueue() = %v, %v", inserted, err)
	}
	deadToken, err := model.NewJobClaimToken()
	if err != nil {
		t.Fatal(err)
	}
	dead, err := nodeA.Job().ClaimNext(ctx, &store.JobClaimRequest{Types: []model.JobType{job.Type}, NodeID: "dead-node", ClaimToken: deadToken, LeaseDuration: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(40 * time.Millisecond)
	liveToken, err := model.NewJobClaimToken()
	if err != nil {
		t.Fatal(err)
	}
	live, err := nodeB.Job().ClaimNext(ctx, &store.JobClaimRequest{Types: []model.JobType{job.Type}, NodeID: "live-node", ClaimToken: liveToken, LeaseDuration: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if live.Job.ID != dead.Job.ID || live.Attempt.Number != 2 {
		t.Fatalf("recovered claim = %#v", live)
	}
	if _, err = nodeA.Job().Complete(ctx, &store.JobCompletion{AttemptID: dead.Attempt.ID, ClaimToken: deadToken, Kind: store.JobCompletionSucceeded, ResultVersion: 1, Result: json.RawMessage(`{}`)}); !store.IsConflict(err) {
		t.Fatalf("dead-node completion error = %v", err)
	}
	if _, err = nodeB.Job().Complete(ctx, &store.JobCompletion{AttemptID: live.Attempt.ID, ClaimToken: liveToken, Kind: store.JobCompletionSucceeded, ResultVersion: 1, Result: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	attempts, err := nodeA.Job().ListAttempts(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 2 || attempts[0].Status != model.JobAttemptStatusLeaseExpired || attempts[1].Status != model.JobAttemptStatusSucceeded {
		t.Fatalf("recovered attempts = %#v", attempts)
	}
}

type profilePictureRaceResult struct {
	result *store.ProfilePicturePublicationResult
	err    error
}

func openPeerTestStore(t *testing.T) *SQLStore {
	t.Helper()
	peer, err := New(context.Background(), testSettings(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := peer.Close(); err != nil {
			t.Errorf("close peer SQL store: %v", err)
		}
	})
	return peer
}

func prepareConcurrentProfilePicturePublication(t *testing.T, ctx context.Context, persistence store.Store, user *model.User, checksumCharacter, nodeID string) *store.ProfilePicturePublication {
	t.Helper()
	at := model.NowUTC()
	entry, err := model.NewFileEntryForPurpose(model.NewFileEntryID(), model.FilePurposeProfilePictureCustom, model.FileIndexingNone, at)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := model.NewFileRevision(model.NewFileRevisionID(), entry.ID, model.FileAvailabilityPending, model.FileIndexingNotRequired, at)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := model.NewUploadLease(model.NewUploadLeaseID(), revision.ID, user.ID, at, at.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = persistence.File().CreateUpload(ctx, &store.FileUploadCreation{Entry: entry, Revision: revision, Lease: lease}); err != nil {
		t.Fatal(err)
	}
	renditions := make([]model.FileRendition, 0, 3)
	for _, size := range []int{128, 256, 512} {
		rendition, renditionErr := model.NewFileRendition(model.NewFileRenditionID(), revision.ID, fmt.Sprintf("profile_%d", size), "image/webp", int64(size), size, size, strings.Repeat(checksumCharacter, 64), at)
		if renditionErr != nil {
			t.Fatal(renditionErr)
		}
		renditions = append(renditions, *rendition)
	}
	audit, err := persistence.Audit().Save(ctx, &model.AuditEvent{
		Action: string(model.ActionUserProfilePictureManage), Resource: model.Resource{Type: model.ResourceUser, ID: user.ID.String()},
		ScopeType: model.RoleScopeInstitution, ScopeID: model.NewId(), Status: model.AuditStatusAttempt, NodeID: nodeID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &store.ProfilePicturePublication{
		ActorID: user.ID, UserID: user.ID, ExpectedUserRevision: user.Revision,
		EntryID: entry.ID, RevisionID: revision.ID, LeaseID: lease.ID, Renditions: renditions,
		ChangedAt: at, AuditEventID: audit.ID.String(), AuditAt: at.UnixMilli(),
	}
}
