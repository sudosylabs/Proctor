// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package realtime

import (
	"encoding/json"
	"errors"
	"fmt"

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
