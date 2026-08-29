// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

const ExamCorrectionResourceStageOperation = "exam.sitting.correction.resource.stage.v1"

// ExamCorrectionResourceStageTarget says whether a purpose-bound stage
// allocates a new stable resource identity or replaces the content behind an
// identity already present in the correction's exact base Revision.
type ExamCorrectionResourceStageTarget string

const (
	ExamCorrectionResourceAddition    ExamCorrectionResourceStageTarget = "addition"
	ExamCorrectionResourceReplacement ExamCorrectionResourceStageTarget = "replacement"
)

func (target ExamCorrectionResourceStageTarget) IsValid() bool {
	return target == ExamCorrectionResourceAddition || target == ExamCorrectionResourceReplacement
}

type ExamCorrectionResourceStageState string

const (
	ExamCorrectionResourceStagePending  ExamCorrectionResourceStageState = "pending"
	ExamCorrectionResourceStageReady    ExamCorrectionResourceStageState = "ready"
	ExamCorrectionResourceStageConsumed ExamCorrectionResourceStageState = "consumed"
)

// ExamCorrectionResourceStage is the complete bounded result of reserving or
// completing one staged resource. Storage keys never cross this boundary.
// Rendition is present only after exact bytes and metadata have been staged.
type ExamCorrectionResourceStage struct {
	ID              model.ExamCorrectionResourceStageID
	ExamID          model.ExamID
	SittingID       model.ExamSittingID
	BaseRevisionID  model.ExamRevisionID
	Target          ExamCorrectionResourceStageTarget
	ResourceID      model.ExamResourceID
	FileEntryID     model.FileEntryID
	FileRevisionID  model.FileRevisionID
	UploadLeaseID   model.UploadLeaseID
	RenditionID     model.FileRenditionID
	CreatedByUserID model.UserID
	State           ExamCorrectionResourceStageState
	CreatedAt       time.Time
	ExpiresAt       time.Time
	ReadyAt         time.Time
	ConsumedAt      time.Time
	Rendition       *model.FileRendition
}

// ExamCorrectionResourceStageReservation preallocates every opaque identity
// before a VFS write. Addition carries a pristine File Entry; replacement
// names the stable File Entry pinned by the base Revision. Revision and Lease
// remain pending/invisible until Apply consumes a Ready stage.
type ExamCorrectionResourceStageReservation struct {
	StageID         model.ExamCorrectionResourceStageID
	ExamID          model.ExamID
	SittingID       model.ExamSittingID
	BaseRevisionID  model.ExamRevisionID
	Target          ExamCorrectionResourceStageTarget
	ResourceID      model.ExamResourceID
	Entry           *model.FileEntry
	FileEntryID     model.FileEntryID
	Revision        *model.FileRevision
	Lease           *model.UploadLease
	RenditionID     model.FileRenditionID
	ActorUserID     model.UserID
	ManagerOverride bool
	CreatedAt       time.Time
	AuditEventID    string
	AuditAt         int64
}

type ExamCorrectionResourceStageReadyInput struct {
	StageID     model.ExamCorrectionResourceStageID
	ActorUserID model.UserID
	Rendition   *model.FileRendition
	ReadyAt     time.Time
}

// ExamCorrectionResourceManifestItem is one entry in the complete desired
// ordered resource manifest. A zero StageID retains the exact base rendition;
// a StageID replaces or adds content according to that bound Ready stage.
type ExamCorrectionResourceManifestItem struct {
	ResourceID          model.ExamResourceID
	DisplayName         string
	DescriptionMarkdown string
	StageID             model.ExamCorrectionResourceStageID
}

type ExamCorrectionApplication struct {
	RevisionID              model.ExamRevisionID
	ExamID                  model.ExamID
	SittingID               model.ExamSittingID
	CurrentRevisionID       model.ExamRevisionID
	ExpectedSittingRevision int64
	ActorUserID             model.UserID
	ManagerOverride         bool
	InstructionsMarkdown    *string
	BrowserPolicy           *model.BrowserPolicy
	Resources               []ExamCorrectionResourceManifestItem
	CandidateSummary        string
	AcknowledgementRequired bool
	PrivateReason           string
	AppliedAt               time.Time
	AuditEventID            string
	AuditAt                 int64
}

// ExamCorrectionResult is intentionally bounded. Full authored snapshot
// content remains available only through ExamRevisionStore.GetSnapshot.
type ExamCorrectionResult struct {
	Revision           *ExamRevisionSummary
	Sitting            *ExamSittingSnapshot
	PreviousRevisionID model.ExamRevisionID
	EffectiveAt        time.Time
	Replayed           bool
}

// ExamCorrectionStore is the narrow durable core for live correction. Reserve
// resolves an exact committed command before current-state checks and returns
// the stage's current Pending/Ready/Consumed state. Apply is the sole atomic
// visibility mutation: it resolves replay first, locks and revalidates the
// Exam/Sitting/base/stages, creates one sealed immutable Revision, consumes
// referenced stages, retargets only that Sitting, appends private provenance,
// completes safe audit, and stores a bounded outcome.
type ExamCorrectionStore interface {
	ReserveResourceStage(context.Context, *ExamCorrectionResourceStageReservation, *CommandIdempotency) (*ExamCorrectionResourceStage, error)
	MarkResourceStageReady(context.Context, *ExamCorrectionResourceStageReadyInput) (*ExamCorrectionResourceStage, error)
	Apply(context.Context, *ExamCorrectionApplication, *CommandIdempotency) (*ExamCorrectionResult, error)
}
