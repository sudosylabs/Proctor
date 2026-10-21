// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
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

func (s FocusLossSource) IsValid() bool {
	switch s {
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

func (s *FocusLossSignal) Validate() error {
	if s == nil || !s.ID.IsValid() || !s.AttemptID.IsValid() || !s.ParticipationID.IsValid() ||
		s.Generation < 1 || s.Sequence < 1 || s.DurationMilliseconds < 1 ||
		s.DurationMilliseconds > FocusLossMaximumDurationMilliseconds || !s.Source.IsValid() || s.ReceivedAt.IsZero() {
		return invalidModelError("FocusLossSignal.Validate", "focus_loss_signal", "", "invalid Focus Loss signal", "")
	}
	return nil
}

// Qualifies applies only the enabled duration boundary. FocusLossEvaluation
// owns the remaining policy decision while persistence applies its result
// atomically under the retained sequence and window locks.
func (s *FocusLossSignal) Qualifies(policy FocusLossPolicy) bool {
	return s != nil && s.Validate() == nil && policy.Validate() == nil && policy.Enabled &&
		time.Duration(s.DurationMilliseconds)*time.Millisecond >= policy.MinimumDuration
}

// SameClaim compares only the client-controlled idempotency semantics. A fresh
// server-proposed identity or receipt time cannot turn changed client input
// into a duplicate.
func (s *FocusLossSignal) SameClaim(other FocusLossSignal) bool {
	return s != nil && s.AttemptID == other.AttemptID && s.ParticipationID == other.ParticipationID &&
		s.Generation == other.Generation && s.Sequence == other.Sequence &&
		s.DurationMilliseconds == other.DurationMilliseconds && s.Source == other.Source
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
func (e FocusLossEvaluation) WindowStartsAt() time.Time {
	return e.signal.ReceivedAt.Add(-e.policy.Window)
}

// RetainInWindow reports whether the current signal contributes one incident
// to the rolling window.
func (e FocusLossEvaluation) RetainInWindow() bool {
	return e.policy.Enabled && e.qualified
}

func (e FocusLossEvaluation) Decide(state FocusLossEvaluationState) (FocusLossEvaluationDecision, error) {
	if e.policy.Validate() != nil || e.signal.Validate() != nil || state.AcceptedSequence < 0 ||
		state.AcceptedSequence >= e.signal.Sequence || state.WindowIncidentCount < 0 ||
		state.UnresolvedMissingCount < 0 || state.DiagnosticCount < 0 {
		return FocusLossEvaluationDecision{}, invalidModelError("FocusLossEvaluation.Decide", "focus_loss_evaluation", "", "invalid Focus Loss evaluation state", "")
	}
	missingBefore := e.signal.Sequence - state.AcceptedSequence - 1
	decision := FocusLossEvaluationDecision{CollectionEnabled: e.policy.Enabled,
		Qualified: e.qualified, MissingBefore: missingBefore,
		UnresolvedMissingCount: state.UnresolvedMissingCount, DiagnosticCount: state.DiagnosticCount,
		WindowIncidentCount: state.WindowIncidentCount}
	if !e.policy.Enabled {
		if state.WindowIncidentCount != 0 {
			return FocusLossEvaluationDecision{}, invalidModelError("FocusLossEvaluation.Decide", "focus_loss_evaluation", "", "disabled Focus Loss evaluation has a pending window", "")
		}
		decision.Qualified = false
		if decision.DiagnosticCount < math.MaxInt64 {
			decision.DiagnosticCount++
		}
		return decision, nil
	}
	if missingBefore > math.MaxInt64-state.UnresolvedMissingCount {
		return FocusLossEvaluationDecision{}, invalidModelError("FocusLossEvaluation.Decide", "focus_loss_evaluation", "", "Focus Loss gap count overflows", "")
	}
	decision.UnresolvedMissingCount += missingBefore
	if e.qualified {
		if state.WindowIncidentCount < 1 || state.WindowIncidentCount > e.policy.IncidentCount {
			return FocusLossEvaluationDecision{}, invalidModelError("FocusLossEvaluation.Decide", "focus_loss_evaluation", "", "qualifying Focus Loss signal has invalid window count", "")
		}
	} else if state.WindowIncidentCount >= e.policy.IncidentCount {
		return FocusLossEvaluationDecision{}, invalidModelError("FocusLossEvaluation.Decide", "focus_loss_evaluation", "", "Focus Loss window crossed without the current signal", "")
	}
	decision.ThresholdCrossed = e.qualified && state.WindowIncidentCount >= e.policy.IncidentCount
	if !decision.ThresholdCrossed {
		return decision, nil
	}
	decision.WindowIncidentCount = 0
	decision.ConsumeWindow = true
	decision.PolicyOutcome = e.policy.Outcome
	decision.CreateFlag = !state.HasOpenFlag
	decision.NotifyManagers = decision.CreateFlag
	decision.CreateCandidateWarning = e.policy.Outcome == IntegrityOutcomeFlagAndWarn && !state.CandidateWarningCreated
	decision.Suspend = e.policy.Outcome == IntegrityOutcomeFlagAndSuspend
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
		return invalidModelError("SuspendExamAttemptForFocusLoss", "focus_loss", "", "invalid Focus Loss suspension aggregate", "")
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
func (p FocusLossPolicy) Validate() error {
	set := DefaultExamPolicySet()
	set.FocusLoss = p
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

func (s FocusLossEvidenceOverflow) Validate() error {
	if !s.AttemptID.IsValid() || !s.ParticipationID.IsValid() || s.Generation < 1 ||
		s.Count < 1 || s.FirstReceivedAt.IsZero() || s.LastReceivedAt.Before(s.FirstReceivedAt) ||
		s.MaximumDurationMilliseconds < 1 || s.MaximumDurationMilliseconds > FocusLossMaximumDurationMilliseconds {
		return invalidModelError("FocusLossEvidenceOverflow.Validate", "focus_loss_evidence_overflow", "", "invalid Focus Loss evidence overflow", "")
	}
	return nil
}
