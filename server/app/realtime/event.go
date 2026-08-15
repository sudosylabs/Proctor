// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package realtime

import (
	"encoding/json"
	"errors"
	"fmt"
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

// NewExamSittingContentCorrectedEvent constructs the content-free fact that
// tells authorized Sitting subscribers to refetch authoritative presentation.
func NewExamSittingContentCorrectedEvent(examID model.ExamID, sittingID model.ExamSittingID,
	previousRevisionID, revisionID model.ExamRevisionID, sittingRevision int64, effectiveAt time.Time,
) (RealtimeEvent, error) {
	if !examID.IsValid() || !sittingID.IsValid() || !previousRevisionID.IsValid() || !revisionID.IsValid() ||
		previousRevisionID == revisionID || sittingRevision < 1 || effectiveAt.IsZero() {
		return RealtimeEvent{}, errors.New("Exam Sitting correction event requires valid bounded metadata")
	}
	data, err := json.Marshal(examSittingContentCorrectedData{
		ExamID: examID.String(), ExamSittingID: sittingID.String(), PreviousRevisionID: previousRevisionID.String(),
		RevisionID: revisionID.String(), SittingRevision: sittingRevision, EffectiveAt: model.TimeUTC(effectiveAt).Format(time.RFC3339Nano),
	})
	if err != nil {
		return RealtimeEvent{}, fmt.Errorf("encode Exam Sitting correction event: %w", err)
	}
	return RealtimeEvent{Name: "exam_sitting_content_corrected", Action: model.ActionExamSittingView,
		Resource: model.Resource{Type: model.ResourceExamSitting, ID: sittingID.String()}, Data: data}, nil
}

// NewExamSittingLifecycleChangedEvent constructs the safe authoritative fact
// emitted after one committed lifecycle transition or deadline extension.
func NewExamSittingLifecycleChangedEvent(examID model.ExamID, sittingID model.ExamSittingID, state model.ExamSittingState,
	revision int64, reasonCode string, scheduledEndAt, changedAt time.Time,
) (RealtimeEvent, error) {
	if !examID.IsValid() || !sittingID.IsValid() || !state.IsValid() || revision < 1 ||
		!validExamSittingLifecycleEventReason(reasonCode) || scheduledEndAt.IsZero() || changedAt.IsZero() {
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
	return RealtimeEvent{Name: "exam_sitting_lifecycle_changed", Action: model.ActionExamSittingView,
		Resource: model.Resource{Type: model.ResourceExamSitting, ID: sittingID.String()}, Data: data}, nil
}

func validExamSittingLifecycleEventReason(value string) bool {
	switch value {
	case "scheduled_start_reached", "manager_paused", "manager_resumed", "manager_extended", "manager_closed",
		"scheduled_end_reached", "schedule_elapsed", "academic_structure_invalid", "closed_no_attempts":
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
