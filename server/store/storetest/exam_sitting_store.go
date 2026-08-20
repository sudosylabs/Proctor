// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestExamSittingStore(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture := newExamSittingFixture(t, ctx, ss)
	start := fixture.period.StartsAt.Add(time.Hour)
	end := start.Add(2 * time.Hour)
	sitting, err := model.NewExamSitting(model.NewExamSittingID(), fixture.examID, fixture.revisionID, fixture.class.ID, start, end, model.NowUTC())
	requireNoError(t, err)
	openJob, deadlineJob := newExamSittingLifecycleJobs(t, sitting.ID, 1, start, end)
	scheduleMail := newExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailScheduled, model.MailTemplateExamSittingScheduled)
	scheduleAudit := saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID)
	command := examCommand(fixture.actor.ID, "exam.sitting.schedule.v1", "sitting-schedule", "sitting-schedule-command")
	created, err := ss.ExamSitting().Schedule(ctx, &store.ExamSittingSchedule{Sitting: sitting, OpenJob: openJob, DeadlineJob: deadlineJob, ActorUserID: fixture.actor.ID,
		AuditEventID: scheduleAudit.ID.String(), AuditAt: model.GetMillis(), Mail: scheduleMail}, command)
	requireNoError(t, err)
	if created.Replayed || created.Value == nil || created.Value.Sitting == nil || created.Value.Sitting.ID != sitting.ID || created.Value.Sitting.Revision != 1 {
		t.Fatalf("Schedule()=%#v", created)
	}
	fanout, err := ss.ExamSitting().GetMailFanout(ctx, scheduleMail.Occurrence.ID)
	requireNoError(t, err)
	if fanout.SittingID != sitting.ID || fanout.SittingRevision != 1 || fanout.ChangeKind != store.ExamSittingMailScheduled ||
		fanout.Bundle == nil || fanout.Bundle.ID != scheduleMail.Occurrence.ID || fanout.CompletedAt.Valid ||
		!fanout.Deadline.Equal(fanout.Occurrence.CreatedAt.Add(72*time.Hour)) {
		t.Fatalf("GetMailFanout()=%#v", fanout)
	}
	if queued, getErr := ss.Job().Get(ctx, scheduleMail.ExpansionJob.ID); getErr != nil || queued.Type != model.JobTypeMailExpandSitting {
		t.Fatalf("expansion Job=(%#v,%v)", queued, getErr)
	}
	candidateInput := newUser()
	candidateInput.EmailVerified = true
	candidate, err := createUser(t, ctx, ss, candidateInput)
	requireNoError(t, err)
	_, err = ss.Affiliation().Save(ctx, &model.Affiliation{UserID: candidate.ID, Kind: model.AffiliationStudent,
		StartsAt: fixture.period.StartsAt})
	requireNoError(t, err)
	membership := &model.ClassMember{ClassID: fixture.class.ID, UserID: candidate.ID,
		AcademicPeriodID: fixture.class.AcademicPeriodID, StartsAt: fixture.period.StartsAt}
	membership.PrepareCreate(model.NewClassMemberID(), model.NowUTC())
	membershipAudit := saveClassMemberAuditAttempt(t, ctx, ss, fixture.class.ID.String())
	_, err = ss.ClassMember().EnrollWithAudit(ctx, &store.ClassMemberEnrollment{Member: membership,
		AuditEventID: membershipAudit.ID.String(), AuditAt: model.GetMillis()})
	requireNoError(t, err)
	mailPage, err := ss.ExamSitting().ListMailRecipients(ctx, store.ExamSittingMailRecipientPageRequest{
		OccurrenceID: scheduleMail.Occurrence.ID, Limit: model.SittingMailExpansionPageSize})
	requireNoError(t, err)
	if len(mailPage.Recipients) != 1 || mailPage.Recipients[0].User.ID != candidate.ID ||
		mailPage.Recipients[0].TemplateKey != model.MailTemplateExamSittingScheduled {
		t.Fatalf("ListMailRecipients()=%#v", mailPage)
	}
	delivery, deliveryJob := newExamSittingMailDelivery(t, fanout, candidate, mailPage.Recipients[0].TemplateKey)
	committed, err := ss.ExamSitting().CommitMailRecipient(ctx, &store.ExamSittingMailRecipientCommit{
		OccurrenceID: fanout.Occurrence.ID, SittingRevision: fanout.SittingRevision, Recipient: candidate,
		Delivery: delivery, DeliveryJob: deliveryJob})
	requireNoError(t, err)
	if !committed.Inserted || committed.Delivery == nil || committed.Delivery.ID != delivery.ID {
		t.Fatalf("CommitMailRecipient()=%#v", committed)
	}
	sending, err := ss.Mail().StartDelivery(ctx, delivery.ID, delivery.Revision, model.NowUTC())
	requireNoError(t, err)
	accepted, err := ss.Mail().CompleteDelivery(ctx, &store.MailDeliveryCompletion{DeliveryID: delivery.ID,
		ExpectedRevision: sending.Revision, Kind: store.MailDeliveryCompletionAccepted, At: model.NowUTC()})
	requireNoError(t, err)
	if accepted.State != model.MailDeliveryAccepted {
		t.Fatalf("accepted delivery=%#v", accepted)
	}
	mailPage, err = ss.ExamSitting().ListMailRecipients(ctx, store.ExamSittingMailRecipientPageRequest{
		OccurrenceID: scheduleMail.Occurrence.ID, Limit: model.SittingMailExpansionPageSize})
	requireNoError(t, err)
	if len(mailPage.Recipients) != 1 || mailPage.Recipients[0].TemplateKey != "" {
		t.Fatalf("communicated recipient page=%#v", mailPage)
	}
	completedFanout, err := ss.ExamSitting().CompleteMailExpansion(ctx,
		&store.ExamSittingMailExpansionCompletion{OccurrenceID: fanout.Occurrence.ID})
	requireNoError(t, err)
	if !completedFanout.CompletedAt.Valid || completedFanout.Bundle != nil {
		t.Fatalf("CompleteMailExpansion()=%#v", completedFanout)
	}
	lateInput := newUser()
	lateInput.EmailVerified = true
	lateCandidate, err := createUser(t, ctx, ss, lateInput)
	requireNoError(t, err)
	_, err = ss.Affiliation().Save(ctx, &model.Affiliation{UserID: lateCandidate.ID, Kind: model.AffiliationStudent,
		StartsAt: fixture.period.StartsAt})
	requireNoError(t, err)
	lateMembership := &model.ClassMember{ClassID: fixture.class.ID, UserID: lateCandidate.ID,
		AcademicPeriodID: fixture.class.AcademicPeriodID, StartsAt: fixture.period.StartsAt}
	lateMembership.PrepareCreate(model.NewClassMemberID(), model.NowUTC())
	lateAudit := saveClassMemberAuditAttempt(t, ctx, ss, fixture.class.ID.String())
	_, err = ss.ClassMember().EnrollWithAudit(ctx, &store.ClassMemberEnrollment{Member: lateMembership,
		AuditEventID: lateAudit.ID.String(), AuditAt: model.GetMillis()})
	requireNoError(t, err)
	due, err := ss.ExamSitting().ListMailReconciliationDue(ctx,
		store.ExamSittingMailReconciliationOptions{Limit: model.SittingMailExpansionPageSize})
	requireNoError(t, err)
	var dueSitting *store.ExamSittingMailReconciliationCandidate
	for index := range due {
		if due[index].Sitting.ID == sitting.ID {
			dueSitting = &due[index]
		}
	}
	if dueSitting == nil || dueSitting.ActorUserID != fixture.actor.ID {
		t.Fatalf("ListMailReconciliationDue()=%#v", due)
	}
	reconciliationMail := newExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailReconciled,
		model.MailTemplateExamSittingScheduled)
	reconciled, err := ss.ExamSitting().ReconcileMail(ctx, &store.ExamSittingMailReconciliation{
		SittingID: sitting.ID, ExpectedRevision: sitting.Revision, ActorUserID: fixture.actor.ID, Mail: reconciliationMail})
	requireNoError(t, err)
	mailPage, err = ss.ExamSitting().ListMailRecipients(ctx, store.ExamSittingMailRecipientPageRequest{
		OccurrenceID: reconciled.Occurrence.ID, Limit: model.SittingMailExpansionPageSize})
	requireNoError(t, err)
	keys := make(map[model.UserID]model.MailTemplateKey, len(mailPage.Recipients))
	for _, recipient := range mailPage.Recipients {
		keys[recipient.User.ID] = recipient.TemplateKey
	}
	if keys[candidate.ID] != "" || keys[lateCandidate.ID] != model.MailTemplateExamSittingScheduled {
		t.Fatalf("reconciliation recipients=%#v", keys)
	}
	_, err = ss.ExamSitting().CompleteMailExpansion(ctx,
		&store.ExamSittingMailExpansionCompletion{OccurrenceID: reconciled.Occurrence.ID})
	requireNoError(t, err)
	exact, err := ss.ExamSitting().Get(ctx, fixture.examID, sitting.ID)
	requireNoError(t, err)
	if exact.AcademicUnitID != fixture.unitID || exact.Sitting.ExamRevisionID != fixture.revisionID || exact.Sitting.ClassID != fixture.class.ID || exact.Sitting.ScheduledEndAt != end {
		t.Fatalf("Get()=%#v", exact)
	}
	resolved, err := ss.ExamSitting().Resolve(ctx, sitting.ID)
	requireNoError(t, err)
	if resolved.AcademicUnitID != fixture.unitID || resolved.Sitting.ID != sitting.ID || resolved.Sitting.ExamID != fixture.examID {
		t.Fatalf("Resolve()=%#v", resolved)
	}
	if _, err = ss.ExamSitting().Get(ctx, model.NewExamID(), sitting.ID); !store.IsNotFound(err) {
		t.Fatalf("Get(foreign Exam) error=%v", err)
	}

	// A sealed Revision is not transferable between Exams, even when both
	// Exams belong to the same Academic Unit. The Store returns one stable
	// lineage conflict without disclosing the foreign snapshot.
	foreignExam := createCatalogExam(t, ctx, ss, fixture.unitID, fixture.actor.ID, model.NowUTC(), "sitting-foreign-exam")
	foreignPublication := examRevisionPublication(t, ctx, ss, foreignExam.Value.Exam.ID, fixture.actor.ID, fixture.unitID, 1, model.NowUTC())
	foreignRevision, err := ss.ExamRevision().Publish(ctx, foreignPublication,
		examCommand(fixture.actor.ID, "exam.revision.publish.v1", "sitting-foreign-revision", "sitting-foreign-revision-command"))
	requireNoError(t, err)
	foreignSelection, err := model.NewExamSitting(model.NewExamSittingID(), fixture.examID, foreignRevision.Revision.ID,
		fixture.class.ID, start, end, model.NowUTC())
	requireNoError(t, err)
	foreignOpen, foreignDeadline := newExamSittingLifecycleJobs(t, foreignSelection.ID, 1, start, end)
	_, err = ss.ExamSitting().Schedule(ctx, &store.ExamSittingSchedule{Sitting: foreignSelection, OpenJob: foreignOpen, DeadlineJob: foreignDeadline, ActorUserID: fixture.actor.ID,
		AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID.String(), AuditAt: model.GetMillis(),
		Mail: newExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailScheduled, model.MailTemplateExamSittingScheduled)},
		examCommand(fixture.actor.ID, "exam.sitting.schedule.v1", "sitting-foreign-selection", "sitting-foreign-selection-command"))
	requireExamSittingConflict(t, err, "exam_sitting_revision_lineage")

	newStart, newEnd := start.Add(time.Hour), end.Add(time.Hour)
	updatedOpen, updatedDeadline := newExamSittingLifecycleJobs(t, sitting.ID, 2, newStart, newEnd)
	updateAudit := saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID)
	updated, err := ss.ExamSitting().UpdateSchedule(ctx, &store.ExamSittingScheduleUpdate{ExamID: fixture.examID, SittingID: sitting.ID,
		ActorUserID: fixture.actor.ID, ExpectedRevision: 1, ExamRevisionID: fixture.revisionID, ClassID: fixture.class.ID,
		ScheduledStartAt: newStart, ScheduledEndAt: newEnd, OpenJob: updatedOpen, DeadlineJob: updatedDeadline,
		ChangedAt: model.NowUTC(), AuditEventID: updateAudit.ID.String(), AuditAt: model.GetMillis(),
		Mail: newExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailRescheduled, model.MailTemplateExamSittingRescheduled)},
		examCommand(fixture.actor.ID, "exam.sitting.schedule.update.v1", "sitting-update", "sitting-update-command"))
	requireNoError(t, err)
	if updated.Value.Sitting.Revision != 2 || updated.Value.Sitting.ScheduledStartAt != newStart || updated.Value.Sitting.ScheduledEndAt != newEnd {
		t.Fatalf("UpdateSchedule()=%#v", updated)
	}

	cancelAudit := saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID)
	cancelCommand := examCommand(fixture.actor.ID, "exam.sitting.cancel.v1", "sitting-cancel", "sitting-cancel-command")
	canceled, err := ss.ExamSitting().Cancel(ctx, &store.ExamSittingCancellation{ExamID: fixture.examID, SittingID: sitting.ID,
		ActorUserID: fixture.actor.ID, ExpectedRevision: 2, PrivateReason: "Room unavailable", CanceledAt: model.NowUTC(),
		AuditEventID: cancelAudit.ID.String(), AuditAt: model.GetMillis(),
		Mail: newExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailCancelled, model.MailTemplateExamSittingCancelled)}, cancelCommand)
	requireNoError(t, err)
	if canceled.Value.Sitting.State != model.ExamSittingCanceled || canceled.Value.Sitting.ReasonCode != model.ExamSittingReasonManagerCanceled || canceled.Value.Sitting.Revision != 3 {
		t.Fatalf("Cancel()=%#v", canceled)
	}
	audit, err := ss.Audit().Get(ctx, cancelAudit.ID.String())
	requireNoError(t, err)
	if bytes.Contains(audit.Result, []byte("Room unavailable")) || bytes.Contains(audit.Parameters, []byte("Room unavailable")) {
		t.Fatalf("private cancellation reason leaked into ordinary audit: %#v", audit)
	}

	// The committed schedule result wins before the now-canceled state and
	// stale relationship/revision fences.
	retry, err := ss.ExamSitting().Schedule(ctx, &store.ExamSittingSchedule{Sitting: sitting, OpenJob: openJob, DeadlineJob: deadlineJob, ActorUserID: fixture.actor.ID,
		AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID.String(), AuditAt: model.GetMillis(), Mail: scheduleMail}, command)
	requireNoError(t, err)
	if !retry.Replayed || retry.Value.Sitting.State != model.ExamSittingScheduled || retry.Value.Sitting.Revision != 1 {
		t.Fatalf("Schedule(replay)=%#v", retry)
	}

	staleAudit := saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID)
	_, err = ss.ExamSitting().Cancel(ctx, &store.ExamSittingCancellation{ExamID: fixture.examID, SittingID: sitting.ID, ActorUserID: fixture.actor.ID,
		ExpectedRevision: 2, PrivateReason: "stale", CanceledAt: model.NowUTC(), AuditEventID: staleAudit.ID.String(), AuditAt: model.GetMillis(),
		Mail: newExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailCancelled, model.MailTemplateExamSittingCancelled)},
		examCommand(fixture.actor.ID, "exam.sitting.cancel.v1", "sitting-cancel-stale", "sitting-cancel-stale-command"))
	requireExamSittingConflict(t, err, "exam_sitting_revision")

	// A second Sitting makes list filtering and tuple pagination observable.
	secondStart := newStart.Add(24 * time.Hour)
	second, err := model.NewExamSitting(model.NewExamSittingID(), fixture.examID, fixture.revisionID, fixture.class.ID, secondStart, secondStart.Add(time.Hour), model.NowUTC())
	requireNoError(t, err)
	secondOpen, secondDeadline := newExamSittingLifecycleJobs(t, second.ID, 1, second.ScheduledStartAt, second.ScheduledEndAt)
	secondResult, err := ss.ExamSitting().Schedule(ctx, &store.ExamSittingSchedule{Sitting: second, OpenJob: secondOpen, DeadlineJob: secondDeadline, ActorUserID: fixture.actor.ID,
		AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID.String(), AuditAt: model.GetMillis(),
		Mail: newExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailScheduled, model.MailTemplateExamSittingScheduled)},
		examCommand(fixture.actor.ID, "exam.sitting.schedule.v1", "sitting-second", "sitting-second-command"))
	requireNoError(t, err)
	page, err := ss.ExamSitting().List(ctx, store.ExamSittingListOptions{ExamID: fixture.examID, ClassID: fixture.class.ID,
		States: []model.ExamSittingState{model.ExamSittingScheduled}, OverlapStartAt: secondStart.Add(-time.Minute), OverlapEndAt: secondStart.Add(30 * time.Minute), Limit: 1})
	requireNoError(t, err)
	if len(page) != 1 || page[0].Sitting.ID != secondResult.Value.Sitting.ID {
		t.Fatalf("filtered List()=%#v", page)
	}
	page, err = ss.ExamSitting().List(ctx, store.ExamSittingListOptions{ExamID: fixture.examID,
		BeforeScheduledStartAt: page[0].Sitting.ScheduledStartAt, BeforeSittingID: page[0].Sitting.ID, Limit: 201})
	requireNoError(t, err)
	if len(page) != 1 || page[0].Sitting.ID != sitting.ID {
		t.Fatalf("cursor List()=%#v", page)
	}
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
	requireNoError(t, err)
	openJob, deadlineJob := newExamSittingLifecycleJobs(t, sitting.ID, 1, start, end)
	prepared := newExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailScheduled,
		model.MailTemplateExamSittingScheduled)
	created, err := ss.ExamSitting().Schedule(ctx, &store.ExamSittingSchedule{Sitting: sitting,
		OpenJob: openJob, DeadlineJob: deadlineJob, ActorUserID: fixture.actor.ID,
		AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID.String(),
		AuditAt:      model.GetMillis(), Mail: prepared},
		examCommand(fixture.actor.ID, "exam.sitting.schedule.v1", "sitting-membership-reconcile", "sitting-membership-reconcile-command"))
	requireNoError(t, err)

	candidateInput := newUser()
	candidateInput.EmailVerified = true
	candidate, err := createUser(t, ctx, ss, candidateInput)
	requireNoError(t, err)
	_, err = ss.Affiliation().Save(ctx, &model.Affiliation{UserID: candidate.ID, Kind: model.AffiliationStudent,
		StartsAt: fixture.period.StartsAt})
	requireNoError(t, err)
	membership := &model.ClassMember{ClassID: fixture.class.ID, UserID: candidate.ID,
		AcademicPeriodID: fixture.class.AcademicPeriodID, StartsAt: fixture.period.StartsAt}
	membership.PrepareCreate(model.NewClassMemberID(), model.NowUTC())
	enrolled, err := ss.ClassMember().EnrollWithAudit(ctx, &store.ClassMemberEnrollment{Member: membership,
		AuditEventID: saveClassMemberAuditAttempt(t, ctx, ss, fixture.class.ID.String()).ID.String(), AuditAt: model.GetMillis()})
	requireNoError(t, err)

	fanout, err := ss.ExamSitting().GetMailFanout(ctx, prepared.Occurrence.ID)
	requireNoError(t, err)
	page, err := ss.ExamSitting().ListMailRecipients(ctx, store.ExamSittingMailRecipientPageRequest{
		OccurrenceID: prepared.Occurrence.ID, Limit: model.SittingMailExpansionPageSize})
	requireNoError(t, err)
	if len(page.Recipients) != 1 || page.Recipients[0].User.ID != candidate.ID {
		t.Fatalf("initial recipients=%#v", page.Recipients)
	}
	delivery, deliveryJob := newExamSittingMailDelivery(t, fanout, candidate, page.Recipients[0].TemplateKey)
	_, err = ss.ExamSitting().CommitMailRecipient(ctx, &store.ExamSittingMailRecipientCommit{
		OccurrenceID: fanout.Occurrence.ID, SittingRevision: fanout.SittingRevision, Recipient: candidate,
		Delivery: delivery, DeliveryJob: deliveryJob})
	requireNoError(t, err)
	_, err = ss.ExamSitting().CompleteMailExpansion(ctx,
		&store.ExamSittingMailExpansionCompletion{OccurrenceID: fanout.Occurrence.ID})
	requireNoError(t, err)

	changeAt := start.Add(-time.Hour)
	_, err = ss.ClassMember().EndWithAudit(ctx, &store.ClassMemberEnd{ID: enrolled.Membership.ID.String(),
		ExpectedRevision: enrolled.Membership.Revision, EndAt: model.MillisFromTime(changeAt),
		AuditEventID: saveClassMemberAuditAttempt(t, ctx, ss, fixture.class.ID.String()).ID.String(), AuditAt: model.GetMillis()})
	requireNoError(t, err)
	suppressed, err := ss.Mail().StartDelivery(ctx, delivery.ID, delivery.Revision, model.NowUTC())
	requireNoError(t, err)
	if suppressed.State != model.MailDeliverySuppressed || suppressed.PublicFailureCode != model.MailDeliveryObsoleteCode {
		t.Fatalf("obsolete delivery=%#v", suppressed)
	}

	restored := &model.ClassMember{ClassID: fixture.class.ID, UserID: candidate.ID,
		AcademicPeriodID: fixture.class.AcademicPeriodID, StartsAt: changeAt}
	restored.PrepareCreate(model.NewClassMemberID(), model.NowUTC())
	_, err = ss.ClassMember().EnrollWithAudit(ctx, &store.ClassMemberEnrollment{Member: restored,
		AuditEventID: saveClassMemberAuditAttempt(t, ctx, ss, fixture.class.ID.String()).ID.String(), AuditAt: model.GetMillis()})
	requireNoError(t, err)
	due, err := ss.ExamSitting().ListMailReconciliationDue(ctx,
		store.ExamSittingMailReconciliationOptions{Limit: model.SittingMailExpansionPageSize})
	requireNoError(t, err)
	for _, item := range due {
		if item.Sitting.ID == created.Value.Sitting.ID {
			return
		}
	}
	t.Fatalf("restored candidate did not make Sitting due for reconciliation: %#v", due)
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
	role, err := ss.Role().Save(ctx, &model.Role{Name: "sitting-inviter-" + model.NewId(), DisplayName: "Sitting inviter",
		Permissions: []string{string(model.ActionInvitationCreate), string(model.ActionClassMembersManage)}})
	requireNoError(t, err)
	_, err = ss.RoleBinding().Save(ctx, &model.RoleBinding{UserID: fixture.actor.ID, RoleID: role.ID,
		ScopeType: model.RoleScopeClass, ScopeID: fixture.class.ID.String(), StartsAt: model.NowUTC().Add(-time.Hour)})
	requireNoError(t, err)
	issue := studentClassInvitationIssueFixture(t, ss, fixture.actor, fixture.class, fixture.period, model.NowUTC())
	issue.Invitation.TargetEmail = fixture.actor.Email
	invitation, err := ss.Invitation().IssueStudentClass(ctx, issue)
	requireNoError(t, err)
	acceptance := studentClassInvitationAcceptanceFixture(t, invitation, model.NowUTC())

	start := fixture.period.StartsAt.Add(20 * time.Hour)
	sitting, err := model.NewExamSitting(model.NewExamSittingID(), fixture.examID, fixture.revisionID,
		fixture.class.ID, start, start.Add(2*time.Hour), model.NowUTC())
	requireNoError(t, err)
	openJob, deadlineJob := newExamSittingLifecycleJobs(t, sitting.ID, 1, sitting.ScheduledStartAt, sitting.ScheduledEndAt)
	initialMail := newExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailScheduled,
		model.MailTemplateExamSittingScheduled)
	_, err = ss.ExamSitting().Schedule(ctx, &store.ExamSittingSchedule{Sitting: sitting, OpenJob: openJob,
		DeadlineJob: deadlineJob, ActorUserID: fixture.actor.ID,
		AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID.String(),
		AuditAt:      model.GetMillis(), Mail: initialMail},
		examCommand(fixture.actor.ID, "exam.sitting.schedule.v1", "sitting-invitation-lock-order", "sitting-invitation-lock-order-command"))
	requireNoError(t, err)
	_, err = ss.ExamSitting().CompleteMailExpansion(ctx,
		&store.ExamSittingMailExpansionCompletion{OccurrenceID: initialMail.Occurrence.ID})
	requireNoError(t, err)
	lateInput := newUser()
	lateInput.EmailVerified = true
	late, err := createUser(t, ctx, ss, lateInput)
	requireNoError(t, err)
	_, err = ss.Affiliation().Save(ctx, &model.Affiliation{UserID: late.ID, Kind: model.AffiliationStudent,
		StartsAt: fixture.period.StartsAt})
	requireNoError(t, err)
	membership := &model.ClassMember{ClassID: fixture.class.ID, UserID: late.ID,
		AcademicPeriodID: fixture.class.AcademicPeriodID, StartsAt: fixture.period.StartsAt}
	membership.PrepareCreate(model.NewClassMemberID(), model.NowUTC())
	_, err = ss.ClassMember().EnrollWithAudit(ctx, &store.ClassMemberEnrollment{Member: membership,
		AuditEventID: saveClassMemberAuditAttempt(t, ctx, ss, fixture.class.ID.String()).ID.String(), AuditAt: model.GetMillis()})
	requireNoError(t, err)
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
	requireNoError(t, err)
	_, err = ss.Affiliation().Save(ctx, &model.Affiliation{UserID: candidate.ID, Kind: model.AffiliationStudent,
		StartsAt: fixture.period.StartsAt})
	requireNoError(t, err)
	membership := &model.ClassMember{ClassID: fixture.class.ID, UserID: candidate.ID,
		AcademicPeriodID: fixture.class.AcademicPeriodID, StartsAt: fixture.period.StartsAt}
	membership.PrepareCreate(model.NewClassMemberID(), model.NowUTC())
	_, err = ss.ClassMember().EnrollWithAudit(ctx, &store.ClassMemberEnrollment{Member: membership,
		AuditEventID: saveClassMemberAuditAttempt(t, ctx, ss, fixture.class.ID.String()).ID.String(), AuditAt: model.GetMillis()})
	requireNoError(t, err)
	start := fixture.period.StartsAt.Add(18 * time.Hour)
	sitting, err := model.NewExamSitting(model.NewExamSittingID(), fixture.examID, fixture.revisionID,
		fixture.class.ID, start, start.Add(2*time.Hour), model.NowUTC())
	requireNoError(t, err)
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
			AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID.String(),
			AuditAt:      model.GetMillis(), Mail: mail},
		Command: examCommand(fixture.actor.ID, "exam.sitting.schedule.v1", key, key+"-command"),
		Verification: &store.PrivilegedEmailVerification{UserID: candidate.ID, ExpectedRevision: candidate.Revision,
			Occurrence: occurrence, Delivery: delivery, Job: job,
			AuditEventID: saveUserProfileAuditAttempt(t, ctx, ss, candidate.ID.String()).ID.String(),
			AuditAt:      model.MillisFromTime(verificationAt)},
	}
}

func TestExamSittingDisabledMailReconciliationConverges(t *testing.T, ss store.Store, probe ExamSittingDisabledMailSQLProbe) {
	if probe.AgeTerminalFanout == nil {
		t.Fatal("disabled Sitting mail retention SQL probe is required")
	}
	ctx := context.Background()
	fixture := newExamSittingFixture(t, ctx, ss)
	candidateInput := newUser()
	candidateInput.EmailVerified = true
	candidate, err := createUser(t, ctx, ss, candidateInput)
	requireNoError(t, err)
	_, err = ss.Affiliation().Save(ctx, &model.Affiliation{UserID: candidate.ID, Kind: model.AffiliationStudent,
		StartsAt: fixture.period.StartsAt})
	requireNoError(t, err)
	membership := &model.ClassMember{ClassID: fixture.class.ID, UserID: candidate.ID,
		AcademicPeriodID: fixture.class.AcademicPeriodID, StartsAt: fixture.period.StartsAt}
	membership.PrepareCreate(model.NewClassMemberID(), model.NowUTC())
	_, err = ss.ClassMember().EnrollWithAudit(ctx, &store.ClassMemberEnrollment{Member: membership,
		AuditEventID: saveClassMemberAuditAttempt(t, ctx, ss, fixture.class.ID.String()).ID.String(), AuditAt: model.GetMillis()})
	requireNoError(t, err)
	prepareCandidate := func(emailVerified bool) (*model.User, *model.ClassMember) {
		input := newUser()
		input.EmailVerified = emailVerified
		user, createErr := createUser(t, ctx, ss, input)
		requireNoError(t, createErr)
		_, createErr = ss.Affiliation().Save(ctx, &model.Affiliation{UserID: user.ID, Kind: model.AffiliationStudent,
			StartsAt: fixture.period.StartsAt})
		requireNoError(t, createErr)
		member := &model.ClassMember{ClassID: fixture.class.ID, UserID: user.ID,
			AcademicPeriodID: fixture.class.AcademicPeriodID, StartsAt: fixture.period.StartsAt}
		member.PrepareCreate(model.NewClassMemberID(), model.NowUTC())
		enrolled, createErr := ss.ClassMember().EnrollWithAudit(ctx, &store.ClassMemberEnrollment{Member: member,
			AuditEventID: saveClassMemberAuditAttempt(t, ctx, ss, fixture.class.ID.String()).ID.String(), AuditAt: model.GetMillis()})
		requireNoError(t, createErr)
		return user, enrolled.Membership
	}
	unverifiedCandidate, _ := prepareCandidate(false)
	disabledCandidate, _ := prepareCandidate(true)
	disableAt := model.GetMillis() + 1
	disabledResult, err := ss.User().SetDisabledWithAudit(ctx, userDisabledStateChangeWithNotice(t, &store.UserDisabledStateChange{
		ID: disabledCandidate.ID.String(), ExpectedRevision: disabledCandidate.Revision, Disabled: true,
		ChangedAt: disableAt, RevocationReason: "account disabled", AuditEventID: saveUserProfileAuditAttempt(t, ctx, ss, disabledCandidate.ID.String()).ID.String(), AuditAt: disableAt,
	}))
	requireNoError(t, err)

	start := fixture.period.StartsAt.Add(5 * time.Hour)
	sitting, err := model.NewExamSitting(model.NewExamSittingID(), fixture.examID, fixture.revisionID,
		fixture.class.ID, start, start.Add(2*time.Hour), model.NowUTC())
	requireNoError(t, err)
	openJob, deadlineJob := newExamSittingLifecycleJobs(t, sitting.ID, 1, sitting.ScheduledStartAt, sitting.ScheduledEndAt)
	disabledMail := newDisabledExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailScheduled,
		model.MailTemplateExamSittingScheduled)
	created, err := ss.ExamSitting().Schedule(ctx, &store.ExamSittingSchedule{Sitting: sitting,
		OpenJob: openJob, DeadlineJob: deadlineJob, ActorUserID: fixture.actor.ID,
		AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID.String(),
		AuditAt:      model.GetMillis(), Mail: disabledMail},
		examCommand(fixture.actor.ID, "exam.sitting.schedule.v1", "sitting-disabled-mail", "sitting-disabled-mail-command"))
	requireNoError(t, err)
	terminal, err := ss.ExamSitting().GetMailFanout(ctx, disabledMail.Occurrence.ID)
	requireNoError(t, err)
	if terminal.Bundle != nil || !terminal.CompletedAt.Valid {
		t.Fatalf("disabled Sitting fan-out=%#v", terminal)
	}

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
		if !store.IsConflict(reconcileErr) {
			t.Fatalf("node %d ReconcileMail(disabled revision) error=%v", index, reconcileErr)
		}
		if _, getErr := ss.ExamSitting().GetMailFanout(ctx, prepared[index].Occurrence.ID); !store.IsNotFound(getErr) {
			t.Fatalf("node %d duplicate fan-out error=%v", index, getErr)
		}
		if _, getErr := ss.Job().Get(ctx, prepared[index].ExpansionJob.ID); !store.IsNotFound(getErr) {
			t.Fatalf("node %d duplicate expansion Job error=%v", index, getErr)
		}
	}
	for pass := 0; pass < 2; pass++ {
		due, listErr := ss.ExamSitting().ListMailReconciliationDue(ctx,
			store.ExamSittingMailReconciliationOptions{Limit: model.SittingMailExpansionPageSize})
		requireNoError(t, listErr)
		for _, item := range due {
			if item.Sitting.ID == sitting.ID {
				t.Fatalf("pass %d returned terminally suppressed Sitting as due: %#v", pass, due)
			}
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
		AuditEventID: saveUserProfileAuditAttempt(t, ctx, ss, unverifiedCandidate.ID.String()).ID.String(),
		AuditAt:      model.MillisFromTime(verificationAt),
	})
	requireNoError(t, err)
	enableAt := disableAt + 1
	enabledResult, err := ss.User().SetDisabledWithAudit(ctx, userDisabledStateChangeWithNotice(t, &store.UserDisabledStateChange{
		ID: disabledCandidate.ID.String(), ExpectedRevision: disabledResult.User.Revision, Disabled: false,
		ChangedAt: enableAt, AuditEventID: saveUserProfileAuditAttempt(t, ctx, ss, disabledCandidate.ID.String()).ID.String(), AuditAt: enableAt,
	}))
	requireNoError(t, err)
	due, err := ss.ExamSitting().ListMailReconciliationDue(ctx,
		store.ExamSittingMailReconciliationOptions{Limit: model.SittingMailExpansionPageSize})
	requireNoError(t, err)
	requireSittingMailReconciliationDue(t, due, sitting.ID)
	eligibilityMail := newExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailReconciled,
		model.MailTemplateExamSittingScheduled)
	eligibilityFanout, err := ss.ExamSitting().ReconcileMail(ctx, &store.ExamSittingMailReconciliation{
		SittingID: sitting.ID, ExpectedRevision: sitting.Revision, ActorUserID: fixture.actor.ID, Mail: eligibilityMail,
	})
	requireNoError(t, err)
	eligibilityPage, err := ss.ExamSitting().ListMailRecipients(ctx, store.ExamSittingMailRecipientPageRequest{
		OccurrenceID: eligibilityFanout.Occurrence.ID, Limit: model.SittingMailExpansionPageSize})
	requireNoError(t, err)
	wantEligibility := map[model.UserID]model.MailTemplateKey{
		candidate.ID:          "",
		verifiedCandidate.ID:  model.MailTemplateExamSittingScheduled,
		enabledResult.User.ID: model.MailTemplateExamSittingScheduled,
	}
	for _, recipient := range eligibilityPage.Recipients {
		if want, ok := wantEligibility[recipient.User.ID]; ok && recipient.TemplateKey != want {
			t.Fatalf("eligibility recipient %s template=%q want=%q", recipient.User.ID, recipient.TemplateKey, want)
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
		if !found {
			t.Fatalf("eligible recipient %s missing from page: %#v", userID, eligibilityPage.Recipients)
		}
	}
	for _, recipient := range eligibilityPage.Recipients {
		if recipient.TemplateKey == "" {
			continue
		}
		delivery, job := newExamSittingMailDelivery(t, eligibilityFanout, recipient.User, recipient.TemplateKey)
		_, err = ss.ExamSitting().CommitMailRecipient(ctx, &store.ExamSittingMailRecipientCommit{
			OccurrenceID: eligibilityFanout.Occurrence.ID, SittingRevision: eligibilityFanout.SittingRevision,
			Recipient: recipient.User, Delivery: delivery, DeliveryJob: job})
		requireNoError(t, err)
		sending, startErr := ss.Mail().StartDelivery(ctx, delivery.ID, delivery.Revision, model.NowUTC())
		requireNoError(t, startErr)
		_, err = ss.Mail().CompleteDelivery(ctx, &store.MailDeliveryCompletion{DeliveryID: delivery.ID,
			ExpectedRevision: sending.Revision, Kind: store.MailDeliveryCompletionAccepted, At: model.NowUTC()})
		requireNoError(t, err)
	}
	_, err = ss.ExamSitting().CompleteMailExpansion(ctx,
		&store.ExamSittingMailExpansionCompletion{OccurrenceID: eligibilityFanout.Occurrence.ID})
	requireNoError(t, err)

	// Mail history observes the ordinary 90-day cutoff. Its removal must not
	// erase the durable decision that this Sitting revision was terminally
	// suppressed while mail was disabled.
	probe.AgeTerminalFanout(t, ctx, disabledMail.Occurrence.ID)
	cleaned, err := ss.Mail().CleanupTerminal(ctx, 10)
	requireNoError(t, err)
	if cleaned.Affected != 1 {
		t.Fatalf("disabled Sitting mail cleanup=%#v", cleaned)
	}
	if _, getErr := ss.ExamSitting().GetMailFanout(ctx, disabledMail.Occurrence.ID); !store.IsNotFound(getErr) {
		t.Fatalf("retired disabled fan-out error=%v", getErr)
	}
	afterRetention := newExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailReconciled,
		model.MailTemplateExamSittingScheduled)
	if _, reconcileErr := ss.ExamSitting().ReconcileMail(ctx, &store.ExamSittingMailReconciliation{
		SittingID: created.Value.Sitting.ID, ExpectedRevision: created.Value.Sitting.Revision,
		ActorUserID: fixture.actor.ID, Mail: afterRetention,
	}); !store.IsConflict(reconcileErr) {
		t.Fatalf("ReconcileMail(after disabled history retention) error=%v", reconcileErr)
	}
	due, err = ss.ExamSitting().ListMailReconciliationDue(ctx,
		store.ExamSittingMailReconciliationOptions{Limit: model.SittingMailExpansionPageSize})
	requireNoError(t, err)
	for _, item := range due {
		if item.Sitting.ID == sitting.ID {
			t.Fatalf("retired disabled history resurrected Sitting reconciliation: %#v", due)
		}
	}

	// A later membership mutation is a new audience fact even though the
	// Sitting revision is unchanged. Re-enablement must notify only that new
	// audience, without resurrecting candidates covered by the disabled fact.
	newCandidateInput := newUser()
	newCandidateInput.EmailVerified = true
	newCandidate, err := createUser(t, ctx, ss, newCandidateInput)
	requireNoError(t, err)
	_, err = ss.Affiliation().Save(ctx, &model.Affiliation{UserID: newCandidate.ID, Kind: model.AffiliationStudent,
		StartsAt: fixture.period.StartsAt})
	requireNoError(t, err)
	newMembership := &model.ClassMember{ClassID: fixture.class.ID, UserID: newCandidate.ID,
		AcademicPeriodID: fixture.class.AcademicPeriodID, StartsAt: fixture.period.StartsAt}
	newMembership.PrepareCreate(model.NewClassMemberID(), model.NowUTC())
	enrolled, err := ss.ClassMember().EnrollWithAudit(ctx, &store.ClassMemberEnrollment{Member: newMembership,
		AuditEventID: saveClassMemberAuditAttempt(t, ctx, ss, fixture.class.ID.String()).ID.String(), AuditAt: model.GetMillis()})
	requireNoError(t, err)
	due, err = ss.ExamSitting().ListMailReconciliationDue(ctx,
		store.ExamSittingMailReconciliationOptions{Limit: model.SittingMailExpansionPageSize})
	requireNoError(t, err)
	requireSittingMailReconciliationDue(t, due, sitting.ID)

	enrollmentMail := newExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailReconciled,
		model.MailTemplateExamSittingScheduled)
	enrollmentFanout, err := ss.ExamSitting().ReconcileMail(ctx, &store.ExamSittingMailReconciliation{
		SittingID: sitting.ID, ExpectedRevision: sitting.Revision, ActorUserID: fixture.actor.ID, Mail: enrollmentMail,
	})
	requireNoError(t, err)
	page, err := ss.ExamSitting().ListMailRecipients(ctx, store.ExamSittingMailRecipientPageRequest{
		OccurrenceID: enrollmentFanout.Occurrence.ID, Limit: model.SittingMailExpansionPageSize})
	requireNoError(t, err)
	var newRecipient *store.ExamSittingMailRecipient
	for index := range page.Recipients {
		recipient := &page.Recipients[index]
		switch recipient.User.ID {
		case candidate.ID:
			if recipient.TemplateKey != "" {
				t.Fatalf("pre-disable candidate was resurrected: %#v", recipient)
			}
		case newCandidate.ID:
			if recipient.TemplateKey != model.MailTemplateExamSittingScheduled {
				t.Fatalf("new candidate template=%q", recipient.TemplateKey)
			}
			newRecipient = recipient
		}
	}
	if newRecipient == nil {
		t.Fatalf("new candidate missing from reconciliation page: %#v", page.Recipients)
	}
	delivery, deliveryJob := newExamSittingMailDelivery(t, enrollmentFanout, newRecipient.User, newRecipient.TemplateKey)
	_, err = ss.ExamSitting().CommitMailRecipient(ctx, &store.ExamSittingMailRecipientCommit{
		OccurrenceID: enrollmentFanout.Occurrence.ID, SittingRevision: enrollmentFanout.SittingRevision,
		Recipient: newRecipient.User, Delivery: delivery, DeliveryJob: deliveryJob})
	requireNoError(t, err)
	sending, err := ss.Mail().StartDelivery(ctx, delivery.ID, delivery.Revision, model.NowUTC())
	requireNoError(t, err)
	_, err = ss.Mail().CompleteDelivery(ctx, &store.MailDeliveryCompletion{DeliveryID: sending.ID,
		ExpectedRevision: sending.Revision, Kind: store.MailDeliveryCompletionAccepted, At: model.NowUTC()})
	requireNoError(t, err)
	_, err = ss.ExamSitting().CompleteMailExpansion(ctx,
		&store.ExamSittingMailExpansionCompletion{OccurrenceID: enrollmentFanout.Occurrence.ID})
	requireNoError(t, err)

	// Ending that same membership is another audience fact without a Sitting
	// revision change and must yield the assignment-removed projection.
	_, err = ss.ClassMember().EndWithAudit(ctx, &store.ClassMemberEnd{ID: enrolled.Membership.ID.String(),
		ExpectedRevision: enrolled.Membership.Revision, EndAt: model.MillisFromTime(start.Add(-time.Hour)),
		AuditEventID: saveClassMemberAuditAttempt(t, ctx, ss, fixture.class.ID.String()).ID.String(), AuditAt: model.GetMillis()})
	requireNoError(t, err)
	due, err = ss.ExamSitting().ListMailReconciliationDue(ctx,
		store.ExamSittingMailReconciliationOptions{Limit: model.SittingMailExpansionPageSize})
	requireNoError(t, err)
	requireSittingMailReconciliationDue(t, due, sitting.ID)
	removalMail := newExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailReconciled,
		model.MailTemplateExamSittingScheduled)
	removalFanout, err := ss.ExamSitting().ReconcileMail(ctx, &store.ExamSittingMailReconciliation{
		SittingID: sitting.ID, ExpectedRevision: sitting.Revision, ActorUserID: fixture.actor.ID, Mail: removalMail,
	})
	requireNoError(t, err)
	page, err = ss.ExamSitting().ListMailRecipients(ctx, store.ExamSittingMailRecipientPageRequest{
		OccurrenceID: removalFanout.Occurrence.ID, Limit: model.SittingMailExpansionPageSize})
	requireNoError(t, err)
	var removedRecipient *store.ExamSittingMailRecipient
	for index := range page.Recipients {
		recipient := &page.Recipients[index]
		if recipient.User.ID == newCandidate.ID && recipient.TemplateKey != model.MailTemplateExamSittingAssignmentRemoved {
			t.Fatalf("ended candidate template=%q", recipient.TemplateKey)
		}
		if recipient.User.ID == newCandidate.ID {
			removedRecipient = recipient
		}
		if recipient.User.ID == candidate.ID && recipient.TemplateKey != "" {
			t.Fatalf("pre-disable candidate was resurrected after end: %#v", recipient)
		}
	}
	if removedRecipient == nil {
		t.Fatalf("ended candidate missing from reconciliation page: %#v", page.Recipients)
	}
	removedDelivery, removedJob := newExamSittingMailDelivery(t, removalFanout, removedRecipient.User, removedRecipient.TemplateKey)
	_, err = ss.ExamSitting().CommitMailRecipient(ctx, &store.ExamSittingMailRecipientCommit{
		OccurrenceID: removalFanout.Occurrence.ID, SittingRevision: removalFanout.SittingRevision,
		Recipient: removedRecipient.User, Delivery: removedDelivery, DeliveryJob: removedJob})
	requireNoError(t, err)
	startedRemoval, err := ss.Mail().StartDelivery(ctx, removedDelivery.ID, removedDelivery.Revision, model.NowUTC())
	requireNoError(t, err)
	if startedRemoval.State != model.MailDeliverySending {
		t.Fatalf("assignment-removed delivery=%#v", startedRemoval)
	}
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
	t.Fatalf("Sitting %s is not due for mail reconciliation: %#v", sittingID, candidates)
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
	requireNoError(t, err)
	openJob, deadlineJob := newExamSittingLifecycleJobs(t, sitting.ID, 1, start, end)
	prepared := newExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailScheduled,
		model.MailTemplateExamSittingScheduled)
	created, err := ss.ExamSitting().Schedule(ctx, &store.ExamSittingSchedule{Sitting: sitting,
		OpenJob: openJob, DeadlineJob: deadlineJob, ActorUserID: fixture.actor.ID,
		AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID.String(),
		AuditAt:      model.GetMillis(), Mail: prepared},
		examCommand(fixture.actor.ID, "exam.sitting.schedule.v1", "sitting-mail-maintenance", "sitting-mail-maintenance-command"))
	requireNoError(t, err)

	candidateInput := newUser()
	candidateInput.EmailVerified = true
	candidate, err := createUser(t, ctx, ss, candidateInput)
	requireNoError(t, err)
	_, err = ss.Affiliation().Save(ctx, &model.Affiliation{UserID: candidate.ID, Kind: model.AffiliationStudent,
		StartsAt: fixture.period.StartsAt})
	requireNoError(t, err)
	membership := &model.ClassMember{ClassID: fixture.class.ID, UserID: candidate.ID,
		AcademicPeriodID: fixture.class.AcademicPeriodID, StartsAt: fixture.period.StartsAt}
	membership.PrepareCreate(model.NewClassMemberID(), model.NowUTC())
	_, err = ss.ClassMember().EnrollWithAudit(ctx, &store.ClassMemberEnrollment{Member: membership,
		AuditEventID: saveClassMemberAuditAttempt(t, ctx, ss, fixture.class.ID.String()).ID.String(), AuditAt: model.GetMillis()})
	requireNoError(t, err)

	fanout, err := ss.ExamSitting().GetMailFanout(ctx, prepared.Occurrence.ID)
	requireNoError(t, err)
	page, err := ss.ExamSitting().ListMailRecipients(ctx, store.ExamSittingMailRecipientPageRequest{
		OccurrenceID: fanout.Occurrence.ID, Limit: model.SittingMailExpansionPageSize})
	requireNoError(t, err)
	delivery, deliveryJob := newExamSittingMailDelivery(t, fanout, candidate, page.Recipients[0].TemplateKey)
	_, err = ss.ExamSitting().CommitMailRecipient(ctx, &store.ExamSittingMailRecipientCommit{
		OccurrenceID: fanout.Occurrence.ID, SittingRevision: fanout.SittingRevision, Recipient: candidate,
		Delivery: delivery, DeliveryJob: deliveryJob})
	requireNoError(t, err)

	token, err := model.NewJobClaimToken()
	requireNoError(t, err)
	claim, err := ss.Job().ClaimNext(ctx, &store.JobClaimRequest{Types: []model.JobType{model.JobTypeMailExpandSitting},
		NodeID: "sitting-mail-maintenance", ClaimToken: token, LeaseDuration: time.Minute})
	requireNoError(t, err)
	if claim.Job.ID != prepared.ExpansionJob.ID {
		t.Fatalf("claimed expansion Job=%s want=%s", claim.Job.ID, prepared.ExpansionJob.ID)
	}
	failed, err := ss.Job().Complete(ctx, &store.JobCompletion{AttemptID: claim.Attempt.ID,
		ClaimToken: token, Kind: store.JobCompletionPermanentFailure, PublicErrorCode: "mail.sitting.unavailable"})
	requireNoError(t, err)
	if failed.Status != model.JobStatusFailed {
		t.Fatalf("failed expansion Job=%#v", failed)
	}

	first, err := ss.ExamSitting().MaintainMailExpansions(ctx, 1)
	requireNoError(t, err)
	if first.FanoutsTerminalized != 1 || first.DeliveriesSuppressed != 0 || !first.More {
		t.Fatalf("first maintenance=%#v", first)
	}
	terminal, err := ss.ExamSitting().GetMailFanout(ctx, fanout.Occurrence.ID)
	requireNoError(t, err)
	if !terminal.CompletedAt.Valid || terminal.Bundle != nil {
		t.Fatalf("terminal fan-out=%#v", terminal)
	}
	second, err := ss.ExamSitting().MaintainMailExpansions(ctx, 1)
	requireNoError(t, err)
	if second.FanoutsTerminalized != 0 || second.DeliveriesSuppressed != 1 || second.More {
		t.Fatalf("second maintenance=%#v", second)
	}
	suppressed, err := ss.Mail().GetDelivery(ctx, delivery.ID)
	requireNoError(t, err)
	if suppressed.State != model.MailDeliverySuppressed || suppressed.PublicFailureCode != model.MailDeliveryObsoleteCode ||
		len(suppressed.EncryptedPayload) != 0 {
		t.Fatalf("maintained child delivery=%#v", suppressed)
	}
	canceledJob, err := ss.Job().Get(ctx, deliveryJob.ID)
	requireNoError(t, err)
	if canceledJob.Status != model.JobStatusCanceled {
		t.Fatalf("maintained child Job=%#v", canceledJob)
	}
	due, err := ss.ExamSitting().ListMailReconciliationDue(ctx,
		store.ExamSittingMailReconciliationOptions{Limit: model.SittingMailExpansionPageSize})
	requireNoError(t, err)
	for _, item := range due {
		if item.Sitting.ID == created.Value.Sitting.ID {
			return
		}
	}
	t.Fatalf("terminal expansion did not release reconciliation: %#v", due)
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
		requireNoError(t, err)
		openJob, deadlineJob := newExamSittingLifecycleJobs(t, sitting.ID, 1, sitting.ScheduledStartAt, sitting.ScheduledEndAt)
		mail := newExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailScheduled,
			model.MailTemplateExamSittingScheduled)
		result, err := ss.ExamSitting().Schedule(ctx, &store.ExamSittingSchedule{Sitting: sitting,
			OpenJob: openJob, DeadlineJob: deadlineJob, ActorUserID: fixture.actor.ID,
			AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID.String(),
			AuditAt:      model.GetMillis(), Mail: mail},
			examCommand(fixture.actor.ID, "exam.sitting.schedule.v1", key, key+"-command"))
		requireNoError(t, err)
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
	if probe.AgeDeliveries == nil || probe.AssertRetired == nil {
		t.Fatal("complete Sitting mail retention SQL probe is required")
	}
	ctx := context.Background()
	fixture := newExamSittingFixture(t, ctx, ss)
	start := fixture.period.StartsAt.Add(8 * time.Hour)
	end := start.Add(2 * time.Hour)
	sitting, err := model.NewExamSitting(model.NewExamSittingID(), fixture.examID, fixture.revisionID,
		fixture.class.ID, start, end, model.NowUTC())
	requireNoError(t, err)
	openJob, deadlineJob := newExamSittingLifecycleJobs(t, sitting.ID, 1, start, end)
	prepared := newExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailScheduled,
		model.MailTemplateExamSittingScheduled)
	_, err = ss.ExamSitting().Schedule(ctx, &store.ExamSittingSchedule{Sitting: sitting,
		OpenJob: openJob, DeadlineJob: deadlineJob, ActorUserID: fixture.actor.ID,
		AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID.String(),
		AuditAt:      model.GetMillis(), Mail: prepared},
		examCommand(fixture.actor.ID, "exam.sitting.schedule.v1", "sitting-mail-retention", "sitting-mail-retention-command"))
	requireNoError(t, err)

	users := make([]*model.User, 3)
	for index := range users {
		input := newUser()
		input.EmailVerified = true
		users[index], err = createUser(t, ctx, ss, input)
		requireNoError(t, err)
		_, err = ss.Affiliation().Save(ctx, &model.Affiliation{UserID: users[index].ID,
			Kind: model.AffiliationStudent, StartsAt: fixture.period.StartsAt})
		requireNoError(t, err)
		membership := &model.ClassMember{ClassID: fixture.class.ID, UserID: users[index].ID,
			AcademicPeriodID: fixture.class.AcademicPeriodID, StartsAt: fixture.period.StartsAt}
		membership.PrepareCreate(model.NewClassMemberID(), model.NowUTC())
		_, err = ss.ClassMember().EnrollWithAudit(ctx, &store.ClassMemberEnrollment{Member: membership,
			AuditEventID: saveClassMemberAuditAttempt(t, ctx, ss, fixture.class.ID.String()).ID.String(), AuditAt: model.GetMillis()})
		requireNoError(t, err)
	}
	fanout, err := ss.ExamSitting().GetMailFanout(ctx, prepared.Occurrence.ID)
	requireNoError(t, err)
	page, err := ss.ExamSitting().ListMailRecipients(ctx, store.ExamSittingMailRecipientPageRequest{
		OccurrenceID: fanout.Occurrence.ID, Limit: model.SittingMailExpansionPageSize})
	requireNoError(t, err)
	if len(page.Recipients) != len(users) {
		t.Fatalf("retention recipients=%#v", page.Recipients)
	}
	deliveries := make(map[model.UserID]*model.MailDelivery, len(users))
	for _, recipient := range page.Recipients {
		delivery, job := newExamSittingMailDelivery(t, fanout, recipient.User, recipient.TemplateKey)
		_, err = ss.ExamSitting().CommitMailRecipient(ctx, &store.ExamSittingMailRecipientCommit{
			OccurrenceID: fanout.Occurrence.ID, SittingRevision: fanout.SittingRevision, Recipient: recipient.User,
			Delivery: delivery, DeliveryJob: job})
		requireNoError(t, err)
		sending, startErr := ss.Mail().StartDelivery(ctx, delivery.ID, delivery.Revision, model.NowUTC())
		requireNoError(t, startErr)
		deliveries[recipient.User.ID] = sending
	}
	accepted, err := ss.Mail().CompleteDelivery(ctx, &store.MailDeliveryCompletion{DeliveryID: deliveries[users[0].ID].ID,
		ExpectedRevision: deliveries[users[0].ID].Revision, Kind: store.MailDeliveryCompletionAccepted, At: model.NowUTC()})
	requireNoError(t, err)
	suppressed, err := ss.Mail().CompleteDelivery(ctx, &store.MailDeliveryCompletion{DeliveryID: deliveries[users[1].ID].ID,
		ExpectedRevision: deliveries[users[1].ID].Revision, Kind: store.MailDeliveryCompletionSuppress,
		PublicFailureCode: model.MailDeliveryObsoleteCode, At: model.NowUTC()})
	requireNoError(t, err)
	failed, err := ss.Mail().CompleteDelivery(ctx, &store.MailDeliveryCompletion{DeliveryID: deliveries[users[2].ID].ID,
		ExpectedRevision: deliveries[users[2].ID].Revision, Kind: store.MailDeliveryCompletionFailed,
		PublicFailureCode: "mail.transport.permanent", At: model.NowUTC()})
	requireNoError(t, err)
	_, err = ss.ExamSitting().CompleteMailExpansion(ctx,
		&store.ExamSittingMailExpansionCompletion{OccurrenceID: fanout.Occurrence.ID})
	requireNoError(t, err)

	probe.AgeDeliveries(t, ctx, accepted.ID, suppressed.ID, failed.ID)
	cleaned, err := ss.Mail().CleanupTerminal(ctx, 3)
	requireNoError(t, err)
	if cleaned.Affected != 3 || cleaned.More {
		t.Fatalf("Sitting mail retention cleanup=%#v", cleaned)
	}
	for _, delivery := range []*model.MailDelivery{accepted, suppressed, failed} {
		if _, getErr := ss.Mail().GetDelivery(ctx, delivery.ID); !store.IsNotFound(getErr) {
			t.Fatalf("retained Sitting delivery %s error=%v", delivery.ID, getErr)
		}
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
	if probe.SetSchedule == nil || probe.PrivateActions == nil || probe.AssertAppendOnly == nil {
		t.Fatal("complete Exam Sitting lifecycle SQL probe is required")
	}

	schedule := func(key string, classID model.ClassID, startAt, endAt time.Time) *store.ExamSittingCommandResult {
		t.Helper()
		sitting, err := model.NewExamSitting(model.NewExamSittingID(), fixture.examID, fixture.revisionID, classID, startAt, endAt, model.NowUTC())
		requireNoError(t, err)
		openJob, deadlineJob := newExamSittingLifecycleJobs(t, sitting.ID, 1, startAt, endAt)
		result, err := ss.ExamSitting().Schedule(ctx, &store.ExamSittingSchedule{Sitting: sitting, OpenJob: openJob, DeadlineJob: deadlineJob,
			ActorUserID: fixture.actor.ID, AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID.String(),
			AuditAt: model.GetMillis(), Mail: newExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailScheduled,
				model.MailTemplateExamSittingScheduled)}, examCommand(fixture.actor.ID, "exam.sitting.schedule.v1", key, key+"-command"))
		requireNoError(t, err)
		return result
	}

	// A prepared-Job collision rolls back the Sitting and its second Job.
	rollbackStart := fixture.period.StartsAt.Add(time.Hour)
	rollbackEnd := rollbackStart.Add(time.Hour)
	rollbackSitting, err := model.NewExamSitting(model.NewExamSittingID(), fixture.examID, fixture.revisionID, fixture.class.ID,
		rollbackStart, rollbackEnd, model.NowUTC())
	requireNoError(t, err)
	reservedOpen, _ := newExamSittingLifecycleJobs(t, rollbackSitting.ID, 1, rollbackStart, rollbackEnd)
	_, inserted, err := ss.Job().Enqueue(ctx, &store.JobEnqueue{Job: reservedOpen})
	requireNoError(t, err)
	if !inserted {
		t.Fatal("failed to reserve conflicting lifecycle Job")
	}
	conflictOpen, conflictDeadline := newExamSittingLifecycleJobs(t, rollbackSitting.ID, 1, rollbackStart, rollbackEnd)
	_, err = ss.ExamSitting().Schedule(ctx, &store.ExamSittingSchedule{Sitting: rollbackSitting, OpenJob: conflictOpen, DeadlineJob: conflictDeadline,
		ActorUserID: fixture.actor.ID, AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID.String(),
		AuditAt: model.GetMillis(), Mail: newExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailScheduled,
			model.MailTemplateExamSittingScheduled)}, examCommand(fixture.actor.ID, "exam.sitting.schedule.v1", "sitting-job-rollback", "sitting-job-rollback-command"))
	requireExamSittingConflict(t, err, "exam_sitting_job_mismatch")
	if _, err = ss.ExamSitting().Get(ctx, fixture.examID, rollbackSitting.ID); !store.IsNotFound(err) {
		t.Fatalf("Sitting survived prepared-Job rollback: %v", err)
	}

	startAt := fixture.period.StartsAt.Add(3 * time.Hour)
	endAt := startAt.Add(2 * time.Hour)
	active := schedule("sitting-lifecycle", fixture.class.ID, startAt, endAt)
	beforeAudit := saveExamSittingSystemAudit(t, ctx, ss, active.Value.Sitting.ID, fixture.unitID)
	before, err := ss.ExamSitting().AdvanceDue(ctx, &store.ExamSittingDueAdvance{SittingID: active.Value.Sitting.ID,
		AuditEventID: beforeAudit.ID.String(), AuditAt: model.GetMillis()})
	requireNoError(t, err)
	if before.Changed || before.Value.Sitting.State != model.ExamSittingScheduled {
		t.Fatalf("AdvanceDue(before start)=%#v", before)
	}

	dueStart, dueEnd := model.NowUTC().Add(-time.Minute), model.NowUTC().Add(time.Hour)
	probe.SetSchedule(t, ctx, active.Value.Sitting.ID, dueStart, dueEnd)
	due, err := ss.ExamSitting().ListLifecycleDue(ctx, store.ExamSittingLifecycleDueOptions{Limit: 201})
	requireNoError(t, err)
	found := false
	for _, item := range due {
		if item.Value.Sitting.ID == active.Value.Sitting.ID && item.DueAt.Equal(dueStart) {
			found = true
		}
	}
	if !found {
		t.Fatalf("ListLifecycleDue()=%#v", due)
	}
	openAudit := saveExamSittingSystemAudit(t, ctx, ss, active.Value.Sitting.ID, fixture.unitID)
	opened, err := ss.ExamSitting().AdvanceDue(ctx, &store.ExamSittingDueAdvance{SittingID: active.Value.Sitting.ID,
		AuditEventID: openAudit.ID.String(), AuditAt: model.GetMillis()})
	requireNoError(t, err)
	if !opened.Changed || opened.Transition != store.ExamSittingTransitionOpened || opened.Value.Sitting.State != model.ExamSittingOpen || opened.Value.Sitting.Revision != 2 {
		t.Fatalf("AdvanceDue(open)=%#v", opened)
	}
	// A successful schedule replay is the exact committed outcome even after a
	// later lifecycle transaction advances the current row.
	replayScheduled := *active.Value.Sitting
	replayOpenJob, replayDeadlineJob := newExamSittingLifecycleJobs(t, replayScheduled.ID, replayScheduled.Revision,
		replayScheduled.ScheduledStartAt, replayScheduled.ScheduledEndAt)
	scheduleReplay, err := ss.ExamSitting().Schedule(ctx, &store.ExamSittingSchedule{Sitting: &replayScheduled,
		OpenJob: replayOpenJob, DeadlineJob: replayDeadlineJob, ActorUserID: fixture.actor.ID,
		AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID.String(), AuditAt: model.GetMillis(),
		Mail: newExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailScheduled, model.MailTemplateExamSittingScheduled)},
		examCommand(fixture.actor.ID, "exam.sitting.schedule.v1", "sitting-lifecycle", "sitting-lifecycle-command"))
	requireNoError(t, err)
	if !scheduleReplay.Replayed || scheduleReplay.Value.Sitting.State != model.ExamSittingScheduled || scheduleReplay.Value.Sitting.Revision != 1 {
		t.Fatalf("Schedule(replay after open)=%#v", scheduleReplay)
	}

	pauseAt := model.NowUTC()
	pauseAudit := saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID)
	pauseInput := &store.ExamSittingManagerTransition{ExamID: fixture.examID, SittingID: active.Value.Sitting.ID,
		ActorUserID: fixture.actor.ID, ExpectedRevision: 2, PrivateReason: "investigate candidate report", ChangedAt: pauseAt,
		AuditEventID: pauseAudit.ID.String(), AuditAt: model.GetMillis()}
	pauseCommand := examCommand(fixture.actor.ID, "exam.sitting.pause.v1", "sitting-pause", "sitting-pause-command")
	paused, err := ss.ExamSitting().Pause(ctx, pauseInput, pauseCommand)
	requireNoError(t, err)
	if paused.Transition != store.ExamSittingTransitionManagerPaused || paused.Value.Sitting.State != model.ExamSittingPaused || paused.Value.Sitting.Revision != 3 {
		t.Fatalf("Pause()=%#v", paused)
	}
	replayInput := *pauseInput
	replayInput.AuditEventID = saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID.String()
	replayed, err := ss.ExamSitting().Pause(ctx, &replayInput, pauseCommand)
	requireNoError(t, err)
	if !replayed.Replayed || replayed.Value.Sitting.Revision != 3 {
		t.Fatalf("Pause(replay)=%#v", replayed)
	}

	resumeAudit := saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID)
	resumed, err := ss.ExamSitting().Resume(ctx, &store.ExamSittingManagerTransition{ExamID: fixture.examID, SittingID: active.Value.Sitting.ID,
		ActorUserID: fixture.actor.ID, ExpectedRevision: 3, PrivateReason: "candidate may continue", ChangedAt: model.NowUTC(),
		AuditEventID: resumeAudit.ID.String(), AuditAt: model.GetMillis()},
		examCommand(fixture.actor.ID, "exam.sitting.resume.v1", "sitting-resume", "sitting-resume-command"))
	requireNoError(t, err)
	if resumed.Value.Sitting.State != model.ExamSittingOpen || resumed.Value.Sitting.Revision != 4 {
		t.Fatalf("Resume()=%#v", resumed)
	}

	extendedEnd := dueEnd.Add(time.Hour)
	_, extendedDeadline := newExamSittingLifecycleJobs(t, active.Value.Sitting.ID, 5, dueStart, extendedEnd)
	extendAudit := saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID)
	extended, err := ss.ExamSitting().Extend(ctx, &store.ExamSittingExtension{ExamID: fixture.examID, SittingID: active.Value.Sitting.ID,
		ActorUserID: fixture.actor.ID, ExpectedRevision: 4, ScheduledEndAt: extendedEnd, DeadlineJob: extendedDeadline,
		PrivateReason: "grant additional coding time", ChangedAt: model.NowUTC(), AuditEventID: extendAudit.ID.String(), AuditAt: model.GetMillis()},
		examCommand(fixture.actor.ID, "exam.sitting.extend.v1", "sitting-extend", "sitting-extend-command"))
	requireNoError(t, err)
	if extended.Value.Sitting.ScheduledEndAt != extendedEnd || extended.Value.Sitting.Revision != 5 {
		t.Fatalf("Extend()=%#v", extended)
	}
	_, equalDeadline := newExamSittingLifecycleJobs(t, active.Value.Sitting.ID, 6, dueStart, extendedEnd)
	_, err = ss.ExamSitting().Extend(ctx, &store.ExamSittingExtension{ExamID: fixture.examID, SittingID: active.Value.Sitting.ID,
		ActorUserID: fixture.actor.ID, ExpectedRevision: 5, ScheduledEndAt: extendedEnd, DeadlineJob: equalDeadline,
		PrivateReason: "no actual extension", ChangedAt: model.NowUTC(),
		AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID.String(), AuditAt: model.GetMillis()},
		examCommand(fixture.actor.ID, "exam.sitting.extend.v1", "sitting-extend-equal", "sitting-extend-equal-command"))
	requireExamSittingConflict(t, err, "exam_sitting_extension_not_later")
	beyondPeriod := fixture.period.EndsAt.Add(time.Second)
	_, beyondDeadline := newExamSittingLifecycleJobs(t, active.Value.Sitting.ID, 6, dueStart, beyondPeriod)
	_, err = ss.ExamSitting().Extend(ctx, &store.ExamSittingExtension{ExamID: fixture.examID, SittingID: active.Value.Sitting.ID,
		ActorUserID: fixture.actor.ID, ExpectedRevision: 5, ScheduledEndAt: beyondPeriod, DeadlineJob: beyondDeadline,
		PrivateReason: "invalid extension", ChangedAt: model.NowUTC(),
		AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID.String(), AuditAt: model.GetMillis()},
		examCommand(fixture.actor.ID, "exam.sitting.extend.v1", "sitting-extend-period", "sitting-extend-period-command"))
	requireExamSittingConflict(t, err, "exam_sitting_schedule_outside_period")
	laterReplayInput := *pauseInput
	laterReplayInput.AuditEventID = saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID.String()
	laterReplay, err := ss.ExamSitting().Pause(ctx, &laterReplayInput, pauseCommand)
	requireNoError(t, err)
	if !laterReplay.Replayed || laterReplay.Value.Sitting.State != model.ExamSittingPaused || laterReplay.Value.Sitting.Revision != 3 {
		t.Fatalf("Pause(replay after Resume and Extend)=%#v", laterReplay)
	}
	currentAfterReplay, err := ss.ExamSitting().Get(ctx, fixture.examID, active.Value.Sitting.ID)
	requireNoError(t, err)
	if currentAfterReplay.Sitting.State != model.ExamSittingOpen || currentAfterReplay.Sitting.Revision != 5 {
		t.Fatalf("current Sitting changed by replay: %#v", currentAfterReplay)
	}

	closeAt := model.NowUTC()
	closeAudit := saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID)
	closing, err := ss.ExamSitting().EarlyClose(ctx, &store.ExamSittingManagerTransition{ExamID: fixture.examID,
		SittingID: active.Value.Sitting.ID, ActorUserID: fixture.actor.ID, ExpectedRevision: 5,
		FinalizeJob: newExamSittingFinalizeJob(t, active.Value.Sitting.ID, 6, closeAt), PrivateReason: "end session early",
		ChangedAt: closeAt, AuditEventID: closeAudit.ID.String(), AuditAt: model.GetMillis()},
		examCommand(fixture.actor.ID, "exam.sitting.close.v1", "sitting-early-close", "sitting-early-close-command"))
	requireNoError(t, err)
	if closing.Value.Sitting.State != model.ExamSittingClosing || closing.Value.Sitting.ReasonCode != model.ExamSittingReasonManagerClosed {
		t.Fatalf("EarlyClose()=%#v", closing)
	}
	closingDue, err := ss.ExamSitting().ListLifecycleDue(ctx, store.ExamSittingLifecycleDueOptions{Limit: 201})
	requireNoError(t, err)
	foundClosing := false
	for _, item := range closingDue {
		if item.Value.Sitting.ID == closing.Value.Sitting.ID && item.Value.Sitting.State == model.ExamSittingClosing &&
			item.DueAt.Equal(closing.Value.Sitting.ClosingAt.Time) {
			foundClosing = true
		}
	}
	if !foundClosing {
		t.Fatalf("ListLifecycleDue() omitted Closing recovery row: %#v", closingDue)
	}
	closedAudit := saveExamSittingSystemAudit(t, ctx, ss, active.Value.Sitting.ID, fixture.unitID)
	closed, err := ss.ExamSitting().FinishSealing(ctx, &store.ExamSittingFinishSealing{SittingID: active.Value.Sitting.ID,
		AuditEventID: closedAudit.ID.String(), AuditAt: model.GetMillis()})
	requireNoError(t, err)
	if !closed.Changed || closed.Value.Sitting.State != model.ExamSittingClosed || closed.Value.Sitting.Revision != 7 {
		t.Fatalf("FinishSealing(no Attempts)=%#v", closed)
	}

	actions := probe.PrivateActions(t, ctx, active.Value.Sitting.ID)
	wantActions := []ExamSittingPrivateActionProbe{
		{ActionCode: "manager_paused", PrivateReason: "investigate candidate report", Revision: 3},
		{ActionCode: "manager_resumed", PrivateReason: "candidate may continue", Revision: 4},
		{ActionCode: "manager_extended", PrivateReason: "grant additional coding time", Revision: 5},
		{ActionCode: "manager_closed", PrivateReason: "end session early", Revision: 6},
	}
	if len(actions) != len(wantActions) {
		t.Fatalf("private actions=%#v", actions)
	}
	for index := range wantActions {
		if actions[index] != wantActions[index] {
			t.Fatalf("private action[%d]=%#v want %#v", index, actions[index], wantActions[index])
		}
	}
	probe.AssertAppendOnly(t, ctx, active.Value.Sitting.ID)

	// Entirely elapsed schedules cancel without opening.
	elapsed := schedule("sitting-elapsed", fixture.class.ID, fixture.period.StartsAt.Add(8*time.Hour), fixture.period.StartsAt.Add(9*time.Hour))
	probe.SetSchedule(t, ctx, elapsed.Value.Sitting.ID, model.NowUTC().Add(-2*time.Hour), model.NowUTC().Add(-time.Hour))
	elapsedAudit := saveExamSittingSystemAudit(t, ctx, ss, elapsed.Value.Sitting.ID, fixture.unitID)
	elapsedResult, err := ss.ExamSitting().AdvanceDue(ctx, &store.ExamSittingDueAdvance{SittingID: elapsed.Value.Sitting.ID,
		AuditEventID: elapsedAudit.ID.String(), AuditAt: model.GetMillis()})
	requireNoError(t, err)
	if elapsedResult.Transition != store.ExamSittingTransitionScheduleElapsed || elapsedResult.Value.Sitting.State != model.ExamSittingCanceled {
		t.Fatalf("AdvanceDue(elapsed)=%#v", elapsedResult)
	}

	// A lineage archived after scheduling cancels at opening revalidation.
	invalidClass := saveClass(t, ctx, ss, fixture.levelID.String(), fixture.period.ID.String(), "sitting-lifecycle-invalid-class")
	invalid := schedule("sitting-invalid-structure", invalidClass.ID, fixture.period.StartsAt.Add(10*time.Hour), fixture.period.StartsAt.Add(11*time.Hour))
	_, err = ss.Class().Archive(ctx, invalidClass.ID.String(), model.GetMillis())
	requireNoError(t, err)
	probe.SetSchedule(t, ctx, invalid.Value.Sitting.ID, model.NowUTC().Add(-time.Minute), model.NowUTC().Add(time.Hour))
	invalidAudit := saveExamSittingSystemAudit(t, ctx, ss, invalid.Value.Sitting.ID, fixture.unitID)
	invalidResult, err := ss.ExamSitting().AdvanceDue(ctx, &store.ExamSittingDueAdvance{SittingID: invalid.Value.Sitting.ID,
		AuditEventID: invalidAudit.ID.String(), AuditAt: model.GetMillis()})
	requireNoError(t, err)
	if invalidResult.Transition != store.ExamSittingTransitionAcademicStructureInvalid || invalidResult.Value.Sitting.State != model.ExamSittingCanceled {
		t.Fatalf("AdvanceDue(invalid structure)=%#v", invalidResult)
	}

	// Deadline processing treats Paused exactly like Open and enqueues the
	// revision-keyed finalize occurrence in the same transaction.
	deadline := schedule("sitting-paused-deadline", fixture.class.ID, fixture.period.StartsAt.Add(12*time.Hour), fixture.period.StartsAt.Add(13*time.Hour))
	deadlineStart, deadlineEnd := model.NowUTC().Add(-time.Minute), model.NowUTC().Add(time.Hour)
	probe.SetSchedule(t, ctx, deadline.Value.Sitting.ID, deadlineStart, deadlineEnd)
	deadlineOpenAudit := saveExamSittingSystemAudit(t, ctx, ss, deadline.Value.Sitting.ID, fixture.unitID)
	deadlineOpen, err := ss.ExamSitting().AdvanceDue(ctx, &store.ExamSittingDueAdvance{SittingID: deadline.Value.Sitting.ID,
		AuditEventID: deadlineOpenAudit.ID.String(), AuditAt: model.GetMillis()})
	requireNoError(t, err)
	deadlinePauseAudit := saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID)
	deadlinePaused, err := ss.ExamSitting().Pause(ctx, &store.ExamSittingManagerTransition{ExamID: fixture.examID,
		SittingID: deadline.Value.Sitting.ID, ActorUserID: fixture.actor.ID, ExpectedRevision: deadlineOpen.Value.Sitting.Revision,
		PrivateReason: "pause through deadline", ChangedAt: model.NowUTC(), AuditEventID: deadlinePauseAudit.ID.String(), AuditAt: model.GetMillis()},
		examCommand(fixture.actor.ID, "exam.sitting.pause.v1", "sitting-deadline-pause", "sitting-deadline-pause-command"))
	requireNoError(t, err)
	pastDeadline := model.NowUTC().Add(-time.Second)
	probe.SetSchedule(t, ctx, deadline.Value.Sitting.ID, deadlineStart, pastDeadline)
	_, err = ss.ExamSitting().Resume(ctx, &store.ExamSittingManagerTransition{ExamID: fixture.examID,
		SittingID: deadline.Value.Sitting.ID, ActorUserID: fixture.actor.ID, ExpectedRevision: deadlinePaused.Value.Sitting.Revision,
		PrivateReason: "too late", ChangedAt: model.NowUTC(),
		AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID.String(), AuditAt: model.GetMillis()},
		examCommand(fixture.actor.ID, "exam.sitting.resume.v1", "sitting-resume-after-deadline", "sitting-resume-after-deadline-command"))
	requireExamSittingConflict(t, err, "exam_sitting_deadline_reached")
	_, err = ss.ExamSitting().Pause(ctx, &store.ExamSittingManagerTransition{ExamID: fixture.examID,
		SittingID: deadline.Value.Sitting.ID, ActorUserID: fixture.actor.ID, ExpectedRevision: deadlinePaused.Value.Sitting.Revision,
		PrivateReason: "wrong state", ChangedAt: model.NowUTC(),
		AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID.String(), AuditAt: model.GetMillis()},
		examCommand(fixture.actor.ID, "exam.sitting.pause.v1", "sitting-pause-after-deadline", "sitting-pause-after-deadline-command"))
	requireExamSittingConflict(t, err, "exam_sitting_state")
	staleFinalizeAudit := saveExamSittingSystemAudit(t, ctx, ss, deadline.Value.Sitting.ID, fixture.unitID)
	_, err = ss.ExamSitting().AdvanceDue(ctx, &store.ExamSittingDueAdvance{SittingID: deadline.Value.Sitting.ID,
		FinalizeJob:  newExamSittingFinalizeJob(t, deadline.Value.Sitting.ID, deadlineOpen.Value.Sitting.Revision+1, pastDeadline),
		AuditEventID: staleFinalizeAudit.ID.String(), AuditAt: model.GetMillis()})
	requireExamSittingConflict(t, err, "exam_sitting_revision")
	afterStaleFinalize, err := ss.ExamSitting().Get(ctx, fixture.examID, deadline.Value.Sitting.ID)
	requireNoError(t, err)
	if afterStaleFinalize.Sitting.State != model.ExamSittingPaused || afterStaleFinalize.Sitting.Revision != deadlinePaused.Value.Sitting.Revision {
		t.Fatalf("stale prepared Finalize Job mutated Sitting: %#v", afterStaleFinalize)
	}
	deadlineAudit := saveExamSittingSystemAudit(t, ctx, ss, deadline.Value.Sitting.ID, fixture.unitID)
	deadlineClosing, err := ss.ExamSitting().AdvanceDue(ctx, &store.ExamSittingDueAdvance{SittingID: deadline.Value.Sitting.ID,
		FinalizeJob:  newExamSittingFinalizeJob(t, deadline.Value.Sitting.ID, deadlinePaused.Value.Sitting.Revision+1, pastDeadline),
		AuditEventID: deadlineAudit.ID.String(), AuditAt: model.GetMillis()})
	requireNoError(t, err)
	if deadlineClosing.Transition != store.ExamSittingTransitionScheduledEndReached || deadlineClosing.Value.Sitting.State != model.ExamSittingClosing ||
		deadlineClosing.Value.Sitting.ReasonCode != model.ExamSittingReasonScheduledEndReached {
		t.Fatalf("AdvanceDue(paused deadline)=%#v", deadlineClosing)
	}

	// Competing manager transitions share the Sitting revision fence. Exactly
	// one commits, and the winner's returned value remains coherent with its own
	// transition even while the loser observes the later row.
	managerRace := schedule("sitting-manager-race", fixture.class.ID, fixture.period.StartsAt.Add(16*time.Hour), fixture.period.StartsAt.Add(17*time.Hour))
	managerRaceStart, managerRaceEnd := model.NowUTC().Add(-time.Minute), model.NowUTC().Add(time.Hour)
	probe.SetSchedule(t, ctx, managerRace.Value.Sitting.ID, managerRaceStart, managerRaceEnd)
	managerRaceOpen, err := ss.ExamSitting().AdvanceDue(ctx, &store.ExamSittingDueAdvance{SittingID: managerRace.Value.Sitting.ID,
		AuditEventID: saveExamSittingSystemAudit(t, ctx, ss, managerRace.Value.Sitting.ID, fixture.unitID).ID.String(), AuditAt: model.GetMillis()})
	requireNoError(t, err)
	raceRevision := managerRaceOpen.Value.Sitting.Revision
	raceCloseAt := model.NowUTC()
	pauseRaceInput := &store.ExamSittingManagerTransition{ExamID: fixture.examID, SittingID: managerRace.Value.Sitting.ID,
		ActorUserID: fixture.actor.ID, ExpectedRevision: raceRevision, PrivateReason: "concurrent pause", ChangedAt: model.NowUTC(),
		AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID.String(), AuditAt: model.GetMillis()}
	closeRaceInput := &store.ExamSittingManagerTransition{ExamID: fixture.examID, SittingID: managerRace.Value.Sitting.ID,
		ActorUserID: fixture.actor.ID, ExpectedRevision: raceRevision,
		FinalizeJob:   newExamSittingFinalizeJob(t, managerRace.Value.Sitting.ID, raceRevision+1, raceCloseAt),
		PrivateReason: "concurrent close", ChangedAt: raceCloseAt,
		AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID.String(), AuditAt: model.GetMillis()}
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
	if (firstRace.err == nil) == (secondRace.err == nil) {
		t.Fatalf("manager transition race results: first=%#v/%v second=%#v/%v", firstRace.value, firstRace.err, secondRace.value, secondRace.err)
	}
	winner, loser := firstRace, secondRace
	if winner.err != nil {
		winner, loser = secondRace, firstRace
	}
	if winner.value.Value.Sitting.Revision != raceRevision+1 ||
		(winner.value.Transition == store.ExamSittingTransitionManagerPaused && winner.value.Value.Sitting.State != model.ExamSittingPaused) ||
		(winner.value.Transition == store.ExamSittingTransitionManagerClosed && winner.value.Value.Sitting.State != model.ExamSittingClosing) {
		t.Fatalf("incoherent manager race winner=%#v", winner.value)
	}
	var loserConflict *store.ErrConflict
	if !errors.As(loser.err, &loserConflict) || (loserConflict.Constraint != "exam_sitting_revision" && loserConflict.Constraint != "exam_sitting_state") {
		t.Fatalf("manager race loser=%v", loser.err)
	}

	// An archived Exam may still be paused or closed when already live, but it
	// cannot be resumed or extended into further participation.
	archivedLive := schedule("sitting-archived-live", fixture.class.ID, fixture.period.StartsAt.Add(14*time.Hour), fixture.period.StartsAt.Add(15*time.Hour))
	archivedStart, archivedEnd := model.NowUTC().Add(-time.Minute), model.NowUTC().Add(time.Hour)
	probe.SetSchedule(t, ctx, archivedLive.Value.Sitting.ID, archivedStart, archivedEnd)
	archivedOpen, err := ss.ExamSitting().AdvanceDue(ctx, &store.ExamSittingDueAdvance{SittingID: archivedLive.Value.Sitting.ID,
		AuditEventID: saveExamSittingSystemAudit(t, ctx, ss, archivedLive.Value.Sitting.ID, fixture.unitID).ID.String(), AuditAt: model.GetMillis()})
	requireNoError(t, err)
	examBeforeArchive, err := ss.ExamAuthoring().Resolve(ctx, fixture.examID)
	requireNoError(t, err)
	archiveAt := model.NowUTC()
	_, err = ss.ExamAuthoring().Archive(ctx,
		newExamArchive(t, ctx, ss, fixture.examID, fixture.actor.ID, examBeforeArchive.Revision, archiveAt),
		examCommand(fixture.actor.ID, "exam.archive.v1", "sitting-live-archive", "sitting-live-archive-command"))
	requireNoError(t, err)
	archivedPaused, err := ss.ExamSitting().Pause(ctx, &store.ExamSittingManagerTransition{ExamID: fixture.examID,
		SittingID: archivedLive.Value.Sitting.ID, ActorUserID: fixture.actor.ID, ExpectedRevision: archivedOpen.Value.Sitting.Revision,
		PrivateReason: "pause archived live Sitting", ChangedAt: model.NowUTC(),
		AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID.String(), AuditAt: model.GetMillis()},
		examCommand(fixture.actor.ID, "exam.sitting.pause.v1", "sitting-archived-pause", "sitting-archived-pause-command"))
	requireNoError(t, err)
	_, err = ss.ExamSitting().Resume(ctx, &store.ExamSittingManagerTransition{ExamID: fixture.examID,
		SittingID: archivedLive.Value.Sitting.ID, ActorUserID: fixture.actor.ID, ExpectedRevision: archivedPaused.Value.Sitting.Revision,
		PrivateReason: "resume archived Sitting", ChangedAt: model.NowUTC(),
		AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID.String(), AuditAt: model.GetMillis()},
		examCommand(fixture.actor.ID, "exam.sitting.resume.v1", "sitting-archived-resume", "sitting-archived-resume-command"))
	requireExamSittingConflict(t, err, "exam_archived")
	archivedCloseAt := model.NowUTC()
	archivedClosing, err := ss.ExamSitting().EarlyClose(ctx, &store.ExamSittingManagerTransition{ExamID: fixture.examID,
		SittingID: archivedLive.Value.Sitting.ID, ActorUserID: fixture.actor.ID, ExpectedRevision: archivedPaused.Value.Sitting.Revision,
		FinalizeJob:   newExamSittingFinalizeJob(t, archivedLive.Value.Sitting.ID, archivedPaused.Value.Sitting.Revision+1, archivedCloseAt),
		PrivateReason: "close archived live Sitting", ChangedAt: archivedCloseAt,
		AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID.String(), AuditAt: model.GetMillis()},
		examCommand(fixture.actor.ID, "exam.sitting.close.v1", "sitting-archived-close", "sitting-archived-close-command"))
	requireNoError(t, err)
	if archivedClosing.Value.Sitting.State != model.ExamSittingClosing || archivedClosing.Value.Sitting.ReasonCode != model.ExamSittingReasonManagerClosed {
		t.Fatalf("EarlyClose(archived live)=%#v", archivedClosing)
	}
}

// TestExamSittingStoreSQLGuards characterizes PostgreSQL-backed aggregate
// guards whose ordering and DB-clock behavior are part of the contract.
func TestExamSittingStoreSQLGuards(t *testing.T, ss store.Store, probe ExamSittingSQLProbe) {
	ctx := context.Background()
	fixture := newExamSittingFixture(t, ctx, ss)

	schedule := func(key string, actor model.UserID, revisionID model.ExamRevisionID, classID model.ClassID, start, end time.Time) (*store.ExamSittingCommandResult, error) {
		sitting, err := model.NewExamSitting(model.NewExamSittingID(), fixture.examID, revisionID, classID, start, end, model.NowUTC())
		if err != nil {
			t.Fatal(err)
		}
		openJob, deadlineJob := newExamSittingLifecycleJobs(t, sitting.ID, 1, start, end)
		return ss.ExamSitting().Schedule(ctx, &store.ExamSittingSchedule{Sitting: sitting, OpenJob: openJob, DeadlineJob: deadlineJob, ActorUserID: actor,
			AuditEventID: saveExamSittingAudit(t, ctx, ss, actor, fixture.examID, fixture.unitID).ID.String(), AuditAt: model.GetMillis(),
			Mail: newExamSittingMailFanout(t, actor, store.ExamSittingMailScheduled, model.MailTemplateExamSittingScheduled)},
			examCommand(actor, "exam.sitting.schedule.v1", key, key+"-command"))
	}
	requireScheduleConflict := func(key, constraint string, revisionID model.ExamRevisionID, classID model.ClassID, start, end time.Time) {
		t.Helper()
		_, err := schedule(key, fixture.actor.ID, revisionID, classID, start, end)
		requireExamSittingConflict(t, err, constraint)
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
	_, err := ss.Class().Archive(ctx, archivedClass.ID.String(), model.GetMillis())
	requireNoError(t, err)
	requireScheduleConflict("sitting-archived-class", "exam_sitting_class_lineage", fixture.revisionID, archivedClass.ID,
		fixture.period.StartsAt.Add(3*time.Hour), fixture.period.StartsAt.Add(4*time.Hour))

	if probe.ArchiveProgrammeLevel == nil {
		t.Fatal("ArchiveProgrammeLevel probe is required")
	}
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
	requireNoError(t, err)
	if boundary.Value.Sitting.ScheduledStartAt != fixture.period.StartsAt || boundary.Value.Sitting.ScheduledEndAt != fixture.period.EndsAt {
		t.Fatalf("boundary Schedule()=%#v", boundary)
	}
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
	requireNoError(t, err)
	exam, err := ss.ExamAuthoring().Resolve(ctx, fixture.examID)
	requireNoError(t, err)
	addManager := newExamManagerMutation(t, ctx, ss, fixture.examID, fixture.actor.ID, removedManager.ID, exam.Revision, model.NowUTC(), false)
	addManager.Notices = examManagerMailNotices(t, model.TimeFromMillis(addManager.ChangedAt),
		examManagerMailRecipient{userID: removedManager.ID, key: model.MailTemplateExamManagerAdded})
	added, err := ss.ExamAuthoring().AddManager(ctx, addManager,
		examCommand(fixture.actor.ID, "exam.manager.add.v1", "sitting-manager-add", "sitting-manager-add-command"))
	requireNoError(t, err)
	removeManager := newExamManagerMutation(t, ctx, ss, fixture.examID, fixture.actor.ID, removedManager.ID, added.Exam.Revision, model.NowUTC(), false)
	removeManager.Notices = examManagerMailNotices(t, model.TimeFromMillis(removeManager.ChangedAt),
		examManagerMailRecipient{userID: removedManager.ID, key: model.MailTemplateExamManagerRemoved})
	_, err = ss.ExamAuthoring().RemoveManager(ctx, removeManager,
		examCommand(fixture.actor.ID, "exam.manager.remove.v1", "sitting-manager-remove", "sitting-manager-remove-command"))
	requireNoError(t, err)
	_, err = schedule("sitting-removed-manager", removedManager.ID, fixture.revisionID, fixture.class.ID,
		fixture.period.StartsAt.Add(7*time.Hour), fixture.period.StartsAt.Add(8*time.Hour))
	if !store.IsNotFound(err) {
		t.Fatalf("Schedule(removed manager) error=%v", err)
	}

	// Once both aggregate rows are locked, stale revision and current state
	// precede validation of the proposed foreign selection.
	precedence, err := schedule("sitting-precedence", fixture.actor.ID, fixture.revisionID, fixture.class.ID,
		fixture.period.StartsAt.Add(9*time.Hour), fixture.period.StartsAt.Add(10*time.Hour))
	requireNoError(t, err)
	_, err = ss.ExamSitting().Cancel(ctx, &store.ExamSittingCancellation{ExamID: fixture.examID, SittingID: precedence.Value.Sitting.ID,
		ActorUserID: fixture.actor.ID, ExpectedRevision: 1, PrivateReason: "precedence", CanceledAt: model.NowUTC(),
		AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID.String(), AuditAt: model.GetMillis(),
		Mail: newExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailCancelled, model.MailTemplateExamSittingCancelled)},
		examCommand(fixture.actor.ID, "exam.sitting.cancel.v1", "sitting-precedence-cancel", "sitting-precedence-cancel-command"))
	requireNoError(t, err)
	invalidUpdate := func(expected int64, key string) error {
		openJob, deadlineJob := newExamSittingLifecycleJobs(t, precedence.Value.Sitting.ID, expected+1,
			fixture.period.StartsAt.Add(11*time.Hour), fixture.period.StartsAt.Add(12*time.Hour))
		_, updateErr := ss.ExamSitting().UpdateSchedule(ctx, &store.ExamSittingScheduleUpdate{ExamID: fixture.examID,
			SittingID: precedence.Value.Sitting.ID, ActorUserID: fixture.actor.ID, ExpectedRevision: expected,
			ExamRevisionID: fixture.revisionID, ClassID: foreignClass.ID, ScheduledStartAt: fixture.period.StartsAt.Add(11 * time.Hour),
			ScheduledEndAt: fixture.period.StartsAt.Add(12 * time.Hour), OpenJob: openJob, DeadlineJob: deadlineJob, ChangedAt: model.NowUTC(),
			AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID.String(), AuditAt: model.GetMillis(),
			Mail: newExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailRescheduled, model.MailTemplateExamSittingRescheduled)},
			examCommand(fixture.actor.ID, "exam.sitting.schedule.update.v1", key, key+"-command"))
		return updateErr
	}
	requireExamSittingConflict(t, invalidUpdate(1, "sitting-precedence-stale"), "exam_sitting_revision")
	requireExamSittingConflict(t, invalidUpdate(2, "sitting-precedence-state"), "exam_sitting_state")

	// Competing mutations serialize on Exam then Sitting. Exactly one commits;
	// the loser observes the advanced optimistic revision (or lifecycle state).
	race, err := schedule("sitting-race", fixture.actor.ID, fixture.revisionID, fixture.class.ID,
		fixture.period.StartsAt.Add(13*time.Hour), fixture.period.StartsAt.Add(14*time.Hour))
	requireNoError(t, err)
	updateAudit := saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID)
	cancelAudit := saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID)
	raceOpenJob, raceDeadlineJob := newExamSittingLifecycleJobs(t, race.Value.Sitting.ID, 2,
		fixture.period.StartsAt.Add(15*time.Hour), fixture.period.StartsAt.Add(16*time.Hour))
	var updateErr, cancelErr error
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		_, updateErr = ss.ExamSitting().UpdateSchedule(ctx, &store.ExamSittingScheduleUpdate{ExamID: fixture.examID,
			SittingID: race.Value.Sitting.ID, ActorUserID: fixture.actor.ID, ExpectedRevision: 1,
			ExamRevisionID: fixture.revisionID, ClassID: fixture.class.ID, ScheduledStartAt: fixture.period.StartsAt.Add(15 * time.Hour),
			ScheduledEndAt: fixture.period.StartsAt.Add(16 * time.Hour), OpenJob: raceOpenJob, DeadlineJob: raceDeadlineJob, ChangedAt: model.NowUTC(),
			AuditEventID: updateAudit.ID.String(), AuditAt: model.GetMillis(),
			Mail: newExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailRescheduled, model.MailTemplateExamSittingRescheduled)},
			examCommand(fixture.actor.ID, "exam.sitting.schedule.update.v1", "sitting-race-update", "sitting-race-update-command"))
	}()
	go func() {
		defer wait.Done()
		_, cancelErr = ss.ExamSitting().Cancel(ctx, &store.ExamSittingCancellation{ExamID: fixture.examID,
			SittingID: race.Value.Sitting.ID, ActorUserID: fixture.actor.ID, ExpectedRevision: 1,
			PrivateReason: "race", CanceledAt: model.NowUTC(), AuditEventID: cancelAudit.ID.String(), AuditAt: model.GetMillis(),
			Mail: newExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailCancelled, model.MailTemplateExamSittingCancelled)},
			examCommand(fixture.actor.ID, "exam.sitting.cancel.v1", "sitting-race-cancel", "sitting-race-cancel-command"))
	}()
	wait.Wait()
	if (updateErr == nil) == (cancelErr == nil) {
		t.Fatalf("race update error=%v cancel error=%v; want exactly one winner", updateErr, cancelErr)
	}
	loser := updateErr
	if loser == nil {
		loser = cancelErr
	}
	var conflict *store.ErrConflict
	if !errors.As(loser, &conflict) || (conflict.Constraint != "exam_sitting_revision" && conflict.Constraint != "exam_sitting_state") {
		t.Fatalf("race loser error=%v", loser)
	}
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
	t.Helper()
	classFixture := saveClassFixture(t, ctx, ss)
	class := saveClass(t, ctx, ss, classFixture.level.ID.String(), classFixture.period.ID.String(), "sitting-class")
	actor := saveUser(t, ctx, ss)
	created := createCatalogExam(t, ctx, ss, classFixture.programme.AcademicUnitID, actor.ID, model.NowUTC(), "sitting-exam")
	publication := examRevisionPublication(t, ctx, ss, created.Value.Exam.ID, actor.ID, classFixture.programme.AcademicUnitID, 1, model.NowUTC())
	published, err := ss.ExamRevision().Publish(ctx, publication, examCommand(actor.ID, "exam.revision.publish.v1", "sitting-revision", "sitting-revision-command"))
	requireNoError(t, err)
	return examSittingFixture{actor: actor, institutionID: classFixture.institution.ID, unitID: classFixture.programme.AcademicUnitID,
		levelID: classFixture.level.ID, examID: created.Value.Exam.ID,
		revisionID: published.Revision.ID, class: class, period: classFixture.period}
}

func saveExamSittingAudit(t *testing.T, ctx context.Context, ss store.Store, actor model.UserID, exam model.ExamID, unit model.AcademicUnitID) *model.AuditEvent {
	t.Helper()
	audit, err := ss.Audit().Save(ctx, &model.AuditEvent{ActorID: actor, Action: "exam.sitting.manage",
		Resource: model.Resource{Type: model.ResourceExam, ID: exam.String()}, ScopeType: model.RoleScopeAcademicUnit,
		ScopeID: unit.String(), Status: model.AuditStatusAttempt, NodeID: "test-node"})
	requireNoError(t, err)
	return audit
}

func saveExamSittingSystemAudit(t *testing.T, ctx context.Context, ss store.Store, sitting model.ExamSittingID, unit model.AcademicUnitID) *model.AuditEvent {
	t.Helper()
	audit, err := ss.Audit().Save(ctx, &model.AuditEvent{Action: string(model.ActionExamSittingManage),
		Resource: model.Resource{Type: model.ResourceExamSitting, ID: sitting.String()}, ScopeType: model.RoleScopeAcademicUnit,
		ScopeID: unit.String(), Status: model.AuditStatusAttempt, NodeID: "test-node"})
	requireNoError(t, err)
	return audit
}

func newExamSittingLifecycleJobs(t *testing.T, sittingID model.ExamSittingID, revision int64, startAt, endAt time.Time) (*model.Job, *model.Job) {
	t.Helper()
	command, err := model.EncodeExamSittingLifecycleCommand(model.ExamSittingLifecycleCommandV1{ExamSittingID: sittingID})
	requireNoError(t, err)
	createdAt := model.NowUTC()
	newJob := func(phase model.ExamSittingLifecycleJobPhase, availableAt time.Time) *model.Job {
		key, keyErr := model.ExamSittingLifecycleDedupeKey(sittingID, phase, revision)
		requireNoError(t, keyErr)
		job, jobErr := model.NewJobWithDedupePolicy(model.NewJobID(), model.JobTypeExamSittingLifecycle, 1, command,
			key, model.JobDedupeActive, createdAt, availableAt, 8)
		requireNoError(t, jobErr)
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
	requireNoError(t, err)
	dedupe, err := model.SittingMailExpansionDedupeKey(occurrenceID)
	requireNoError(t, err)
	job, err := model.NewJobWithDedupePolicy(model.NewJobID(), model.JobTypeMailExpandSitting, 1, command, dedupe,
		model.JobDedupePermanent, at, at, model.MailMaximumAttempts)
	requireNoError(t, err)
	return &store.ExamSittingMailFanout{Occurrence: occurrence, Bundle: bundle, ExpansionJob: job,
		ChangeKind: change, DeliveryLifetime: 72 * time.Hour}
}

func newDisabledExamSittingMailFanout(t *testing.T, actorID model.UserID, change store.ExamSittingMailChangeKind,
	templateKey model.MailTemplateKey,
) *store.ExamSittingMailFanout {
	t.Helper()
	prepared := newExamSittingMailFanout(t, actorID, change, templateKey)
	canceled, err := prepared.ExpansionJob.RequestCancellation(prepared.ExpansionJob.CreatedAt)
	requireNoError(t, err)
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
	requireNoError(t, err)
	job, err := model.NewJob(jobID, model.JobTypeMailDeliver, 1, command, deliveryID.String(),
		fanout.Occurrence.CreatedAt, fanout.Occurrence.CreatedAt, model.MailMaximumAttempts)
	requireNoError(t, err)
	delivery := &model.MailDelivery{ID: deliveryID, OccurrenceID: fanout.Occurrence.ID, JobID: jobID,
		TargetUserID: user.ID, TemplateKey: templateKey, TemplateDigest: strings.Repeat("a", 64),
		MaskedRecipient: "***@example.edu", State: model.MailDeliveryQueued,
		CreatedAt: fanout.Occurrence.CreatedAt, UpdatedAt: fanout.Occurrence.CreatedAt, MessageDate: fanout.Occurrence.CreatedAt,
		Deadline: fanout.Deadline, MessageID: "<" + deliveryID.String() + "@example.edu>",
		EncryptedPayload: []byte(`{"key_id":"11111111111111111111111111111111","version":1,"nonce":"n","ciphertext":"c"}`), Revision: 1}
	requireNoError(t, delivery.Validate())
	return delivery, job
}

func newExamSittingFinalizeJob(t *testing.T, sittingID model.ExamSittingID, revision int64, availableAt time.Time) *model.Job {
	t.Helper()
	command, err := model.EncodeExamSittingLifecycleCommand(model.ExamSittingLifecycleCommandV1{ExamSittingID: sittingID})
	requireNoError(t, err)
	key, err := model.ExamSittingLifecycleDedupeKey(sittingID, model.ExamSittingLifecycleJobFinalize, revision)
	requireNoError(t, err)
	job, err := model.NewJobWithDedupePolicy(model.NewJobID(), model.JobTypeExamSittingSealing, 1, command, key,
		model.JobDedupeActive, model.NowUTC(), availableAt, 8)
	requireNoError(t, err)
	return job
}

func requireExamSittingConflict(t *testing.T, err error, constraint string) {
	t.Helper()
	var conflict *store.ErrConflict
	if !errors.As(err, &conflict) || conflict.Constraint != constraint {
		t.Fatalf("error=%v want conflict %q", err, constraint)
	}
}
