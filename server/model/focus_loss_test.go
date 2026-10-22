// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFocusLossSignalIsBoundedGenerationScopedAndUsesServerReceiptTime(t *testing.T) {
	t.Parallel()
	receivedAt := time.Date(2026, time.August, 21, 9, 0, 0, 0, time.UTC)
	signal, err := NewFocusLossSignal(NewFocusLossSignalID(), NewExamAttemptID(), NewAttemptParticipationID(),
		3, 7, 500, FocusLossSourceDocumentHidden, receivedAt)
	require.NoError(t, err)
	require.Equal(t, int64(3), signal.Generation)
	require.Equal(t, int64(7), signal.Sequence)
	require.Equal(t, int64(500), signal.DurationMilliseconds)
	require.Equal(t, FocusLossSourceDocumentHidden, signal.Source)
	require.Equal(t, receivedAt, signal.ReceivedAt)
	policy := DefaultExamPolicySet().FocusLoss
	policy.MinimumDuration = 500 * time.Millisecond
	require.True(t, signal.Qualifies(policy), "duration equal to the minimum did not qualify")
	belowMinimum := *signal
	belowMinimum.DurationMilliseconds--
	require.False(t, belowMinimum.Qualifies(policy), "duration below the minimum qualified")
	disabled := policy
	disabled.Enabled = false
	require.False(t, signal.Qualifies(disabled), "disabled policy qualified a signal")
	duplicate := *signal
	duplicate.ID = NewFocusLossSignalID()
	require.True(t, signal.SameClaim(duplicate), "same sequence/duration/source was not recognized as an exact duplicate")
	duplicate.DurationMilliseconds++
	require.False(t, signal.SameClaim(duplicate), "changed same-sequence semantics were accepted as an exact duplicate")
	for _, source := range []FocusLossSource{"", FocusLossSourceWindowBlur, FocusLossSourceDocumentHidden,
		FocusLossSourceApplicationBackgrounded, FocusLossSourceFullscreenExited} {
		candidate, createErr := NewFocusLossSignal(NewFocusLossSignalID(), NewExamAttemptID(), NewAttemptParticipationID(),
			1, 1, 1, source, receivedAt)
		require.NoError(t, createErr, "optional source %q rejected: %#v", source, candidate)
		require.Equal(t, source, candidate.Source, "optional source %q rejected: %#v", source, candidate)
	}
	maximum, err := NewFocusLossSignal(NewFocusLossSignalID(), NewExamAttemptID(), NewAttemptParticipationID(),
		1, 1, FocusLossMaximumDurationMilliseconds, "", receivedAt)
	require.NoError(t, err, "exact 24-hour duration boundary rejected: %#v", maximum)
	require.Equal(t, FocusLossMaximumDurationMilliseconds, maximum.DurationMilliseconds, "exact 24-hour duration boundary rejected: %#v", maximum)
}

func TestFocusLossSignalRejectsUnboundedOrClientInventedValues(t *testing.T) {
	t.Parallel()
	receivedAt := time.Date(2026, time.August, 21, 9, 0, 0, 0, time.UTC)
	validID, attemptID, participationID := NewFocusLossSignalID(), NewExamAttemptID(), NewAttemptParticipationID()
	for _, test := range []struct {
		name       string
		id         FocusLossSignalID
		generation int64
		sequence   int64
		duration   int64
		source     FocusLossSource
		at         time.Time
	}{
		{name: "identity", generation: 1, sequence: 1, duration: 1, at: receivedAt},
		{name: "generation", id: validID, sequence: 1, duration: 1, at: receivedAt},
		{name: "sequence", id: validID, generation: 1, duration: 1, at: receivedAt},
		{name: "zero duration", id: validID, generation: 1, sequence: 1, at: receivedAt},
		{name: "duration overflow", id: validID, generation: 1, sequence: 1, duration: FocusLossMaximumDurationMilliseconds + 1, at: receivedAt},
		{name: "source", id: validID, generation: 1, sequence: 1, duration: 1, source: "clipboard", at: receivedAt},
		{name: "receipt time", id: validID, generation: 1, sequence: 1, duration: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewFocusLossSignal(test.id, attemptID, participationID, test.generation, test.sequence,
				test.duration, test.source, test.at)
			require.Error(t, err, "invalid Focus Loss signal was accepted: %#v", test)
		})
	}
}

func TestFocusLossOverflowSummaryIsBoundedAndOrdered(t *testing.T) {
	t.Parallel()
	require.Equal(t, 100, FocusLossMaximumEvidenceEpisodes, "Focus Loss evidence cap")
	first := time.Date(2026, time.August, 21, 9, 0, 0, 0, time.UTC)
	summary := FocusLossEvidenceOverflow{AttemptID: NewExamAttemptID(), ParticipationID: NewAttemptParticipationID(),
		Generation: 4, Count: 8, FirstReceivedAt: first, LastReceivedAt: first.Add(time.Minute), MaximumDurationMilliseconds: 12_000}
	require.NoError(t, summary.Validate())
	for _, invalid := range []FocusLossEvidenceOverflow{
		{},
		{AttemptID: summary.AttemptID, ParticipationID: summary.ParticipationID, Generation: 4, Count: 0,
			FirstReceivedAt: first, LastReceivedAt: first, MaximumDurationMilliseconds: 1},
		{AttemptID: summary.AttemptID, ParticipationID: summary.ParticipationID, Generation: 4, Count: 1,
			FirstReceivedAt: first, LastReceivedAt: first.Add(-time.Second), MaximumDurationMilliseconds: 1},
		{AttemptID: summary.AttemptID, ParticipationID: summary.ParticipationID, Generation: 4, Count: 1,
			FirstReceivedAt: first, LastReceivedAt: first, MaximumDurationMilliseconds: FocusLossMaximumDurationMilliseconds + 1},
	} {
		require.Error(t, invalid.Validate(), "invalid overflow summary accepted: %#v", invalid)
	}
}

func TestFocusLossPolicyCanOpenItsOwnNeutralSuspension(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 21, 9, 0, 0, 0, time.UTC)
	flag, err := NewIntegrityFlag(NewIntegrityFlagID(), NewExamAttemptID(), 2, IntegrityPolicyFocusLoss, at)
	require.NoError(t, err)
	suspension, err := NewPolicyAttemptSuspension(NewAttemptSuspensionID(), flag.AttemptID, NewAttemptParticipationID(),
		flag.ID, flag.Generation, AttemptSuspensionCandidateReasonFocusLossPolicy, at)
	require.NoError(t, err)
	require.Equal(t, AttemptSuspensionCandidateReasonFocusLossPolicy, suspension.CandidateReason)
	require.Equal(t, flag.ID, suspension.FlagID)
}

func TestFocusLossSuspensionEndsParticipationAndConnectionWithPolicyReason(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 21, 9, 0, 0, 0, time.UTC)
	attemptID, participationID := NewExamAttemptID(), NewAttemptParticipationID()
	participation, err := NewAttemptParticipation(participationID, attemptID, NewSessionID(), 2,
		HashToken(NewCredentialToken()), at)
	require.NoError(t, err)
	connection, err := NewAttemptConnection(NewAttemptConnectionID(), attemptID, participation.ID, NewSessionID(), at)
	require.NoError(t, err)
	err = participation.End(AttemptParticipationEndPolicySuspended, at.Add(time.Second))
	require.NoError(t, err)
	err = connection.Close(AttemptConnectionClosePolicySuspended, at.Add(time.Second))
	require.NoError(t, err)
	require.Equal(t, AttemptParticipationEndPolicySuspended, participation.EndReason, "policy fence = %#v / %#v", participation, connection)
	require.Equal(t, AttemptConnectionClosePolicySuspended, connection.CloseReason, "policy fence = %#v / %#v", participation, connection)
	require.Equal(t, participation.ID, participationID, "policy fence = %#v / %#v", participation, connection)
}

func TestFocusLossEvaluationOwnsWindowThresholdAndWarningDecision(t *testing.T) {
	t.Parallel()
	receivedAt := time.Date(2026, time.August, 21, 9, 5, 0, 0, time.UTC)
	signal, err := NewFocusLossSignal(NewFocusLossSignalID(), NewExamAttemptID(), NewAttemptParticipationID(),
		2, 8, 500, FocusLossSourceWindowBlur, receivedAt)
	require.NoError(t, err)
	policy := FocusLossPolicy{Enabled: true, MinimumDuration: 500 * time.Millisecond, IncidentCount: 3,
		Window: 10 * time.Second, Outcome: IntegrityOutcomeFlagAndWarn}
	evaluation, err := NewFocusLossEvaluation(policy, *signal)
	require.NoError(t, err)
	require.True(t, evaluation.RetainInWindow(), "Focus Loss evaluation window = retain")
	require.Equal(t, receivedAt.Add(-10*time.Second), evaluation.WindowStartsAt(), "Focus Loss evaluation window = retain")
	decision, err := evaluation.Decide(FocusLossEvaluationState{AcceptedSequence: 6, WindowIncidentCount: 3,
		UnresolvedMissingCount: 4, DiagnosticCount: 2})
	require.NoError(t, err)
	require.True(t, decision.CollectionEnabled)
	require.True(t, decision.Qualified)
	require.Equal(t, int64(1), decision.MissingBefore)
	require.Equal(t, int64(5), decision.UnresolvedMissingCount)
	require.Equal(t, int64(2), decision.DiagnosticCount)
	require.Equal(t, 0, decision.WindowIncidentCount)
	require.True(t, decision.ThresholdCrossed)
	require.True(t, decision.ConsumeWindow)
	require.Equal(t, IntegrityOutcomeFlagAndWarn, decision.PolicyOutcome)
	require.True(t, decision.CreateFlag)
	require.True(t, decision.CreateCandidateWarning)
	require.True(t, decision.NotifyManagers)
	require.False(t, decision.Suspend)
}

func TestFocusLossEvaluationOwnsDisabledFlagAndSuspensionOutcomes(t *testing.T) {
	t.Parallel()
	receivedAt := time.Date(2026, time.August, 21, 9, 5, 0, 0, time.UTC)
	signal, err := NewFocusLossSignal(NewFocusLossSignalID(), NewExamAttemptID(), NewAttemptParticipationID(),
		2, 3, 500, "", receivedAt)
	require.NoError(t, err)
	base := FocusLossPolicy{Enabled: true, MinimumDuration: 500 * time.Millisecond, IncidentCount: 2,
		Window: 10 * time.Second, Outcome: IntegrityOutcomeFlag}
	tests := []struct {
		name   string
		policy FocusLossPolicy
		state  FocusLossEvaluationState
		assert func(*testing.T, FocusLossEvaluation, FocusLossEvaluationDecision)
	}{
		{name: "disabled diagnostic saturates without enforcement", policy: FocusLossPolicy{Enabled: false,
			MinimumDuration: 500 * time.Millisecond, IncidentCount: 2, Window: 10 * time.Second, Outcome: IntegrityOutcomeFlag},
			state: FocusLossEvaluationState{AcceptedSequence: 1, UnresolvedMissingCount: 7, DiagnosticCount: math.MaxInt64},
			assert: func(t *testing.T, evaluation FocusLossEvaluation, decision FocusLossEvaluationDecision) {
				t.Helper()
				require.False(t, evaluation.RetainInWindow())
				require.False(t, decision.CollectionEnabled)
				require.False(t, decision.Qualified)
				require.Equal(t, int64(1), decision.MissingBefore)
				require.Equal(t, int64(7), decision.UnresolvedMissingCount)
				require.Equal(t, int64(math.MaxInt64), decision.DiagnosticCount)
				require.False(t, decision.ThresholdCrossed)
				require.False(t, decision.ConsumeWindow)
				require.Empty(t, decision.PolicyOutcome)
				require.False(t, decision.CreateFlag)
				require.False(t, decision.CreateCandidateWarning)
				require.False(t, decision.NotifyManagers)
				require.False(t, decision.Suspend)
			}},
		{name: "flag reuses open flag", policy: base,
			state: FocusLossEvaluationState{AcceptedSequence: 2, WindowIncidentCount: 2, HasOpenFlag: true},
			assert: func(t *testing.T, _ FocusLossEvaluation, decision FocusLossEvaluationDecision) {
				t.Helper()
				require.True(t, decision.ThresholdCrossed)
				require.True(t, decision.ConsumeWindow)
				require.Equal(t, IntegrityOutcomeFlag, decision.PolicyOutcome)
				require.Equal(t, 0, decision.WindowIncidentCount)
				require.False(t, decision.CreateFlag)
				require.False(t, decision.NotifyManagers)
				require.False(t, decision.CreateCandidateWarning)
				require.False(t, decision.Suspend)
			}},
		{name: "warning is emitted once", policy: func() FocusLossPolicy {
			policy := base
			policy.Outcome = IntegrityOutcomeFlagAndWarn
			return policy
		}(), state: FocusLossEvaluationState{AcceptedSequence: 2, WindowIncidentCount: 2, HasOpenFlag: true,
			CandidateWarningCreated: true},
			assert: func(t *testing.T, _ FocusLossEvaluation, decision FocusLossEvaluationDecision) {
				t.Helper()
				require.True(t, decision.ThresholdCrossed)
				require.False(t, decision.CreateFlag)
				require.False(t, decision.CreateCandidateWarning)
				require.False(t, decision.NotifyManagers)
				require.False(t, decision.Suspend)
			}},
		{name: "suspension is selected by frozen outcome", policy: func() FocusLossPolicy {
			policy := base
			policy.Outcome = IntegrityOutcomeFlagAndSuspend
			return policy
		}(), state: FocusLossEvaluationState{AcceptedSequence: 2, WindowIncidentCount: 2},
			assert: func(t *testing.T, _ FocusLossEvaluation, decision FocusLossEvaluationDecision) {
				t.Helper()
				require.True(t, decision.ThresholdCrossed)
				require.Equal(t, IntegrityOutcomeFlagAndSuspend, decision.PolicyOutcome)
				require.True(t, decision.CreateFlag)
				require.True(t, decision.NotifyManagers)
				require.False(t, decision.CreateCandidateWarning)
				require.True(t, decision.Suspend)
			}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			evaluation, createErr := NewFocusLossEvaluation(test.policy, *signal)
			require.NoError(t, createErr)
			decision, decideErr := evaluation.Decide(test.state)
			require.NoError(t, decideErr)
			test.assert(t, evaluation, decision)
		})
	}
}

func TestFocusLossEvaluationRejectsImpossibleRetainedState(t *testing.T) {
	t.Parallel()
	receivedAt := time.Date(2026, time.August, 21, 9, 5, 0, 0, time.UTC)
	signal, err := NewFocusLossSignal(NewFocusLossSignalID(), NewExamAttemptID(), NewAttemptParticipationID(),
		2, 3, 500, "", receivedAt)
	require.NoError(t, err)
	policy := FocusLossPolicy{Enabled: true, MinimumDuration: 500 * time.Millisecond, IncidentCount: 2,
		Window: 10 * time.Second, Outcome: IntegrityOutcomeFlag}
	evaluation, err := NewFocusLossEvaluation(policy, *signal)
	require.NoError(t, err)
	for _, state := range []FocusLossEvaluationState{
		{AcceptedSequence: 3, WindowIncidentCount: 1},
		{AcceptedSequence: 2, WindowIncidentCount: 0},
		{AcceptedSequence: 2, WindowIncidentCount: 3},
		{AcceptedSequence: 1, WindowIncidentCount: 1, UnresolvedMissingCount: math.MaxInt64},
	} {
		decision, decideErr := evaluation.Decide(state)
		require.Error(t, decideErr, "impossible Focus Loss state accepted: %#v => %#v", state, decision)
	}
}

func TestSuspendExamAttemptForFocusLossAppliesOneCausalDomainTransition(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 21, 9, 0, 0, 0, time.UTC)
	attempt, err := NewExamAttempt(NewExamAttemptID(), NewExamID(), NewExamSittingID(), NewUserID(), NewExamRevisionID(), at)
	require.NoError(t, err)
	participation, err := NewAttemptParticipation(NewAttemptParticipationID(), attempt.ID, NewSessionID(), 3,
		HashToken(NewCredentialToken()), at)
	require.NoError(t, err)
	connection, err := NewAttemptConnection(NewAttemptConnectionID(), attempt.ID, participation.ID, NewSessionID(), at)
	require.NoError(t, err)
	transitionAt := at.Add(time.Second)
	err = SuspendExamAttemptForFocusLoss(attempt, participation, connection, transitionAt)
	require.NoError(t, err)
	require.Equal(t, ExamAttemptSuspended, attempt.State, "Focus Loss suspension transition = %#v / %#v / %#v", attempt, participation, connection)
	require.Equal(t, int64(2), attempt.Revision, "Focus Loss suspension transition = %#v / %#v / %#v", attempt, participation, connection)
	require.Equal(t, transitionAt, attempt.UpdatedAt, "Focus Loss suspension transition = %#v / %#v / %#v", attempt, participation, connection)
	require.Equal(t, AttemptParticipationEnded, participation.State, "Focus Loss suspension transition = %#v / %#v / %#v", attempt, participation, connection)
	require.Equal(t, AttemptParticipationEndPolicySuspended, participation.EndReason, "Focus Loss suspension transition = %#v / %#v / %#v", attempt, participation, connection)
	require.Equal(t, transitionAt, participation.EndedAt.Time, "Focus Loss suspension transition = %#v / %#v / %#v", attempt, participation, connection)
	require.Equal(t, AttemptConnectionClosed, connection.State, "Focus Loss suspension transition = %#v / %#v / %#v", attempt, participation, connection)
	require.Equal(t, AttemptConnectionClosePolicySuspended, connection.CloseReason, "Focus Loss suspension transition = %#v / %#v / %#v", attempt, participation, connection)
	require.Equal(t, transitionAt, connection.ClosedAt.Time, "Focus Loss suspension transition = %#v / %#v / %#v", attempt, participation, connection)
}

func TestSuspendExamAttemptForFocusLossRejectsMismatchedAggregateWithoutPartialMutation(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 21, 9, 0, 0, 0, time.UTC)
	attempt, err := NewExamAttempt(NewExamAttemptID(), NewExamID(), NewExamSittingID(), NewUserID(), NewExamRevisionID(), at)
	require.NoError(t, err)
	participation, err := NewAttemptParticipation(NewAttemptParticipationID(), attempt.ID, NewSessionID(), 3,
		HashToken(NewCredentialToken()), at)
	require.NoError(t, err)
	connection, err := NewAttemptConnection(NewAttemptConnectionID(), NewExamAttemptID(), participation.ID, NewSessionID(), at)
	require.NoError(t, err)
	wantAttempt, wantParticipation, wantConnection := *attempt, *participation, *connection
	err = SuspendExamAttemptForFocusLoss(attempt, participation, connection, at.Add(time.Second))
	require.Error(t, err, "mismatched Focus Loss aggregate was suspended")
	require.Equal(t, wantAttempt, *attempt, "rejected Focus Loss suspension mutated aggregate = %#v / %#v / %#v", attempt, participation, connection)
	require.Equal(t, wantParticipation, *participation, "rejected Focus Loss suspension mutated aggregate = %#v / %#v / %#v", attempt, participation, connection)
	require.Equal(t, wantConnection, *connection, "rejected Focus Loss suspension mutated aggregate = %#v / %#v / %#v", attempt, participation, connection)
}
