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
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/storetest"
)

// timeAuthorityExamStore runs an existing conformance flow while moving the
// rows a mutation supersedes ahead of the database clock immediately before
// the mutation, as a node whose clock leads PostgreSQL would have stamped them.
type timeAuthorityExamStore struct {
	*SQLStore
	authoring  store.ExamAuthoringStore
	resources  store.ExamResourceStore
	correction store.ExamCorrectionStore
}

func (s *timeAuthorityExamStore) ExamAuthoring() store.ExamAuthoringStore {
	if s.authoring != nil {
		return s.authoring
	}
	return s.SQLStore.ExamAuthoring()
}

func (s *timeAuthorityExamStore) ExamResource() store.ExamResourceStore {
	if s.resources != nil {
		return s.resources
	}
	return s.SQLStore.ExamResource()
}

func (s *timeAuthorityExamStore) ExamCorrection() store.ExamCorrectionStore {
	if s.correction != nil {
		return s.correction
	}
	return s.SQLStore.ExamCorrection()
}

type timeAuthorityLead struct {
	t           *testing.T
	persistence *SQLStore
	mu          sync.Mutex
	moved       int64
}

func (l *timeAuthorityLead) exec(ctx context.Context, query string, args ...any) {
	l.t.Helper()
	result, err := l.persistence.GetMaster().Exec(ctx, query, args...)
	if !assert.NoError(l.t, err) {
		return
	}
	affected, err := result.RowsAffected()
	if !assert.NoError(l.t, err) {
		return
	}
	l.mu.Lock()
	l.moved += affected
	l.mu.Unlock()
}

func (l *timeAuthorityLead) requireMoved() {
	l.t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	require.NotEqual(l.t, int64(0), l.moved, "no row was moved ahead of the database clock")
}

type timeAuthorityManagerStore struct {
	store.ExamAuthoringStore
	lead *timeAuthorityLead
}

func (s timeAuthorityManagerStore) AddManager(ctx context.Context, input *store.ExamManagerMutation, command *store.CommandIdempotency) (*store.ExamManagerCommandResult, error) {
	if input != nil {
		s.lead.exec(ctx, `UPDATE exams SET created_at=created_at+INTERVAL '1 minute',updated_at=updated_at+INTERVAL '1 minute'
			WHERE id=? AND archived_at IS NULL`, input.ExamID.String())
	}
	return s.ExamAuthoringStore.AddManager(ctx, input, command)
}

// TestTimeAuthorityExamManagerMutationKeepsExamLifecycle proves an Exam
// Manager mutation never records an Exam modification before the Exam's own
// lifecycle when the Exam row was stamped by a leading node clock (or inside
// the same millisecond as the mutation's millisecond instant).
func TestTimeAuthorityExamManagerMutationKeepsExamLifecycle(t *testing.T) {
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	lead := &timeAuthorityLead{t: t, persistence: persistence}
	decorated := &timeAuthorityExamStore{SQLStore: persistence,
		authoring: timeAuthorityManagerStore{ExamAuthoringStore: persistence.ExamAuthoring(), lead: lead}}
	storetest.TestExamAuthoringStore(t, decorated)
	lead.requireMoved()
	requireExamLifecycleRows(t, persistence)
}

type timeAuthorityResourceStore struct {
	store.ExamResourceStore
	lead *timeAuthorityLead
}

func (s timeAuthorityResourceStore) Reorder(ctx context.Context, input *store.ExamResourceReorder, command *store.CommandIdempotency) (*store.ExamResourceCommandResult, error) {
	if input != nil {
		s.lead.exec(ctx, `UPDATE exam_resources SET created_at=created_at+INTERVAL '1 minute',updated_at=updated_at+INTERVAL '1 minute'
			WHERE exam_id=? AND archived_at IS NULL`, input.ExamID.String())
	}
	return s.ExamResourceStore.Reorder(ctx, input, command)
}

// TestTimeAuthorityExamResourceReorderKeepsResourceLifecycle proves Resource
// modification stamps never move before a Resource stamped by a leading node.
func TestTimeAuthorityExamResourceReorderKeepsResourceLifecycle(t *testing.T) {
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	lead := &timeAuthorityLead{t: t, persistence: persistence}
	decorated := &timeAuthorityExamStore{SQLStore: persistence,
		resources: timeAuthorityResourceStore{ExamResourceStore: persistence.ExamResource(), lead: lead}}
	storetest.TestExamResourceStore(t, decorated)
	lead.requireMoved()
	var violations int
	require.NoError(t, persistence.GetMaster().Get(context.Background(), &violations, `SELECT count(*) FROM exam_resources
		WHERE updated_at<created_at OR archived_at<created_at`))
	require.Equal(t, 0, violations, "Exam Resources with modification before creation")
}

type timeAuthorityCorrectionStore struct {
	store.ExamCorrectionStore
	lead *timeAuthorityLead
}

func (s timeAuthorityCorrectionStore) Apply(ctx context.Context, input *store.ExamCorrectionApplication, command *store.CommandIdempotency) (*store.ExamCorrectionResult, error) {
	if input != nil {
		for _, item := range input.Resources {
			if item.StageID.IsZero() {
				continue
			}
			// Only ready, unexpired stages are moved; the lease keeps expires_at > created_at.
			s.lead.exec(ctx, `UPDATE upload_leases l SET created_at=l.created_at+INTERVAL '1 minute',updated_at=l.updated_at+INTERVAL '1 minute'
				FROM exam_correction_resource_stages s WHERE s.id=? AND s.state='ready' AND l.id=s.upload_lease_id
				AND l.consumed_at IS NULL AND l.expires_at>l.created_at+INTERVAL '2 minutes'`, item.StageID.String())
			s.lead.exec(ctx, `UPDATE exam_correction_resource_stages s SET created_at=s.created_at+INTERVAL '1 minute',ready_at=s.ready_at+INTERVAL '1 minute'
				FROM upload_leases l WHERE s.id=? AND s.state='ready' AND l.id=s.upload_lease_id
				AND s.cleanup_protected_until>s.created_at+INTERVAL '2 minutes'`, item.StageID.String())
		}
	}
	return s.ExamCorrectionStore.Apply(ctx, input, command)
}

// TestTimeAuthorityExamCorrectionConsumesStagesAfterTheirLifecycle proves a
// live correction consumes node-clock staged uploads at PostgreSQL time
// without recording consumption before the stage's readiness or the upload
// lease's creation.
func TestTimeAuthorityExamCorrectionConsumesStagesAfterTheirLifecycle(t *testing.T) {
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	lead := &timeAuthorityLead{t: t, persistence: persistence}
	decorated := &timeAuthorityExamStore{SQLStore: persistence,
		correction: timeAuthorityCorrectionStore{ExamCorrectionStore: persistence.ExamCorrection(), lead: lead}}
	storetest.TestExamCorrectionStore(t, decorated, examCorrectionSQLProbe(t, persistence))
	lead.requireMoved()
	var violations int
	require.NoError(t, persistence.GetMaster().Get(context.Background(), &violations, `SELECT
		(SELECT count(*) FROM exam_correction_resource_stages WHERE consumed_at<ready_at OR ready_at<created_at)+
		(SELECT count(*) FROM upload_leases WHERE consumed_at<created_at OR updated_at<created_at)`))
	require.Equal(t, 0, violations, "correction stages or leases consumed before their lifecycle")
}

type timeAuthorityReviewStore struct {
	store.ExamIntegrityReviewStore
	lead  *timeAuthorityLead
	mu    sync.Mutex
	moved map[model.SubmissionReviewID]bool
}

func (s *timeAuthorityReviewStore) PrepareRelease(ctx context.Context, submissionID model.SubmissionID, reviewID model.SubmissionReviewID, expectedRevision int64) (*store.ExamIntegrityReviewReleasePreparation, error) {
	s.mu.Lock()
	first := !s.moved[reviewID]
	s.moved[reviewID] = true
	s.mu.Unlock()
	if first {
		s.leadFinalizedReview(ctx, reviewID)
	}
	return s.ExamIntegrityReviewStore.PrepareRelease(ctx, submissionID, reviewID, expectedRevision)
}

// leadFinalizedReview moves a finalized Review's immutable lifecycle ahead of
// the database clock. The Review guard trigger forbids that rewrite, so the
// probe suspends it inside its own transaction only.
func (s *timeAuthorityReviewStore) leadFinalizedReview(ctx context.Context, reviewID model.SubmissionReviewID) {
	t := s.lead.t
	t.Helper()
	tx, err := s.lead.persistence.GetMaster().DB().BeginTxx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `ALTER TABLE submission_reviews DISABLE TRIGGER submission_reviews_guard`)
	require.NoError(t, err)
	result, err := tx.ExecContext(ctx, `UPDATE submission_reviews SET created_at=created_at+INTERVAL '1 minute',
		updated_at=updated_at+INTERVAL '1 minute',finalized_at=finalized_at+INTERVAL '1 minute'
		WHERE id=$1 AND state='finalized' AND release_state='withheld'`, reviewID.String())
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `ALTER TABLE submission_reviews ENABLE TRIGGER submission_reviews_guard`)
	require.NoError(t, err)
	err = tx.Commit()
	require.NoError(t, err)
	affected, err := result.RowsAffected()
	require.NoError(t, err)
	s.lead.mu.Lock()
	s.lead.moved += affected
	s.lead.mu.Unlock()
}

// TestTimeAuthorityStudentResultReleaseFollowsFinalization proves a Student
// Result released at PostgreSQL time never precedes a Review finalized by a
// node whose clock leads the database, and that the reserved release instant
// remains accepted by the terminal release.
func TestTimeAuthorityStudentResultReleaseFollowsFinalization(t *testing.T) {
	persistence := openTestStore(t)
	peerPersistence := openTestStore(t)
	resetTestStore(t, persistence)
	lead := &timeAuthorityLead{t: t, persistence: persistence}
	reviews := &timeAuthorityReviewStore{ExamIntegrityReviewStore: NewSQLExamIntegrityReviewStore(persistence), lead: lead,
		moved: map[model.SubmissionReviewID]bool{}}
	storetest.TestExamIntegrityReviewStore(t, persistence, reviews,
		storetest.ExamIntegrityReviewSQLProbe{ConcurrentPeer: NewSQLExamIntegrityReviewStore(peerPersistence)})
	lead.requireMoved()
	var violations int
	require.NoError(t, persistence.GetMaster().Get(context.Background(), &violations, `SELECT count(*) FROM submission_reviews
		WHERE released_at<finalized_at OR updated_at<created_at OR finalized_at<created_at`))
	require.Equal(t, 0, violations, "Submission Reviews released before finalization")
}

// TestTimeAuthoritySittingMailFanoutTerminalizationFollowsCreation proves
// fan-out maintenance at transaction-start PostgreSQL time never records a
// completion before a fan-out committed later by another transaction.
func TestTimeAuthoritySittingMailFanoutTerminalizationFollowsCreation(t *testing.T) {
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	fixture := storetest.PrepareExamSittingMailRecoveryFixture(t, persistence)
	ctx := context.Background()
	_, err := persistence.GetMaster().Exec(ctx, `UPDATE exam_sitting_mail_fanouts
		SET created_at=created_at+INTERVAL '1 minute',deadline=deadline+INTERVAL '1 minute' WHERE occurrence_id=?`,
		fixture.OrphanOccurrence.String())
	require.NoError(t, err)
	_, err = persistence.GetMaster().Exec(ctx, `DELETE FROM jobs WHERE id=?`, fixture.OrphanJob.String())
	require.NoError(t, err)
	result, err := persistence.ExamSitting().MaintainMailExpansions(ctx, 2)
	require.NoError(t, err)
	require.Equal(t, 1, result.FanoutsTerminalized)
	var row struct {
		CreatedAt      time.Time `db:"created_at"`
		CompletedAt    time.Time `db:"completed_at"`
		TerminalReason string    `db:"terminal_reason"`
	}
	err = persistence.GetMaster().Get(ctx, &row, `SELECT created_at,completed_at,terminal_reason
		FROM exam_sitting_mail_fanouts WHERE occurrence_id=?`, fixture.OrphanOccurrence.String())
	require.NoError(t, err)
	require.Equal(t, "orphaned", row.TerminalReason)
	require.False(t, row.CompletedAt.Before(row.CreatedAt))
	fanout, err := persistence.ExamSitting().GetMailFanout(ctx, fixture.OrphanOccurrence)
	require.NoError(t, err)
	require.True(t, fanout.CompletedAt.Valid, "GetMailFanout().CompletedAt=%v stored=%v", fanout.CompletedAt, row.CompletedAt)
	require.True(t, fanout.CompletedAt.Time.Equal(model.TimeUTC(row.CompletedAt)), "GetMailFanout().CompletedAt=%v stored=%v", fanout.CompletedAt, row.CompletedAt)
}

func requireExamLifecycleRows(t *testing.T, persistence *SQLStore) {
	t.Helper()
	var violations int
	require.NoError(t, persistence.GetMaster().Get(context.Background(), &violations, `SELECT count(*) FROM exams
		WHERE updated_at<created_at OR archived_at<created_at`))
	require.Equal(t, 0, violations, "Exams with modification before creation")
}
