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
	Sitting *model.ExamSitting
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
	ChangedAt        time.Time
	AuditEventID     string
	AuditAt          int64
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
	Schedule(context.Context, *ExamSittingSchedule, *CommandIdempotency) (*ExamSittingCommandResult, error)
	UpdateSchedule(context.Context, *ExamSittingScheduleUpdate, *CommandIdempotency) (*ExamSittingCommandResult, error)
	Cancel(context.Context, *ExamSittingCancellation, *CommandIdempotency) (*ExamSittingCommandResult, error)
}
