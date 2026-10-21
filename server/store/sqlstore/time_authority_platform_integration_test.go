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
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"

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
	if _, err := persistence.GetMaster().Exec(ctx, `UPDATE `+table+` SET `+strings.Join(assignments, ",")+` WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
}

func enqueuePlatformJob(t *testing.T, ctx context.Context, persistence *SQLStore, dedupe string) *model.Job {
	t.Helper()
	at := model.NowUTC()
	job, err := model.NewJob(model.NewJobID(), model.JobTypeCleanup, 1, json.RawMessage(`{"scope":"time-authority"}`), dedupe, at, at.Add(-time.Second), 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, inserted, err := persistence.Job().Enqueue(ctx, &store.JobEnqueue{Job: job}); err != nil || !inserted {
		t.Fatalf("Enqueue() = %v, %v", inserted, err)
	}
	return job
}

func claimPlatformJob(t *testing.T, ctx context.Context, persistence *SQLStore) *store.JobClaim {
	t.Helper()
	token, err := model.NewJobClaimToken()
	if err != nil {
		t.Fatal(err)
	}
	claim, err := persistence.Job().ClaimNext(ctx, &store.JobClaimRequest{
		Types: []model.JobType{model.JobTypeCleanup}, NodeID: "time-authority-node", ClaimToken: token, LeaseDuration: time.Minute,
	})
	if err != nil {
		t.Fatalf("ClaimNext() error = %v", err)
	}
	return claim
}

func requireStoredPlatformJob(t *testing.T, ctx context.Context, persistence *SQLStore, returned *model.Job) {
	t.Helper()
	if err := returned.Validate(); err != nil || returned.UpdatedAt.Before(returned.CreatedAt) {
		t.Fatalf("returned Job = %#v, %v", returned, err)
	}
	stored, err := persistence.Job().Get(ctx, returned.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != returned.Status || !stored.UpdatedAt.Equal(returned.UpdatedAt) || stored.Revision != returned.Revision ||
		stored.StartedAt != returned.StartedAt || stored.CompletedAt != returned.CompletedAt || !stored.AvailableAt.Equal(returned.AvailableAt) {
		t.Fatalf("Get() = %#v, returned %#v", stored, returned)
	}
}

func requireStoredPlatformAttempt(t *testing.T, ctx context.Context, persistence *SQLStore, returned *model.JobAttempt) {
	t.Helper()
	if err := returned.Validate(); err != nil {
		t.Fatalf("returned attempt = %#v, %v", returned, err)
	}
	attempts, err := persistence.Job().ListAttempts(ctx, returned.JobID)
	if err != nil || len(attempts) != 1 || !reflect.DeepEqual(attempts[0], *returned) {
		t.Fatalf("ListAttempts() = %#v, %v; returned %#v", attempts, err, returned)
	}
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
	if claim.Job.Status != model.JobStatusRunning || claim.Job.UpdatedAt.Before(claim.Job.CreatedAt) {
		t.Fatalf("ClaimNext() = %#v", claim.Job)
	}
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
	if err != nil {
		t.Fatalf("Heartbeat() error = %v", err)
	}
	if heartbeat.HeartbeatAt.Before(claim.Attempt.HeartbeatAt.Add(time.Minute)) || !heartbeat.LeaseExpiresAt.After(heartbeat.HeartbeatAt) {
		t.Fatalf("Heartbeat() = %#v", heartbeat)
	}
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
	if err != nil {
		t.Fatalf("Checkpoint() error = %v", err)
	}
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
			if err != nil {
				t.Fatalf("Complete() error = %v", err)
			}
			requireStoredPlatformJob(t, ctx, persistence, completed)
			attempts, err := persistence.Job().ListAttempts(ctx, completed.ID)
			if err != nil || len(attempts) != 1 || !attempts[0].CompletedAt.Valid || attempts[0].CompletedAt.Time.Before(attempts[0].HeartbeatAt) {
				t.Fatalf("ListAttempts() = %#v, %v", attempts, err)
			}
			if kind == store.JobCompletionRetryableFailure && !completed.AvailableAt.After(completed.UpdatedAt) {
				t.Fatalf("retry available_at %v does not follow %v", completed.AvailableAt, completed.UpdatedAt)
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
		Action: string(model.ActionJobManage), Resource: model.Resource{Type: model.ResourceInstitution, ID: model.NewId()},
		ScopeType: model.RoleScopeInstitution, ScopeID: model.NewId(), Status: model.AuditStatusAttempt, NodeID: "test-node",
		Parameters: []byte(`{"operation":"cancel","job_id":"` + job.ID.String() + `"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	canceled, err := persistence.Job().Cancel(ctx, &store.JobMutation{ID: job.ID, ExpectedRevision: job.Revision, AuditEventID: audit.ID, AuditAt: model.GetMillis()})
	if err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}
	if canceled.Status != model.JobStatusCanceled {
		t.Fatalf("Cancel() = %#v", canceled)
	}
	requireStoredPlatformJob(t, ctx, persistence, canceled)
}

func enqueuePlatformMail(t *testing.T, ctx context.Context, persistence *SQLStore, name string) *model.MailDelivery {
	t.Helper()
	institution, err := persistence.Institution().Save(ctx, &model.Institution{Name: name, DisplayName: name})
	if err != nil {
		t.Fatal(err)
	}
	user := saveIntegrationUser(t, ctx, persistence, &model.User{Username: name, Email: name + "@example.edu"})
	delivery, err := persistence.Mail().EnqueueTest(ctx, storetest.MailTestEnqueueFixtureForSQLTest(t, user, institution, model.NowUTC()))
	if err != nil {
		t.Fatal(err)
	}
	return delivery
}

func requireStoredPlatformMail(t *testing.T, ctx context.Context, persistence *SQLStore, returned *model.MailDelivery) {
	t.Helper()
	if err := returned.Validate(); err != nil {
		t.Fatalf("returned delivery = %#v, %v", returned, err)
	}
	stored, err := persistence.Mail().GetDelivery(ctx, returned.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != returned.State || !stored.UpdatedAt.Equal(returned.UpdatedAt) || stored.Revision != returned.Revision ||
		stored.AcceptedAt != returned.AcceptedAt || stored.FailedAt != returned.FailedAt {
		t.Fatalf("GetDelivery() = %#v, returned %#v", stored, returned)
	}
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
		if err != nil {
			t.Fatalf("StartDelivery() error = %v", err)
		}
		if sending.State != model.MailDeliverySending || !sending.UpdatedAt.Equal(delivery.UpdatedAt.Add(time.Minute)) {
			t.Fatalf("StartDelivery() = %#v", sending)
		}
		requireStoredPlatformMail(t, ctx, persistence, sending)
	})

	t.Run("accept", func(t *testing.T) {
		resetTestStore(t, persistence)
		delivery := enqueuePlatformMail(t, ctx, persistence, "mail-accept-leads")
		sending, err := persistence.Mail().StartDelivery(ctx, delivery.ID, delivery.Revision, model.NowUTC())
		if err != nil {
			t.Fatal(err)
		}
		leadPlatformRow(t, ctx, persistence, "mail_deliveries", "updated_at", delivery.ID.String())
		accepted, err := persistence.Mail().CompleteDelivery(ctx, &store.MailDeliveryCompletion{
			DeliveryID: delivery.ID, ExpectedRevision: sending.Revision, Kind: store.MailDeliveryCompletionAccepted, At: model.NowUTC(),
		})
		if err != nil {
			t.Fatalf("CompleteDelivery() error = %v", err)
		}
		if accepted.State != model.MailDeliveryAccepted || !accepted.AcceptedAt.Time.Equal(accepted.UpdatedAt) ||
			!accepted.UpdatedAt.Equal(sending.UpdatedAt.Add(time.Minute)) {
			t.Fatalf("CompleteDelivery() = %#v", accepted)
		}
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
	// The uploading node's clock leads the node that publishes the picture.
	leadPlatformRow(t, ctx, persistence, "upload_leases", "created_at,updated_at", lease.ID.String())
	renditions := make([]model.FileRendition, 0, 3)
	for _, size := range []int{128, 256, 512} {
		rendition, renditionErr := model.NewFileRendition(model.NewFileRenditionID(), revision.ID, fmt.Sprintf("profile_%d", size), "image/webp", 11, size, size, strings.Repeat("d", 64), at)
		if renditionErr != nil {
			t.Fatal(renditionErr)
		}
		renditions = append(renditions, *rendition)
	}
	if _, err = persistence.File().PublishDefaultProfilePicture(ctx, &store.DefaultProfilePicturePublication{
		UserID: user.ID, ExpectedUserRevision: user.Revision, EntryID: entry.ID, RevisionID: revision.ID, LeaseID: lease.ID,
		Renditions: renditions, AttachedAt: model.NowUTC(),
	}); err != nil {
		t.Fatalf("PublishDefaultProfilePicture() error = %v", err)
	}
	var row uploadLeaseRow
	if err = persistence.GetMaster().Get(ctx, &row, `SELECT id, file_revision_id, created_by_user_id, created_at, updated_at, expires_at, consumed_at, revision, bytes_received FROM upload_leases WHERE id=?`, lease.ID.String()); err != nil {
		t.Fatal(err)
	}
	consumed, err := row.model()
	if err != nil {
		t.Fatalf("consumed lease model error = %v (%#v)", err, row)
	}
	if !consumed.ConsumedAt.Valid || !consumed.ConsumedAt.Time.Equal(consumed.CreatedAt) {
		t.Fatalf("consumed lease = %#v", consumed)
	}
}

func TestTimeAuthorityPlatformAcademicArchiveKeepsModificationMonotonic(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	institution, err := persistence.Institution().Save(ctx, &model.Institution{Name: "archive-leads", DisplayName: "Archive leads"})
	if err != nil {
		t.Fatal(err)
	}
	unit, err := storetest.SaveAcademicUnit(ctx, persistence, &model.AcademicUnit{InstitutionID: institution.ID, Name: "archive-leads", DisplayName: "Archive leads"})
	if err != nil {
		t.Fatal(err)
	}
	// A later update by a node whose clock leads the archiving node.
	if _, err = persistence.GetMaster().Exec(ctx, `UPDATE academic_units SET updated_at=updated_at+INTERVAL '1 minute' WHERE id=?`, unit.ID.String()); err != nil {
		t.Fatal(err)
	}
	archiveAt := model.GetMillis()
	archived, err := storetest.ArchiveAcademicUnit(ctx, persistence, unit.ID, archiveAt)
	if err != nil {
		t.Fatalf("Archive() error = %v", err)
	}
	if err = archived.Validate(); err != nil || !archived.UpdatedAt.Equal(unit.UpdatedAt.Add(time.Minute)) ||
		archived.ArchivedAt.Time.Before(archived.CreatedAt) {
		t.Fatalf("Archive() = %#v, %v", archived, err)
	}
	var stored struct {
		UpdatedAt  time.Time `db:"updated_at"`
		ArchivedAt time.Time `db:"archived_at"`
	}
	if err = persistence.GetMaster().Get(ctx, &stored, `SELECT updated_at, archived_at FROM academic_units WHERE id=?`, unit.ID.String()); err != nil {
		t.Fatal(err)
	}
	if !model.TimeUTC(stored.UpdatedAt).Equal(archived.UpdatedAt) || !model.TimeUTC(stored.ArchivedAt).Equal(archived.ArchivedAt.Time) {
		t.Fatalf("stored archive = %#v, returned %#v", stored, archived)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.Exec(ctx, `SET LOCAL session_replication_role = replica`); err != nil {
		t.Fatal(err)
	}
	var startedAt time.Time
	if err = tx.Get(ctx, &startedAt, `SELECT date_trunc('milliseconds', clock_timestamp()) + INTERVAL '700 microseconds'`); err != nil {
		t.Fatal(err)
	}
	attemptID, participationID := model.NewId(), model.NewId()
	if _, err = tx.Exec(ctx, `INSERT INTO exam_attempt_participations(id,exam_attempt_id,session_id,state,generation,continuity_credential_hash,started_at,updated_at,lease_expires_at)
		VALUES(?,?,?,'active',1,?,?,?,?)`, participationID, attemptID, model.NewId(), strings.Repeat("a", 64), startedAt, startedAt, startedAt.Add(20*time.Second)); err != nil {
		t.Fatal(err)
	}
	sourceID := model.BrowserSourceSessionID("4f1d6a0e-8c43-4f5e-9d2b-6a1c3e5f7b90")
	digest := "sha256:" + strings.Repeat("b", 64)
	if _, err = tx.Exec(ctx, `INSERT INTO browser_activity_sources(id,exam_id,exam_sitting_id,exam_attempt_id,participation_id,generation,session_id,connection_id,
		candidate_user_id,registration_id,key_thumbprint,policy_revision_id,policy_digest,start_ordinal,start_transition,start_digest,start_canonical,reserved_bytes,state,started_at)
		VALUES(?::uuid,?,?,?,?,1,?,?,?,?,'thumbprint',?,?,1,'initial',?,'\x00'::bytea,1,'current',?)`,
		string(sourceID), model.NewId(), model.NewId(), attemptID, participationID, model.NewId(), model.NewId(), model.NewId(), model.NewId(), model.NewId(), digest, digest, startedAt); err != nil {
		t.Fatal(err)
	}
	closeErr := closeBrowserSource(ctx, tx, sourceID, model.DeliveryClosedRuntimeReset, startedAt.Add(200*time.Microsecond))
	var postgresErr *pq.Error
	if errors.As(closeErr, &postgresErr) {
		t.Fatalf("closeBrowserSource() error = %v (%s)", closeErr, postgresErr.Constraint)
	}
	// Settlement needs the Participation owner rows this fixture omits; the
	// closure UPDATE has already run.
	var ended struct {
		State   string    `db:"state"`
		Started time.Time `db:"started_at"`
		Ended   time.Time `db:"ended_at"`
	}
	if err = tx.Get(ctx, &ended, `SELECT state, started_at, ended_at FROM browser_activity_sources WHERE id=?::uuid`, string(sourceID)); err != nil {
		t.Fatalf("read closed source: %v (close error %v)", err, closeErr)
	}
	want := startedAt.Truncate(time.Millisecond).Add(time.Millisecond)
	if ended.State != "closed" || !ended.Ended.Equal(want) || ended.Ended.Before(ended.Started) {
		t.Fatalf("closed source = %#v, want ended_at %v", ended, want)
	}
}
