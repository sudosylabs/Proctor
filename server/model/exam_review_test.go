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

	"github.com/stretchr/testify/require"
)

func TestSubmissionReviewLifecycleSeparatesDecisionFinalizationAndRelease(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 15, 10, 0, 0, 0, time.UTC)
	review, err := NewSubmissionReview(NewSubmissionReviewID(), NewSubmissionID(), NewUserID(), at)
	require.NoError(t, err)
	decision, err := NewIntegrityReviewDecision(NewIntegrityReviewDecisionID(), review.ID, NewIntegrityFlagID(),
		IntegrityReviewConfirmed, NewUserID(), "Observed continuity break was verified.", at)
	require.NoError(t, err)
	err = decision.Revise(1, IntegrityReviewInconclusive, NewUserID(), "Evidence remains incomplete.", at.Add(time.Minute))
	require.NoError(t, err)
	require.Equal(t, int64(2), decision.Revision)
	require.Equal(t, IntegrityReviewInconclusive, decision.Outcome)
	err = decision.Revise(1, IntegrityReviewDismissed, NewUserID(), "stale", at.Add(2*time.Minute))
	require.Error(t, err, "decision.Revise() accepted stale revision")

	err = review.UpdateDraft(1, "Private manager notes", "Please discuss the connectivity interruption.", at.Add(2*time.Minute))
	require.NoError(t, err)
	err = review.Finalize(2, NewUserID(), 1, 3, 0, strings.Repeat("a", 64), at.Add(3*time.Minute))
	require.NoError(t, err)
	require.Equal(t, SubmissionReviewFinalized, review.State)
	require.Equal(t, SubmissionReviewWithheld, review.ReleaseState)
	require.Equal(t, int64(3), review.Revision)
	err = review.UpdateDraft(3, "changed", "changed", at.Add(4*time.Minute))
	require.Error(t, err, "finalized Review accepted draft edit")
	err = review.Release(3, NewUserID(), at.Add(5*time.Minute))
	require.NoError(t, err)
	require.Equal(t, SubmissionReviewReleased, review.ReleaseState)
	require.Equal(t, int64(4), review.Revision)
	require.True(t, review.ReleasedAt.Valid)
	err = review.Release(4, NewUserID(), at.Add(6*time.Minute))
	require.Error(t, err, "released Review accepted a second release")
}

func TestSubmissionReviewRejectsUnboundedOrUntrimmedPrivateText(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 15, 10, 0, 0, 0, time.UTC)
	_, err := NewIntegrityReviewDecision(NewIntegrityReviewDecisionID(), NewSubmissionReviewID(), NewIntegrityFlagID(),
		IntegrityReviewConfirmed, NewUserID(), " rationale ", at)
	require.Error(t, err, "NewIntegrityReviewDecision() accepted untrimmed rationale")
	review, err := NewSubmissionReview(NewSubmissionReviewID(), NewSubmissionID(), NewUserID(), at)
	require.NoError(t, err)
	err = review.UpdateDraft(1, strings.Repeat("x", SubmissionReviewPrivateNotesMaximumRunes+1), "", at)
	require.Error(t, err, "UpdateDraft() accepted oversized private notes")
	err = review.UpdateDraft(1, "", " remarks ", at)
	require.Error(t, err, "UpdateDraft() accepted untrimmed student remarks")
}

func TestReleasedStudentResultContainsOnlyApprovedProjection(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 15, 10, 0, 0, 0, time.UTC)
	review, err := NewSubmissionReview(NewSubmissionReviewID(), NewSubmissionID(), NewUserID(), at)
	require.NoError(t, err)
	err = review.UpdateDraft(1, "private", "Candidate-facing remarks", at.Add(time.Minute))
	require.NoError(t, err)
	err = review.Finalize(2, NewUserID(), 0, 0, 0, strings.Repeat("b", 64), at.Add(2*time.Minute))
	require.NoError(t, err)
	_, err = review.StudentResult(NewExamAttemptID(), NewUserID())
	require.Error(t, err, "StudentResult() exposed an unreleased Review")
	err = review.Release(3, NewUserID(), at.Add(3*time.Minute))
	require.NoError(t, err)
	result, err := review.StudentResult(NewExamAttemptID(), NewUserID())
	require.NoError(t, err)
	require.Equal(t, review.ID, result.ReviewID)
	require.Equal(t, review.SubmissionID, result.SubmissionID)
	require.Equal(t, "Candidate-facing remarks", result.StudentRemarksMarkdown)
	require.Equal(t, review.ReleasedAt.Time, result.ReleasedAt)
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
	require.NoError(t, err)
	reversed, err := IntegrityReviewInventoryDigest([]IntegrityReviewInventoryFlag{flags[1], flags[0]},
		[]IntegrityReviewInventoryEvidence{evidence[1], evidence[0]},
		[]IntegrityReviewInventoryDiscrepancy{discrepancies[1], discrepancies[0]})
	require.NoError(t, err, "reversed digest")
	require.Equal(t, digest, reversed, "reversed digest")
	const want = "589fd26678d772b26eba2f83258241fecc46ebd753d795e39430ea4eaf3dc4ba"
	require.Equal(t, want, digest, "digest")
}

func TestIntegrityDiscrepancyValidatesBoundedLateFocusLossRecord(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 16, 9, 0, 0, 0, time.UTC)
	discrepancy, err := NewIntegrityDiscrepancy(IntegrityDiscrepancySpecification{
		ID: NewIntegrityDiscrepancyID(), SubmissionID: NewSubmissionID(), AttemptID: NewExamAttemptID(),
		ParticipationID: NewAttemptParticipationID(), Generation: 2, Kind: IntegrityDiscrepancyLateFocusLoss, SignalID: NewFocusLossSignalID(), Sequence: 14,
		DurationMilliseconds: 2300, Source: FocusLossSourceFullscreenExited, MissingBefore: 2, ReceivedAt: at,
	})
	require.NoError(t, err, "NewIntegrityDiscrepancy() = %#v", discrepancy)
	require.NoError(t, discrepancy.Validate(), "NewIntegrityDiscrepancy() = %#v", discrepancy)
	require.Equal(t, at, discrepancy.ReceivedAt, "NewIntegrityDiscrepancy() = %#v", discrepancy)
	invalid := *discrepancy
	invalid.Kind = "future_kind"
	require.Error(t, invalid.Validate(), "IntegrityDiscrepancy accepted an unknown kind")
	invalid = *discrepancy
	invalid.DurationMilliseconds = FocusLossMaximumDurationMilliseconds + 1
	require.Error(t, invalid.Validate(), "IntegrityDiscrepancy accepted an oversized claim")
}

func TestIntegrityDiscrepancyValidatesTerminalUncertaintyKinds(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 29, 9, 0, 0, 0, time.UTC)
	base := IntegrityDiscrepancySpecification{ID: NewIntegrityDiscrepancyID(), SubmissionID: NewSubmissionID(),
		AttemptID: NewExamAttemptID(), ParticipationID: NewAttemptParticipationID(), Generation: 3, ReceivedAt: at}
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
		require.NoError(t, err, "NewIntegrityDiscrepancy(%q) = %#v", specification.Kind, discrepancy)
		require.NoError(t, discrepancy.Validate(), "NewIntegrityDiscrepancy(%q) = %#v", specification.Kind, discrepancy)
	}

	invalid := tests[2]
	invalid.UnresolvedCount = 2
	_, err := NewIntegrityDiscrepancy(invalid)
	require.Error(t, err, "missing correction acknowledgement accepted an aggregate count")
}
