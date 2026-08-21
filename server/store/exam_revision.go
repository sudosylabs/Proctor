// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

type ExamRevisionPublication struct {
	RevisionID            model.ExamRevisionID
	ExamID                model.ExamID
	ActorUserID           model.UserID
	ManagerOverride       bool
	ExpectedDraftRevision int64
	Kind                  model.ExamRevisionPublicationKind
	PublishedAt           time.Time
	AuditEventID          string
	AuditAt               int64
}

type ExamRevisionPublicationResult struct {
	Revision      *ExamRevisionSummary
	ExamRevision  int64
	DraftRevision int64
	Replayed      bool
}

// ExamRevisionSummary is the bounded public-authoring projection. It excludes
// instructions, canonical policy bytes, resource metadata, Starter Workspace
// paths and every opaque content identity.
type ExamRevisionSummary struct {
	ID                      model.ExamRevisionID
	ExamID                  model.ExamID
	Number                  int64
	SourceDraftRevision     int64
	Title                   string
	PolicySchemaVersion     int
	PolicyDigest            string
	ExecutionProfileDigest  string
	StarterWorkspaceDigest  string
	ContentDigest           string
	ResourceCount           int
	StarterWorkspaceEntries int
	StarterWorkspaceBytes   int64
	PublishedByUserID       model.UserID
	PublishedAt             time.Time
	BaseRevisionID          model.ExamRevisionID
	Kind                    model.ExamRevisionPublicationKind
}

type ExamRevisionListOptions struct {
	ExamID           model.ExamID
	BeforeNumber     int64
	BeforeRevisionID model.ExamRevisionID
	Limit            int
}

// ExamRevisionStore owns immutable publication. Publish resolves a committed
// command before every stale/archive/no-change decision. Otherwise one
// transaction locks Exam and Draft, rechecks current manager authority,
// materializes and validates all active resources and Starter Workspace state,
// canonically freezes them, writes immutable history, selects the new default,
// rebases and advances the Draft, advances Exam revision, completes audit and
// records the small idempotent outcome. GetSnapshot is an internal exact read;
// public callers use GetSummary/List and never receive authored content.
type ExamRevisionStore interface {
	Publish(context.Context, *ExamRevisionPublication, *CommandIdempotency) (*ExamRevisionPublicationResult, error)
	GetSummary(context.Context, model.ExamID, model.ExamRevisionID) (*ExamRevisionSummary, error)
	List(context.Context, ExamRevisionListOptions) ([]ExamRevisionSummary, error)
	GetSnapshot(context.Context, model.ExamID, model.ExamRevisionID) (*model.ExamRevision, error)
}
