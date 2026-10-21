// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sitting

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestPauseAuthorizesCurrentManagerAndKeepsPrivateReasonOutOfAuditAndEffect(t *testing.T) {
	fixture := newFixture(t)
	current := fixture.sitting(t)
	require.NoError(t, current.Open(testNow))
	paused := cloneSitting(current)
	require.NoError(t, paused.Pause(testNow.Add(time.Minute)))
	fixture.persistence.lifecycle = lifecycleResult(paused, store.ExamSittingTransitionManagerPaused, true, false)

	got, err := fixture.service.PauseExamSitting(context.Background(), fixture.invocation, PauseCommand{
		ExamID: fixture.examID, SittingID: fixture.sittingID, ExpectedRevision: current.Revision,
		PrivateReason: "Candidate requested clarification", IdempotencyKey: "test-key",
	})
	require.NoError(t, err)
	require.NotNil(t, got.Sitting)
	require.Equal(t, model.ExamSittingPaused, got.Sitting.State)
	require.Equal(t, model.ActionExamSittingManage, fixture.authorizer.action, "authorization")
	require.Equal(t, model.Resource{Type: model.ResourceExamSitting, ID: fixture.sittingID.String()}, fixture.authorizer.resource, "authorization")
	wantAudit := map[string]any{"exam_id": fixture.examID.String(), "exam_sitting_id": fixture.sittingID.String(),
		"expected_sitting_revision": current.Revision, "transition": string(store.ExamSittingTransitionManagerPaused)}
	require.Equal(t, wantAudit, fixture.auditor.value)
	require.NotNil(t, fixture.persistence.pause)
	require.Equal(t, "Candidate requested clarification", fixture.persistence.pause.PrivateReason)
	wantIdempotency, err := prepareTransitionIdempotency(fixture.invocation, idempotencyOperationPause, PauseCommand{
		ExamID: fixture.examID, SittingID: fixture.sittingID, ExpectedRevision: current.Revision,
		PrivateReason: "Candidate requested clarification", IdempotencyKey: "test-key",
	})
	require.NoError(t, err)
	require.Equal(t, wantIdempotency, fixture.persistence.command)
	require.Len(t, fixture.effects.events, 1)
	require.Equal(t, string(store.ExamSittingTransitionManagerPaused), fixture.effects.events[0].kind)
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
				return f.service.PauseExamSitting(context.Background(), f.invocation, PauseCommand{ExamID: f.examID, SittingID: f.sittingID,
					ExpectedRevision: current.Revision, PrivateReason: "Investigating active incident", IdempotencyKey: "test-key"})
			}, input: func(fake *sittingStoreFake) any { return fake.pause }, wantAction: model.ActionExamSittingManage},
		{name: "resume rejects archived Exam", archived: true, transition: store.ExamSittingTransitionManagerResumed,
			run: func(f fixture, current *model.ExamSitting) (store.ExamSittingSnapshot, error) {
				return f.service.ResumeExamSitting(context.Background(), f.invocation, ResumeCommand{ExamID: f.examID, SittingID: f.sittingID,
					ExpectedRevision: current.Revision, PrivateReason: "Issue resolved", IdempotencyKey: "test-key"})
			}, wantFault: "exam.archived"},
		{name: "resume active Sitting", transition: store.ExamSittingTransitionManagerResumed,
			run: func(f fixture, current *model.ExamSitting) (store.ExamSittingSnapshot, error) {
				return f.service.ResumeExamSitting(context.Background(), f.invocation, ResumeCommand{ExamID: f.examID, SittingID: f.sittingID,
					ExpectedRevision: current.Revision, PrivateReason: "Issue resolved", IdempotencyKey: "test-key"})
			}, input: func(fake *sittingStoreFake) any { return fake.resume }, wantAction: model.ActionExamSittingManage},
		{name: "extend rejects archived Exam", archived: true, transition: store.ExamSittingTransitionManagerExtended,
			run: func(f fixture, current *model.ExamSitting) (store.ExamSittingSnapshot, error) {
				return f.service.ExtendExamSitting(context.Background(), f.invocation, ExtendCommand{ExamID: f.examID, SittingID: f.sittingID,
					ExpectedRevision: current.Revision, ScheduledEndAt: current.ScheduledEndAt.Add(time.Hour),
					PrivateReason: "Compensating for disruption", IdempotencyKey: "test-key"})
			}, wantFault: "exam.archived"},
		{name: "extend active Sitting", transition: store.ExamSittingTransitionManagerExtended,
			run: func(f fixture, current *model.ExamSitting) (store.ExamSittingSnapshot, error) {
				return f.service.ExtendExamSitting(context.Background(), f.invocation, ExtendCommand{ExamID: f.examID, SittingID: f.sittingID,
					ExpectedRevision: current.Revision, ScheduledEndAt: current.ScheduledEndAt.Add(time.Hour),
					PrivateReason: "Compensating for disruption", IdempotencyKey: "test-key"})
			}, input: func(fake *sittingStoreFake) any { return fake.extend }, wantAction: model.ActionExamSittingManage},
		{name: "early close remains available after archive", archived: true, transition: store.ExamSittingTransitionManagerClosed,
			run: func(f fixture, current *model.ExamSitting) (store.ExamSittingSnapshot, error) {
				return f.service.CloseExamSitting(context.Background(), f.invocation, EarlyCloseCommand{ExamID: f.examID, SittingID: f.sittingID,
					ExpectedRevision: current.Revision, PrivateReason: "Safety stop", IdempotencyKey: "test-key"})
			}, input: func(fake *sittingStoreFake) any { return fake.earlyClose }, wantAction: model.ActionExamSittingManage, finalizes: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newFixture(t)
			current := fixture.sitting(t)
			require.NoError(t, current.Open(testNow))
			if test.transition == store.ExamSittingTransitionManagerResumed {
				require.NoError(t, current.Pause(testNow))
			}
			if test.archived {
				require.NoError(t, fixture.access.snapshot.Exam.Archive(testNow))
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
				require.Nil(t, fixture.persistence.resume, "archived command reached Store")
				require.Nil(t, fixture.persistence.extend, "archived command reached Store")
				require.Nil(t, fixture.persistence.earlyClose, "archived command reached Store")
				return
			}
			require.NoError(t, err)
			require.NotNil(t, test.input(fixture.persistence), "Store input/action")
			require.Equal(t, test.wantAction, fixture.authorizer.action, "Store input/action")
			var wantIdempotency *store.CommandIdempotency
			switch test.transition {
			case store.ExamSittingTransitionManagerPaused:
				wantIdempotency, err = prepareTransitionIdempotency(fixture.invocation, idempotencyOperationPause, PauseCommand{
					ExamID: fixture.examID, SittingID: fixture.sittingID, ExpectedRevision: current.Revision,
					PrivateReason: "Investigating active incident", IdempotencyKey: "test-key",
				})
			case store.ExamSittingTransitionManagerResumed:
				wantIdempotency, err = prepareTransitionIdempotency(fixture.invocation, idempotencyOperationResume, PauseCommand{
					ExamID: fixture.examID, SittingID: fixture.sittingID, ExpectedRevision: current.Revision,
					PrivateReason: "Issue resolved", IdempotencyKey: "test-key",
				})
			case store.ExamSittingTransitionManagerExtended:
				wantIdempotency, err = prepareExtensionIdempotency(fixture.invocation, ExtendCommand{
					ExamID: fixture.examID, SittingID: fixture.sittingID, ExpectedRevision: current.Revision,
					ScheduledEndAt: current.ScheduledEndAt.Add(time.Hour), PrivateReason: "Compensating for disruption",
					IdempotencyKey: "test-key",
				})
			case store.ExamSittingTransitionManagerClosed:
				wantIdempotency, err = prepareTransitionIdempotency(fixture.invocation, idempotencyOperationClose, PauseCommand{
					ExamID: fixture.examID, SittingID: fixture.sittingID, ExpectedRevision: current.Revision,
					PrivateReason: "Safety stop", IdempotencyKey: "test-key",
				})
			}
			require.NoError(t, err)
			require.Equal(t, wantIdempotency, fixture.persistence.command)
			if test.finalizes {
				require.Equal(t, current.Revision+1, fixture.jobs.finalizeRevision, "finalize intent = revision %d at %s", fixture.jobs.finalizeRevision, fixture.jobs.finalizeAvailableAt)
				require.True(t, fixture.jobs.finalizeAvailableAt.Equal(testNow), "finalize intent = revision %d at %s", fixture.jobs.finalizeRevision, fixture.jobs.finalizeAvailableAt)
			}
		})
	}

	fixture := newFixture(t)
	current := fixture.sitting(t)
	_ = current.Open(testNow)
	paused := cloneSitting(current)
	_ = paused.Pause(testNow)
	fixture.persistence.lifecycle = lifecycleResult(paused, store.ExamSittingTransitionManagerPaused, true, true)
	_, err := fixture.service.PauseExamSitting(context.Background(), fixture.invocation, PauseCommand{ExamID: fixture.examID, SittingID: fixture.sittingID,
		ExpectedRevision: current.Revision, PrivateReason: "Replay", IdempotencyKey: "test-key"})
	require.NoError(t, err)
	require.Empty(t, fixture.effects.events)
}

func TestPauseUsesExplicitOverrideWithoutCurrentExactUnitMembership(t *testing.T) {
	fixture := newFixture(t)
	fixture.memberships.items = nil
	current := fixture.sitting(t)
	_ = current.Open(testNow)
	paused := cloneSitting(current)
	_ = paused.Pause(testNow)
	fixture.persistence.lifecycle = lifecycleResult(paused, store.ExamSittingTransitionManagerPaused, true, false)

	_, err := fixture.service.PauseExamSitting(context.Background(), fixture.invocation, PauseCommand{ExamID: fixture.examID,
		SittingID: fixture.sittingID, ExpectedRevision: current.Revision, PrivateReason: "Administrator intervention",
		IdempotencyKey: "test-key"})
	require.NoError(t, err)
	require.Equal(t, model.ActionExamSittingManageOverride, fixture.authorizer.action, "override authorization/store")
	require.NotNil(t, fixture.persistence.pause, "override authorization/store")
	require.True(t, fixture.persistence.pause.ManagerOverride, "override authorization/store")
}

func TestLifecycleCommandsValidatePrivateReasonBounds(t *testing.T) {
	fixture := newFixture(t)
	commands := []struct {
		name string
		run  func(string) error
	}{
		{"pause", func(reason string) error {
			_, err := fixture.service.PauseExamSitting(context.Background(), fixture.invocation, PauseCommand{ExamID: fixture.examID, SittingID: fixture.sittingID, ExpectedRevision: 1, PrivateReason: reason, IdempotencyKey: "test-key"})
			return err
		}},
		{"resume", func(reason string) error {
			_, err := fixture.service.ResumeExamSitting(context.Background(), fixture.invocation, ResumeCommand{ExamID: fixture.examID, SittingID: fixture.sittingID, ExpectedRevision: 1, PrivateReason: reason, IdempotencyKey: "test-key"})
			return err
		}},
		{"extend", func(reason string) error {
			_, err := fixture.service.ExtendExamSitting(context.Background(), fixture.invocation, ExtendCommand{ExamID: fixture.examID, SittingID: fixture.sittingID, ExpectedRevision: 1, ScheduledEndAt: testNow.Add(time.Hour), PrivateReason: reason, IdempotencyKey: "test-key"})
			return err
		}},
		{"close", func(reason string) error {
			_, err := fixture.service.CloseExamSitting(context.Background(), fixture.invocation, EarlyCloseCommand{ExamID: fixture.examID, SittingID: fixture.sittingID, ExpectedRevision: 1, PrivateReason: reason, IdempotencyKey: "test-key"})
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
	require.NoError(t, opened.Open(testNow))
	fixture.persistence.lifecycle = lifecycleResult(opened, store.ExamSittingTransitionOpened, true, false)
	system := invocation.System{JobID: model.NewJobID(), AttemptID: model.NewJobAttemptID()}

	got, err := fixture.service.AdvanceDue(context.Background(), system, fixture.sittingID)
	require.NoError(t, err)
	require.NotNil(t, got.Value, "AdvanceDue() = %#v, audit=%#v, input", got, fixture.systemAudit)
	require.Equal(t, model.ExamSittingOpen, got.Value.Sitting.State, "AdvanceDue() = %#v, audit=%#v, input", got, fixture.systemAudit)
	require.Equal(t, system, fixture.systemAudit.system, "AdvanceDue() = %#v, audit=%#v, input", got, fixture.systemAudit)
	require.Equal(t, model.ActionExamSittingManage, fixture.systemAudit.action, "AdvanceDue() = %#v, audit=%#v, input", got, fixture.systemAudit)
	require.Equal(t, model.RoleScopeAcademicUnit, fixture.systemAudit.scopeType, "AdvanceDue() = %#v, audit=%#v, input", got, fixture.systemAudit)
	require.Equal(t, fixture.unitID.String(), fixture.systemAudit.scopeID, "AdvanceDue() = %#v, audit=%#v, input", got, fixture.systemAudit)
	require.NotNil(t, fixture.persistence.advance, "AdvanceDue() = %#v, audit=%#v, input", got, fixture.systemAudit)
	require.Len(t, fixture.effects.events, 1)
	require.Equal(t, string(store.ExamSittingTransitionOpened), fixture.effects.events[0].kind)
	require.True(t, fixture.jobs.finalizeAvailableAt.Equal(current.ScheduledEndAt), "system finalize availability = %s, want deadline", fixture.jobs.finalizeAvailableAt)

	fixture.effects.events = nil
	fixture.persistence.lifecycle = lifecycleResult(current, "", false, false)
	_, err = fixture.service.AdvanceDue(context.Background(), system, fixture.sittingID)
	require.NoError(t, err)
	require.Empty(t, fixture.effects.events)

	structurallyInvalid := cloneSitting(current)
	err = structurallyInvalid.Cancel(model.ExamSittingReasonAcademicStructureInvalid, testNow)
	require.NoError(t, err)
	fixture.persistence.lifecycle = lifecycleResult(structurallyInvalid, store.ExamSittingTransitionAcademicStructureInvalid, true, false)
	_, err = fixture.service.AdvanceDue(context.Background(), system, fixture.sittingID)
	require.NoError(t, err)
	require.Len(t, fixture.effects.events, 1)
	require.Equal(t, string(store.ExamSittingTransitionAcademicStructureInvalid), fixture.effects.events[0].kind)
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
	system := invocation.System{JobID: model.NewJobID(), AttemptID: model.NewJobAttemptID()}

	_, err := fixture.service.FinishSealing(context.Background(), system, fixture.sittingID)
	require.NoError(t, err)
	require.NotNil(t, fixture.persistence.finish, "finish input/effects")
	require.Len(t, fixture.effects.events, 1, "finish input/effects")

	dueAt := testNow.Add(-time.Minute)
	fixture.persistence.due = []store.ExamSittingLifecycleDue{{Value: &store.ExamSittingSnapshot{Sitting: closing}, DueAt: dueAt}}
	items, err := fixture.service.ListLifecycleDue(context.Background(), store.ExamSittingLifecycleDueOptions{Limit: 50})
	require.NoError(t, err, "ListLifecycleDue()")
	require.Len(t, items, 1, "ListLifecycleDue()")
	require.True(t, items[0].DueAt.Equal(dueAt), "ListLifecycleDue()")
	require.Equal(t, 50, fixture.persistence.dueOptions.Limit, "ListLifecycleDue()")
}

func cloneSitting(value *model.ExamSitting) *model.ExamSitting {
	copy := *value
	return &copy
}

func lifecycleResult(value *model.ExamSitting, transition store.ExamSittingLifecycleTransitionCode, changed, replayed bool) *store.ExamSittingLifecycleResult {
	return &store.ExamSittingLifecycleResult{Value: &store.ExamSittingSnapshot{Sitting: value}, Transition: transition, Changed: changed, Replayed: replayed}
}
