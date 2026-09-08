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

type ExecutionWorkspaceNode struct {
	EntryID         model.AttemptWorkspaceEntryID
	Kind            model.StarterWorkspaceEntryKind
	Path            string
	ContentVersion  model.WorkspaceContentVersion
	SizeBytes       int64
	SHA256          string
	StorageOrigin   model.AttemptWorkspaceObjectStorage
	StarterObjectID model.StarterWorkspaceObjectID
	AttemptObjectID model.AttemptWorkspaceObjectID
}

// ExecutionWorkspaceChange is a private projection of one retained journal
// position. Content selects that position's immutable object, never current
// live bytes. SourceGrantID is explicit provenance, not inferred from context.
type ExecutionWorkspaceChange struct {
	Change                 model.AttemptWorkspaceJournalEntry
	ExpectedContentVersion model.WorkspaceContentVersion
	Content                *ExecutionWorkspaceNode
	SourceGrantID          model.ExecutionGrantID
}

type ExecutionWorkspacePage struct {
	CurrentCursor   int64
	Changes         []ExecutionWorkspaceChange
	HasMore         bool
	RefreshRequired bool
}

type ExecutionWorkspaceSnapshot struct {
	Cursor int64
	Nodes  []ExecutionWorkspaceNode
}

// ExecutionGrantReservation is a placement decision made from one current,
// transient host-catalog observation. PostgreSQL rechecks Attempt eligibility
// before making it authoritative.
type ExecutionGrantReservation struct {
	ID        model.ExecutionGrantID
	AttemptID model.ExamAttemptID
	HostID    string
	Image     string
	Network   model.ExecutionNetwork
	At        time.Time
}

type ExecutionGrantReassignment struct {
	CurrentID       model.ExecutionGrantID
	CurrentRevision int64
	Replacement     ExecutionGrantReservation
}

type ExecutionGrantReassignmentResult struct {
	Previous *model.ExecutionGrant
	Current  *model.ExecutionGrant
}

// ExecutionGrantConvergence is the bounded durable projection used to make a
// current guest agree with its authoritative Attempt and Sitting lifecycle.
type ExecutionGrantConvergence struct {
	SecurityBlocked         bool
	Grant                   *model.ExecutionGrant
	AttemptState            model.ExamAttemptState
	SittingState            model.ExamSittingState
	SittingRevision         int64
	WorkspaceCursor         int64
	AcknowledgementRequired bool
}

// ExecutionLifecycleLease serializes transient host lifecycle effects for one
// exact grant across all application nodes. Process or connection loss must
// release the lease automatically in the concrete adapter.
type ExecutionLifecycleLease interface {
	Validate(context.Context) error
	Release(context.Context) error
}

// ExecutionGrantStore owns the durable half of execution placement. Reserve
// and Reassign lock and recheck that the Attempt and Sitting are executable.
// Release commits before the best-effort host revocation is attempted.
type ExecutionGrantStore interface {
	// PrepareProjection checks exact immutable journal/snapshot metadata, current
	// control and starting cursor, then retains the entire bounded request before
	// host I/O. Matching retries return the same request or completed receipt;
	// changed bytes for an existing mutation ID conflict. One effect may be pending
	// per grant; at most 65,536 receipts are retained for its lifetime, with refusal
	// rather than eviction. Callers hold the grant's lifecycle lease across effects.
	PrepareProjection(context.Context, ExecutionProjectionRequest) (*ExecutionProjectionEffect, error)
	// PendingProjection recovers an interrupted effect. Absence is ErrNotFound.
	PendingProjection(context.Context, model.ExecutionGrantID) (*ExecutionProjectionRequest, error)
	// CompleteProjection verifies the exact retained request/receipt and advances
	// only this grant's applied cursor. Later unrelated Workspace commits do not
	// invalidate the acknowledged prefix. Completion never opens execution gates;
	// acquisition separately requires current confirmed control and Workspace state.
	CompleteProjection(context.Context, ExecutionProjectionReceipt) (*model.ExecutionGrant, error)
	// RejectProjection records a definitive host validation refusal without
	// advancing projection. Never call this after a timeout or unknown outcome.
	// It retains the exact digest to prevent reuse and permits a new request after
	// pending host observations are reconciled. The lifecycle lease must be held.
	RejectProjection(context.Context, model.ExecutionFence, string) (*model.ExecutionGrant, error)

	// WorkspaceChanges reads a consecutive bounded prefix after the exact grant's
	// applied cursor under its current confirmed running fence. Limit is 1..128;
	// metadata is at most 256 KiB. Missing/pruned positions or immutable content
	// return RefreshRequired with no partial changes. Only retained unapplied
	// positions of an active fenced grant protect superseded object cleanup.
	WorkspaceChanges(context.Context, model.ExecutionFence, int) (*ExecutionWorkspacePage, error)

	// PrepareControl locks the grant and current Attempt/Sitting/security/correction
	// authority. It binds an epoch once while reserved, refuses replacement epochs,
	// and commits a monotonically fenced intent before host I/O. Unchanged authority
	// returns the same intent, including after an uncertain host response. Callers
	// hold the exact-grant lifecycle lease across preparation, effect and completion.
	PrepareControl(context.Context, model.ExecutionGrantID, string, time.Time) (*model.ExecutionGrant, error)
	// AcknowledgeControl accepts only the exact prepared fence/state and unchanged
	// current authority. Exact completion retries are idempotent. It never treats
	// an old acknowledgement as proof for a newer intent, even with the same state.
	AcknowledgeControl(context.Context, model.ExecutionFence, model.ExecutionControlState, time.Time) (*model.ExecutionGrant, error)

	Current(context.Context, model.ExamAttemptID) (*model.ExecutionGrant, error)
	Reserve(context.Context, ExecutionGrantReservation) (*model.ExecutionGrant, error)
	Reassign(context.Context, ExecutionGrantReassignment) (*ExecutionGrantReassignmentResult, error)
	// PrepareWorkspaceEffect records uncertainty before an exact-grant host
	// effect. Completion requires the same authoritative Workspace cursor and
	// makes an initially reserved grant ready. Both run under its lifecycle lease.
	PrepareWorkspaceEffect(context.Context, model.ExecutionGrantID, int64, int64, time.Time) (*model.ExecutionGrant, error)
	MarkWorkspaceApplied(context.Context, model.ExecutionGrantID, int64, int64, time.Time) (*model.ExecutionGrant, error)
	PrepareSittingStateEffect(context.Context, model.ExecutionGrantID, int64, model.ExamSittingState, int64, time.Time) (*model.ExecutionGrant, error)
	MarkSittingStateApplied(context.Context, model.ExecutionGrantID, int64, model.ExamSittingState, int64, time.Time) (*model.ExecutionGrant, error)
	Release(context.Context, model.ExamAttemptID, time.Time) (*model.ExecutionGrant, error)
	ReleaseGrant(context.Context, model.ExecutionGrantID, time.Time) (*model.ExecutionGrant, error)
	MarkRevoked(context.Context, model.ExecutionGrantID, int64, time.Time) (*model.ExecutionGrant, error)
	ListPendingRevocations(context.Context, model.ExecutionGrantID, int) ([]*model.ExecutionGrant, error)
	AcquireLifecycleLease(context.Context, model.ExecutionGrantID) (ExecutionLifecycleLease, error)
	CurrentForReconciliation(context.Context, model.ExecutionGrantID) (*ExecutionGrantConvergence, error)
	ListCurrentForReconciliation(context.Context, model.ExecutionGrantID, int) ([]ExecutionGrantConvergence, error)
	ListCurrentForSitting(context.Context, model.ExamSittingID, model.ExecutionGrantID, int) ([]*model.ExecutionGrant, error)
	WorkspaceSnapshot(context.Context, model.ExamAttemptID) (*ExecutionWorkspaceSnapshot, error)
}
