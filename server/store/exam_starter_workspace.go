// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package store

import (
	"context"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

// ExamStarterWorkspaceItem is one bounded visible Draft entry and, for files,
// the exact current opaque content metadata selected by PostgreSQL.
type ExamStarterWorkspaceItem struct {
	Entry  model.StarterWorkspaceEntry
	Object *model.StarterWorkspaceObject
}

type ExamStarterWorkspaceReservation struct {
	Object *model.StarterWorkspaceObject
}

// ExamStarterWorkspaceMutation is the common authorization, optimistic
// concurrency, audit, and timing envelope for a hierarchy mutation.
type ExamStarterWorkspaceMutation struct {
	ExamID                 model.ExamID
	ActorUserID            model.UserID
	ManagerOverride        bool
	ExpectedDraftRevision  int64
	ChangedAt              int64
	AuditEventID           string
	AuditAt                int64
	EntryID                model.StarterWorkspaceEntryID
	Path                   string
	ObjectID               model.StarterWorkspaceObjectID
	ExpectedContentVersion model.WorkspaceContentVersion
	ContentVersion         model.WorkspaceContentVersion
	MediaType              string
	SizeBytes              int64
	SHA256                 string
}

type ExamStarterWorkspaceMutationResult struct {
	Entry             *model.StarterWorkspaceEntry
	Object            *model.StarterWorkspaceObject
	DraftRevision     int64
	ReclaimableObject model.StarterWorkspaceObjectID
	Replayed          bool
}

// ExamStarterWorkspaceStore owns the complete bounded Draft hierarchy.
// Object reservation creates only invisible, expiring opaque state and makes
// no authorization or Draft-currentness decision. Mutations lock and recheck
// the active Exam, current manager relationship
// unless override is explicit, and expected Draft revision. Successful
// hierarchy changes, Draft revision, audit completion, and command outcome
// commit atomically. File finalization additionally consumes one unexpired
// staged object and publishes its verified metadata. Replacements make the
// prior object reclaimable only after the safety window. Exact replays return
// the committed result without repeating mutation or changing cleanup state.
type ExamStarterWorkspaceStore interface {
	List(context.Context, model.ExamID) ([]ExamStarterWorkspaceItem, error)
	GetFile(context.Context, model.ExamID, model.StarterWorkspaceEntryID) (*ExamStarterWorkspaceItem, error)
	ReserveObject(context.Context, *ExamStarterWorkspaceReservation) (*model.StarterWorkspaceObject, error)
	CreateDirectory(context.Context, *ExamStarterWorkspaceMutation, *CommandIdempotency) (*ExamStarterWorkspaceMutationResult, error)
	CreateFile(context.Context, *ExamStarterWorkspaceMutation, *CommandIdempotency) (*ExamStarterWorkspaceMutationResult, error)
	MoveEntry(context.Context, *ExamStarterWorkspaceMutation, *CommandIdempotency) (*ExamStarterWorkspaceMutationResult, error)
	ReplaceFile(context.Context, *ExamStarterWorkspaceMutation, *CommandIdempotency) (*ExamStarterWorkspaceMutationResult, error)
	RemoveEntry(context.Context, *ExamStarterWorkspaceMutation, *CommandIdempotency) (*ExamStarterWorkspaceMutationResult, error)
	MarkObjectReclaimable(context.Context, model.StarterWorkspaceObjectID, time.Time) error
	ClaimObjectsForCleanup(context.Context, int, string) ([]model.StarterWorkspaceObject, error)
	CompleteObjectCleanup(context.Context, model.StarterWorkspaceObjectID, string) error
	ReleaseObjectCleanup(context.Context, model.StarterWorkspaceObjectID, string) error
}
