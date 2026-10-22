// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package storetest

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestExamSittingStore(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture := newExamSittingFixture(t, ctx, ss)
	start := fixture.period.StartsAt.Add(time.Hour)
	end := start.Add(2 * time.Hour)
	sitting, err := model.NewExamSitting(model.NewExamSittingID(), fixture.examID, fixture.revisionID, fixture.class.ID, start, end, model.NowUTC())
	require.NoError(t, err)
	openJob, deadlineJob := newExamSittingLifecycleJobs(t, sitting.ID, 1, start, end)
	scheduleMail := newExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailScheduled, model.MailTemplateExamSittingScheduled)
	scheduleAudit := saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID)
	command := examCommand(fixture.actor.ID, "exam.sitting.schedule.v1", "sitting-schedule", "sitting-schedule-command")
	created, err := ss.ExamSitting().Schedule(ctx, &store.ExamSittingSchedule{Sitting: sitting, OpenJob: openJob, DeadlineJob: deadlineJob, ActorUserID: fixture.actor.ID,
		AuditEventID: scheduleAudit.ID, AuditAt: model.GetMillis(), Mail: scheduleMail}, command)
	require.NoError(t, err)
	require.False(t, created.Replayed)
	require.NotNil(t, created.Value)
	require.NotNil(t, created.Value.Sitting)
	require.Equal(t, sitting.ID, created.Value.Sitting.ID)
	require.Equal(t, int64(1), created.Value.Sitting.Revision)
	fanout, err := ss.ExamSitting().GetMailFanout(ctx, scheduleMail.Occurrence.ID)
	require.NoError(t, err)
	require.Equal(t, sitting.ID, fanout.SittingID)
	require.Equal(t, int64(1), fanout.SittingRevision)
	require.Equal(t, store.ExamSittingMailScheduled, fanout.ChangeKind)
	require.NotNil(t, fanout.Bundle)
	require.Equal(t, scheduleMail.Occurrence.ID, fanout.Bundle.ID)
	require.False(t, fanout.CompletedAt.Valid)
	require.True(t, fanout.Deadline.Equal(fanout.Occurrence.CreatedAt.Add(72*time.Hour)))
	queued, getErr := ss.Job().Get(ctx, scheduleMail.ExpansionJob.ID)
	require.NoError(t, getErr, "expansion Job=(%#v,%v)", queued, getErr)
	require.Equal(t, model.JobTypeMailExpandSitting, queued.Type, "expansion Job=(%#v,%v)", queued, getErr)
	candidateInput := newUser()
	candidateInput.EmailVerified = true
	candidate, err := createUser(t, ctx, ss, candidateInput)
	require.NoError(t, err)
	_, err = ss.Affiliation().Save(ctx, &model.Affiliation{UserID: candidate.ID, Kind: model.AffiliationStudent,
		StartsAt: fixture.period.StartsAt})
	require.NoError(t, err)
	membership := &model.ClassMember{ClassID: fixture.class.ID, UserID: candidate.ID,
		AcademicPeriodID: fixture.class.AcademicPeriodID, StartsAt: fixture.period.StartsAt}
	membership.PrepareCreate(model.NewClassMemberID(), model.NowUTC())
	membershipAudit := saveClassMemberAuditAttempt(t, ctx, ss, fixture.class.ID.String())
	_, err = ss.ClassMember().Enroll(ctx, &store.ClassMemberEnrollment{Member: membership,
		ExpectedRecipientRevision: candidate.Revision,
		Notice:                    classMemberPreparedMail(t, membership, model.MailTemplateAcademicClassEnrolled, membership.CreatedAt),
		AuditEventID:              membershipAudit.ID, AuditAt: model.GetMillis()})
	require.NoError(t, err)
	candidateInvalidations, err := ss.ExamSitting().ListCandidateInvalidationTargetsBySitting(
		ctx, sitting.ID, model.UserID(""), 200,
	)
	require.NoError(t, err)
	require.Len(t, candidateInvalidations, 1)
	require.Equal(t, candidate.ID, candidateInvalidations[0])
	boardInvalidations, err := ss.ExamSitting().ListInvalidationTargetsByExam(
		ctx, fixture.examID, model.ExamSittingID(""), 200,
	)
	require.NoError(t, err)
	require.Len(t, boardInvalidations, 1)
	require.Equal(t, store.ExamSittingInvalidationTarget{
		ExamID: fixture.examID, SittingID: sitting.ID,
	}, boardInvalidations[0])
	mailPage, err := ss.ExamSitting().ListMailRecipients(ctx, store.ExamSittingMailRecipientPageRequest{
		OccurrenceID: scheduleMail.Occurrence.ID, Limit: model.SittingMailExpansionPageSize})
	require.NoError(t, err)
	require.Len(t, mailPage.Recipients, 1)
	require.Equal(t, candidate.ID, mailPage.Recipients[0].User.ID)
	require.Equal(t, model.MailTemplateExamSittingScheduled, mailPage.Recipients[0].TemplateKey)
	delivery, deliveryJob := newExamSittingMailDelivery(t, fanout, candidate, mailPage.Recipients[0].TemplateKey)
	committed, err := ss.ExamSitting().CommitMailRecipient(ctx, &store.ExamSittingMailRecipientCommit{
		OccurrenceID: fanout.Occurrence.ID, SittingRevision: fanout.SittingRevision, Recipient: candidate,
		Delivery: delivery, DeliveryJob: deliveryJob})
	require.NoError(t, err)
	require.True(t, committed.Inserted)
	require.NotNil(t, committed.Delivery)
	require.Equal(t, delivery.ID, committed.Delivery.ID)
	sending, err := ss.Mail().StartDelivery(ctx, delivery.ID, delivery.Revision, model.NowUTC())
	require.NoError(t, err)
	accepted, err := ss.Mail().CompleteDelivery(ctx, &store.MailDeliveryCompletion{DeliveryID: delivery.ID,
		ExpectedRevision: sending.Revision, Kind: store.MailDeliveryCompletionAccepted, At: model.NowUTC()})
	require.NoError(t, err)
	require.Equal(t, model.MailDeliveryAccepted, accepted.State)
	mailPage, err = ss.ExamSitting().ListMailRecipients(ctx, store.ExamSittingMailRecipientPageRequest{
		OccurrenceID: scheduleMail.Occurrence.ID, Limit: model.SittingMailExpansionPageSize})
	require.NoError(t, err)
	require.Len(t, mailPage.Recipients, 1)
	require.Empty(t, mailPage.Recipients[0].TemplateKey)
	completedFanout, err := ss.ExamSitting().CompleteMailExpansion(ctx,
		&store.ExamSittingMailExpansionCompletion{OccurrenceID: fanout.Occurrence.ID})
	require.NoError(t, err)
	require.True(t, completedFanout.CompletedAt.Valid)
	require.Nil(t, completedFanout.Bundle)
	lateInput := newUser()
	lateInput.EmailVerified = true
	lateCandidate, err := createUser(t, ctx, ss, lateInput)
	require.NoError(t, err)
	_, err = ss.Affiliation().Save(ctx, &model.Affiliation{UserID: lateCandidate.ID, Kind: model.AffiliationStudent,
		StartsAt: fixture.period.StartsAt})
	require.NoError(t, err)
	lateMembership := &model.ClassMember{ClassID: fixture.class.ID, UserID: lateCandidate.ID,
		AcademicPeriodID: fixture.class.AcademicPeriodID, StartsAt: fixture.period.StartsAt}
	lateMembership.PrepareCreate(model.NewClassMemberID(), model.NowUTC())
	lateAudit := saveClassMemberAuditAttempt(t, ctx, ss, fixture.class.ID.String())
	_, err = ss.ClassMember().Enroll(ctx, &store.ClassMemberEnrollment{Member: lateMembership,
		ExpectedRecipientRevision: lateCandidate.Revision,
		Notice:                    classMemberPreparedMail(t, lateMembership, model.MailTemplateAcademicClassEnrolled, lateMembership.CreatedAt),
		AuditEventID:              lateAudit.ID, AuditAt: model.GetMillis()})
	require.NoError(t, err)
	due, err := ss.ExamSitting().ListMailReconciliationDue(ctx,
		store.ExamSittingMailReconciliationOptions{Limit: model.SittingMailExpansionPageSize})
	require.NoError(t, err)
	var dueSitting *store.ExamSittingMailReconciliationCandidate
	for index := range due {
		if due[index].Sitting.ID == sitting.ID {
			dueSitting = &due[index]
		}
	}
	require.NotNil(t, dueSitting)
	require.Equal(t, fixture.actor.ID, dueSitting.ActorUserID)
	reconciliationMail := newExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailReconciled,
		model.MailTemplateExamSittingScheduled)
	reconciled, err := ss.ExamSitting().ReconcileMail(ctx, &store.ExamSittingMailReconciliation{
		SittingID: sitting.ID, ExpectedRevision: sitting.Revision, ActorUserID: fixture.actor.ID, Mail: reconciliationMail})
	require.NoError(t, err)
	mailPage, err = ss.ExamSitting().ListMailRecipients(ctx, store.ExamSittingMailRecipientPageRequest{
		OccurrenceID: reconciled.Occurrence.ID, Limit: model.SittingMailExpansionPageSize})
	require.NoError(t, err)
	keys := make(map[model.UserID]model.MailTemplateKey, len(mailPage.Recipients))
	for _, recipient := range mailPage.Recipients {
		keys[recipient.User.ID] = recipient.TemplateKey
	}
	require.Empty(t, keys[candidate.ID])
	require.Equal(t, model.MailTemplateExamSittingScheduled, keys[lateCandidate.ID])
	_, err = ss.ExamSitting().CompleteMailExpansion(ctx,
		&store.ExamSittingMailExpansionCompletion{OccurrenceID: reconciled.Occurrence.ID})
	require.NoError(t, err)
	exact, err := ss.ExamSitting().Get(ctx, fixture.examID, sitting.ID)
	require.NoError(t, err)
	require.Equal(t, fixture.unitID, exact.AcademicUnitID)
	require.Equal(t, fixture.revisionID, exact.Sitting.ExamRevisionID)
	require.Equal(t, fixture.class.ID, exact.Sitting.ClassID)
	require.Equal(t, end, exact.Sitting.ScheduledEndAt)
	resolved, err := ss.ExamSitting().Resolve(ctx, sitting.ID)
	require.NoError(t, err)
	require.Equal(t, fixture.unitID, resolved.AcademicUnitID)
	require.Equal(t, sitting.ID, resolved.Sitting.ID)
	require.Equal(t, fixture.examID, resolved.Sitting.ExamID)
	_, err = ss.ExamSitting().Get(ctx, model.NewExamID(), sitting.ID)
	require.True(t, store.IsNotFound(err), "Get(foreign Exam) error")

	// A sealed Revision is not transferable between Exams, even when both
	// Exams belong to the same Academic Unit. The Store returns one stable
	// lineage conflict without disclosing the foreign snapshot.
	foreignExam := createCatalogExam(t, ctx, ss, fixture.unitID, fixture.actor.ID, model.NowUTC(), "sitting-foreign-exam")
	foreignPublication := examRevisionPublication(t, ctx, ss, foreignExam.Value.Exam.ID, fixture.actor.ID, fixture.unitID, 1, model.NowUTC())
	foreignRevision, err := ss.ExamRevision().Publish(ctx, foreignPublication,
		examCommand(fixture.actor.ID, "exam.revision.publish.v1", "sitting-foreign-revision", "sitting-foreign-revision-command"))
	require.NoError(t, err)
	foreignSelection, err := model.NewExamSitting(model.NewExamSittingID(), fixture.examID, foreignRevision.Revision.ID,
		fixture.class.ID, start, end, model.NowUTC())
	require.NoError(t, err)
	foreignOpen, foreignDeadline := newExamSittingLifecycleJobs(t, foreignSelection.ID, 1, start, end)
	_, err = ss.ExamSitting().Schedule(ctx, &store.ExamSittingSchedule{Sitting: foreignSelection, OpenJob: foreignOpen, DeadlineJob: foreignDeadline, ActorUserID: fixture.actor.ID,
		AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID, AuditAt: model.GetMillis(),
		Mail: newExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailScheduled, model.MailTemplateExamSittingScheduled)},
		examCommand(fixture.actor.ID, "exam.sitting.schedule.v1", "sitting-foreign-selection", "sitting-foreign-selection-command"))
	requireConflictConstraint(t, err, "exam_sitting_revision_lineage")

	newStart, newEnd := start.Add(time.Hour), end.Add(time.Hour)
	updatedOpen, updatedDeadline := newExamSittingLifecycleJobs(t, sitting.ID, 2, newStart, newEnd)
	updateAudit := saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID)
	updated, err := ss.ExamSitting().UpdateSchedule(ctx, &store.ExamSittingScheduleUpdate{ExamID: fixture.examID, SittingID: sitting.ID,
		ActorUserID: fixture.actor.ID, ExpectedRevision: 1, ExamRevisionID: fixture.revisionID, ClassID: fixture.class.ID,
		ScheduledStartAt: newStart, ScheduledEndAt: newEnd, OpenJob: updatedOpen, DeadlineJob: updatedDeadline,
		AuditEventID: updateAudit.ID, AuditAt: model.GetMillis(),
		Mail: newExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailRescheduled, model.MailTemplateExamSittingRescheduled)},
		examCommand(fixture.actor.ID, "exam.sitting.schedule.update.v1", "sitting-update", "sitting-update-command"))
	require.NoError(t, err)
	require.Equal(t, int64(2), updated.Value.Sitting.Revision)
	require.Equal(t, newStart, updated.Value.Sitting.ScheduledStartAt)
	require.Equal(t, newEnd, updated.Value.Sitting.ScheduledEndAt)

	cancelAudit := saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID)
	cancelCommand := examCommand(fixture.actor.ID, "exam.sitting.cancel.v1", "sitting-cancel", "sitting-cancel-command")
	canceled, err := ss.ExamSitting().Cancel(ctx, &store.ExamSittingCancellation{ExamID: fixture.examID, SittingID: sitting.ID,
		ActorUserID: fixture.actor.ID, ExpectedRevision: 2, PrivateReason: "Room unavailable",
		AuditEventID: cancelAudit.ID, AuditAt: model.GetMillis(),
		Mail: newExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailCanceled, model.MailTemplateExamSittingCanceled)}, cancelCommand)
	require.NoError(t, err)
	require.Equal(t, model.ExamSittingCanceled, canceled.Value.Sitting.State)
	require.Equal(t, model.ExamSittingReasonManagerCanceled, canceled.Value.Sitting.ReasonCode)
	require.Equal(t, int64(3), canceled.Value.Sitting.Revision)
	audit, err := ss.Audit().Get(ctx, cancelAudit.ID)
	require.NoError(t, err)
	require.False(t, bytes.Contains(audit.Result, []byte("Room unavailable")))
	require.False(t, bytes.Contains(audit.Parameters, []byte("Room unavailable")))

	// The committed schedule result wins before the now-canceled state and
	// stale relationship/revision fences.
	retry, err := ss.ExamSitting().Schedule(ctx, &store.ExamSittingSchedule{Sitting: sitting, OpenJob: openJob, DeadlineJob: deadlineJob, ActorUserID: fixture.actor.ID,
		AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID, AuditAt: model.GetMillis(), Mail: scheduleMail}, command)
	require.NoError(t, err)
	require.True(t, retry.Replayed)
	require.Equal(t, model.ExamSittingScheduled, retry.Value.Sitting.State)
	require.Equal(t, int64(1), retry.Value.Sitting.Revision)

	staleAudit := saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID)
	_, err = ss.ExamSitting().Cancel(ctx, &store.ExamSittingCancellation{ExamID: fixture.examID, SittingID: sitting.ID, ActorUserID: fixture.actor.ID,
		ExpectedRevision: 2, PrivateReason: "stale", AuditEventID: staleAudit.ID, AuditAt: model.GetMillis(),
		Mail: newExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailCanceled, model.MailTemplateExamSittingCanceled)},
		examCommand(fixture.actor.ID, "exam.sitting.cancel.v1", "sitting-cancel-stale", "sitting-cancel-stale-command"))
	requireConflictConstraint(t, err, "exam_sitting_revision")

	// A second Sitting makes list filtering and tuple pagination observable.
	secondStart := newStart.Add(24 * time.Hour)
	second, err := model.NewExamSitting(model.NewExamSittingID(), fixture.examID, fixture.revisionID, fixture.class.ID, secondStart, secondStart.Add(time.Hour), model.NowUTC())
	require.NoError(t, err)
	secondOpen, secondDeadline := newExamSittingLifecycleJobs(t, second.ID, 1, second.ScheduledStartAt, second.ScheduledEndAt)
	secondResult, err := ss.ExamSitting().Schedule(ctx, &store.ExamSittingSchedule{Sitting: second, OpenJob: secondOpen, DeadlineJob: secondDeadline, ActorUserID: fixture.actor.ID,
		AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID, AuditAt: model.GetMillis(),
		Mail: newExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailScheduled, model.MailTemplateExamSittingScheduled)},
		examCommand(fixture.actor.ID, "exam.sitting.schedule.v1", "sitting-second", "sitting-second-command"))
	require.NoError(t, err)
	page, err := ss.ExamSitting().List(ctx, store.ExamSittingListOptions{ExamID: fixture.examID, ClassID: fixture.class.ID,
		States: []model.ExamSittingState{model.ExamSittingScheduled}, OverlapStartAt: secondStart.Add(-time.Minute), OverlapEndAt: secondStart.Add(30 * time.Minute), Limit: 1})
	require.NoError(t, err)
	require.Len(t, page, 1)
	require.Equal(t, secondResult.Value.Sitting.ID, page[0].Sitting.ID)
	page, err = ss.ExamSitting().List(ctx, store.ExamSittingListOptions{ExamID: fixture.examID,
		BeforeScheduledStartAt: page[0].Sitting.ScheduledStartAt, BeforeSittingID: page[0].Sitting.ID, Limit: 201})
	require.NoError(t, err)
	require.Len(t, page, 1)
	require.Equal(t, sitting.ID, page[0].Sitting.ID)
}

// TestExamSittingMailReconciliationAfterObsoleteSuppression proves that a
// delivery suppressed by the authoritative pre-send membership fence does not
// strand the candidate's desired projection. A later eligible enrollment must
// make the unchanged upcoming Sitting due for reconciliation again.
func TestExamSittingMailReconciliationAfterObsoleteSuppression(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture := newExamSittingFixture(t, ctx, ss)
	start := fixture.period.StartsAt.Add(4 * time.Hour)
	end := start.Add(2 * time.Hour)
	sitting, err := model.NewExamSitting(model.NewExamSittingID(), fixture.examID, fixture.revisionID,
		fixture.class.ID, start, end, model.NowUTC())
	require.NoError(t, err)
	openJob, deadlineJob := newExamSittingLifecycleJobs(t, sitting.ID, 1, start, end)
	prepared := newExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailScheduled,
		model.MailTemplateExamSittingScheduled)
	created, err := ss.ExamSitting().Schedule(ctx, &store.ExamSittingSchedule{Sitting: sitting,
		OpenJob: openJob, DeadlineJob: deadlineJob, ActorUserID: fixture.actor.ID,
		AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID,
		AuditAt:      model.GetMillis(), Mail: prepared},
		examCommand(fixture.actor.ID, "exam.sitting.schedule.v1", "sitting-membership-reconcile", "sitting-membership-reconcile-command"))
	require.NoError(t, err)

	candidateInput := newUser()
	candidateInput.EmailVerified = true
	candidate, err := createUser(t, ctx, ss, candidateInput)
	require.NoError(t, err)
	_, err = ss.Affiliation().Save(ctx, &model.Affiliation{UserID: candidate.ID, Kind: model.AffiliationStudent,
		StartsAt: fixture.period.StartsAt})
	require.NoError(t, err)
	membership := &model.ClassMember{ClassID: fixture.class.ID, UserID: candidate.ID,
		AcademicPeriodID: fixture.class.AcademicPeriodID, StartsAt: fixture.period.StartsAt}
	membership.PrepareCreate(model.NewClassMemberID(), model.NowUTC())
	enrolled, err := ss.ClassMember().Enroll(ctx, &store.ClassMemberEnrollment{Member: membership,
		ExpectedRecipientRevision: candidate.Revision,
		Notice:                    classMemberPreparedMail(t, membership, model.MailTemplateAcademicClassEnrolled, membership.CreatedAt),
		AuditEventID:              saveClassMemberAuditAttempt(t, ctx, ss, fixture.class.ID.String()).ID, AuditAt: model.GetMillis()})
	require.NoError(t, err)

	fanout, err := ss.ExamSitting().GetMailFanout(ctx, prepared.Occurrence.ID)
	require.NoError(t, err)
	page, err := ss.ExamSitting().ListMailRecipients(ctx, store.ExamSittingMailRecipientPageRequest{
		OccurrenceID: prepared.Occurrence.ID, Limit: model.SittingMailExpansionPageSize})
	require.NoError(t, err)
	require.Len(t, page.Recipients, 1)
	require.Equal(t, candidate.ID, page.Recipients[0].User.ID)
	delivery, deliveryJob := newExamSittingMailDelivery(t, fanout, candidate, page.Recipients[0].TemplateKey)
	_, err = ss.ExamSitting().CommitMailRecipient(ctx, &store.ExamSittingMailRecipientCommit{
		OccurrenceID: fanout.Occurrence.ID, SittingRevision: fanout.SittingRevision, Recipient: candidate,
		Delivery: delivery, DeliveryJob: deliveryJob})
	require.NoError(t, err)
	_, err = ss.ExamSitting().CompleteMailExpansion(ctx,
		&store.ExamSittingMailExpansionCompletion{OccurrenceID: fanout.Occurrence.ID})
	require.NoError(t, err)

	changeAt := start.Add(-time.Hour)
	_, err = ss.ClassMember().End(ctx, &store.ClassMemberEnd{ID: enrolled.Membership.ID,
		ExpectedRevision: enrolled.Membership.Revision, ExpectedRecipientRevision: candidate.Revision,
		EndAt:        model.MillisFromTime(changeAt),
		Notice:       classMemberPreparedMail(t, enrolled.Membership, model.MailTemplateAcademicClassEnrollmentEnded, changeAt),
		AuditEventID: saveClassMemberAuditAttempt(t, ctx, ss, fixture.class.ID.String()).ID, AuditAt: model.GetMillis()})
	require.NoError(t, err)
	suppressed, err := ss.Mail().StartDelivery(ctx, delivery.ID, delivery.Revision, model.NowUTC())
	require.NoError(t, err)
	require.Equal(t, model.MailDeliverySuppressed, suppressed.State)
	require.Equal(t, model.MailDeliveryObsoleteCode, suppressed.PublicFailureCode)

	restored := &model.ClassMember{ClassID: fixture.class.ID, UserID: candidate.ID,
		AcademicPeriodID: fixture.class.AcademicPeriodID, StartsAt: changeAt}
	restored.PrepareCreate(model.NewClassMemberID(), model.NowUTC())
	_, err = ss.ClassMember().Enroll(ctx, &store.ClassMemberEnrollment{Member: restored,
		ExpectedRecipientRevision: candidate.Revision,
		Notice:                    classMemberPreparedMail(t, restored, model.MailTemplateAcademicClassEnrolled, restored.CreatedAt),
		AuditEventID:              saveClassMemberAuditAttempt(t, ctx, ss, fixture.class.ID.String()).ID, AuditAt: model.GetMillis()})
	require.NoError(t, err)
	due, err := ss.ExamSitting().ListMailReconciliationDue(ctx,
		store.ExamSittingMailReconciliationOptions{Limit: model.SittingMailExpansionPageSize})
	require.NoError(t, err)
	for _, item := range due {
		if item.Sitting.ID == created.Value.Sitting.ID {
			return
		}
	}
	require.FailNow(t, fmt.Sprintf("restored candidate did not make Sitting due for reconciliation: %#v", due))
}

// TestExamSittingDisabledMailReconciliationConverges proves that the terminal
// suppression recorded by a Sitting transition is authoritative for that
// revision. Repeated reconcilers, including concurrent nodes whose mail
// capability later becomes enabled, must not manufacture another occurrence.
type ExamSittingDisabledMailSQLProbe struct {
	AgeTerminalFanout func(*testing.T, context.Context, model.MailOccurrenceID)
}

// ExamSittingDisabledEligibilityRaceFixture exposes only the public aggregate
// inputs needed by the SQL adapter's deterministic chronology race tests.
type ExamSittingDisabledEligibilityRaceFixture struct {
	SittingID    model.ExamSittingID
	Schedule     *store.ExamSittingSchedule
	Command      *store.CommandIdempotency
	Verification *store.PrivilegedEmailVerification
}

type ExamSittingInvitationLockOrderFixture struct {
	Acceptance     *store.StudentClassInvitationAcceptance
	Reconciliation *store.ExamSittingMailReconciliation
}

func PrepareExamSittingInvitationLockOrderFixture(t *testing.T, ss store.Store) ExamSittingInvitationLockOrderFixture {
	t.Helper()
	ctx := context.Background()
	fixture := newExamSittingFixture(t, ctx, ss)
	role, err := SaveRole(ctx, ss, &model.Role{Name: "sitting-inviter-" + model.NewID(), DisplayName: "Sitting inviter",
		Permissions: []string{string(model.ActionInvitationCreate), string(model.ActionClassMembersManage)}})
	require.NoError(t, err)
	_, err = SaveRoleBinding(ctx, ss, &model.RoleBinding{UserID: fixture.actor.ID, RoleID: role.ID,
		ScopeType: model.RoleScopeClass, ScopeID: fixture.class.ID.String(), StartsAt: model.NowUTC().Add(-time.Hour)})
	require.NoError(t, err)
	issue := studentClassInvitationIssueFixture(t, ss, fixture.actor, fixture.class, fixture.period, model.NowUTC())
	issue.Invitation.TargetEmail = fixture.actor.Email
	invitation, err := ss.Invitation().IssueStudentClass(ctx, issue)
	require.NoError(t, err)
	acceptance := studentClassInvitationAcceptanceFixture(t, invitation, model.NowUTC())

	start := fixture.period.StartsAt.Add(20 * time.Hour)
	sitting, err := model.NewExamSitting(model.NewExamSittingID(), fixture.examID, fixture.revisionID,
		fixture.class.ID, start, start.Add(2*time.Hour), model.NowUTC())
	require.NoError(t, err)
	openJob, deadlineJob := newExamSittingLifecycleJobs(t, sitting.ID, 1, sitting.ScheduledStartAt, sitting.ScheduledEndAt)
	initialMail := newExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailScheduled,
		model.MailTemplateExamSittingScheduled)
	_, err = ss.ExamSitting().Schedule(ctx, &store.ExamSittingSchedule{Sitting: sitting, OpenJob: openJob,
		DeadlineJob: deadlineJob, ActorUserID: fixture.actor.ID,
		AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID,
		AuditAt:      model.GetMillis(), Mail: initialMail},
		examCommand(fixture.actor.ID, "exam.sitting.schedule.v1", "sitting-invitation-lock-order", "sitting-invitation-lock-order-command"))
	require.NoError(t, err)
	_, err = ss.ExamSitting().CompleteMailExpansion(ctx,
		&store.ExamSittingMailExpansionCompletion{OccurrenceID: initialMail.Occurrence.ID})
	require.NoError(t, err)
	lateInput := newUser()
	lateInput.EmailVerified = true
	late, err := createUser(t, ctx, ss, lateInput)
	require.NoError(t, err)
	_, err = ss.Affiliation().Save(ctx, &model.Affiliation{UserID: late.ID, Kind: model.AffiliationStudent,
		StartsAt: fixture.period.StartsAt})
	require.NoError(t, err)
	membership := &model.ClassMember{ClassID: fixture.class.ID, UserID: late.ID,
		AcademicPeriodID: fixture.class.AcademicPeriodID, StartsAt: fixture.period.StartsAt}
	membership.PrepareCreate(model.NewClassMemberID(), model.NowUTC())
	_, err = ss.ClassMember().Enroll(ctx, &store.ClassMemberEnrollment{Member: membership,
		ExpectedRecipientRevision: late.Revision,
		Notice:                    classMemberPreparedMail(t, membership, model.MailTemplateAcademicClassEnrolled, membership.CreatedAt),
		AuditEventID:              saveClassMemberAuditAttempt(t, ctx, ss, fixture.class.ID.String()).ID, AuditAt: model.GetMillis()})
	require.NoError(t, err)
	return ExamSittingInvitationLockOrderFixture{
		Acceptance: acceptance,
		Reconciliation: &store.ExamSittingMailReconciliation{SittingID: sitting.ID, ExpectedRevision: sitting.Revision,
			ActorUserID: fixture.actor.ID, Mail: newExamSittingMailFanout(t, fixture.actor.ID,
				store.ExamSittingMailReconciled, model.MailTemplateExamSittingScheduled)},
	}
}

func PrepareExamSittingDisabledEligibilityRaceFixture(t *testing.T, ss store.Store,
	key string,
) ExamSittingDisabledEligibilityRaceFixture {
	t.Helper()
	ctx := context.Background()
	fixture := newExamSittingFixture(t, ctx, ss)
	candidate, err := createUser(t, ctx, ss, newUser())
	require.NoError(t, err)
	_, err = ss.Affiliation().Save(ctx, &model.Affiliation{UserID: candidate.ID, Kind: model.AffiliationStudent,
		StartsAt: fixture.period.StartsAt})
	require.NoError(t, err)
	membership := &model.ClassMember{ClassID: fixture.class.ID, UserID: candidate.ID,
		AcademicPeriodID: fixture.class.AcademicPeriodID, StartsAt: fixture.period.StartsAt}
	membership.PrepareCreate(model.NewClassMemberID(), model.NowUTC())
	_, err = ss.ClassMember().Enroll(ctx, &store.ClassMemberEnrollment{Member: membership,
		ExpectedRecipientRevision: candidate.Revision,
		Notice:                    classMemberPreparedMail(t, membership, model.MailTemplateAcademicClassEnrolled, membership.CreatedAt),
		AuditEventID:              saveClassMemberAuditAttempt(t, ctx, ss, fixture.class.ID.String()).ID, AuditAt: model.GetMillis()})
	require.NoError(t, err)
	start := fixture.period.StartsAt.Add(18 * time.Hour)
	sitting, err := model.NewExamSitting(model.NewExamSittingID(), fixture.examID, fixture.revisionID,
		fixture.class.ID, start, start.Add(2*time.Hour), model.NowUTC())
	require.NoError(t, err)
	openJob, deadlineJob := newExamSittingLifecycleJobs(t, sitting.ID, 1, sitting.ScheduledStartAt, sitting.ScheduledEndAt)
	mail := newDisabledExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailScheduled,
		model.MailTemplateExamSittingScheduled)
	verificationAt := model.NowUTC()
	occurrence, delivery, job := userTokenMailFixture(t, candidate.ID, model.NewMailOccurrenceID(),
		model.MailOccurrenceSecurityNotice, model.MailTemplateIdentityEmailVerifiedByAdmin, model.JobTypeMailDeliver,
		verificationAt, verificationAt.Add(24*time.Hour))
	return ExamSittingDisabledEligibilityRaceFixture{
		SittingID: sitting.ID,
		Schedule: &store.ExamSittingSchedule{Sitting: sitting, OpenJob: openJob, DeadlineJob: deadlineJob,
			ActorUserID:  fixture.actor.ID,
			AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID,
			AuditAt:      model.GetMillis(), Mail: mail},
		Command: examCommand(fixture.actor.ID, "exam.sitting.schedule.v1", key, key+"-command"),
		Verification: &store.PrivilegedEmailVerification{UserID: candidate.ID, ExpectedRevision: candidate.Revision,
			Occurrence: occurrence, Delivery: delivery, Job: job,
			AuditEventID: saveUserProfileAuditAttempt(t, ctx, ss, candidate.ID.String()).ID,
			AuditAt:      model.MillisFromTime(verificationAt)},
	}
}

func TestExamSittingDisabledMailReconciliationConverges(t *testing.T, ss store.Store, probe ExamSittingDisabledMailSQLProbe) {
	require.NotNil(t, probe.AgeTerminalFanout, "disabled Sitting mail retention SQL probe is required")
	ctx := context.Background()
	fixture := newExamSittingFixture(t, ctx, ss)
	candidateInput := newUser()
	candidateInput.EmailVerified = true
	candidate, err := createUser(t, ctx, ss, candidateInput)
	require.NoError(t, err)
	_, err = ss.Affiliation().Save(ctx, &model.Affiliation{UserID: candidate.ID, Kind: model.AffiliationStudent,
		StartsAt: fixture.period.StartsAt})
	require.NoError(t, err)
	membership := &model.ClassMember{ClassID: fixture.class.ID, UserID: candidate.ID,
		AcademicPeriodID: fixture.class.AcademicPeriodID, StartsAt: fixture.period.StartsAt}
	membership.PrepareCreate(model.NewClassMemberID(), model.NowUTC())
	_, err = ss.ClassMember().Enroll(ctx, &store.ClassMemberEnrollment{Member: membership,
		ExpectedRecipientRevision: candidate.Revision,
		Notice:                    classMemberPreparedMail(t, membership, model.MailTemplateAcademicClassEnrolled, membership.CreatedAt),
		AuditEventID:              saveClassMemberAuditAttempt(t, ctx, ss, fixture.class.ID.String()).ID, AuditAt: model.GetMillis()})
	require.NoError(t, err)
	prepareCandidate := func(emailVerified bool) (*model.User, *model.ClassMember) {
		input := newUser()
		input.EmailVerified = emailVerified
		user, createErr := createUser(t, ctx, ss, input)
		require.NoError(t, createErr)
		_, createErr = ss.Affiliation().Save(ctx, &model.Affiliation{UserID: user.ID, Kind: model.AffiliationStudent,
			StartsAt: fixture.period.StartsAt})
		require.NoError(t, createErr)
		member := &model.ClassMember{ClassID: fixture.class.ID, UserID: user.ID,
			AcademicPeriodID: fixture.class.AcademicPeriodID, StartsAt: fixture.period.StartsAt}
		member.PrepareCreate(model.NewClassMemberID(), model.NowUTC())
		enrolled, createErr := ss.ClassMember().Enroll(ctx, &store.ClassMemberEnrollment{Member: member,
			ExpectedRecipientRevision: user.Revision,
			Notice:                    classMemberPreparedMail(t, member, model.MailTemplateAcademicClassEnrolled, member.CreatedAt),
			AuditEventID:              saveClassMemberAuditAttempt(t, ctx, ss, fixture.class.ID.String()).ID, AuditAt: model.GetMillis()})
		require.NoError(t, createErr)
		return user, enrolled.Membership
	}
	unverifiedCandidate, _ := prepareCandidate(false)
	disabledCandidate, _ := prepareCandidate(true)
	disableAt := model.GetMillis() + 1
	disabledResult, err := ss.User().SetDisabled(ctx, userDisabledStateChangeWithNotice(t, &store.UserDisabledStateChange{
		ID: disabledCandidate.ID, ExpectedRevision: disabledCandidate.Revision, Disabled: true,
		ChangedAt: disableAt, RevocationReason: model.SessionRevocationAccountDisabled, AuditEventID: saveUserProfileAuditAttempt(t, ctx, ss, disabledCandidate.ID.String()).ID, AuditAt: disableAt,
	}))
	require.NoError(t, err)

	start := fixture.period.StartsAt.Add(5 * time.Hour)
	sitting, err := model.NewExamSitting(model.NewExamSittingID(), fixture.examID, fixture.revisionID,
		fixture.class.ID, start, start.Add(2*time.Hour), model.NowUTC())
	require.NoError(t, err)
	openJob, deadlineJob := newExamSittingLifecycleJobs(t, sitting.ID, 1, sitting.ScheduledStartAt, sitting.ScheduledEndAt)
	disabledMail := newDisabledExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailScheduled,
		model.MailTemplateExamSittingScheduled)
	created, err := ss.ExamSitting().Schedule(ctx, &store.ExamSittingSchedule{Sitting: sitting,
		OpenJob: openJob, DeadlineJob: deadlineJob, ActorUserID: fixture.actor.ID,
		AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID,
		AuditAt:      model.GetMillis(), Mail: disabledMail},
		examCommand(fixture.actor.ID, "exam.sitting.schedule.v1", "sitting-disabled-mail", "sitting-disabled-mail-command"))
	require.NoError(t, err)
	terminal, err := ss.ExamSitting().GetMailFanout(ctx, disabledMail.Occurrence.ID)
	require.NoError(t, err)
	require.Nil(t, terminal.Bundle)
	require.True(t, terminal.CompletedAt.Valid)

	// Two nodes may observe the same installation after mail is re-enabled.
	// The Store is the authoritative terminal fence even if both bypass their
	// empty due page and race an enabled prepared fan-out directly.
	prepared := []*store.ExamSittingMailFanout{
		newExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailReconciled, model.MailTemplateExamSittingScheduled),
		newExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailReconciled, model.MailTemplateExamSittingScheduled),
	}
	errs := make([]error, len(prepared))
	var wait sync.WaitGroup
	for index := range prepared {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, errs[index] = ss.ExamSitting().ReconcileMail(ctx, &store.ExamSittingMailReconciliation{
				SittingID: created.Value.Sitting.ID, ExpectedRevision: created.Value.Sitting.Revision,
				ActorUserID: fixture.actor.ID, Mail: prepared[index],
			})
		}()
	}
	wait.Wait()
	for index, reconcileErr := range errs {
		require.True(t, store.IsConflict(reconcileErr), "node %d ReconcileMail(disabled revision) error", index)
		_, getErr := ss.ExamSitting().GetMailFanout(ctx, prepared[index].Occurrence.ID)
		require.True(t, store.IsNotFound(getErr), "node %d duplicate fan-out error", index)
		_, getErr = ss.Job().Get(ctx, prepared[index].ExpansionJob.ID)
		require.True(t, store.IsNotFound(getErr), "node %d duplicate expansion Job error", index)
	}
	for pass := 0; pass < 2; pass++ {
		due, listErr := ss.ExamSitting().ListMailReconciliationDue(ctx,
			store.ExamSittingMailReconciliationOptions{Limit: model.SittingMailExpansionPageSize})
		require.NoError(t, listErr)
		for _, item := range due {
			require.NotEqual(t, sitting.ID, item.Sitting.ID, "pass %d returned terminally suppressed Sitting as due: %#v", pass, due)
		}
	}

	// User mail eligibility is independent of Class membership chronology. A
	// verification or account-enable transition after the disabled watermark is
	// a new audience fact at the same Sitting revision, while the unchanged
	// candidate covered by the watermark remains converged.
	verificationAt := model.NowUTC()
	verificationOccurrence, verificationDelivery, verificationJob := userTokenMailFixture(t, unverifiedCandidate.ID,
		model.NewMailOccurrenceID(), model.MailOccurrenceSecurityNotice, model.MailTemplateIdentityEmailVerifiedByAdmin,
		model.JobTypeMailDeliver, verificationAt, verificationAt.Add(24*time.Hour))
	verifiedCandidate, err := ss.UserToken().VerifyEmailPrivileged(ctx, &store.PrivilegedEmailVerification{
		UserID: unverifiedCandidate.ID, ExpectedRevision: unverifiedCandidate.Revision,
		Occurrence: verificationOccurrence, Delivery: verificationDelivery, Job: verificationJob,
		AuditEventID: saveUserProfileAuditAttempt(t, ctx, ss, unverifiedCandidate.ID.String()).ID,
		AuditAt:      model.MillisFromTime(verificationAt),
	})
	require.NoError(t, err)
	enableAt := disableAt + 1
	enabledResult, err := ss.User().SetDisabled(ctx, userDisabledStateChangeWithNotice(t, &store.UserDisabledStateChange{
		ID: disabledCandidate.ID, ExpectedRevision: disabledResult.User.Revision, Disabled: false,
		ChangedAt: enableAt, AuditEventID: saveUserProfileAuditAttempt(t, ctx, ss, disabledCandidate.ID.String()).ID, AuditAt: enableAt,
	}))
	require.NoError(t, err)
	due, err := ss.ExamSitting().ListMailReconciliationDue(ctx,
		store.ExamSittingMailReconciliationOptions{Limit: model.SittingMailExpansionPageSize})
	require.NoError(t, err)
	requireSittingMailReconciliationDue(t, due, sitting.ID)
	eligibilityMail := newExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailReconciled,
		model.MailTemplateExamSittingScheduled)
	eligibilityFanout, err := ss.ExamSitting().ReconcileMail(ctx, &store.ExamSittingMailReconciliation{
		SittingID: sitting.ID, ExpectedRevision: sitting.Revision, ActorUserID: fixture.actor.ID, Mail: eligibilityMail,
	})
	require.NoError(t, err)
	eligibilityPage, err := ss.ExamSitting().ListMailRecipients(ctx, store.ExamSittingMailRecipientPageRequest{
		OccurrenceID: eligibilityFanout.Occurrence.ID, Limit: model.SittingMailExpansionPageSize})
	require.NoError(t, err)
	wantEligibility := map[model.UserID]model.MailTemplateKey{
		candidate.ID:          "",
		verifiedCandidate.ID:  model.MailTemplateExamSittingScheduled,
		enabledResult.User.ID: model.MailTemplateExamSittingScheduled,
	}
	for _, recipient := range eligibilityPage.Recipients {
		want, ok := wantEligibility[recipient.User.ID]
		if ok {
			require.Equal(t, want, recipient.TemplateKey, "eligibility recipient %s template", recipient.User.ID)
		}
	}
	for userID, want := range wantEligibility {
		if want == "" {
			continue
		}
		var found bool
		for _, recipient := range eligibilityPage.Recipients {
			found = found || recipient.User.ID == userID
		}
		require.True(t, found, "eligible recipient %s missing from page: %#v", userID, eligibilityPage.Recipients)
	}
	for _, recipient := range eligibilityPage.Recipients {
		if recipient.TemplateKey == "" {
			continue
		}
		delivery, job := newExamSittingMailDelivery(t, eligibilityFanout, recipient.User, recipient.TemplateKey)
		_, err = ss.ExamSitting().CommitMailRecipient(ctx, &store.ExamSittingMailRecipientCommit{
			OccurrenceID: eligibilityFanout.Occurrence.ID, SittingRevision: eligibilityFanout.SittingRevision,
			Recipient: recipient.User, Delivery: delivery, DeliveryJob: job})
		require.NoError(t, err)
		sending, startErr := ss.Mail().StartDelivery(ctx, delivery.ID, delivery.Revision, model.NowUTC())
		require.NoError(t, startErr)
		_, err = ss.Mail().CompleteDelivery(ctx, &store.MailDeliveryCompletion{DeliveryID: delivery.ID,
			ExpectedRevision: sending.Revision, Kind: store.MailDeliveryCompletionAccepted, At: model.NowUTC()})
		require.NoError(t, err)
	}
	_, err = ss.ExamSitting().CompleteMailExpansion(ctx,
		&store.ExamSittingMailExpansionCompletion{OccurrenceID: eligibilityFanout.Occurrence.ID})
	require.NoError(t, err)

	// Mail history observes the ordinary 90-day cutoff. Its removal must not
	// erase the durable decision that this Sitting revision was terminally
	// suppressed while mail was disabled.
	probe.AgeTerminalFanout(t, ctx, disabledMail.Occurrence.ID)
	cleaned, err := ss.Mail().CleanupTerminal(ctx, 10)
	require.NoError(t, err)
	require.Equal(t, 1, cleaned.Affected)
	_, getErr := ss.ExamSitting().GetMailFanout(ctx, disabledMail.Occurrence.ID)
	require.True(t, store.IsNotFound(getErr), "retired disabled fan-out error")
	afterRetention := newExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailReconciled,
		model.MailTemplateExamSittingScheduled)
	_, reconcileErr := ss.ExamSitting().ReconcileMail(ctx, &store.ExamSittingMailReconciliation{
		SittingID: created.Value.Sitting.ID, ExpectedRevision: created.Value.Sitting.Revision,
		ActorUserID: fixture.actor.ID, Mail: afterRetention,
	})
	require.True(t, store.IsConflict(reconcileErr), "ReconcileMail(after disabled history retention) error")
	due, err = ss.ExamSitting().ListMailReconciliationDue(ctx,
		store.ExamSittingMailReconciliationOptions{Limit: model.SittingMailExpansionPageSize})
	require.NoError(t, err)
	for _, item := range due {
		require.NotEqual(t, sitting.ID, item.Sitting.ID, "retired disabled history resurrected Sitting reconciliation: %#v", due)
	}

	// A later membership mutation is a new audience fact even though the
	// Sitting revision is unchanged. Re-enablement must notify only that new
	// audience, without resurrecting candidates covered by the disabled fact.
	newCandidateInput := newUser()
	newCandidateInput.EmailVerified = true
	newCandidate, err := createUser(t, ctx, ss, newCandidateInput)
	require.NoError(t, err)
	_, err = ss.Affiliation().Save(ctx, &model.Affiliation{UserID: newCandidate.ID, Kind: model.AffiliationStudent,
		StartsAt: fixture.period.StartsAt})
	require.NoError(t, err)
	newMembership := &model.ClassMember{ClassID: fixture.class.ID, UserID: newCandidate.ID,
		AcademicPeriodID: fixture.class.AcademicPeriodID, StartsAt: fixture.period.StartsAt}
	newMembership.PrepareCreate(model.NewClassMemberID(), model.NowUTC())
	enrolled, err := ss.ClassMember().Enroll(ctx, &store.ClassMemberEnrollment{Member: newMembership,
		ExpectedRecipientRevision: newCandidate.Revision,
		Notice:                    classMemberPreparedMail(t, newMembership, model.MailTemplateAcademicClassEnrolled, newMembership.CreatedAt),
		AuditEventID:              saveClassMemberAuditAttempt(t, ctx, ss, fixture.class.ID.String()).ID, AuditAt: model.GetMillis()})
	require.NoError(t, err)
	due, err = ss.ExamSitting().ListMailReconciliationDue(ctx,
		store.ExamSittingMailReconciliationOptions{Limit: model.SittingMailExpansionPageSize})
	require.NoError(t, err)
	requireSittingMailReconciliationDue(t, due, sitting.ID)

	enrollmentMail := newExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailReconciled,
		model.MailTemplateExamSittingScheduled)
	enrollmentFanout, err := ss.ExamSitting().ReconcileMail(ctx, &store.ExamSittingMailReconciliation{
		SittingID: sitting.ID, ExpectedRevision: sitting.Revision, ActorUserID: fixture.actor.ID, Mail: enrollmentMail,
	})
	require.NoError(t, err)
	page, err := ss.ExamSitting().ListMailRecipients(ctx, store.ExamSittingMailRecipientPageRequest{
		OccurrenceID: enrollmentFanout.Occurrence.ID, Limit: model.SittingMailExpansionPageSize})
	require.NoError(t, err)
	var newRecipient *store.ExamSittingMailRecipient
	for index := range page.Recipients {
		recipient := &page.Recipients[index]
		switch recipient.User.ID {
		case candidate.ID:
			require.Empty(t, recipient.TemplateKey)
		case newCandidate.ID:
			require.Equal(t, model.MailTemplateExamSittingScheduled, recipient.TemplateKey)
			newRecipient = recipient
		}
	}
	require.NotNil(t, newRecipient, "new candidate missing from reconciliation page: %#v", page.Recipients)
	delivery, deliveryJob := newExamSittingMailDelivery(t, enrollmentFanout, newRecipient.User, newRecipient.TemplateKey)
	_, err = ss.ExamSitting().CommitMailRecipient(ctx, &store.ExamSittingMailRecipientCommit{
		OccurrenceID: enrollmentFanout.Occurrence.ID, SittingRevision: enrollmentFanout.SittingRevision,
		Recipient: newRecipient.User, Delivery: delivery, DeliveryJob: deliveryJob})
	require.NoError(t, err)
	sending, err := ss.Mail().StartDelivery(ctx, delivery.ID, delivery.Revision, model.NowUTC())
	require.NoError(t, err)
	_, err = ss.Mail().CompleteDelivery(ctx, &store.MailDeliveryCompletion{DeliveryID: sending.ID,
		ExpectedRevision: sending.Revision, Kind: store.MailDeliveryCompletionAccepted, At: model.NowUTC()})
	require.NoError(t, err)
	_, err = ss.ExamSitting().CompleteMailExpansion(ctx,
		&store.ExamSittingMailExpansionCompletion{OccurrenceID: enrollmentFanout.Occurrence.ID})
	require.NoError(t, err)

	// Ending that same membership is another audience fact without a Sitting
	// revision change and must yield the assignment-removed projection.
	_, err = ss.ClassMember().End(ctx, &store.ClassMemberEnd{ID: enrolled.Membership.ID,
		ExpectedRevision: enrolled.Membership.Revision, ExpectedRecipientRevision: newCandidate.Revision,
		EndAt:        model.MillisFromTime(start.Add(-time.Hour)),
		Notice:       classMemberPreparedMail(t, enrolled.Membership, model.MailTemplateAcademicClassEnrollmentEnded, start.Add(-time.Hour)),
		AuditEventID: saveClassMemberAuditAttempt(t, ctx, ss, fixture.class.ID.String()).ID, AuditAt: model.GetMillis()})
	require.NoError(t, err)
	due, err = ss.ExamSitting().ListMailReconciliationDue(ctx,
		store.ExamSittingMailReconciliationOptions{Limit: model.SittingMailExpansionPageSize})
	require.NoError(t, err)
	requireSittingMailReconciliationDue(t, due, sitting.ID)
	removalMail := newExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailReconciled,
		model.MailTemplateExamSittingScheduled)
	removalFanout, err := ss.ExamSitting().ReconcileMail(ctx, &store.ExamSittingMailReconciliation{
		SittingID: sitting.ID, ExpectedRevision: sitting.Revision, ActorUserID: fixture.actor.ID, Mail: removalMail,
	})
	require.NoError(t, err)
	page, err = ss.ExamSitting().ListMailRecipients(ctx, store.ExamSittingMailRecipientPageRequest{
		OccurrenceID: removalFanout.Occurrence.ID, Limit: model.SittingMailExpansionPageSize})
	require.NoError(t, err)
	var removedRecipient *store.ExamSittingMailRecipient
	for index := range page.Recipients {
		recipient := &page.Recipients[index]
		if recipient.User.ID == newCandidate.ID {
			require.Equal(t, model.MailTemplateExamSittingAssignmentRemoved, recipient.TemplateKey)
		}
		if recipient.User.ID == newCandidate.ID {
			removedRecipient = recipient
		}
		if recipient.User.ID == candidate.ID {
			require.Empty(t, recipient.TemplateKey)
		}
	}
	require.NotNil(t, removedRecipient, "ended candidate missing from reconciliation page: %#v", page.Recipients)
	removedDelivery, removedJob := newExamSittingMailDelivery(t, removalFanout, removedRecipient.User, removedRecipient.TemplateKey)
	_, err = ss.ExamSitting().CommitMailRecipient(ctx, &store.ExamSittingMailRecipientCommit{
		OccurrenceID: removalFanout.Occurrence.ID, SittingRevision: removalFanout.SittingRevision,
		Recipient: removedRecipient.User, Delivery: removedDelivery, DeliveryJob: removedJob})
	require.NoError(t, err)
	startedRemoval, err := ss.Mail().StartDelivery(ctx, removedDelivery.ID, removedDelivery.Revision, model.NowUTC())
	require.NoError(t, err)
	require.Equal(t, model.MailDeliverySending, startedRemoval.State)
}

// TestExamSittingClassTransferMailReconciliation proves that one future-dated
// transfer advances both affected Class audiences. The source Sitting emits an
// assignment removal while the destination Sitting emits a new schedule, and
// replay cannot create another reconciliation occurrence.
func TestExamSittingClassTransferMailReconciliation(t *testing.T, ss store.Store) {
	t.Helper()
	ctx := context.Background()
	fixture := newExamSittingFixture(t, ctx, ss)
	destinationClass := saveClass(t, ctx, ss, fixture.levelID.String(), fixture.period.ID.String(), "sitting-transfer-destination")
	start, end := fixture.period.StartsAt.Add(12*time.Hour), fixture.period.StartsAt.Add(14*time.Hour)

	candidateInput := newUser()
	candidateInput.EmailVerified = true
	candidate, err := createUser(t, ctx, ss, candidateInput)
	require.NoError(t, err)
	_, err = ss.Affiliation().Save(ctx, &model.Affiliation{UserID: candidate.ID, Kind: model.AffiliationStudent,
		StartsAt: fixture.period.StartsAt})
	require.NoError(t, err)
	sourceMember := &model.ClassMember{ClassID: fixture.class.ID, UserID: candidate.ID,
		AcademicPeriodID: fixture.period.ID, StartsAt: fixture.period.StartsAt}
	sourceMember.PrepareCreate(model.NewClassMemberID(), model.NowUTC())
	sourceEnrollment, err := ss.ClassMember().Enroll(ctx, &store.ClassMemberEnrollment{
		Member: sourceMember, ExpectedRecipientRevision: candidate.Revision,
		Notice:       classMemberPreparedMail(t, sourceMember, model.MailTemplateAcademicClassEnrolled, sourceMember.CreatedAt),
		AuditEventID: saveClassMemberAuditAttempt(t, ctx, ss, fixture.class.ID.String()).ID, AuditAt: model.GetMillis(),
	})
	require.NoError(t, err)

	schedule := func(class *model.Class, suffix string) (*model.ExamSitting, *store.ExamSittingMailFanoutSnapshot) {
		t.Helper()
		sitting, sittingErr := model.NewExamSitting(model.NewExamSittingID(), fixture.examID, fixture.revisionID, class.ID,
			start, end, model.NowUTC())
		require.NoError(t, sittingErr)
		openJob, deadlineJob := newExamSittingLifecycleJobs(t, sitting.ID, 1, start, end)
		fanout := newExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailScheduled, model.MailTemplateExamSittingScheduled)
		created, scheduleErr := ss.ExamSitting().Schedule(ctx, &store.ExamSittingSchedule{Sitting: sitting,
			OpenJob: openJob, DeadlineJob: deadlineJob, ActorUserID: fixture.actor.ID,
			AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID,
			AuditAt:      model.GetMillis(), Mail: fanout},
			examCommand(fixture.actor.ID, "exam.sitting.schedule.v1", "sitting-transfer-"+suffix, "sitting-transfer-command-"+suffix))
		require.NoError(t, scheduleErr)
		snapshot, getErr := ss.ExamSitting().GetMailFanout(ctx, fanout.Occurrence.ID)
		require.NoError(t, getErr)
		return created.Value.Sitting, snapshot
	}
	sourceSitting, sourceFanout := schedule(fixture.class, "source")
	destinationSitting, destinationFanout := schedule(destinationClass, "destination")

	sourcePage, err := ss.ExamSitting().ListMailRecipients(ctx, store.ExamSittingMailRecipientPageRequest{
		OccurrenceID: sourceFanout.Occurrence.ID, Limit: model.SittingMailExpansionPageSize})
	require.NoError(t, err)
	require.Len(t, sourcePage.Recipients, 1)
	require.Equal(t, candidate.ID, sourcePage.Recipients[0].User.ID)
	require.Equal(t, model.MailTemplateExamSittingScheduled, sourcePage.Recipients[0].TemplateKey)
	delivery, deliveryJob := newExamSittingMailDelivery(t, sourceFanout, candidate, sourcePage.Recipients[0].TemplateKey)
	_, err = ss.ExamSitting().CommitMailRecipient(ctx, &store.ExamSittingMailRecipientCommit{
		OccurrenceID: sourceFanout.Occurrence.ID, SittingRevision: 1, Recipient: candidate,
		Delivery: delivery, DeliveryJob: deliveryJob})
	require.NoError(t, err)
	sending, err := ss.Mail().StartDelivery(ctx, delivery.ID, delivery.Revision, model.NowUTC())
	require.NoError(t, err)
	_, err = ss.Mail().CompleteDelivery(ctx, &store.MailDeliveryCompletion{DeliveryID: delivery.ID,
		ExpectedRevision: sending.Revision, Kind: store.MailDeliveryCompletionAccepted, At: model.NowUTC()})
	require.NoError(t, err)
	for _, occurrenceID := range []model.MailOccurrenceID{sourceFanout.Occurrence.ID, destinationFanout.Occurrence.ID} {
		_, err = ss.ExamSitting().CompleteMailExpansion(ctx, &store.ExamSittingMailExpansionCompletion{OccurrenceID: occurrenceID})
		require.NoError(t, err)
	}

	transferAt := start.Add(-time.Hour)
	destinationMember := &model.ClassMember{ClassID: destinationClass.ID, UserID: candidate.ID,
		AcademicPeriodID: fixture.period.ID, StartsAt: transferAt}
	destinationMember.PrepareCreate(model.NewClassMemberID(), model.NowUTC())
	destinationAudit := saveClassMemberAuditAttempt(t, ctx, ss, destinationClass.ID.String())
	sourceAudit := saveClassMemberAuditAttempt(t, ctx, ss, fixture.class.ID.String())
	transferNotice := classMemberPreparedMail(t, destinationMember, model.MailTemplateAcademicClassTransferred, destinationMember.CreatedAt)
	transferred, err := ss.ClassMember().Enroll(ctx, &store.ClassMemberEnrollment{
		Member: destinationMember, ExpectedPreviousID: sourceEnrollment.Membership.ID,
		ExpectedRecipientRevision: candidate.Revision, Notice: transferNotice,
		AuditEventID: destinationAudit.ID, PreviousAuditEventID: sourceAudit.ID, AuditAt: model.GetMillis(),
	})
	require.NoError(t, err)
	require.NotNil(t, transferred.Previous)
	require.Equal(t, sourceEnrollment.Membership.ID, transferred.Previous.ID)
	require.Equal(t, transferAt, transferred.Previous.EndsAt.Time)
	require.NoError(t, requireClassMemberMail(t, ctx, ss, transferNotice, model.MailTemplateAcademicClassTransferred))

	due, err := ss.ExamSitting().ListMailReconciliationDue(ctx,
		store.ExamSittingMailReconciliationOptions{Limit: model.SittingMailExpansionPageSize})
	require.NoError(t, err)
	requireSittingMailReconciliationDue(t, due, sourceSitting.ID)
	requireSittingMailReconciliationDue(t, due, destinationSitting.ID)

	reconcile := func(sitting *model.ExamSitting, want model.MailTemplateKey, suffix string) model.MailOccurrenceID {
		t.Helper()
		mail := newExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailReconciled, model.MailTemplateExamSittingScheduled)
		fanout, reconcileErr := ss.ExamSitting().ReconcileMail(ctx, &store.ExamSittingMailReconciliation{
			SittingID: sitting.ID, ExpectedRevision: sitting.Revision, ActorUserID: fixture.actor.ID, Mail: mail})
		require.NoError(t, reconcileErr)
		page, pageErr := ss.ExamSitting().ListMailRecipients(ctx, store.ExamSittingMailRecipientPageRequest{
			OccurrenceID: fanout.Occurrence.ID, Limit: model.SittingMailExpansionPageSize})
		require.NoError(t, pageErr)
		require.Len(t, page.Recipients, 1, "%s transfer reconciliation recipients", suffix)
		require.Equal(t, candidate.ID, page.Recipients[0].User.ID, "%s transfer reconciliation recipients", suffix)
		require.Equal(t, want, page.Recipients[0].TemplateKey, "%s transfer reconciliation recipients", suffix)
		delivery, deliveryJob := newExamSittingMailDelivery(t, fanout, page.Recipients[0].User, page.Recipients[0].TemplateKey)
		_, commitErr := ss.ExamSitting().CommitMailRecipient(ctx, &store.ExamSittingMailRecipientCommit{
			OccurrenceID: fanout.Occurrence.ID, SittingRevision: fanout.SittingRevision,
			Recipient: page.Recipients[0].User, Delivery: delivery, DeliveryJob: deliveryJob})
		require.NoError(t, commitErr)
		sending, startErr := ss.Mail().StartDelivery(ctx, delivery.ID, delivery.Revision, model.NowUTC())
		require.NoError(t, startErr)
		_, acceptErr := ss.Mail().CompleteDelivery(ctx, &store.MailDeliveryCompletion{DeliveryID: delivery.ID,
			ExpectedRevision: sending.Revision, Kind: store.MailDeliveryCompletionAccepted, At: model.NowUTC()})
		require.NoError(t, acceptErr)
		_, completeErr := ss.ExamSitting().CompleteMailExpansion(ctx,
			&store.ExamSittingMailExpansionCompletion{OccurrenceID: fanout.Occurrence.ID})
		require.NoError(t, completeErr)
		return fanout.Occurrence.ID
	}
	reconcile(sourceSitting, model.MailTemplateExamSittingAssignmentRemoved, "source")
	reconcile(destinationSitting, model.MailTemplateExamSittingScheduled, "destination")

	replayMail := newExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailReconciled, model.MailTemplateExamSittingScheduled)
	_, err = ss.ExamSitting().ReconcileMail(ctx, &store.ExamSittingMailReconciliation{
		SittingID: sourceSitting.ID, ExpectedRevision: sourceSitting.Revision, ActorUserID: fixture.actor.ID,
		Mail: replayMail,
	})
	require.True(t, store.IsConflict(err), "source reconciliation replay error")
}

func requireSittingMailReconciliationDue(t *testing.T, candidates []store.ExamSittingMailReconciliationCandidate,
	sittingID model.ExamSittingID,
) {
	t.Helper()
	for _, candidate := range candidates {
		if candidate.Sitting != nil && candidate.Sitting.ID == sittingID {
			return
		}
	}
	require.FailNow(t, fmt.Sprintf("Sitting %s is not due for mail reconciliation: %#v", sittingID, candidates))
}

// TestExamSittingMailExpansionMaintenance proves that a permanently failed
// expansion releases reconciliation without retaining its shared ciphertext
// or leaving already-created recipient work runnable.
func TestExamSittingMailExpansionMaintenance(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture := newExamSittingFixture(t, ctx, ss)
	start := fixture.period.StartsAt.Add(6 * time.Hour)
	end := start.Add(2 * time.Hour)
	sitting, err := model.NewExamSitting(model.NewExamSittingID(), fixture.examID, fixture.revisionID,
		fixture.class.ID, start, end, model.NowUTC())
	require.NoError(t, err)
	openJob, deadlineJob := newExamSittingLifecycleJobs(t, sitting.ID, 1, start, end)
	prepared := newExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailScheduled,
		model.MailTemplateExamSittingScheduled)
	created, err := ss.ExamSitting().Schedule(ctx, &store.ExamSittingSchedule{Sitting: sitting,
		OpenJob: openJob, DeadlineJob: deadlineJob, ActorUserID: fixture.actor.ID,
		AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID,
		AuditAt:      model.GetMillis(), Mail: prepared},
		examCommand(fixture.actor.ID, "exam.sitting.schedule.v1", "sitting-mail-maintenance", "sitting-mail-maintenance-command"))
	require.NoError(t, err)

	candidateInput := newUser()
	candidateInput.EmailVerified = true
	candidate, err := createUser(t, ctx, ss, candidateInput)
	require.NoError(t, err)
	_, err = ss.Affiliation().Save(ctx, &model.Affiliation{UserID: candidate.ID, Kind: model.AffiliationStudent,
		StartsAt: fixture.period.StartsAt})
	require.NoError(t, err)
	membership := &model.ClassMember{ClassID: fixture.class.ID, UserID: candidate.ID,
		AcademicPeriodID: fixture.class.AcademicPeriodID, StartsAt: fixture.period.StartsAt}
	membership.PrepareCreate(model.NewClassMemberID(), model.NowUTC())
	_, err = ss.ClassMember().Enroll(ctx, &store.ClassMemberEnrollment{Member: membership,
		ExpectedRecipientRevision: candidate.Revision,
		Notice:                    classMemberPreparedMail(t, membership, model.MailTemplateAcademicClassEnrolled, membership.CreatedAt),
		AuditEventID:              saveClassMemberAuditAttempt(t, ctx, ss, fixture.class.ID.String()).ID, AuditAt: model.GetMillis()})
	require.NoError(t, err)

	fanout, err := ss.ExamSitting().GetMailFanout(ctx, prepared.Occurrence.ID)
	require.NoError(t, err)
	page, err := ss.ExamSitting().ListMailRecipients(ctx, store.ExamSittingMailRecipientPageRequest{
		OccurrenceID: fanout.Occurrence.ID, Limit: model.SittingMailExpansionPageSize})
	require.NoError(t, err)
	delivery, deliveryJob := newExamSittingMailDelivery(t, fanout, candidate, page.Recipients[0].TemplateKey)
	_, err = ss.ExamSitting().CommitMailRecipient(ctx, &store.ExamSittingMailRecipientCommit{
		OccurrenceID: fanout.Occurrence.ID, SittingRevision: fanout.SittingRevision, Recipient: candidate,
		Delivery: delivery, DeliveryJob: deliveryJob})
	require.NoError(t, err)

	token, err := model.NewJobClaimToken()
	require.NoError(t, err)
	claim, err := ss.Job().ClaimNext(ctx, &store.JobClaimRequest{Types: []model.JobType{model.JobTypeMailExpandSitting},
		NodeID: "sitting-mail-maintenance", ClaimToken: token, LeaseDuration: time.Minute})
	require.NoError(t, err)
	require.Equal(t, prepared.ExpansionJob.ID, claim.Job.ID, "claimed expansion Job")
	failed, err := ss.Job().Complete(ctx, &store.JobCompletion{AttemptID: claim.Attempt.ID,
		ClaimToken: token, Kind: store.JobCompletionPermanentFailure, PublicErrorCode: "mail.sitting.unavailable"})
	require.NoError(t, err)
	require.Equal(t, model.JobStatusFailed, failed.Status)

	first, err := ss.ExamSitting().MaintainMailExpansions(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, 1, first.FanoutsTerminalized)
	require.Equal(t, 0, first.DeliveriesSuppressed)
	require.True(t, first.More)
	terminal, err := ss.ExamSitting().GetMailFanout(ctx, fanout.Occurrence.ID)
	require.NoError(t, err)
	require.True(t, terminal.CompletedAt.Valid)
	require.Nil(t, terminal.Bundle)
	second, err := ss.ExamSitting().MaintainMailExpansions(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, 0, second.FanoutsTerminalized)
	require.Equal(t, 1, second.DeliveriesSuppressed)
	require.False(t, second.More)
	suppressed, err := ss.Mail().GetDelivery(ctx, delivery.ID)
	require.NoError(t, err)
	require.Equal(t, model.MailDeliverySuppressed, suppressed.State)
	require.Equal(t, model.MailDeliveryObsoleteCode, suppressed.PublicFailureCode)
	require.Empty(t, suppressed.EncryptedPayload)
	canceledJob, err := ss.Job().Get(ctx, deliveryJob.ID)
	require.NoError(t, err)
	require.Equal(t, model.JobStatusCanceled, canceledJob.Status)
	due, err := ss.ExamSitting().ListMailReconciliationDue(ctx,
		store.ExamSittingMailReconciliationOptions{Limit: model.SittingMailExpansionPageSize})
	require.NoError(t, err)
	for _, item := range due {
		if item.Sitting.ID == created.Value.Sitting.ID {
			return
		}
	}
	require.FailNow(t, fmt.Sprintf("terminal expansion did not release reconciliation: %#v", due))
}

type ExamSittingMailRetentionSQLProbe struct {
	AgeDeliveries func(*testing.T, context.Context, model.MailDeliveryID, model.MailDeliveryID, model.MailDeliveryID)
	AssertRetired func(*testing.T, context.Context, model.ExamSittingID, model.MailOccurrenceID, []model.UserID)
}

type ExamSittingMailRecoveryFixture struct {
	ExpiredOccurrence model.MailOccurrenceID
	ExpiredJob        model.JobID
	OrphanOccurrence  model.MailOccurrenceID
	OrphanJob         model.JobID
}

// PrepareExamSittingMailRecoveryFixture creates two active, recipient-free
// expansions through the public aggregate. SQL tests may then move only their
// durable Job/deadline facts to exercise otherwise unreachable recovery paths.
func PrepareExamSittingMailRecoveryFixture(t *testing.T, ss store.Store) ExamSittingMailRecoveryFixture {
	t.Helper()
	ctx := context.Background()
	fixture := newExamSittingFixture(t, ctx, ss)
	create := func(key string, offset time.Duration) (*store.ExamSittingMailFanout, *store.ExamSittingCommandResult) {
		start := fixture.period.StartsAt.Add(offset)
		sitting, err := model.NewExamSitting(model.NewExamSittingID(), fixture.examID, fixture.revisionID,
			fixture.class.ID, start, start.Add(time.Hour), model.NowUTC())
		require.NoError(t, err)
		openJob, deadlineJob := newExamSittingLifecycleJobs(t, sitting.ID, 1, sitting.ScheduledStartAt, sitting.ScheduledEndAt)
		mail := newExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailScheduled,
			model.MailTemplateExamSittingScheduled)
		result, err := ss.ExamSitting().Schedule(ctx, &store.ExamSittingSchedule{Sitting: sitting,
			OpenJob: openJob, DeadlineJob: deadlineJob, ActorUserID: fixture.actor.ID,
			AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID,
			AuditAt:      model.GetMillis(), Mail: mail},
			examCommand(fixture.actor.ID, "exam.sitting.schedule.v1", key, key+"-command"))
		require.NoError(t, err)
		return mail, result
	}
	expired, _ := create("sitting-mail-expired-recovery", 10*time.Hour)
	orphan, _ := create("sitting-mail-orphan-recovery", 12*time.Hour)
	return ExamSittingMailRecoveryFixture{ExpiredOccurrence: expired.Occurrence.ID, ExpiredJob: expired.ExpansionJob.ID,
		OrphanOccurrence: orphan.Occurrence.ID, OrphanJob: orphan.ExpansionJob.ID}
}

// TestExamSittingMailRetentionCleanup proves retention through the public Mail
// Store while the SQL probe only moves immutable terminal timestamps beyond
// their documented cutoffs and observes the FK/projection result.
func TestExamSittingMailRetentionCleanup(t *testing.T, ss store.Store, probe ExamSittingMailRetentionSQLProbe) {
	require.NotNil(t, probe.AgeDeliveries, "complete Sitting mail retention SQL probe is required")
	require.NotNil(t, probe.AssertRetired, "complete Sitting mail retention SQL probe is required")
	ctx := context.Background()
	fixture := newExamSittingFixture(t, ctx, ss)
	start := fixture.period.StartsAt.Add(8 * time.Hour)
	end := start.Add(2 * time.Hour)
	sitting, err := model.NewExamSitting(model.NewExamSittingID(), fixture.examID, fixture.revisionID,
		fixture.class.ID, start, end, model.NowUTC())
	require.NoError(t, err)
	openJob, deadlineJob := newExamSittingLifecycleJobs(t, sitting.ID, 1, start, end)
	prepared := newExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailScheduled,
		model.MailTemplateExamSittingScheduled)
	_, err = ss.ExamSitting().Schedule(ctx, &store.ExamSittingSchedule{Sitting: sitting,
		OpenJob: openJob, DeadlineJob: deadlineJob, ActorUserID: fixture.actor.ID,
		AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID,
		AuditAt:      model.GetMillis(), Mail: prepared},
		examCommand(fixture.actor.ID, "exam.sitting.schedule.v1", "sitting-mail-retention", "sitting-mail-retention-command"))
	require.NoError(t, err)

	users := make([]*model.User, 3)
	for index := range users {
		input := newUser()
		input.EmailVerified = true
		users[index], err = createUser(t, ctx, ss, input)
		require.NoError(t, err)
		_, err = ss.Affiliation().Save(ctx, &model.Affiliation{UserID: users[index].ID,
			Kind: model.AffiliationStudent, StartsAt: fixture.period.StartsAt})
		require.NoError(t, err)
		membership := &model.ClassMember{ClassID: fixture.class.ID, UserID: users[index].ID,
			AcademicPeriodID: fixture.class.AcademicPeriodID, StartsAt: fixture.period.StartsAt}
		membership.PrepareCreate(model.NewClassMemberID(), model.NowUTC())
		_, err = ss.ClassMember().Enroll(ctx, &store.ClassMemberEnrollment{Member: membership,
			ExpectedRecipientRevision: users[index].Revision,
			Notice:                    classMemberPreparedMail(t, membership, model.MailTemplateAcademicClassEnrolled, membership.CreatedAt),
			AuditEventID:              saveClassMemberAuditAttempt(t, ctx, ss, fixture.class.ID.String()).ID, AuditAt: model.GetMillis()})
		require.NoError(t, err)
	}
	fanout, err := ss.ExamSitting().GetMailFanout(ctx, prepared.Occurrence.ID)
	require.NoError(t, err)
	page, err := ss.ExamSitting().ListMailRecipients(ctx, store.ExamSittingMailRecipientPageRequest{
		OccurrenceID: fanout.Occurrence.ID, Limit: model.SittingMailExpansionPageSize})
	require.NoError(t, err)
	require.Len(t, page.Recipients, len(users))
	deliveries := make(map[model.UserID]*model.MailDelivery, len(users))
	for _, recipient := range page.Recipients {
		delivery, job := newExamSittingMailDelivery(t, fanout, recipient.User, recipient.TemplateKey)
		_, err = ss.ExamSitting().CommitMailRecipient(ctx, &store.ExamSittingMailRecipientCommit{
			OccurrenceID: fanout.Occurrence.ID, SittingRevision: fanout.SittingRevision, Recipient: recipient.User,
			Delivery: delivery, DeliveryJob: job})
		require.NoError(t, err)
		sending, startErr := ss.Mail().StartDelivery(ctx, delivery.ID, delivery.Revision, model.NowUTC())
		require.NoError(t, startErr)
		deliveries[recipient.User.ID] = sending
	}
	accepted, err := ss.Mail().CompleteDelivery(ctx, &store.MailDeliveryCompletion{DeliveryID: deliveries[users[0].ID].ID,
		ExpectedRevision: deliveries[users[0].ID].Revision, Kind: store.MailDeliveryCompletionAccepted, At: model.NowUTC()})
	require.NoError(t, err)
	suppressed, err := ss.Mail().CompleteDelivery(ctx, &store.MailDeliveryCompletion{DeliveryID: deliveries[users[1].ID].ID,
		ExpectedRevision: deliveries[users[1].ID].Revision, Kind: store.MailDeliveryCompletionSuppress,
		PublicFailureCode: model.MailDeliveryObsoleteCode, At: model.NowUTC()})
	require.NoError(t, err)
	failed, err := ss.Mail().CompleteDelivery(ctx, &store.MailDeliveryCompletion{DeliveryID: deliveries[users[2].ID].ID,
		ExpectedRevision: deliveries[users[2].ID].Revision, Kind: store.MailDeliveryCompletionFailed,
		PublicFailureCode: "mail.transport.permanent", At: model.NowUTC()})
	require.NoError(t, err)
	_, err = ss.ExamSitting().CompleteMailExpansion(ctx,
		&store.ExamSittingMailExpansionCompletion{OccurrenceID: fanout.Occurrence.ID})
	require.NoError(t, err)

	probe.AgeDeliveries(t, ctx, accepted.ID, suppressed.ID, failed.ID)
	cleaned, err := ss.Mail().CleanupTerminal(ctx, 3)
	require.NoError(t, err)
	require.Equal(t, 3, cleaned.Affected)
	require.False(t, cleaned.More)
	for _, delivery := range []*model.MailDelivery{accepted, suppressed, failed} {
		_, getErr := ss.Mail().GetDelivery(ctx, delivery.ID)
		require.True(t, store.IsNotFound(getErr), "retained Sitting delivery %s error", delivery.ID)
	}
	ids := []model.UserID{users[0].ID, users[1].ID, users[2].ID}
	probe.AssertRetired(t, ctx, sitting.ID, fanout.Occurrence.ID, ids)
}

// ExamSittingSQLProbe exposes only the corruption/race setup that cannot be
// reached through the public Store because normal lifecycle guards correctly
// prevent archiving an ancestor with a live Class.
type ExamSittingSQLProbe struct {
	ArchiveProgrammeLevel func(*testing.T, context.Context, model.ProgrammeLevelID)
}

type ExamSittingPrivateActionProbe struct {
	ActionCode    string
	PrivateReason string
	Revision      int64
}

type ExamSittingLifecycleSQLProbe struct {
	SetSchedule      func(*testing.T, context.Context, model.ExamSittingID, time.Time, time.Time)
	PrivateActions   func(*testing.T, context.Context, model.ExamSittingID) []ExamSittingPrivateActionProbe
	AssertAppendOnly func(*testing.T, context.Context, model.ExamSittingID)
}

func TestExamSittingLifecycleStore(t *testing.T, ss store.Store, probe ExamSittingLifecycleSQLProbe) {
	ctx := context.Background()
	fixture := newExamSittingFixture(t, ctx, ss)
	require.NotNil(t, probe.SetSchedule, "complete Exam Sitting lifecycle SQL probe is required")
	require.NotNil(t, probe.PrivateActions, "complete Exam Sitting lifecycle SQL probe is required")
	require.NotNil(t, probe.AssertAppendOnly, "complete Exam Sitting lifecycle SQL probe is required")

	schedule := func(key string, classID model.ClassID, startAt, endAt time.Time) *store.ExamSittingCommandResult {
		t.Helper()
		sitting, err := model.NewExamSitting(model.NewExamSittingID(), fixture.examID, fixture.revisionID, classID, startAt, endAt, model.NowUTC())
		require.NoError(t, err)
		openJob, deadlineJob := newExamSittingLifecycleJobs(t, sitting.ID, 1, startAt, endAt)
		result, err := ss.ExamSitting().Schedule(ctx, &store.ExamSittingSchedule{Sitting: sitting, OpenJob: openJob, DeadlineJob: deadlineJob,
			ActorUserID: fixture.actor.ID, AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID,
			AuditAt: model.GetMillis(), Mail: newExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailScheduled,
				model.MailTemplateExamSittingScheduled)}, examCommand(fixture.actor.ID, "exam.sitting.schedule.v1", key, key+"-command"))
		require.NoError(t, err)
		return result
	}

	// A prepared-Job collision rolls back the Sitting and its second Job.
	rollbackStart := fixture.period.StartsAt.Add(time.Hour)
	rollbackEnd := rollbackStart.Add(time.Hour)
	rollbackSitting, err := model.NewExamSitting(model.NewExamSittingID(), fixture.examID, fixture.revisionID, fixture.class.ID,
		rollbackStart, rollbackEnd, model.NowUTC())
	require.NoError(t, err)
	reservedOpen, _ := newExamSittingLifecycleJobs(t, rollbackSitting.ID, 1, rollbackStart, rollbackEnd)
	_, inserted, err := ss.Job().Enqueue(ctx, &store.JobEnqueue{Job: reservedOpen})
	require.NoError(t, err)
	require.True(t, inserted, "failed to reserve conflicting lifecycle Job")
	conflictOpen, conflictDeadline := newExamSittingLifecycleJobs(t, rollbackSitting.ID, 1, rollbackStart, rollbackEnd)
	_, err = ss.ExamSitting().Schedule(ctx, &store.ExamSittingSchedule{Sitting: rollbackSitting, OpenJob: conflictOpen, DeadlineJob: conflictDeadline,
		ActorUserID: fixture.actor.ID, AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID,
		AuditAt: model.GetMillis(), Mail: newExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailScheduled,
			model.MailTemplateExamSittingScheduled)}, examCommand(fixture.actor.ID, "exam.sitting.schedule.v1", "sitting-job-rollback", "sitting-job-rollback-command"))
	requireConflictConstraint(t, err, "exam_sitting_job_mismatch")
	_, err = ss.ExamSitting().Get(ctx, fixture.examID, rollbackSitting.ID)
	require.True(t, store.IsNotFound(err), "Sitting survived prepared-Job rollback")

	startAt := fixture.period.StartsAt.Add(3 * time.Hour)
	endAt := startAt.Add(2 * time.Hour)
	active := schedule("sitting-lifecycle", fixture.class.ID, startAt, endAt)
	beforeAudit := saveExamSittingSystemAudit(t, ctx, ss, active.Value.Sitting.ID, fixture.unitID)
	before, err := ss.ExamSitting().AdvanceDue(ctx, &store.ExamSittingDueAdvance{SittingID: active.Value.Sitting.ID,
		AuditEventID: beforeAudit.ID, AuditAt: model.GetMillis()})
	require.NoError(t, err)
	require.False(t, before.Changed)
	require.Equal(t, model.ExamSittingScheduled, before.Value.Sitting.State)

	dueStart, dueEnd := model.NowUTC().Add(-time.Minute), model.NowUTC().Add(time.Hour)
	probe.SetSchedule(t, ctx, active.Value.Sitting.ID, dueStart, dueEnd)
	due, err := ss.ExamSitting().ListLifecycleDue(ctx, store.ExamSittingLifecycleDueOptions{Limit: 201})
	require.NoError(t, err)
	found := false
	for _, item := range due {
		if item.Value.Sitting.ID == active.Value.Sitting.ID && item.DueAt.Equal(dueStart) {
			found = true
		}
	}
	require.True(t, found, "ListLifecycleDue()=%#v", due)
	openAudit := saveExamSittingSystemAudit(t, ctx, ss, active.Value.Sitting.ID, fixture.unitID)
	opened, err := ss.ExamSitting().AdvanceDue(ctx, &store.ExamSittingDueAdvance{SittingID: active.Value.Sitting.ID,
		AuditEventID: openAudit.ID, AuditAt: model.GetMillis()})
	require.NoError(t, err)
	require.True(t, opened.Changed)
	require.Equal(t, store.ExamSittingTransitionOpened, opened.Transition)
	require.Equal(t, model.ExamSittingOpen, opened.Value.Sitting.State)
	require.Equal(t, int64(2), opened.Value.Sitting.Revision)
	// A successful schedule replay is the exact committed outcome even after a
	// later lifecycle transaction advances the current row.
	replayScheduled := *active.Value.Sitting
	replayOpenJob, replayDeadlineJob := newExamSittingLifecycleJobs(t, replayScheduled.ID, replayScheduled.Revision,
		replayScheduled.ScheduledStartAt, replayScheduled.ScheduledEndAt)
	scheduleReplay, err := ss.ExamSitting().Schedule(ctx, &store.ExamSittingSchedule{Sitting: &replayScheduled,
		OpenJob: replayOpenJob, DeadlineJob: replayDeadlineJob, ActorUserID: fixture.actor.ID,
		AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID, AuditAt: model.GetMillis(),
		Mail: newExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailScheduled, model.MailTemplateExamSittingScheduled)},
		examCommand(fixture.actor.ID, "exam.sitting.schedule.v1", "sitting-lifecycle", "sitting-lifecycle-command"))
	require.NoError(t, err)
	require.True(t, scheduleReplay.Replayed)
	require.Equal(t, model.ExamSittingScheduled, scheduleReplay.Value.Sitting.State)
	require.Equal(t, int64(1), scheduleReplay.Value.Sitting.Revision)

	pauseAt := model.NowUTC()
	pauseAudit := saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID)
	pauseInput := &store.ExamSittingManagerTransition{ExamID: fixture.examID, SittingID: active.Value.Sitting.ID,
		ActorUserID: fixture.actor.ID, ExpectedRevision: 2, PrivateReason: "investigate candidate report", ChangedAt: pauseAt,
		AuditEventID: pauseAudit.ID, AuditAt: model.GetMillis()}
	pauseCommand := examCommand(fixture.actor.ID, "exam.sitting.pause.v1", "sitting-pause", "sitting-pause-command")
	paused, err := ss.ExamSitting().Pause(ctx, pauseInput, pauseCommand)
	require.NoError(t, err)
	require.Equal(t, store.ExamSittingTransitionManagerPaused, paused.Transition)
	require.Equal(t, model.ExamSittingPaused, paused.Value.Sitting.State)
	require.Equal(t, int64(3), paused.Value.Sitting.Revision)
	replayInput := *pauseInput
	replayInput.AuditEventID = saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID
	replayed, err := ss.ExamSitting().Pause(ctx, &replayInput, pauseCommand)
	require.NoError(t, err)
	require.True(t, replayed.Replayed)
	require.Equal(t, int64(3), replayed.Value.Sitting.Revision)

	resumeAudit := saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID)
	resumed, err := ss.ExamSitting().Resume(ctx, &store.ExamSittingManagerTransition{ExamID: fixture.examID, SittingID: active.Value.Sitting.ID,
		ActorUserID: fixture.actor.ID, ExpectedRevision: 3, PrivateReason: "candidate may continue", ChangedAt: model.NowUTC(),
		AuditEventID: resumeAudit.ID, AuditAt: model.GetMillis()},
		examCommand(fixture.actor.ID, "exam.sitting.resume.v1", "sitting-resume", "sitting-resume-command"))
	require.NoError(t, err)
	require.Equal(t, model.ExamSittingOpen, resumed.Value.Sitting.State)
	require.Equal(t, int64(4), resumed.Value.Sitting.Revision)

	extendedEnd := dueEnd.Add(time.Hour)
	_, extendedDeadline := newExamSittingLifecycleJobs(t, active.Value.Sitting.ID, 5, dueStart, extendedEnd)
	extendAudit := saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID)
	extended, err := ss.ExamSitting().Extend(ctx, &store.ExamSittingExtension{ExamID: fixture.examID, SittingID: active.Value.Sitting.ID,
		ActorUserID: fixture.actor.ID, ExpectedRevision: 4, ScheduledEndAt: extendedEnd, DeadlineJob: extendedDeadline,
		PrivateReason: "grant additional coding time", ChangedAt: model.NowUTC(), AuditEventID: extendAudit.ID, AuditAt: model.GetMillis()},
		examCommand(fixture.actor.ID, "exam.sitting.extend.v1", "sitting-extend", "sitting-extend-command"))
	require.NoError(t, err)
	require.Equal(t, extendedEnd, extended.Value.Sitting.ScheduledEndAt)
	require.Equal(t, int64(5), extended.Value.Sitting.Revision)
	_, equalDeadline := newExamSittingLifecycleJobs(t, active.Value.Sitting.ID, 6, dueStart, extendedEnd)
	_, err = ss.ExamSitting().Extend(ctx, &store.ExamSittingExtension{ExamID: fixture.examID, SittingID: active.Value.Sitting.ID,
		ActorUserID: fixture.actor.ID, ExpectedRevision: 5, ScheduledEndAt: extendedEnd, DeadlineJob: equalDeadline,
		PrivateReason: "no actual extension", ChangedAt: model.NowUTC(),
		AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID, AuditAt: model.GetMillis()},
		examCommand(fixture.actor.ID, "exam.sitting.extend.v1", "sitting-extend-equal", "sitting-extend-equal-command"))
	requireConflictConstraint(t, err, "exam_sitting_extension_not_later")
	beyondPeriod := fixture.period.EndsAt.Add(time.Second)
	_, beyondDeadline := newExamSittingLifecycleJobs(t, active.Value.Sitting.ID, 6, dueStart, beyondPeriod)
	_, err = ss.ExamSitting().Extend(ctx, &store.ExamSittingExtension{ExamID: fixture.examID, SittingID: active.Value.Sitting.ID,
		ActorUserID: fixture.actor.ID, ExpectedRevision: 5, ScheduledEndAt: beyondPeriod, DeadlineJob: beyondDeadline,
		PrivateReason: "invalid extension", ChangedAt: model.NowUTC(),
		AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID, AuditAt: model.GetMillis()},
		examCommand(fixture.actor.ID, "exam.sitting.extend.v1", "sitting-extend-period", "sitting-extend-period-command"))
	requireConflictConstraint(t, err, "exam_sitting_schedule_outside_period")
	laterReplayInput := *pauseInput
	laterReplayInput.AuditEventID = saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID
	laterReplay, err := ss.ExamSitting().Pause(ctx, &laterReplayInput, pauseCommand)
	require.NoError(t, err)
	require.True(t, laterReplay.Replayed)
	require.Equal(t, model.ExamSittingPaused, laterReplay.Value.Sitting.State)
	require.Equal(t, int64(3), laterReplay.Value.Sitting.Revision)
	currentAfterReplay, err := ss.ExamSitting().Get(ctx, fixture.examID, active.Value.Sitting.ID)
	require.NoError(t, err)
	require.Equal(t, model.ExamSittingOpen, currentAfterReplay.Sitting.State)
	require.Equal(t, int64(5), currentAfterReplay.Sitting.Revision)

	closeAt := model.NowUTC()
	closeAudit := saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID)
	closing, err := ss.ExamSitting().EarlyClose(ctx, &store.ExamSittingManagerTransition{ExamID: fixture.examID,
		SittingID: active.Value.Sitting.ID, ActorUserID: fixture.actor.ID, ExpectedRevision: 5,
		FinalizeJob: newExamSittingFinalizeJob(t, active.Value.Sitting.ID, 6, closeAt), PrivateReason: "end session early",
		ChangedAt: closeAt, AuditEventID: closeAudit.ID, AuditAt: model.GetMillis()},
		examCommand(fixture.actor.ID, "exam.sitting.close.v1", "sitting-early-close", "sitting-early-close-command"))
	require.NoError(t, err)
	require.Equal(t, model.ExamSittingClosing, closing.Value.Sitting.State)
	require.Equal(t, model.ExamSittingReasonManagerClosed, closing.Value.Sitting.ReasonCode)
	closingDue, err := ss.ExamSitting().ListLifecycleDue(ctx, store.ExamSittingLifecycleDueOptions{Limit: 201})
	require.NoError(t, err)
	foundClosing := false
	for _, item := range closingDue {
		if item.Value.Sitting.ID == closing.Value.Sitting.ID && item.Value.Sitting.State == model.ExamSittingClosing &&
			item.DueAt.Equal(closing.Value.Sitting.ClosingAt.Time) {
			foundClosing = true
		}
	}
	require.True(t, foundClosing, "ListLifecycleDue() omitted Closing recovery row: %#v", closingDue)
	closedAudit := saveExamSittingSystemAudit(t, ctx, ss, active.Value.Sitting.ID, fixture.unitID)
	closed, err := ss.ExamSitting().FinishSealing(ctx, &store.ExamSittingFinishSealing{SittingID: active.Value.Sitting.ID,
		AuditEventID: closedAudit.ID, AuditAt: model.GetMillis()})
	require.NoError(t, err)
	require.True(t, closed.Changed)
	require.Equal(t, model.ExamSittingClosed, closed.Value.Sitting.State)
	require.Equal(t, int64(7), closed.Value.Sitting.Revision)

	actions := probe.PrivateActions(t, ctx, active.Value.Sitting.ID)
	wantActions := []ExamSittingPrivateActionProbe{
		{ActionCode: "manager_paused", PrivateReason: "investigate candidate report", Revision: 3},
		{ActionCode: "manager_resumed", PrivateReason: "candidate may continue", Revision: 4},
		{ActionCode: "manager_extended", PrivateReason: "grant additional coding time", Revision: 5},
		{ActionCode: "manager_closed", PrivateReason: "end session early", Revision: 6},
	}
	require.Len(t, actions, len(wantActions))
	for index := range wantActions {
		require.Equal(t, wantActions[index], actions[index], "private action[%d]", index)
	}
	probe.AssertAppendOnly(t, ctx, active.Value.Sitting.ID)

	// Entirely elapsed schedules cancel without opening.
	elapsed := schedule("sitting-elapsed", fixture.class.ID, fixture.period.StartsAt.Add(8*time.Hour), fixture.period.StartsAt.Add(9*time.Hour))
	probe.SetSchedule(t, ctx, elapsed.Value.Sitting.ID, model.NowUTC().Add(-2*time.Hour), model.NowUTC().Add(-time.Hour))
	elapsedAudit := saveExamSittingSystemAudit(t, ctx, ss, elapsed.Value.Sitting.ID, fixture.unitID)
	elapsedResult, err := ss.ExamSitting().AdvanceDue(ctx, &store.ExamSittingDueAdvance{SittingID: elapsed.Value.Sitting.ID,
		AuditEventID: elapsedAudit.ID, AuditAt: model.GetMillis()})
	require.NoError(t, err)
	require.Equal(t, store.ExamSittingTransitionScheduleElapsed, elapsedResult.Transition)
	require.Equal(t, model.ExamSittingCanceled, elapsedResult.Value.Sitting.State)

	// A lineage archived after scheduling cancels at opening revalidation.
	invalidClass := saveClass(t, ctx, ss, fixture.levelID.String(), fixture.period.ID.String(), "sitting-lifecycle-invalid-class")
	invalid := schedule("sitting-invalid-structure", invalidClass.ID, fixture.period.StartsAt.Add(10*time.Hour), fixture.period.StartsAt.Add(11*time.Hour))
	_, err = ArchiveClass(ctx, ss, invalidClass.ID, model.GetMillis())
	require.NoError(t, err)
	probe.SetSchedule(t, ctx, invalid.Value.Sitting.ID, model.NowUTC().Add(-time.Minute), model.NowUTC().Add(time.Hour))
	invalidAudit := saveExamSittingSystemAudit(t, ctx, ss, invalid.Value.Sitting.ID, fixture.unitID)
	invalidResult, err := ss.ExamSitting().AdvanceDue(ctx, &store.ExamSittingDueAdvance{SittingID: invalid.Value.Sitting.ID,
		AuditEventID: invalidAudit.ID, AuditAt: model.GetMillis()})
	require.NoError(t, err)
	require.Equal(t, store.ExamSittingTransitionAcademicStructureInvalid, invalidResult.Transition)
	require.Equal(t, model.ExamSittingCanceled, invalidResult.Value.Sitting.State)

	// Deadline processing treats Paused exactly like Open and enqueues the
	// revision-keyed finalize occurrence in the same transaction.
	deadline := schedule("sitting-paused-deadline", fixture.class.ID, fixture.period.StartsAt.Add(12*time.Hour), fixture.period.StartsAt.Add(13*time.Hour))
	deadlineStart, deadlineEnd := model.NowUTC().Add(-time.Minute), model.NowUTC().Add(time.Hour)
	probe.SetSchedule(t, ctx, deadline.Value.Sitting.ID, deadlineStart, deadlineEnd)
	deadlineOpenAudit := saveExamSittingSystemAudit(t, ctx, ss, deadline.Value.Sitting.ID, fixture.unitID)
	deadlineOpen, err := ss.ExamSitting().AdvanceDue(ctx, &store.ExamSittingDueAdvance{SittingID: deadline.Value.Sitting.ID,
		AuditEventID: deadlineOpenAudit.ID, AuditAt: model.GetMillis()})
	require.NoError(t, err)
	deadlinePauseAudit := saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID)
	deadlinePaused, err := ss.ExamSitting().Pause(ctx, &store.ExamSittingManagerTransition{ExamID: fixture.examID,
		SittingID: deadline.Value.Sitting.ID, ActorUserID: fixture.actor.ID, ExpectedRevision: deadlineOpen.Value.Sitting.Revision,
		PrivateReason: "pause through deadline", ChangedAt: model.NowUTC(), AuditEventID: deadlinePauseAudit.ID, AuditAt: model.GetMillis()},
		examCommand(fixture.actor.ID, "exam.sitting.pause.v1", "sitting-deadline-pause", "sitting-deadline-pause-command"))
	require.NoError(t, err)
	pastDeadline := model.NowUTC().Add(-time.Second)
	probe.SetSchedule(t, ctx, deadline.Value.Sitting.ID, deadlineStart, pastDeadline)
	_, err = ss.ExamSitting().Resume(ctx, &store.ExamSittingManagerTransition{ExamID: fixture.examID,
		SittingID: deadline.Value.Sitting.ID, ActorUserID: fixture.actor.ID, ExpectedRevision: deadlinePaused.Value.Sitting.Revision,
		PrivateReason: "too late", ChangedAt: model.NowUTC(),
		AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID, AuditAt: model.GetMillis()},
		examCommand(fixture.actor.ID, "exam.sitting.resume.v1", "sitting-resume-after-deadline", "sitting-resume-after-deadline-command"))
	requireConflictConstraint(t, err, "exam_sitting_deadline_reached")
	_, err = ss.ExamSitting().Pause(ctx, &store.ExamSittingManagerTransition{ExamID: fixture.examID,
		SittingID: deadline.Value.Sitting.ID, ActorUserID: fixture.actor.ID, ExpectedRevision: deadlinePaused.Value.Sitting.Revision,
		PrivateReason: "wrong state", ChangedAt: model.NowUTC(),
		AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID, AuditAt: model.GetMillis()},
		examCommand(fixture.actor.ID, "exam.sitting.pause.v1", "sitting-pause-after-deadline", "sitting-pause-after-deadline-command"))
	requireConflictConstraint(t, err, "exam_sitting_state")
	staleFinalizeAudit := saveExamSittingSystemAudit(t, ctx, ss, deadline.Value.Sitting.ID, fixture.unitID)
	_, err = ss.ExamSitting().AdvanceDue(ctx, &store.ExamSittingDueAdvance{SittingID: deadline.Value.Sitting.ID,
		FinalizeJob:  newExamSittingFinalizeJob(t, deadline.Value.Sitting.ID, deadlineOpen.Value.Sitting.Revision+1, pastDeadline),
		AuditEventID: staleFinalizeAudit.ID, AuditAt: model.GetMillis()})
	requireConflictConstraint(t, err, "exam_sitting_revision")
	afterStaleFinalize, err := ss.ExamSitting().Get(ctx, fixture.examID, deadline.Value.Sitting.ID)
	require.NoError(t, err)
	require.Equal(t, model.ExamSittingPaused, afterStaleFinalize.Sitting.State)
	require.Equal(t, deadlinePaused.Value.Sitting.Revision, afterStaleFinalize.Sitting.Revision)
	deadlineAudit := saveExamSittingSystemAudit(t, ctx, ss, deadline.Value.Sitting.ID, fixture.unitID)
	deadlineClosing, err := ss.ExamSitting().AdvanceDue(ctx, &store.ExamSittingDueAdvance{SittingID: deadline.Value.Sitting.ID,
		FinalizeJob:  newExamSittingFinalizeJob(t, deadline.Value.Sitting.ID, deadlinePaused.Value.Sitting.Revision+1, pastDeadline),
		AuditEventID: deadlineAudit.ID, AuditAt: model.GetMillis()})
	require.NoError(t, err)
	require.Equal(t, store.ExamSittingTransitionScheduledEndReached, deadlineClosing.Transition)
	require.Equal(t, model.ExamSittingClosing, deadlineClosing.Value.Sitting.State)
	require.Equal(t, model.ExamSittingReasonScheduledEndReached, deadlineClosing.Value.Sitting.ReasonCode)

	// Competing manager transitions share the Sitting revision fence. Exactly
	// one commits, and the winner's returned value remains coherent with its own
	// transition even while the loser observes the later row.
	managerRace := schedule("sitting-manager-race", fixture.class.ID, fixture.period.StartsAt.Add(16*time.Hour), fixture.period.StartsAt.Add(17*time.Hour))
	managerRaceStart, managerRaceEnd := model.NowUTC().Add(-time.Minute), model.NowUTC().Add(time.Hour)
	probe.SetSchedule(t, ctx, managerRace.Value.Sitting.ID, managerRaceStart, managerRaceEnd)
	managerRaceOpen, err := ss.ExamSitting().AdvanceDue(ctx, &store.ExamSittingDueAdvance{SittingID: managerRace.Value.Sitting.ID,
		AuditEventID: saveExamSittingSystemAudit(t, ctx, ss, managerRace.Value.Sitting.ID, fixture.unitID).ID, AuditAt: model.GetMillis()})
	require.NoError(t, err)
	raceRevision := managerRaceOpen.Value.Sitting.Revision
	raceCloseAt := model.NowUTC()
	pauseRaceInput := &store.ExamSittingManagerTransition{ExamID: fixture.examID, SittingID: managerRace.Value.Sitting.ID,
		ActorUserID: fixture.actor.ID, ExpectedRevision: raceRevision, PrivateReason: "concurrent pause", ChangedAt: model.NowUTC(),
		AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID, AuditAt: model.GetMillis()}
	closeRaceInput := &store.ExamSittingManagerTransition{ExamID: fixture.examID, SittingID: managerRace.Value.Sitting.ID,
		ActorUserID: fixture.actor.ID, ExpectedRevision: raceRevision,
		FinalizeJob:   newExamSittingFinalizeJob(t, managerRace.Value.Sitting.ID, raceRevision+1, raceCloseAt),
		PrivateReason: "concurrent close", ChangedAt: raceCloseAt,
		AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID, AuditAt: model.GetMillis()}
	type lifecycleRaceResult struct {
		value *store.ExamSittingLifecycleResult
		err   error
	}
	raceResults := make(chan lifecycleRaceResult, 2)
	go func() {
		value, raceErr := ss.ExamSitting().Pause(ctx, pauseRaceInput,
			examCommand(fixture.actor.ID, "exam.sitting.pause.v1", "sitting-manager-race-pause", "sitting-manager-race-pause-command"))
		raceResults <- lifecycleRaceResult{value: value, err: raceErr}
	}()
	go func() {
		value, raceErr := ss.ExamSitting().EarlyClose(ctx, closeRaceInput,
			examCommand(fixture.actor.ID, "exam.sitting.close.v1", "sitting-manager-race-close", "sitting-manager-race-close-command"))
		raceResults <- lifecycleRaceResult{value: value, err: raceErr}
	}()
	firstRace, secondRace := <-raceResults, <-raceResults
	require.NotEqual(t, secondRace.err == nil, firstRace.err == nil, "manager transition race results: first=%#v/%v second=%#v/%v", firstRace.value, firstRace.err, secondRace.value, secondRace.err)
	winner, loser := firstRace, secondRace
	if winner.err != nil {
		winner, loser = secondRace, firstRace
	}
	require.Equal(t, raceRevision+1, winner.value.Value.Sitting.Revision)
	require.False(t, winner.value.Transition == store.ExamSittingTransitionManagerPaused && winner.value.Value.Sitting.State != model.ExamSittingPaused)
	require.False(t, winner.value.Transition == store.ExamSittingTransitionManagerClosed && winner.value.Value.Sitting.State != model.ExamSittingClosing)
	var loserConflict *store.ErrConflict
	require.ErrorAs(t, loser.err, &loserConflict)
	require.False(t, loserConflict.Constraint != "exam_sitting_revision" && loserConflict.Constraint != "exam_sitting_state")

	// An archived Exam may still be paused or closed when already live, but it
	// cannot be resumed or extended into further participation.
	archivedLive := schedule("sitting-archived-live", fixture.class.ID, fixture.period.StartsAt.Add(14*time.Hour), fixture.period.StartsAt.Add(15*time.Hour))
	archivedStart, archivedEnd := model.NowUTC().Add(-time.Minute), model.NowUTC().Add(time.Hour)
	probe.SetSchedule(t, ctx, archivedLive.Value.Sitting.ID, archivedStart, archivedEnd)
	archivedOpen, err := ss.ExamSitting().AdvanceDue(ctx, &store.ExamSittingDueAdvance{SittingID: archivedLive.Value.Sitting.ID,
		AuditEventID: saveExamSittingSystemAudit(t, ctx, ss, archivedLive.Value.Sitting.ID, fixture.unitID).ID, AuditAt: model.GetMillis()})
	require.NoError(t, err)
	examBeforeArchive, err := ss.ExamAuthoring().Resolve(ctx, fixture.examID)
	require.NoError(t, err)
	archiveAt := model.NowUTC()
	_, err = ss.ExamAuthoring().Archive(ctx,
		newExamArchive(t, ctx, ss, fixture.examID, fixture.actor.ID, examBeforeArchive.Revision, archiveAt),
		examCommand(fixture.actor.ID, "exam.archive.v1", "sitting-live-archive", "sitting-live-archive-command"))
	require.NoError(t, err)
	archivedPaused, err := ss.ExamSitting().Pause(ctx, &store.ExamSittingManagerTransition{ExamID: fixture.examID,
		SittingID: archivedLive.Value.Sitting.ID, ActorUserID: fixture.actor.ID, ExpectedRevision: archivedOpen.Value.Sitting.Revision,
		PrivateReason: "pause archived live Sitting", ChangedAt: model.NowUTC(),
		AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID, AuditAt: model.GetMillis()},
		examCommand(fixture.actor.ID, "exam.sitting.pause.v1", "sitting-archived-pause", "sitting-archived-pause-command"))
	require.NoError(t, err)
	_, err = ss.ExamSitting().Resume(ctx, &store.ExamSittingManagerTransition{ExamID: fixture.examID,
		SittingID: archivedLive.Value.Sitting.ID, ActorUserID: fixture.actor.ID, ExpectedRevision: archivedPaused.Value.Sitting.Revision,
		PrivateReason: "resume archived Sitting", ChangedAt: model.NowUTC(),
		AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID, AuditAt: model.GetMillis()},
		examCommand(fixture.actor.ID, "exam.sitting.resume.v1", "sitting-archived-resume", "sitting-archived-resume-command"))
	requireConflictConstraint(t, err, "exam_archived")
	archivedCloseAt := model.NowUTC()
	archivedClosing, err := ss.ExamSitting().EarlyClose(ctx, &store.ExamSittingManagerTransition{ExamID: fixture.examID,
		SittingID: archivedLive.Value.Sitting.ID, ActorUserID: fixture.actor.ID, ExpectedRevision: archivedPaused.Value.Sitting.Revision,
		FinalizeJob:   newExamSittingFinalizeJob(t, archivedLive.Value.Sitting.ID, archivedPaused.Value.Sitting.Revision+1, archivedCloseAt),
		PrivateReason: "close archived live Sitting", ChangedAt: archivedCloseAt,
		AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID, AuditAt: model.GetMillis()},
		examCommand(fixture.actor.ID, "exam.sitting.close.v1", "sitting-archived-close", "sitting-archived-close-command"))
	require.NoError(t, err)
	require.Equal(t, model.ExamSittingClosing, archivedClosing.Value.Sitting.State)
	require.Equal(t, model.ExamSittingReasonManagerClosed, archivedClosing.Value.Sitting.ReasonCode)
}

// TestExamSittingStoreSQLGuards characterizes PostgreSQL-backed aggregate
// guards whose ordering and DB-clock behavior are part of the contract.
func TestExamSittingStoreSQLGuards(t *testing.T, ss store.Store, probe ExamSittingSQLProbe) {
	ctx := context.Background()
	fixture := newExamSittingFixture(t, ctx, ss)

	schedule := func(key string, actor model.UserID, revisionID model.ExamRevisionID, classID model.ClassID, start, end time.Time) (*store.ExamSittingCommandResult, error) {
		sitting, err := model.NewExamSitting(model.NewExamSittingID(), fixture.examID, revisionID, classID, start, end, model.NowUTC())
		require.NoError(t, err)
		openJob, deadlineJob := newExamSittingLifecycleJobs(t, sitting.ID, 1, start, end)
		return ss.ExamSitting().Schedule(ctx, &store.ExamSittingSchedule{Sitting: sitting, OpenJob: openJob, DeadlineJob: deadlineJob, ActorUserID: actor,
			AuditEventID: saveExamSittingAudit(t, ctx, ss, actor, fixture.examID, fixture.unitID).ID, AuditAt: model.GetMillis(),
			Mail: newExamSittingMailFanout(t, actor, store.ExamSittingMailScheduled, model.MailTemplateExamSittingScheduled)},
			examCommand(actor, "exam.sitting.schedule.v1", key, key+"-command"))
	}
	requireScheduleConflict := func(key, constraint string, revisionID model.ExamRevisionID, classID model.ClassID, start, end time.Time) {
		t.Helper()
		_, err := schedule(key, fixture.actor.ID, revisionID, classID, start, end)
		requireConflictConstraint(t, err, constraint)
	}

	// A Class in another Academic Unit of the same Institution still does not
	// belong to the Exam's exact ownership scope.
	foreignUnit := saveAcademicUnit(t, ctx, ss, fixture.institutionID.String(), "", "sitting-foreign-unit")
	foreignProgramme := saveProgramme(t, ctx, ss, foreignUnit.ID.String(), "sitting-foreign-programme")
	foreignLevel := saveProgrammeLevel(t, ctx, ss, foreignProgramme.ID.String(), "sitting-foreign-level")
	foreignClass := saveClass(t, ctx, ss, foreignLevel.ID.String(), fixture.period.ID.String(), "sitting-foreign-class")
	requireScheduleConflict("sitting-foreign-au", "exam_sitting_class_lineage", fixture.revisionID, foreignClass.ID,
		fixture.period.StartsAt.Add(time.Hour), fixture.period.StartsAt.Add(2*time.Hour))

	archivedClass := saveClass(t, ctx, ss, fixture.levelID.String(), fixture.period.ID.String(), "sitting-archived-class")
	_, err := ArchiveClass(ctx, ss, archivedClass.ID, model.GetMillis())
	require.NoError(t, err)
	requireScheduleConflict("sitting-archived-class", "exam_sitting_class_lineage", fixture.revisionID, archivedClass.ID,
		fixture.period.StartsAt.Add(3*time.Hour), fixture.period.StartsAt.Add(4*time.Hour))

	require.NotNil(t, probe.ArchiveProgrammeLevel, "ArchiveProgrammeLevel probe is required")
	ancestorProgramme := saveProgramme(t, ctx, ss, fixture.unitID.String(), "sitting-archived-ancestor-programme")
	ancestorLevel := saveProgrammeLevel(t, ctx, ss, ancestorProgramme.ID.String(), "sitting-archived-ancestor-level")
	ancestorClass := saveClass(t, ctx, ss, ancestorLevel.ID.String(), fixture.period.ID.String(), "sitting-archived-ancestor-class")
	probe.ArchiveProgrammeLevel(t, ctx, ancestorLevel.ID)
	requireScheduleConflict("sitting-archived-ancestor", "exam_sitting_class_lineage", fixture.revisionID, ancestorClass.ID,
		fixture.period.StartsAt.Add(5*time.Hour), fixture.period.StartsAt.Add(6*time.Hour))

	// Academic Period containment is inclusive at both outer boundaries while
	// the Sitting interval itself remains nonempty and half-open.
	boundary, err := schedule("sitting-period-boundaries", fixture.actor.ID, fixture.revisionID, fixture.class.ID,
		fixture.period.StartsAt, fixture.period.EndsAt)
	require.NoError(t, err)
	require.Equal(t, fixture.period.StartsAt, boundary.Value.Sitting.ScheduledStartAt)
	require.Equal(t, fixture.period.EndsAt, boundary.Value.Sitting.ScheduledEndAt)
	requireScheduleConflict("sitting-before-period", "exam_sitting_period_containment", fixture.revisionID, fixture.class.ID,
		fixture.period.StartsAt.Add(-time.Hour), fixture.period.StartsAt.Add(time.Hour))
	requireScheduleConflict("sitting-after-period", "exam_sitting_period_containment", fixture.revisionID, fixture.class.ID,
		fixture.period.EndsAt.Add(-time.Hour), fixture.period.EndsAt.Add(time.Hour))

	// Use a currently active Period to isolate the database-clock fence from
	// containment. A client-side "now" is necessarily equal to or behind the
	// statement timestamp by the time the locked SQL guard executes.
	now := model.NowUTC()
	currentPeriod := saveAcademicPeriod(t, ctx, ss, fixture.institutionID.String(), "sitting-current-period",
		model.MillisFromTime(now.Add(-24*time.Hour)))
	currentClass := saveClass(t, ctx, ss, fixture.levelID.String(), currentPeriod.ID.String(), "sitting-current-class")
	requireScheduleConflict("sitting-past-start", "exam_sitting_not_future", fixture.revisionID, currentClass.ID,
		now.Add(-time.Minute), now.Add(time.Hour))
	equalNow := model.NowUTC()
	requireScheduleConflict("sitting-equal-now-start", "exam_sitting_not_future", fixture.revisionID, currentClass.ID,
		equalNow, equalNow.Add(time.Hour))

	// Manager eligibility is rechecked inside the scheduling transaction.
	removedManager := saveUser(t, ctx, ss)
	_, err = ss.AcademicUnitMember().Save(ctx, &model.AcademicUnitMember{AcademicUnitID: fixture.unitID,
		UserID: removedManager.ID, StartsAt: model.NowUTC().Add(-time.Hour)})
	require.NoError(t, err)
	exam, err := ss.ExamAuthoring().Resolve(ctx, fixture.examID)
	require.NoError(t, err)
	addManager := newExamManagerMutation(t, ctx, ss, fixture.examID, fixture.actor.ID, removedManager.ID, exam.Revision, model.NowUTC(), false)
	addManager.Notices = examManagerMailNotices(t, model.TimeFromMillis(addManager.ChangedAt),
		examManagerMailRecipient{userID: removedManager.ID, key: model.MailTemplateExamManagerAdded})
	added, err := ss.ExamAuthoring().AddManager(ctx, addManager,
		examCommand(fixture.actor.ID, "exam.manager.add.v1", "sitting-manager-add", "sitting-manager-add-command"))
	require.NoError(t, err)
	removeManager := newExamManagerMutation(t, ctx, ss, fixture.examID, fixture.actor.ID, removedManager.ID, added.Exam.Revision, model.NowUTC(), false)
	removeManager.Notices = examManagerMailNotices(t, model.TimeFromMillis(removeManager.ChangedAt),
		examManagerMailRecipient{userID: removedManager.ID, key: model.MailTemplateExamManagerRemoved})
	_, err = ss.ExamAuthoring().RemoveManager(ctx, removeManager,
		examCommand(fixture.actor.ID, "exam.manager.remove.v1", "sitting-manager-remove", "sitting-manager-remove-command"))
	require.NoError(t, err)
	_, err = schedule("sitting-removed-manager", removedManager.ID, fixture.revisionID, fixture.class.ID,
		fixture.period.StartsAt.Add(7*time.Hour), fixture.period.StartsAt.Add(8*time.Hour))
	require.True(t, store.IsNotFound(err), "Schedule(removed manager) error")

	// Once both aggregate rows are locked, stale revision and current state
	// precede validation of the proposed foreign selection.
	precedence, err := schedule("sitting-precedence", fixture.actor.ID, fixture.revisionID, fixture.class.ID,
		fixture.period.StartsAt.Add(9*time.Hour), fixture.period.StartsAt.Add(10*time.Hour))
	require.NoError(t, err)
	_, err = ss.ExamSitting().Cancel(ctx, &store.ExamSittingCancellation{ExamID: fixture.examID, SittingID: precedence.Value.Sitting.ID,
		ActorUserID: fixture.actor.ID, ExpectedRevision: 1, PrivateReason: "precedence",
		AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID, AuditAt: model.GetMillis(),
		Mail: newExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailCanceled, model.MailTemplateExamSittingCanceled)},
		examCommand(fixture.actor.ID, "exam.sitting.cancel.v1", "sitting-precedence-cancel", "sitting-precedence-cancel-command"))
	require.NoError(t, err)
	invalidUpdate := func(expected int64, key string) error {
		openJob, deadlineJob := newExamSittingLifecycleJobs(t, precedence.Value.Sitting.ID, expected+1,
			fixture.period.StartsAt.Add(11*time.Hour), fixture.period.StartsAt.Add(12*time.Hour))
		_, updateErr := ss.ExamSitting().UpdateSchedule(ctx, &store.ExamSittingScheduleUpdate{ExamID: fixture.examID,
			SittingID: precedence.Value.Sitting.ID, ActorUserID: fixture.actor.ID, ExpectedRevision: expected,
			ExamRevisionID: fixture.revisionID, ClassID: foreignClass.ID, ScheduledStartAt: fixture.period.StartsAt.Add(11 * time.Hour),
			ScheduledEndAt: fixture.period.StartsAt.Add(12 * time.Hour), OpenJob: openJob, DeadlineJob: deadlineJob,
			AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID, AuditAt: model.GetMillis(),
			Mail: newExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailRescheduled, model.MailTemplateExamSittingRescheduled)},
			examCommand(fixture.actor.ID, "exam.sitting.schedule.update.v1", key, key+"-command"))
		return updateErr
	}
	requireConflictConstraint(t, invalidUpdate(1, "sitting-precedence-stale"), "exam_sitting_revision")
	requireConflictConstraint(t, invalidUpdate(2, "sitting-precedence-state"), "exam_sitting_state")

	// Competing mutations serialize on Exam then Sitting. Exactly one commits;
	// the loser observes the advanced optimistic revision (or lifecycle state).
	race, err := schedule("sitting-race", fixture.actor.ID, fixture.revisionID, fixture.class.ID,
		fixture.period.StartsAt.Add(13*time.Hour), fixture.period.StartsAt.Add(14*time.Hour))
	require.NoError(t, err)
	updateAudit := saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID)
	cancelAudit := saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID)
	raceOpenJob, raceDeadlineJob := newExamSittingLifecycleJobs(t, race.Value.Sitting.ID, 2,
		fixture.period.StartsAt.Add(15*time.Hour), fixture.period.StartsAt.Add(16*time.Hour))
	rescheduledMail := newExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailRescheduled, model.MailTemplateExamSittingRescheduled)
	canceledMail := newExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailCanceled, model.MailTemplateExamSittingCanceled)
	var updateErr, cancelErr error
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		_, updateErr = ss.ExamSitting().UpdateSchedule(ctx, &store.ExamSittingScheduleUpdate{ExamID: fixture.examID,
			SittingID: race.Value.Sitting.ID, ActorUserID: fixture.actor.ID, ExpectedRevision: 1,
			ExamRevisionID: fixture.revisionID, ClassID: fixture.class.ID, ScheduledStartAt: fixture.period.StartsAt.Add(15 * time.Hour),
			ScheduledEndAt: fixture.period.StartsAt.Add(16 * time.Hour), OpenJob: raceOpenJob, DeadlineJob: raceDeadlineJob,
			AuditEventID: updateAudit.ID, AuditAt: model.GetMillis(),
			Mail: rescheduledMail},
			examCommand(fixture.actor.ID, "exam.sitting.schedule.update.v1", "sitting-race-update", "sitting-race-update-command"))
	}()
	go func() {
		defer wait.Done()
		_, cancelErr = ss.ExamSitting().Cancel(ctx, &store.ExamSittingCancellation{ExamID: fixture.examID,
			SittingID: race.Value.Sitting.ID, ActorUserID: fixture.actor.ID, ExpectedRevision: 1,
			PrivateReason: "race", AuditEventID: cancelAudit.ID, AuditAt: model.GetMillis(),
			Mail: canceledMail},
			examCommand(fixture.actor.ID, "exam.sitting.cancel.v1", "sitting-race-cancel", "sitting-race-cancel-command"))
	}()
	wait.Wait()
	require.NotEqual(t, cancelErr == nil, updateErr == nil, "race update error=%v cancel error=%v; want exactly one winner", updateErr, cancelErr)
	loser := updateErr
	if loser == nil {
		loser = cancelErr
	}
	var conflict *store.ErrConflict
	require.ErrorAs(t, loser, &conflict)
	require.False(t, conflict.Constraint != "exam_sitting_revision" && conflict.Constraint != "exam_sitting_state")
}

type examSittingFixture struct {
	actor         *model.User
	institutionID model.InstitutionID
	unitID        model.AcademicUnitID
	levelID       model.ProgrammeLevelID
	examID        model.ExamID
	revisionID    model.ExamRevisionID
	class         *model.Class
	period        *model.AcademicPeriod
}

func newExamSittingFixture(t *testing.T, ctx context.Context, ss store.Store) examSittingFixture {
	return newExamSittingFixtureWithCapacity(t, ctx, ss, model.ExamCapacityPolicy{})
}

func newExamSittingFixtureWithCapacity(t *testing.T, ctx context.Context, ss store.Store, capacity model.ExamCapacityPolicy) examSittingFixture {
	t.Helper()
	classFixture := saveClassFixture(t, ctx, ss)
	if !capacity.IsZero() {
		institution, err := ss.Institution().GetSingleton(ctx)
		require.NoError(t, err)
		institution.ExamCapacity = capacity
		_, err = UpdateInstitution(ctx, ss, institution)
		require.NoError(t, err)
	}
	class := saveClass(t, ctx, ss, classFixture.level.ID.String(), classFixture.period.ID.String(), "sitting-class")
	actor := saveUser(t, ctx, ss)
	created := createCatalogExam(t, ctx, ss, classFixture.programme.AcademicUnitID, actor.ID, model.NowUTC(), "sitting-exam")
	publication := examRevisionPublication(t, ctx, ss, created.Value.Exam.ID, actor.ID, classFixture.programme.AcademicUnitID, 1, model.NowUTC())
	published, err := ss.ExamRevision().Publish(ctx, publication, examCommand(actor.ID, "exam.revision.publish.v1", "sitting-revision", "sitting-revision-command"))
	require.NoError(t, err)
	return examSittingFixture{actor: actor, institutionID: classFixture.institution.ID, unitID: classFixture.programme.AcademicUnitID,
		levelID: classFixture.level.ID, examID: created.Value.Exam.ID,
		revisionID: published.Revision.ID, class: class, period: classFixture.period}
}

func saveExamSittingAudit(t *testing.T, ctx context.Context, ss store.Store, actor model.UserID, exam model.ExamID, unit model.AcademicUnitID) *model.AuditEvent {
	t.Helper()
	audit, err := ss.Audit().Save(ctx, &model.AuditEvent{ActorID: actor, Action: "exam.sitting.manage",
		Resource: model.Resource{Type: model.ResourceExam, ID: exam.String()}, ScopeType: model.RoleScopeAcademicUnit,
		ScopeID: unit.String(), Status: model.AuditStatusAttempt, NodeID: "test-node"})
	require.NoError(t, err)
	return audit
}

func saveExamSittingSystemAudit(t *testing.T, ctx context.Context, ss store.Store, sitting model.ExamSittingID, unit model.AcademicUnitID) *model.AuditEvent {
	t.Helper()
	audit, err := ss.Audit().Save(ctx, &model.AuditEvent{Action: string(model.ActionExamSittingManage),
		Resource: model.Resource{Type: model.ResourceExamSitting, ID: sitting.String()}, ScopeType: model.RoleScopeAcademicUnit,
		ScopeID: unit.String(), Status: model.AuditStatusAttempt, NodeID: "test-node"})
	require.NoError(t, err)
	return audit
}

func newExamSittingLifecycleJobs(t *testing.T, sittingID model.ExamSittingID, revision int64, startAt, endAt time.Time) (*model.Job, *model.Job) {
	t.Helper()
	command, err := model.EncodeExamSittingLifecycleCommand(model.ExamSittingLifecycleCommandV1{ExamSittingID: sittingID})
	require.NoError(t, err)
	createdAt := model.NowUTC()
	newJob := func(phase model.ExamSittingLifecycleJobPhase, availableAt time.Time) *model.Job {
		key, keyErr := model.ExamSittingLifecycleDedupeKey(sittingID, phase, revision)
		require.NoError(t, keyErr)
		job, jobErr := model.NewJobWithDedupePolicy(model.NewJobID(), model.JobTypeExamSittingLifecycle, 1, command,
			key, model.JobDedupeActive, createdAt, availableAt, 8)
		require.NoError(t, jobErr)
		return job
	}
	return newJob(model.ExamSittingLifecycleJobOpen, startAt), newJob(model.ExamSittingLifecycleJobDeadline, endAt)
}

func newExamSittingMailFanout(t *testing.T, actorID model.UserID, change store.ExamSittingMailChangeKind,
	templateKey model.MailTemplateKey,
) *store.ExamSittingMailFanout {
	t.Helper()
	at := model.NowUTC().Add(-2 * time.Hour)
	occurrenceID := model.NewMailOccurrenceID()
	occurrence := &model.MailOccurrence{ID: occurrenceID, Kind: model.MailOccurrenceSittingSchedule,
		TemplateKey: templateKey, ActorUserID: actorID, CreatedAt: at}
	bundle := &model.MailFanoutBundle{ID: occurrenceID,
		EncryptedPayload: []byte(`{"key_id":"11111111111111111111111111111111","version":1,"nonce":"n","ciphertext":"c"}`),
		CreatedAt:        at, Revision: 1}
	command, err := model.EncodeSittingMailExpansionCommand(model.SittingMailExpansionCommandV1{OccurrenceID: occurrenceID})
	require.NoError(t, err)
	dedupe, err := model.SittingMailExpansionDedupeKey(occurrenceID)
	require.NoError(t, err)
	job, err := model.NewJobWithDedupePolicy(model.NewJobID(), model.JobTypeMailExpandSitting, 1, command, dedupe,
		model.JobDedupePermanent, at, at, model.MailMaximumAttempts)
	require.NoError(t, err)
	return &store.ExamSittingMailFanout{Occurrence: occurrence, Bundle: bundle, ExpansionJob: job,
		ChangeKind: change, DeliveryLifetime: 72 * time.Hour}
}

func newDisabledExamSittingMailFanout(t *testing.T, actorID model.UserID, change store.ExamSittingMailChangeKind,
	templateKey model.MailTemplateKey,
) *store.ExamSittingMailFanout {
	t.Helper()
	prepared := newExamSittingMailFanout(t, actorID, change, templateKey)
	canceled, err := prepared.ExpansionJob.RequestCancellation(prepared.ExpansionJob.CreatedAt)
	require.NoError(t, err)
	prepared.Bundle = nil
	prepared.ExpansionJob = canceled
	return prepared
}

func newExamSittingMailDelivery(t *testing.T, fanout *store.ExamSittingMailFanoutSnapshot, user *model.User,
	templateKey model.MailTemplateKey,
) (*model.MailDelivery, *model.Job) {
	t.Helper()
	deliveryID, jobID := model.NewMailDeliveryID(), model.NewJobID()
	command, err := model.EncodeMailDeliveryCommand(model.MailDeliveryCommandV1{DeliveryID: deliveryID})
	require.NoError(t, err)
	job, err := model.NewJob(jobID, model.JobTypeMailDeliver, 1, command, deliveryID.String(),
		fanout.Occurrence.CreatedAt, fanout.Occurrence.CreatedAt, model.MailMaximumAttempts)
	require.NoError(t, err)
	delivery := &model.MailDelivery{ID: deliveryID, OccurrenceID: fanout.Occurrence.ID, JobID: jobID,
		TargetUserID: user.ID, TemplateKey: templateKey, TemplateDigest: strings.Repeat("a", 64),
		MaskedRecipient: "***@example.edu", State: model.MailDeliveryQueued,
		CreatedAt: fanout.Occurrence.CreatedAt, UpdatedAt: fanout.Occurrence.CreatedAt, MessageDate: fanout.Occurrence.CreatedAt,
		Deadline: fanout.Deadline, MessageID: "<" + deliveryID.String() + "@example.edu>",
		EncryptedPayload: []byte(`{"key_id":"11111111111111111111111111111111","version":1,"nonce":"n","ciphertext":"c"}`), Revision: 1}
	require.NoError(t, delivery.Validate())
	return delivery, job
}

func newExamSittingFinalizeJob(t *testing.T, sittingID model.ExamSittingID, revision int64, availableAt time.Time) *model.Job {
	t.Helper()
	command, err := model.EncodeExamSittingLifecycleCommand(model.ExamSittingLifecycleCommandV1{ExamSittingID: sittingID})
	require.NoError(t, err)
	key, err := model.ExamSittingLifecycleDedupeKey(sittingID, model.ExamSittingLifecycleJobFinalize, revision)
	require.NoError(t, err)
	job, err := model.NewJobWithDedupePolicy(model.NewJobID(), model.JobTypeExamSittingSealing, 1, command, key,
		model.JobDedupeActive, model.NowUTC(), availableAt, 8)
	require.NoError(t, err)
	return job
}
