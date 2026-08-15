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

// AllowsCandidateAdmission is the lifecycle-only half of admission. Current
// Class membership and Attempt/Participation policy remain separate checks.
func (state ExamSittingState) AllowsCandidateAdmission() bool {
	return state == ExamSittingOpen
}

// AllowsCandidateMutation gates workspace writes, execution, and submission
// before the later Attempt- and Participation-specific checks are applied.
func (state ExamSittingState) AllowsCandidateMutation() bool {
	return state == ExamSittingOpen
}

// AllowsProtectedPresentation retains read-only authored material while a live
// Sitting is paused; terminal and closing states expose no live candidate view.
func (state ExamSittingState) AllowsProtectedPresentation() bool {
	return state == ExamSittingOpen || state == ExamSittingPaused
}

// RequiresIntegrityMonitoring remains true through Pause even though mutable
// candidate capabilities are blocked.
func (state ExamSittingState) RequiresIntegrityMonitoring() bool {
	return state == ExamSittingOpen || state == ExamSittingPaused
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
	ExamSittingReasonManagerCanceled          ExamSittingReasonCode = "manager_canceled"
	ExamSittingReasonScheduleElapsed          ExamSittingReasonCode = "schedule_elapsed"
	ExamSittingReasonAcademicStructureInvalid ExamSittingReasonCode = "academic_structure_invalid"
	ExamSittingReasonManagerClosed            ExamSittingReasonCode = "manager_closed"
	ExamSittingReasonScheduledEndReached      ExamSittingReasonCode = "scheduled_end_reached"
)

func (reason ExamSittingReasonCode) IsValid() bool {
	switch reason {
	case ExamSittingReasonManagerCanceled, ExamSittingReasonScheduleElapsed,
		ExamSittingReasonAcademicStructureInvalid, ExamSittingReasonManagerClosed,
		ExamSittingReasonScheduledEndReached:
		return true
	default:
		return false
	}
}

func (reason ExamSittingReasonCode) isCancellation() bool {
	return reason == ExamSittingReasonManagerCanceled || reason == ExamSittingReasonScheduleElapsed ||
		reason == ExamSittingReasonAcademicStructureInvalid
}

func (reason ExamSittingReasonCode) isClosing() bool {
	return reason == ExamSittingReasonManagerClosed || reason == ExamSittingReasonScheduledEndReached
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
	if !reason.isCancellation() {
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

// Open starts delivery once. PostgreSQL owns the schedule-boundary and current
// academic-structure decision; this method owns only the local transition.
func (s *ExamSitting) Open(at time.Time) error {
	if s == nil {
		return invalidModelError("ExamSitting.Open", "exam_sitting", "value", "is required", "")
	}
	if s.State != ExamSittingScheduled {
		return invalidModelError("ExamSitting.Open", "exam_sitting", "state", "must be scheduled", "id="+s.ID.String())
	}
	candidate := *s
	candidate.State = ExamSittingOpen
	candidate.OpenedAt = OptionalTimeFrom(examSittingTransitionTime(s.UpdatedAt, at))
	candidate.UpdatedAt = candidate.OpenedAt.Time
	candidate.Revision++
	return s.applyLifecycleCandidate(candidate)
}

// Pause blocks mutable candidate capabilities without changing the fixed v1
// deadline or discarding the first-open time.
func (s *ExamSitting) Pause(at time.Time) error {
	if s == nil {
		return invalidModelError("ExamSitting.Pause", "exam_sitting", "value", "is required", "")
	}
	if s.State != ExamSittingOpen {
		return invalidModelError("ExamSitting.Pause", "exam_sitting", "state", "must be open", "id="+s.ID.String())
	}
	candidate := *s
	candidate.State = ExamSittingPaused
	candidate.PausedAt = OptionalTimeFrom(examSittingTransitionTime(s.UpdatedAt, at))
	candidate.UpdatedAt = candidate.PausedAt.Time
	candidate.Revision++
	return s.applyLifecycleCandidate(candidate)
}

// Resume clears only the current pause marker. OpenedAt and ScheduledEndAt are
// preserved; version 1 never extends the deadline by paused duration.
func (s *ExamSitting) Resume(at time.Time) error {
	if s == nil {
		return invalidModelError("ExamSitting.Resume", "exam_sitting", "value", "is required", "")
	}
	if s.State != ExamSittingPaused {
		return invalidModelError("ExamSitting.Resume", "exam_sitting", "state", "must be paused", "id="+s.ID.String())
	}
	candidate := *s
	candidate.State = ExamSittingOpen
	candidate.PausedAt = OptionalTime{}
	candidate.UpdatedAt = examSittingTransitionTime(s.UpdatedAt, at)
	candidate.Revision++
	return s.applyLifecycleCandidate(candidate)
}

// ExtendEnd advances the sole v1 delivery deadline for an Open or Paused
// Sitting. Equality and shortening are rejected rather than treated as no-op.
func (s *ExamSitting) ExtendEnd(endAt, at time.Time) error {
	if s == nil {
		return invalidModelError("ExamSitting.ExtendEnd", "exam_sitting", "value", "is required", "")
	}
	if s.State != ExamSittingOpen && s.State != ExamSittingPaused {
		return invalidModelError("ExamSitting.ExtendEnd", "exam_sitting", "state", "must be open or paused", "id="+s.ID.String())
	}
	endAt = TimeUTC(endAt)
	if !endAt.After(s.ScheduledEndAt) {
		return invalidModelError("ExamSitting.ExtendEnd", "exam_sitting", "scheduled_end_at", "must extend the current deadline", "id="+s.ID.String())
	}
	candidate := *s
	candidate.ScheduledEndAt = endAt
	candidate.UpdatedAt = examSittingTransitionTime(s.UpdatedAt, at)
	candidate.Revision++
	return s.applyLifecycleCandidate(candidate)
}

// EnterClosing denies new participation and mutable candidate work. The safe
// terminal cause is retained through Closed; private manager rationale lives
// only in dedicated persistence provenance.
func (s *ExamSitting) EnterClosing(reason ExamSittingReasonCode, at time.Time) error {
	if s == nil {
		return invalidModelError("ExamSitting.EnterClosing", "exam_sitting", "value", "is required", "")
	}
	if s.State != ExamSittingOpen && s.State != ExamSittingPaused {
		return invalidModelError("ExamSitting.EnterClosing", "exam_sitting", "state", "must be open or paused", "id="+s.ID.String())
	}
	if !reason.isClosing() {
		return invalidModelError("ExamSitting.EnterClosing", "exam_sitting", "reason_code", "must be a closing reason", "id="+s.ID.String())
	}
	candidate := *s
	candidate.State = ExamSittingClosing
	candidate.PausedAt = OptionalTime{}
	candidate.ClosingAt = OptionalTimeFrom(examSittingTransitionTime(s.UpdatedAt, at))
	candidate.UpdatedAt = candidate.ClosingAt.Time
	candidate.ReasonCode = reason
	candidate.Revision++
	return s.applyLifecycleCandidate(candidate)
}

// Close completes a Sitting that has already entered Closing.
func (s *ExamSitting) Close(at time.Time) error {
	if s == nil {
		return invalidModelError("ExamSitting.Close", "exam_sitting", "value", "is required", "")
	}
	if s.State != ExamSittingClosing {
		return invalidModelError("ExamSitting.Close", "exam_sitting", "state", "must be closing", "id="+s.ID.String())
	}
	candidate := *s
	candidate.State = ExamSittingClosed
	candidate.ClosedAt = OptionalTimeFrom(examSittingTransitionTime(s.UpdatedAt, at))
	candidate.UpdatedAt = candidate.ClosedAt.Time
	candidate.Revision++
	return s.applyLifecycleCandidate(candidate)
}

func (s *ExamSitting) applyLifecycleCandidate(candidate ExamSitting) error {
	if err := candidate.Validate(); err != nil {
		return err
	}
	*s = candidate
	return nil
}

func examSittingTransitionTime(notBefore, at time.Time) time.Time {
	at = TimeUTC(at)
	if at.Before(notBefore) {
		return notBefore
	}
	return at
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
			s.PausedAt.Valid || s.ClosedAt.Valid || s.CanceledAt.Valid || !s.ReasonCode.isClosing() {
			return invalidModelError(where, "exam_sitting", "lifecycle", "does not match closing state", details)
		}
	case ExamSittingClosed:
		if !validExamSittingLifecycleTime(s.OpenedAt, s.CreatedAt, s.UpdatedAt) || !validExamSittingLifecycleTime(s.ClosingAt, s.OpenedAt.Time, s.UpdatedAt) ||
			!validExamSittingLifecycleTime(s.ClosedAt, s.ClosingAt.Time, s.UpdatedAt) || s.PausedAt.Valid || s.CanceledAt.Valid || !s.ReasonCode.isClosing() {
			return invalidModelError(where, "exam_sitting", "lifecycle", "does not match closed state", details)
		}
	case ExamSittingCanceled:
		if s.OpenedAt.Valid || s.PausedAt.Valid || s.ClosingAt.Valid || s.ClosedAt.Valid ||
			!s.CanceledAt.Valid || s.CanceledAt.Time.IsZero() || s.CanceledAt.Time.Location() != time.UTC ||
			s.CanceledAt.Time.Before(s.CreatedAt) || s.CanceledAt.Time.After(s.UpdatedAt) || !s.ReasonCode.isCancellation() {
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
