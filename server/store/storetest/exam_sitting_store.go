// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"bytes"
	"context"
	"errors"
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
	scheduleAudit := saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID)
	command := examCommand(fixture.actor.ID, "exam.sitting.schedule.v1", "sitting-schedule", "sitting-schedule-command")
	created, err := ss.ExamSitting().Schedule(ctx, &store.ExamSittingSchedule{Sitting: sitting, OpenJob: openJob, DeadlineJob: deadlineJob, ActorUserID: fixture.actor.ID,
		AuditEventID: scheduleAudit.ID.String(), AuditAt: model.GetMillis()}, command)
	requireNoError(t, err)
	if created.Replayed || created.Value == nil || created.Value.Sitting == nil || created.Value.Sitting.ID != sitting.ID || created.Value.Sitting.Revision != 1 {
		t.Fatalf("Schedule()=%#v", created)
	}
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
		AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID.String(), AuditAt: model.GetMillis()},
		examCommand(fixture.actor.ID, "exam.sitting.schedule.v1", "sitting-foreign-selection", "sitting-foreign-selection-command"))
	requireExamSittingConflict(t, err, "exam_sitting_revision_lineage")

	newStart, newEnd := start.Add(time.Hour), end.Add(time.Hour)
	updatedOpen, updatedDeadline := newExamSittingLifecycleJobs(t, sitting.ID, 2, newStart, newEnd)
	updateAudit := saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID)
	updated, err := ss.ExamSitting().UpdateSchedule(ctx, &store.ExamSittingScheduleUpdate{ExamID: fixture.examID, SittingID: sitting.ID,
		ActorUserID: fixture.actor.ID, ExpectedRevision: 1, ExamRevisionID: fixture.revisionID, ClassID: fixture.class.ID,
		ScheduledStartAt: newStart, ScheduledEndAt: newEnd, OpenJob: updatedOpen, DeadlineJob: updatedDeadline,
		ChangedAt: model.NowUTC(), AuditEventID: updateAudit.ID.String(), AuditAt: model.GetMillis()},
		examCommand(fixture.actor.ID, "exam.sitting.schedule.update.v1", "sitting-update", "sitting-update-command"))
	requireNoError(t, err)
	if updated.Value.Sitting.Revision != 2 || updated.Value.Sitting.ScheduledStartAt != newStart || updated.Value.Sitting.ScheduledEndAt != newEnd {
		t.Fatalf("UpdateSchedule()=%#v", updated)
	}

	cancelAudit := saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID)
	cancelCommand := examCommand(fixture.actor.ID, "exam.sitting.cancel.v1", "sitting-cancel", "sitting-cancel-command")
	canceled, err := ss.ExamSitting().Cancel(ctx, &store.ExamSittingCancellation{ExamID: fixture.examID, SittingID: sitting.ID,
		ActorUserID: fixture.actor.ID, ExpectedRevision: 2, PrivateReason: "Room unavailable", CanceledAt: model.NowUTC(),
		AuditEventID: cancelAudit.ID.String(), AuditAt: model.GetMillis()}, cancelCommand)
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
		AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID.String(), AuditAt: model.GetMillis()}, command)
	requireNoError(t, err)
	if !retry.Replayed || retry.Value.Sitting.State != model.ExamSittingScheduled || retry.Value.Sitting.Revision != 1 {
		t.Fatalf("Schedule(replay)=%#v", retry)
	}

	staleAudit := saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID)
	_, err = ss.ExamSitting().Cancel(ctx, &store.ExamSittingCancellation{ExamID: fixture.examID, SittingID: sitting.ID, ActorUserID: fixture.actor.ID,
		ExpectedRevision: 2, PrivateReason: "stale", CanceledAt: model.NowUTC(), AuditEventID: staleAudit.ID.String(), AuditAt: model.GetMillis()},
		examCommand(fixture.actor.ID, "exam.sitting.cancel.v1", "sitting-cancel-stale", "sitting-cancel-stale-command"))
	requireExamSittingConflict(t, err, "exam_sitting_revision")

	// A second Sitting makes list filtering and tuple pagination observable.
	secondStart := newStart.Add(24 * time.Hour)
	second, err := model.NewExamSitting(model.NewExamSittingID(), fixture.examID, fixture.revisionID, fixture.class.ID, secondStart, secondStart.Add(time.Hour), model.NowUTC())
	requireNoError(t, err)
	secondOpen, secondDeadline := newExamSittingLifecycleJobs(t, second.ID, 1, second.ScheduledStartAt, second.ScheduledEndAt)
	secondResult, err := ss.ExamSitting().Schedule(ctx, &store.ExamSittingSchedule{Sitting: second, OpenJob: secondOpen, DeadlineJob: secondDeadline, ActorUserID: fixture.actor.ID,
		AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID.String(), AuditAt: model.GetMillis()},
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
			AuditAt: model.GetMillis()}, examCommand(fixture.actor.ID, "exam.sitting.schedule.v1", key, key+"-command"))
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
		AuditAt: model.GetMillis()}, examCommand(fixture.actor.ID, "exam.sitting.schedule.v1", "sitting-job-rollback", "sitting-job-rollback-command"))
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
		AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID.String(), AuditAt: model.GetMillis()},
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
			AuditEventID: saveExamSittingAudit(t, ctx, ss, actor, fixture.examID, fixture.unitID).ID.String(), AuditAt: model.GetMillis()},
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
	added, err := ss.ExamAuthoring().AddManager(ctx,
		newExamManagerMutation(t, ctx, ss, fixture.examID, fixture.actor.ID, removedManager.ID, exam.Revision, model.NowUTC(), false),
		examCommand(fixture.actor.ID, "exam.manager.add.v1", "sitting-manager-add", "sitting-manager-add-command"))
	requireNoError(t, err)
	_, err = ss.ExamAuthoring().RemoveManager(ctx,
		newExamManagerMutation(t, ctx, ss, fixture.examID, fixture.actor.ID, removedManager.ID, added.Exam.Revision, model.NowUTC(), false),
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
		AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID.String(), AuditAt: model.GetMillis()},
		examCommand(fixture.actor.ID, "exam.sitting.cancel.v1", "sitting-precedence-cancel", "sitting-precedence-cancel-command"))
	requireNoError(t, err)
	invalidUpdate := func(expected int64, key string) error {
		openJob, deadlineJob := newExamSittingLifecycleJobs(t, precedence.Value.Sitting.ID, expected+1,
			fixture.period.StartsAt.Add(11*time.Hour), fixture.period.StartsAt.Add(12*time.Hour))
		_, updateErr := ss.ExamSitting().UpdateSchedule(ctx, &store.ExamSittingScheduleUpdate{ExamID: fixture.examID,
			SittingID: precedence.Value.Sitting.ID, ActorUserID: fixture.actor.ID, ExpectedRevision: expected,
			ExamRevisionID: fixture.revisionID, ClassID: foreignClass.ID, ScheduledStartAt: fixture.period.StartsAt.Add(11 * time.Hour),
			ScheduledEndAt: fixture.period.StartsAt.Add(12 * time.Hour), OpenJob: openJob, DeadlineJob: deadlineJob, ChangedAt: model.NowUTC(),
			AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID.String(), AuditAt: model.GetMillis()},
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
			AuditEventID: updateAudit.ID.String(), AuditAt: model.GetMillis()},
			examCommand(fixture.actor.ID, "exam.sitting.schedule.update.v1", "sitting-race-update", "sitting-race-update-command"))
	}()
	go func() {
		defer wait.Done()
		_, cancelErr = ss.ExamSitting().Cancel(ctx, &store.ExamSittingCancellation{ExamID: fixture.examID,
			SittingID: race.Value.Sitting.ID, ActorUserID: fixture.actor.ID, ExpectedRevision: 1,
			PrivateReason: "race", CanceledAt: model.NowUTC(), AuditEventID: cancelAudit.ID.String(), AuditAt: model.GetMillis()},
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
