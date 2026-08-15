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
	scheduleAudit := saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID)
	command := examCommand(fixture.actor.ID, "exam.sitting.schedule.v1", "sitting-schedule", "sitting-schedule-command")
	created, err := ss.ExamSitting().Schedule(ctx, &store.ExamSittingSchedule{Sitting: sitting, ActorUserID: fixture.actor.ID,
		AuditEventID: scheduleAudit.ID.String(), AuditAt: model.GetMillis()}, command)
	requireNoError(t, err)
	if created.Replayed || created.Value == nil || created.Value.Sitting == nil || created.Value.Sitting.ID != sitting.ID || created.Value.Sitting.Revision != 1 {
		t.Fatalf("Schedule()=%#v", created)
	}
	exact, err := ss.ExamSitting().Get(ctx, fixture.examID, sitting.ID)
	requireNoError(t, err)
	if exact.Sitting.ExamRevisionID != fixture.revisionID || exact.Sitting.ClassID != fixture.class.ID || exact.Sitting.ScheduledEndAt != end {
		t.Fatalf("Get()=%#v", exact)
	}
	resolved, err := ss.ExamSitting().Resolve(ctx, sitting.ID)
	requireNoError(t, err)
	if resolved.Sitting.ID != sitting.ID || resolved.Sitting.ExamID != fixture.examID {
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
	_, err = ss.ExamSitting().Schedule(ctx, &store.ExamSittingSchedule{Sitting: foreignSelection, ActorUserID: fixture.actor.ID,
		AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID.String(), AuditAt: model.GetMillis()},
		examCommand(fixture.actor.ID, "exam.sitting.schedule.v1", "sitting-foreign-selection", "sitting-foreign-selection-command"))
	requireExamSittingConflict(t, err, "exam_sitting_revision_lineage")

	newStart, newEnd := start.Add(time.Hour), end.Add(time.Hour)
	updateAudit := saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID)
	updated, err := ss.ExamSitting().UpdateSchedule(ctx, &store.ExamSittingScheduleUpdate{ExamID: fixture.examID, SittingID: sitting.ID,
		ActorUserID: fixture.actor.ID, ExpectedRevision: 1, ExamRevisionID: fixture.revisionID, ClassID: fixture.class.ID,
		ScheduledStartAt: newStart, ScheduledEndAt: newEnd, ChangedAt: model.NowUTC(), AuditEventID: updateAudit.ID.String(), AuditAt: model.GetMillis()},
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
	retry, err := ss.ExamSitting().Schedule(ctx, &store.ExamSittingSchedule{Sitting: sitting, ActorUserID: fixture.actor.ID,
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
	secondResult, err := ss.ExamSitting().Schedule(ctx, &store.ExamSittingSchedule{Sitting: second, ActorUserID: fixture.actor.ID,
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
		return ss.ExamSitting().Schedule(ctx, &store.ExamSittingSchedule{Sitting: sitting, ActorUserID: actor,
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
		_, updateErr := ss.ExamSitting().UpdateSchedule(ctx, &store.ExamSittingScheduleUpdate{ExamID: fixture.examID,
			SittingID: precedence.Value.Sitting.ID, ActorUserID: fixture.actor.ID, ExpectedRevision: expected,
			ExamRevisionID: fixture.revisionID, ClassID: foreignClass.ID, ScheduledStartAt: fixture.period.StartsAt.Add(11 * time.Hour),
			ScheduledEndAt: fixture.period.StartsAt.Add(12 * time.Hour), ChangedAt: model.NowUTC(),
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
	var updateErr, cancelErr error
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		_, updateErr = ss.ExamSitting().UpdateSchedule(ctx, &store.ExamSittingScheduleUpdate{ExamID: fixture.examID,
			SittingID: race.Value.Sitting.ID, ActorUserID: fixture.actor.ID, ExpectedRevision: 1,
			ExamRevisionID: fixture.revisionID, ClassID: fixture.class.ID, ScheduledStartAt: fixture.period.StartsAt.Add(15 * time.Hour),
			ScheduledEndAt: fixture.period.StartsAt.Add(16 * time.Hour), ChangedAt: model.NowUTC(),
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

func requireExamSittingConflict(t *testing.T, err error, constraint string) {
	t.Helper()
	var conflict *store.ErrConflict
	if !errors.As(err, &conflict) || conflict.Constraint != constraint {
		t.Fatalf("error=%v want conflict %q", err, constraint)
	}
}
