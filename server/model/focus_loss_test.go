// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"math"
	"strings"
	"testing"
	"time"
)

func TestFocusLossSignalIsBoundedGenerationScopedAndUsesServerReceiptTime(t *testing.T) {
	t.Parallel()
	receivedAt := time.Date(2026, time.August, 21, 9, 0, 0, 0, time.UTC)
	signal, err := NewFocusLossSignal(NewFocusLossSignalID(), NewExamAttemptID(), NewAttemptParticipationID(),
		3, 7, 500, FocusLossSourceDocumentHidden, receivedAt)
	if err != nil {
		t.Fatal(err)
	}
	if signal.Generation != 3 || signal.Sequence != 7 || signal.DurationMilliseconds != 500 ||
		signal.Source != FocusLossSourceDocumentHidden || signal.ReceivedAt != receivedAt {
		t.Fatalf("Focus Loss signal = %#v", signal)
	}
	policy := DefaultExamPolicySet().FocusLoss
	policy.MinimumDuration = 500 * time.Millisecond
	if !signal.Qualifies(policy) {
		t.Fatal("duration equal to the minimum did not qualify")
	}
	belowMinimum := *signal
	belowMinimum.DurationMilliseconds--
	if belowMinimum.Qualifies(policy) {
		t.Fatal("duration below the minimum qualified")
	}
	disabled := policy
	disabled.Enabled = false
	if signal.Qualifies(disabled) {
		t.Fatal("disabled policy qualified a signal")
	}
	duplicate := *signal
	duplicate.ID = NewFocusLossSignalID()
	if !signal.SameClaim(duplicate) {
		t.Fatal("same sequence/duration/source was not recognized as an exact duplicate")
	}
	duplicate.DurationMilliseconds++
	if signal.SameClaim(duplicate) {
		t.Fatal("changed same-sequence semantics were accepted as an exact duplicate")
	}
	for _, source := range []FocusLossSource{"", FocusLossSourceWindowBlur, FocusLossSourceDocumentHidden,
		FocusLossSourceApplicationBackgrounded, FocusLossSourceFullscreenExited} {
		candidate, createErr := NewFocusLossSignal(NewFocusLossSignalID(), NewExamAttemptID(), NewAttemptParticipationID(),
			1, 1, 1, source, receivedAt)
		if createErr != nil || candidate.Source != source {
			t.Fatalf("optional source %q rejected: %#v / %v", source, candidate, createErr)
		}
	}
	maximum, err := NewFocusLossSignal(NewFocusLossSignalID(), NewExamAttemptID(), NewAttemptParticipationID(),
		1, 1, FocusLossMaximumDurationMilliseconds, "", receivedAt)
	if err != nil || maximum.DurationMilliseconds != FocusLossMaximumDurationMilliseconds {
		t.Fatalf("exact 24-hour duration boundary rejected: %#v / %v", maximum, err)
	}
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
			if _, err := NewFocusLossSignal(test.id, attemptID, participationID, test.generation, test.sequence,
				test.duration, test.source, test.at); err == nil {
				t.Fatalf("invalid Focus Loss signal was accepted: %#v", test)
			}
		})
	}
}

func TestFocusLossEvidenceRetainsOnlyBoundedEpisodeAndGapMetadata(t *testing.T) {
	t.Parallel()
	receivedAt := time.Date(2026, time.August, 21, 9, 0, 0, 0, time.UTC)
	signal, err := NewFocusLossSignal(NewFocusLossSignalID(), NewExamAttemptID(), NewAttemptParticipationID(),
		2, 9, 750, FocusLossSourceWindowBlur, receivedAt)
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := NewFocusLossEvidence(NewIntegrityEvidenceID(), *signal, NewIntegrityFlagID(), 3, receivedAt)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.Kind != IntegrityPolicyFocusLoss || evidence.SignalID != signal.ID || evidence.Sequence != signal.Sequence ||
		evidence.DurationMilliseconds != 750 || evidence.Source != signal.Source || evidence.MissingBefore != 3 ||
		evidence.ObservedAt != receivedAt || evidence.RecordedAt != receivedAt {
		t.Fatalf("Focus Loss evidence = %#v", evidence)
	}
	capture := strings.ToLower(strings.Join([]string{string(evidence.Kind), string(evidence.Source)}, " "))
	for _, forbidden := range []string{"clipboard", "source code", "screenshot", "terminal", "payload"} {
		if strings.Contains(capture, forbidden) {
			t.Fatalf("Focus Loss evidence retained forbidden material %q: %#v", forbidden, evidence)
		}
	}
}

func TestFocusLossOverflowSummaryIsBoundedAndOrdered(t *testing.T) {
	t.Parallel()
	if FocusLossMaximumEvidenceEpisodes != 100 {
		t.Fatalf("Focus Loss evidence cap = %d, want 100", FocusLossMaximumEvidenceEpisodes)
	}
	first := time.Date(2026, time.August, 21, 9, 0, 0, 0, time.UTC)
	summary := FocusLossEvidenceOverflow{AttemptID: NewExamAttemptID(), ParticipationID: NewAttemptParticipationID(),
		Generation: 4, Count: 8, FirstReceivedAt: first, LastReceivedAt: first.Add(time.Minute), MaximumDurationMilliseconds: 12_000}
	if err := summary.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []FocusLossEvidenceOverflow{
		{},
		{AttemptID: summary.AttemptID, ParticipationID: summary.ParticipationID, Generation: 4, Count: 0,
			FirstReceivedAt: first, LastReceivedAt: first, MaximumDurationMilliseconds: 1},
		{AttemptID: summary.AttemptID, ParticipationID: summary.ParticipationID, Generation: 4, Count: 1,
			FirstReceivedAt: first, LastReceivedAt: first.Add(-time.Second), MaximumDurationMilliseconds: 1},
		{AttemptID: summary.AttemptID, ParticipationID: summary.ParticipationID, Generation: 4, Count: 1,
			FirstReceivedAt: first, LastReceivedAt: first, MaximumDurationMilliseconds: FocusLossMaximumDurationMilliseconds + 1},
	} {
		if err := invalid.Validate(); err == nil {
			t.Fatalf("invalid overflow summary accepted: %#v", invalid)
		}
	}
}

func TestFocusLossPolicyCanOpenItsOwnNeutralSuspension(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 21, 9, 0, 0, 0, time.UTC)
	flag, err := NewIntegrityFlag(NewIntegrityFlagID(), NewExamAttemptID(), 2, IntegrityPolicyFocusLoss, at)
	if err != nil {
		t.Fatal(err)
	}
	suspension, err := NewPolicyAttemptSuspension(NewAttemptSuspensionID(), flag.AttemptID, NewAttemptParticipationID(),
		flag.ID, flag.Generation, AttemptSuspensionCandidateReasonFocusLossPolicy, at)
	if err != nil {
		t.Fatal(err)
	}
	if suspension.CandidateReason != AttemptSuspensionCandidateReasonFocusLossPolicy || suspension.FlagID != flag.ID {
		t.Fatalf("Focus Loss suspension = %#v", suspension)
	}
}

func TestFocusLossSuspensionEndsParticipationAndConnectionWithPolicyReason(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 21, 9, 0, 0, 0, time.UTC)
	attemptID, participationID := NewExamAttemptID(), NewAttemptParticipationID()
	participation, err := NewAttemptParticipation(participationID, attemptID, NewSessionID(), 2,
		HashToken(NewCredentialToken()), at)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := NewAttemptConnection(NewAttemptConnectionID(), attemptID, participation.ID, NewSessionID(), at)
	if err != nil {
		t.Fatal(err)
	}
	if err = participation.End(AttemptParticipationEndPolicySuspended, at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err = connection.Close(AttemptConnectionClosePolicySuspended, at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if participation.EndReason != AttemptParticipationEndPolicySuspended || connection.CloseReason != AttemptConnectionClosePolicySuspended ||
		participationID != participation.ID {
		t.Fatalf("policy fence = %#v / %#v", participation, connection)
	}
}

func TestFocusLossEvaluationOwnsWindowThresholdAndWarningDecision(t *testing.T) {
	t.Parallel()
	receivedAt := time.Date(2026, time.August, 21, 9, 5, 0, 0, time.UTC)
	signal, err := NewFocusLossSignal(NewFocusLossSignalID(), NewExamAttemptID(), NewAttemptParticipationID(),
		2, 8, 500, FocusLossSourceWindowBlur, receivedAt)
	if err != nil {
		t.Fatal(err)
	}
	policy := FocusLossPolicy{Enabled: true, MinimumDuration: 500 * time.Millisecond, IncidentCount: 3,
		Window: 10 * time.Second, Outcome: IntegrityOutcomeFlagAndWarn}
	evaluation, err := NewFocusLossEvaluation(policy, *signal)
	if err != nil {
		t.Fatal(err)
	}
	if !evaluation.RetainInWindow() || evaluation.WindowStartsAt() != receivedAt.Add(-10*time.Second) {
		t.Fatalf("Focus Loss evaluation window = retain %t, start %v", evaluation.RetainInWindow(), evaluation.WindowStartsAt())
	}
	decision, err := evaluation.Decide(FocusLossEvaluationState{AcceptedSequence: 6, WindowIncidentCount: 3,
		UnresolvedMissingCount: 4, DiagnosticCount: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !decision.CollectionEnabled || !decision.Qualified || decision.MissingBefore != 1 ||
		decision.UnresolvedMissingCount != 5 || decision.DiagnosticCount != 2 || decision.WindowIncidentCount != 0 ||
		!decision.ThresholdCrossed || !decision.ConsumeWindow || decision.PolicyOutcome != IntegrityOutcomeFlagAndWarn ||
		!decision.CreateFlag || !decision.CreateCandidateWarning || !decision.NotifyManagers || decision.Suspend {
		t.Fatalf("Focus Loss warning decision = %#v", decision)
	}
}

func TestFocusLossEvaluationOwnsDisabledFlagAndSuspensionOutcomes(t *testing.T) {
	t.Parallel()
	receivedAt := time.Date(2026, time.August, 21, 9, 5, 0, 0, time.UTC)
	signal, err := NewFocusLossSignal(NewFocusLossSignalID(), NewExamAttemptID(), NewAttemptParticipationID(),
		2, 3, 500, "", receivedAt)
	if err != nil {
		t.Fatal(err)
	}
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
				if evaluation.RetainInWindow() || decision.CollectionEnabled || decision.Qualified || decision.MissingBefore != 1 ||
					decision.UnresolvedMissingCount != 7 || decision.DiagnosticCount != math.MaxInt64 || decision.ThresholdCrossed ||
					decision.ConsumeWindow || decision.PolicyOutcome != "" || decision.CreateFlag ||
					decision.CreateCandidateWarning || decision.NotifyManagers || decision.Suspend {
					t.Fatalf("disabled Focus Loss decision = %#v", decision)
				}
			}},
		{name: "flag reuses open flag", policy: base,
			state: FocusLossEvaluationState{AcceptedSequence: 2, WindowIncidentCount: 2, HasOpenFlag: true},
			assert: func(t *testing.T, _ FocusLossEvaluation, decision FocusLossEvaluationDecision) {
				t.Helper()
				if !decision.ThresholdCrossed || !decision.ConsumeWindow || decision.PolicyOutcome != IntegrityOutcomeFlag ||
					decision.WindowIncidentCount != 0 || decision.CreateFlag || decision.NotifyManagers ||
					decision.CreateCandidateWarning || decision.Suspend {
					t.Fatalf("repeated Focus Loss Flag decision = %#v", decision)
				}
			}},
		{name: "warning is emitted once", policy: func() FocusLossPolicy {
			policy := base
			policy.Outcome = IntegrityOutcomeFlagAndWarn
			return policy
		}(), state: FocusLossEvaluationState{AcceptedSequence: 2, WindowIncidentCount: 2, HasOpenFlag: true,
			CandidateWarningCreated: true},
			assert: func(t *testing.T, _ FocusLossEvaluation, decision FocusLossEvaluationDecision) {
				t.Helper()
				if !decision.ThresholdCrossed || decision.CreateFlag || decision.CreateCandidateWarning || decision.NotifyManagers || decision.Suspend {
					t.Fatalf("repeated Focus Loss warning decision = %#v", decision)
				}
			}},
		{name: "suspension is selected by frozen outcome", policy: func() FocusLossPolicy {
			policy := base
			policy.Outcome = IntegrityOutcomeFlagAndSuspend
			return policy
		}(), state: FocusLossEvaluationState{AcceptedSequence: 2, WindowIncidentCount: 2},
			assert: func(t *testing.T, _ FocusLossEvaluation, decision FocusLossEvaluationDecision) {
				t.Helper()
				if !decision.ThresholdCrossed || decision.PolicyOutcome != IntegrityOutcomeFlagAndSuspend ||
					!decision.CreateFlag || !decision.NotifyManagers || decision.CreateCandidateWarning || !decision.Suspend {
					t.Fatalf("Focus Loss suspension decision = %#v", decision)
				}
			}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			evaluation, createErr := NewFocusLossEvaluation(test.policy, *signal)
			if createErr != nil {
				t.Fatal(createErr)
			}
			decision, decideErr := evaluation.Decide(test.state)
			if decideErr != nil {
				t.Fatal(decideErr)
			}
			test.assert(t, evaluation, decision)
		})
	}
}

func TestFocusLossEvaluationRejectsImpossibleRetainedState(t *testing.T) {
	t.Parallel()
	receivedAt := time.Date(2026, time.August, 21, 9, 5, 0, 0, time.UTC)
	signal, err := NewFocusLossSignal(NewFocusLossSignalID(), NewExamAttemptID(), NewAttemptParticipationID(),
		2, 3, 500, "", receivedAt)
	if err != nil {
		t.Fatal(err)
	}
	policy := FocusLossPolicy{Enabled: true, MinimumDuration: 500 * time.Millisecond, IncidentCount: 2,
		Window: 10 * time.Second, Outcome: IntegrityOutcomeFlag}
	evaluation, err := NewFocusLossEvaluation(policy, *signal)
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []FocusLossEvaluationState{
		{AcceptedSequence: 3, WindowIncidentCount: 1},
		{AcceptedSequence: 2, WindowIncidentCount: 0},
		{AcceptedSequence: 2, WindowIncidentCount: 3},
		{AcceptedSequence: 1, WindowIncidentCount: 1, UnresolvedMissingCount: math.MaxInt64},
	} {
		if decision, decideErr := evaluation.Decide(state); decideErr == nil {
			t.Fatalf("impossible Focus Loss state accepted: %#v => %#v", state, decision)
		}
	}
}

func TestSuspendExamAttemptForFocusLossAppliesOneCausalDomainTransition(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 21, 9, 0, 0, 0, time.UTC)
	attempt, err := NewExamAttempt(NewExamAttemptID(), NewExamID(), NewExamSittingID(), NewUserID(), NewExamRevisionID(), at)
	if err != nil {
		t.Fatal(err)
	}
	participation, err := NewAttemptParticipation(NewAttemptParticipationID(), attempt.ID, NewSessionID(), 3,
		HashToken(NewCredentialToken()), at)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := NewAttemptConnection(NewAttemptConnectionID(), attempt.ID, participation.ID, NewSessionID(), at)
	if err != nil {
		t.Fatal(err)
	}
	transitionAt := at.Add(time.Second)
	if err = SuspendExamAttemptForFocusLoss(attempt, participation, connection, transitionAt); err != nil {
		t.Fatal(err)
	}
	if attempt.State != ExamAttemptSuspended || attempt.Revision != 2 || attempt.UpdatedAt != transitionAt ||
		participation.State != AttemptParticipationEnded || participation.EndReason != AttemptParticipationEndPolicySuspended ||
		participation.EndedAt.Time != transitionAt || connection.State != AttemptConnectionClosed ||
		connection.CloseReason != AttemptConnectionClosePolicySuspended || connection.ClosedAt.Time != transitionAt {
		t.Fatalf("Focus Loss suspension transition = %#v / %#v / %#v", attempt, participation, connection)
	}
}

func TestSuspendExamAttemptForFocusLossRejectsMismatchedAggregateWithoutPartialMutation(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 21, 9, 0, 0, 0, time.UTC)
	attempt, err := NewExamAttempt(NewExamAttemptID(), NewExamID(), NewExamSittingID(), NewUserID(), NewExamRevisionID(), at)
	if err != nil {
		t.Fatal(err)
	}
	participation, err := NewAttemptParticipation(NewAttemptParticipationID(), attempt.ID, NewSessionID(), 3,
		HashToken(NewCredentialToken()), at)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := NewAttemptConnection(NewAttemptConnectionID(), NewExamAttemptID(), participation.ID, NewSessionID(), at)
	if err != nil {
		t.Fatal(err)
	}
	wantAttempt, wantParticipation, wantConnection := *attempt, *participation, *connection
	if err = SuspendExamAttemptForFocusLoss(attempt, participation, connection, at.Add(time.Second)); err == nil {
		t.Fatal("mismatched Focus Loss aggregate was suspended")
	}
	if *attempt != wantAttempt || *participation != wantParticipation || *connection != wantConnection {
		t.Fatalf("rejected Focus Loss suspension mutated aggregate = %#v / %#v / %#v", attempt, participation, connection)
	}
}
