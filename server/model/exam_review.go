// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	IntegrityReviewPrivateRationaleMaximumRunes = 1000
	IntegrityReviewPrivateRationaleMaximumBytes = 4000
	// The combined private Review projection is retained as one bounded
	// idempotency outcome. Keep both fields comfortably below that Store
	// envelope, including JSON framing and safe aggregate metadata.
	SubmissionReviewPrivateNotesMaximumRunes   = 3000
	SubmissionReviewPrivateNotesMaximumBytes   = 12000
	SubmissionReviewStudentRemarksMaximumRunes = 8192
	SubmissionReviewStudentRemarksMaximumBytes = 32768
	SubmissionReviewMaximumFlags               = 200
	SubmissionReviewMaximumEvidence            = 20000
	SubmissionReviewMaximumDiscrepancies       = 200
)

// IntegrityDiscrepancyKind identifies a bounded uncertainty record associated
// with a sealed Submission. A discrepancy is never reinterpreted as evidence
// and never mutates the sealed Submission.
type IntegrityDiscrepancyKind string

const (
	IntegrityDiscrepancyLateFocusLoss                    IntegrityDiscrepancyKind = "late_focus_loss"
	IntegrityDiscrepancyFocusLossGap                     IntegrityDiscrepancyKind = "focus_loss_gap"
	IntegrityDiscrepancyBrowserActivityGap               IntegrityDiscrepancyKind = "browser_activity_gap"
	IntegrityDiscrepancyCorrectionAcknowledgementMissing IntegrityDiscrepancyKind = "correction_acknowledgement_missing"
)

type IntegrityDiscrepancyFocusLossGapReason string

const (
	IntegrityDiscrepancyFocusLossSequenceGap                      IntegrityDiscrepancyFocusLossGapReason = "sequence_gap"
	IntegrityDiscrepancyFocusLossSourceNotFinalized               IntegrityDiscrepancyFocusLossGapReason = "source_not_finalized"
	IntegrityDiscrepancyFocusLossSequenceGapAndSourceNotFinalized IntegrityDiscrepancyFocusLossGapReason = "sequence_gap_and_source_not_finalized"
)

func (reason IntegrityDiscrepancyFocusLossGapReason) IsValid() bool {
	return reason == IntegrityDiscrepancyFocusLossSequenceGap ||
		reason == IntegrityDiscrepancyFocusLossSourceNotFinalized ||
		reason == IntegrityDiscrepancyFocusLossSequenceGapAndSourceNotFinalized
}

type IntegrityDiscrepancyBrowserActivityGapReason string

const IntegrityDiscrepancyBrowserActivityPriorSourceGap IntegrityDiscrepancyBrowserActivityGapReason = "prior_source_gap"

func (reason IntegrityDiscrepancyBrowserActivityGapReason) IsValid() bool {
	return reason == IntegrityDiscrepancyBrowserActivityPriorSourceGap ||
		BrowserActivitySubmissionGapReason(reason).IsValid()
}

// IntegrityDiscrepancySpecification contains the complete immutable value for
// one late record. ReceivedAt is authoritative database receipt time.
type IntegrityDiscrepancySpecification struct {
	ID                     IntegrityDiscrepancyID
	SubmissionID           SubmissionID
	AttemptID              ExamAttemptID
	ParticipationID        AttemptParticipationID
	Generation             int64
	Kind                   IntegrityDiscrepancyKind
	SchemaVersion          int
	SignalID               FocusLossSignalID
	Sequence               int64
	DurationMilliseconds   int64
	Source                 FocusLossSource
	MissingBefore          int64
	CorrectionRevisionID   ExamRevisionID
	BrowserSourceSessionID BrowserSourceSessionID
	FinalSequence          *int64
	GapReason              string
	UnresolvedCount        int64
	ReceivedAt             time.Time
}

// IntegrityDiscrepancy is an immutable, bounded post-collection diagnostic.
// It is manager-visible only through the Submission Review capability.
type IntegrityDiscrepancy struct {
	IntegrityDiscrepancySpecification
}

func NewIntegrityDiscrepancy(specification IntegrityDiscrepancySpecification) (*IntegrityDiscrepancy, error) {
	value := &IntegrityDiscrepancy{IntegrityDiscrepancySpecification: specification}
	if value.FinalSequence != nil {
		sequence := *value.FinalSequence
		value.FinalSequence = &sequence
	}
	value.ReceivedAt = TimeUTC(value.ReceivedAt)
	if err := value.Validate(); err != nil {
		return nil, err
	}
	return value, nil
}

func (value *IntegrityDiscrepancy) Validate() error {
	if value == nil || !value.ID.IsValid() || !value.SubmissionID.IsValid() || !value.AttemptID.IsValid() ||
		!value.ParticipationID.IsValid() || value.Generation < 1 || value.SchemaVersion != 1 || value.ReceivedAt.IsZero() {
		return errors.New("model: invalid Integrity Discrepancy")
	}
	switch value.Kind {
	case IntegrityDiscrepancyLateFocusLoss:
		if !value.SignalID.IsValid() || value.Sequence < 1 || value.DurationMilliseconds < 1 ||
			value.DurationMilliseconds > FocusLossMaximumDurationMilliseconds || !value.Source.IsValid() ||
			value.MissingBefore < 0 || value.CorrectionRevisionID != "" || value.BrowserSourceSessionID != "" ||
			value.FinalSequence != nil || value.GapReason != "" || value.UnresolvedCount != 0 {
			return errors.New("model: invalid late Focus Loss Integrity Discrepancy")
		}
	case IntegrityDiscrepancyFocusLossGap:
		if value.SignalID != "" || value.Sequence != 0 || value.DurationMilliseconds != 0 || value.Source != "" ||
			value.MissingBefore != 0 || value.CorrectionRevisionID != "" || value.BrowserSourceSessionID != "" ||
			value.FinalSequence != nil || !IntegrityDiscrepancyFocusLossGapReason(value.GapReason).IsValid() ||
			value.UnresolvedCount < 1 {
			return errors.New("model: invalid Focus Loss gap Integrity Discrepancy")
		}
	case IntegrityDiscrepancyBrowserActivityGap:
		if value.SignalID != "" || value.Sequence != 0 || value.DurationMilliseconds != 0 || value.Source != "" ||
			value.MissingBefore != 0 || value.CorrectionRevisionID != "" || !value.BrowserSourceSessionID.IsValid() ||
			value.FinalSequence != nil && *value.FinalSequence < 1 ||
			!IntegrityDiscrepancyBrowserActivityGapReason(value.GapReason).IsValid() || value.UnresolvedCount < 1 {
			return errors.New("model: invalid Browser Activity gap Integrity Discrepancy")
		}
	case IntegrityDiscrepancyCorrectionAcknowledgementMissing:
		if value.SignalID != "" || value.Sequence != 0 || value.DurationMilliseconds != 0 || value.Source != "" ||
			value.MissingBefore != 0 || !value.CorrectionRevisionID.IsValid() || value.BrowserSourceSessionID != "" ||
			value.FinalSequence != nil || value.GapReason != "" || value.UnresolvedCount != 1 {
			return errors.New("model: invalid correction acknowledgement Integrity Discrepancy")
		}
	default:
		return errors.New("model: invalid Integrity Discrepancy kind")
	}
	return nil
}

// IntegrityReviewOutcome is a manager's non-academic disposition of one
// Integrity Flag. It is deliberately not a grade, score, or guilt verdict.
type IntegrityReviewOutcome string

const (
	IntegrityReviewConfirmed    IntegrityReviewOutcome = "confirmed"
	IntegrityReviewDismissed    IntegrityReviewOutcome = "dismissed"
	IntegrityReviewInconclusive IntegrityReviewOutcome = "inconclusive"
)

func (outcome IntegrityReviewOutcome) IsValid() bool {
	return outcome == IntegrityReviewConfirmed || outcome == IntegrityReviewDismissed || outcome == IntegrityReviewInconclusive
}

// IntegrityReviewDecision is the sole current revision-fenced disposition of
// one Flag within a draft Submission Review. PrivateRationale is manager-only.
type IntegrityReviewDecision struct {
	ID               IntegrityReviewDecisionID
	ReviewID         SubmissionReviewID
	FlagID           IntegrityFlagID
	Outcome          IntegrityReviewOutcome
	Revision         int64
	ActorUserID      UserID
	PrivateRationale string
	DecidedAt        time.Time
}

func NewIntegrityReviewDecision(id IntegrityReviewDecisionID, reviewID SubmissionReviewID, flagID IntegrityFlagID,
	outcome IntegrityReviewOutcome, actorID UserID, rationale string, at time.Time,
) (*IntegrityReviewDecision, error) {
	decision := &IntegrityReviewDecision{ID: id, ReviewID: reviewID, FlagID: flagID, Outcome: outcome, Revision: 1,
		ActorUserID: actorID, PrivateRationale: rationale, DecidedAt: TimeUTC(at)}
	if err := decision.Validate(); err != nil {
		return nil, err
	}
	return decision, nil
}

func (decision *IntegrityReviewDecision) Validate() error {
	if decision == nil || !decision.ID.IsValid() || !decision.ReviewID.IsValid() || !decision.FlagID.IsValid() ||
		!decision.Outcome.IsValid() || decision.Revision < 1 || !decision.ActorUserID.IsValid() || decision.DecidedAt.IsZero() ||
		!validBoundedReviewText(decision.PrivateRationale, 1, IntegrityReviewPrivateRationaleMaximumRunes,
			IntegrityReviewPrivateRationaleMaximumBytes) {
		return errors.New("model: invalid Integrity Review decision")
	}
	return nil
}

func (decision *IntegrityReviewDecision) Revise(expectedRevision int64, outcome IntegrityReviewOutcome,
	actorID UserID, rationale string, at time.Time,
) error {
	if decision == nil || decision.Validate() != nil || decision.Revision != expectedRevision {
		return errors.New("model: Integrity Review decision revision conflict")
	}
	candidate := *decision
	candidate.Outcome = outcome
	candidate.Revision++
	candidate.ActorUserID = actorID
	candidate.PrivateRationale = rationale
	candidate.DecidedAt = TimeUTC(at)
	if err := candidate.Validate(); err != nil {
		return err
	}
	*decision = candidate
	return nil
}

type SubmissionReviewState string

const (
	SubmissionReviewDraft     SubmissionReviewState = "draft"
	SubmissionReviewFinalized SubmissionReviewState = "finalized"
)

type SubmissionReviewReleaseState string

const (
	SubmissionReviewWithheld SubmissionReviewReleaseState = "withheld"
	SubmissionReviewReleased SubmissionReviewReleaseState = "released"
)

// SubmissionReview is the one revision-fenced integrity Review associated
// with a sealed Submission. Finalization freezes its text, decisions, and
// inventory digest. Release is a later one-way transition.
type SubmissionReview struct {
	ID                      SubmissionReviewID
	SubmissionID            SubmissionID
	State                   SubmissionReviewState
	ReleaseState            SubmissionReviewReleaseState
	Revision                int64
	CreatedByUserID         UserID
	ManagerNotes            string
	StudentRemarksMarkdown  string
	FlagCount               int
	EvidenceCount           int
	DiscrepancyCount        int
	EvidenceInventoryDigest string
	CreatedAt               time.Time
	UpdatedAt               time.Time
	FinalizedAt             OptionalTime
	FinalizedByUserID       UserID
	ReleasedAt              OptionalTime
	ReleasedByUserID        UserID
}

func NewSubmissionReview(id SubmissionReviewID, submissionID SubmissionID, actorID UserID, at time.Time) (*SubmissionReview, error) {
	at = TimeUTC(at)
	review := &SubmissionReview{ID: id, SubmissionID: submissionID, State: SubmissionReviewDraft,
		ReleaseState: SubmissionReviewWithheld, Revision: 1, CreatedByUserID: actorID, CreatedAt: at, UpdatedAt: at}
	if err := review.Validate(); err != nil {
		return nil, err
	}
	return review, nil
}

func (review *SubmissionReview) Validate() error {
	if review == nil || !review.ID.IsValid() || !review.SubmissionID.IsValid() || review.Revision < 1 ||
		!review.CreatedByUserID.IsValid() || review.CreatedAt.IsZero() || review.UpdatedAt.IsZero() || review.UpdatedAt.Before(review.CreatedAt) ||
		!validBoundedReviewText(review.ManagerNotes, 0, SubmissionReviewPrivateNotesMaximumRunes, SubmissionReviewPrivateNotesMaximumBytes) ||
		!validBoundedReviewText(review.StudentRemarksMarkdown, 0, SubmissionReviewStudentRemarksMaximumRunes, SubmissionReviewStudentRemarksMaximumBytes) {
		return errors.New("model: invalid Submission Review")
	}
	switch review.State {
	case SubmissionReviewDraft:
		if review.ReleaseState != SubmissionReviewWithheld || review.FlagCount != 0 || review.EvidenceCount != 0 ||
			review.DiscrepancyCount != 0 || review.EvidenceInventoryDigest != "" || review.FinalizedAt.Valid ||
			!review.FinalizedByUserID.IsZero() || review.ReleasedAt.Valid || !review.ReleasedByUserID.IsZero() {
			return errors.New("model: invalid draft Submission Review")
		}
	case SubmissionReviewFinalized:
		if review.FlagCount < 0 || review.FlagCount > SubmissionReviewMaximumFlags || review.EvidenceCount < 0 ||
			review.EvidenceCount > SubmissionReviewMaximumEvidence || review.DiscrepancyCount < 0 ||
			review.DiscrepancyCount > SubmissionReviewMaximumDiscrepancies || !validLowerSHA256(review.EvidenceInventoryDigest) ||
			!review.FinalizedAt.Valid || review.FinalizedAt.Time.Before(review.CreatedAt) || !review.FinalizedByUserID.IsValid() {
			return errors.New("model: invalid finalized Submission Review")
		}
		switch review.ReleaseState {
		case SubmissionReviewWithheld:
			if review.ReleasedAt.Valid || !review.ReleasedByUserID.IsZero() {
				return errors.New("model: withheld Submission Review has release metadata")
			}
		case SubmissionReviewReleased:
			if !review.ReleasedAt.Valid || review.ReleasedAt.Time.Before(review.FinalizedAt.Time) || !review.ReleasedByUserID.IsValid() {
				return errors.New("model: invalid released Submission Review")
			}
		default:
			return errors.New("model: invalid Submission Review release state")
		}
	default:
		return errors.New("model: invalid Submission Review state")
	}
	return nil
}

func (review *SubmissionReview) UpdateDraft(expectedRevision int64, managerNotes, studentRemarks string, at time.Time) error {
	if review == nil || review.Validate() != nil || review.State != SubmissionReviewDraft || review.Revision != expectedRevision {
		return errors.New("model: Submission Review cannot be edited")
	}
	candidate := *review
	candidate.ManagerNotes = managerNotes
	candidate.StudentRemarksMarkdown = studentRemarks
	candidate.Revision++
	candidate.UpdatedAt = TimeUTC(at)
	if err := candidate.Validate(); err != nil {
		return err
	}
	*review = candidate
	return nil
}

// TouchDraft advances the aggregate revision when a child Flag decision is
// created or revised without changing Review text.
func (review *SubmissionReview) TouchDraft(expectedRevision int64, at time.Time) error {
	if review == nil || review.Validate() != nil || review.State != SubmissionReviewDraft || review.Revision != expectedRevision {
		return errors.New("model: Submission Review cannot advance")
	}
	candidate := *review
	candidate.Revision++
	candidate.UpdatedAt = TimeUTC(at)
	if err := candidate.Validate(); err != nil {
		return err
	}
	*review = candidate
	return nil
}

func (review *SubmissionReview) Finalize(expectedRevision int64, actorID UserID, flagCount, evidenceCount,
	discrepancyCount int, inventoryDigest string, at time.Time,
) error {
	if review == nil || review.Validate() != nil || review.State != SubmissionReviewDraft || review.Revision != expectedRevision {
		return errors.New("model: Submission Review cannot be finalized")
	}
	candidate := *review
	candidate.State = SubmissionReviewFinalized
	candidate.Revision++
	candidate.FlagCount = flagCount
	candidate.EvidenceCount = evidenceCount
	candidate.DiscrepancyCount = discrepancyCount
	candidate.EvidenceInventoryDigest = inventoryDigest
	candidate.FinalizedAt = OptionalTimeFrom(at)
	candidate.FinalizedByUserID = actorID
	candidate.UpdatedAt = TimeUTC(at)
	if err := candidate.Validate(); err != nil {
		return err
	}
	*review = candidate
	return nil
}

func (review *SubmissionReview) Release(expectedRevision int64, actorID UserID, at time.Time) error {
	if review == nil || review.Validate() != nil || review.State != SubmissionReviewFinalized ||
		review.ReleaseState != SubmissionReviewWithheld || review.Revision != expectedRevision {
		return errors.New("model: Submission Review cannot be released")
	}
	candidate := *review
	candidate.ReleaseState = SubmissionReviewReleased
	candidate.Revision++
	candidate.ReleasedAt = OptionalTimeFrom(at)
	candidate.ReleasedByUserID = actorID
	candidate.UpdatedAt = TimeUTC(at)
	if err := candidate.Validate(); err != nil {
		return err
	}
	*review = candidate
	return nil
}

// StudentResult is the entire candidate-visible release projection. It omits
// evidence, decisions, private rationale, manager notes, and sealed content.
type StudentResult struct {
	ReviewID               SubmissionReviewID
	SubmissionID           SubmissionID
	AttemptID              ExamAttemptID
	CandidateUserID        UserID
	StudentRemarksMarkdown string
	ReleasedAt             time.Time
}

func (review *SubmissionReview) StudentResult(attemptID ExamAttemptID, candidateID UserID) (*StudentResult, error) {
	if review == nil || review.Validate() != nil || review.State != SubmissionReviewFinalized ||
		review.ReleaseState != SubmissionReviewReleased || !attemptID.IsValid() || !candidateID.IsValid() {
		return nil, errors.New("model: Student Result is unavailable")
	}
	return &StudentResult{ReviewID: review.ID, SubmissionID: review.SubmissionID, AttemptID: attemptID,
		CandidateUserID: candidateID, StudentRemarksMarkdown: review.StudentRemarksMarkdown, ReleasedAt: review.ReleasedAt.Time}, nil
}

func validBoundedReviewText(value string, minimumRunes, maximumRunes, maximumBytes int) bool {
	if !utf8.ValidString(value) || value != strings.TrimSpace(value) || len(value) > maximumBytes {
		return false
	}
	runes := utf8.RuneCountInString(value)
	return runes >= minimumRunes && runes <= maximumRunes
}

type IntegrityReviewInventoryFlag struct {
	FlagID           IntegrityFlagID
	DecisionID       IntegrityReviewDecisionID
	DecisionRevision int64
}

type IntegrityReviewInventoryEvidence struct {
	EvidenceID IntegrityEvidenceID
	FlagID     IntegrityFlagID
}

type IntegrityReviewInventoryDiscrepancy struct {
	DiscrepancyID IntegrityDiscrepancyID
}

// IntegrityReviewInventoryDigest returns the canonical version-1 digest used
// to freeze the exact Flag, decision revision, and Evidence identities seen by
// finalization. Input order is irrelevant and caller slices are not mutated.
func IntegrityReviewInventoryDigest(flags []IntegrityReviewInventoryFlag,
	evidence []IntegrityReviewInventoryEvidence, discrepancies []IntegrityReviewInventoryDiscrepancy,
) (string, error) {
	if len(flags) > SubmissionReviewMaximumFlags || len(evidence) > SubmissionReviewMaximumEvidence ||
		len(discrepancies) > SubmissionReviewMaximumDiscrepancies {
		return "", errors.New("model: Integrity Review inventory exceeds bounds")
	}
	flagCopy := append([]IntegrityReviewInventoryFlag(nil), flags...)
	evidenceCopy := append([]IntegrityReviewInventoryEvidence(nil), evidence...)
	discrepancyCopy := append([]IntegrityReviewInventoryDiscrepancy(nil), discrepancies...)
	for _, item := range flagCopy {
		if !item.FlagID.IsValid() || !item.DecisionID.IsValid() || item.DecisionRevision < 1 {
			return "", errors.New("model: invalid Integrity Review Flag inventory")
		}
	}
	for _, item := range evidenceCopy {
		if !item.EvidenceID.IsValid() || !item.FlagID.IsValid() {
			return "", errors.New("model: invalid Integrity Review Evidence inventory")
		}
	}
	for _, item := range discrepancyCopy {
		if !item.DiscrepancyID.IsValid() {
			return "", errors.New("model: invalid Integrity Review Discrepancy inventory")
		}
	}
	sort.Slice(flagCopy, func(i, j int) bool { return flagCopy[i].FlagID.String() < flagCopy[j].FlagID.String() })
	sort.Slice(evidenceCopy, func(i, j int) bool { return evidenceCopy[i].EvidenceID.String() < evidenceCopy[j].EvidenceID.String() })
	sort.Slice(discrepancyCopy, func(i, j int) bool {
		return discrepancyCopy[i].DiscrepancyID.String() < discrepancyCopy[j].DiscrepancyID.String()
	})
	hash := sha256.New()
	hash.Write([]byte("proctor.integrity-review.inventory.v1\x00"))
	writeReviewInventoryCount(hash, len(flagCopy))
	for _, item := range flagCopy {
		hash.Write([]byte(item.FlagID.String()))
		hash.Write([]byte(item.DecisionID.String()))
		var revision [8]byte
		binary.BigEndian.PutUint64(revision[:], uint64(item.DecisionRevision))
		hash.Write(revision[:])
	}
	writeReviewInventoryCount(hash, len(evidenceCopy))
	for _, item := range evidenceCopy {
		hash.Write([]byte(item.EvidenceID.String()))
		hash.Write([]byte(item.FlagID.String()))
	}
	writeReviewInventoryCount(hash, len(discrepancyCopy))
	for _, item := range discrepancyCopy {
		hash.Write([]byte(item.DiscrepancyID.String()))
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

type reviewInventoryWriter interface{ Write([]byte) (int, error) }

func writeReviewInventoryCount(writer reviewInventoryWriter, count int) {
	var encoded [4]byte
	binary.BigEndian.PutUint32(encoded[:], uint32(count))
	_, _ = writer.Write(encoded[:])
}
