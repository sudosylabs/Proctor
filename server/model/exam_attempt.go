// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"fmt"
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

func (state ExamAttemptState) AllowsCandidateConnection() bool { return state == ExamAttemptActive }
func (state ExamAttemptState) IsTerminal() bool                { return state == ExamAttemptSubmitted }
func (state ExamAttemptState) IsUnresolved() bool {
	return state == ExamAttemptReady || state == ExamAttemptActive || state == ExamAttemptSuspended
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

func (attempt *ExamAttempt) Validate() error {
	if attempt == nil || !attempt.ID.IsValid() || !attempt.ExamID.IsValid() || !attempt.SittingID.IsValid() ||
		!attempt.CandidateUserID.IsValid() || !attempt.AdmissionRevisionID.IsValid() || attempt.CreatedAt.IsZero() ||
		attempt.UpdatedAt.IsZero() || attempt.UpdatedAt.Before(attempt.CreatedAt) || attempt.Revision < 1 {
		return fmt.Errorf("model: invalid Exam Attempt")
	}
	switch attempt.State {
	case ExamAttemptReady, ExamAttemptActive, ExamAttemptSuspended:
		if attempt.SubmittedAt.Valid {
			return fmt.Errorf("model: non-submitted Exam Attempt has submitted_at")
		}
	case ExamAttemptSubmitted:
		if !attempt.SubmittedAt.Valid || attempt.SubmittedAt.Time.IsZero() || attempt.SubmittedAt.Time.Before(attempt.CreatedAt) || attempt.SubmittedAt.Time.After(attempt.UpdatedAt) {
			return fmt.Errorf("model: invalid submitted Exam Attempt lifecycle")
		}
	default:
		return fmt.Errorf("model: invalid Exam Attempt state")
	}
	return nil
}

// Suspend enters one blocking enforcement episode. Persistence is responsible
// for committing the causal evidence, flag, Suspension, audit, and this state
// transition atomically.
func (attempt *ExamAttempt) Suspend(at time.Time) error {
	if attempt == nil || attempt.State != ExamAttemptActive {
		return fmt.Errorf("model: Exam Attempt cannot suspend")
	}
	candidate := *attempt
	candidate.State = ExamAttemptSuspended
	candidate.UpdatedAt = TimeUTC(at)
	candidate.Revision++
	if err := candidate.Validate(); err != nil {
		return err
	}
	*attempt = candidate
	return nil
}

// Reallow closes the blocking effect of the current Suspension and moves the
// Attempt to ready. A later secure admission creates a new Participation and
// is the only transition that can make the Attempt active again.
func (attempt *ExamAttempt) Reallow(at time.Time) error {
	if attempt == nil || attempt.State != ExamAttemptSuspended {
		return fmt.Errorf("model: Exam Attempt cannot be re-allowed")
	}
	candidate := *attempt
	candidate.State = ExamAttemptReady
	candidate.UpdatedAt = TimeUTC(at)
	candidate.Revision++
	if err := candidate.Validate(); err != nil {
		return err
	}
	*attempt = candidate
	return nil
}

// Activate admits a ready Attempt after all candidate, Sitting, Session,
// registered-key, compatibility, and posture checks have succeeded.
func (attempt *ExamAttempt) Activate(at time.Time) error {
	if attempt == nil || attempt.State != ExamAttemptReady {
		return fmt.Errorf("model: Exam Attempt cannot activate")
	}
	candidate := *attempt
	candidate.State = ExamAttemptActive
	candidate.UpdatedAt = TimeUTC(at)
	candidate.Revision++
	if err := candidate.Validate(); err != nil {
		return err
	}
	*attempt = candidate
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

func (reason AttemptParticipationEndReason) isValid() bool {
	switch reason {
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

func (participation *AttemptParticipation) Validate() error {
	if participation == nil || !participation.ID.IsValid() || !participation.AttemptID.IsValid() || !participation.SessionID.IsValid() || participation.Generation < 1 ||
		participation.RenewalSequence < 0 || !IsValidTokenHash(participation.ContinuityCredentialHash) ||
		participation.ContinuityCredentialHash != strings.ToLower(participation.ContinuityCredentialHash) ||
		participation.StartedAt.IsZero() || participation.UpdatedAt.IsZero() || participation.UpdatedAt.Before(participation.StartedAt) ||
		participation.LeaseExpiresAt.IsZero() || participation.LeaseExpiresAt.Before(participation.StartedAt) {
		return fmt.Errorf("model: invalid Attempt Participation")
	}
	switch participation.State {
	case AttemptParticipationActive:
		if participation.EndedAt.Valid || participation.EndReason != "" {
			return fmt.Errorf("model: active Attempt Participation has end metadata")
		}
	case AttemptParticipationEnded:
		if !participation.EndedAt.Valid || participation.EndedAt.Time.IsZero() || participation.EndedAt.Time.Before(participation.StartedAt) ||
			participation.EndedAt.Time.After(participation.UpdatedAt) || !participation.EndReason.isValid() {
			return fmt.Errorf("model: invalid ended Attempt Participation")
		}
	default:
		return fmt.Errorf("model: invalid Attempt Participation state")
	}
	return nil
}

func (participation *AttemptParticipation) IsExpiredAt(at time.Time) bool {
	return participation == nil || !TimeUTC(at).Before(participation.LeaseExpiresAt)
}

// Renew applies one database-time Participation renewal. The caller supplies
// PostgreSQL's decision time; local application clocks are never authoritative.
// Repeating the currently accepted sequence is an idempotent no-op. An older
// sequence, another generation, a non-active Participation, or the exclusive
// expiry boundary is permanently fenced.
func (participation *AttemptParticipation) Renew(generation, sequence int64, databaseNow time.Time) (bool, error) {
	if participation == nil || participation.State != AttemptParticipationActive ||
		generation != participation.Generation || sequence < 1 || sequence < participation.RenewalSequence {
		return false, fmt.Errorf("model: Attempt Participation renewal is fenced")
	}
	databaseNow = TimeUTC(databaseNow)
	if databaseNow.IsZero() || databaseNow.Before(participation.UpdatedAt) || participation.IsExpiredAt(databaseNow) {
		return false, fmt.Errorf("model: Attempt Participation renewal is expired")
	}
	if sequence == participation.RenewalSequence {
		return true, nil
	}
	candidate := *participation
	candidate.RenewalSequence = sequence
	candidate.UpdatedAt = databaseNow
	candidate.LeaseExpiresAt = databaseNow.Add(AttemptParticipationInitialLease)
	if err := candidate.Validate(); err != nil {
		return false, err
	}
	*participation = candidate
	return false, nil
}

func (participation *AttemptParticipation) End(reason AttemptParticipationEndReason, at time.Time) error {
	if participation == nil || participation.State != AttemptParticipationActive || !reason.isValid() {
		return fmt.Errorf("model: Attempt Participation cannot end")
	}
	candidate := *participation
	candidate.State = AttemptParticipationEnded
	candidate.EndReason = reason
	candidate.EndedAt = OptionalTimeFrom(at)
	candidate.UpdatedAt = TimeUTC(at)
	if err := candidate.Validate(); err != nil {
		return err
	}
	*participation = candidate
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
func (reason AttemptConnectionCloseReason) IsValid() bool {
	switch reason {
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

func (connection *AttemptConnection) Validate() error {
	if connection == nil || !connection.ID.IsValid() || !connection.AttemptID.IsValid() || !connection.ParticipationID.IsValid() ||
		!connection.SessionID.IsValid() || connection.OpenedAt.IsZero() {
		return fmt.Errorf("model: invalid Attempt Connection")
	}
	switch connection.State {
	case AttemptConnectionOpen:
		if connection.ClosedAt.Valid || connection.CloseReason != "" {
			return fmt.Errorf("model: open Attempt Connection has close metadata")
		}
	case AttemptConnectionClosed:
		if !connection.ClosedAt.Valid || connection.ClosedAt.Time.IsZero() || connection.ClosedAt.Time.Before(connection.OpenedAt) || !connection.CloseReason.IsValid() {
			return fmt.Errorf("model: invalid closed Attempt Connection")
		}
	default:
		return fmt.Errorf("model: invalid Attempt Connection state")
	}
	return nil
}

func (connection *AttemptConnection) Close(reason AttemptConnectionCloseReason, at time.Time) error {
	if connection == nil || connection.State != AttemptConnectionOpen || !reason.IsValid() {
		return fmt.Errorf("model: Attempt Connection cannot close")
	}
	candidate := *connection
	candidate.State = AttemptConnectionClosed
	candidate.CloseReason = reason
	candidate.ClosedAt = OptionalTimeFrom(at)
	if err := candidate.Validate(); err != nil {
		return err
	}
	*connection = candidate
	return nil
}

// IntegrityPolicyKind identifies the bounded detector policy that caused
// evidence and a flag. It is not a verdict or severity supplied by a client.
type IntegrityPolicyKind string

const (
	IntegrityPolicyConnectionLoss IntegrityPolicyKind = "connection_loss"
	IntegrityPolicyFocusLoss      IntegrityPolicyKind = "focus_loss"
)

func (kind IntegrityPolicyKind) isValid() bool {
	return kind == IntegrityPolicyConnectionLoss || kind == IntegrityPolicyFocusLoss
}

// IntegrityEvidence is neutral server-owned evidence for one policy flag.
// Connection Loss evidence intentionally has no accusation, free-form text,
// client time, credential, Session identity, or transport payload.
type IntegrityEvidence struct {
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

func (evidence *IntegrityEvidence) Validate() error {
	if evidence == nil || !evidence.ID.IsValid() || !evidence.AttemptID.IsValid() || !evidence.ParticipationID.IsValid() ||
		evidence.Generation < 1 || !evidence.Kind.isValid() || evidence.ObservedAt.IsZero() ||
		evidence.RecordedAt.IsZero() || evidence.RecordedAt.Before(evidence.ObservedAt) {
		return fmt.Errorf("model: invalid Integrity Evidence")
	}
	switch evidence.Kind {
	case IntegrityPolicyConnectionLoss:
		if !evidence.FlagID.IsValid() || !evidence.SignalID.IsZero() || evidence.Sequence != 0 ||
			evidence.DurationMilliseconds != 0 || evidence.Source != "" || evidence.MissingBefore != 0 {
			return fmt.Errorf("model: invalid Connection Loss evidence")
		}
	case IntegrityPolicyFocusLoss:
		if !evidence.FlagID.IsValid() || !evidence.SignalID.IsValid() || evidence.Sequence < 1 ||
			evidence.DurationMilliseconds < 1 || evidence.DurationMilliseconds > FocusLossMaximumDurationMilliseconds ||
			!evidence.Source.IsValid() || evidence.MissingBefore < 0 {
			return fmt.Errorf("model: invalid Focus Loss evidence")
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

func (flag *IntegrityFlag) Validate() error {
	if flag == nil || !flag.ID.IsValid() || !flag.AttemptID.IsValid() || flag.Generation < 1 ||
		!flag.Kind.isValid() || flag.State != IntegrityFlagOpen || flag.CreatedAt.IsZero() {
		return fmt.Errorf("model: invalid Integrity Flag")
	}
	return nil
}

type AttemptSuspensionState string

const (
	AttemptSuspensionActive AttemptSuspensionState = "active"
	AttemptSuspensionClosed AttemptSuspensionState = "closed"
)

type AttemptSuspensionSource string

const AttemptSuspensionSourcePolicy AttemptSuspensionSource = "policy"

type AttemptSuspensionCandidateReason string

const (
	AttemptSuspensionCandidateReasonSecureContinuityLost AttemptSuspensionCandidateReason = "secure_connectivity_lost"
	AttemptSuspensionCandidateReasonFocusLossPolicy      AttemptSuspensionCandidateReason = "focus_policy_review_required"
	AttemptSuspensionPrivateReasonMaximumRunes                                            = 1000
)

func (reason AttemptSuspensionCandidateReason) IsValid() bool {
	return reason == AttemptSuspensionCandidateReasonSecureContinuityLost || reason == AttemptSuspensionCandidateReasonFocusLossPolicy
}

// AttemptSuspension is one append-preserving blocking episode. PrivateReason
// is manager-only and must never enter ordinary logs, audit data, or realtime.
type AttemptSuspension struct {
	ID                AttemptSuspensionID
	AttemptID         ExamAttemptID
	ParticipationID   AttemptParticipationID
	FlagID            IntegrityFlagID
	Generation        int64
	State             AttemptSuspensionState
	Source            AttemptSuspensionSource
	CandidateReason   AttemptSuspensionCandidateReason
	StartedAt         time.Time
	EndedAt           OptionalTime
	ReallowedByUserID UserID
	PrivateReason     string
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

func (suspension *AttemptSuspension) Validate() error {
	if suspension == nil || !suspension.ID.IsValid() || !suspension.AttemptID.IsValid() || !suspension.ParticipationID.IsValid() ||
		!suspension.FlagID.IsValid() || suspension.Generation < 1 || suspension.Source != AttemptSuspensionSourcePolicy ||
		(suspension.CandidateReason != AttemptSuspensionCandidateReasonSecureContinuityLost &&
			suspension.CandidateReason != AttemptSuspensionCandidateReasonFocusLossPolicy) || suspension.StartedAt.IsZero() {
		return fmt.Errorf("model: invalid Attempt Suspension")
	}
	switch suspension.State {
	case AttemptSuspensionActive:
		if suspension.EndedAt.Valid || !suspension.ReallowedByUserID.IsZero() || suspension.PrivateReason != "" {
			return fmt.Errorf("model: active Attempt Suspension has re-allow metadata")
		}
	case AttemptSuspensionClosed:
		if !suspension.EndedAt.Valid || suspension.EndedAt.Time.Before(suspension.StartedAt) ||
			!suspension.ReallowedByUserID.IsValid() || !validAttemptSuspensionPrivateReason(suspension.PrivateReason) {
			return fmt.Errorf("model: invalid closed Attempt Suspension")
		}
	default:
		return fmt.Errorf("model: invalid Attempt Suspension state")
	}
	return nil
}

func (suspension *AttemptSuspension) Reallow(actorID UserID, privateReason string, at time.Time) error {
	if suspension == nil || suspension.State != AttemptSuspensionActive || !actorID.IsValid() ||
		!validAttemptSuspensionPrivateReason(privateReason) {
		return fmt.Errorf("model: Attempt Suspension cannot be re-allowed")
	}
	candidate := *suspension
	candidate.State = AttemptSuspensionClosed
	candidate.EndedAt = OptionalTimeFrom(at)
	candidate.ReallowedByUserID = actorID
	candidate.PrivateReason = privateReason
	if err := candidate.Validate(); err != nil {
		return err
	}
	*suspension = candidate
	return nil
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

func (workspace *ExamAttemptWorkspace) Validate() error {
	if workspace == nil || !workspace.ID.IsValid() || !workspace.AttemptID.IsValid() || workspace.Cursor < 0 ||
		workspace.CreatedAt.IsZero() || workspace.UpdatedAt.IsZero() || workspace.UpdatedAt.Before(workspace.CreatedAt) {
		return fmt.Errorf("model: invalid Exam Attempt Workspace")
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

func (object *AttemptWorkspaceObject) Validate() error {
	return validateAttemptWorkspaceObject(object)
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
		return nil, fmt.Errorf("model: Attempt Workspace bootstrap requires source entry")
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

func (entry *AttemptWorkspaceEntry) Validate() error {
	if entry == nil || !entry.ID.IsValid() || !entry.WorkspaceID.IsValid() || !entry.AdmissionRevisionID.IsValid() ||
		(!entry.SourceStarterEntryID.IsZero() && !entry.SourceStarterEntryID.IsValid()) || entry.CreatedAt.IsZero() || entry.UpdatedAt.IsZero() || entry.UpdatedAt.Before(entry.CreatedAt) {
		return fmt.Errorf("model: invalid Attempt Workspace entry")
	}
	if normalized, err := NormalizeStarterWorkspacePath(entry.Path); err != nil || normalized != entry.Path {
		return fmt.Errorf("model: invalid Attempt Workspace entry path")
	}
	switch entry.Kind {
	case StarterWorkspaceEntryFile:
		if !entry.CurrentObjectID.IsValid() {
			return fmt.Errorf("model: Attempt Workspace file requires current object")
		}
	case StarterWorkspaceEntryDirectory:
		if !entry.CurrentObjectID.IsZero() {
			return fmt.Errorf("model: Attempt Workspace directory cannot have content")
		}
	default:
		return fmt.Errorf("model: invalid Attempt Workspace entry kind")
	}
	return nil
}
