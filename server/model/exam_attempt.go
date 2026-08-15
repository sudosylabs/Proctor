// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"fmt"
	"strings"
	"time"
)

const AttemptParticipationInitialLease = 20 * time.Second

type ExamAttemptState string

const (
	ExamAttemptActive    ExamAttemptState = "active"
	ExamAttemptSuspended ExamAttemptState = "suspended"
	ExamAttemptSubmitted ExamAttemptState = "submitted"
)

func (state ExamAttemptState) AllowsCandidateConnection() bool { return state == ExamAttemptActive }
func (state ExamAttemptState) IsTerminal() bool                { return state == ExamAttemptSubmitted }

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
	case ExamAttemptActive, ExamAttemptSuspended:
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

type AttemptParticipationState string

const (
	AttemptParticipationActive AttemptParticipationState = "active"
	AttemptParticipationEnded  AttemptParticipationState = "ended"
)

type AttemptParticipationEndReason string

const (
	AttemptParticipationEndInterrupted   AttemptParticipationEndReason = "interrupted"
	AttemptParticipationEndLeaseExpired  AttemptParticipationEndReason = "lease_expired"
	AttemptParticipationEndKicked        AttemptParticipationEndReason = "kicked"
	AttemptParticipationEndSubmitted     AttemptParticipationEndReason = "submitted"
	AttemptParticipationEndSittingClosed AttemptParticipationEndReason = "sitting_closed"
)

func (reason AttemptParticipationEndReason) isValid() bool {
	switch reason {
	case AttemptParticipationEndInterrupted, AttemptParticipationEndLeaseExpired, AttemptParticipationEndKicked,
		AttemptParticipationEndSubmitted, AttemptParticipationEndSittingClosed:
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

func NewAttemptParticipation(id AttemptParticipationID, attemptID ExamAttemptID, generation int64, credentialHash string, startedAt time.Time) (*AttemptParticipation, error) {
	startedAt = TimeUTC(startedAt)
	participation := &AttemptParticipation{ID: id, AttemptID: attemptID, State: AttemptParticipationActive,
		Generation: generation, ContinuityCredentialHash: credentialHash, StartedAt: startedAt,
		UpdatedAt: startedAt, LeaseExpiresAt: startedAt.Add(AttemptParticipationInitialLease)}
	if err := participation.Validate(); err != nil {
		return nil, err
	}
	return participation, nil
}

func (participation *AttemptParticipation) Validate() error {
	if participation == nil || !participation.ID.IsValid() || !participation.AttemptID.IsValid() || participation.Generation < 1 ||
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
	AttemptConnectionCloseTransport     AttemptConnectionCloseReason = "transport_closed"
	AttemptConnectionCloseInterrupted   AttemptConnectionCloseReason = "interrupted"
	AttemptConnectionCloseLeaseExpired  AttemptConnectionCloseReason = "lease_expired"
	AttemptConnectionCloseKicked        AttemptConnectionCloseReason = "kicked"
	AttemptConnectionCloseSubmitted     AttemptConnectionCloseReason = "submitted"
	AttemptConnectionCloseSittingClosed AttemptConnectionCloseReason = "sitting_closed"
)

// IsValid reports whether reason is one of the closed Connection states owned
// by the domain model.
func (reason AttemptConnectionCloseReason) IsValid() bool {
	switch reason {
	case AttemptConnectionCloseTransport, AttemptConnectionCloseInterrupted, AttemptConnectionCloseLeaseExpired,
		AttemptConnectionCloseKicked, AttemptConnectionCloseSubmitted, AttemptConnectionCloseSittingClosed:
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
	ContentVersion  WorkspaceContentVersion
	MediaType       string
	SizeBytes       int64
	SHA256          string
	CreatedAt       time.Time
}

func NewStarterOriginAttemptWorkspaceObject(id AttemptWorkspaceObjectID, workspaceID ExamAttemptWorkspaceID, starterObjectID StarterWorkspaceObjectID, version WorkspaceContentVersion, mediaType string, size int64, checksum string, at time.Time) (*AttemptWorkspaceObject, error) {
	object := &AttemptWorkspaceObject{ID: id, WorkspaceID: workspaceID, StorageOrigin: AttemptWorkspaceStorageStarter,
		StarterObjectID: starterObjectID, ContentVersion: version, MediaType: strings.TrimSpace(mediaType),
		SizeBytes: size, SHA256: strings.ToLower(checksum), CreatedAt: TimeUTC(at)}
	if err := object.Validate(); err != nil {
		return nil, err
	}
	return object, nil
}

func (object *AttemptWorkspaceObject) Validate() error {
	if object == nil || !object.ID.IsValid() || !object.WorkspaceID.IsValid() || !object.ContentVersion.IsValid() ||
		object.MediaType == "" || strings.TrimSpace(object.MediaType) != object.MediaType || len(object.MediaType) > 255 ||
		object.SizeBytes < 0 || object.SizeBytes > StarterWorkspaceMaximumFileBytes || !validLowerSHA256(object.SHA256) || object.CreatedAt.IsZero() {
		return fmt.Errorf("model: invalid Attempt Workspace object")
	}
	switch object.StorageOrigin {
	case AttemptWorkspaceStorageStarter:
		if !object.StarterObjectID.IsValid() {
			return fmt.Errorf("model: starter-origin Attempt Workspace object requires source")
		}
	case AttemptWorkspaceStorageAttempt:
		if !object.StarterObjectID.IsZero() {
			return fmt.Errorf("model: attempt-origin Attempt Workspace object cannot reference starter source")
		}
	default:
		return fmt.Errorf("model: invalid Attempt Workspace storage origin")
	}
	return nil
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
