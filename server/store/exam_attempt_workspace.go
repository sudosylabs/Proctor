// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"

	"github.com/sudosylabs/proctor/server/model"
)

const ExamAttemptWorkspaceMutationOperation = "exam.attempt.workspace.mutate.v1"

// ExamAttemptWorkspaceMutationAccess is the complete hash-only selector for a
// candidate write. Persistence rechecks the current Class membership, Open
// Sitting, Active Attempt, exact active unexpired Participation generation,
// and owning Session-bound open Connection on every reservation, readiness,
// mutation, and exact replay.
type ExamAttemptWorkspaceMutationAccess struct {
	AttemptID                model.ExamAttemptID
	ParticipationID          model.AttemptParticipationID
	Generation               int64
	CandidateUserID          model.UserID
	SessionID                model.SessionID
	DesktopRegistrationID    model.DesktopRegistrationID
	DPoPKeyThumbprint        string
	ConnectionID             model.AttemptConnectionID
	ContinuityCredentialHash string
}

// ExamAttemptWorkspaceMutationTarget is the bounded preflight projection used
// to begin one Class-scoped audit attempt and address post-commit candidate
// effects. It exposes no path, content, credential, Session, or object selector.
// ApplyMutation rechecks all target ownership and eligibility under its locks.
type ExamAttemptWorkspaceMutationTarget struct {
	ExamID          model.ExamID
	SittingID       model.ExamSittingID
	ClassID         model.ClassID
	CandidateUserID model.UserID
	WorkspaceID     model.ExamAttemptWorkspaceID
}

// ExamAttemptWorkspaceObjectReservation creates invisible, expiring metadata
// before VFS staging. PostgreSQL supplies CreatedAt and ExpiresAt using its
// clock. Exact repeats of the same unused ObjectID return the same reservation.
type ExamAttemptWorkspaceObjectReservation struct {
	Access   ExamAttemptWorkspaceMutationAccess
	ObjectID model.AttemptWorkspaceObjectID
}

// ExamAttemptWorkspaceObjectReady records verified bounded metadata after VFS
// staging. It never carries an object key or backend revision. Exact repeats
// with identical content are idempotent; different content conflicts.
type ExamAttemptWorkspaceObjectReady struct {
	Access         ExamAttemptWorkspaceMutationAccess
	ObjectID       model.AttemptWorkspaceObjectID
	ContentVersion model.WorkspaceContentVersion
	Content        model.AttemptWorkspaceContent
}

// ExamAttemptWorkspaceMutation applies one selective entry-level fence. Create
// uses EntryID and DestinationPath. Move additionally requires ExpectedPath.
// Replace requires EntryID, ExpectedPath, ExpectedContentVersion, and a Ready
// ObjectID. Delete requires EntryID and ExpectedPath, plus
// ExpectedContentVersion for a file. Cursor is deliberately absent: unrelated
// entry mutations may commute while PostgreSQL still orders accepted changes.
// Rename and move are the same MoveEntry operation; moving a directory
// atomically rewrites its descendants after checking every resulting path and
// collision, while deleting a non-empty directory conflicts rather than
// recursively deleting it.
type ExamAttemptWorkspaceMutation struct {
	Access                 ExamAttemptWorkspaceMutationAccess
	Operation              model.AttemptWorkspaceMutationKind
	EntryID                model.AttemptWorkspaceEntryID
	ExpectedPath           string
	DestinationPath        string
	ExpectedContentVersion model.WorkspaceContentVersion
	ObjectID               model.AttemptWorkspaceObjectID
	AuditEventID           string
	AuditAt                int64
}

// ExamAttemptWorkspaceMutationResult is the safe acknowledged state. Entry is
// nil only for deletion. Change carries the resulting Workspace Cursor but no
// mutation-key digest, object selector, credential, Session, or content body.
type ExamAttemptWorkspaceMutationResult struct {
	SittingID       model.ExamSittingID
	ClassID         model.ClassID
	CandidateUserID model.UserID
	WorkspaceID     model.ExamAttemptWorkspaceID
	Entry           *CandidateAttemptWorkspaceItem
	Change          model.AttemptWorkspaceJournalEntry
	Replayed        bool
}

// CandidateWorkspaceJournalOptions requests ordered changes strictly after a
// numeric cursor. Limit is 1..200. Paths remain in the protected response body
// and never enter a URL, pagination token, audit field, log, or realtime event.
type CandidateWorkspaceJournalOptions struct {
	Access      CandidateAttemptAccess
	AfterCursor int64
	Limit       int
}

// CandidateWorkspaceJournalPage signals RefreshRequired with no Entries when
// AfterCursor predates the retained 4,096-entry window. The caller then pages
// a complete manifest at CurrentCursor before resuming journal recovery.
type CandidateWorkspaceJournalPage struct {
	WorkspaceID     model.ExamAttemptWorkspaceID
	CurrentCursor   int64
	Entries         []model.AttemptWorkspaceJournalEntry
	HasMore         bool
	RefreshRequired bool
}

// ExamAttemptWorkspaceStore is the deep seam for the acknowledged live
// Workspace. It owns access revalidation, hierarchy/quota checks, object-stage
// visibility, selective concurrency, ordered journal/cursor advancement,
// attempt-scoped command outcome, and cleanup eligibility.
//
// ApplyMutation atomically changes the logical hierarchy/current content
// pointer, advances the Workspace Cursor, appends one journal record, marks a
// consumed Ready object Current, makes a superseded/deleted attempt-owned
// object reclaimable after the safety window, and commits the idempotent
// outcome and supplied audit attempt. Audit data is limited to safe scope,
// operation, Entry identity, and resulting cursor; paths, content metadata,
// bodies, and object identities are excluded. Conflicts or failed commits
// change none of those facts. Exact replays recheck current write eligibility
// and return the retained result. The durable journal records the command's
// bounded KeyDigest for correlation, but candidate journal projections omit it.
// At most the newest 4,096 entries remain; pruning and append are atomic.
// Stable conflicts are attempt_workspace_path, attempt_workspace_entry,
// attempt_workspace_content_version, attempt_workspace_not_empty,
// attempt_workspace_entry_limit, attempt_workspace_size_limit, and
// attempt_workspace_object_state.
//
// Cleanup claims use PostgreSQL time, limit 1..200, and a trimmed token of at
// most 128 bytes. ClaimObjectsForCleanup selects only attempt-origin objects
// whose safe window elapsed and which have no current Entry, retained command
// outcome, or other durable reference. It also recovers stale claims. Physical
// removal is idempotent; an outcome-unknown removal remains Claimed so a later
// stale-claim retry repeats removal before CompleteObjectCleanup deletes the
// metadata. ReleaseObjectCleanup handles known physical-removal failure.
type ExamAttemptWorkspaceStore interface {
	List(context.Context, CandidateWorkspaceListOptions) (*CandidateAttemptWorkspacePage, error)
	ResolveFile(context.Context, CandidateAttemptAccess, model.AttemptWorkspaceEntryID) (*CandidateWorkspaceContent, error)
	ListJournal(context.Context, CandidateWorkspaceJournalOptions) (*CandidateWorkspaceJournalPage, error)
	ResolveMutationTarget(context.Context, ExamAttemptWorkspaceMutationAccess) (*ExamAttemptWorkspaceMutationTarget, error)
	ReserveObject(context.Context, *ExamAttemptWorkspaceObjectReservation) (*model.AttemptWorkspaceObject, error)
	MarkObjectReady(context.Context, *ExamAttemptWorkspaceObjectReady) (*model.AttemptWorkspaceObject, error)
	ApplyMutation(context.Context, *ExamAttemptWorkspaceMutation, *CommandIdempotency) (*ExamAttemptWorkspaceMutationResult, error)
	MarkObjectReclaimable(context.Context, model.AttemptWorkspaceObjectID) error
	ClaimObjectsForCleanup(context.Context, int, string) ([]model.AttemptWorkspaceObject, error)
	CompleteObjectCleanup(context.Context, model.AttemptWorkspaceObjectID, string) error
	ReleaseObjectCleanup(context.Context, model.AttemptWorkspaceObjectID, string) error
}
