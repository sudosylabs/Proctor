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
	Kind            model.StarterWorkspaceEntryKind
	Path            string
	ContentVersion  model.WorkspaceContentVersion
	SizeBytes       int64
	SHA256          string
	StorageOrigin   model.AttemptWorkspaceObjectStorage
	StarterObjectID model.StarterWorkspaceObjectID
	AttemptObjectID model.AttemptWorkspaceObjectID
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
	Grant                   *model.ExecutionGrant
	AttemptState            model.ExamAttemptState
	SittingState            model.ExamSittingState
	SittingRevision         int64
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
	Current(context.Context, model.ExamAttemptID) (*model.ExecutionGrant, error)
	Reserve(context.Context, ExecutionGrantReservation) (*model.ExecutionGrant, error)
	Reassign(context.Context, ExecutionGrantReassignment) (*ExecutionGrantReassignmentResult, error)
	MarkReady(context.Context, model.ExecutionGrantID, int64, time.Time) (*model.ExecutionGrant, error)
	PrepareSittingStateEffect(context.Context, model.ExecutionGrantID, int64, model.ExamSittingState, int64, time.Time) (*model.ExecutionGrant, error)
	MarkSittingStateApplied(context.Context, model.ExecutionGrantID, int64, model.ExamSittingState, int64, time.Time) (*model.ExecutionGrant, error)
	Release(context.Context, model.ExamAttemptID, time.Time) (*model.ExecutionGrant, error)
	ReleaseGrant(context.Context, model.ExecutionGrantID, time.Time) (*model.ExecutionGrant, error)
	MarkRevoked(context.Context, model.ExecutionGrantID, int64, time.Time) (*model.ExecutionGrant, error)
	ListPendingRevocations(context.Context, int) ([]*model.ExecutionGrant, error)
	AcquireLifecycleLease(context.Context, model.ExecutionGrantID) (ExecutionLifecycleLease, error)
	CurrentForReconciliation(context.Context, model.ExecutionGrantID) (*ExecutionGrantConvergence, error)
	ListCurrentForReconciliation(context.Context, model.ExecutionGrantID, int) ([]ExecutionGrantConvergence, error)
	ListCurrentForSitting(context.Context, model.ExamSittingID, model.ExecutionGrantID, int) ([]*model.ExecutionGrant, error)
	WorkspaceSnapshot(context.Context, model.ExamAttemptID) (*ExecutionWorkspaceSnapshot, error)
}
