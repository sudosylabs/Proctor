//go:build integration

// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
			require.Nil(t, winner, "more than one concurrent profile-picture publication succeeded")
			winner = result.result
		case store.IsConflict(result.err):
			conflicts++
		default:
			require.FailNow(t, fmt.Sprintf("concurrent publication error = %v", result.err))
		}
	}
	require.NotNil(t, winner, "winner/conflicts")
	require.Equal(t, 1, conflicts, "winner/conflicts")

	state, err := nodeB.File().GetProfilePictureState(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, winner.User.CustomProfilePictureFileID, state.EntryID, "visible state = %#v, winner = %#v", state, winner)
	require.Equal(t, winner.Revision.ID, state.RevisionID, "visible state = %#v, winner = %#v", state, winner)
	require.Len(t, state.Renditions, 3, "visible state = %#v, winner = %#v", state, winner)
	for _, rendition := range state.Renditions {
		require.Equal(t, state.RevisionID, rendition.RevisionID)
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
		require.FailNow(t, fmt.Sprint(err))
	}
	require.Equal(t, int64(1), inserted.Load(), "permanent occurrence insertions")

	const workCount = 12
	jobIDs := make(map[model.JobID]struct{}, workCount)
	for index := range workCount {
		job, err := model.NewJob(
			model.NewJobID(), model.JobTypeFilePurgeExpiredContent, 1,
			json.RawMessage(`{"batch_size":1}`), fmt.Sprintf("parallel:%02d", index), at, at, 3,
		)
		require.NoError(t, err)
		_, created, err := nodeA.Job().Enqueue(ctx, &store.JobEnqueue{Job: job})
		require.NoError(t, err, "enqueue parallel job %d", index)
		require.True(t, created, "enqueue parallel job %d", index)
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
		require.FailNow(t, fmt.Sprint(err))
	}
	require.Len(t, claimed, workCount, "distinct claimed jobs")
	for id := range jobIDs {
		attempts, err := nodeB.Job().ListAttempts(ctx, id)
		require.NoError(t, err)
		require.Len(t, attempts, 1, "job %s attempts", id)
		require.Equal(t, model.JobAttemptStatusSucceeded, attempts[0].Status, "job %s attempts", id)
	}
}

func TestClusteredJobLeaseRecoveryFencesTheDeadNodeAndPreservesAttempts(t *testing.T) {
	ctx := context.Background()
	nodeA := openTestStore(t)
	resetTestStore(t, nodeA)
	nodeB := openPeerTestStore(t)
	at := model.NowUTC().Add(-time.Second)
	job, err := model.NewJob(model.NewJobID(), model.JobTypeFilePurgeExpiredContent, 1, json.RawMessage(`{"batch_size":1}`), "recover-across-nodes", at, at, 3)
	require.NoError(t, err)
	_, inserted, err := nodeA.Job().Enqueue(ctx, &store.JobEnqueue{Job: job})
	require.NoError(t, err, "Enqueue()")
	require.True(t, inserted, "Enqueue()")
	deadToken, err := model.NewJobClaimToken()
	require.NoError(t, err)
	dead, err := nodeA.Job().ClaimNext(ctx, &store.JobClaimRequest{Types: []model.JobType{job.Type}, NodeID: "dead-node", ClaimToken: deadToken, LeaseDuration: 20 * time.Millisecond})
	require.NoError(t, err)
	time.Sleep(40 * time.Millisecond)
	skipLeaseExpiryBackoff(t, ctx, nodeB, job.ID)
	liveToken, err := model.NewJobClaimToken()
	require.NoError(t, err)
	live, err := nodeB.Job().ClaimNext(ctx, &store.JobClaimRequest{Types: []model.JobType{job.Type}, NodeID: "live-node", ClaimToken: liveToken, LeaseDuration: time.Minute})
	require.NoError(t, err)
	require.Equal(t, dead.Job.ID, live.Job.ID)
	require.Equal(t, 2, live.Attempt.Number)
	_, err = nodeA.Job().Complete(ctx, &store.JobCompletion{AttemptID: dead.Attempt.ID, ClaimToken: deadToken, Kind: store.JobCompletionSucceeded, ResultVersion: 1, Result: json.RawMessage(`{}`)})
	require.True(t, store.IsConflict(err), "dead-node completion error")
	_, err = nodeB.Job().Complete(ctx, &store.JobCompletion{AttemptID: live.Attempt.ID, ClaimToken: liveToken, Kind: store.JobCompletionSucceeded, ResultVersion: 1, Result: json.RawMessage(`{}`)})
	require.NoError(t, err)
	attempts, err := nodeA.Job().ListAttempts(ctx, job.ID)
	require.NoError(t, err)
	require.Len(t, attempts, 2)
	require.Equal(t, model.JobAttemptStatusLeaseExpired, attempts[0].Status)
	require.Equal(t, model.JobAttemptStatusSucceeded, attempts[1].Status)
}

type profilePictureRaceResult struct {
	result *store.ProfilePicturePublicationResult
	err    error
}

func openPeerTestStore(t *testing.T) *SQLStore {
	t.Helper()
	peer, err := New(context.Background(), testSettings(t))
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, peer.Close())
	})
	return peer
}

func prepareConcurrentProfilePicturePublication(t *testing.T, ctx context.Context, persistence store.Store, user *model.User, checksumCharacter, nodeID string) *store.ProfilePicturePublication {
	t.Helper()
	at := model.NowUTC()
	entry, err := model.NewFileEntryForPurpose(model.NewFileEntryID(), model.FilePurposeProfilePictureCustom, model.FileIndexingNone, at)
	require.NoError(t, err)
	revision, err := model.NewFileRevision(model.NewFileRevisionID(), entry.ID, model.FileAvailabilityPending, model.FileIndexingNotRequired, at)
	require.NoError(t, err)
	lease, err := model.NewUploadLease(model.NewUploadLeaseID(), revision.ID, user.ID, at, at.Add(time.Hour))
	require.NoError(t, err)
	_, err = persistence.File().CreateUpload(ctx, &store.FileUploadCreation{Entry: entry, Revision: revision, Lease: lease})
	require.NoError(t, err)
	renditions := make([]model.FileRendition, 0, 3)
	for _, size := range []int{128, 256, 512} {
		rendition, renditionErr := model.NewFileRendition(model.NewFileRenditionID(), revision.ID, fmt.Sprintf("profile_%d", size), "image/webp", int64(size), size, size, strings.Repeat(checksumCharacter, 64), at)
		require.NoError(t, renditionErr)
		renditions = append(renditions, *rendition)
	}
	audit, err := persistence.Audit().Save(ctx, &model.AuditEvent{
		Action: string(model.ActionUserProfilePictureManage), Resource: model.Resource{Type: model.ResourceUser, ID: user.ID.String()},
		ScopeType: model.RoleScopeInstitution, ScopeID: model.NewID(), Status: model.AuditStatusAttempt, NodeID: nodeID,
	})
	require.NoError(t, err)
	return &store.ProfilePicturePublication{
		ActorID: user.ID, UserID: user.ID, ExpectedUserRevision: user.Revision,
		EntryID: entry.ID, RevisionID: revision.ID, LeaseID: lease.ID, Renditions: renditions,
		ChangedAt: at, AuditEventID: audit.ID, AuditAt: at.UnixMilli(),
	}
}

// skipLeaseExpiryBackoff recovers an expired lease, which requeues the Job
// behind its crash backoff, and then makes the Job available at once so a test
// can take over from the dead node without waiting out the backoff.
func skipLeaseExpiryBackoff(t *testing.T, ctx context.Context, persistence *SQLStore, jobID model.JobID) {
	t.Helper()
	token, err := model.NewJobClaimToken()
	require.NoError(t, err)
	_, err = persistence.Job().ClaimNext(ctx, &store.JobClaimRequest{Types: []model.JobType{model.JobTypeCleanup}, NodeID: "lease-recovery", ClaimToken: token, LeaseDuration: time.Minute})
	require.True(t, err == nil || store.IsNotFound(err), "recover expired leases: %v", err)
	_, err = persistence.GetMaster().Exec(ctx, `UPDATE jobs SET available_at=CURRENT_TIMESTAMP WHERE id=? AND status='queued'`, jobID.String())
	require.NoError(t, err)
}
