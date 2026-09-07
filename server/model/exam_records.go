// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"errors"
	"math"
	"strings"
	"time"
	"unicode/utf8"
)

// ExamSittingRecordsCompletion is the separate records lifecycle of a Closed
// Sitting. It never changes delivery, Submission seals, or finalized Reviews.
// Revision fences the full snapshot; EvidenceRevision identifies newly accepted
// integrity state. A stale completion cannot authorize retention.
type ExamSittingRecordsCompletion struct {
	SittingID                 ExamSittingID
	Revision                  int64
	EvidenceRevision          int64
	CompletedEvidenceRevision int64
	CompletedAt               OptionalTime
	CompletedByUserID         UserID
	StaleAt                   OptionalTime
}

func NewExamSittingRecordsCompletion(id ExamSittingID) *ExamSittingRecordsCompletion {
	return &ExamSittingRecordsCompletion{SittingID: id, Revision: 1}
}

func (c *ExamSittingRecordsCompletion) Validate() error {
	if c == nil || !c.SittingID.IsValid() || c.Revision < 1 || c.EvidenceRevision < 0 ||
		c.CompletedEvidenceRevision < 0 || c.CompletedEvidenceRevision > c.EvidenceRevision ||
		(c.CompletedAt.Valid != c.CompletedByUserID.IsValid()) ||
		(c.CompletedAt.Valid && c.CompletedAt.Time.IsZero()) ||
		(!c.CompletedAt.Valid && (c.CompletedEvidenceRevision != 0 || !c.CompletedByUserID.IsZero() || c.StaleAt.Valid)) ||
		(c.StaleAt.Valid && (c.StaleAt.Time.IsZero() || c.StaleAt.Time.Before(c.CompletedAt.Time))) ||
		(c.CompletedAt.Valid && !c.StaleAt.Valid && c.CompletedEvidenceRevision != c.EvidenceRevision) {
		return errors.New("model: invalid Sitting records completion")
	}
	return nil
}

func (c *ExamSittingRecordsCompletion) IsCurrent() bool {
	return c != nil && c.Validate() == nil && c.CompletedAt.Valid && !c.StaleAt.Valid
}

// Complete requires explicit acknowledgement of the current integrity revision.
// Repeating an already-current completion never restarts its retention clock.
func (c *ExamSittingRecordsCompletion) Complete(expected, acknowledged int64, actor UserID, at time.Time) (bool, error) {
	if c.Validate() != nil || !actor.IsValid() || at.IsZero() || c.Revision != expected || acknowledged != c.EvidenceRevision {
		return false, errors.New("model: Sitting records completion conflict")
	}
	if c.IsCurrent() {
		return false, nil
	}
	if c.Revision == math.MaxInt64 {
		return false, errors.New("model: Sitting records revision exhausted")
	}
	at = TimeUTC(at)
	if c.StaleAt.Valid && at.Before(c.StaleAt.Time) || c.CompletedAt.Valid && at.Before(c.CompletedAt.Time) {
		return false, errors.New("model: Sitting records completion time regressed")
	}
	c.Revision++
	c.CompletedEvidenceRevision = c.EvidenceRevision
	c.CompletedAt, c.CompletedByUserID, c.StaleAt = OptionalTimeFrom(at), actor, OptionalTime{}
	return true, nil
}

// ObserveIntegrity records a fresh accepted change; callers must exclude exact
// replay before invoking it. Previous completion provenance remains visible.
func (c *ExamSittingRecordsCompletion) ObserveIntegrity(at time.Time) error {
	if c.Validate() != nil || at.IsZero() || c.Revision == math.MaxInt64 || c.EvidenceRevision == math.MaxInt64 {
		return errors.New("model: invalid Sitting records integrity change")
	}
	at = TimeUTC(at)
	if c.CompletedAt.Valid && at.Before(c.CompletedAt.Time) {
		return errors.New("model: Sitting records integrity time regressed")
	}
	c.Revision++
	c.EvidenceRevision++
	if c.CompletedAt.Valid && !c.StaleAt.Valid {
		c.StaleAt = OptionalTimeFrom(at)
	}
	return nil
}

// SubmissionReviewWaiver records a deliberate decision that the current
// Submission inventory needs no finalized Review. It is invalidated by later
// discrepancies or Review edits. PrivateReason never belongs in ordinary audit.
type SubmissionReviewWaiver struct {
	SubmissionID     SubmissionID
	Revision         int64
	ReviewRevision   int64
	DiscrepancyCount int64
	ActorUserID      UserID
	RecordedAt       time.Time
	ReasonCode       string
	PrivateReason    string
}

func (w *SubmissionReviewWaiver) Validate() error {
	if w == nil || !w.SubmissionID.IsValid() || w.Revision < 1 || w.ReviewRevision < 0 ||
		w.DiscrepancyCount < 0 || !w.ActorUserID.IsValid() || w.RecordedAt.IsZero() ||
		ValidateRecordsReason(w.ReasonCode, w.PrivateReason) != nil {
		return errors.New("model: invalid Submission Review waiver")
	}
	return nil
}

// RetentionHoldScope is an exact preservation target. ExamID is always supplied
// so nested identifiers cannot be used to cross an Exam boundary.
type RetentionHoldScope struct {
	ExamID       ExamID
	SittingID    ExamSittingID
	SubmissionID SubmissionID
}

func (s RetentionHoldScope) Validate() error {
	if !s.ExamID.IsValid() || !s.SittingID.IsZero() && !s.SittingID.IsValid() ||
		!s.SubmissionID.IsZero() && (!s.SubmissionID.IsValid() || !s.SittingID.IsValid()) {
		return errors.New("model: invalid retention hold scope")
	}
	return nil
}

func (s RetentionHoldScope) Resource() Resource {
	if !s.SubmissionID.IsZero() {
		return Resource{Type: ResourceSubmission, ID: s.SubmissionID.String()}
	}
	if !s.SittingID.IsZero() {
		return Resource{Type: ResourceExamSitting, ID: s.SittingID.String()}
	}
	return Resource{Type: ResourceExam, ID: s.ExamID.String()}
}

// RetentionHold protects its exact scope and future descendants until an
// explicitly authorized release. It has no automatic expiry.
type RetentionHold struct {
	ID                   RetentionHoldID
	Scope                RetentionHoldScope
	Revision             int64
	CreatedAt            time.Time
	CreatedByUserID      UserID
	ReasonCode           string
	PrivateReason        string
	ReleasedAt           OptionalTime
	ReleasedByUserID     UserID
	ReleaseReasonCode    string
	ReleasePrivateReason string
	// These creation-time counts disclose content that a new hold cannot restore.
	WorkRetiredSubmissionCount      int64
	IntegrityRetiredSubmissionCount int64
}

func (h *RetentionHold) Validate() error {
	if h == nil || !h.ID.IsValid() || h.Scope.Validate() != nil || h.Revision < 1 ||
		h.WorkRetiredSubmissionCount < 0 || h.IntegrityRetiredSubmissionCount < 0 ||
		(h.Scope.SubmissionID.IsValid() && (h.WorkRetiredSubmissionCount > 1 || h.IntegrityRetiredSubmissionCount > 1)) ||
		h.CreatedAt.IsZero() || !h.CreatedByUserID.IsValid() || ValidateRecordsReason(h.ReasonCode, h.PrivateReason) != nil ||
		(h.ReleasedAt.Valid != h.ReleasedByUserID.IsValid()) ||
		(h.ReleasedAt.Valid && (h.ReleasedAt.Time.Before(h.CreatedAt) || ValidateRecordsReason(h.ReleaseReasonCode, h.ReleasePrivateReason) != nil)) ||
		(!h.ReleasedAt.Valid && (!h.ReleasedByUserID.IsZero() || h.ReleaseReasonCode != "" || h.ReleasePrivateReason != "")) {
		return errors.New("model: invalid retention hold")
	}
	return nil
}

func (h *RetentionHold) Release(expected int64, actor UserID, code, reason string, at time.Time) error {
	if h.Validate() != nil || expected != h.Revision || h.ReleasedAt.Valid || h.Revision == math.MaxInt64 ||
		!actor.IsValid() || ValidateRecordsReason(code, reason) != nil || at.IsZero() || TimeUTC(at).Before(h.CreatedAt) {
		return errors.New("model: retention hold release conflict")
	}
	h.Revision++
	h.ReleasedAt, h.ReleasedByUserID = OptionalTimeFrom(at), actor
	h.ReleaseReasonCode, h.ReleasePrivateReason = code, reason
	return nil
}

// ValidateRecordsReason uses a closed audit vocabulary and bounds dedicated
// private rationale. The private text is never ordinary audit data.
func ValidateRecordsReason(code, private string) error {
	switch code {
	case "institution_request", "integrity_review", "records_review", "review_not_required", "case_closed", "mistake", "other":
	default:
		return errors.New("model: invalid records reason code")
	}
	if !utf8.ValidString(private) || private != strings.TrimSpace(private) ||
		utf8.RuneCountInString(private) < 1 || utf8.RuneCountInString(private) > 1000 || len(private) > 4000 {
		return errors.New("model: invalid records reason")
	}
	return nil
}
