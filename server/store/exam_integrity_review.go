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
	ExamIntegrityReviewDecisionOperation      = "exam.integrity_review.decision.v1"
	ExamIntegrityReviewDraftOperation         = "exam.integrity_review.draft.v1"
	ExamIntegrityReviewFinalizeOperation      = "exam.integrity_review.finalize.v1"
	ExamIntegrityReviewReleaseOperation       = "exam.integrity_review.release.v1"
	ExamIntegrityReviewEvidenceReadMaximum    = 100
	ExamIntegrityReviewDiscrepancyReadMaximum = 100
	ExamIntegrityReviewFlagReadMaximum        = 200
)

// ExamIntegrityReviewAuthorization is the canonical ownership projection for
// review/release authorization and nested-route concealment.
type ExamIntegrityReviewAuthorization struct {
	SubmissionID    model.SubmissionID
	ExamID          model.ExamID
	SittingID       model.ExamSittingID
	AttemptID       model.ExamAttemptID
	CandidateUserID model.UserID
	AcademicUnitID  model.AcademicUnitID
}

// ExamIntegrityFlagSummary is the bounded manager projection of one Flag. It
// contains no evidence details or private candidate-authored material.
type ExamIntegrityFlagSummary struct {
	Flag                   model.IntegrityFlag
	EvidenceCount          int
	OverflowCount          int64
	UnresolvedMissingCount int64
}

type ExamIntegrityFlagListOptions struct {
	SubmissionID model.SubmissionID
	AfterFlagID  model.IntegrityFlagID
	Limit        int
}

type ExamIntegrityFlagPage struct {
	Items   []ExamIntegrityFlagSummary
	HasMore bool
}

type ExamIntegrityEvidenceListOptions struct {
	SubmissionID    model.SubmissionID
	FlagID          model.IntegrityFlagID
	AfterEvidenceID model.IntegrityEvidenceID
	Limit           int
}

type ExamIntegrityEvidencePage struct {
	Items   []model.IntegrityEvidence
	HasMore bool
}

type ExamIntegrityDiscrepancyListOptions struct {
	SubmissionID       model.SubmissionID
	AfterDiscrepancyID model.IntegrityDiscrepancyID
	Limit              int
}

type ExamIntegrityDiscrepancyPage struct {
	Items   []model.IntegrityDiscrepancy
	HasMore bool
}

// ExamSubmissionReviewSnapshot is the bounded manager-visible Review state.
// Decision rationale and manager notes are deliberately present only here,
// behind current review authorization; candidate result reads use a separate
// projection.
type ExamSubmissionReviewSnapshot struct {
	Authorization ExamIntegrityReviewAuthorization
	Submission    *model.ExamSubmission
	Review        *model.SubmissionReview
	Decisions     []model.IntegrityReviewDecision
}

type ExamIntegrityReviewDecisionMutation struct {
	SubmissionID             model.SubmissionID
	ReviewID                 model.SubmissionReviewID
	DecisionID               model.IntegrityReviewDecisionID
	FlagID                   model.IntegrityFlagID
	ActorUserID              model.UserID
	ManagerOverride          bool
	ExpectedReviewRevision   int64
	ExpectedDecisionRevision int64
	Outcome                  model.IntegrityReviewOutcome
	PrivateRationale         string
	ChangedAt                time.Time
	AuditEventID             string
	AuditAt                  int64
}

type ExamIntegrityReviewDraftMutation struct {
	SubmissionID           model.SubmissionID
	ReviewID               model.SubmissionReviewID
	ActorUserID            model.UserID
	ManagerOverride        bool
	ExpectedReviewRevision int64
	ManagerNotes           string
	StudentRemarksMarkdown string
	ChangedAt              time.Time
	AuditEventID           string
	AuditAt                int64
}

type ExamIntegrityReviewFinalize struct {
	SubmissionID           model.SubmissionID
	ReviewID               model.SubmissionReviewID
	ActorUserID            model.UserID
	ManagerOverride        bool
	ExpectedReviewRevision int64
	ChangedAt              time.Time
	AuditEventID           string
	AuditAt                int64
}

type ExamIntegrityReviewRelease struct {
	SubmissionID              model.SubmissionID
	ReviewID                  model.SubmissionReviewID
	ActorUserID               model.UserID
	ManagerOverride           bool
	ExpectedReviewRevision    int64
	ChangedAt                 time.Time
	AuditEventID              string
	AuditAt                   int64
	CandidateUserID           model.UserID
	Notice                    *PreparedMail
	ExpectedRecipientRevision int64
}

// ExamIntegrityReviewReleasePreparation reserves the PostgreSQL release time
// for a fresh transition and identifies an already-retained one-way release so
// the application never prepares another candidate notification on replay.
type ExamIntegrityReviewReleasePreparation struct {
	Replayed  bool
	ReleaseAt time.Time
}

type ExamIntegrityReviewMutationResult struct {
	Authorization ExamIntegrityReviewAuthorization
	Review        *model.SubmissionReview
	Decision      *model.IntegrityReviewDecision
	Replayed      bool
}

// ExamIntegrityReviewStore owns the one Review aggregate for a sealed
// Submission. Manager reads are purpose-specific and bounded. Each mutation
// rechecks the canonical Submission/Attempt lineage, current Exam Manager or
// explicit override, optimistic revisions, audit attempt, and exact command
// replay under one transaction.
//
// SaveDecision and UpdateDraft create the sole draft Review when expected
// Review revision is zero, otherwise mutate only the exact current draft.
// Finalize locks the terminal Submission, complete Flag/evidence inventory,
// Review, and decisions; requires one decision per Flag; rejects any missing,
// changed, or over-limit inventory; snapshots immutable inventory rows and a
// canonical digest; freezes the Review; and commits audit/outcome atomically.
// PrepareRelease locks the exact finalized Review, distinguishes its retained
// one-way release, and durably reserves a millisecond PostgreSQL release time
// without changing Review state. Release is a distinct one-way authorized
// transaction that locks and consumes that exact preparation. Fresh
// release first locks and revalidates the canonical candidate User and frozen
// recipient revision, then the authorization hierarchy, Submission/Attempt,
// and Review. It commits the released Review, safe audit result, one semantic
// candidate occurrence, queued or terminally suppressed delivery, delivery
// Job, and retained command outcome atomically. Encrypted mail holds the
// primary-key fence through commit. Failure commits none of them. Exact
// replays repeat current authorization/audit, return the retained result, and
// require or insert no notice. GetReleasedStudentResult exposes only the
// candidate-owned released projection and conceals all pre-release state.
type ExamIntegrityReviewStore interface {
	Resolve(context.Context, model.SubmissionID) (*ExamIntegrityReviewAuthorization, error)
	Get(context.Context, model.SubmissionID) (*ExamSubmissionReviewSnapshot, error)
	ListFlags(context.Context, ExamIntegrityFlagListOptions) (*ExamIntegrityFlagPage, error)
	ListEvidence(context.Context, ExamIntegrityEvidenceListOptions) (*ExamIntegrityEvidencePage, error)
	ListDiscrepancies(context.Context, ExamIntegrityDiscrepancyListOptions) (*ExamIntegrityDiscrepancyPage, error)
	SaveDecision(context.Context, *ExamIntegrityReviewDecisionMutation, *CommandIdempotency) (*ExamIntegrityReviewMutationResult, error)
	UpdateDraft(context.Context, *ExamIntegrityReviewDraftMutation, *CommandIdempotency) (*ExamIntegrityReviewMutationResult, error)
	Finalize(context.Context, *ExamIntegrityReviewFinalize, *CommandIdempotency) (*ExamIntegrityReviewMutationResult, error)
	PrepareRelease(context.Context, model.SubmissionID, model.SubmissionReviewID, int64) (*ExamIntegrityReviewReleasePreparation, error)
	Release(context.Context, *ExamIntegrityReviewRelease, *CommandIdempotency) (*ExamIntegrityReviewMutationResult, error)
	GetReleasedStudentResult(context.Context, model.ExamAttemptID, model.UserID) (*model.StudentResult, error)
}
