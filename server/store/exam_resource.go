// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

// ExamResourceRecord is the complete bounded Draft projection needed to list
// or open one resource. The exact selected primary rendition is authoritative;
// storage keys and upload leases never leave persistence/File Content.
type ExamResourceRecord struct {
	Resource      *model.ExamResource
	Rendition     *model.FileRendition
	DraftRevision int64
}

type ExamResourceUploadReservation struct {
	ExamID                model.ExamID
	ActorUserID           model.UserID
	ManagerOverride       bool
	ExpectedDraftRevision int64
	ResourceID            model.ExamResourceID
	Entry                 *model.FileEntry
	EntryID               model.FileEntryID
	Revision              *model.FileRevision
	Lease                 *model.UploadLease
	Replacement           bool
}

type ExamResourceUploadFinalization struct {
	ExamID                model.ExamID
	ActorUserID           model.UserID
	ManagerOverride       bool
	ExpectedDraftRevision int64
	Resource              *model.ExamResource
	LeaseID               model.UploadLeaseID
	Rendition             *model.FileRendition
	ChangedAt             time.Time
	AuditEventID          string
	AuditAt               int64
}

type ExamResourceMetadataUpdate struct {
	ExamID                model.ExamID
	ActorUserID           model.UserID
	ManagerOverride       bool
	ExpectedDraftRevision int64
	ResourceID            model.ExamResourceID
	DisplayName           string
	DescriptionMarkdown   string
	ChangedAt             time.Time
	AuditEventID          string
	AuditAt               int64
}

type ExamResourceReorder struct {
	ExamID                model.ExamID
	ActorUserID           model.UserID
	ManagerOverride       bool
	ExpectedDraftRevision int64
	ResourceIDs           []model.ExamResourceID
	ChangedAt             time.Time
	AuditEventID          string
	AuditAt               int64
}

type ExamResourceRemoval struct {
	ExamID                model.ExamID
	ActorUserID           model.UserID
	ManagerOverride       bool
	ExpectedDraftRevision int64
	ResourceID            model.ExamResourceID
	ChangedAt             time.Time
	AuditEventID          string
	AuditAt               int64
}

type ExamResourceCommandResult struct {
	Value         *ExamResourceRecord
	Items         []ExamResourceRecord
	DraftRevision int64
	Replayed      bool
}

// ExamResourceStore owns Draft-resource metadata and the two-phase boundary
// between opaque byte staging and visible authoring state. ReserveUpload makes
// only opaque pending file metadata durable and deliberately does not inspect
// the Exam or Draft, allowing an exact retry to reach its stored command
// outcome after the original commit advanced the Draft. FinalizeUpload rechecks active Exam,
// Draft revision, Manager relationship, upload lease, purpose and rendition,
// then atomically makes the immutable revision available, selects it, advances
// the Draft, completes audit and records the idempotent outcome. All other
// mutations have the same active/manager/revision/audit/outcome guarantees.
type ExamResourceStore interface {
	ReserveUpload(context.Context, *ExamResourceUploadReservation) (*FileUpload, error)
	FinalizeUpload(context.Context, *ExamResourceUploadFinalization, *CommandIdempotency) (*ExamResourceCommandResult, error)
	List(context.Context, model.ExamID) ([]ExamResourceRecord, error)
	Get(context.Context, model.ExamID, model.ExamResourceID) (*ExamResourceRecord, error)
	UpdateMetadata(context.Context, *ExamResourceMetadataUpdate, *CommandIdempotency) (*ExamResourceCommandResult, error)
	Reorder(context.Context, *ExamResourceReorder, *CommandIdempotency) (*ExamResourceCommandResult, error)
	Remove(context.Context, *ExamResourceRemoval, *CommandIdempotency) (*ExamResourceCommandResult, error)
}
