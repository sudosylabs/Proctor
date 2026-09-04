// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"fmt"
	"math"
	"time"
)

const (
	FocusLossSignalSchemaVersion         = 1
	FocusLossMaximumDurationMilliseconds = int64((24 * time.Hour) / time.Millisecond)
	FocusLossMaximumEvidenceEpisodes     = 100
)

// FocusLossSource is an optional closed classification emitted by a trusted
// client adapter. It contains no free-form native event, application content,
// severity, outcome, or accusation.
type FocusLossSource string

const (
	FocusLossSourceWindowBlur              FocusLossSource = "window_blur"
	FocusLossSourceDocumentHidden          FocusLossSource = "document_hidden"
	FocusLossSourceApplicationBackgrounded FocusLossSource = "application_backgrounded"
	FocusLossSourceFullscreenExited        FocusLossSource = "fullscreen_exited"
)

func (source FocusLossSource) IsValid() bool {
	switch source {
	case "", FocusLossSourceWindowBlur, FocusLossSourceDocumentHidden,
		FocusLossSourceApplicationBackgrounded, FocusLossSourceFullscreenExited:
		return true
	default:
		return false
	}
}

// FocusLossSignal is one accepted, server-identified observation. ReceivedAt
// is authoritative server receipt time; clients never supply it or an outcome.
type FocusLossSignal struct {
	ID                   FocusLossSignalID
	AttemptID            ExamAttemptID
	ParticipationID      AttemptParticipationID
	Generation           int64
	Sequence             int64
	DurationMilliseconds int64
	Source               FocusLossSource
	ReceivedAt           time.Time
}

func NewFocusLossSignal(id FocusLossSignalID, attemptID ExamAttemptID, participationID AttemptParticipationID,
	generation, sequence, durationMilliseconds int64, source FocusLossSource, receivedAt time.Time,
) (*FocusLossSignal, error) {
	signal := &FocusLossSignal{ID: id, AttemptID: attemptID, ParticipationID: participationID,
		Generation: generation, Sequence: sequence, DurationMilliseconds: durationMilliseconds,
		Source: source, ReceivedAt: TimeUTC(receivedAt)}
	if err := signal.Validate(); err != nil {
		return nil, err
	}
	return signal, nil
}

func (signal *FocusLossSignal) Validate() error {
	if signal == nil || !signal.ID.IsValid() || !signal.AttemptID.IsValid() || !signal.ParticipationID.IsValid() ||
		signal.Generation < 1 || signal.Sequence < 1 || signal.DurationMilliseconds < 1 ||
		signal.DurationMilliseconds > FocusLossMaximumDurationMilliseconds || !signal.Source.IsValid() || signal.ReceivedAt.IsZero() {
		return fmt.Errorf("model: invalid Focus Loss signal")
	}
	return nil
}

// Qualifies applies only the enabled duration boundary. FocusLossEvaluation
// owns the remaining policy decision while persistence applies its result
// atomically under the retained sequence and window locks.
func (signal *FocusLossSignal) Qualifies(policy FocusLossPolicy) bool {
	return signal != nil && signal.Validate() == nil && policy.Validate() == nil && policy.Enabled &&
		time.Duration(signal.DurationMilliseconds)*time.Millisecond >= policy.MinimumDuration
}

// SameClaim compares only the client-controlled idempotency semantics. A fresh
// server-proposed identity or receipt time cannot turn changed client input
// into a duplicate.
func (signal *FocusLossSignal) SameClaim(other FocusLossSignal) bool {
	return signal != nil && signal.AttemptID == other.AttemptID && signal.ParticipationID == other.ParticipationID &&
		signal.Generation == other.Generation && signal.Sequence == other.Sequence &&
		signal.DurationMilliseconds == other.DurationMilliseconds && signal.Source == other.Source
}

// FocusLossEvaluationState is the locked retained state supplied to one pure
// policy decision. WindowIncidentCount includes the current signal when
// RetainInWindow reports true; persistence remains responsible for pruning,
// retaining, counting, and locking those rows.
type FocusLossEvaluationState struct {
	AcceptedSequence        int64
	WindowIncidentCount     int
	UnresolvedMissingCount  int64
	DiagnosticCount         int64
	HasOpenFlag             bool
	CandidateWarningCreated bool
}

// FocusLossEvaluationDecision is the complete domain decision for one newly
// accepted sequence. Persistence applies it atomically; callers publish only
// the post-commit effects selected here.
type FocusLossEvaluationDecision struct {
	CollectionEnabled      bool
	Qualified              bool
	MissingBefore          int64
	UnresolvedMissingCount int64
	DiagnosticCount        int64
	WindowIncidentCount    int
	ThresholdCrossed       bool
	ConsumeWindow          bool
	PolicyOutcome          IntegrityThresholdOutcome
	CreateFlag             bool
	CreateCandidateWarning bool
	NotifyManagers         bool
	Suspend                bool
}

// FocusLossEvaluation binds one valid frozen policy to one valid accepted
// signal. It is immutable and contains no persistence or transport mechanics.
type FocusLossEvaluation struct {
	policy    FocusLossPolicy
	signal    FocusLossSignal
	qualified bool
}

func NewFocusLossEvaluation(policy FocusLossPolicy, signal FocusLossSignal) (FocusLossEvaluation, error) {
	if err := policy.Validate(); err != nil {
		return FocusLossEvaluation{}, err
	}
	if err := signal.Validate(); err != nil {
		return FocusLossEvaluation{}, err
	}
	return FocusLossEvaluation{policy: policy, signal: signal, qualified: signal.Qualifies(policy)}, nil
}

// WindowStartsAt returns the inclusive receipt-time boundary for the current
// rolling window. Persistence removes observations strictly before it.
func (evaluation FocusLossEvaluation) WindowStartsAt() time.Time {
	return evaluation.signal.ReceivedAt.Add(-evaluation.policy.Window)
}

// RetainInWindow reports whether the current signal contributes one incident
// to the rolling window.
func (evaluation FocusLossEvaluation) RetainInWindow() bool {
	return evaluation.policy.Enabled && evaluation.qualified
}

func (evaluation FocusLossEvaluation) Decide(state FocusLossEvaluationState) (FocusLossEvaluationDecision, error) {
	if evaluation.policy.Validate() != nil || evaluation.signal.Validate() != nil || state.AcceptedSequence < 0 ||
		state.AcceptedSequence >= evaluation.signal.Sequence || state.WindowIncidentCount < 0 ||
		state.UnresolvedMissingCount < 0 || state.DiagnosticCount < 0 {
		return FocusLossEvaluationDecision{}, fmt.Errorf("model: invalid Focus Loss evaluation state")
	}
	missingBefore := evaluation.signal.Sequence - state.AcceptedSequence - 1
	decision := FocusLossEvaluationDecision{CollectionEnabled: evaluation.policy.Enabled,
		Qualified: evaluation.qualified, MissingBefore: missingBefore,
		UnresolvedMissingCount: state.UnresolvedMissingCount, DiagnosticCount: state.DiagnosticCount,
		WindowIncidentCount: state.WindowIncidentCount}
	if !evaluation.policy.Enabled {
		if state.WindowIncidentCount != 0 {
			return FocusLossEvaluationDecision{}, fmt.Errorf("model: disabled Focus Loss evaluation has a pending window")
		}
		decision.Qualified = false
		if decision.DiagnosticCount < math.MaxInt64 {
			decision.DiagnosticCount++
		}
		return decision, nil
	}
	if missingBefore > math.MaxInt64-state.UnresolvedMissingCount {
		return FocusLossEvaluationDecision{}, fmt.Errorf("model: Focus Loss gap count overflows")
	}
	decision.UnresolvedMissingCount += missingBefore
	if evaluation.qualified {
		if state.WindowIncidentCount < 1 || state.WindowIncidentCount > evaluation.policy.IncidentCount {
			return FocusLossEvaluationDecision{}, fmt.Errorf("model: qualifying Focus Loss signal has invalid window count")
		}
	} else if state.WindowIncidentCount >= evaluation.policy.IncidentCount {
		return FocusLossEvaluationDecision{}, fmt.Errorf("model: Focus Loss window crossed without the current signal")
	}
	decision.ThresholdCrossed = evaluation.qualified && state.WindowIncidentCount >= evaluation.policy.IncidentCount
	if !decision.ThresholdCrossed {
		return decision, nil
	}
	decision.WindowIncidentCount = 0
	decision.ConsumeWindow = true
	decision.PolicyOutcome = evaluation.policy.Outcome
	decision.CreateFlag = !state.HasOpenFlag
	decision.NotifyManagers = decision.CreateFlag
	decision.CreateCandidateWarning = evaluation.policy.Outcome == IntegrityOutcomeFlagAndWarn && !state.CandidateWarningCreated
	decision.Suspend = evaluation.policy.Outcome == IntegrityOutcomeFlagAndSuspend
	return decision, nil
}

// SuspendExamAttemptForFocusLoss applies the coordinated local lifecycle
// transition selected by a Focus Loss policy decision. It mutates the three
// domain values only after every ownership check and transition succeeds;
// persistence remains responsible for committing them with the causal Flag,
// Suspension, evidence, and audit record atomically.
func SuspendExamAttemptForFocusLoss(attempt *ExamAttempt, participation *AttemptParticipation,
	connection *AttemptConnection, at time.Time,
) error {
	if attempt == nil || participation == nil || connection == nil || attempt.Validate() != nil ||
		participation.Validate() != nil || connection.Validate() != nil || participation.AttemptID != attempt.ID ||
		connection.AttemptID != attempt.ID || connection.ParticipationID != participation.ID {
		return fmt.Errorf("model: invalid Focus Loss suspension aggregate")
	}
	attemptCandidate, participationCandidate, connectionCandidate := *attempt, *participation, *connection
	if err := attemptCandidate.Suspend(at); err != nil {
		return err
	}
	if err := participationCandidate.End(AttemptParticipationEndPolicySuspended, at); err != nil {
		return err
	}
	if err := connectionCandidate.Close(AttemptConnectionClosePolicySuspended, at); err != nil {
		return err
	}
	*attempt, *participation, *connection = attemptCandidate, participationCandidate, connectionCandidate
	return nil
}

// Validate checks a standalone Focus Loss policy without weakening the full
// Exam Policy Set validation used for publication.
func (policy FocusLossPolicy) Validate() error {
	set := DefaultExamPolicySet()
	set.FocusLoss = policy
	return set.Validate()
}

// FocusLossEvidenceOverflow is the fixed-size summary used after the retained
// per-kind/per-generation episode limit. Count is aggregate metadata, not a
// collection of client payloads.
type FocusLossEvidenceOverflow struct {
	AttemptID                   ExamAttemptID
	ParticipationID             AttemptParticipationID
	Generation                  int64
	Count                       int64
	FirstReceivedAt             time.Time
	LastReceivedAt              time.Time
	MaximumDurationMilliseconds int64
}

func (summary FocusLossEvidenceOverflow) Validate() error {
	if !summary.AttemptID.IsValid() || !summary.ParticipationID.IsValid() || summary.Generation < 1 ||
		summary.Count < 1 || summary.FirstReceivedAt.IsZero() || summary.LastReceivedAt.Before(summary.FirstReceivedAt) ||
		summary.MaximumDurationMilliseconds < 1 || summary.MaximumDurationMilliseconds > FocusLossMaximumDurationMilliseconds {
		return fmt.Errorf("model: invalid Focus Loss evidence overflow")
	}
	return nil
}
