// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sitting

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestPauseAuthorizesCurrentManagerAndKeepsPrivateReasonOutOfAuditAndEffect(t *testing.T) {
	fixture := newFixture(t)
	current := fixture.sitting(t)
	if err := current.Open(testNow); err != nil {
		t.Fatal(err)
	}
	paused := cloneSitting(current)
	if err := paused.Pause(testNow.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	fixture.persistence.lifecycle = lifecycleResult(paused, store.ExamSittingTransitionManagerPaused, true, false)

	got, err := fixture.service.Pause(context.Background(), fixture.call, PauseCommand{
		ExamID: fixture.examID, SittingID: fixture.sittingID, ExpectedRevision: current.Revision,
		PrivateReason: "Candidate requested clarification", Idempotency: &store.CommandIdempotency{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Sitting == nil || got.Sitting.State != model.ExamSittingPaused {
		t.Fatalf("Pause() = %#v", got)
	}
	if fixture.authorizer.action != model.ActionExamSittingManage || fixture.authorizer.resource != (model.Resource{Type: model.ResourceExamSitting, ID: fixture.sittingID.String()}) {
		t.Fatalf("authorization = %q %#v", fixture.authorizer.action, fixture.authorizer.resource)
	}
	wantAudit := map[string]any{"exam_id": fixture.examID.String(), "exam_sitting_id": fixture.sittingID.String(),
		"expected_sitting_revision": current.Revision, "transition": string(store.ExamSittingTransitionManagerPaused)}
	if !reflect.DeepEqual(fixture.auditor.value, wantAudit) {
		t.Fatalf("audit value = %#v", fixture.auditor.value)
	}
	if fixture.persistence.pause == nil || fixture.persistence.pause.PrivateReason != "Candidate requested clarification" {
		t.Fatalf("Store Pause = %#v", fixture.persistence.pause)
	}
	if len(fixture.effects.events) != 1 || fixture.effects.events[0].kind != string(store.ExamSittingTransitionManagerPaused) {
		t.Fatalf("effects = %#v", fixture.effects.events)
	}
}

func TestLifecycleManagerCommandsUseExplicitStatesJobsAndReplaySuppression(t *testing.T) {
	tests := []struct {
		name       string
		archived   bool
		transition store.ExamSittingLifecycleTransitionCode
		run        func(fixture, *model.ExamSitting) (store.ExamSittingSnapshot, error)
		input      func(*sittingStoreFake) any
		wantAction model.Action
		wantFault  string
		finalizes  bool
	}{
		{name: "pause remains available after archive", archived: true, transition: store.ExamSittingTransitionManagerPaused,
			run: func(f fixture, current *model.ExamSitting) (store.ExamSittingSnapshot, error) {
				return f.service.Pause(context.Background(), f.call, PauseCommand{ExamID: f.examID, SittingID: f.sittingID,
					ExpectedRevision: current.Revision, PrivateReason: "Investigating active incident", Idempotency: &store.CommandIdempotency{}})
			}, input: func(fake *sittingStoreFake) any { return fake.pause }, wantAction: model.ActionExamSittingManage},
		{name: "resume rejects archived Exam", archived: true, transition: store.ExamSittingTransitionManagerResumed,
			run: func(f fixture, current *model.ExamSitting) (store.ExamSittingSnapshot, error) {
				return f.service.Resume(context.Background(), f.call, ResumeCommand{ExamID: f.examID, SittingID: f.sittingID,
					ExpectedRevision: current.Revision, PrivateReason: "Issue resolved", Idempotency: &store.CommandIdempotency{}})
			}, wantFault: "exam.archived"},
		{name: "extend rejects archived Exam", archived: true, transition: store.ExamSittingTransitionManagerExtended,
			run: func(f fixture, current *model.ExamSitting) (store.ExamSittingSnapshot, error) {
				return f.service.Extend(context.Background(), f.call, ExtendCommand{ExamID: f.examID, SittingID: f.sittingID,
					ExpectedRevision: current.Revision, ScheduledEndAt: current.ScheduledEndAt.Add(time.Hour),
					PrivateReason: "Compensating for disruption", Idempotency: &store.CommandIdempotency{}})
			}, wantFault: "exam.archived"},
		{name: "early close remains available after archive", archived: true, transition: store.ExamSittingTransitionManagerClosed,
			run: func(f fixture, current *model.ExamSitting) (store.ExamSittingSnapshot, error) {
				return f.service.EarlyClose(context.Background(), f.call, EarlyCloseCommand{ExamID: f.examID, SittingID: f.sittingID,
					ExpectedRevision: current.Revision, PrivateReason: "Safety stop", Idempotency: &store.CommandIdempotency{}})
			}, input: func(fake *sittingStoreFake) any { return fake.earlyClose }, wantAction: model.ActionExamSittingManage, finalizes: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newFixture(t)
			current := fixture.sitting(t)
			if err := current.Open(testNow); err != nil {
				t.Fatal(err)
			}
			if test.transition == store.ExamSittingTransitionManagerResumed {
				if err := current.Pause(testNow); err != nil {
					t.Fatal(err)
				}
			}
			if test.archived {
				if err := fixture.access.snapshot.Exam.Archive(testNow); err != nil {
					t.Fatal(err)
				}
			}
			result := cloneSitting(current)
			switch test.transition {
			case store.ExamSittingTransitionManagerPaused:
				_ = result.Pause(testNow)
			case store.ExamSittingTransitionManagerResumed:
				_ = result.Resume(testNow)
			case store.ExamSittingTransitionManagerExtended:
				_ = result.ExtendEnd(current.ScheduledEndAt.Add(time.Hour), testNow)
			case store.ExamSittingTransitionManagerClosed:
				_ = result.EnterClosing(model.ExamSittingReasonManagerClosed, testNow)
			}
			fixture.persistence.lifecycle = lifecycleResult(result, test.transition, true, false)
			_, err := test.run(fixture, current)
			if test.wantFault != "" {
				assertFaultCode(t, err, test.wantFault)
				if fixture.persistence.resume != nil || fixture.persistence.extend != nil || fixture.persistence.earlyClose != nil {
					t.Fatal("archived command reached Store")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if test.input(fixture.persistence) == nil || fixture.authorizer.action != test.wantAction {
				t.Fatalf("Store input/action = %#v %q", test.input(fixture.persistence), fixture.authorizer.action)
			}
			if test.finalizes && (fixture.jobs.finalizeRevision != current.Revision+1 || !fixture.jobs.finalizeAvailableAt.Equal(testNow)) {
				t.Fatalf("finalize intent = revision %d at %s", fixture.jobs.finalizeRevision, fixture.jobs.finalizeAvailableAt)
			}
		})
	}

	fixture := newFixture(t)
	current := fixture.sitting(t)
	_ = current.Open(testNow)
	paused := cloneSitting(current)
	_ = paused.Pause(testNow)
	fixture.persistence.lifecycle = lifecycleResult(paused, store.ExamSittingTransitionManagerPaused, true, true)
	_, err := fixture.service.Pause(context.Background(), fixture.call, PauseCommand{ExamID: fixture.examID, SittingID: fixture.sittingID,
		ExpectedRevision: current.Revision, PrivateReason: "Replay", Idempotency: &store.CommandIdempotency{}})
	if err != nil {
		t.Fatal(err)
	}
	if len(fixture.effects.events) != 0 {
		t.Fatalf("replay effects = %#v", fixture.effects.events)
	}
}

func TestPauseUsesExplicitOverrideWithoutCurrentExactUnitMembership(t *testing.T) {
	fixture := newFixture(t)
	fixture.memberships.items = nil
	current := fixture.sitting(t)
	_ = current.Open(testNow)
	paused := cloneSitting(current)
	_ = paused.Pause(testNow)
	fixture.persistence.lifecycle = lifecycleResult(paused, store.ExamSittingTransitionManagerPaused, true, false)

	_, err := fixture.service.Pause(context.Background(), fixture.call, PauseCommand{ExamID: fixture.examID,
		SittingID: fixture.sittingID, ExpectedRevision: current.Revision, PrivateReason: "Administrator intervention",
		Idempotency: &store.CommandIdempotency{}})
	if err != nil {
		t.Fatal(err)
	}
	if fixture.authorizer.action != model.ActionExamSittingManageOverride || fixture.persistence.pause == nil || !fixture.persistence.pause.ManagerOverride {
		t.Fatalf("override authorization/store = %q %#v", fixture.authorizer.action, fixture.persistence.pause)
	}
}

func TestLifecycleCommandsValidatePrivateReasonBounds(t *testing.T) {
	fixture := newFixture(t)
	commands := []struct {
		name string
		run  func(string) error
	}{
		{"pause", func(reason string) error {
			_, err := fixture.service.Pause(context.Background(), fixture.call, PauseCommand{ExamID: fixture.examID, SittingID: fixture.sittingID, ExpectedRevision: 1, PrivateReason: reason, Idempotency: &store.CommandIdempotency{}})
			return err
		}},
		{"resume", func(reason string) error {
			_, err := fixture.service.Resume(context.Background(), fixture.call, ResumeCommand{ExamID: fixture.examID, SittingID: fixture.sittingID, ExpectedRevision: 1, PrivateReason: reason, Idempotency: &store.CommandIdempotency{}})
			return err
		}},
		{"extend", func(reason string) error {
			_, err := fixture.service.Extend(context.Background(), fixture.call, ExtendCommand{ExamID: fixture.examID, SittingID: fixture.sittingID, ExpectedRevision: 1, ScheduledEndAt: testNow.Add(time.Hour), PrivateReason: reason, Idempotency: &store.CommandIdempotency{}})
			return err
		}},
		{"close", func(reason string) error {
			_, err := fixture.service.EarlyClose(context.Background(), fixture.call, EarlyCloseCommand{ExamID: fixture.examID, SittingID: fixture.sittingID, ExpectedRevision: 1, PrivateReason: reason, Idempotency: &store.CommandIdempotency{}})
			return err
		}},
	}
	for _, command := range commands {
		for _, reason := range []string{"", " padded ", strings.Repeat("x", 1001), strings.Repeat("é", 2001)} {
			err := command.run(reason)
			assertFaultCode(t, err, "exam.sitting.invalid")
		}
	}
}

func TestSystemAdvancePublishesOnlyChangedOutcomeAndUsesSystemAudit(t *testing.T) {
	fixture := newFixture(t)
	current := fixture.sitting(t)
	fixture.persistence.snapshot = &store.ExamSittingSnapshot{Sitting: current, AcademicUnitID: fixture.unitID}
	opened := cloneSitting(current)
	if err := opened.Open(testNow); err != nil {
		t.Fatal(err)
	}
	fixture.persistence.lifecycle = lifecycleResult(opened, store.ExamSittingTransitionOpened, true, false)
	call := SystemCall{JobID: model.NewJobID(), AttemptID: model.NewJobAttemptID()}

	got, err := fixture.service.AdvanceDue(context.Background(), call, fixture.sittingID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Value == nil || got.Value.Sitting.State != model.ExamSittingOpen || fixture.systemAudit.call != call ||
		fixture.systemAudit.action != model.ActionExamSittingManage || fixture.systemAudit.scopeType != model.RoleScopeAcademicUnit ||
		fixture.systemAudit.scopeID != fixture.unitID.String() || fixture.persistence.advance == nil {
		t.Fatalf("AdvanceDue() = %#v, audit=%#v, input=%#v", got, fixture.systemAudit, fixture.persistence.advance)
	}
	if len(fixture.effects.events) != 1 || fixture.effects.events[0].kind != string(store.ExamSittingTransitionOpened) {
		t.Fatalf("effects = %#v", fixture.effects.events)
	}
	if !fixture.jobs.finalizeAvailableAt.Equal(current.ScheduledEndAt) {
		t.Fatalf("system finalize availability = %s, want deadline %s", fixture.jobs.finalizeAvailableAt, current.ScheduledEndAt)
	}

	fixture.effects.events = nil
	fixture.persistence.lifecycle = lifecycleResult(current, "", false, false)
	if _, err = fixture.service.AdvanceDue(context.Background(), call, fixture.sittingID); err != nil {
		t.Fatal(err)
	}
	if len(fixture.effects.events) != 0 {
		t.Fatalf("no-op effects = %#v", fixture.effects.events)
	}

	structurallyInvalid := cloneSitting(current)
	if err = structurallyInvalid.Cancel(model.ExamSittingReasonAcademicStructureInvalid, testNow); err != nil {
		t.Fatal(err)
	}
	fixture.persistence.lifecycle = lifecycleResult(structurallyInvalid, store.ExamSittingTransitionAcademicStructureInvalid, true, false)
	if _, err = fixture.service.AdvanceDue(context.Background(), call, fixture.sittingID); err != nil {
		t.Fatal(err)
	}
	if len(fixture.effects.events) != 1 || fixture.effects.events[0].kind != string(store.ExamSittingTransitionAcademicStructureInvalid) {
		t.Fatalf("structural-invalid effects = %#v", fixture.effects.events)
	}
}

func TestSystemCloseAndDueListingUseBoundedSystemSeam(t *testing.T) {
	fixture := newFixture(t)
	closing := fixture.sitting(t)
	_ = closing.Open(testNow)
	_ = closing.EnterClosing(model.ExamSittingReasonScheduledEndReached, testNow)
	closed := cloneSitting(closing)
	_ = closed.Close(testNow)
	fixture.persistence.snapshot = &store.ExamSittingSnapshot{Sitting: closing, AcademicUnitID: fixture.unitID}
	fixture.persistence.lifecycle = lifecycleResult(closed, store.ExamSittingTransitionClosedNoAttempts, true, false)
	call := SystemCall{JobID: model.NewJobID(), AttemptID: model.NewJobAttemptID()}

	if _, err := fixture.service.FinishSealing(context.Background(), call, fixture.sittingID); err != nil {
		t.Fatal(err)
	}
	if fixture.persistence.finish == nil || len(fixture.effects.events) != 1 {
		t.Fatalf("finish input/effects = %#v %#v", fixture.persistence.finish, fixture.effects.events)
	}

	dueAt := testNow.Add(-time.Minute)
	fixture.persistence.due = []store.ExamSittingLifecycleDue{{Value: &store.ExamSittingSnapshot{Sitting: closing}, DueAt: dueAt}}
	items, err := fixture.service.ListLifecycleDue(context.Background(), store.ExamSittingLifecycleDueOptions{Limit: 50})
	if err != nil || len(items) != 1 || !items[0].DueAt.Equal(dueAt) || fixture.persistence.dueOptions.Limit != 50 {
		t.Fatalf("ListLifecycleDue() = %#v, %v", items, err)
	}
}

func cloneSitting(value *model.ExamSitting) *model.ExamSitting {
	copy := *value
	return &copy
}

func lifecycleResult(value *model.ExamSitting, transition store.ExamSittingLifecycleTransitionCode, changed, replayed bool) *store.ExamSittingLifecycleResult {
	return &store.ExamSittingLifecycleResult{Value: &store.ExamSittingSnapshot{Sitting: value}, Transition: transition, Changed: changed, Replayed: replayed}
}
