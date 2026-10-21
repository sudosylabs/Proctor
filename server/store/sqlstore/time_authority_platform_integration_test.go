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
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/storetest"
)

// leadPlatformRow moves one row's lifecycle instants ahead of the database
// clock, as rows written by a node whose clock leads PostgreSQL, or by a
// transaction that started later and committed first, appear.
func leadPlatformRow(t *testing.T, ctx context.Context, persistence *SQLStore, table, columns, id string) {
	t.Helper()
	assignments := make([]string, 0, 4)
	for _, column := range strings.Split(columns, ",") {
		assignments = append(assignments, column+"="+column+"+INTERVAL '1 minute'")
	}
	_, err := persistence.GetMaster().Exec(ctx, `UPDATE `+table+` SET `+strings.Join(assignments, ",")+` WHERE id=?`, id)
	require.NoError(t, err)
}

func enqueuePlatformJob(t *testing.T, ctx context.Context, persistence *SQLStore, dedupe string) *model.Job {
	t.Helper()
	at := model.NowUTC()
	job, err := model.NewJob(model.NewJobID(), model.JobTypeCleanup, 1, json.RawMessage(`{"scope":"time-authority"}`), dedupe, at, at.Add(-time.Second), 3)
	require.NoError(t, err)
	_, inserted, err := persistence.Job().Enqueue(ctx, &store.JobEnqueue{Job: job})
	require.NoError(t, err, "Enqueue()")
	require.True(t, inserted, "Enqueue()")
	return job
}

func claimPlatformJob(t *testing.T, ctx context.Context, persistence *SQLStore) *store.JobClaim {
	t.Helper()
	token, err := model.NewJobClaimToken()
	require.NoError(t, err)
	claim, err := persistence.Job().ClaimNext(ctx, &store.JobClaimRequest{
		Types: []model.JobType{model.JobTypeCleanup}, NodeID: "time-authority-node", ClaimToken: token, LeaseDuration: time.Minute,
	})
	require.NoError(t, err)
	return claim
}

func requireStoredPlatformJob(t *testing.T, ctx context.Context, persistence *SQLStore, returned *model.Job) {
	t.Helper()
	require.NoError(t, returned.Validate(), "returned Job = %#v", returned)
	require.False(t, returned.UpdatedAt.Before(returned.CreatedAt), "returned Job = %#v", returned)
	stored, err := persistence.Job().Get(ctx, returned.ID)
	require.NoError(t, err)
	require.Equal(t, returned.Status, stored.Status, "Get() = %#v, returned %#v", stored, returned)
	require.True(t, stored.UpdatedAt.Equal(returned.UpdatedAt), "Get() = %#v, returned %#v", stored, returned)
	require.Equal(t, returned.Revision, stored.Revision, "Get() = %#v, returned %#v", stored, returned)
	require.Equal(t, returned.StartedAt, stored.StartedAt, "Get() = %#v, returned %#v", stored, returned)
	require.Equal(t, returned.CompletedAt, stored.CompletedAt, "Get() = %#v, returned %#v", stored, returned)
	require.True(t, stored.AvailableAt.Equal(returned.AvailableAt), "Get() = %#v, returned %#v", stored, returned)
}

func requireStoredPlatformAttempt(t *testing.T, ctx context.Context, persistence *SQLStore, returned *model.JobAttempt) {
	t.Helper()
	require.NoError(t, returned.Validate(), "returned attempt = %#v", returned)
	attempts, err := persistence.Job().ListAttempts(ctx, returned.JobID)
	require.NoError(t, err, "ListAttempts() = %#v, %v; returned %#v", attempts, err, returned)
	require.Len(t, attempts, 1, "ListAttempts() = %#v, %v; returned %#v", attempts, err, returned)
	require.Equal(t, *returned, attempts[0], "ListAttempts() = %#v, %v; returned %#v", attempts, err, returned)
}

func TestTimeAuthorityPlatformJobClaimAfterLeadingEnqueue(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	job := enqueuePlatformJob(t, ctx, persistence, "claim")
	// available_at keeps the requested instant, so the Job is claimable now.
	leadPlatformRow(t, ctx, persistence, "jobs", "created_at,updated_at", job.ID.String())
	claim := claimPlatformJob(t, ctx, persistence)
	require.Equal(t, model.JobStatusRunning, claim.Job.Status)
	require.False(t, claim.Job.UpdatedAt.Before(claim.Job.CreatedAt))
	requireStoredPlatformJob(t, ctx, persistence, claim.Job)
}

func TestTimeAuthorityPlatformJobHeartbeatAfterLaterHeartbeat(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	enqueuePlatformJob(t, ctx, persistence, "heartbeat")
	claim := claimPlatformJob(t, ctx, persistence)
	leadPlatformRow(t, ctx, persistence, "job_attempts", "started_at,heartbeat_at,lease_expires_at", claim.Attempt.ID.String())
	heartbeat, err := persistence.Job().Heartbeat(ctx, &store.JobHeartbeat{AttemptID: claim.Attempt.ID, ClaimToken: claim.Attempt.ClaimToken, LeaseDuration: time.Minute})
	require.NoError(t, err)
	require.False(t, heartbeat.HeartbeatAt.Before(claim.Attempt.HeartbeatAt.Add(time.Minute)))
	require.True(t, heartbeat.LeaseExpiresAt.After(heartbeat.HeartbeatAt))
	requireStoredPlatformAttempt(t, ctx, persistence, heartbeat)
}

func TestTimeAuthorityPlatformJobCheckpointAfterLeadingTransition(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	enqueuePlatformJob(t, ctx, persistence, "checkpoint")
	claim := claimPlatformJob(t, ctx, persistence)
	leadPlatformRow(t, ctx, persistence, "jobs", "created_at,updated_at", claim.Job.ID.String())
	updated, err := persistence.Job().Checkpoint(ctx, &store.JobCheckpoint{
		AttemptID: claim.Attempt.ID, ClaimToken: claim.Attempt.ClaimToken,
		Progress: &model.JobProgress{Current: 1, Total: 2, Stage: "copy"}, CheckpointVersion: 1, Checkpoint: json.RawMessage(`{"cursor":1}`),
	})
	require.NoError(t, err)
	requireStoredPlatformJob(t, ctx, persistence, updated)
}

func TestTimeAuthorityPlatformJobCompletionAfterLeadingTransitions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	for _, kind := range []store.JobCompletionKind{store.JobCompletionSucceeded, store.JobCompletionRetryableFailure} {
		t.Run(string(kind), func(t *testing.T) {
			resetTestStore(t, persistence)
			enqueuePlatformJob(t, ctx, persistence, "complete-"+string(kind))
			claim := claimPlatformJob(t, ctx, persistence)
			leadPlatformRow(t, ctx, persistence, "jobs", "created_at,updated_at", claim.Job.ID.String())
			leadPlatformRow(t, ctx, persistence, "job_attempts", "started_at,heartbeat_at,lease_expires_at", claim.Attempt.ID.String())
			input := &store.JobCompletion{AttemptID: claim.Attempt.ID, ClaimToken: claim.Attempt.ClaimToken, Kind: kind}
			if kind == store.JobCompletionSucceeded {
				input.ResultVersion, input.Result = 1, json.RawMessage(`{"done":true}`)
			} else {
				input.PublicErrorCode, input.RetryDelay = "job.temporary", time.Second
			}
			completed, err := persistence.Job().Complete(ctx, input)
			require.NoError(t, err)
			requireStoredPlatformJob(t, ctx, persistence, completed)
			attempts, err := persistence.Job().ListAttempts(ctx, completed.ID)
			require.NoError(t, err, "ListAttempts()")
			require.Len(t, attempts, 1, "ListAttempts()")
			require.True(t, attempts[0].CompletedAt.Valid, "ListAttempts()")
			require.False(t, attempts[0].CompletedAt.Time.Before(attempts[0].HeartbeatAt), "ListAttempts()")
			if kind == store.JobCompletionRetryableFailure {
				require.True(t, completed.AvailableAt.After(completed.UpdatedAt), "retry available_at %v does not follow", completed.AvailableAt)
			}
		})
	}
}

func TestTimeAuthorityPlatformJobOperatorCancelAfterLeadingEnqueue(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	job := enqueuePlatformJob(t, ctx, persistence, "operator-cancel")
	leadPlatformRow(t, ctx, persistence, "jobs", "created_at,updated_at", job.ID.String())
	audit, err := persistence.Audit().Save(ctx, &model.AuditEvent{
		Action: string(model.ActionJobManage), Resource: model.Resource{Type: model.ResourceInstitution, ID: model.NewID()},
		ScopeType: model.RoleScopeInstitution, ScopeID: model.NewID(), Status: model.AuditStatusAttempt, NodeID: "test-node",
		Parameters: []byte(`{"operation":"cancel","job_id":"` + job.ID.String() + `"}`),
	})
	require.NoError(t, err)
	canceled, err := persistence.Job().Cancel(ctx, &store.JobMutation{ID: job.ID, ExpectedRevision: job.Revision, AuditEventID: audit.ID, AuditAt: model.GetMillis()})
	require.NoError(t, err)
	require.Equal(t, model.JobStatusCanceled, canceled.Status)
	requireStoredPlatformJob(t, ctx, persistence, canceled)
}

func enqueuePlatformMail(t *testing.T, ctx context.Context, persistence *SQLStore, name string) *model.MailDelivery {
	t.Helper()
	institution, err := persistence.Institution().Save(ctx, &model.Institution{Name: name, DisplayName: name})
	require.NoError(t, err)
	user := saveIntegrationUser(t, ctx, persistence, &model.User{Username: name, Email: name + "@example.edu"})
	delivery, err := persistence.Mail().EnqueueTest(ctx, storetest.MailTestEnqueueFixtureForSQLTest(t, user, institution, model.NowUTC()))
	require.NoError(t, err)
	return delivery
}

func requireStoredPlatformMail(t *testing.T, ctx context.Context, persistence *SQLStore, returned *model.MailDelivery) {
	t.Helper()
	require.NoError(t, returned.Validate(), "returned delivery = %#v", returned)
	stored, err := persistence.Mail().GetDelivery(ctx, returned.ID)
	require.NoError(t, err)
	require.Equal(t, returned.State, stored.State, "GetDelivery() = %#v, returned %#v", stored, returned)
	require.True(t, stored.UpdatedAt.Equal(returned.UpdatedAt), "GetDelivery() = %#v, returned %#v", stored, returned)
	require.Equal(t, returned.Revision, stored.Revision, "GetDelivery() = %#v, returned %#v", stored, returned)
	require.Equal(t, returned.AcceptedAt, stored.AcceptedAt, "GetDelivery() = %#v, returned %#v", stored, returned)
	require.Equal(t, returned.FailedAt, stored.FailedAt, "GetDelivery() = %#v, returned %#v", stored, returned)
}

func TestTimeAuthorityPlatformMailWorkerTransitionsAfterLeadingTransition(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	persistence := openTestStore(t)

	t.Run("start", func(t *testing.T) {
		resetTestStore(t, persistence)
		delivery := enqueuePlatformMail(t, ctx, persistence, "mail-start-leads")
		leadPlatformRow(t, ctx, persistence, "mail_deliveries", "updated_at", delivery.ID.String())
		sending, err := persistence.Mail().StartDelivery(ctx, delivery.ID, delivery.Revision, model.NowUTC())
		require.NoError(t, err)
		require.Equal(t, model.MailDeliverySending, sending.State)
		require.True(t, sending.UpdatedAt.Equal(delivery.UpdatedAt.Add(time.Minute)))
		requireStoredPlatformMail(t, ctx, persistence, sending)
	})

	t.Run("accept", func(t *testing.T) {
		resetTestStore(t, persistence)
		delivery := enqueuePlatformMail(t, ctx, persistence, "mail-accept-leads")
		sending, err := persistence.Mail().StartDelivery(ctx, delivery.ID, delivery.Revision, model.NowUTC())
		require.NoError(t, err)
		leadPlatformRow(t, ctx, persistence, "mail_deliveries", "updated_at", delivery.ID.String())
		accepted, err := persistence.Mail().CompleteDelivery(ctx, &store.MailDeliveryCompletion{
			DeliveryID: delivery.ID, ExpectedRevision: sending.Revision, Kind: store.MailDeliveryCompletionAccepted, At: model.NowUTC(),
		})
		require.NoError(t, err)
		require.Equal(t, model.MailDeliveryAccepted, accepted.State)
		require.True(t, accepted.AcceptedAt.Time.Equal(accepted.UpdatedAt))
		require.True(t, accepted.UpdatedAt.Equal(sending.UpdatedAt.Add(time.Minute)))
		requireStoredPlatformMail(t, ctx, persistence, accepted)
	})
}

func TestTimeAuthorityPlatformUploadConsumptionAfterLeadingCreation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	user := saveIntegrationUser(t, ctx, persistence, &model.User{Username: "upload-leads", Email: "upload-leads@example.edu"})
	at := model.NowUTC()
	entry, err := model.NewFileEntry(model.NewFileEntryID(), model.FileIndexingNone, at)
	require.NoError(t, err)
	revision, err := model.NewFileRevision(model.NewFileRevisionID(), entry.ID, model.FileAvailabilityPending, model.FileIndexingNotRequired, at)
	require.NoError(t, err)
	lease, err := model.NewUploadLease(model.NewUploadLeaseID(), revision.ID, user.ID, at, at.Add(time.Hour))
	require.NoError(t, err)
	_, err = persistence.File().CreateUpload(ctx, &store.FileUploadCreation{Entry: entry, Revision: revision, Lease: lease})
	require.NoError(t, err)
	// The uploading node's clock leads the node that publishes the picture.
	leadPlatformRow(t, ctx, persistence, "upload_leases", "created_at,updated_at", lease.ID.String())
	renditions := make([]model.FileRendition, 0, 3)
	for _, size := range []int{128, 256, 512} {
		rendition, renditionErr := model.NewFileRendition(model.NewFileRenditionID(), revision.ID, fmt.Sprintf("profile_%d", size), "image/webp", 11, size, size, strings.Repeat("d", 64), at)
		require.NoError(t, renditionErr)
		renditions = append(renditions, *rendition)
	}
	_, err = persistence.File().PublishDefaultProfilePicture(ctx, &store.DefaultProfilePicturePublication{
		UserID: user.ID, ExpectedUserRevision: user.Revision, EntryID: entry.ID, RevisionID: revision.ID, LeaseID: lease.ID,
		Renditions: renditions, AttachedAt: model.NowUTC(),
	})
	require.NoError(t, err)
	var row uploadLeaseRow
	err = persistence.GetMaster().Get(ctx, &row, `SELECT id, file_revision_id, created_by_user_id, created_at, updated_at, expires_at, consumed_at, revision, bytes_received FROM upload_leases WHERE id=?`, lease.ID.String())
	require.NoError(t, err)
	consumed, err := row.model()
	require.NoError(t, err, "consumed lease model error = %v (%#v)", err, row)
	require.True(t, consumed.ConsumedAt.Valid)
	require.True(t, consumed.ConsumedAt.Time.Equal(consumed.CreatedAt))
}

func TestTimeAuthorityPlatformAcademicArchiveKeepsModificationMonotonic(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	institution, err := persistence.Institution().Save(ctx, &model.Institution{Name: "archive-leads", DisplayName: "Archive leads"})
	require.NoError(t, err)
	unit, err := storetest.SaveAcademicUnit(ctx, persistence, &model.AcademicUnit{InstitutionID: institution.ID, Name: "archive-leads", DisplayName: "Archive leads"})
	require.NoError(t, err)
	// A later update by a node whose clock leads the archiving node.
	_, err = persistence.GetMaster().Exec(ctx, `UPDATE academic_units SET updated_at=updated_at+INTERVAL '1 minute' WHERE id=?`, unit.ID.String())
	require.NoError(t, err)
	archiveAt := model.GetMillis()
	archived, err := storetest.ArchiveAcademicUnit(ctx, persistence, unit.ID, archiveAt)
	require.NoError(t, err)
	err = archived.Validate()
	require.NoError(t, err, "Archive() = %#v", archived)
	require.True(t, archived.UpdatedAt.Equal(unit.UpdatedAt.Add(time.Minute)), "Archive() = %#v", archived)
	require.False(t, archived.ArchivedAt.Time.Before(archived.CreatedAt), "Archive() = %#v", archived)
	var stored struct {
		UpdatedAt  time.Time `db:"updated_at"`
		ArchivedAt time.Time `db:"archived_at"`
	}
	err = persistence.GetMaster().Get(ctx, &stored, `SELECT updated_at, archived_at FROM academic_units WHERE id=?`, unit.ID.String())
	require.NoError(t, err)
	require.True(t, model.TimeUTC(stored.UpdatedAt).Equal(archived.UpdatedAt), "stored archive = %#v, returned %#v", stored, archived)
	require.True(t, model.TimeUTC(stored.ArchivedAt).Equal(archived.ArchivedAt.Time), "stored archive = %#v, returned %#v", stored, archived)
}

// TestTimeAuthorityPlatformBrowserSourceCloseWithinStartMillisecond closes a
// Browser source in the millisecond it started. The closure instant is
// millisecond-exact while started_at is microsecond PostgreSQL time; ending
// the source must not precede its start. Foreign keys are relaxed for this
// rolled-back transaction because only the source row's lifecycle is under
// test.
func TestTimeAuthorityPlatformBrowserSourceCloseWithinStartMillisecond(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	tx, err := persistence.GetMaster().Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.Exec(ctx, `SET LOCAL session_replication_role = replica`)
	require.NoError(t, err)
	var startedAt time.Time
	err = tx.Get(ctx, &startedAt, `SELECT date_trunc('milliseconds', clock_timestamp()) + INTERVAL '700 microseconds'`)
	require.NoError(t, err)
	attemptID, participationID := model.NewID(), model.NewID()
	_, err = tx.Exec(ctx, `INSERT INTO exam_attempt_participations(id,exam_attempt_id,session_id,state,generation,continuity_credential_hash,started_at,updated_at,lease_expires_at)
		VALUES(?,?,?,'active',1,?,?,?,?)`, participationID, attemptID, model.NewID(), strings.Repeat("a", 64), startedAt, startedAt, startedAt.Add(20*time.Second))
	require.NoError(t, err)
	sourceID := model.BrowserSourceSessionID("4f1d6a0e-8c43-4f5e-9d2b-6a1c3e5f7b90")
	digest := "sha256:" + strings.Repeat("b", 64)
	_, err = tx.Exec(ctx, `INSERT INTO browser_activity_sources(id,exam_id,exam_sitting_id,exam_attempt_id,participation_id,generation,session_id,connection_id,
		candidate_user_id,registration_id,key_thumbprint,policy_revision_id,policy_digest,start_ordinal,start_transition,start_digest,start_canonical,reserved_bytes,state,started_at)
		VALUES(?::uuid,?,?,?,?,1,?,?,?,?,'thumbprint',?,?,1,'initial',?,'\x00'::bytea,1,'current',?)`,
		string(sourceID), model.NewID(), model.NewID(), attemptID, participationID, model.NewID(), model.NewID(), model.NewID(), model.NewID(), model.NewID(), digest, digest, startedAt)
	require.NoError(t, err)
	closeErr := closeBrowserSource(ctx, tx, sourceID, model.DeliveryClosedRuntimeReset, startedAt.Add(200*time.Microsecond))
	var postgresErr *pq.Error
	require.NotErrorAs(t, closeErr, &postgresErr, "closeBrowserSource() violated a PostgreSQL constraint")
	// Settlement needs the Participation owner rows this fixture omits; the
	// closure UPDATE has already run.
	var ended struct {
		State   string    `db:"state"`
		Started time.Time `db:"started_at"`
		Ended   time.Time `db:"ended_at"`
	}
	err = tx.Get(ctx, &ended, `SELECT state, started_at, ended_at FROM browser_activity_sources WHERE id=?::uuid`, string(sourceID))
	require.NoError(t, err, "read closed source: %v (close error %v)", err, closeErr)
	want := startedAt.Truncate(time.Millisecond).Add(time.Millisecond)
	require.Equal(t, "closed", ended.State, "closed source = %#v, want ended_at", ended)
	require.True(t, ended.Ended.Equal(want), "closed source = %#v, want ended_at", ended)
	require.False(t, ended.Ended.Before(ended.Started), "closed source = %#v, want ended_at", ended)
}
