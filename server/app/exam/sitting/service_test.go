// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sitting

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

var testNow = time.Date(2026, 8, 15, 9, 0, 0, 0, time.UTC)

func TestScheduleCreatesAuthorizedSittingAndPublishesSafeEffect(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	start, end := testNow.Add(time.Hour), testNow.Add(3*time.Hour)
	command := ScheduleCommand{
		ExamID: fixture.examID, ExamRevisionID: fixture.revisionID, ClassID: fixture.classID,
		ScheduledStartAt: start, ScheduledEndAt: end, IdempotencyKey: "test-key",
	}

	got, err := fixture.service.ScheduleExamSitting(context.Background(), fixture.invocation, command)
	if err != nil {
		t.Fatal(err)
	}
	if got.Sitting == nil || got.Sitting.ID != fixture.sittingID || got.Sitting.State != model.ExamSittingScheduled {
		t.Fatalf("Schedule() = %#v", got)
	}
	if fixture.authorizer.action != model.ActionExamSittingCreate || fixture.authorizer.resource != (model.Resource{Type: model.ResourceExam, ID: fixture.examID.String()}) {
		t.Fatalf("authorization = %q %#v", fixture.authorizer.action, fixture.authorizer.resource)
	}
	if fixture.persistence.schedule == nil || fixture.persistence.schedule.ManagerOverride || fixture.persistence.schedule.ActorUserID != fixture.userID ||
		fixture.persistence.schedule.Sitting.ID != fixture.sittingID || fixture.persistence.schedule.Sitting.ScheduledStartAt != start || fixture.persistence.command == nil ||
		fixture.persistence.schedule.OpenJob != fixture.jobs.open || fixture.persistence.schedule.DeadlineJob != fixture.jobs.deadline ||
		fixture.jobs.boundaryRevision != 1 || !fixture.jobs.boundaryStart.Equal(start) || !fixture.jobs.boundaryEnd.Equal(end) ||
		fixture.persistence.schedule.Mail != fixture.mail.result || fixture.mail.request.ChangeKind != store.ExamSittingMailScheduled {
		t.Fatalf("store schedule = %#v, command=%#v", fixture.persistence.schedule, fixture.persistence.command)
	}
	wantIdempotency, err := prepareScheduleIdempotency(fixture.invocation, command)
	if err != nil {
		t.Fatal(err)
	}
	assertStoreBoundaryCommand(t, fixture.persistence.command, wantIdempotency)
	wantAudit := map[string]any{
		"exam_id": fixture.examID.String(), "exam_sitting_id": fixture.sittingID.String(),
		"exam_revision_id": fixture.revisionID.String(), "class_id": fixture.classID.String(),
	}
	if fixture.auditor.action != model.ActionExamSittingCreate || fixture.auditor.operation != "schedule" || !reflect.DeepEqual(fixture.auditor.value, wantAudit) {
		t.Fatalf("audit = %q %q %#v", fixture.auditor.action, fixture.auditor.operation, fixture.auditor.value)
	}
	if len(fixture.effects.events) != 1 || fixture.effects.events[0] != (effectEvent{kind: "scheduled", examID: fixture.examID, sittingID: fixture.sittingID, state: model.ExamSittingScheduled, revision: 1, at: testNow}) {
		t.Fatalf("effects = %#v", fixture.effects.events)
	}
}

func TestGetAuthorizesExactSittingResource(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	fixture.persistence.snapshot = &store.ExamSittingSnapshot{Sitting: fixture.sitting(t)}

	got, err := fixture.service.GetExamSitting(context.Background(), fixture.invocation, GetQuery{ExamID: fixture.examID, SittingID: fixture.sittingID})
	if err != nil {
		t.Fatal(err)
	}
	if got.Sitting == nil || got.Sitting.ID != fixture.sittingID {
		t.Fatalf("Get() = %#v", got)
	}
	if fixture.authorizer.action != model.ActionExamSittingView || fixture.authorizer.resource != (model.Resource{Type: model.ResourceExamSitting, ID: fixture.sittingID.String()}) {
		t.Fatalf("authorization = %q %#v", fixture.authorizer.action, fixture.authorizer.resource)
	}
	if fixture.persistence.getExamID != fixture.examID || fixture.persistence.getSittingID != fixture.sittingID {
		t.Fatalf("store Get() ids = %q %q", fixture.persistence.getExamID, fixture.persistence.getSittingID)
	}
}

func TestAuthorizeViewRechecksCurrentSittingRelationship(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	fixture.persistence.snapshot = &store.ExamSittingSnapshot{Sitting: fixture.sitting(t)}

	if err := fixture.service.AuthorizeView(context.Background(), fixture.invocation, fixture.sittingID); err != nil {
		t.Fatal(err)
	}
	if fixture.persistence.resolveID != fixture.sittingID || fixture.authorizer.action != model.ActionExamSittingView ||
		fixture.authorizer.resource != (model.Resource{Type: model.ResourceExamSitting, ID: fixture.sittingID.String()}) {
		t.Fatalf("subscription authorization = resolve %s action %q resource %#v", fixture.persistence.resolveID,
			fixture.authorizer.action, fixture.authorizer.resource)
	}

	fixture.memberships.items = nil
	if err := fixture.service.AuthorizeView(context.Background(), fixture.invocation, fixture.sittingID); err != nil {
		t.Fatal(err)
	}
	if fixture.authorizer.action != model.ActionExamSittingViewOverride {
		t.Fatalf("subscription override action = %q", fixture.authorizer.action)
	}
}

func TestBrowserActivityRequiresCurrentExactManagerAndDedicatedPermission(t *testing.T) {
	for _, test := range []struct {
		name                                                      string
		removeManager, removeUnit, denyPermission, failMembership bool
	}{
		{name: "exact manager"}, {name: "administrator grant without manager", removeManager: true},
		{name: "manager without exact unit", removeUnit: true}, {name: "manager without action", denyPermission: true},
		{name: "unrelated unit membership is not consulted", failMembership: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newFixture(t)
			f.persistence.snapshot = &store.ExamSittingSnapshot{Sitting: f.sitting(t)}
			f.access.snapshot.ActorIsManager = !test.removeManager
			if test.removeUnit {
				f.memberships.items = nil
			}
			if test.denyPermission {
				f.authorizer.err = errors.New("permission denied")
			}
			if test.failMembership {
				f.memberships.err = errors.New("membership unavailable")
			}
			unit, err := f.service.AuthorizeBrowserActivityView(context.Background(), f.invocation, f.sittingID)
			wantAllowed := !test.removeManager && !test.denyPermission
			if (err == nil) != wantAllowed {
				t.Fatalf("AuthorizeBrowserActivityView() = %s, %v", unit, err)
			}
			if wantAllowed && unit != f.unitID {
				t.Fatal("wrong authorized unit")
			}
			if !wantAllowed && unit != "" {
				t.Fatal("denial returned a scope")
			}
			if f.authorizer.action != model.ActionExamAttemptBrowserActivityView ||
				f.authorizer.resource.ID != f.sittingID.String() {
				t.Fatal("used an unrelated action or resource")
			}
			if f.authorizer.denied != test.removeManager {
				t.Fatal("relationship denial was not audited")
			}
		})
	}
}

func TestBrowserActivityRechecksMembershipBetweenPagesAndFailsClosedOnAudit(t *testing.T) {
	f := newFixture(t)
	f.persistence.snapshot = &store.ExamSittingSnapshot{Sitting: f.sitting(t)}
	if _, err := f.service.AuthorizeBrowserActivityView(context.Background(), f.invocation, f.sittingID); err != nil {
		t.Fatal(err)
	}
	f.access.snapshot.ActorIsManager = false
	if _, err := f.service.AuthorizeBrowserActivityView(context.Background(), f.invocation, f.sittingID); err == nil || !f.authorizer.denied {
		t.Fatal("later page retained removed Manager authority")
	}
	auditErr := errors.New("audit unavailable")
	f.authorizer.denyErr = auditErr
	if _, err := f.service.AuthorizeBrowserActivityView(context.Background(), f.invocation, f.sittingID); !errors.Is(err, auditErr) {
		t.Fatalf("audit failure = %v", err)
	}
}

func TestAuthorizeManageReportsOrdinaryAndOverrideDecision(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	fixture.persistence.snapshot = &store.ExamSittingSnapshot{Sitting: fixture.sitting(t)}
	override, err := fixture.service.AuthorizeManage(context.Background(), fixture.invocation, fixture.sittingID)
	if err != nil || override || fixture.authorizer.action != model.ActionExamSittingManage {
		t.Fatalf("ordinary manage override=%t action=%q error=%v", override, fixture.authorizer.action, err)
	}
	fixture.memberships.items = nil
	override, err = fixture.service.AuthorizeManage(context.Background(), fixture.invocation, fixture.sittingID)
	if err != nil || !override || fixture.authorizer.action != model.ActionExamSittingManageOverride {
		t.Fatalf("override manage override=%t action=%q error=%v", override, fixture.authorizer.action, err)
	}
}

func TestAuthorizeSubmissionViewUsesCanonicalSubmissionResourceAndCurrentManagerRelationship(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	submissionID := model.NewSubmissionID()

	if err := fixture.service.AuthorizeSubmissionView(context.Background(), fixture.invocation, fixture.examID, submissionID); err != nil {
		t.Fatal(err)
	}
	if fixture.authorizer.action != model.ActionSubmissionView ||
		fixture.authorizer.resource != (model.Resource{Type: model.ResourceSubmission, ID: submissionID.String()}) {
		t.Fatalf("ordinary Submission authorization = %q %#v", fixture.authorizer.action, fixture.authorizer.resource)
	}

	fixture.memberships.items = nil
	if err := fixture.service.AuthorizeSubmissionView(context.Background(), fixture.invocation, fixture.examID, submissionID); err != nil {
		t.Fatal(err)
	}
	if fixture.authorizer.action != model.ActionSubmissionViewOverride {
		t.Fatalf("Submission override action = %q", fixture.authorizer.action)
	}
}

func TestAuthorizeSubmissionReviewAndReleaseUseDistinctCurrentPermissions(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	submissionID := model.NewSubmissionID()

	override, err := fixture.service.AuthorizeSubmissionReview(context.Background(), fixture.invocation, fixture.examID, submissionID)
	if err != nil || override || fixture.authorizer.action != model.ActionSubmissionReview {
		t.Fatalf("ordinary Review authorization override=%t action=%q error=%v", override, fixture.authorizer.action, err)
	}
	override, err = fixture.service.AuthorizeSubmissionRelease(context.Background(), fixture.invocation, fixture.examID, submissionID)
	if err != nil || override || fixture.authorizer.action != model.ActionSubmissionRelease {
		t.Fatalf("ordinary release authorization override=%t action=%q error=%v", override, fixture.authorizer.action, err)
	}

	fixture.memberships.items = nil
	override, err = fixture.service.AuthorizeSubmissionReview(context.Background(), fixture.invocation, fixture.examID, submissionID)
	if err != nil || !override || fixture.authorizer.action != model.ActionSubmissionReviewOverride {
		t.Fatalf("Review override authorization override=%t action=%q error=%v", override, fixture.authorizer.action, err)
	}
	override, err = fixture.service.AuthorizeSubmissionRelease(context.Background(), fixture.invocation, fixture.examID, submissionID)
	if err != nil || !override || fixture.authorizer.action != model.ActionSubmissionReleaseOverride {
		t.Fatalf("release override authorization override=%t action=%q error=%v", override, fixture.authorizer.action, err)
	}
}

func TestListAppliesBoundedFiltersAndLookAhead(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	first := fixture.sitting(t)
	second := *first
	second.ID = model.NewExamSittingID()
	second.ScheduledStartAt = first.ScheduledStartAt.Add(-time.Hour)
	second.ScheduledEndAt = first.ScheduledEndAt.Add(-time.Hour)
	fixture.persistence.items = []store.ExamSittingSnapshot{{Sitting: first}, {Sitting: &second}}
	overlapStart, overlapEnd := testNow.Add(30*time.Minute), testNow.Add(4*time.Hour)

	page, err := fixture.service.ListExamSittings(context.Background(), fixture.invocation, ListQuery{
		ExamID: fixture.examID, ClassID: fixture.classID, States: []model.ExamSittingState{model.ExamSittingScheduled},
		OverlapStartAt: overlapStart, OverlapEndAt: overlapEnd, Limit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || !page.HasMore || page.Items[0].Sitting.ID != first.ID {
		t.Fatalf("List() = %#v", page)
	}
	if fixture.authorizer.action != model.ActionExamView || fixture.authorizer.resource != (model.Resource{Type: model.ResourceExam, ID: fixture.examID.String()}) {
		t.Fatalf("authorization = %q %#v", fixture.authorizer.action, fixture.authorizer.resource)
	}
	want := store.ExamSittingListOptions{ExamID: fixture.examID, ClassID: fixture.classID,
		States: []model.ExamSittingState{model.ExamSittingScheduled}, OverlapStartAt: overlapStart,
		OverlapEndAt: overlapEnd, Limit: 2}
	if !reflect.DeepEqual(fixture.persistence.listOptions, want) {
		t.Fatalf("store List options = %#v", fixture.persistence.listOptions)
	}
}

func TestUpdateScheduleUsesSittingManagementAndOptimisticFence(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	updated := fixture.sitting(t)
	current := fixture.sitting(t)
	changedAt := testNow
	changedStart, changedEnd := testNow.Add(2*time.Hour), testNow.Add(5*time.Hour)
	changed, err := updated.ApplySchedule(fixture.revisionID, fixture.classID, changedStart, changedEnd, changedAt)
	if err != nil || !changed {
		t.Fatalf("ApplySchedule() = %v, %v", changed, err)
	}
	fixture.persistence.snapshot = &store.ExamSittingSnapshot{Sitting: current}
	fixture.persistence.result = &store.ExamSittingCommandResult{Value: &store.ExamSittingSnapshot{Sitting: updated}}

	command := UpdateScheduleCommand{
		ExamID: fixture.examID, SittingID: fixture.sittingID, ExpectedRevision: 1,
		ScheduledStartAt: &changedStart, ScheduledEndAt: &changedEnd, IdempotencyKey: "test-key",
	}
	got, err := fixture.service.UpdateExamSittingSchedule(context.Background(), fixture.invocation, command)
	if err != nil {
		t.Fatal(err)
	}
	if got.Sitting == nil || got.Sitting.Revision != 2 {
		t.Fatalf("UpdateSchedule() = %#v", got)
	}
	if fixture.authorizer.action != model.ActionExamSittingManage || fixture.authorizer.resource.Type != model.ResourceExamSitting {
		t.Fatalf("authorization = %q %#v", fixture.authorizer.action, fixture.authorizer.resource)
	}
	input := fixture.persistence.update
	if input == nil || input.ExpectedRevision != 1 || input.ManagerOverride || fixture.persistence.command == nil {
		t.Fatalf("store update = %#v, command=%#v", input, fixture.persistence.command)
	}
	wantIdempotency, err := prepareScheduleUpdateIdempotency(fixture.invocation, command)
	if err != nil {
		t.Fatal(err)
	}
	assertStoreBoundaryCommand(t, fixture.persistence.command, wantIdempotency)
	if input.OpenJob != fixture.jobs.open || input.DeadlineJob != fixture.jobs.deadline || fixture.jobs.boundaryRevision != 2 ||
		!fixture.jobs.boundaryStart.Equal(changedStart) || !fixture.jobs.boundaryEnd.Equal(changedEnd) {
		t.Fatalf("boundary Jobs = %#v %#v, factory revision/times = %d %v %v", input.OpenJob, input.DeadlineJob,
			fixture.jobs.boundaryRevision, fixture.jobs.boundaryStart, fixture.jobs.boundaryEnd)
	}
	if input.Mail != fixture.mail.result || fixture.mail.request.ChangeKind != store.ExamSittingMailRescheduled ||
		fixture.mail.request.PriorClassID != fixture.classID || fixture.mail.request.SittingRevision != 2 {
		t.Fatalf("mail preparation = %#v, store=%#v", fixture.mail.request, input.Mail)
	}
	if input.ExamRevisionID != fixture.revisionID || input.ClassID != fixture.classID || !input.ScheduledStartAt.Equal(changedStart) || !input.ScheduledEndAt.Equal(changedEnd) {
		t.Fatalf("merged store update = %#v", input)
	}
	wantAudit := map[string]any{"exam_id": fixture.examID.String(), "exam_sitting_id": fixture.sittingID.String(),
		"exam_revision_id": fixture.revisionID.String(), "class_id": fixture.classID.String(), "expected_sitting_revision": int64(1)}
	if fixture.auditor.operation != "update_schedule" || !reflect.DeepEqual(fixture.auditor.value, wantAudit) {
		t.Fatalf("audit = %q %#v", fixture.auditor.operation, fixture.auditor.value)
	}
	if len(fixture.effects.events) != 1 || fixture.effects.events[0].kind != "schedule_updated" || fixture.effects.events[0].revision != 2 {
		t.Fatalf("effects = %#v", fixture.effects.events)
	}
}

func TestUpdateScheduleReturnsCurrentNoChangesOnlyForCurrentActiveScheduledState(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	current := fixture.sitting(t)
	fixture.persistence.snapshot = &store.ExamSittingSnapshot{Sitting: current}
	revisionID := fixture.revisionID

	_, err := fixture.service.UpdateExamSittingSchedule(context.Background(), fixture.invocation, UpdateScheduleCommand{
		ExamID: fixture.examID, SittingID: fixture.sittingID, ExpectedRevision: 1,
		ExamRevisionID: &revisionID, IdempotencyKey: "test-key",
	})
	assertFaultCode(t, err, "exam.sitting.no_changes")
	if fixture.persistence.update != nil || fixture.auditor.operation != "" {
		t.Fatalf("no-op reached mutation boundary: update=%#v audit=%q", fixture.persistence.update, fixture.auditor.operation)
	}
}

func TestUpdateScheduleSendsStaleNoOpToStore(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	current := fixture.sitting(t)
	current.Revision = 2
	fixture.persistence.snapshot = &store.ExamSittingSnapshot{Sitting: current}
	fixture.persistence.err = store.NewErrConflict("exam_sitting", "exam_sitting_revision", nil)
	revisionID := fixture.revisionID

	_, err := fixture.service.UpdateExamSittingSchedule(context.Background(), fixture.invocation, UpdateScheduleCommand{
		ExamID: fixture.examID, SittingID: fixture.sittingID, ExpectedRevision: 1,
		ExamRevisionID: &revisionID, IdempotencyKey: "test-key",
	})
	assertFaultCode(t, err, "exam.sitting.revision_conflict")
	if fixture.persistence.update == nil || fixture.auditor.operation != "update_schedule" {
		t.Fatalf("stale no-op did not reach mutation boundary: update=%#v audit=%q", fixture.persistence.update, fixture.auditor.operation)
	}
}

func TestUpdateScheduleSendsArchivedAndNonScheduledNoOpsToStore(t *testing.T) {
	t.Parallel()
	t.Run("archived exam", func(t *testing.T) {
		t.Parallel()
		fixture := newFixture(t)
		fixture.persistence.snapshot = &store.ExamSittingSnapshot{Sitting: fixture.sitting(t)}
		if err := fixture.access.snapshot.Exam.Archive(testNow); err != nil {
			t.Fatal(err)
		}
		fixture.persistence.err = store.NewErrConflict("exam_sitting", "exam_archived", nil)
		revisionID := fixture.revisionID

		_, err := fixture.service.UpdateExamSittingSchedule(context.Background(), fixture.invocation, UpdateScheduleCommand{
			ExamID: fixture.examID, SittingID: fixture.sittingID, ExpectedRevision: 1,
			ExamRevisionID: &revisionID, IdempotencyKey: "test-key",
		})
		assertFaultCode(t, err, "exam.archived")
		if fixture.persistence.update == nil {
			t.Fatal("archived no-op did not reach Store")
		}
	})

	t.Run("non-scheduled sitting", func(t *testing.T) {
		t.Parallel()
		fixture := newFixture(t)
		current := fixture.sitting(t)
		if err := current.Cancel(model.ExamSittingReasonManagerCanceled, testNow); err != nil {
			t.Fatal(err)
		}
		fixture.persistence.snapshot = &store.ExamSittingSnapshot{Sitting: current}
		fixture.persistence.err = store.NewErrConflict("exam_sitting", "exam_sitting_state", nil)
		revisionID := fixture.revisionID

		_, err := fixture.service.UpdateExamSittingSchedule(context.Background(), fixture.invocation, UpdateScheduleCommand{
			ExamID: fixture.examID, SittingID: fixture.sittingID, ExpectedRevision: current.Revision,
			ExamRevisionID: &revisionID, IdempotencyKey: "test-key",
		})
		assertFaultCode(t, err, "exam.sitting.state_conflict")
		if fixture.persistence.update == nil {
			t.Fatal("state-conflicting no-op did not reach Store")
		}
	})
}

func TestScheduleUsesExplicitOverrideWithoutCurrentExactUnitMembership(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	fixture.memberships.items = nil
	start, end := testNow.Add(time.Hour), testNow.Add(3*time.Hour)

	_, err := fixture.service.ScheduleExamSitting(context.Background(), fixture.invocation, ScheduleCommand{
		ExamID: fixture.examID, ExamRevisionID: fixture.revisionID, ClassID: fixture.classID,
		ScheduledStartAt: start, ScheduledEndAt: end, IdempotencyKey: "test-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	if fixture.authorizer.action != model.ActionExamSittingCreateOverride || fixture.persistence.schedule == nil || !fixture.persistence.schedule.ManagerOverride {
		t.Fatalf("override authorization/store = %q %#v", fixture.authorizer.action, fixture.persistence.schedule)
	}
}

func TestScheduleAuthorizationFailureStopsMutationAndAudit(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	fixture.authorizer.err = errors.New("denied")
	start, end := testNow.Add(time.Hour), testNow.Add(3*time.Hour)

	_, err := fixture.service.ScheduleExamSitting(context.Background(), fixture.invocation, ScheduleCommand{
		ExamID: fixture.examID, ExamRevisionID: fixture.revisionID, ClassID: fixture.classID,
		ScheduledStartAt: start, ScheduledEndAt: end, IdempotencyKey: "test-key",
	})
	if err == nil || fixture.persistence.schedule != nil || fixture.auditor.operation != "" {
		t.Fatalf("unauthorized mutation continued: err=%v store=%#v audit=%q", err, fixture.persistence.schedule, fixture.auditor.operation)
	}
}

func TestScheduleStopsWhenCurrentManagerMembershipCannotBeRead(t *testing.T) {
	t.Parallel()
	testFixture := newFixture(t)
	failure := errors.New("membership unavailable")
	testFixture.memberships.err = failure
	_, err := testFixture.service.ScheduleExamSitting(context.Background(), testFixture.invocation, ScheduleCommand{
		ExamID: testFixture.examID, ExamRevisionID: testFixture.revisionID, ClassID: testFixture.classID,
		ScheduledStartAt: testNow.Add(time.Hour), ScheduledEndAt: testNow.Add(3 * time.Hour), IdempotencyKey: "test-key",
	})
	var fault *apperror.Error
	if !errors.As(err, &fault) || fault.Code() != "exam.sitting.unavailable" || !errors.Is(err, failure) {
		t.Fatalf("error = %v, want unavailable membership failure", err)
	}
	if testFixture.authorizer.action != "" || testFixture.persistence.schedule != nil || testFixture.auditor.operation != "" {
		t.Fatalf("membership failure continued: action=%q schedule=%#v audit=%q", testFixture.authorizer.action, testFixture.persistence.schedule, testFixture.auditor.operation)
	}
}

func TestReplayedCommandsDoNotPublishEffects(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	original := fixture.sitting(t)
	original.ID = model.NewExamSittingID()
	fixture.persistence.result = &store.ExamSittingCommandResult{
		Value: &store.ExamSittingSnapshot{Sitting: original}, Replayed: true,
	}
	start, end := testNow.Add(time.Hour), testNow.Add(3*time.Hour)

	got, err := fixture.service.ScheduleExamSitting(context.Background(), fixture.invocation, ScheduleCommand{
		ExamID: fixture.examID, ExamRevisionID: fixture.revisionID, ClassID: fixture.classID,
		ScheduledStartAt: start, ScheduledEndAt: end, IdempotencyKey: "test-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Sitting == nil || got.Sitting.ID != original.ID {
		t.Fatalf("replayed Sitting = %#v, want original %s", got, original.ID)
	}
	if len(fixture.effects.events) != 0 {
		t.Fatalf("replayed effects = %#v", fixture.effects.events)
	}
}

func TestReplayedUpdateAndCancellationDoNotPublishEffects(t *testing.T) {
	t.Parallel()
	t.Run("update", func(t *testing.T) {
		t.Parallel()
		fixture := newFixture(t)
		fixture.persistence.snapshot = &store.ExamSittingSnapshot{Sitting: fixture.sitting(t)}
		updated := fixture.sitting(t)
		start, end := testNow.Add(2*time.Hour), testNow.Add(5*time.Hour)
		if _, err := updated.ApplySchedule(fixture.revisionID, fixture.classID, start, end, testNow); err != nil {
			t.Fatal(err)
		}
		fixture.persistence.result = &store.ExamSittingCommandResult{Value: &store.ExamSittingSnapshot{Sitting: updated}, Replayed: true}

		_, err := fixture.service.UpdateExamSittingSchedule(context.Background(), fixture.invocation, UpdateScheduleCommand{
			ExamID: fixture.examID, SittingID: fixture.sittingID, ExpectedRevision: 1,
			ScheduledStartAt: &start, ScheduledEndAt: &end, IdempotencyKey: "test-key",
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(fixture.effects.events) != 0 {
			t.Fatalf("replayed update effects = %#v", fixture.effects.events)
		}
	})

	t.Run("cancel", func(t *testing.T) {
		t.Parallel()
		fixture := newFixture(t)
		fixture.persistence.snapshot = &store.ExamSittingSnapshot{Sitting: fixture.sitting(t)}
		canceled := fixture.sitting(t)
		if err := canceled.Cancel(model.ExamSittingReasonManagerCanceled, testNow); err != nil {
			t.Fatal(err)
		}
		fixture.persistence.result = &store.ExamSittingCommandResult{Value: &store.ExamSittingSnapshot{Sitting: canceled}, Replayed: true}

		_, err := fixture.service.CancelExamSitting(context.Background(), fixture.invocation, CancelCommand{
			ExamID: fixture.examID, SittingID: fixture.sittingID, ExpectedRevision: 1,
			PrivateReason: "Manager canceled", IdempotencyKey: "test-key",
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(fixture.effects.events) != 0 {
			t.Fatalf("replayed cancel effects = %#v", fixture.effects.events)
		}
	})
}

func TestGetRejectsMismatchedExamOwnership(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	foreign := fixture.sitting(t)
	foreign.ExamID = model.NewExamID()
	fixture.persistence.snapshot = &store.ExamSittingSnapshot{Sitting: foreign}

	_, err := fixture.service.GetExamSitting(context.Background(), fixture.invocation, GetQuery{ExamID: fixture.examID, SittingID: fixture.sittingID})
	assertFaultCode(t, err, "exam.sitting.unavailable")
}

func TestPrivateCancellationReasonBounds(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		reason string
		valid  bool
	}{
		{name: "ordinary", reason: "Proctor ended the sitting", valid: true},
		{name: "empty", reason: ""},
		{name: "leading whitespace", reason: " reason"},
		{name: "trailing whitespace", reason: "reason\n"},
		{name: "invalid UTF-8", reason: string([]byte{0xff})},
		{name: "maximum UTF-8", reason: strings.Repeat("😀", 1000), valid: true},
		{name: "over rune and byte bounds", reason: strings.Repeat("😀", 1001)},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := validPrivateReason(test.reason); got != test.valid {
				t.Fatalf("validPrivateReason() = %v, want %v", got, test.valid)
			}
		})
	}
}

func TestStoreConflictsHaveStableFaults(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"exam_sitting_revision":                "exam.sitting.revision_conflict",
		"exam_sitting_no_changes":              "exam.sitting.no_changes",
		"exam_sitting_state":                   "exam.sitting.state_conflict",
		"exam_sitting_extension":               "exam.sitting.extension_not_later",
		"exam_sitting_extension_not_later":     "exam.sitting.extension_not_later",
		"exam_sitting_deadline_reached":        "exam.sitting.deadline_reached",
		"exam_sitting_class_lineage":           "exam.sitting.class_ineligible",
		"exam_sitting_period_containment":      "exam.sitting.schedule_outside_period",
		"exam_sitting_schedule_outside_period": "exam.sitting.schedule_outside_period",
		"exam_sitting_not_future":              "exam.sitting.schedule_not_future",
		"exam_sitting_revision_lineage":        "exam.sitting.not_found",
		"exam_archived":                        "exam.archived",
	}
	for constraint, want := range tests {
		constraint, want := constraint, want
		t.Run(constraint, func(t *testing.T) {
			t.Parallel()
			assertFaultCode(t, storeErrors.Translate(store.NewErrConflict("exam_sitting", constraint, nil)), want)
		})
	}
}

func TestListRejectsUnboundedAndPartialFilters(t *testing.T) {
	t.Parallel()
	examID, sittingID := model.NewExamID(), model.NewExamSittingID()
	tests := map[string]ListQuery{
		"zero limit":        {ExamID: examID},
		"excess limit":      {ExamID: examID, Limit: 201},
		"invalid class":     {ExamID: examID, ClassID: model.ClassID("invalid"), Limit: 20},
		"partial cursor at": {ExamID: examID, BeforeScheduledStartAt: testNow, Limit: 20},
		"partial cursor id": {ExamID: examID, BeforeSittingID: sittingID, Limit: 20},
		"partial overlap":   {ExamID: examID, OverlapStartAt: testNow, Limit: 20},
		"reversed overlap":  {ExamID: examID, OverlapStartAt: testNow, OverlapEndAt: testNow.Add(-time.Minute), Limit: 20},
		"invalid state":     {ExamID: examID, States: []model.ExamSittingState{"unknown"}, Limit: 20},
		"duplicate state":   {ExamID: examID, States: []model.ExamSittingState{model.ExamSittingScheduled, model.ExamSittingScheduled}, Limit: 20},
		"too many states": {ExamID: examID, States: []model.ExamSittingState{
			model.ExamSittingScheduled, model.ExamSittingOpen, model.ExamSittingPaused, model.ExamSittingClosing,
			model.ExamSittingClosed, model.ExamSittingCanceled, model.ExamSittingScheduled,
		}, Limit: 20},
	}
	for name, query := range tests {
		name, query := name, query
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := listOptions(query); err == nil {
				t.Fatal("listOptions() accepted invalid query")
			}
		})
	}
}

func TestUpdateRejectsMismatchedStoreOutcome(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	fixture.persistence.snapshot = &store.ExamSittingSnapshot{Sitting: fixture.sitting(t)}
	foreign := fixture.sitting(t)
	foreign.ID = model.NewExamSittingID()
	start := testNow.Add(2 * time.Hour)
	fixture.persistence.result = &store.ExamSittingCommandResult{Value: &store.ExamSittingSnapshot{Sitting: foreign}}

	_, err := fixture.service.UpdateExamSittingSchedule(context.Background(), fixture.invocation, UpdateScheduleCommand{
		ExamID: fixture.examID, SittingID: fixture.sittingID, ExpectedRevision: 1,
		ScheduledStartAt: &start, IdempotencyKey: "test-key",
	})
	assertFaultCode(t, err, "exam.sitting.unavailable")
	if len(fixture.effects.events) != 0 {
		t.Fatalf("mismatched outcome effects = %#v", fixture.effects.events)
	}
}

func TestCancelRetainsPrivateReasonOnlyInStoreCommand(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	fixture.persistence.snapshot = &store.ExamSittingSnapshot{Sitting: fixture.sitting(t)}
	canceled := fixture.sitting(t)
	if err := canceled.Cancel(model.ExamSittingReasonManagerCanceled, testNow); err != nil {
		t.Fatal(err)
	}
	fixture.persistence.result = &store.ExamSittingCommandResult{Value: &store.ExamSittingSnapshot{Sitting: canceled}}

	command := CancelCommand{
		ExamID: fixture.examID, SittingID: fixture.sittingID, ExpectedRevision: 1,
		PrivateReason: "Room became unavailable", IdempotencyKey: "test-key",
	}
	got, err := fixture.service.CancelExamSitting(context.Background(), fixture.invocation, command)
	if err != nil {
		t.Fatal(err)
	}
	if got.Sitting == nil || got.Sitting.State != model.ExamSittingCanceled || got.Sitting.ReasonCode != model.ExamSittingReasonManagerCanceled {
		t.Fatalf("Cancel() = %#v", got)
	}
	if fixture.persistence.cancel == nil || fixture.persistence.cancel.PrivateReason != "Room became unavailable" || fixture.persistence.cancel.ExpectedRevision != 1 {
		t.Fatalf("store cancel = %#v", fixture.persistence.cancel)
	}
	if fixture.persistence.cancel.Mail != fixture.mail.result || fixture.mail.request.ChangeKind != store.ExamSittingMailCancelled {
		t.Fatalf("cancellation mail = %#v, store=%#v", fixture.mail.request, fixture.persistence.cancel.Mail)
	}
	wantIdempotency, err := prepareTransitionIdempotency(fixture.invocation, idempotencyOperationCancel, PauseCommand(command))
	if err != nil {
		t.Fatal(err)
	}
	assertStoreBoundaryCommand(t, fixture.persistence.command, wantIdempotency)
	wantAudit := map[string]any{"exam_id": fixture.examID.String(), "exam_sitting_id": fixture.sittingID.String(),
		"expected_sitting_revision": int64(1), "reason_code": string(model.ExamSittingReasonManagerCanceled)}
	if fixture.auditor.operation != "cancel" || !reflect.DeepEqual(fixture.auditor.value, wantAudit) {
		t.Fatalf("audit = %q %#v", fixture.auditor.operation, fixture.auditor.value)
	}
	if reflect.ValueOf(fixture.auditor.value).String() == "Room became unavailable" {
		t.Fatal("private cancellation reason leaked into audit")
	}
	if len(fixture.effects.events) != 1 || fixture.effects.events[0].kind != "canceled" || fixture.effects.events[0].state != model.ExamSittingCanceled {
		t.Fatalf("effects = %#v", fixture.effects.events)
	}
}

type fixture struct {
	service     *Service
	invocation  invocation.Invocation
	userID      model.UserID
	unitID      model.AcademicUnitID
	examID      model.ExamID
	revisionID  model.ExamRevisionID
	classID     model.ClassID
	sittingID   model.ExamSittingID
	persistence *sittingStoreFake
	access      *accessFake
	memberships *membershipsFake
	authorizer  *authorizerFake
	auditor     *auditorFake
	systemAudit *systemAuditorFake
	effects     *effectsFake
	jobs        *lifecycleJobFactoryFake
	mail        *scheduleMailPreparerFake
}

func (f fixture) sitting(t *testing.T) *model.ExamSitting {
	t.Helper()
	sitting, err := model.NewExamSitting(f.sittingID, f.examID, f.revisionID, f.classID,
		testNow.Add(time.Hour), testNow.Add(3*time.Hour), testNow)
	if err != nil {
		t.Fatal(err)
	}
	return sitting
}

func TestListNoShowsAuthorizesClosingSittingAndReturnsBoundedPage(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	sitting, err := model.NewExamSitting(fixture.sittingID, fixture.examID, fixture.revisionID, fixture.classID,
		testNow.Add(-2*time.Hour), testNow.Add(-time.Hour), testNow.Add(-3*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err = sitting.Open(testNow.Add(-2 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err = sitting.EnterClosing(model.ExamSittingReasonScheduledEndReached, testNow.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	fixture.persistence.snapshot = &store.ExamSittingSnapshot{Sitting: sitting, AcademicUnitID: fixture.unitID}
	ids := []model.UserID{model.NewUserID(), model.NewUserID(), model.NewUserID()}
	slices.SortFunc(ids, func(left, right model.UserID) int { return strings.Compare(left.String(), right.String()) })
	fixture.persistence.noShows = []store.ExamSittingNoShow{{CandidateUserID: ids[0]}, {CandidateUserID: ids[1]}, {CandidateUserID: ids[2]}}
	page, err := fixture.service.ListExamSittingNoShows(context.Background(), fixture.invocation, ListNoShowsQuery{ExamID: fixture.examID, SittingID: fixture.sittingID, AfterCandidateUserID: model.UserID(""), Limit: 2})
	if err != nil || len(page.Items) != 2 || !page.HasMore || page.Items[1].CandidateUserID != ids[1] ||
		fixture.persistence.noShowOptions.Limit != 3 {
		t.Fatalf("page=%#v options=%#v err=%v", page, fixture.persistence.noShowOptions, err)
	}
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	userID, unitID := model.NewUserID(), model.NewAcademicUnitID()
	examID, revisionID := model.NewExamID(), model.NewExamRevisionID()
	classID, sittingID := model.NewClassID(), model.NewExamSittingID()
	exam, err := model.NewExam(examID, unitID, userID, testNow.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	persistence := &sittingStoreFake{}
	access := &accessFake{snapshot: &store.ExamAccessSnapshot{Exam: exam, ActorIsManager: true}}
	memberships := &membershipsFake{items: []*model.AcademicUnitMember{{AcademicUnitID: unitID}}}
	authorizer, auditor, systemAudit, effects := &authorizerFake{}, &auditorFake{id: model.NewId()}, &systemAuditorFake{id: model.NewId()}, &effectsFake{}
	jobs := &lifecycleJobFactoryFake{open: &model.Job{}, deadline: &model.Job{}, finalize: &model.Job{}}
	mail := &scheduleMailPreparerFake{result: &store.ExamSittingMailFanout{}}
	service, err := New(Dependencies{
		Persistence:    persistence,
		Access:         access,
		Memberships:    memberships,
		Authorizer:     authorizer,
		Auditor:        auditor,
		SystemAuditor:  systemAudit,
		Effects:        effects,
		EffectFailures: effects,
		Jobs:           jobs,
		Mail:           mail,
		Now:            func() time.Time { return testNow },
		NewID:          func() model.ExamSittingID { return sittingID },
	})
	if err != nil {
		t.Fatal(err)
	}
	return fixture{service: service, invocation: invocation.New(testPrincipal(userID), model.RequestMetadata{}), userID: userID,
		unitID: unitID, examID: examID, revisionID: revisionID, classID: classID, sittingID: sittingID,
		persistence: persistence, access: access, memberships: memberships, authorizer: authorizer, auditor: auditor,
		systemAudit: systemAudit, effects: effects, jobs: jobs, mail: mail}
}

func assertFaultCode(t *testing.T, err error, want string) {
	t.Helper()
	var fault *apperror.Error
	if !errors.As(err, &fault) || fault.Code() != want {
		t.Fatalf("fault = %v, want %q", err, want)
	}
}

func testPrincipal(userID model.UserID) model.Principal {
	return model.Principal{UserID: userID, SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType: model.SessionClientWeb, AuthenticatedAt: testNow.Add(-time.Hour)}
}

type sittingStoreFake struct {
	store.ExamSittingStore
	schedule      *store.ExamSittingSchedule
	command       *store.CommandIdempotency
	result        *store.ExamSittingCommandResult
	update        *store.ExamSittingScheduleUpdate
	cancel        *store.ExamSittingCancellation
	pause         *store.ExamSittingManagerTransition
	resume        *store.ExamSittingManagerTransition
	extend        *store.ExamSittingExtension
	earlyClose    *store.ExamSittingManagerTransition
	advance       *store.ExamSittingDueAdvance
	finish        *store.ExamSittingFinishSealing
	lifecycle     *store.ExamSittingLifecycleResult
	dueOptions    store.ExamSittingLifecycleDueOptions
	due           []store.ExamSittingLifecycleDue
	noShowOptions store.ExamSittingNoShowListOptions
	noShows       []store.ExamSittingNoShow
	snapshot      *store.ExamSittingSnapshot
	getExamID     model.ExamID
	getSittingID  model.ExamSittingID
	resolveID     model.ExamSittingID
	listOptions   store.ExamSittingListOptions
	items         []store.ExamSittingSnapshot
	err           error
	getErr        error
}

type scheduleMailPreparerFake struct {
	request ScheduleMailRequest
	result  *store.ExamSittingMailFanout
	err     error
}

func (f *scheduleMailPreparerFake) Prepare(_ context.Context, request ScheduleMailRequest) (*store.ExamSittingMailFanout, error) {
	f.request = request
	return f.result, f.err
}

func (f *sittingStoreFake) Resolve(_ context.Context, sittingID model.ExamSittingID) (*store.ExamSittingSnapshot, error) {
	f.resolveID = sittingID
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.snapshot, nil
}

func (f *sittingStoreFake) Schedule(_ context.Context, input *store.ExamSittingSchedule, command *store.CommandIdempotency) (*store.ExamSittingCommandResult, error) {
	f.schedule, f.command = input, command
	if f.err != nil {
		return nil, f.err
	}
	if f.result != nil {
		return f.result, nil
	}
	return &store.ExamSittingCommandResult{Value: &store.ExamSittingSnapshot{Sitting: input.Sitting}}, nil
}

func (f *sittingStoreFake) Get(_ context.Context, examID model.ExamID, sittingID model.ExamSittingID) (*store.ExamSittingSnapshot, error) {
	f.getExamID, f.getSittingID = examID, sittingID
	return f.snapshot, f.getErr
}

func (f *sittingStoreFake) List(_ context.Context, options store.ExamSittingListOptions) ([]store.ExamSittingSnapshot, error) {
	f.listOptions = options
	return f.items, f.err
}

func (f *sittingStoreFake) UpdateSchedule(_ context.Context, input *store.ExamSittingScheduleUpdate, command *store.CommandIdempotency) (*store.ExamSittingCommandResult, error) {
	f.update, f.command = input, command
	return f.result, f.err
}

func (f *sittingStoreFake) Cancel(_ context.Context, input *store.ExamSittingCancellation, command *store.CommandIdempotency) (*store.ExamSittingCommandResult, error) {
	f.cancel, f.command = input, command
	return f.result, f.err
}

func (f *sittingStoreFake) Pause(_ context.Context, input *store.ExamSittingManagerTransition, command *store.CommandIdempotency) (*store.ExamSittingLifecycleResult, error) {
	f.pause, f.command = input, command
	return f.lifecycle, f.err
}

func (f *sittingStoreFake) Resume(_ context.Context, input *store.ExamSittingManagerTransition, command *store.CommandIdempotency) (*store.ExamSittingLifecycleResult, error) {
	f.resume, f.command = input, command
	return f.lifecycle, f.err
}

func (f *sittingStoreFake) Extend(_ context.Context, input *store.ExamSittingExtension, command *store.CommandIdempotency) (*store.ExamSittingLifecycleResult, error) {
	f.extend, f.command = input, command
	return f.lifecycle, f.err
}

func (f *sittingStoreFake) EarlyClose(_ context.Context, input *store.ExamSittingManagerTransition, command *store.CommandIdempotency) (*store.ExamSittingLifecycleResult, error) {
	f.earlyClose, f.command = input, command
	return f.lifecycle, f.err
}

func (f *sittingStoreFake) AdvanceDue(_ context.Context, input *store.ExamSittingDueAdvance) (*store.ExamSittingLifecycleResult, error) {
	f.advance = input
	return f.lifecycle, f.err
}

func (f *sittingStoreFake) FinishSealing(_ context.Context, input *store.ExamSittingFinishSealing) (*store.ExamSittingLifecycleResult, error) {
	f.finish = input
	return f.lifecycle, f.err
}

func (f *sittingStoreFake) ListLifecycleDue(_ context.Context, options store.ExamSittingLifecycleDueOptions) ([]store.ExamSittingLifecycleDue, error) {
	f.dueOptions = options
	return f.due, f.err
}

func (f *sittingStoreFake) ListNoShows(_ context.Context, options store.ExamSittingNoShowListOptions) ([]store.ExamSittingNoShow, error) {
	f.noShowOptions = options
	return f.noShows, f.err
}

type accessFake struct {
	snapshot *store.ExamAccessSnapshot
	err      error
}

func (f *accessFake) Access(context.Context, model.ExamID, model.UserID) (*store.ExamAccessSnapshot, error) {
	return f.snapshot, f.err
}

type membershipsFake struct {
	items []*model.AcademicUnitMember
	err   error
	at    time.Time
}

func (f *membershipsFake) ListActiveByUser(_ context.Context, _ model.UserID, at time.Time) ([]*model.AcademicUnitMember, error) {
	f.at = at
	return f.items, f.err
}

type authorizerFake struct {
	action   model.Action
	resource model.Resource
	err      error
	denied   bool
	denyErr  error
}

func (f *authorizerFake) Authorize(_ context.Context, _ invocation.Invocation, action model.Action, resource model.Resource) error {
	f.action, f.resource = action, resource
	return f.err
}

func (f *authorizerFake) RecordDenial(_ context.Context, _ invocation.Invocation, action model.Action, resource model.Resource, _ model.AcademicUnitID) error {
	f.action, f.resource, f.denied = action, resource, true
	return f.denyErr
}

type auditorFake struct {
	id        string
	action    model.Action
	resource  model.Resource
	operation string
	value     map[string]any
	failCode  string
	err       error
	failErr   error
}

func (f *auditorFake) BeginAtScope(_ context.Context, _ invocation.Invocation, action model.Action, resource model.Resource, _ model.RoleScopeType, _, operation string, value, _ map[string]any) (model.AuditEventID, error) {
	f.action, f.resource, f.operation, f.value = action, resource, operation, value
	return model.AuditEventID(f.id), f.err
}

func (f *auditorFake) Fail(_ context.Context, _ model.AuditEventID, code string) error {
	f.failCode = code
	return f.failErr
}

type effectEvent struct {
	kind      string
	examID    model.ExamID
	sittingID model.ExamSittingID
	state     model.ExamSittingState
	revision  int64
	at        time.Time
}

type effectsFake struct {
	events   []effectEvent
	reported []error
}

func (f *effectsFake) LifecycleChanged(_ context.Context, examID model.ExamID, sittingID model.ExamSittingID,
	state model.ExamSittingState, revision int64, transition store.ExamSittingLifecycleTransitionCode, scheduledEndAt, at time.Time,
) error {
	f.events = append(f.events, effectEvent{kind: string(transition), examID: examID, sittingID: sittingID, state: state, revision: revision, at: at})
	return nil
}

func (f *effectsFake) Scheduled(_ context.Context, examID model.ExamID, sittingID model.ExamSittingID, state model.ExamSittingState, revision int64, at time.Time) error {
	f.events = append(f.events, effectEvent{kind: "scheduled", examID: examID, sittingID: sittingID, state: state, revision: revision, at: at})
	return nil
}

func (f *effectsFake) ScheduleUpdated(_ context.Context, examID model.ExamID, sittingID model.ExamSittingID, state model.ExamSittingState, revision int64, at time.Time) error {
	f.events = append(f.events, effectEvent{kind: "schedule_updated", examID: examID, sittingID: sittingID, state: state, revision: revision, at: at})
	return nil
}

func (f *effectsFake) Canceled(_ context.Context, examID model.ExamID, sittingID model.ExamSittingID, state model.ExamSittingState, revision int64, at time.Time) error {
	f.events = append(f.events, effectEvent{kind: "canceled", examID: examID, sittingID: sittingID, state: state, revision: revision, at: at})
	return nil
}

func (f *effectsFake) Report(_ context.Context, _ string, err error) {
	f.reported = append(f.reported, err)
}

type systemAuditorFake struct {
	id, operation, failCode string
	action                  model.Action
	resource                model.Resource
	scopeType               model.RoleScopeType
	scopeID                 string
	system                  invocation.System
	value                   map[string]any
	err, failErr            error
}

func (f *systemAuditorFake) Begin(_ context.Context, system invocation.System, action model.Action, resource model.Resource, scopeType model.RoleScopeType, scopeID, operation string, value map[string]any) (model.AuditEventID, error) {
	f.system, f.action, f.resource, f.operation, f.value = system, action, resource, operation, value
	f.scopeType, f.scopeID = scopeType, scopeID
	return model.AuditEventID(f.id), f.err
}

func (f *systemAuditorFake) Fail(_ context.Context, _ model.AuditEventID, code string) error {
	f.failCode = code
	return f.failErr
}

type lifecycleJobFactoryFake struct {
	open, deadline, finalize                                    *model.Job
	boundaryRevision, deadlineRevision, finalizeRevision        int64
	boundaryStart, boundaryEnd, deadlineAt, finalizeAvailableAt time.Time
	err                                                         error
}

func (f *lifecycleJobFactoryFake) BoundaryJobs(_ model.ExamSittingID, revision int64, startAt, endAt time.Time) (*model.Job, *model.Job, error) {
	f.boundaryRevision, f.boundaryStart, f.boundaryEnd = revision, startAt, endAt
	return f.open, f.deadline, f.err
}

func (f *lifecycleJobFactoryFake) DeadlineJob(_ model.ExamSittingID, revision int64, deadline time.Time) (*model.Job, error) {
	f.deadlineRevision, f.deadlineAt = revision, deadline
	return f.deadline, f.err
}

func (f *lifecycleJobFactoryFake) FinalizeJob(_ model.ExamSittingID, revision int64, availableAt time.Time) (*model.Job, error) {
	f.finalizeRevision = revision
	f.finalizeAvailableAt = availableAt
	return f.finalize, f.err
}
