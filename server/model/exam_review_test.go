// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"strings"
	"testing"
	"time"
)

func TestSubmissionReviewLifecycleSeparatesDecisionFinalizationAndRelease(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 15, 10, 0, 0, 0, time.UTC)
	review, err := NewSubmissionReview(NewSubmissionReviewID(), NewSubmissionID(), NewUserID(), at)
	if err != nil {
		t.Fatalf("NewSubmissionReview() error = %v", err)
	}
	decision, err := NewIntegrityReviewDecision(NewIntegrityReviewDecisionID(), review.ID, NewIntegrityFlagID(),
		IntegrityReviewConfirmed, NewUserID(), "Observed continuity break was verified.", at)
	if err != nil {
		t.Fatalf("NewIntegrityReviewDecision() error = %v", err)
	}
	if err = decision.Revise(1, IntegrityReviewInconclusive, NewUserID(), "Evidence remains incomplete.", at.Add(time.Minute)); err != nil {
		t.Fatalf("decision.Revise() error = %v", err)
	}
	if decision.Revision != 2 || decision.Outcome != IntegrityReviewInconclusive {
		t.Fatalf("decision = %#v", decision)
	}
	if err = decision.Revise(1, IntegrityReviewDismissed, NewUserID(), "stale", at.Add(2*time.Minute)); err == nil {
		t.Fatal("decision.Revise() accepted stale revision")
	}

	if err = review.UpdateDraft(1, "Private manager notes", "Please discuss the connectivity interruption.", at.Add(2*time.Minute)); err != nil {
		t.Fatalf("review.UpdateDraft() error = %v", err)
	}
	if err = review.Finalize(2, NewUserID(), 1, 3, 0, strings.Repeat("a", 64), at.Add(3*time.Minute)); err != nil {
		t.Fatalf("review.Finalize() error = %v", err)
	}
	if review.State != SubmissionReviewFinalized || review.ReleaseState != SubmissionReviewWithheld || review.Revision != 3 {
		t.Fatalf("finalized review = %#v", review)
	}
	if err = review.UpdateDraft(3, "changed", "changed", at.Add(4*time.Minute)); err == nil {
		t.Fatal("finalized Review accepted draft edit")
	}
	if err = review.Release(3, NewUserID(), at.Add(5*time.Minute)); err != nil {
		t.Fatalf("review.Release() error = %v", err)
	}
	if review.ReleaseState != SubmissionReviewReleased || review.Revision != 4 || !review.ReleasedAt.Valid {
		t.Fatalf("released review = %#v", review)
	}
	if err = review.Release(4, NewUserID(), at.Add(6*time.Minute)); err == nil {
		t.Fatal("released Review accepted a second release")
	}
}

func TestSubmissionReviewRejectsUnboundedOrUntrimmedPrivateText(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 15, 10, 0, 0, 0, time.UTC)
	if _, err := NewIntegrityReviewDecision(NewIntegrityReviewDecisionID(), NewSubmissionReviewID(), NewIntegrityFlagID(),
		IntegrityReviewConfirmed, NewUserID(), " rationale ", at); err == nil {
		t.Fatal("NewIntegrityReviewDecision() accepted untrimmed rationale")
	}
	review, err := NewSubmissionReview(NewSubmissionReviewID(), NewSubmissionID(), NewUserID(), at)
	if err != nil {
		t.Fatal(err)
	}
	if err = review.UpdateDraft(1, strings.Repeat("x", SubmissionReviewPrivateNotesMaximumRunes+1), "", at); err == nil {
		t.Fatal("UpdateDraft() accepted oversized private notes")
	}
	if err = review.UpdateDraft(1, "", " remarks ", at); err == nil {
		t.Fatal("UpdateDraft() accepted untrimmed student remarks")
	}
}

func TestReleasedStudentResultContainsOnlyApprovedProjection(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 15, 10, 0, 0, 0, time.UTC)
	review, err := NewSubmissionReview(NewSubmissionReviewID(), NewSubmissionID(), NewUserID(), at)
	if err != nil {
		t.Fatal(err)
	}
	if err = review.UpdateDraft(1, "private", "Candidate-facing remarks", at.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err = review.Finalize(2, NewUserID(), 0, 0, 0, strings.Repeat("b", 64), at.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err = review.StudentResult(NewExamAttemptID(), NewUserID()); err == nil {
		t.Fatal("StudentResult() exposed an unreleased Review")
	}
	if err = review.Release(3, NewUserID(), at.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	result, err := review.StudentResult(NewExamAttemptID(), NewUserID())
	if err != nil {
		t.Fatalf("StudentResult() error = %v", err)
	}
	if result.ReviewID != review.ID || result.SubmissionID != review.SubmissionID || result.StudentRemarksMarkdown != "Candidate-facing remarks" || result.ReleasedAt != review.ReleasedAt.Time {
		t.Fatalf("StudentResult() = %#v", result)
	}
}

func TestIntegrityReviewInventoryDigestIsOrderIndependentAndPinned(t *testing.T) {
	t.Parallel()

	flagA, flagB := IntegrityFlagID("yyyyyyyyyyyyyyyyyyyyyyyyyy"), IntegrityFlagID("yyyyyyyyyyyyyyyyyyyyyyyyyo")
	decisionA, decisionB := IntegrityReviewDecisionID("yyyyyyyyyyyyyyyyyyyyyyyyye"), IntegrityReviewDecisionID("yyyyyyyyyyyyyyyyyyyyyyyyya")
	evidenceA, evidenceB := IntegrityEvidenceID("yyyyyyyyyyyyyyyyyyyyyyyyyn"), IntegrityEvidenceID("yyyyyyyyyyyyyyyyyyyyyyyyyr")
	flags := []IntegrityReviewInventoryFlag{{FlagID: flagB, DecisionID: decisionB, DecisionRevision: 4},
		{FlagID: flagA, DecisionID: decisionA, DecisionRevision: 2}}
	evidence := []IntegrityReviewInventoryEvidence{{EvidenceID: evidenceB, FlagID: flagB}, {EvidenceID: evidenceA, FlagID: flagA}}
	discrepancyA, discrepancyB := IntegrityDiscrepancyID("yyyyyyyyyyyyyyyyyyyyyyyyyc"), IntegrityDiscrepancyID("yyyyyyyyyyyyyyyyyyyyyyyyyp")
	discrepancies := []IntegrityReviewInventoryDiscrepancy{{DiscrepancyID: discrepancyB}, {DiscrepancyID: discrepancyA}}
	digest, err := IntegrityReviewInventoryDigest(flags, evidence, discrepancies)
	if err != nil {
		t.Fatal(err)
	}
	reversed, err := IntegrityReviewInventoryDigest([]IntegrityReviewInventoryFlag{flags[1], flags[0]},
		[]IntegrityReviewInventoryEvidence{evidence[1], evidence[0]},
		[]IntegrityReviewInventoryDiscrepancy{discrepancies[1], discrepancies[0]})
	if err != nil || reversed != digest {
		t.Fatalf("reversed digest = %q, %v; want %q", reversed, err, digest)
	}
	const want = "49c4e99013eaf6ba074a8fa307e3f310d58c1ecad274a9ed3fc76718d1e111c5"
	if digest != want {
		t.Fatalf("digest = %q, want %q", digest, want)
	}
}

func TestIntegrityDiscrepancyValidatesBoundedLateFocusLossRecord(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 16, 9, 0, 0, 0, time.UTC)
	discrepancy, err := NewIntegrityDiscrepancy(IntegrityDiscrepancySpecification{
		ID: NewIntegrityDiscrepancyID(), SubmissionID: NewSubmissionID(), AttemptID: NewExamAttemptID(),
		ParticipationID: NewAttemptParticipationID(), Generation: 2, Kind: IntegrityDiscrepancyLateFocusLoss,
		SchemaVersion: FocusLossSignalSchemaVersion, SignalID: NewFocusLossSignalID(), Sequence: 14,
		DurationMilliseconds: 2300, Source: FocusLossSourceFullscreenExited, MissingBefore: 2, ReceivedAt: at,
	})
	if err != nil || discrepancy.Validate() != nil || discrepancy.ReceivedAt != at {
		t.Fatalf("NewIntegrityDiscrepancy() = %#v, %v", discrepancy, err)
	}
	invalid := *discrepancy
	invalid.Kind = "future_kind"
	if invalid.Validate() == nil {
		t.Fatal("IntegrityDiscrepancy accepted an unknown kind")
	}
	invalid = *discrepancy
	invalid.DurationMilliseconds = FocusLossMaximumDurationMilliseconds + 1
	if invalid.Validate() == nil {
		t.Fatal("IntegrityDiscrepancy accepted an oversized claim")
	}
}

func TestIntegrityDiscrepancyValidatesTerminalUncertaintyKinds(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 29, 9, 0, 0, 0, time.UTC)
	base := IntegrityDiscrepancySpecification{ID: NewIntegrityDiscrepancyID(), SubmissionID: NewSubmissionID(),
		AttemptID: NewExamAttemptID(), ParticipationID: NewAttemptParticipationID(), Generation: 3,
		SchemaVersion: 1, ReceivedAt: at}
	finalSequence := int64(9)
	tests := []IntegrityDiscrepancySpecification{
		func() IntegrityDiscrepancySpecification {
			value := base
			value.Kind, value.GapReason, value.UnresolvedCount = IntegrityDiscrepancyFocusLossGap,
				string(IntegrityDiscrepancyFocusLossSequenceGapAndSourceNotFinalized), 4
			return value
		}(),
		func() IntegrityDiscrepancySpecification {
			value := base
			value.ID, value.Kind = NewIntegrityDiscrepancyID(), IntegrityDiscrepancyBrowserActivityGap
			value.BrowserSourceSessionID, value.FinalSequence = BrowserSourceSessionID("018f47a0-6e53-4cc4-9d0b-97c9b6d98011"), &finalSequence
			value.GapReason, value.UnresolvedCount = string(BrowserActivityGapDeliveryIncomplete), 2
			return value
		}(),
		func() IntegrityDiscrepancySpecification {
			value := base
			value.ID, value.Kind = NewIntegrityDiscrepancyID(), IntegrityDiscrepancyCorrectionAcknowledgementMissing
			value.CorrectionRevisionID, value.UnresolvedCount = NewExamRevisionID(), 1
			return value
		}(),
	}
	for _, specification := range tests {
		discrepancy, err := NewIntegrityDiscrepancy(specification)
		if err != nil || discrepancy.Validate() != nil {
			t.Fatalf("NewIntegrityDiscrepancy(%q) = %#v, %v", specification.Kind, discrepancy, err)
		}
	}

	invalid := tests[2]
	invalid.UnresolvedCount = 2
	if _, err := NewIntegrityDiscrepancy(invalid); err == nil {
		t.Fatal("missing correction acknowledgement accepted an aggregate count")
	}
}
