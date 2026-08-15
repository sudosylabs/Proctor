// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package realtime

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

// RealtimeEvent is a transport-neutral, past-tense application fact published
// after durable commit. Sequence is never assigned here: each owning connection
// stamps sequence at the WebSocket boundary.
//
// JSON field names match the existing cluster publication payload so multi-node
// peers remain wire-compatible while ownership of wire DTOs moves outward.
// Cluster fan-out is always best-effort; there is no durable delivery class.
type RealtimeEvent struct {
	ID       string
	Name     string
	UserID   string
	Action   model.Action
	Resource model.Resource
	Data     json.RawMessage
}

const (
	maxRealtimeEventNameBytes = 128
	maxRealtimeEventDataBytes = 256 << 10
)

type userSettingsChangedData struct {
	Revision      string `json:"revision"`
	FormatVersion int    `json:"format_version"`
	ChangedAt     int64  `json:"changed_at"`
}

// NewUserSettingsChangedEvent constructs the content-free refetch hint sent to
// the owning User's live sessions after a durable settings replacement. The
// source document and all client/session details deliberately remain absent.
func NewUserSettingsChangedEvent(
	userID model.UserID,
	revision model.UserSettingsRevision,
	formatVersion int,
	changedAt time.Time,
) (RealtimeEvent, error) {
	if !userID.IsValid() || !revision.IsValid() || formatVersion <= 0 || changedAt.IsZero() {
		return RealtimeEvent{}, errors.New("User Settings change event requires valid bounded metadata")
	}
	data, err := json.Marshal(userSettingsChangedData{
		Revision: revision.String(), FormatVersion: formatVersion,
		ChangedAt: model.TimeUTC(changedAt).UnixMilli(),
	})
	if err != nil {
		return RealtimeEvent{}, fmt.Errorf("encode User Settings change event: %w", err)
	}
	return RealtimeEvent{
		Name: "user_settings_changed", UserID: userID.String(), Data: data,
	}, nil
}

type examDraftUpdatedData struct {
	ExamID        string `json:"exam_id"`
	DraftRevision int64  `json:"draft_revision"`
}

type examStarterWorkspaceChangedData struct {
	ExamID        string `json:"exam_id"`
	EntryID       string `json:"entry_id"`
	Operation     string `json:"operation"`
	DraftRevision int64  `json:"draft_revision"`
	ChangedAt     string `json:"changed_at"`
}

type examArchivedData struct {
	ExamID       string `json:"exam_id"`
	ExamRevision int64  `json:"exam_revision"`
	ArchivedAt   string `json:"archived_at"`
}

type examManagerChangedData struct {
	ExamID       string `json:"exam_id"`
	UserID       string `json:"user_id"`
	Present      bool   `json:"present"`
	ExamRevision int64  `json:"exam_revision"`
	ChangedAt    string `json:"changed_at"`
}

type examOwnerTransferredData struct {
	ExamID       string `json:"exam_id"`
	OwnerUserID  string `json:"owner_user_id"`
	ExamRevision int64  `json:"exam_revision"`
	ChangedAt    string `json:"changed_at"`
}

type examRevisionPublishedData struct {
	ExamID       string `json:"exam_id"`
	RevisionID   string `json:"revision_id"`
	Number       int64  `json:"number"`
	PolicyDigest string `json:"policy_digest"`
	Kind         string `json:"kind"`
	PublishedAt  string `json:"published_at"`
}

type examSittingChangedData struct {
	ExamID        string `json:"exam_id"`
	ExamSittingID string `json:"exam_sitting_id"`
	State         string `json:"state"`
	Revision      int64  `json:"revision"`
	ChangedAt     string `json:"changed_at"`
}

type examSittingLifecycleChangedData struct {
	ExamID        string `json:"exam_id"`
	ExamSittingID string `json:"exam_sitting_id"`
	State         string `json:"state"`
	Revision      int64  `json:"revision"`
	ReasonCode    string `json:"reason_code"`
	ScheduledEnd  string `json:"scheduled_end_at"`
	ChangedAt     string `json:"changed_at"`
}

type examSittingContentCorrectedData struct {
	ExamID             string `json:"exam_id"`
	ExamSittingID      string `json:"exam_sitting_id"`
	PreviousRevisionID string `json:"previous_revision_id"`
	RevisionID         string `json:"revision_id"`
	SittingRevision    int64  `json:"sitting_revision"`
	EffectiveAt        string `json:"effective_at"`
}

type examAttemptConnectionChangedData struct {
	ExamSittingID string `json:"exam_sitting_id"`
	ExamAttemptID string `json:"exam_attempt_id"`
	CandidateID   string `json:"candidate_user_id"`
	ConnectionID  string `json:"attempt_connection_id"`
	State         string `json:"state"`
	CloseReason   string `json:"close_reason,omitempty"`
	ChangedAt     string `json:"changed_at"`
}

type candidateExamAttemptAccessChangedData struct {
	ExamSittingID string `json:"exam_sitting_id"`
	ExamAttemptID string `json:"exam_attempt_id"`
	State         string `json:"state"`
	ReasonCode    string `json:"reason_code,omitempty"`
	ChangedAt     string `json:"changed_at"`
}

type managerExamAttemptSuspendedData struct {
	ExamSittingID string `json:"exam_sitting_id"`
	ExamAttemptID string `json:"exam_attempt_id"`
	CandidateID   string `json:"candidate_user_id"`
	ConnectionID  string `json:"attempt_connection_id"`
	FlagID        string `json:"integrity_flag_id"`
	SuspensionID  string `json:"suspension_id"`
	Revision      int64  `json:"attempt_revision"`
	ChangedAt     string `json:"changed_at"`
}

type managerExamAttemptReallowedData struct {
	ExamSittingID string `json:"exam_sitting_id"`
	ExamAttemptID string `json:"exam_attempt_id"`
	CandidateID   string `json:"candidate_user_id"`
	SuspensionID  string `json:"suspension_id"`
	Revision      int64  `json:"attempt_revision"`
	ChangedAt     string `json:"changed_at"`
}

type candidateExamAttemptWorkspaceChangedData struct {
	ExamSittingID string `json:"exam_sitting_id"`
	ExamAttemptID string `json:"exam_attempt_id"`
	EntryID       string `json:"entry_id"`
	Operation     string `json:"operation"`
	Cursor        int64  `json:"workspace_cursor"`
	ChangedAt     string `json:"changed_at"`
}

type candidateExamAttemptFocusLossWarningData struct {
	ExamSittingID string `json:"exam_sitting_id"`
	ExamAttemptID string `json:"exam_attempt_id"`
	WarningCode   string `json:"warning_code"`
	ChangedAt     string `json:"changed_at"`
}

type managerExamAttemptIntegrityFlaggedData struct {
	ExamSittingID     string `json:"exam_sitting_id"`
	ExamAttemptID     string `json:"exam_attempt_id"`
	CandidateID       string `json:"candidate_user_id"`
	FlagID            string `json:"integrity_flag_id"`
	PolicyKind        string `json:"policy_kind"`
	Outcome           string `json:"outcome"`
	RetainedEvidence  int    `json:"retained_evidence_count"`
	EvidenceOverflow  int64  `json:"evidence_overflow_count"`
	EvidenceAvailable bool   `json:"evidence_available"`
	ChangedAt         string `json:"changed_at"`
}

type managerExamAttemptSubmittedData struct {
	ExamSittingID   string `json:"exam_sitting_id"`
	ExamAttemptID   string `json:"exam_attempt_id"`
	CandidateID     string `json:"candidate_user_id"`
	SubmissionID    string `json:"submission_id"`
	State           string `json:"state"`
	WorkspaceCursor int64  `json:"workspace_cursor"`
	ManifestDigest  string `json:"manifest_digest"`
	SubmittedAt     string `json:"submitted_at"`
}

type candidateExamAttemptSubmittedData struct {
	ExamSittingID   string `json:"exam_sitting_id"`
	ExamAttemptID   string `json:"exam_attempt_id"`
	SubmissionID    string `json:"submission_id"`
	State           string `json:"state"`
	WorkspaceCursor int64  `json:"workspace_cursor"`
	ManifestDigest  string `json:"manifest_digest"`
	SubmittedAt     string `json:"submitted_at"`
}

type managerExamIntegrityReviewChangedData struct {
	SubmissionID  string `json:"submission_id"`
	ExamAttemptID string `json:"exam_attempt_id"`
	CandidateID   string `json:"candidate_user_id"`
	ReviewID      string `json:"submission_review_id"`
	ReviewState   string `json:"review_state"`
	Revision      int64  `json:"review_revision"`
	ReleaseState  string `json:"release_state"`
	ChangedAt     string `json:"changed_at"`
}

type managerExamIntegrityDiscrepancyData struct {
	SubmissionID  string `json:"submission_id"`
	ExamSittingID string `json:"exam_sitting_id"`
	ExamAttemptID string `json:"exam_attempt_id"`
	CandidateID   string `json:"candidate_user_id"`
	DiscrepancyID string `json:"integrity_discrepancy_id"`
	RecordedAt    string `json:"recorded_at"`
}

func NewExamIntegrityDiscrepancyRecordedEvent(submissionID model.SubmissionID, sittingID model.ExamSittingID,
	attemptID model.ExamAttemptID, candidateID model.UserID, discrepancyID model.IntegrityDiscrepancyID,
	recordedAt time.Time,
) (RealtimeEvent, error) {
	if !submissionID.IsValid() || !sittingID.IsValid() || !attemptID.IsValid() || !candidateID.IsValid() ||
		!discrepancyID.IsValid() || recordedAt.IsZero() {
		return RealtimeEvent{}, errors.New("manager Integrity Discrepancy event requires valid bounded metadata")
	}
	data, err := json.Marshal(managerExamIntegrityDiscrepancyData{SubmissionID: submissionID.String(),
		ExamSittingID: sittingID.String(), ExamAttemptID: attemptID.String(), CandidateID: candidateID.String(),
		DiscrepancyID: discrepancyID.String(), RecordedAt: model.TimeUTC(recordedAt).Format(time.RFC3339Nano)})
	if err != nil {
		return RealtimeEvent{}, fmt.Errorf("encode manager Integrity Discrepancy event: %w", err)
	}
	return RealtimeEvent{Name: "exam_integrity_discrepancy_recorded", Action: model.ActionSubmissionView,
		Resource: model.Resource{Type: model.ResourceSubmission, ID: submissionID.String()}, Data: data}, nil
}

type candidateStudentResultReleasedData struct {
	SubmissionID  string `json:"submission_id"`
	ExamAttemptID string `json:"exam_attempt_id"`
	ReviewID      string `json:"submission_review_id"`
	ReviewState   string `json:"review_state"`
	Revision      int64  `json:"review_revision"`
	ReleaseState  string `json:"release_state"`
	ChangedAt     string `json:"changed_at"`
}

// ExamIntegrityReviewEventFact is the complete bounded lifecycle fact shared
// by manager Review events and the candidate release notification. It excludes
// decisions, rationale, evidence, private notes, and student remarks.
type ExamIntegrityReviewEventFact struct {
	SubmissionID model.SubmissionID
	AttemptID    model.ExamAttemptID
	CandidateID  model.UserID
	ReviewID     model.SubmissionReviewID
	State        model.SubmissionReviewState
	Revision     int64
	ReleaseState model.SubmissionReviewReleaseState
	ChangedAt    time.Time
}

func NewExamIntegrityReviewChangedEvent(fact ExamIntegrityReviewEventFact) (RealtimeEvent, error) {
	return newManagerExamIntegrityReviewEvent("exam_integrity_review_changed", fact)
}

func NewExamIntegrityReviewFinalizedEvent(fact ExamIntegrityReviewEventFact) (RealtimeEvent, error) {
	if fact.State != model.SubmissionReviewFinalized || fact.ReleaseState != model.SubmissionReviewWithheld {
		return RealtimeEvent{}, errors.New("finalized Integrity Review event requires a withheld finalized Review")
	}
	return newManagerExamIntegrityReviewEvent("exam_integrity_review_finalized", fact)
}

func NewExamStudentResultReleasedEvent(fact ExamIntegrityReviewEventFact) (RealtimeEvent, error) {
	if fact.State != model.SubmissionReviewFinalized || fact.ReleaseState != model.SubmissionReviewReleased {
		return RealtimeEvent{}, errors.New("released Student Result event requires a released finalized Review")
	}
	return newManagerExamIntegrityReviewEvent("exam_student_result_released", fact)
}

func NewCandidateStudentResultReleasedEvent(fact ExamIntegrityReviewEventFact) (RealtimeEvent, error) {
	if !fact.SubmissionID.IsValid() || !fact.AttemptID.IsValid() || !fact.CandidateID.IsValid() || !fact.ReviewID.IsValid() ||
		fact.State != model.SubmissionReviewFinalized || fact.ReleaseState != model.SubmissionReviewReleased ||
		fact.Revision < 1 || fact.ChangedAt.IsZero() {
		return RealtimeEvent{}, errors.New("candidate Student Result event requires valid released Review metadata")
	}
	data, err := json.Marshal(candidateStudentResultReleasedData{SubmissionID: fact.SubmissionID.String(),
		ExamAttemptID: fact.AttemptID.String(), ReviewID: fact.ReviewID.String(), ReviewState: string(fact.State), Revision: fact.Revision,
		ReleaseState: string(fact.ReleaseState), ChangedAt: model.TimeUTC(fact.ChangedAt).Format(time.RFC3339Nano)})
	if err != nil {
		return RealtimeEvent{}, fmt.Errorf("encode candidate Student Result event: %w", err)
	}
	return RealtimeEvent{Name: "exam_student_result_released", UserID: fact.CandidateID.String(), Data: data}, nil
}

func newManagerExamIntegrityReviewEvent(name string, fact ExamIntegrityReviewEventFact) (RealtimeEvent, error) {
	if !fact.SubmissionID.IsValid() || !fact.AttemptID.IsValid() || !fact.CandidateID.IsValid() || !fact.ReviewID.IsValid() ||
		fact.Revision < 1 || fact.ChangedAt.IsZero() ||
		(fact.State != model.SubmissionReviewDraft && fact.State != model.SubmissionReviewFinalized) ||
		(fact.ReleaseState != model.SubmissionReviewWithheld && fact.ReleaseState != model.SubmissionReviewReleased) {
		return RealtimeEvent{}, errors.New("manager Integrity Review event requires valid bounded metadata")
	}
	data, err := json.Marshal(managerExamIntegrityReviewChangedData{SubmissionID: fact.SubmissionID.String(),
		ExamAttemptID: fact.AttemptID.String(), CandidateID: fact.CandidateID.String(), ReviewID: fact.ReviewID.String(),
		ReviewState: string(fact.State), Revision: fact.Revision, ReleaseState: string(fact.ReleaseState),
		ChangedAt: model.TimeUTC(fact.ChangedAt).Format(time.RFC3339Nano)})
	if err != nil {
		return RealtimeEvent{}, fmt.Errorf("encode manager Integrity Review event: %w", err)
	}
	return RealtimeEvent{Name: name, Action: model.ActionSubmissionView,
		Resource: model.Resource{Type: model.ResourceSubmission, ID: fact.SubmissionID.String()}, Data: data}, nil
}

func NewExamAttemptSubmittedEvent(sittingID model.ExamSittingID, attemptID model.ExamAttemptID,
	candidateID model.UserID, submissionID model.SubmissionID, workspaceCursor int64, manifestDigest string,
	submittedAt time.Time,
) (RealtimeEvent, error) {
	if !sittingID.IsValid() || !attemptID.IsValid() || !candidateID.IsValid() || !submissionID.IsValid() ||
		workspaceCursor < 0 || !validEventSHA256(manifestDigest) || submittedAt.IsZero() {
		return RealtimeEvent{}, errors.New("manager Exam Attempt submitted event requires valid bounded metadata")
	}
	data, err := json.Marshal(managerExamAttemptSubmittedData{ExamSittingID: sittingID.String(),
		ExamAttemptID: attemptID.String(), CandidateID: candidateID.String(), SubmissionID: submissionID.String(),
		State: string(model.ExamAttemptSubmitted), WorkspaceCursor: workspaceCursor, ManifestDigest: manifestDigest,
		SubmittedAt: model.TimeUTC(submittedAt).Format(time.RFC3339Nano)})
	if err != nil {
		return RealtimeEvent{}, err
	}
	return RealtimeEvent{Name: "exam_attempt_submitted", Action: model.ActionExamSittingView,
		Resource: model.Resource{Type: model.ResourceExamSitting, ID: sittingID.String()}, Data: data}, nil
}

func NewCandidateExamAttemptSubmittedEvent(sittingID model.ExamSittingID, attemptID model.ExamAttemptID,
	candidateID model.UserID, submissionID model.SubmissionID, workspaceCursor int64, manifestDigest string,
	submittedAt time.Time,
) (RealtimeEvent, error) {
	if !sittingID.IsValid() || !attemptID.IsValid() || !candidateID.IsValid() || !submissionID.IsValid() ||
		workspaceCursor < 0 || !validEventSHA256(manifestDigest) || submittedAt.IsZero() {
		return RealtimeEvent{}, errors.New("candidate Exam Attempt submitted event requires valid bounded metadata")
	}
	data, err := json.Marshal(candidateExamAttemptSubmittedData{ExamSittingID: sittingID.String(),
		ExamAttemptID: attemptID.String(), SubmissionID: submissionID.String(), State: string(model.ExamAttemptSubmitted),
		WorkspaceCursor: workspaceCursor, ManifestDigest: manifestDigest,
		SubmittedAt: model.TimeUTC(submittedAt).Format(time.RFC3339Nano)})
	if err != nil {
		return RealtimeEvent{}, err
	}
	return RealtimeEvent{Name: "exam_attempt_submitted", UserID: candidateID.String(), Action: model.ActionExamSittingParticipate,
		Resource: model.Resource{Type: model.ResourceExamSitting, ID: sittingID.String()}, Data: data}, nil
}

func validEventSHA256(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}

func NewCandidateExamAttemptFocusLossWarningEvent(sittingID model.ExamSittingID, attemptID model.ExamAttemptID,
	candidateID model.UserID, changedAt time.Time,
) (RealtimeEvent, error) {
	if !sittingID.IsValid() || !attemptID.IsValid() || !candidateID.IsValid() || changedAt.IsZero() {
		return RealtimeEvent{}, errors.New("candidate Exam Attempt Focus Loss warning requires valid bounded metadata")
	}
	data, err := json.Marshal(candidateExamAttemptFocusLossWarningData{ExamSittingID: sittingID.String(),
		ExamAttemptID: attemptID.String(), WarningCode: "focus_loss_policy_warning",
		ChangedAt: model.TimeUTC(changedAt).Format(time.RFC3339Nano)})
	if err != nil {
		return RealtimeEvent{}, err
	}
	return RealtimeEvent{Name: "exam_attempt_focus_warning", UserID: candidateID.String(),
		Action:   model.ActionExamSittingParticipate,
		Resource: model.Resource{Type: model.ResourceExamSitting, ID: sittingID.String()}, Data: data}, nil
}

func NewExamAttemptIntegrityFlaggedEvent(sittingID model.ExamSittingID, attemptID model.ExamAttemptID,
	candidateID model.UserID, flagID model.IntegrityFlagID, outcome model.IntegrityThresholdOutcome,
	retainedEvidence int, evidenceOverflow int64, changedAt time.Time,
) (RealtimeEvent, error) {
	if !sittingID.IsValid() || !attemptID.IsValid() || !candidateID.IsValid() || !flagID.IsValid() ||
		retainedEvidence < 0 || retainedEvidence > model.FocusLossMaximumEvidenceEpisodes || evidenceOverflow < 0 ||
		(retainedEvidence == 0 && evidenceOverflow == 0) || changedAt.IsZero() {
		return RealtimeEvent{}, errors.New("manager Exam Attempt integrity Flag event requires valid bounded metadata")
	}
	switch outcome {
	case model.IntegrityOutcomeFlag, model.IntegrityOutcomeFlagAndWarn, model.IntegrityOutcomeFlagAndSuspend:
	default:
		return RealtimeEvent{}, errors.New("manager Exam Attempt integrity Flag event requires a valid outcome")
	}
	data, err := json.Marshal(managerExamAttemptIntegrityFlaggedData{ExamSittingID: sittingID.String(),
		ExamAttemptID: attemptID.String(), CandidateID: candidateID.String(), FlagID: flagID.String(),
		PolicyKind: string(model.IntegrityPolicyFocusLoss), Outcome: string(outcome), RetainedEvidence: retainedEvidence,
		EvidenceOverflow: evidenceOverflow, EvidenceAvailable: true,
		ChangedAt: model.TimeUTC(changedAt).Format(time.RFC3339Nano)})
	if err != nil {
		return RealtimeEvent{}, err
	}
	return RealtimeEvent{Name: "exam_attempt_integrity_flagged", Action: model.ActionExamSittingView,
		Resource: model.Resource{Type: model.ResourceExamSitting, ID: sittingID.String()}, Data: data}, nil
}

// NewCandidateExamAttemptWorkspaceChangedEvent publishes only a targeted
// refetch hint. Logical paths, content metadata, object selectors, continuity
// selectors, and mutation bodies remain confined to protected HTTP responses.
func NewCandidateExamAttemptWorkspaceChangedEvent(sittingID model.ExamSittingID, attemptID model.ExamAttemptID,
	candidateID model.UserID, entryID model.AttemptWorkspaceEntryID, operation model.AttemptWorkspaceMutationKind,
	cursor int64, changedAt time.Time,
) (RealtimeEvent, error) {
	if !sittingID.IsValid() || !attemptID.IsValid() || !candidateID.IsValid() || !entryID.IsValid() ||
		!operation.IsValid() || cursor < 1 || changedAt.IsZero() {
		return RealtimeEvent{}, errors.New("candidate Exam Attempt Workspace event requires valid bounded metadata")
	}
	data, err := json.Marshal(candidateExamAttemptWorkspaceChangedData{ExamSittingID: sittingID.String(),
		ExamAttemptID: attemptID.String(), EntryID: entryID.String(), Operation: string(operation), Cursor: cursor,
		ChangedAt: model.TimeUTC(changedAt).Format(time.RFC3339Nano)})
	if err != nil {
		return RealtimeEvent{}, err
	}
	return RealtimeEvent{Name: "exam_attempt_workspace_changed", UserID: candidateID.String(),
		Action: model.ActionExamSittingParticipate, Resource: model.Resource{Type: model.ResourceExamSitting, ID: sittingID.String()},
		Data: data}, nil
}

func NewCandidateExamAttemptSuspendedEvent(sittingID model.ExamSittingID, attemptID model.ExamAttemptID,
	candidateID model.UserID, reason model.AttemptSuspensionCandidateReason, changedAt time.Time,
) (RealtimeEvent, error) {
	if reason != model.AttemptSuspensionCandidateReasonSecureContinuityLost &&
		reason != model.AttemptSuspensionCandidateReasonFocusLossPolicy {
		return RealtimeEvent{}, errors.New("candidate Exam Attempt suspension event requires a safe reason")
	}
	return newCandidateExamAttemptAccessEvent("exam_attempt_access_suspended", sittingID, attemptID, candidateID,
		model.ExamAttemptSuspended, string(reason), changedAt)
}

func NewCandidateExamAttemptReallowedEvent(sittingID model.ExamSittingID, attemptID model.ExamAttemptID,
	candidateID model.UserID, changedAt time.Time,
) (RealtimeEvent, error) {
	return newCandidateExamAttemptAccessEvent("exam_attempt_access_reallowed", sittingID, attemptID, candidateID,
		model.ExamAttemptActive, "", changedAt)
}

func newCandidateExamAttemptAccessEvent(name string, sittingID model.ExamSittingID, attemptID model.ExamAttemptID,
	candidateID model.UserID, state model.ExamAttemptState, reason string, changedAt time.Time,
) (RealtimeEvent, error) {
	if !sittingID.IsValid() || !attemptID.IsValid() || !candidateID.IsValid() || changedAt.IsZero() ||
		(state != model.ExamAttemptSuspended && state != model.ExamAttemptActive) {
		return RealtimeEvent{}, errors.New("candidate Exam Attempt access event requires valid bounded metadata")
	}
	data, err := json.Marshal(candidateExamAttemptAccessChangedData{ExamSittingID: sittingID.String(), ExamAttemptID: attemptID.String(),
		State: string(state), ReasonCode: reason, ChangedAt: model.TimeUTC(changedAt).Format(time.RFC3339Nano)})
	if err != nil {
		return RealtimeEvent{}, err
	}
	return RealtimeEvent{Name: name, UserID: candidateID.String(), Action: model.ActionExamSittingParticipate,
		Resource: model.Resource{Type: model.ResourceExamSitting, ID: sittingID.String()}, Data: data}, nil
}

func NewExamAttemptSuspendedEvent(sittingID model.ExamSittingID, attemptID model.ExamAttemptID, candidateID model.UserID,
	connectionID model.AttemptConnectionID, flagID model.IntegrityFlagID, suspensionID model.AttemptSuspensionID,
	revision int64, changedAt time.Time,
) (RealtimeEvent, error) {
	if !sittingID.IsValid() || !attemptID.IsValid() || !candidateID.IsValid() || !connectionID.IsValid() ||
		!flagID.IsValid() || !suspensionID.IsValid() || revision < 1 || changedAt.IsZero() {
		return RealtimeEvent{}, errors.New("manager Exam Attempt suspension event requires valid bounded metadata")
	}
	data, err := json.Marshal(managerExamAttemptSuspendedData{ExamSittingID: sittingID.String(), ExamAttemptID: attemptID.String(),
		CandidateID: candidateID.String(), ConnectionID: connectionID.String(), FlagID: flagID.String(),
		SuspensionID: suspensionID.String(), Revision: revision, ChangedAt: model.TimeUTC(changedAt).Format(time.RFC3339Nano)})
	if err != nil {
		return RealtimeEvent{}, err
	}
	return RealtimeEvent{Name: "exam_attempt_suspended", Action: model.ActionExamSittingView,
		Resource: model.Resource{Type: model.ResourceExamSitting, ID: sittingID.String()}, Data: data}, nil
}

func NewExamAttemptReallowedEvent(sittingID model.ExamSittingID, attemptID model.ExamAttemptID, candidateID model.UserID,
	suspensionID model.AttemptSuspensionID, revision int64, changedAt time.Time,
) (RealtimeEvent, error) {
	if !sittingID.IsValid() || !attemptID.IsValid() || !candidateID.IsValid() || !suspensionID.IsValid() || revision < 1 || changedAt.IsZero() {
		return RealtimeEvent{}, errors.New("manager Exam Attempt re-allow event requires valid bounded metadata")
	}
	data, err := json.Marshal(managerExamAttemptReallowedData{ExamSittingID: sittingID.String(), ExamAttemptID: attemptID.String(),
		CandidateID: candidateID.String(), SuspensionID: suspensionID.String(), Revision: revision,
		ChangedAt: model.TimeUTC(changedAt).Format(time.RFC3339Nano)})
	if err != nil {
		return RealtimeEvent{}, err
	}
	return RealtimeEvent{Name: "exam_attempt_reallowed", Action: model.ActionExamSittingView,
		Resource: model.Resource{Type: model.ResourceExamSitting, ID: sittingID.String()}, Data: data}, nil
}

func NewExamAttemptConnectionOpenedEvent(sittingID model.ExamSittingID, attemptID model.ExamAttemptID,
	candidateID model.UserID, connectionID model.AttemptConnectionID, openedAt time.Time,
) (RealtimeEvent, error) {
	return newExamAttemptConnectionChangedEvent("exam_attempt_connection_opened", sittingID, attemptID, candidateID,
		connectionID, model.AttemptConnectionOpen, "", openedAt)
}

func NewExamAttemptConnectionClosedEvent(sittingID model.ExamSittingID, attemptID model.ExamAttemptID,
	candidateID model.UserID, connectionID model.AttemptConnectionID, reason model.AttemptConnectionCloseReason, closedAt time.Time,
) (RealtimeEvent, error) {
	if !reason.IsValid() {
		return RealtimeEvent{}, errors.New("Exam Attempt Connection closed event requires a valid reason")
	}
	return newExamAttemptConnectionChangedEvent("exam_attempt_connection_closed", sittingID, attemptID, candidateID,
		connectionID, model.AttemptConnectionClosed, reason, closedAt)
}

func newExamAttemptConnectionChangedEvent(name string, sittingID model.ExamSittingID, attemptID model.ExamAttemptID,
	candidateID model.UserID, connectionID model.AttemptConnectionID, state model.AttemptConnectionState,
	reason model.AttemptConnectionCloseReason, changedAt time.Time,
) (RealtimeEvent, error) {
	if !sittingID.IsValid() || !attemptID.IsValid() || !candidateID.IsValid() || !connectionID.IsValid() ||
		changedAt.IsZero() || (state != model.AttemptConnectionOpen && state != model.AttemptConnectionClosed) {
		return RealtimeEvent{}, errors.New("Exam Attempt Connection event requires valid bounded metadata")
	}
	data, err := json.Marshal(examAttemptConnectionChangedData{ExamSittingID: sittingID.String(), ExamAttemptID: attemptID.String(),
		CandidateID: candidateID.String(), ConnectionID: connectionID.String(), State: string(state), CloseReason: string(reason),
		ChangedAt: model.TimeUTC(changedAt).Format(time.RFC3339Nano)})
	if err != nil {
		return RealtimeEvent{}, fmt.Errorf("encode Exam Attempt Connection event: %w", err)
	}
	return RealtimeEvent{Name: name, Action: model.ActionExamSittingView,
		Resource: model.Resource{Type: model.ResourceExamSitting, ID: sittingID.String()}, Data: data}, nil
}

// NewExamSittingContentCorrectedEvent constructs the content-free fact that
// tells authorized Sitting subscribers to refetch authoritative presentation.
func NewExamSittingContentCorrectedEvent(examID model.ExamID, sittingID model.ExamSittingID,
	previousRevisionID, revisionID model.ExamRevisionID, sittingRevision int64, effectiveAt time.Time,
) (RealtimeEvent, error) {
	return newExamSittingContentCorrectedEvent(model.ActionExamSittingView, examID, sittingID,
		previousRevisionID, revisionID, sittingRevision, effectiveAt)
}

// NewCandidateExamSittingContentCorrectedEvent constructs the independently
// authorized candidate fact for the same committed correction. Candidate and
// manager subscriptions deliberately use different actions so later manager
// payloads cannot become candidate-visible by accident.
func NewCandidateExamSittingContentCorrectedEvent(examID model.ExamID, sittingID model.ExamSittingID,
	previousRevisionID, revisionID model.ExamRevisionID, sittingRevision int64, effectiveAt time.Time,
) (RealtimeEvent, error) {
	return newExamSittingContentCorrectedEvent(model.ActionExamSittingParticipate, examID, sittingID,
		previousRevisionID, revisionID, sittingRevision, effectiveAt)
}

func newExamSittingContentCorrectedEvent(action model.Action, examID model.ExamID, sittingID model.ExamSittingID,
	previousRevisionID, revisionID model.ExamRevisionID, sittingRevision int64, effectiveAt time.Time,
) (RealtimeEvent, error) {
	if !examID.IsValid() || !sittingID.IsValid() || !previousRevisionID.IsValid() || !revisionID.IsValid() ||
		previousRevisionID == revisionID || sittingRevision < 1 || effectiveAt.IsZero() ||
		(action != model.ActionExamSittingView && action != model.ActionExamSittingParticipate) {
		return RealtimeEvent{}, errors.New("Exam Sitting correction event requires valid bounded metadata")
	}
	data, err := json.Marshal(examSittingContentCorrectedData{
		ExamID: examID.String(), ExamSittingID: sittingID.String(), PreviousRevisionID: previousRevisionID.String(),
		RevisionID: revisionID.String(), SittingRevision: sittingRevision, EffectiveAt: model.TimeUTC(effectiveAt).Format(time.RFC3339Nano),
	})
	if err != nil {
		return RealtimeEvent{}, fmt.Errorf("encode Exam Sitting correction event: %w", err)
	}
	return RealtimeEvent{Name: "exam_sitting_content_corrected", Action: action,
		Resource: model.Resource{Type: model.ResourceExamSitting, ID: sittingID.String()}, Data: data}, nil
}

// NewExamSittingLifecycleChangedEvent constructs the safe authoritative fact
// emitted after one committed lifecycle transition or deadline extension.
func NewExamSittingLifecycleChangedEvent(examID model.ExamID, sittingID model.ExamSittingID, state model.ExamSittingState,
	revision int64, reasonCode string, scheduledEndAt, changedAt time.Time,
) (RealtimeEvent, error) {
	return newExamSittingLifecycleChangedEvent(model.ActionExamSittingView, examID, sittingID, state,
		revision, reasonCode, scheduledEndAt, changedAt)
}

// NewCandidateExamSittingLifecycleChangedEvent constructs the candidate-safe
// lifecycle fact under the relationship-only participation action.
func NewCandidateExamSittingLifecycleChangedEvent(examID model.ExamID, sittingID model.ExamSittingID, state model.ExamSittingState,
	revision int64, reasonCode string, scheduledEndAt, changedAt time.Time,
) (RealtimeEvent, error) {
	return newExamSittingLifecycleChangedEvent(model.ActionExamSittingParticipate, examID, sittingID, state,
		revision, reasonCode, scheduledEndAt, changedAt)
}

func newExamSittingLifecycleChangedEvent(action model.Action, examID model.ExamID, sittingID model.ExamSittingID, state model.ExamSittingState,
	revision int64, reasonCode string, scheduledEndAt, changedAt time.Time,
) (RealtimeEvent, error) {
	if !examID.IsValid() || !sittingID.IsValid() || !state.IsValid() || revision < 1 ||
		!validExamSittingLifecycleEventReason(reasonCode) || scheduledEndAt.IsZero() || changedAt.IsZero() ||
		(action != model.ActionExamSittingView && action != model.ActionExamSittingParticipate) {
		return RealtimeEvent{}, errors.New("Exam Sitting lifecycle event requires valid bounded metadata")
	}
	data, err := json.Marshal(examSittingLifecycleChangedData{
		ExamID: examID.String(), ExamSittingID: sittingID.String(), State: string(state), Revision: revision,
		ReasonCode: reasonCode, ScheduledEnd: model.TimeUTC(scheduledEndAt).Format(time.RFC3339Nano),
		ChangedAt: model.TimeUTC(changedAt).Format(time.RFC3339Nano),
	})
	if err != nil {
		return RealtimeEvent{}, fmt.Errorf("encode Exam Sitting lifecycle event: %w", err)
	}
	return RealtimeEvent{Name: "exam_sitting_lifecycle_changed", Action: action,
		Resource: model.Resource{Type: model.ResourceExamSitting, ID: sittingID.String()}, Data: data}, nil
}

func validExamSittingLifecycleEventReason(value string) bool {
	switch value {
	case "scheduled_start_reached", "manager_paused", "manager_resumed", "manager_extended", "manager_closed",
		"scheduled_end_reached", "schedule_elapsed", "academic_structure_invalid", "closed_no_attempts", "sealing_completed":
		return true
	default:
		return false
	}
}

func NewExamSittingScheduledEvent(examID model.ExamID, sittingID model.ExamSittingID, state model.ExamSittingState, revision int64, changedAt time.Time) (RealtimeEvent, error) {
	if state != model.ExamSittingScheduled {
		return RealtimeEvent{}, errors.New("scheduled Exam Sitting event requires Scheduled state")
	}
	return newExamSittingChangedEvent("exam_sitting_scheduled", examID, sittingID, state, revision, changedAt)
}

func NewExamSittingScheduleUpdatedEvent(examID model.ExamID, sittingID model.ExamSittingID, state model.ExamSittingState, revision int64, changedAt time.Time) (RealtimeEvent, error) {
	if state != model.ExamSittingScheduled {
		return RealtimeEvent{}, errors.New("Exam Sitting schedule update event requires Scheduled state")
	}
	return newExamSittingChangedEvent("exam_sitting_schedule_updated", examID, sittingID, state, revision, changedAt)
}

func NewExamSittingCanceledEvent(examID model.ExamID, sittingID model.ExamSittingID, state model.ExamSittingState, revision int64, changedAt time.Time) (RealtimeEvent, error) {
	if state != model.ExamSittingCanceled {
		return RealtimeEvent{}, errors.New("canceled Exam Sitting event requires Canceled state")
	}
	return newExamSittingChangedEvent("exam_sitting_canceled", examID, sittingID, state, revision, changedAt)
}

func newExamSittingChangedEvent(name string, examID model.ExamID, sittingID model.ExamSittingID, state model.ExamSittingState, revision int64, changedAt time.Time) (RealtimeEvent, error) {
	if !examID.IsValid() || !sittingID.IsValid() || !state.IsValid() || revision < 1 || changedAt.IsZero() {
		return RealtimeEvent{}, errors.New("Exam Sitting event requires valid bounded metadata")
	}
	data, err := json.Marshal(examSittingChangedData{ExamID: examID.String(), ExamSittingID: sittingID.String(), State: string(state),
		Revision: revision, ChangedAt: model.TimeUTC(changedAt).Format(time.RFC3339Nano)})
	if err != nil {
		return RealtimeEvent{}, fmt.Errorf("encode Exam Sitting event: %w", err)
	}
	return RealtimeEvent{Name: name, Action: model.ActionExamSittingView,
		Resource: model.Resource{Type: model.ResourceExamSitting, ID: sittingID.String()}, Data: data}, nil
}

func NewExamRevisionPublishedEvent(revisionID model.ExamRevisionID, examID model.ExamID, number int64, policyDigest string, kind model.ExamRevisionPublicationKind, publishedAt time.Time) (RealtimeEvent, error) {
	if !revisionID.IsValid() || !examID.IsValid() || number < 1 || len(policyDigest) != 64 || !kind.IsValid() || publishedAt.IsZero() {
		return RealtimeEvent{}, errors.New("Exam Revision published event requires valid bounded metadata")
	}
	data, err := json.Marshal(examRevisionPublishedData{ExamID: examID.String(), RevisionID: revisionID.String(), Number: number,
		PolicyDigest: policyDigest, Kind: string(kind), PublishedAt: model.TimeUTC(publishedAt).Format(time.RFC3339Nano)})
	if err != nil {
		return RealtimeEvent{}, fmt.Errorf("encode Exam Revision published event: %w", err)
	}
	return RealtimeEvent{Name: "exam_revision_published", Action: model.ActionExamView,
		Resource: model.Resource{Type: model.ResourceExam, ID: examID.String()}, Data: data}, nil
}

// NewExamCreatedEvent constructs the stable, content-free authoring event.
func NewExamCreatedEvent(examID model.ExamID) (RealtimeEvent, error) {
	if !examID.IsValid() {
		return RealtimeEvent{}, errors.New("exam created event requires a valid Exam ID")
	}
	return RealtimeEvent{Name: "exam_created", Action: model.ActionExamView,
		Resource: model.Resource{Type: model.ResourceExam, ID: examID.String()}}, nil
}

// NewExamDraftUpdatedEvent owns the bounded wire projection for a Draft text
// or policy change. Authored content and policy values are deliberately absent.
func NewExamDraftUpdatedEvent(examID model.ExamID, revision int64) (RealtimeEvent, error) {
	if !examID.IsValid() || revision < 1 {
		return RealtimeEvent{}, errors.New("exam Draft update event requires a valid identity and revision")
	}
	data, err := json.Marshal(examDraftUpdatedData{ExamID: examID.String(), DraftRevision: revision})
	if err != nil {
		return RealtimeEvent{}, fmt.Errorf("encode exam Draft update event: %w", err)
	}
	return RealtimeEvent{Name: "exam_draft_updated", Action: model.ActionExamView,
		Resource: model.Resource{Type: model.ResourceExam, ID: examID.String()}, Data: data}, nil
}

// NewExamStarterWorkspaceChangedEvent constructs the content-free fact emitted
// after one Starter Workspace hierarchy/content mutation commits. Logical
// paths, opaque object identities, checksums, and authored bytes are absent.
func NewExamStarterWorkspaceChangedEvent(examID model.ExamID, entryID model.StarterWorkspaceEntryID, revision int64, operation string, changedAt time.Time) (RealtimeEvent, error) {
	if !examID.IsValid() || !entryID.IsValid() || revision < 1 || !validExamStarterWorkspaceOperation(operation) || changedAt.IsZero() {
		return RealtimeEvent{}, errors.New("Starter Workspace change event requires valid identities, operation, revision, and time")
	}
	data, err := json.Marshal(examStarterWorkspaceChangedData{ExamID: examID.String(), EntryID: entryID.String(), Operation: operation,
		DraftRevision: revision, ChangedAt: model.TimeUTC(changedAt).Format(time.RFC3339Nano)})
	if err != nil {
		return RealtimeEvent{}, fmt.Errorf("encode Starter Workspace change event: %w", err)
	}
	return RealtimeEvent{Name: "exam_starter_workspace_changed", Action: model.ActionExamView,
		Resource: model.Resource{Type: model.ResourceExam, ID: examID.String()}, Data: data}, nil
}

func validExamStarterWorkspaceOperation(operation string) bool {
	switch operation {
	case "directory_created", "file_created", "entry_moved", "file_replaced", "entry_removed":
		return true
	default:
		return false
	}
}

// NewExamArchivedEvent constructs the content-free Exam lifecycle event.
func NewExamArchivedEvent(examID model.ExamID, revision int64, archivedAt time.Time) (RealtimeEvent, error) {
	if !examID.IsValid() || revision < 1 || archivedAt.IsZero() {
		return RealtimeEvent{}, errors.New("exam archived event requires a valid identity, revision, and time")
	}
	data, err := json.Marshal(examArchivedData{ExamID: examID.String(), ExamRevision: revision,
		ArchivedAt: model.TimeUTC(archivedAt).Format(time.RFC3339Nano)})
	if err != nil {
		return RealtimeEvent{}, fmt.Errorf("encode exam archived event: %w", err)
	}
	return RealtimeEvent{Name: "exam_archived", Action: model.ActionExamView,
		Resource: model.Resource{Type: model.ResourceExam, ID: examID.String()}, Data: data}, nil
}

func NewExamManagerChangedEvent(examID model.ExamID, userID model.UserID, present bool, revision int64, changedAt time.Time) (RealtimeEvent, error) {
	if !examID.IsValid() || !userID.IsValid() || revision < 1 || changedAt.IsZero() {
		return RealtimeEvent{}, errors.New("Exam Manager change event requires valid identities, revision, and time")
	}
	data, err := json.Marshal(examManagerChangedData{ExamID: examID.String(), UserID: userID.String(), Present: present,
		ExamRevision: revision, ChangedAt: model.TimeUTC(changedAt).Format(time.RFC3339Nano)})
	if err != nil {
		return RealtimeEvent{}, fmt.Errorf("encode Exam Manager change event: %w", err)
	}
	return RealtimeEvent{Name: "exam_manager_changed", Action: model.ActionExamView,
		Resource: model.Resource{Type: model.ResourceExam, ID: examID.String()}, Data: data}, nil
}

func NewExamOwnerTransferredEvent(examID model.ExamID, ownerID model.UserID, revision int64, changedAt time.Time) (RealtimeEvent, error) {
	if !examID.IsValid() || !ownerID.IsValid() || revision < 1 || changedAt.IsZero() {
		return RealtimeEvent{}, errors.New("Exam owner transfer event requires valid identities, revision, and time")
	}
	data, err := json.Marshal(examOwnerTransferredData{ExamID: examID.String(), OwnerUserID: ownerID.String(),
		ExamRevision: revision, ChangedAt: model.TimeUTC(changedAt).Format(time.RFC3339Nano)})
	if err != nil {
		return RealtimeEvent{}, fmt.Errorf("encode Exam owner transfer event: %w", err)
	}
	return RealtimeEvent{Name: "exam_owner_transferred", Action: model.ActionExamView,
		Resource: model.Resource{Type: model.ResourceExam, ID: examID.String()}, Data: data}, nil
}

// Clone returns a deep copy safe for concurrent local and cluster delivery.
func (e RealtimeEvent) Clone() RealtimeEvent {
	cloned := e
	if e.Data != nil {
		cloned.Data = append(json.RawMessage(nil), e.Data...)
	}
	return cloned
}

// ValidateForPublish checks local publication invariants. It performs no I/O.
func (e RealtimeEvent) ValidateForPublish() error {
	if e.ID != "" && !model.IsValidId(e.ID) {
		return errors.New("realtime event ID is invalid")
	}
	if len(e.Name) == 0 || len(e.Name) > maxRealtimeEventNameBytes ||
		!validRealtimeName(e.Name) {
		return fmt.Errorf("invalid realtime event %q", e.Name)
	}
	if e.UserID != "" && !model.IsValidId(e.UserID) {
		return errors.New("realtime event user ID is invalid")
	}
	if e.Action == "" && e.Resource == (model.Resource{}) {
		if e.UserID == "" {
			return errors.New("realtime event requires a user target or authorized resource")
		}
	} else {
		definition, ok := model.DefinitionForAction(e.Action)
		if !ok || e.Resource.Validate() != nil || definition.ResourceType != e.Resource.Type {
			return errors.New("realtime event authorization target is invalid")
		}
	}
	if len(e.Data) > maxRealtimeEventDataBytes {
		return fmt.Errorf("realtime event data exceeds %d bytes", maxRealtimeEventDataBytes)
	}
	if len(e.Data) != 0 && !json.Valid(e.Data) {
		return errors.New("realtime event data is not valid JSON")
	}
	return nil
}

// ConnectionCloseReason is a transport-neutral reason the local connection
// boundary maps to protocol close codes and text.
type ConnectionCloseReason string

const (
	ConnectionCloseSessionRevoked       ConnectionCloseReason = "session_revoked"
	ConnectionCloseAuthorizationChanged ConnectionCloseReason = "authorization_changed"
)

func validRealtimeName(value string) bool {
	for _, character := range value {
		if (character < 'a' || character > 'z') &&
			(character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') &&
			character != '.' && character != '_' && character != '-' && character != ':' {
			return false
		}
	}
	return true
}
