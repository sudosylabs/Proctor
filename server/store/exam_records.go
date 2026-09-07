// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package store

import (
	"context"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

const (
	ExamRecordsCompleteOperation    = "exam.records.complete.v1"
	ExamRecordsWaiveReviewOperation = "exam.records.waive_review.v1"
	ExamRecordsCreateHoldOperation  = "exam.records.hold.create.v1"
	ExamRecordsReleaseHoldOperation = "exam.records.hold.release.v1"
)

type ExamRecordsScope struct {
	Scope           model.RetentionHoldScope
	InstitutionID   model.InstitutionID
	AcademicUnitID  model.AcademicUnitID
	CandidateUserID model.UserID
	AttemptID       model.ExamAttemptID
}

// ExamRecordsCompletionSnapshot exposes bounded counts, not student work.
// PendingReviews counts Submissions with neither a finalized Review nor a
// current waiver. Counts and state belong to the same database snapshot.
type ExamRecordsCompletionSnapshot struct {
	Completion      model.ExamSittingRecordsCompletion
	SittingState    model.ExamSittingState
	SubmissionCount int64
	PendingReviews  int64
}

type ExamRecordsMutation struct {
	Scope        model.RetentionHoldScope
	Principal    model.Principal
	Action       model.Action
	AuditEventID string
	AuditAt      int64
}

type ExamRecordsCompletion struct {
	ExamRecordsMutation
	ExpectedRevision             int64
	AcknowledgedEvidenceRevision int64
}

type ExamRecordsReviewWaiver struct {
	ExamRecordsMutation
	ExpectedRevision         int64 // zero creates the first waiver
	ExpectedReviewRevision   int64
	ExpectedDiscrepancyCount int64
	ReasonCode               string
	PrivateReason            string
}

type ExamRecordsHoldCreation struct {
	ExamRecordsMutation
	HoldID        model.RetentionHoldID
	ReasonCode    string
	PrivateReason string
}

type ExamRecordsHoldRelease struct {
	ExamRecordsMutation
	HoldID                  model.RetentionHoldID
	ExpectedRevision        int64
	ReasonCode              string
	PrivateReason           string
	RecentAuthenticationTTL time.Duration
}

type ExamRecordsHoldListOptions struct {
	Scope           model.RetentionHoldScope
	After           model.RetentionHoldID
	Limit           int
	IncludeReleased bool
}

type ExamRecordsHoldPage struct {
	Holds   []model.RetentionHold
	HasMore bool
}

type ExamRecordsCompletionResult struct {
	Completion *model.ExamSittingRecordsCompletion
	Replayed   bool
}
type ExamRecordsWaiverResult struct {
	Waiver   *model.SubmissionReviewWaiver
	Replayed bool
}
type ExamRecordsHoldResult struct {
	Hold     *model.RetentionHold
	Replayed bool
}

// ExamRecordsStore owns records completion and preservation. App preflights
// authorization before reads. Mutations reauthorize the current credential,
// Role, scope, Manager relationship and exact membership in the transaction,
// including idempotent replay. Completion locks the Closed Sitting and checks
// every Submission has a finalized Review or inventory-current waiver. Waivers
// exclude the candidate actor and all undecided Flags. New accepted integrity
// data stales completion atomically; exact retries do not. Holds are durable,
// bounded, manually released and apply to existing and later descendants.
// Hold release requires a current strong recent protected administrator Session.
// The transition, critical audit completion and retry outcome commit together.
// No operation here retires content, schedules cleanup or creates an export.
type ExamRecordsStore interface {
	Resolve(context.Context, model.RetentionHoldScope) (*ExamRecordsScope, error)
	GetCompletion(context.Context, model.ExamID, model.ExamSittingID) (*ExamRecordsCompletionSnapshot, error)
	FindReviewWaiver(context.Context, model.RetentionHoldScope) (*model.SubmissionReviewWaiver, bool, error)
	CompleteRecords(context.Context, *ExamRecordsCompletion, *CommandIdempotency) (*ExamRecordsCompletionResult, error)
	WaiveReview(context.Context, *ExamRecordsReviewWaiver, *CommandIdempotency) (*ExamRecordsWaiverResult, error)
	ListHolds(context.Context, ExamRecordsHoldListOptions) (*ExamRecordsHoldPage, error)
	CreateHold(context.Context, *ExamRecordsHoldCreation, *CommandIdempotency) (*ExamRecordsHoldResult, error)
	ReleaseHold(context.Context, *ExamRecordsHoldRelease, *CommandIdempotency) (*ExamRecordsHoldResult, error)
}
