// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewExamSittingCreatesScheduledHalfOpenDelivery(t *testing.T) {
	t.Parallel()
	createdAt := time.Date(2026, time.August, 16, 9, 0, 0, 0, time.FixedZone("source", 2*60*60))
	startAt := createdAt.Add(24 * time.Hour)
	endAt := startAt.Add(2 * time.Hour)

	sitting, err := NewExamSitting(NewExamSittingID(), NewExamID(), NewExamRevisionID(), NewClassID(), startAt, endAt, createdAt)
	require.NoError(t, err)
	require.Equal(t, ExamSittingScheduled, sitting.State, "new Sitting state/revision")
	require.Equal(t, int64(1), sitting.Revision, "new Sitting state/revision")
	require.Same(t, time.UTC, sitting.ScheduledStartAt.Location())
	require.Same(t, time.UTC, sitting.ScheduledEndAt.Location())
	require.Same(t, time.UTC, sitting.CreatedAt.Location())
	require.Equal(t, sitting.UpdatedAt, sitting.CreatedAt)
	require.False(t, sitting.CanceledAt.Valid)
	require.Empty(t, sitting.ReasonCode)
	err = sitting.Validate()
	require.NoError(t, err)
}

func TestExamSittingApplyScheduleIsScheduledOnlyAndAtomic(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 16, 9, 0, 0, 0, time.UTC)
	sitting, err := NewExamSitting(NewExamSittingID(), NewExamID(), NewExamRevisionID(), NewClassID(), at.Add(time.Hour), at.Add(2*time.Hour), at)
	require.NoError(t, err)

	revisionID, classID := NewExamRevisionID(), NewClassID()
	startAt, endAt := at.Add(48*time.Hour), at.Add(51*time.Hour)
	changed, err := sitting.ApplySchedule(revisionID, classID, startAt, endAt, at.Add(time.Minute))
	require.NoError(t, err, "ApplySchedule()")
	require.True(t, changed, "ApplySchedule()")
	require.Equal(t, revisionID, sitting.ExamRevisionID)
	require.Equal(t, classID, sitting.ClassID)
	require.Equal(t, startAt, sitting.ScheduledStartAt)
	require.Equal(t, endAt, sitting.ScheduledEndAt)
	require.Equal(t, int64(2), sitting.Revision)
	require.Equal(t, at.Add(time.Minute), sitting.UpdatedAt)
	unchanged := *sitting
	changed, err = sitting.ApplySchedule(revisionID, classID, startAt, endAt, at.Add(2*time.Minute))
	require.NoError(t, err, "no-op ApplySchedule() = %v, %v, Sitting=%#v", changed, err, sitting)
	require.False(t, changed, "no-op ApplySchedule() = %v, %v, Sitting=%#v", changed, err, sitting)
	require.Equal(t, unchanged, *sitting, "no-op ApplySchedule() = %v, %v, Sitting=%#v", changed, err, sitting)

	sitting.State = ExamSittingOpen
	before := *sitting
	changed, err = sitting.ApplySchedule(NewExamRevisionID(), NewClassID(), startAt.Add(time.Hour), endAt.Add(time.Hour), at.Add(3*time.Minute))
	require.Error(t, err, "open ApplySchedule() = %v, %v, Sitting=%#v", changed, err, sitting)
	require.False(t, changed, "open ApplySchedule() = %v, %v, Sitting=%#v", changed, err, sitting)
	require.Equal(t, before, *sitting, "open ApplySchedule() = %v, %v, Sitting=%#v", changed, err, sitting)
}

func TestExamSittingCancelRetainsOnlySafeReasonAndIsTerminal(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 16, 9, 0, 0, 0, time.UTC)
	sitting, err := NewExamSitting(NewExamSittingID(), NewExamID(), NewExamRevisionID(), NewClassID(), at.Add(time.Hour), at.Add(2*time.Hour), at)
	require.NoError(t, err)

	err = sitting.Cancel(ExamSittingReasonManagerCanceled, at.Add(time.Minute))
	require.NoError(t, err)
	require.Equal(t, ExamSittingCanceled, sitting.State)
	require.Equal(t, ExamSittingReasonManagerCanceled, sitting.ReasonCode)
	require.True(t, sitting.CanceledAt.Valid)
	require.Equal(t, at.Add(time.Minute), sitting.CanceledAt.Time)
	require.Equal(t, at.Add(time.Minute), sitting.UpdatedAt)
	require.Equal(t, int64(2), sitting.Revision)
	before := *sitting
	err = sitting.Cancel(ExamSittingReasonManagerCanceled, at.Add(2*time.Minute))
	require.Error(t, err, "repeated Cancel() error=%v Sitting=%#v", err, sitting)
	require.Equal(t, before, *sitting, "repeated Cancel() error=%v Sitting=%#v", err, sitting)

	invalid, err := NewExamSitting(NewExamSittingID(), NewExamID(), NewExamRevisionID(), NewClassID(), at.Add(time.Hour), at.Add(2*time.Hour), at)
	require.NoError(t, err)
	invalidBefore := *invalid
	err = invalid.Cancel(ExamSittingReasonCode("private teacher explanation"), at.Add(time.Minute))
	require.Error(t, err, "invalid Cancel() error=%v Sitting=%#v", err, invalid)
	require.Equal(t, invalidBefore, *invalid, "invalid Cancel() error=%v Sitting=%#v", err, invalid)
}

func TestExamSittingValidateAcceptsOnlyCoherentLifecycleTimestamps(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 16, 9, 0, 0, 0, time.UTC)
	base, err := NewExamSitting(NewExamSittingID(), NewExamID(), NewExamRevisionID(), NewClassID(), at.Add(time.Hour), at.Add(2*time.Hour), at)
	require.NoError(t, err)
	openedAt, pausedAt := at.Add(time.Hour), at.Add(70*time.Minute)
	closingAt, closedAt := at.Add(2*time.Hour), at.Add(121*time.Minute)

	valid := []ExamSitting{
		func() ExamSitting { value := *base; return value }(),
		func() ExamSitting {
			value := *base
			value.State, value.OpenedAt, value.UpdatedAt = ExamSittingOpen, OptionalTimeFrom(openedAt), openedAt
			return value
		}(),
		func() ExamSitting {
			value := *base
			value.State, value.OpenedAt, value.PausedAt, value.UpdatedAt = ExamSittingPaused, OptionalTimeFrom(openedAt), OptionalTimeFrom(pausedAt), pausedAt
			return value
		}(),
		func() ExamSitting {
			value := *base
			value.State, value.OpenedAt, value.ClosingAt, value.UpdatedAt = ExamSittingClosing, OptionalTimeFrom(openedAt), OptionalTimeFrom(closingAt), closingAt
			value.ReasonCode = ExamSittingReasonScheduledEndReached
			return value
		}(),
		func() ExamSitting {
			value := *base
			value.State, value.OpenedAt, value.ClosingAt, value.ClosedAt, value.UpdatedAt = ExamSittingClosed, OptionalTimeFrom(openedAt), OptionalTimeFrom(closingAt), OptionalTimeFrom(closedAt), closedAt
			value.ReasonCode = ExamSittingReasonScheduledEndReached
			return value
		}(),
	}
	for index := range valid {
		err = valid[index].Validate()
		require.NoError(t, err, "valid lifecycle %d", index)
	}

	bad := valid[2]
	bad.OpenedAt = OptionalTime{}
	err = bad.Validate()
	require.Error(t, err, "Paused Sitting without opened_at was accepted")
	bad = valid[4]
	bad.ClosedAt = OptionalTimeFrom(closingAt.Add(-time.Second))
	err = bad.Validate()
	require.Error(t, err, "Closed Sitting with reversed lifecycle times was accepted")
}

func TestExamSittingLifecycleTransitionsAreClosedAndAtomic(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 16, 9, 0, 0, 0, time.UTC)
	sitting, err := NewExamSitting(NewExamSittingID(), NewExamID(), NewExamRevisionID(), NewClassID(), at.Add(time.Hour), at.Add(2*time.Hour), at)
	require.NoError(t, err)

	openedAt := at.Add(time.Hour)
	err = sitting.Open(openedAt)
	require.NoError(t, err)
	require.Equal(t, ExamSittingOpen, sitting.State)
	require.True(t, sitting.OpenedAt.Valid)
	require.Equal(t, openedAt, sitting.OpenedAt.Time)
	require.Equal(t, int64(2), sitting.Revision)
	require.Empty(t, sitting.ReasonCode)

	pausedAt := openedAt.Add(10 * time.Minute)
	err = sitting.Pause(pausedAt)
	require.NoError(t, err)
	require.Equal(t, ExamSittingPaused, sitting.State)
	require.True(t, sitting.PausedAt.Valid)
	require.Equal(t, pausedAt, sitting.PausedAt.Time)
	require.Equal(t, int64(3), sitting.Revision)
	require.Empty(t, sitting.ReasonCode)

	extendedEnd := sitting.ScheduledEndAt.Add(30 * time.Minute)
	err = sitting.ExtendEnd(extendedEnd, pausedAt.Add(time.Minute))
	require.NoError(t, err)
	require.Equal(t, ExamSittingPaused, sitting.State)
	require.Equal(t, extendedEnd, sitting.ScheduledEndAt)
	require.Equal(t, int64(4), sitting.Revision)
	require.Empty(t, sitting.ReasonCode)
	beforeInvalidExtension := *sitting
	err = sitting.ExtendEnd(extendedEnd, pausedAt.Add(2*time.Minute))
	require.Error(t, err, "non-extension error=%v Sitting=%#v", err, sitting)
	require.Equal(t, beforeInvalidExtension, *sitting, "non-extension error=%v Sitting=%#v", err, sitting)

	resumedAt := pausedAt.Add(3 * time.Minute)
	err = sitting.Resume(resumedAt)
	require.NoError(t, err)
	require.Equal(t, ExamSittingOpen, sitting.State)
	require.False(t, sitting.PausedAt.Valid)
	require.Equal(t, openedAt, sitting.OpenedAt.Time)
	require.Equal(t, int64(5), sitting.Revision)
	require.Empty(t, sitting.ReasonCode)

	closingAt := resumedAt.Add(time.Minute)
	err = sitting.EnterClosing(ExamSittingReasonManagerClosed, closingAt)
	require.NoError(t, err)
	require.Equal(t, ExamSittingClosing, sitting.State)
	require.True(t, sitting.ClosingAt.Valid)
	require.Equal(t, closingAt, sitting.ClosingAt.Time)
	require.Equal(t, int64(6), sitting.Revision)
	require.Equal(t, ExamSittingReasonManagerClosed, sitting.ReasonCode)

	closedAt := closingAt.Add(time.Second)
	err = sitting.Close(closedAt)
	require.NoError(t, err)
	require.Equal(t, ExamSittingClosed, sitting.State)
	require.True(t, sitting.ClosedAt.Valid)
	require.Equal(t, closedAt, sitting.ClosedAt.Time)
	require.Equal(t, int64(7), sitting.Revision)
	require.Equal(t, ExamSittingReasonManagerClosed, sitting.ReasonCode)
	beforeTerminalMutation := *sitting
	err = sitting.Pause(closedAt.Add(time.Second))
	require.Error(t, err, "terminal Pause() error=%v Sitting=%#v", err, sitting)
	require.Equal(t, beforeTerminalMutation, *sitting, "terminal Pause() error=%v Sitting=%#v", err, sitting)
}

func TestExamSittingAutomaticCancellationReasonsAreBounded(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 16, 9, 0, 0, 0, time.UTC)
	for _, reason := range []ExamSittingReasonCode{ExamSittingReasonScheduleElapsed, ExamSittingReasonAcademicStructureInvalid} {
		sitting, err := NewExamSitting(NewExamSittingID(), NewExamID(), NewExamRevisionID(), NewClassID(), at.Add(time.Hour), at.Add(2*time.Hour), at)
		require.NoError(t, err)
		err = sitting.Cancel(reason, at.Add(time.Minute))
		require.NoError(t, err, "Cancel(%q)", reason)
		require.Equal(t, reason, sitting.ReasonCode, "Cancel(%q) = %#v", reason, sitting)
		require.Equal(t, ExamSittingCanceled, sitting.State, "Cancel(%q) = %#v", reason, sitting)
	}
}

func TestExamSittingStateCapabilityPredicatesFailClosed(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		state                  ExamSittingState
		admit, mutate, present bool
		monitor                bool
	}{
		{state: ExamSittingScheduled},
		{state: ExamSittingOpen, admit: true, mutate: true, present: true, monitor: true},
		{state: ExamSittingPaused, present: true, monitor: true},
		{state: ExamSittingClosing},
		{state: ExamSittingClosed},
		{state: ExamSittingCanceled},
		{state: ExamSittingState("future")},
	} {
		t.Run(string(test.state), func(t *testing.T) {
			require.Equal(t, test.admit, test.state.AllowsCandidateAdmission(), "capabilities for %q did not fail closed", test.state)
			require.Equal(t, test.mutate, test.state.AllowsCandidateMutation(), "capabilities for %q did not fail closed", test.state)
			require.Equal(t, test.present, test.state.AllowsProtectedPresentation(), "capabilities for %q did not fail closed", test.state)
			require.Equal(t, test.monitor, test.state.RequiresIntegrityMonitoring(), "capabilities for %q did not fail closed", test.state)
		})
	}
}

func TestExamSittingRetargetRevisionIsLiveOnlyAndAtomic(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 16, 9, 0, 0, 0, time.UTC)
	for _, state := range []ExamSittingState{ExamSittingOpen, ExamSittingPaused} {
		t.Run(string(state), func(t *testing.T) {
			sitting, err := NewExamSitting(NewExamSittingID(), NewExamID(), NewExamRevisionID(), NewClassID(), at.Add(time.Hour), at.Add(3*time.Hour), at)
			require.NoError(t, err)
			err = sitting.Open(at.Add(time.Hour))
			require.NoError(t, err)
			if state == ExamSittingPaused {
				err = sitting.Pause(at.Add(70 * time.Minute))
				require.NoError(t, err)
			}
			previousState, previousRevision := sitting.State, sitting.Revision
			newRevisionID := NewExamRevisionID()
			err = sitting.RetargetRevision(newRevisionID, at.Add(80*time.Minute))
			require.NoError(t, err)
			require.Equal(t, newRevisionID, sitting.ExamRevisionID)
			require.Equal(t, previousState, sitting.State)
			require.Equal(t, previousRevision+1, sitting.Revision)
			require.Equal(t, at.Add(80*time.Minute), sitting.UpdatedAt)
			before := *sitting
			err = sitting.RetargetRevision(newRevisionID, at.Add(90*time.Minute))
			require.Error(t, err, "same-Revision retarget error=%v Sitting=%#v", err, sitting)
			require.Equal(t, before, *sitting, "same-Revision retarget error=%v Sitting=%#v", err, sitting)
		})
	}
	scheduled, err := NewExamSitting(NewExamSittingID(), NewExamID(), NewExamRevisionID(), NewClassID(), at.Add(time.Hour), at.Add(3*time.Hour), at)
	require.NoError(t, err)
	before := *scheduled
	err = scheduled.RetargetRevision(NewExamRevisionID(), at.Add(time.Minute))
	require.Error(t, err, "Scheduled retarget error=%v Sitting=%#v", err, scheduled)
	require.Equal(t, before, *scheduled, "Scheduled retarget error=%v Sitting=%#v", err, scheduled)
}
