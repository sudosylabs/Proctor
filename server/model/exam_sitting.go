// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import "time"

// ExamSittingState identifies one durable lifecycle position of an Exam
// Sitting.
type ExamSittingState string

const (
	ExamSittingScheduled ExamSittingState = "scheduled"
	ExamSittingOpen      ExamSittingState = "open"
	ExamSittingPaused    ExamSittingState = "paused"
	ExamSittingClosing   ExamSittingState = "closing"
	ExamSittingClosed    ExamSittingState = "closed"
	ExamSittingCanceled  ExamSittingState = "canceled"
)

func (state ExamSittingState) IsValid() bool {
	switch state {
	case ExamSittingScheduled, ExamSittingOpen, ExamSittingPaused, ExamSittingClosing, ExamSittingClosed, ExamSittingCanceled:
		return true
	default:
		return false
	}
}

// ExamSitting is one scheduled delivery of an immutable Exam Revision to one
// exact Class. Cross-row Revision, Class-lineage, and Academic-Period interval
// checks belong to the atomic Store operation.
type ExamSitting struct {
	ID               ExamSittingID
	ExamID           ExamID
	ExamRevisionID   ExamRevisionID
	ClassID          ClassID
	ScheduledStartAt time.Time
	ScheduledEndAt   time.Time
	State            ExamSittingState
	CreatedAt        time.Time
	UpdatedAt        time.Time
	OpenedAt         OptionalTime
	PausedAt         OptionalTime
	ClosingAt        OptionalTime
	ClosedAt         OptionalTime
	CanceledAt       OptionalTime
	ReasonCode       ExamSittingReasonCode
	Revision         int64
}

// ExamSittingReasonCode is a bounded, candidate-safe lifecycle reason. Private
// manager rationale belongs to dedicated private persistence/provenance and
// must never enter ordinary audit fields.
type ExamSittingReasonCode string

const (
	ExamSittingReasonManagerCanceled ExamSittingReasonCode = "manager_canceled"
	ExamSittingReasonScheduleElapsed ExamSittingReasonCode = "schedule_elapsed"
)

func (reason ExamSittingReasonCode) IsValid() bool {
	return reason == ExamSittingReasonManagerCanceled || reason == ExamSittingReasonScheduleElapsed
}

// NewExamSitting constructs a Scheduled Sitting. The application supplies
// identity and time; ScheduledEndAt is the sole v1 delivery deadline.
func NewExamSitting(id ExamSittingID, examID ExamID, examRevisionID ExamRevisionID, classID ClassID, scheduledStartAt, scheduledEndAt, at time.Time) (*ExamSitting, error) {
	sitting := &ExamSitting{
		ID: id, ExamID: examID, ExamRevisionID: examRevisionID, ClassID: classID,
		ScheduledStartAt: TimeUTC(scheduledStartAt), ScheduledEndAt: TimeUTC(scheduledEndAt),
		State: ExamSittingScheduled, CreatedAt: TimeUTC(at), UpdatedAt: TimeUTC(at), Revision: 1,
	}
	if err := sitting.Validate(); err != nil {
		return nil, err
	}
	return sitting, nil
}

// ApplySchedule atomically changes the complete pre-open delivery selection.
// Once lifecycle processing has begun, schedule extension is owned by its
// separate transition and this operation fails without mutation.
func (s *ExamSitting) ApplySchedule(examRevisionID ExamRevisionID, classID ClassID, scheduledStartAt, scheduledEndAt, at time.Time) (bool, error) {
	if s == nil {
		return false, invalidModelError("ExamSitting.ApplySchedule", "exam_sitting", "value", "is required", "")
	}
	if s.State != ExamSittingScheduled {
		return false, invalidModelError("ExamSitting.ApplySchedule", "exam_sitting", "state", "must be scheduled", "id="+s.ID.String())
	}
	scheduledStartAt, scheduledEndAt = TimeUTC(scheduledStartAt), TimeUTC(scheduledEndAt)
	if s.ExamRevisionID == examRevisionID && s.ClassID == classID && s.ScheduledStartAt == scheduledStartAt && s.ScheduledEndAt == scheduledEndAt {
		return false, nil
	}
	candidate := *s
	candidate.ExamRevisionID = examRevisionID
	candidate.ClassID = classID
	candidate.ScheduledStartAt = scheduledStartAt
	candidate.ScheduledEndAt = scheduledEndAt
	candidate.UpdatedAt = TimeUTC(at)
	if candidate.UpdatedAt.Before(s.UpdatedAt) {
		candidate.UpdatedAt = s.UpdatedAt
	}
	candidate.Revision++
	if err := candidate.Validate(); err != nil {
		return false, err
	}
	*s = candidate
	return true, nil
}

// Cancel records only a stable candidate-safe reason. Private manager rationale
// belongs to dedicated private persistence/provenance and never enters this
// public domain entity or ordinary audit fields.
func (s *ExamSitting) Cancel(reason ExamSittingReasonCode, at time.Time) error {
	if s == nil {
		return invalidModelError("ExamSitting.Cancel", "exam_sitting", "value", "is required", "")
	}
	if s.State != ExamSittingScheduled {
		return invalidModelError("ExamSitting.Cancel", "exam_sitting", "state", "must be scheduled", "id="+s.ID.String())
	}
	if !reason.IsValid() {
		return invalidModelError("ExamSitting.Cancel", "exam_sitting", "reason_code", "must be candidate-safe", "id="+s.ID.String())
	}
	candidate := *s
	candidate.State = ExamSittingCanceled
	candidate.ReasonCode = reason
	candidate.CanceledAt = OptionalTimeFrom(at)
	candidate.UpdatedAt = TimeUTC(at)
	if candidate.UpdatedAt.Before(s.UpdatedAt) {
		candidate.UpdatedAt = s.UpdatedAt
		candidate.CanceledAt = OptionalTimeFrom(s.UpdatedAt)
	}
	candidate.Revision++
	if err := candidate.Validate(); err != nil {
		return err
	}
	*s = candidate
	return nil
}

// Validate checks complete rehydrated local Sitting state. Aggregate
// relationships and Academic Period containment require persistence and are
// deliberately outside this model.
func (s *ExamSitting) Validate() error {
	const where = "ExamSitting.Validate"
	if s == nil {
		return invalidModelError(where, "exam_sitting", "value", "is required", "")
	}
	if !s.ID.IsValid() || !s.ExamID.IsValid() || !s.ExamRevisionID.IsValid() || !s.ClassID.IsValid() {
		return invalidModelError(where, "exam_sitting", "identity", "must contain valid identifiers", "")
	}
	details := "id=" + s.ID.String()
	if !s.State.IsValid() {
		return invalidModelError(where, "exam_sitting", "state", "must be valid", details)
	}
	if s.ScheduledStartAt.IsZero() || s.ScheduledEndAt.IsZero() || !s.ScheduledStartAt.Before(s.ScheduledEndAt) {
		return invalidModelError(where, "exam_sitting", "schedule", "must be a nonempty half-open interval", details)
	}
	if s.ScheduledStartAt.Location() != time.UTC || s.ScheduledEndAt.Location() != time.UTC ||
		s.CreatedAt.Location() != time.UTC || s.UpdatedAt.Location() != time.UTC {
		return invalidModelError(where, "exam_sitting", "timestamps", "must use UTC", details)
	}
	if s.CreatedAt.IsZero() || s.UpdatedAt.IsZero() || s.UpdatedAt.Before(s.CreatedAt) {
		return invalidModelError(where, "exam_sitting", "timestamps", "must be ordered and nonzero", details)
	}
	switch s.State {
	case ExamSittingScheduled:
		if s.OpenedAt.Valid || s.PausedAt.Valid || s.ClosingAt.Valid || s.ClosedAt.Valid || s.CanceledAt.Valid || s.ReasonCode != "" {
			return invalidModelError(where, "exam_sitting", "lifecycle", "does not match scheduled state", details)
		}
	case ExamSittingOpen:
		if !validExamSittingLifecycleTime(s.OpenedAt, s.CreatedAt, s.UpdatedAt) || s.PausedAt.Valid || s.ClosingAt.Valid || s.ClosedAt.Valid || s.CanceledAt.Valid || s.ReasonCode != "" {
			return invalidModelError(where, "exam_sitting", "lifecycle", "does not match open state", details)
		}
	case ExamSittingPaused:
		if !validExamSittingLifecycleTime(s.OpenedAt, s.CreatedAt, s.UpdatedAt) || !validExamSittingLifecycleTime(s.PausedAt, s.OpenedAt.Time, s.UpdatedAt) ||
			s.ClosingAt.Valid || s.ClosedAt.Valid || s.CanceledAt.Valid || s.ReasonCode != "" {
			return invalidModelError(where, "exam_sitting", "lifecycle", "does not match paused state", details)
		}
	case ExamSittingClosing:
		if !validExamSittingLifecycleTime(s.OpenedAt, s.CreatedAt, s.UpdatedAt) || !validExamSittingLifecycleTime(s.ClosingAt, s.OpenedAt.Time, s.UpdatedAt) ||
			s.PausedAt.Valid || s.ClosedAt.Valid || s.CanceledAt.Valid || s.ReasonCode != "" {
			return invalidModelError(where, "exam_sitting", "lifecycle", "does not match closing state", details)
		}
	case ExamSittingClosed:
		if !validExamSittingLifecycleTime(s.OpenedAt, s.CreatedAt, s.UpdatedAt) || !validExamSittingLifecycleTime(s.ClosingAt, s.OpenedAt.Time, s.UpdatedAt) ||
			!validExamSittingLifecycleTime(s.ClosedAt, s.ClosingAt.Time, s.UpdatedAt) || s.PausedAt.Valid || s.CanceledAt.Valid || s.ReasonCode != "" {
			return invalidModelError(where, "exam_sitting", "lifecycle", "does not match closed state", details)
		}
	case ExamSittingCanceled:
		if s.OpenedAt.Valid || s.PausedAt.Valid || s.ClosingAt.Valid || s.ClosedAt.Valid ||
			!s.CanceledAt.Valid || s.CanceledAt.Time.IsZero() || s.CanceledAt.Time.Location() != time.UTC ||
			s.CanceledAt.Time.Before(s.CreatedAt) || s.CanceledAt.Time.After(s.UpdatedAt) || !s.ReasonCode.IsValid() {
			return invalidModelError(where, "exam_sitting", "lifecycle", "does not match canceled state", details)
		}
	}
	if s.Revision < 1 {
		return invalidModelError(where, "exam_sitting", "revision", "must be positive", details)
	}
	return nil
}

func validExamSittingLifecycleTime(value OptionalTime, notBefore, notAfter time.Time) bool {
	return value.Valid && !value.Time.IsZero() && value.Time.Location() == time.UTC && !value.Time.Before(notBefore) && !value.Time.After(notAfter)
}
