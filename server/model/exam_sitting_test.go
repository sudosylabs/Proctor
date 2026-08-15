// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"testing"
	"time"
)

func TestNewExamSittingCreatesScheduledHalfOpenDelivery(t *testing.T) {
	t.Parallel()
	createdAt := time.Date(2026, time.August, 16, 9, 0, 0, 0, time.FixedZone("source", 2*60*60))
	startAt := createdAt.Add(24 * time.Hour)
	endAt := startAt.Add(2 * time.Hour)

	sitting, err := NewExamSitting(NewExamSittingID(), NewExamID(), NewExamRevisionID(), NewClassID(), startAt, endAt, createdAt)
	if err != nil {
		t.Fatal(err)
	}
	if sitting.State != ExamSittingScheduled || sitting.Revision != 1 {
		t.Fatalf("new Sitting state/revision = %q/%d", sitting.State, sitting.Revision)
	}
	if sitting.ScheduledStartAt.Location() != time.UTC || sitting.ScheduledEndAt.Location() != time.UTC || sitting.CreatedAt.Location() != time.UTC {
		t.Fatalf("new Sitting times = %#v", sitting)
	}
	if sitting.CreatedAt != sitting.UpdatedAt || sitting.CanceledAt.Valid || sitting.ReasonCode != "" {
		t.Fatalf("new Sitting lifecycle = %#v", sitting)
	}
	if err = sitting.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestExamSittingApplyScheduleIsScheduledOnlyAndAtomic(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 16, 9, 0, 0, 0, time.UTC)
	sitting, err := NewExamSitting(NewExamSittingID(), NewExamID(), NewExamRevisionID(), NewClassID(), at.Add(time.Hour), at.Add(2*time.Hour), at)
	if err != nil {
		t.Fatal(err)
	}

	revisionID, classID := NewExamRevisionID(), NewClassID()
	startAt, endAt := at.Add(48*time.Hour), at.Add(51*time.Hour)
	changed, err := sitting.ApplySchedule(revisionID, classID, startAt, endAt, at.Add(time.Minute))
	if err != nil || !changed {
		t.Fatalf("ApplySchedule() = %v, %v", changed, err)
	}
	if sitting.ExamRevisionID != revisionID || sitting.ClassID != classID || sitting.ScheduledStartAt != startAt ||
		sitting.ScheduledEndAt != endAt || sitting.Revision != 2 || sitting.UpdatedAt != at.Add(time.Minute) {
		t.Fatalf("updated Sitting = %#v", sitting)
	}
	unchanged := *sitting
	changed, err = sitting.ApplySchedule(revisionID, classID, startAt, endAt, at.Add(2*time.Minute))
	if err != nil || changed || *sitting != unchanged {
		t.Fatalf("no-op ApplySchedule() = %v, %v, Sitting=%#v", changed, err, sitting)
	}

	sitting.State = ExamSittingOpen
	before := *sitting
	if changed, err = sitting.ApplySchedule(NewExamRevisionID(), NewClassID(), startAt.Add(time.Hour), endAt.Add(time.Hour), at.Add(3*time.Minute)); err == nil || changed || *sitting != before {
		t.Fatalf("open ApplySchedule() = %v, %v, Sitting=%#v", changed, err, sitting)
	}
}

func TestExamSittingCancelRetainsOnlySafeReasonAndIsTerminal(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 16, 9, 0, 0, 0, time.UTC)
	sitting, err := NewExamSitting(NewExamSittingID(), NewExamID(), NewExamRevisionID(), NewClassID(), at.Add(time.Hour), at.Add(2*time.Hour), at)
	if err != nil {
		t.Fatal(err)
	}

	if err = sitting.Cancel(ExamSittingReasonManagerCanceled, at.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if sitting.State != ExamSittingCanceled || sitting.ReasonCode != ExamSittingReasonManagerCanceled ||
		!sitting.CanceledAt.Valid || sitting.CanceledAt.Time != at.Add(time.Minute) || sitting.UpdatedAt != at.Add(time.Minute) || sitting.Revision != 2 {
		t.Fatalf("canceled Sitting = %#v", sitting)
	}
	before := *sitting
	if err = sitting.Cancel(ExamSittingReasonManagerCanceled, at.Add(2*time.Minute)); err == nil || *sitting != before {
		t.Fatalf("repeated Cancel() error=%v Sitting=%#v", err, sitting)
	}

	invalid, err := NewExamSitting(NewExamSittingID(), NewExamID(), NewExamRevisionID(), NewClassID(), at.Add(time.Hour), at.Add(2*time.Hour), at)
	if err != nil {
		t.Fatal(err)
	}
	invalidBefore := *invalid
	if err = invalid.Cancel(ExamSittingReasonCode("private teacher explanation"), at.Add(time.Minute)); err == nil || *invalid != invalidBefore {
		t.Fatalf("invalid Cancel() error=%v Sitting=%#v", err, invalid)
	}
}

func TestExamSittingValidateAcceptsOnlyCoherentLifecycleTimestamps(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 16, 9, 0, 0, 0, time.UTC)
	base, err := NewExamSitting(NewExamSittingID(), NewExamID(), NewExamRevisionID(), NewClassID(), at.Add(time.Hour), at.Add(2*time.Hour), at)
	if err != nil {
		t.Fatal(err)
	}
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
			return value
		}(),
		func() ExamSitting {
			value := *base
			value.State, value.OpenedAt, value.ClosingAt, value.ClosedAt, value.UpdatedAt = ExamSittingClosed, OptionalTimeFrom(openedAt), OptionalTimeFrom(closingAt), OptionalTimeFrom(closedAt), closedAt
			return value
		}(),
	}
	for index := range valid {
		if err = valid[index].Validate(); err != nil {
			t.Fatalf("valid lifecycle %d: %v", index, err)
		}
	}

	bad := valid[2]
	bad.OpenedAt = OptionalTime{}
	if err = bad.Validate(); err == nil {
		t.Fatal("Paused Sitting without opened_at was accepted")
	}
	bad = valid[4]
	bad.ClosedAt = OptionalTimeFrom(closingAt.Add(-time.Second))
	if err = bad.Validate(); err == nil {
		t.Fatal("Closed Sitting with reversed lifecycle times was accepted")
	}
}
