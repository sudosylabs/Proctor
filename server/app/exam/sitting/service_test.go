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

	"github.com/stretchr/testify/require"

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
	require.NoError(t, err)
	require.NotNil(t, got.Sitting)
	require.Equal(t, fixture.sittingID, got.Sitting.ID)
	require.Equal(t, model.ExamSittingScheduled, got.Sitting.State)
	require.Equal(t, model.ActionExamSittingCreate, fixture.authorizer.action, "authorization")
	require.Equal(t, model.Resource{Type: model.ResourceExam, ID: fixture.examID.String()}, fixture.authorizer.resource, "authorization")
	require.NotNil(t, fixture.persistence.schedule, "store schedule")
	require.False(t, fixture.persistence.schedule.ManagerOverride, "store schedule")
	require.Equal(t, fixture.userID, fixture.persistence.schedule.ActorUserID, "store schedule")
	require.Equal(t, fixture.sittingID, fixture.persistence.schedule.Sitting.ID, "store schedule")
	require.Equal(t, start, fixture.persistence.schedule.Sitting.ScheduledStartAt, "store schedule")
	require.NotNil(t, fixture.persistence.command, "store schedule")
	require.Same(t, fixture.jobs.open, fixture.persistence.schedule.OpenJob, "store schedule")
	require.Same(t, fixture.jobs.deadline, fixture.persistence.schedule.DeadlineJob, "store schedule")
	require.Equal(t, int64(1), fixture.jobs.boundaryRevision, "store schedule")
	require.True(t, fixture.jobs.boundaryStart.Equal(start), "store schedule")
	require.True(t, fixture.jobs.boundaryEnd.Equal(end), "store schedule")
	require.Same(t, fixture.mail.result, fixture.persistence.schedule.Mail, "store schedule")
	require.Equal(t, store.ExamSittingMailScheduled, fixture.mail.request.ChangeKind, "store schedule")
	wantIdempotency, err := prepareScheduleIdempotency(fixture.invocation, command)
	require.NoError(t, err)
	require.Equal(t, wantIdempotency, fixture.persistence.command)
	wantAudit := map[string]any{
		"exam_id": fixture.examID.String(), "exam_sitting_id": fixture.sittingID.String(),
		"exam_revision_id": fixture.revisionID.String(), "class_id": fixture.classID.String(),
	}
	require.Equal(t, model.ActionExamSittingCreate, fixture.auditor.action, "audit")
	require.Equal(t, "schedule", fixture.auditor.operation, "audit")
	require.Equal(t, wantAudit, fixture.auditor.value, "audit")
	require.Len(t, fixture.effects.events, 1)
	require.Equal(t, effectEvent{kind: "scheduled", examID: fixture.examID, sittingID: fixture.sittingID, state: model.ExamSittingScheduled, revision: 1, at: testNow}, fixture.effects.events[0])
}

func TestGetAuthorizesExactSittingResource(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	fixture.persistence.snapshot = &store.ExamSittingSnapshot{Sitting: fixture.sitting(t)}

	got, err := fixture.service.GetExamSitting(context.Background(), fixture.invocation, GetQuery{ExamID: fixture.examID, SittingID: fixture.sittingID})
	require.NoError(t, err)
	require.NotNil(t, got.Sitting)
	require.Equal(t, fixture.sittingID, got.Sitting.ID)
	require.Equal(t, model.ActionExamSittingView, fixture.authorizer.action, "authorization")
	require.Equal(t, model.Resource{Type: model.ResourceExamSitting, ID: fixture.sittingID.String()}, fixture.authorizer.resource, "authorization")
	require.Equal(t, fixture.examID, fixture.persistence.getExamID, "store Get() ids")
	require.Equal(t, fixture.sittingID, fixture.persistence.getSittingID, "store Get() ids")
}

func TestAuthorizeViewRechecksCurrentSittingRelationship(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	fixture.persistence.snapshot = &store.ExamSittingSnapshot{Sitting: fixture.sitting(t)}

	require.NoError(t, fixture.service.AuthorizeView(context.Background(), fixture.invocation, fixture.sittingID))
	require.Equal(t, fixture.sittingID, fixture.persistence.resolveID, "subscription authorization = resolve")
	require.Equal(t, model.ActionExamSittingView, fixture.authorizer.action, "subscription authorization = resolve")
	require.Equal(t, model.Resource{Type: model.ResourceExamSitting, ID: fixture.sittingID.String()}, fixture.authorizer.resource, "subscription authorization = resolve")

	fixture.memberships.items = nil
	require.NoError(t, fixture.service.AuthorizeView(context.Background(), fixture.invocation, fixture.sittingID))
	require.Equal(t, model.ActionExamSittingViewOverride, fixture.authorizer.action)
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
			require.Equal(t, wantAllowed, err == nil, "AuthorizeBrowserActivityView() = %s, %v", unit, err)
			if wantAllowed {
				require.Equal(t, f.unitID, unit, "wrong authorized unit")
			}
			if !wantAllowed {
				require.Empty(t, unit, "denial returned a scope")
			}
			require.Equal(t, model.ActionExamAttemptBrowserActivityView, f.authorizer.action, "used an unrelated action or resource")
			require.Equal(t, f.sittingID.String(), f.authorizer.resource.ID, "used an unrelated action or resource")
			require.Equal(t, test.removeManager, f.authorizer.denied, "relationship denial was not audited")
		})
	}
}

func TestBrowserActivityRechecksMembershipBetweenPagesAndFailsClosedOnAudit(t *testing.T) {
	f := newFixture(t)
	f.persistence.snapshot = &store.ExamSittingSnapshot{Sitting: f.sitting(t)}
	_, err := f.service.AuthorizeBrowserActivityView(context.Background(), f.invocation, f.sittingID)
	require.NoError(t, err)
	f.access.snapshot.ActorIsManager = false
	_, err = f.service.AuthorizeBrowserActivityView(context.Background(), f.invocation, f.sittingID)
	require.Error(t, err, "later page retained removed Manager authority")
	require.True(t, f.authorizer.denied, "later page retained removed Manager authority")
	auditErr := errors.New("audit unavailable")
	f.authorizer.denyErr = auditErr
	_, err = f.service.AuthorizeBrowserActivityView(context.Background(), f.invocation, f.sittingID)
	require.ErrorIs(t, err, auditErr)
}

func TestAuthorizeManageReportsOrdinaryAndOverrideDecision(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	fixture.persistence.snapshot = &store.ExamSittingSnapshot{Sitting: fixture.sitting(t)}
	override, err := fixture.service.AuthorizeManage(context.Background(), fixture.invocation, fixture.sittingID)
	require.NoError(t, err, "ordinary manage override")
	require.False(t, override, "ordinary manage override")
	require.Equal(t, model.ActionExamSittingManage, fixture.authorizer.action, "ordinary manage override")
	fixture.memberships.items = nil
	override, err = fixture.service.AuthorizeManage(context.Background(), fixture.invocation, fixture.sittingID)
	require.NoError(t, err, "override manage override")
	require.True(t, override, "override manage override")
	require.Equal(t, model.ActionExamSittingManageOverride, fixture.authorizer.action, "override manage override")
}

func TestAuthorizeSubmissionViewUsesCanonicalSubmissionResourceAndCurrentManagerRelationship(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	submissionID := model.NewSubmissionID()

	require.NoError(t, fixture.service.AuthorizeSubmissionView(context.Background(), fixture.invocation, fixture.examID, submissionID))
	require.Equal(t, model.ActionSubmissionView, fixture.authorizer.action, "ordinary Submission authorization")
	require.Equal(t, model.Resource{Type: model.ResourceSubmission, ID: submissionID.String()}, fixture.authorizer.resource, "ordinary Submission authorization")

	fixture.memberships.items = nil
	require.NoError(t, fixture.service.AuthorizeSubmissionView(context.Background(), fixture.invocation, fixture.examID, submissionID))
	require.Equal(t, model.ActionSubmissionViewOverride, fixture.authorizer.action)
}

func TestAuthorizeSubmissionReviewAndReleaseUseDistinctCurrentPermissions(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	submissionID := model.NewSubmissionID()

	override, err := fixture.service.AuthorizeSubmissionReview(context.Background(), fixture.invocation, fixture.examID, submissionID)
	require.NoError(t, err, "ordinary Review authorization override")
	require.False(t, override, "ordinary Review authorization override")
	require.Equal(t, model.ActionSubmissionReview, fixture.authorizer.action, "ordinary Review authorization override")
	override, err = fixture.service.AuthorizeSubmissionRelease(context.Background(), fixture.invocation, fixture.examID, submissionID)
	require.NoError(t, err, "ordinary release authorization override")
	require.False(t, override, "ordinary release authorization override")
	require.Equal(t, model.ActionSubmissionRelease, fixture.authorizer.action, "ordinary release authorization override")

	fixture.memberships.items = nil
	override, err = fixture.service.AuthorizeSubmissionReview(context.Background(), fixture.invocation, fixture.examID, submissionID)
	require.NoError(t, err, "Review override authorization override")
	require.True(t, override, "Review override authorization override")
	require.Equal(t, model.ActionSubmissionReviewOverride, fixture.authorizer.action, "Review override authorization override")
	override, err = fixture.service.AuthorizeSubmissionRelease(context.Background(), fixture.invocation, fixture.examID, submissionID)
	require.NoError(t, err, "release override authorization override")
	require.True(t, override, "release override authorization override")
	require.Equal(t, model.ActionSubmissionReleaseOverride, fixture.authorizer.action, "release override authorization override")
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
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.True(t, page.HasMore)
	require.Equal(t, first.ID, page.Items[0].Sitting.ID)
	require.Equal(t, model.ActionExamView, fixture.authorizer.action, "authorization")
	require.Equal(t, model.Resource{Type: model.ResourceExam, ID: fixture.examID.String()}, fixture.authorizer.resource, "authorization")
	want := store.ExamSittingListOptions{ExamID: fixture.examID, ClassID: fixture.classID,
		States: []model.ExamSittingState{model.ExamSittingScheduled}, OverlapStartAt: overlapStart,
		OverlapEndAt: overlapEnd, Limit: 2}
	require.Equal(t, want, fixture.persistence.listOptions)
}

func TestUpdateScheduleUsesSittingManagementAndOptimisticFence(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	updated := fixture.sitting(t)
	current := fixture.sitting(t)
	changedAt := testNow
	changedStart, changedEnd := testNow.Add(2*time.Hour), testNow.Add(5*time.Hour)
	changed, err := updated.ApplySchedule(fixture.revisionID, fixture.classID, changedStart, changedEnd, changedAt)
	require.NoError(t, err, "ApplySchedule()")
	require.True(t, changed, "ApplySchedule()")
	fixture.persistence.snapshot = &store.ExamSittingSnapshot{Sitting: current}
	fixture.persistence.result = &store.ExamSittingCommandResult{Value: &store.ExamSittingSnapshot{Sitting: updated}}

	command := UpdateScheduleCommand{
		ExamID: fixture.examID, SittingID: fixture.sittingID, ExpectedRevision: 1,
		ScheduledStartAt: &changedStart, ScheduledEndAt: &changedEnd, IdempotencyKey: "test-key",
	}
	got, err := fixture.service.UpdateExamSittingSchedule(context.Background(), fixture.invocation, command)
	require.NoError(t, err)
	require.NotNil(t, got.Sitting)
	require.Equal(t, int64(2), got.Sitting.Revision)
	require.Equal(t, model.ActionExamSittingManage, fixture.authorizer.action, "authorization = %q %#v", fixture.authorizer.action, fixture.authorizer.resource)
	require.Equal(t, model.ResourceExamSitting, fixture.authorizer.resource.Type, "authorization = %q %#v", fixture.authorizer.action, fixture.authorizer.resource)
	input := fixture.persistence.update
	require.NotNil(t, input, "store update")
	require.Equal(t, int64(1), input.ExpectedRevision, "store update")
	require.False(t, input.ManagerOverride, "store update")
	require.NotNil(t, fixture.persistence.command, "store update")
	wantIdempotency, err := prepareScheduleUpdateIdempotency(fixture.invocation, command)
	require.NoError(t, err)
	require.Equal(t, wantIdempotency, fixture.persistence.command)
	require.Same(t, fixture.jobs.open, input.OpenJob, "boundary Jobs = %#v %#v, factory revision/times = %d %v %v", input.OpenJob, input.DeadlineJob, fixture.jobs.boundaryRevision, fixture.jobs.boundaryStart, fixture.jobs.boundaryEnd)
	require.Same(t, fixture.jobs.deadline, input.DeadlineJob, "boundary Jobs = %#v %#v, factory revision/times = %d %v %v", input.OpenJob, input.DeadlineJob, fixture.jobs.boundaryRevision, fixture.jobs.boundaryStart, fixture.jobs.boundaryEnd)
	require.Equal(t, int64(2), fixture.jobs.boundaryRevision, "boundary Jobs = %#v %#v, factory revision/times = %d %v %v", input.OpenJob, input.DeadlineJob, fixture.jobs.boundaryRevision, fixture.jobs.boundaryStart, fixture.jobs.boundaryEnd)
	require.True(t, fixture.jobs.boundaryStart.Equal(changedStart), "boundary Jobs = %#v %#v, factory revision/times = %d %v %v", input.OpenJob, input.DeadlineJob, fixture.jobs.boundaryRevision, fixture.jobs.boundaryStart, fixture.jobs.boundaryEnd)
	require.True(t, fixture.jobs.boundaryEnd.Equal(changedEnd), "boundary Jobs = %#v %#v, factory revision/times = %d %v %v", input.OpenJob, input.DeadlineJob, fixture.jobs.boundaryRevision, fixture.jobs.boundaryStart, fixture.jobs.boundaryEnd)
	require.Same(t, fixture.mail.result, input.Mail, "mail preparation = %#v, store", fixture.mail.request)
	require.Equal(t, store.ExamSittingMailRescheduled, fixture.mail.request.ChangeKind, "mail preparation = %#v, store", fixture.mail.request)
	require.Equal(t, fixture.classID, fixture.mail.request.PriorClassID, "mail preparation = %#v, store", fixture.mail.request)
	require.Equal(t, int64(2), fixture.mail.request.SittingRevision, "mail preparation = %#v, store", fixture.mail.request)
	require.Equal(t, fixture.revisionID, input.ExamRevisionID)
	require.Equal(t, fixture.classID, input.ClassID)
	require.True(t, input.ScheduledStartAt.Equal(changedStart))
	require.True(t, input.ScheduledEndAt.Equal(changedEnd))
	wantAudit := map[string]any{"exam_id": fixture.examID.String(), "exam_sitting_id": fixture.sittingID.String(),
		"exam_revision_id": fixture.revisionID.String(), "class_id": fixture.classID.String(), "expected_sitting_revision": int64(1)}
	require.Equal(t, "update_schedule", fixture.auditor.operation, "audit")
	require.Equal(t, wantAudit, fixture.auditor.value, "audit")
	require.Len(t, fixture.effects.events, 1)
	require.Equal(t, "schedule_updated", fixture.effects.events[0].kind)
	require.Equal(t, int64(2), fixture.effects.events[0].revision)
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
	require.Nil(t, fixture.persistence.update, "no-op reached mutation boundary: update")
	require.Empty(t, fixture.auditor.operation, "no-op reached mutation boundary: update")
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
	require.NotNil(t, fixture.persistence.update, "stale no-op did not reach mutation boundary: update")
	require.Equal(t, "update_schedule", fixture.auditor.operation, "stale no-op did not reach mutation boundary: update")
}

func TestUpdateScheduleSendsArchivedAndNonScheduledNoOpsToStore(t *testing.T) {
	t.Parallel()
	t.Run("archived exam", func(t *testing.T) {
		t.Parallel()
		fixture := newFixture(t)
		fixture.persistence.snapshot = &store.ExamSittingSnapshot{Sitting: fixture.sitting(t)}
		require.NoError(t, fixture.access.snapshot.Exam.Archive(testNow))
		fixture.persistence.err = store.NewErrConflict("exam_sitting", "exam_archived", nil)
		revisionID := fixture.revisionID

		_, err := fixture.service.UpdateExamSittingSchedule(context.Background(), fixture.invocation, UpdateScheduleCommand{
			ExamID: fixture.examID, SittingID: fixture.sittingID, ExpectedRevision: 1,
			ExamRevisionID: &revisionID, IdempotencyKey: "test-key",
		})
		assertFaultCode(t, err, "exam.archived")
		require.NotNil(t, fixture.persistence.update, "archived no-op did not reach Store")
	})

	t.Run("non-scheduled sitting", func(t *testing.T) {
		t.Parallel()
		fixture := newFixture(t)
		current := fixture.sitting(t)
		require.NoError(t, current.Cancel(model.ExamSittingReasonManagerCanceled, testNow))
		fixture.persistence.snapshot = &store.ExamSittingSnapshot{Sitting: current}
		fixture.persistence.err = store.NewErrConflict("exam_sitting", "exam_sitting_state", nil)
		revisionID := fixture.revisionID

		_, err := fixture.service.UpdateExamSittingSchedule(context.Background(), fixture.invocation, UpdateScheduleCommand{
			ExamID: fixture.examID, SittingID: fixture.sittingID, ExpectedRevision: current.Revision,
			ExamRevisionID: &revisionID, IdempotencyKey: "test-key",
		})
		assertFaultCode(t, err, "exam.sitting.state_conflict")
		require.NotNil(t, fixture.persistence.update, "state-conflicting no-op did not reach Store")
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
	require.NoError(t, err)
	require.Equal(t, model.ActionExamSittingCreateOverride, fixture.authorizer.action, "override authorization/store")
	require.NotNil(t, fixture.persistence.schedule, "override authorization/store")
	require.True(t, fixture.persistence.schedule.ManagerOverride, "override authorization/store")
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
	require.Error(t, err, "unauthorized mutation continued: err")
	require.Nil(t, fixture.persistence.schedule, "unauthorized mutation continued: err")
	require.Empty(t, fixture.auditor.operation, "unauthorized mutation continued: err")
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
	require.ErrorAs(t, err, &fault)
	require.Equal(t, "exam.sitting.unavailable", fault.Code())
	require.ErrorIs(t, err, failure)
	require.Empty(t, testFixture.authorizer.action, "membership failure continued: action")
	require.Nil(t, testFixture.persistence.schedule, "membership failure continued: action")
	require.Empty(t, testFixture.auditor.operation, "membership failure continued: action")
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
	require.NoError(t, err)
	require.NotNil(t, got.Sitting, "replayed Sitting = %#v, want original", got)
	require.Equal(t, original.ID, got.Sitting.ID, "replayed Sitting = %#v, want original", got)
	require.Empty(t, fixture.effects.events)
}

func TestReplayedUpdateAndCancellationDoNotPublishEffects(t *testing.T) {
	t.Parallel()
	t.Run("update", func(t *testing.T) {
		t.Parallel()
		fixture := newFixture(t)
		fixture.persistence.snapshot = &store.ExamSittingSnapshot{Sitting: fixture.sitting(t)}
		updated := fixture.sitting(t)
		start, end := testNow.Add(2*time.Hour), testNow.Add(5*time.Hour)
		_, err := updated.ApplySchedule(fixture.revisionID, fixture.classID, start, end, testNow)
		require.NoError(t, err)
		fixture.persistence.result = &store.ExamSittingCommandResult{Value: &store.ExamSittingSnapshot{Sitting: updated}, Replayed: true}

		_, err = fixture.service.UpdateExamSittingSchedule(context.Background(), fixture.invocation, UpdateScheduleCommand{
			ExamID: fixture.examID, SittingID: fixture.sittingID, ExpectedRevision: 1,
			ScheduledStartAt: &start, ScheduledEndAt: &end, IdempotencyKey: "test-key",
		})
		require.NoError(t, err)
		require.Empty(t, fixture.effects.events)
	})

	t.Run("cancel", func(t *testing.T) {
		t.Parallel()
		fixture := newFixture(t)
		fixture.persistence.snapshot = &store.ExamSittingSnapshot{Sitting: fixture.sitting(t)}
		canceled := fixture.sitting(t)
		require.NoError(t, canceled.Cancel(model.ExamSittingReasonManagerCanceled, testNow))
		fixture.persistence.result = &store.ExamSittingCommandResult{Value: &store.ExamSittingSnapshot{Sitting: canceled}, Replayed: true}

		_, err := fixture.service.CancelExamSitting(context.Background(), fixture.invocation, CancelCommand{
			ExamID: fixture.examID, SittingID: fixture.sittingID, ExpectedRevision: 1,
			PrivateReason: "Manager canceled", IdempotencyKey: "test-key",
		})
		require.NoError(t, err)
		require.Empty(t, fixture.effects.events)
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
			require.Equal(t, test.valid, validPrivateReason(test.reason), "validPrivateReason()")
		})
	}
}

func TestStoreConflictsHaveStableFaults(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"exam_sitting_revision":                "exam.sitting.revision_conflict",
		"exam_sitting_no_changes":              "exam.sitting.no_changes",
		"exam_sitting_state":                   "exam.sitting.state_conflict",
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
			_, err := listOptions(query)
			require.Error(t, err, "listOptions() accepted invalid query")
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
	require.Empty(t, fixture.effects.events)
}

func TestCancelRetainsPrivateReasonOnlyInStoreCommand(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	fixture.persistence.snapshot = &store.ExamSittingSnapshot{Sitting: fixture.sitting(t)}
	canceled := fixture.sitting(t)
	require.NoError(t, canceled.Cancel(model.ExamSittingReasonManagerCanceled, testNow))
	fixture.persistence.result = &store.ExamSittingCommandResult{Value: &store.ExamSittingSnapshot{Sitting: canceled}}

	command := CancelCommand{
		ExamID: fixture.examID, SittingID: fixture.sittingID, ExpectedRevision: 1,
		PrivateReason: "Room became unavailable", IdempotencyKey: "test-key",
	}
	got, err := fixture.service.CancelExamSitting(context.Background(), fixture.invocation, command)
	require.NoError(t, err)
	require.NotNil(t, got.Sitting)
	require.Equal(t, model.ExamSittingCanceled, got.Sitting.State)
	require.Equal(t, model.ExamSittingReasonManagerCanceled, got.Sitting.ReasonCode)
	require.NotNil(t, fixture.persistence.cancel)
	require.Equal(t, "Room became unavailable", fixture.persistence.cancel.PrivateReason)
	require.Equal(t, int64(1), fixture.persistence.cancel.ExpectedRevision)
	require.Same(t, fixture.mail.result, fixture.persistence.cancel.Mail, "cancellation mail = %#v, store", fixture.mail.request)
	require.Equal(t, store.ExamSittingMailCancelled, fixture.mail.request.ChangeKind, "cancellation mail = %#v, store", fixture.mail.request)
	wantIdempotency, err := prepareTransitionIdempotency(fixture.invocation, idempotencyOperationCancel, PauseCommand(command))
	require.NoError(t, err)
	require.Equal(t, wantIdempotency, fixture.persistence.command)
	wantAudit := map[string]any{"exam_id": fixture.examID.String(), "exam_sitting_id": fixture.sittingID.String(),
		"expected_sitting_revision": int64(1), "reason_code": string(model.ExamSittingReasonManagerCanceled)}
	require.Equal(t, "cancel", fixture.auditor.operation, "audit")
	require.Equal(t, wantAudit, fixture.auditor.value, "audit")
	require.NotEqual(t, "Room became unavailable", reflect.ValueOf(fixture.auditor.value).String(), "private cancellation reason leaked into audit")
	require.Len(t, fixture.effects.events, 1)
	require.Equal(t, "canceled", fixture.effects.events[0].kind)
	require.Equal(t, model.ExamSittingCanceled, fixture.effects.events[0].state)
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
	require.NoError(t, err)
	return sitting
}

func TestListNoShowsAuthorizesClosingSittingAndReturnsBoundedPage(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	sitting, err := model.NewExamSitting(fixture.sittingID, fixture.examID, fixture.revisionID, fixture.classID,
		testNow.Add(-2*time.Hour), testNow.Add(-time.Hour), testNow.Add(-3*time.Hour))
	require.NoError(t, err)
	err = sitting.Open(testNow.Add(-2 * time.Hour))
	require.NoError(t, err)
	err = sitting.EnterClosing(model.ExamSittingReasonScheduledEndReached, testNow.Add(-time.Hour))
	require.NoError(t, err)
	fixture.persistence.snapshot = &store.ExamSittingSnapshot{Sitting: sitting, AcademicUnitID: fixture.unitID}
	ids := []model.UserID{model.NewUserID(), model.NewUserID(), model.NewUserID()}
	slices.SortFunc(ids, func(left, right model.UserID) int { return strings.Compare(left.String(), right.String()) })
	fixture.persistence.noShows = []store.ExamSittingNoShow{{CandidateUserID: ids[0]}, {CandidateUserID: ids[1]}, {CandidateUserID: ids[2]}}
	page, err := fixture.service.ListExamSittingNoShows(context.Background(), fixture.invocation, ListNoShowsQuery{ExamID: fixture.examID, SittingID: fixture.sittingID, AfterCandidateUserID: model.UserID(""), Limit: 2})
	require.NoError(t, err, "page=%#v options=%#v err", page, fixture.persistence.noShowOptions)
	require.Len(t, page.Items, 2, "page=%#v options=%#v err", page, fixture.persistence.noShowOptions)
	require.True(t, page.HasMore, "page=%#v options=%#v err", page, fixture.persistence.noShowOptions)
	require.Equal(t, ids[1], page.Items[1].CandidateUserID, "page=%#v options=%#v err", page, fixture.persistence.noShowOptions)
	require.Equal(t, 3, fixture.persistence.noShowOptions.Limit, "page=%#v options=%#v err", page, fixture.persistence.noShowOptions)
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	userID, unitID := model.NewUserID(), model.NewAcademicUnitID()
	examID, revisionID := model.NewExamID(), model.NewExamRevisionID()
	classID, sittingID := model.NewClassID(), model.NewExamSittingID()
	exam, err := model.NewExam(examID, unitID, userID, testNow.Add(-time.Hour))
	require.NoError(t, err)
	persistence := &sittingStoreFake{}
	access := &accessFake{snapshot: &store.ExamAccessSnapshot{Exam: exam, ActorIsManager: true}}
	memberships := &membershipsFake{items: []*model.AcademicUnitMember{{AcademicUnitID: unitID}}}
	authorizer, auditor, systemAudit, effects := &authorizerFake{}, &auditorFake{id: model.NewID()}, &systemAuditorFake{id: model.NewID()}, &effectsFake{}
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
	require.NoError(t, err)
	return fixture{service: service, invocation: invocation.New(testPrincipal(userID), model.RequestMetadata{}), userID: userID,
		unitID: unitID, examID: examID, revisionID: revisionID, classID: classID, sittingID: sittingID,
		persistence: persistence, access: access, memberships: memberships, authorizer: authorizer, auditor: auditor,
		systemAudit: systemAudit, effects: effects, jobs: jobs, mail: mail}
}

func assertFaultCode(t *testing.T, err error, want string) {
	t.Helper()
	var fault *apperror.Error
	require.ErrorAs(t, err, &fault, "fault")
	require.Equal(t, want, fault.Code(), "fault")
}

func testPrincipal(userID model.UserID) model.Principal {
	return model.Principal{UserID: userID, SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewID()),
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
