// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

// ExamSittingSnapshot is the exact bounded application projection for one
// Sitting. It contains no Class roster, authored Revision content, private
// cancellation rationale, or authorization intermediate.
type ExamSittingSnapshot struct {
	Sitting        *model.ExamSitting
	AcademicUnitID model.AcademicUnitID
}

// ExamSittingListOptions defines the bounded Exam-scoped catalog query.
// ClassID and States are optional filters. OverlapStartAt and OverlapEndAt are
// either both zero or one nonempty half-open interval matched against the
// Sitting's scheduled start and scheduled end. The complete keyset cursor is
// (BeforeScheduledStartAt, BeforeSittingID), both descending.
type ExamSittingListOptions struct {
	ExamID                 model.ExamID
	ClassID                model.ClassID
	States                 []model.ExamSittingState
	OverlapStartAt         time.Time
	OverlapEndAt           time.Time
	BeforeScheduledStartAt time.Time
	BeforeSittingID        model.ExamSittingID
	Limit                  int
}

// ExamSittingSchedule is the complete new Sitting plus the already-authorized
// actor and durable audit attempt. Persistence rechecks active Exam, current
// Manager unless override is explicit, same-Exam published Revision, exact
// active Class lineage, and Academic Period containment atomically.
type ExamSittingSchedule struct {
	Sitting         *model.ExamSitting
	OpenJob         *model.Job
	DeadlineJob     *model.Job
	ActorUserID     model.UserID
	ManagerOverride bool
	AuditEventID    string
	AuditAt         int64
}

// ExamSittingScheduleUpdate replaces the complete Scheduled-only selection
// behind one optimistic Sitting revision fence.
type ExamSittingScheduleUpdate struct {
	ExamID           model.ExamID
	SittingID        model.ExamSittingID
	ActorUserID      model.UserID
	ManagerOverride  bool
	ExpectedRevision int64
	ExamRevisionID   model.ExamRevisionID
	ClassID          model.ClassID
	ScheduledStartAt time.Time
	ScheduledEndAt   time.Time
	OpenJob          *model.Job
	DeadlineJob      *model.Job
	ChangedAt        time.Time
	AuditEventID     string
	AuditAt          int64
}

// ExamSittingManagerTransition is the common revision-fenced manager command
// for Pause, Resume, and EarlyClose. PrivateReason is retained only in the
// append-only private action provenance, never in the returned snapshot,
// ordinary audit, command outcome, or realtime event.
type ExamSittingManagerTransition struct {
	ExamID           model.ExamID
	SittingID        model.ExamSittingID
	ActorUserID      model.UserID
	ManagerOverride  bool
	ExpectedRevision int64
	FinalizeJob      *model.Job
	PrivateReason    string
	ChangedAt        time.Time
	AuditEventID     string
	AuditAt          int64
}

type ExamSittingExtension struct {
	ExamID           model.ExamID
	SittingID        model.ExamSittingID
	ActorUserID      model.UserID
	ManagerOverride  bool
	ExpectedRevision int64
	ScheduledEndAt   time.Time
	DeadlineJob      *model.Job
	PrivateReason    string
	ChangedAt        time.Time
	AuditEventID     string
	AuditAt          int64
}

type ExamSittingLifecycleTransitionCode string

const (
	ExamSittingTransitionOpened                   ExamSittingLifecycleTransitionCode = "scheduled_start_reached"
	ExamSittingTransitionManagerPaused            ExamSittingLifecycleTransitionCode = "manager_paused"
	ExamSittingTransitionManagerResumed           ExamSittingLifecycleTransitionCode = "manager_resumed"
	ExamSittingTransitionManagerExtended          ExamSittingLifecycleTransitionCode = "manager_extended"
	ExamSittingTransitionManagerClosed            ExamSittingLifecycleTransitionCode = "manager_closed"
	ExamSittingTransitionAcademicStructureInvalid ExamSittingLifecycleTransitionCode = "academic_structure_invalid"
	ExamSittingTransitionScheduleElapsed          ExamSittingLifecycleTransitionCode = "schedule_elapsed"
	ExamSittingTransitionScheduledEndReached      ExamSittingLifecycleTransitionCode = "scheduled_end_reached"
	ExamSittingTransitionClosedNoAttempts         ExamSittingLifecycleTransitionCode = "closed_no_attempts"
	ExamSittingTransitionSealingCompleted         ExamSittingLifecycleTransitionCode = "sealing_completed"
)

type ExamSittingLifecycleResult struct {
	Value      *ExamSittingSnapshot
	Transition ExamSittingLifecycleTransitionCode
	Changed    bool
	Replayed   bool
}

type ExamSittingLifecycleDueOptions struct {
	AfterDueAt     time.Time
	AfterSittingID model.ExamSittingID
	Limit          int
}

type ExamSittingLifecycleDue struct {
	Value *ExamSittingSnapshot
	DueAt time.Time
}

type ExamSittingDueAdvance struct {
	SittingID    model.ExamSittingID
	FinalizeJob  *model.Job
	AuditEventID string
	AuditAt      int64
}

// ExamSittingFinishSealing closes only when every created Attempt is terminal
// and owns a sealed Submission. It is safe to invoke after every bounded pass;
// incomplete work leaves the Sitting visibly Closing.
type ExamSittingFinishSealing struct {
	SittingID    model.ExamSittingID
	AuditEventID string
	AuditAt      int64
}

type ExamSittingNoShowListOptions struct {
	SittingID            model.ExamSittingID
	AfterCandidateUserID model.UserID
	Limit                int
}

// ExamSittingNoShow is derived from Class membership active at the Sitting's
// actual OpenedAt and absence of an Attempt. It contains no profile data.
type ExamSittingNoShow struct {
	CandidateUserID model.UserID
}

// ExamSittingCancellation cancels one Scheduled Sitting. PrivateReason is
// normalized, bounded manager-only material retained in dedicated private
// persistence/provenance. It must never enter ordinary audit fields. The
// returned Sitting contains only model.ExamSittingReasonManagerCanceled.
type ExamSittingCancellation struct {
	ExamID           model.ExamID
	SittingID        model.ExamSittingID
	ActorUserID      model.UserID
	ManagerOverride  bool
	ExpectedRevision int64
	PrivateReason    string
	CanceledAt       time.Time
	AuditEventID     string
	AuditAt          int64
}

type ExamSittingCommandResult struct {
	Value    *ExamSittingSnapshot
	Replayed bool
}

// ExamSittingStore owns bounded Sitting discovery and atomic scheduling
// mutations. Every fresh mutation locks and rechecks the active Exam, current
// Manager relationship unless override is explicit, same-Exam sealed Revision,
// active Class lineage in the Exam's exact Academic Unit, and complete interval
// containment in the Class's Academic Period. The Sitting transition, audit
// completion, and idempotent outcome commit together. Exact replays return the
// committed snapshot before stale, state, archive, or relationship checks.
type ExamSittingStore interface {
	// Resolve returns the minimal exact Sitting needed to map an authorization
	// resource to its owning Exam. It never returns private cancellation
	// rationale or authored Revision content.
	Resolve(context.Context, model.ExamSittingID) (*ExamSittingSnapshot, error)
	Get(context.Context, model.ExamID, model.ExamSittingID) (*ExamSittingSnapshot, error)
	// List returns at most Limit snapshots in descending
	// (ScheduledStartAt, SittingID) order. Limit accepts at most 201 so the
	// application can request one bounded look-ahead row for a public page of 200.
	List(context.Context, ExamSittingListOptions) ([]ExamSittingSnapshot, error)
	ListLifecycleDue(context.Context, ExamSittingLifecycleDueOptions) ([]ExamSittingLifecycleDue, error)
	Schedule(context.Context, *ExamSittingSchedule, *CommandIdempotency) (*ExamSittingCommandResult, error)
	UpdateSchedule(context.Context, *ExamSittingScheduleUpdate, *CommandIdempotency) (*ExamSittingCommandResult, error)
	Cancel(context.Context, *ExamSittingCancellation, *CommandIdempotency) (*ExamSittingCommandResult, error)
	Pause(context.Context, *ExamSittingManagerTransition, *CommandIdempotency) (*ExamSittingLifecycleResult, error)
	Resume(context.Context, *ExamSittingManagerTransition, *CommandIdempotency) (*ExamSittingLifecycleResult, error)
	Extend(context.Context, *ExamSittingExtension, *CommandIdempotency) (*ExamSittingLifecycleResult, error)
	EarlyClose(context.Context, *ExamSittingManagerTransition, *CommandIdempotency) (*ExamSittingLifecycleResult, error)
	AdvanceDue(context.Context, *ExamSittingDueAdvance) (*ExamSittingLifecycleResult, error)
	FinishSealing(context.Context, *ExamSittingFinishSealing) (*ExamSittingLifecycleResult, error)
	ListNoShows(context.Context, ExamSittingNoShowListOptions) ([]ExamSittingNoShow, error)
}
