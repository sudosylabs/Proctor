// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"strings"
	"time"
	"unicode/utf8"
)

const (
	AttemptParticipationRenewalInterval = 5 * time.Second
	AttemptParticipationInitialLease    = 20 * time.Second
)

type ExamAttemptState string

const (
	ExamAttemptActive    ExamAttemptState = "active"
	ExamAttemptSuspended ExamAttemptState = "suspended"
	ExamAttemptReady     ExamAttemptState = "ready"
	ExamAttemptSubmitted ExamAttemptState = "submitted"
)

func (s ExamAttemptState) AllowsCandidateConnection() bool { return s == ExamAttemptActive }
func (s ExamAttemptState) IsTerminal() bool                { return s == ExamAttemptSubmitted }
func (s ExamAttemptState) IsUnresolved() bool {
	return s == ExamAttemptReady || s == ExamAttemptActive || s == ExamAttemptSuspended
}

// ExamAttempt is one candidate's stable work identity for one Sitting. Its
// admission Revision remains pinned for the complete Attempt lifetime.
type ExamAttempt struct {
	ID                  ExamAttemptID
	ExamID              ExamID
	SittingID           ExamSittingID
	CandidateUserID     UserID
	AdmissionRevisionID ExamRevisionID
	State               ExamAttemptState
	CreatedAt           time.Time
	UpdatedAt           time.Time
	SubmittedAt         OptionalTime
	Revision            int64
}

func NewExamAttempt(id ExamAttemptID, examID ExamID, sittingID ExamSittingID, candidateID UserID, admissionRevisionID ExamRevisionID, at time.Time) (*ExamAttempt, error) {
	at = TimeUTC(at)
	attempt := &ExamAttempt{ID: id, ExamID: examID, SittingID: sittingID, CandidateUserID: candidateID,
		AdmissionRevisionID: admissionRevisionID, State: ExamAttemptActive, CreatedAt: at, UpdatedAt: at, Revision: 1}
	if err := attempt.Validate(); err != nil {
		return nil, err
	}
	return attempt, nil
}

func (a *ExamAttempt) Validate() error {
	if a == nil || !a.ID.IsValid() || !a.ExamID.IsValid() || !a.SittingID.IsValid() ||
		!a.CandidateUserID.IsValid() || !a.AdmissionRevisionID.IsValid() || a.CreatedAt.IsZero() ||
		a.UpdatedAt.IsZero() || a.UpdatedAt.Before(a.CreatedAt) || a.Revision < 1 {
		return invalidModelError("ExamAttempt.Validate", "exam_attempt", "", "invalid Exam Attempt", "")
	}
	switch a.State {
	case ExamAttemptReady, ExamAttemptActive, ExamAttemptSuspended:
		if a.SubmittedAt.Valid {
			return invalidModelError("ExamAttempt.Validate", "exam_attempt", "", "non-submitted Exam Attempt has submitted_at", "")
		}
	case ExamAttemptSubmitted:
		if !a.SubmittedAt.Valid || a.SubmittedAt.Time.IsZero() || a.SubmittedAt.Time.Before(a.CreatedAt) || a.SubmittedAt.Time.After(a.UpdatedAt) {
			return invalidModelError("ExamAttempt.Validate", "exam_attempt", "", "invalid submitted Exam Attempt lifecycle", "")
		}
	default:
		return invalidModelError("ExamAttempt.Validate", "exam_attempt", "", "invalid Exam Attempt state", "")
	}
	return nil
}

// Suspend enters one blocking enforcement episode. Persistence is responsible
// for committing the causal evidence, flag, Suspension, audit, and this state
// transition atomically.
func (a *ExamAttempt) Suspend(at time.Time) error {
	if a == nil || a.State != ExamAttemptActive {
		return invalidModelError("ExamAttempt.Suspend", "exam_attempt", "", "Exam Attempt cannot suspend", "")
	}
	candidate := *a
	candidate.State = ExamAttemptSuspended
	candidate.UpdatedAt = TimeUTC(at)
	candidate.Revision++
	if err := candidate.Validate(); err != nil {
		return err
	}
	*a = candidate
	return nil
}

// Reallow closes the blocking effect of the current Suspension and moves the
// Attempt to ready. A later secure admission creates a new Participation and
// is the only transition that can make the Attempt active again.
func (a *ExamAttempt) Reallow(at time.Time) error {
	if a == nil || a.State != ExamAttemptSuspended {
		return invalidModelError("ExamAttempt.Reallow", "exam_attempt", "", "Exam Attempt cannot be re-allowed", "")
	}
	candidate := *a
	candidate.State = ExamAttemptReady
	candidate.UpdatedAt = TimeUTC(at)
	candidate.Revision++
	if err := candidate.Validate(); err != nil {
		return err
	}
	*a = candidate
	return nil
}

// Activate admits a ready Attempt after all candidate, Sitting, Session,
// registered-key, compatibility, and posture checks have succeeded.
func (a *ExamAttempt) Activate(at time.Time) error {
	if a == nil || a.State != ExamAttemptReady {
		return invalidModelError("ExamAttempt.Activate", "exam_attempt", "", "Exam Attempt cannot activate", "")
	}
	candidate := *a
	candidate.State = ExamAttemptActive
	candidate.UpdatedAt = TimeUTC(at)
	candidate.Revision++
	if err := candidate.Validate(); err != nil {
		return err
	}
	*a = candidate
	return nil
}

type AttemptParticipationState string

const (
	AttemptParticipationActive AttemptParticipationState = "active"
	AttemptParticipationEnded  AttemptParticipationState = "ended"
)

type AttemptParticipationEndReason string

const (
	AttemptParticipationEndInterrupted     AttemptParticipationEndReason = "interrupted"
	AttemptParticipationEndLeaseExpired    AttemptParticipationEndReason = "lease_expired"
	AttemptParticipationEndPolicySuspended AttemptParticipationEndReason = "policy_suspended"
	AttemptParticipationEndKicked          AttemptParticipationEndReason = "kicked"
	AttemptParticipationEndSubmitted       AttemptParticipationEndReason = "submitted"
	AttemptParticipationEndManagerEnded    AttemptParticipationEndReason = "manager_ended"
	AttemptParticipationEndSittingClosed   AttemptParticipationEndReason = "sitting_closed"
)

func (r AttemptParticipationEndReason) IsValid() bool {
	switch r {
	case AttemptParticipationEndInterrupted, AttemptParticipationEndLeaseExpired, AttemptParticipationEndPolicySuspended, AttemptParticipationEndKicked,
		AttemptParticipationEndSubmitted, AttemptParticipationEndManagerEnded, AttemptParticipationEndSittingClosed:
		return true
	default:
		return false
	}
}

// AttemptParticipation is one renewable continuity lease. Only its credential
// hash is domain state; the raw credential is never persisted.
type AttemptParticipation struct {
	ID                       AttemptParticipationID
	AttemptID                ExamAttemptID
	SessionID                SessionID
	State                    AttemptParticipationState
	Generation               int64
	RenewalSequence          int64
	ContinuityCredentialHash string
	StartedAt                time.Time
	UpdatedAt                time.Time
	LeaseExpiresAt           time.Time
	EndedAt                  OptionalTime
	EndReason                AttemptParticipationEndReason
}

func NewAttemptParticipation(id AttemptParticipationID, attemptID ExamAttemptID, sessionID SessionID, generation int64, credentialHash string, startedAt time.Time) (*AttemptParticipation, error) {
	startedAt = TimeUTC(startedAt)
	participation := &AttemptParticipation{ID: id, AttemptID: attemptID, SessionID: sessionID, State: AttemptParticipationActive,
		Generation: generation, ContinuityCredentialHash: credentialHash, StartedAt: startedAt,
		UpdatedAt: startedAt, LeaseExpiresAt: startedAt.Add(AttemptParticipationInitialLease)}
	if err := participation.Validate(); err != nil {
		return nil, err
	}
	return participation, nil
}

func (p *AttemptParticipation) Validate() error {
	if p == nil || !p.ID.IsValid() || !p.AttemptID.IsValid() || !p.SessionID.IsValid() || p.Generation < 1 ||
		p.RenewalSequence < 0 || !IsValidTokenHash(p.ContinuityCredentialHash) ||
		p.ContinuityCredentialHash != strings.ToLower(p.ContinuityCredentialHash) ||
		p.StartedAt.IsZero() || p.UpdatedAt.IsZero() || p.UpdatedAt.Before(p.StartedAt) ||
		p.LeaseExpiresAt.IsZero() || p.LeaseExpiresAt.Before(p.StartedAt) {
		return invalidModelError("AttemptParticipation.Validate", "attempt_participation", "", "invalid Attempt Participation", "")
	}
	switch p.State {
	case AttemptParticipationActive:
		if p.EndedAt.Valid || p.EndReason != "" {
			return invalidModelError("AttemptParticipation.Validate", "attempt_participation", "", "active Attempt Participation has end metadata", "")
		}
	case AttemptParticipationEnded:
		if !p.EndedAt.Valid || p.EndedAt.Time.IsZero() || p.EndedAt.Time.Before(p.StartedAt) ||
			p.EndedAt.Time.After(p.UpdatedAt) || !p.EndReason.IsValid() {
			return invalidModelError("AttemptParticipation.Validate", "attempt_participation", "", "invalid ended Attempt Participation", "")
		}
	default:
		return invalidModelError("AttemptParticipation.Validate", "attempt_participation", "", "invalid Attempt Participation state", "")
	}
	return nil
}

func (p *AttemptParticipation) IsExpiredAt(at time.Time) bool {
	return p == nil || !TimeUTC(at).Before(p.LeaseExpiresAt)
}

// Renew applies one database-time Participation renewal. The caller supplies
// PostgreSQL's decision time; local application clocks are never authoritative.
// Repeating the currently accepted sequence is an idempotent no-op. An older
// sequence, another generation, a non-active Participation, or the exclusive
// expiry boundary is permanently fenced.
func (p *AttemptParticipation) Renew(generation, sequence int64, databaseNow time.Time) (bool, error) {
	if p == nil || p.State != AttemptParticipationActive ||
		generation != p.Generation || sequence < 1 || sequence < p.RenewalSequence {
		return false, invalidModelError("AttemptParticipation.Renew", "attempt_participation", "", "Attempt Participation renewal is fenced", "")
	}
	databaseNow = TimeUTC(databaseNow)
	if databaseNow.IsZero() || databaseNow.Before(p.UpdatedAt) || p.IsExpiredAt(databaseNow) {
		return false, invalidModelError("AttemptParticipation.Renew", "attempt_participation", "", "Attempt Participation renewal is expired", "")
	}
	if sequence == p.RenewalSequence {
		return true, nil
	}
	candidate := *p
	candidate.RenewalSequence = sequence
	candidate.UpdatedAt = databaseNow
	candidate.LeaseExpiresAt = databaseNow.Add(AttemptParticipationInitialLease)
	if err := candidate.Validate(); err != nil {
		return false, err
	}
	*p = candidate
	return false, nil
}

func (p *AttemptParticipation) End(reason AttemptParticipationEndReason, at time.Time) error {
	if p == nil || p.State != AttemptParticipationActive || !reason.IsValid() {
		return invalidModelError("AttemptParticipation.End", "attempt_participation", "", "Attempt Participation cannot end", "")
	}
	candidate := *p
	candidate.State = AttemptParticipationEnded
	candidate.EndReason = reason
	candidate.EndedAt = OptionalTimeFrom(at)
	candidate.UpdatedAt = TimeUTC(at)
	if err := candidate.Validate(); err != nil {
		return err
	}
	*p = candidate
	return nil
}

type AttemptConnectionState string

const (
	AttemptConnectionOpen   AttemptConnectionState = "open"
	AttemptConnectionClosed AttemptConnectionState = "closed"
)

type AttemptConnectionCloseReason string

const (
	AttemptConnectionCloseTransport       AttemptConnectionCloseReason = "transport_closed"
	AttemptConnectionCloseInterrupted     AttemptConnectionCloseReason = "interrupted"
	AttemptConnectionCloseLeaseExpired    AttemptConnectionCloseReason = "lease_expired"
	AttemptConnectionClosePolicySuspended AttemptConnectionCloseReason = "policy_suspended"
	AttemptConnectionCloseKicked          AttemptConnectionCloseReason = "kicked"
	AttemptConnectionCloseSubmitted       AttemptConnectionCloseReason = "submitted"
	AttemptConnectionCloseManagerEnded    AttemptConnectionCloseReason = "manager_ended"
	AttemptConnectionCloseSittingClosed   AttemptConnectionCloseReason = "sitting_closed"
)

// IsValid reports whether reason is one of the closed Connection states owned
// by the domain model.
func (r AttemptConnectionCloseReason) IsValid() bool {
	switch r {
	case AttemptConnectionCloseTransport, AttemptConnectionCloseInterrupted, AttemptConnectionCloseLeaseExpired, AttemptConnectionClosePolicySuspended,
		AttemptConnectionCloseKicked, AttemptConnectionCloseSubmitted, AttemptConnectionCloseManagerEnded, AttemptConnectionCloseSittingClosed:
		return true
	default:
		return false
	}
}

// AttemptConnection is one concrete transport attached to a Participation.
type AttemptConnection struct {
	ID              AttemptConnectionID
	AttemptID       ExamAttemptID
	ParticipationID AttemptParticipationID
	SessionID       SessionID
	State           AttemptConnectionState
	OpenedAt        time.Time
	ClosedAt        OptionalTime
	CloseReason     AttemptConnectionCloseReason
}

func NewAttemptConnection(id AttemptConnectionID, attemptID ExamAttemptID, participationID AttemptParticipationID, sessionID SessionID, at time.Time) (*AttemptConnection, error) {
	connection := &AttemptConnection{ID: id, AttemptID: attemptID, ParticipationID: participationID,
		SessionID: sessionID, State: AttemptConnectionOpen, OpenedAt: TimeUTC(at)}
	if err := connection.Validate(); err != nil {
		return nil, err
	}
	return connection, nil
}

func (c *AttemptConnection) Validate() error {
	if c == nil || !c.ID.IsValid() || !c.AttemptID.IsValid() || !c.ParticipationID.IsValid() ||
		!c.SessionID.IsValid() || c.OpenedAt.IsZero() {
		return invalidModelError("AttemptConnection.Validate", "attempt_connection", "", "invalid Attempt Connection", "")
	}
	switch c.State {
	case AttemptConnectionOpen:
		if c.ClosedAt.Valid || c.CloseReason != "" {
			return invalidModelError("AttemptConnection.Validate", "attempt_connection", "", "open Attempt Connection has close metadata", "")
		}
	case AttemptConnectionClosed:
		if !c.ClosedAt.Valid || c.ClosedAt.Time.IsZero() || c.ClosedAt.Time.Before(c.OpenedAt) || !c.CloseReason.IsValid() {
			return invalidModelError("AttemptConnection.Validate", "attempt_connection", "", "invalid closed Attempt Connection", "")
		}
	default:
		return invalidModelError("AttemptConnection.Validate", "attempt_connection", "", "invalid Attempt Connection state", "")
	}
	return nil
}

func (c *AttemptConnection) Close(reason AttemptConnectionCloseReason, at time.Time) error {
	if c == nil || c.State != AttemptConnectionOpen || !reason.IsValid() {
		return invalidModelError("AttemptConnection.Close", "attempt_connection", "", "Attempt Connection cannot close", "")
	}
	candidate := *c
	candidate.State = AttemptConnectionClosed
	candidate.CloseReason = reason
	candidate.ClosedAt = OptionalTimeFrom(at)
	if err := candidate.Validate(); err != nil {
		return err
	}
	*c = candidate
	return nil
}

// IntegrityPolicyKind identifies the bounded detector policy that caused
// evidence and a flag. It is not a verdict or severity supplied by a client.
type IntegrityPolicyKind string

const (
	IntegrityPolicyConnectionLoss    IntegrityPolicyKind = "connection_loss"
	IntegrityPolicyFocusLoss         IntegrityPolicyKind = "focus_loss"
	IntegrityPolicyBrowserNavigation IntegrityPolicyKind = "browser_navigation"
)

func (k IntegrityPolicyKind) IsValid() bool {
	return k == IntegrityPolicyConnectionLoss || k == IntegrityPolicyFocusLoss || k == IntegrityPolicyBrowserNavigation
}

// IntegrityEvidence is neutral server-owned evidence for one policy flag.
// Connection Loss evidence intentionally has no accusation, free-form text,
// client time, credential, Session identity, or transport payload.
type IntegrityEvidence struct {
	Browser              *BrowserIntegrityEvidence
	ID                   IntegrityEvidenceID
	AttemptID            ExamAttemptID
	ParticipationID      AttemptParticipationID
	FlagID               IntegrityFlagID
	Generation           int64
	Kind                 IntegrityPolicyKind
	SignalID             FocusLossSignalID
	Sequence             int64
	DurationMilliseconds int64
	Source               FocusLossSource
	MissingBefore        int64
	ObservedAt           time.Time
	RecordedAt           time.Time
}

func NewConnectionLossEvidence(id IntegrityEvidenceID, attemptID ExamAttemptID, participationID AttemptParticipationID,
	flagID IntegrityFlagID, generation int64, leaseExpiredAt, recordedAt time.Time,
) (*IntegrityEvidence, error) {
	evidence := &IntegrityEvidence{ID: id, AttemptID: attemptID, ParticipationID: participationID, FlagID: flagID,
		Generation: generation, Kind: IntegrityPolicyConnectionLoss, ObservedAt: TimeUTC(leaseExpiredAt), RecordedAt: TimeUTC(recordedAt)}
	if err := evidence.Validate(); err != nil {
		return nil, err
	}
	return evidence, nil
}

// NewFocusLossEvidence converts one qualifying episode from the consumed
// evaluation bucket into bounded evidence associated with its generation Flag.
// Pending bucket rows are not Integrity Evidence and remain Store-internal.
func NewFocusLossEvidence(id IntegrityEvidenceID, signal FocusLossSignal, flagID IntegrityFlagID,
	missingBefore int64, recordedAt time.Time,
) (*IntegrityEvidence, error) {
	evidence := &IntegrityEvidence{ID: id, AttemptID: signal.AttemptID, ParticipationID: signal.ParticipationID,
		FlagID: flagID, Generation: signal.Generation, Kind: IntegrityPolicyFocusLoss, SignalID: signal.ID,
		Sequence: signal.Sequence, DurationMilliseconds: signal.DurationMilliseconds, Source: signal.Source,
		MissingBefore: missingBefore, ObservedAt: signal.ReceivedAt, RecordedAt: TimeUTC(recordedAt)}
	if err := evidence.Validate(); err != nil {
		return nil, err
	}
	return evidence, nil
}

func (e *IntegrityEvidence) Validate() error {
	if e == nil || !e.ID.IsValid() || !e.AttemptID.IsValid() || !e.ParticipationID.IsValid() ||
		e.Generation < 1 || !e.Kind.IsValid() || e.ObservedAt.IsZero() ||
		e.RecordedAt.IsZero() || e.RecordedAt.Before(e.ObservedAt) {
		return invalidModelError("IntegrityEvidence.Validate", "integrity_evidence", "", "invalid Integrity Evidence", "")
	}
	switch e.Kind {
	case IntegrityPolicyBrowserNavigation:
		if !e.FlagID.IsValid() || e.Browser == nil || e.Browser.Validate() != nil || !e.SignalID.IsZero() || e.Sequence != 0 || e.DurationMilliseconds != 0 || e.Source != "" || e.MissingBefore != 0 {
			return invalidModelError("IntegrityEvidence.Validate", "integrity_evidence", "", "invalid Browser integrity evidence", "")
		}
	case IntegrityPolicyConnectionLoss:
		if e.Browser != nil || !e.FlagID.IsValid() || !e.SignalID.IsZero() || e.Sequence != 0 ||
			e.DurationMilliseconds != 0 || e.Source != "" || e.MissingBefore != 0 {
			return invalidModelError("IntegrityEvidence.Validate", "integrity_evidence", "", "invalid Connection Loss evidence", "")
		}
	case IntegrityPolicyFocusLoss:
		if e.Browser != nil || !e.FlagID.IsValid() || !e.SignalID.IsValid() || e.Sequence < 1 ||
			e.DurationMilliseconds < 1 || e.DurationMilliseconds > FocusLossMaximumDurationMilliseconds ||
			!e.Source.IsValid() || e.MissingBefore < 0 {
			return invalidModelError("IntegrityEvidence.Validate", "integrity_evidence", "", "invalid Focus Loss evidence", "")
		}
	}
	return nil
}

type IntegrityFlagState string

const IntegrityFlagOpen IntegrityFlagState = "open"

// IntegrityFlag is an append-preserving indication for review, never a guilt
// finding. Ticket 13 creates one open flag per Attempt/policy/generation.
type IntegrityFlag struct {
	ID         IntegrityFlagID
	AttemptID  ExamAttemptID
	Generation int64
	Kind       IntegrityPolicyKind
	State      IntegrityFlagState
	CreatedAt  time.Time
}

func NewIntegrityFlag(id IntegrityFlagID, attemptID ExamAttemptID, generation int64, kind IntegrityPolicyKind, at time.Time) (*IntegrityFlag, error) {
	flag := &IntegrityFlag{ID: id, AttemptID: attemptID, Generation: generation, Kind: kind, State: IntegrityFlagOpen, CreatedAt: TimeUTC(at)}
	if err := flag.Validate(); err != nil {
		return nil, err
	}
	return flag, nil
}

func (f *IntegrityFlag) Validate() error {
	if f == nil || !f.ID.IsValid() || !f.AttemptID.IsValid() || f.Generation < 1 ||
		!f.Kind.IsValid() || f.State != IntegrityFlagOpen || f.CreatedAt.IsZero() {
		return invalidModelError("IntegrityFlag.Validate", "integrity_flag", "", "invalid Integrity Flag", "")
	}
	return nil
}

type AttemptSuspensionState string

const (
	AttemptSuspensionActive AttemptSuspensionState = "active"
	AttemptSuspensionClosed AttemptSuspensionState = "closed"
)

type AttemptSuspensionSource string

const (
	AttemptSuspensionSourcePolicy AttemptSuspensionSource = "policy"
	// AttemptSuspensionSourceManual is an authorized Exam Manager's kick.
	AttemptSuspensionSourceManual AttemptSuspensionSource = "manual"
)

func (s AttemptSuspensionSource) IsValid() bool {
	return s == AttemptSuspensionSourcePolicy || s == AttemptSuspensionSourceManual
}

type AttemptSuspensionCandidateReason string

const (
	AttemptSuspensionCandidateReasonSecureContinuityLost AttemptSuspensionCandidateReason = "secure_connectivity_lost"
	AttemptSuspensionCandidateReasonFocusLossPolicy      AttemptSuspensionCandidateReason = "focus_policy_review_required"
	// AttemptSuspensionCandidateReasonManagerSuspended is the only safe
	// candidate explanation of a manual suspension; the manager's private
	// reason is never shown to the candidate.
	AttemptSuspensionCandidateReasonManagerSuspended AttemptSuspensionCandidateReason = "manager_suspended"
)

const AttemptSuspensionPrivateReasonMaximumRunes = 1000

func (r AttemptSuspensionCandidateReason) IsValid() bool {
	return r.IsPolicy() || r == AttemptSuspensionCandidateReasonManagerSuspended
}

// IsPolicy reports whether the reason belongs to automatic policy enforcement.
func (r AttemptSuspensionCandidateReason) IsPolicy() bool {
	return r == AttemptSuspensionCandidateReasonSecureContinuityLost || r == AttemptSuspensionCandidateReasonFocusLossPolicy
}

// AttemptSuspension is one append-preserving blocking episode. A policy
// episode links its Integrity Flag; a manual episode links none and records
// the suspending manager and their private reason. PrivateReason (re-allow)
// and SuspensionPrivateReason are manager-only and must never enter ordinary
// logs, audit data, or realtime.
type AttemptSuspension struct {
	ID                      AttemptSuspensionID
	AttemptID               ExamAttemptID
	ParticipationID         AttemptParticipationID
	FlagID                  IntegrityFlagID
	Generation              int64
	State                   AttemptSuspensionState
	Source                  AttemptSuspensionSource
	CandidateReason         AttemptSuspensionCandidateReason
	StartedAt               time.Time
	EndedAt                 OptionalTime
	ReallowedByUserID       UserID
	PrivateReason           string
	SuspendedByUserID       UserID
	SuspensionPrivateReason string
}

func NewPolicyAttemptSuspension(id AttemptSuspensionID, attemptID ExamAttemptID, participationID AttemptParticipationID,
	flagID IntegrityFlagID, generation int64, candidateReason AttemptSuspensionCandidateReason, at time.Time,
) (*AttemptSuspension, error) {
	suspension := &AttemptSuspension{ID: id, AttemptID: attemptID, ParticipationID: participationID, FlagID: flagID,
		Generation: generation, State: AttemptSuspensionActive, Source: AttemptSuspensionSourcePolicy,
		CandidateReason: candidateReason, StartedAt: TimeUTC(at)}
	if err := suspension.Validate(); err != nil {
		return nil, err
	}
	return suspension, nil
}

// NewManualAttemptSuspension records an Exam Manager's kick of the exact
// active Participation generation. It links no Integrity Flag.
func NewManualAttemptSuspension(id AttemptSuspensionID, attemptID ExamAttemptID, participationID AttemptParticipationID,
	generation int64, actorID UserID, privateReason string, at time.Time,
) (*AttemptSuspension, error) {
	suspension := &AttemptSuspension{ID: id, AttemptID: attemptID, ParticipationID: participationID,
		Generation: generation, State: AttemptSuspensionActive, Source: AttemptSuspensionSourceManual,
		CandidateReason: AttemptSuspensionCandidateReasonManagerSuspended, StartedAt: TimeUTC(at),
		SuspendedByUserID: actorID, SuspensionPrivateReason: privateReason}
	if err := suspension.Validate(); err != nil {
		return nil, err
	}
	return suspension, nil
}

func (s *AttemptSuspension) Validate() error {
	if s == nil || !s.ID.IsValid() || !s.AttemptID.IsValid() || !s.ParticipationID.IsValid() ||
		s.Generation < 1 || s.StartedAt.IsZero() {
		return invalidModelError("AttemptSuspension.Validate", "attempt_suspension", "", "invalid Attempt Suspension", "")
	}
	switch s.Source {
	case AttemptSuspensionSourcePolicy:
		if !s.FlagID.IsValid() || !s.CandidateReason.IsPolicy() || !s.SuspendedByUserID.IsZero() ||
			s.SuspensionPrivateReason != "" {
			return invalidModelError("AttemptSuspension.Validate", "attempt_suspension", "", "invalid policy Attempt Suspension", "")
		}
	case AttemptSuspensionSourceManual:
		if !s.FlagID.IsZero() || s.CandidateReason != AttemptSuspensionCandidateReasonManagerSuspended ||
			!s.SuspendedByUserID.IsValid() || !validAttemptSuspensionPrivateReason(s.SuspensionPrivateReason) {
			return invalidModelError("AttemptSuspension.Validate", "attempt_suspension", "", "invalid manual Attempt Suspension", "")
		}
	default:
		return invalidModelError("AttemptSuspension.Validate", "attempt_suspension", "", "invalid Attempt Suspension source", "")
	}
	switch s.State {
	case AttemptSuspensionActive:
		if s.EndedAt.Valid || !s.ReallowedByUserID.IsZero() || s.PrivateReason != "" {
			return invalidModelError("AttemptSuspension.Validate", "attempt_suspension", "", "active Attempt Suspension has re-allow metadata", "")
		}
	case AttemptSuspensionClosed:
		if !s.EndedAt.Valid || s.EndedAt.Time.Before(s.StartedAt) ||
			!s.ReallowedByUserID.IsValid() || !validAttemptSuspensionPrivateReason(s.PrivateReason) {
			return invalidModelError("AttemptSuspension.Validate", "attempt_suspension", "", "invalid closed Attempt Suspension", "")
		}
	default:
		return invalidModelError("AttemptSuspension.Validate", "attempt_suspension", "", "invalid Attempt Suspension state", "")
	}
	return nil
}

func (s *AttemptSuspension) Reallow(actorID UserID, privateReason string, at time.Time) error {
	if s == nil || s.State != AttemptSuspensionActive || !actorID.IsValid() ||
		!validAttemptSuspensionPrivateReason(privateReason) {
		return invalidModelError("AttemptSuspension.Reallow", "attempt_suspension", "", "Attempt Suspension cannot be re-allowed", "")
	}
	candidate := *s
	candidate.State = AttemptSuspensionClosed
	candidate.EndedAt = OptionalTimeFrom(at)
	candidate.ReallowedByUserID = actorID
	candidate.PrivateReason = privateReason
	if err := candidate.Validate(); err != nil {
		return err
	}
	*s = candidate
	return nil
}

// IsValidAttemptSuspensionPrivateReason reports whether a manager's private
// suspension or re-allow reason is trimmed, valid UTF-8, and bounded.
func IsValidAttemptSuspensionPrivateReason(reason string) bool {
	return validAttemptSuspensionPrivateReason(reason)
}

func validAttemptSuspensionPrivateReason(reason string) bool {
	return utf8.ValidString(reason) && reason == strings.TrimSpace(reason) && utf8.RuneCountInString(reason) >= 1 &&
		utf8.RuneCountInString(reason) <= AttemptSuspensionPrivateReasonMaximumRunes && len(reason) <= 4000
}

type AttemptWorkspaceObjectStorage string

const (
	AttemptWorkspaceStorageStarter AttemptWorkspaceObjectStorage = "starter"
	AttemptWorkspaceStorageAttempt AttemptWorkspaceObjectStorage = "attempt"
)

// ExamAttemptWorkspace is the stable private Workspace owned one-to-one by an
// Attempt. Cursor fences the ordered mutation stream without making paths or
// content objects into aggregate identities.
type ExamAttemptWorkspace struct {
	ID        ExamAttemptWorkspaceID
	AttemptID ExamAttemptID
	Cursor    int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

func NewExamAttemptWorkspace(id ExamAttemptWorkspaceID, attemptID ExamAttemptID, at time.Time) (*ExamAttemptWorkspace, error) {
	at = TimeUTC(at)
	workspace := &ExamAttemptWorkspace{ID: id, AttemptID: attemptID, CreatedAt: at, UpdatedAt: at}
	if err := workspace.Validate(); err != nil {
		return nil, err
	}
	return workspace, nil
}

func (w *ExamAttemptWorkspace) Validate() error {
	if w == nil || !w.ID.IsValid() || !w.AttemptID.IsValid() || w.Cursor < 0 ||
		w.CreatedAt.IsZero() || w.UpdatedAt.IsZero() || w.UpdatedAt.Before(w.CreatedAt) {
		return invalidModelError("ExamAttemptWorkspace.Validate", "exam_attempt_workspace", "", "invalid Exam Attempt Workspace", "")
	}
	return nil
}

// AttemptWorkspaceObject is attempt-owned immutable content metadata. Starter
// origin references pinned starter bytes; Attempt origin references bytes
// written by the candidate in a later copy-on-write mutation.
type AttemptWorkspaceObject struct {
	ID              AttemptWorkspaceObjectID
	WorkspaceID     ExamAttemptWorkspaceID
	StorageOrigin   AttemptWorkspaceObjectStorage
	StarterObjectID StarterWorkspaceObjectID
	State           AttemptWorkspaceObjectState
	ContentVersion  WorkspaceContentVersion
	MediaType       string
	SizeBytes       int64
	SHA256          string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	ExpiresAt       time.Time
	ReclaimAfter    OptionalTime
	ClaimToken      string
	ClaimedAt       OptionalTime
}

func NewStarterOriginAttemptWorkspaceObject(id AttemptWorkspaceObjectID, workspaceID ExamAttemptWorkspaceID, starterObjectID StarterWorkspaceObjectID, version WorkspaceContentVersion, mediaType string, size int64, checksum string, at time.Time) (*AttemptWorkspaceObject, error) {
	object := &AttemptWorkspaceObject{ID: id, WorkspaceID: workspaceID, StorageOrigin: AttemptWorkspaceStorageStarter,
		StarterObjectID: starterObjectID, State: AttemptWorkspaceObjectCurrent, ContentVersion: version, MediaType: strings.TrimSpace(mediaType),
		SizeBytes: size, SHA256: strings.ToLower(checksum), CreatedAt: TimeUTC(at), UpdatedAt: TimeUTC(at)}
	if err := object.Validate(); err != nil {
		return nil, err
	}
	return object, nil
}

func (o *AttemptWorkspaceObject) Validate() error {
	return validateAttemptWorkspaceObject(o)
}

// AttemptWorkspaceEntry is attempt-owned logical metadata. AdmissionRevisionID
// and SourceStarterEntryID preserve the exact bootstrap provenance.
type AttemptWorkspaceEntry struct {
	ID                   AttemptWorkspaceEntryID
	WorkspaceID          ExamAttemptWorkspaceID
	AdmissionRevisionID  ExamRevisionID
	SourceStarterEntryID StarterWorkspaceEntryID
	Kind                 StarterWorkspaceEntryKind
	Path                 string
	CurrentObjectID      AttemptWorkspaceObjectID
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

func NewAttemptWorkspaceFile(id AttemptWorkspaceEntryID, workspaceID ExamAttemptWorkspaceID, revisionID ExamRevisionID, sourceID StarterWorkspaceEntryID, path string, objectID AttemptWorkspaceObjectID, at time.Time) (*AttemptWorkspaceEntry, error) {
	return newAttemptWorkspaceEntry(id, workspaceID, revisionID, sourceID, StarterWorkspaceEntryFile, path, objectID, at)
}

func NewAttemptWorkspaceDirectory(id AttemptWorkspaceEntryID, workspaceID ExamAttemptWorkspaceID, revisionID ExamRevisionID, sourceID StarterWorkspaceEntryID, path string, at time.Time) (*AttemptWorkspaceEntry, error) {
	return newAttemptWorkspaceEntry(id, workspaceID, revisionID, sourceID, StarterWorkspaceEntryDirectory, path, "", at)
}

func newAttemptWorkspaceEntry(id AttemptWorkspaceEntryID, workspaceID ExamAttemptWorkspaceID, revisionID ExamRevisionID, sourceID StarterWorkspaceEntryID, kind StarterWorkspaceEntryKind, path string, objectID AttemptWorkspaceObjectID, at time.Time) (*AttemptWorkspaceEntry, error) {
	if !sourceID.IsValid() {
		return nil, invalidModelError("newAttemptWorkspaceEntry", "exam_attempt", "", "Attempt Workspace bootstrap requires source entry", "")
	}
	normalized, err := NormalizeStarterWorkspacePath(path)
	if err != nil {
		return nil, err
	}
	at = TimeUTC(at)
	entry := &AttemptWorkspaceEntry{ID: id, WorkspaceID: workspaceID, AdmissionRevisionID: revisionID,
		SourceStarterEntryID: sourceID, Kind: kind, Path: normalized, CurrentObjectID: objectID, CreatedAt: at, UpdatedAt: at}
	if err := entry.Validate(); err != nil {
		return nil, err
	}
	return entry, nil
}

func (e *AttemptWorkspaceEntry) Validate() error {
	if e == nil || !e.ID.IsValid() || !e.WorkspaceID.IsValid() || !e.AdmissionRevisionID.IsValid() ||
		(!e.SourceStarterEntryID.IsZero() && !e.SourceStarterEntryID.IsValid()) || e.CreatedAt.IsZero() || e.UpdatedAt.IsZero() || e.UpdatedAt.Before(e.CreatedAt) {
		return invalidModelError("AttemptWorkspaceEntry.Validate", "attempt_workspace_entry", "", "invalid Attempt Workspace entry", "")
	}
	if normalized, err := NormalizeStarterWorkspacePath(e.Path); err != nil || normalized != e.Path {
		return invalidModelError("AttemptWorkspaceEntry.Validate", "attempt_workspace_entry", "", "invalid Attempt Workspace entry path", "")
	}
	switch e.Kind {
	case StarterWorkspaceEntryFile:
		if !e.CurrentObjectID.IsValid() {
			return invalidModelError("AttemptWorkspaceEntry.Validate", "attempt_workspace_entry", "", "Attempt Workspace file requires current object", "")
		}
	case StarterWorkspaceEntryDirectory:
		if !e.CurrentObjectID.IsZero() {
			return invalidModelError("AttemptWorkspaceEntry.Validate", "attempt_workspace_entry", "", "Attempt Workspace directory cannot have content", "")
		}
	default:
		return invalidModelError("AttemptWorkspaceEntry.Validate", "attempt_workspace_entry", "", "invalid Attempt Workspace entry kind", "")
	}
	return nil
}
