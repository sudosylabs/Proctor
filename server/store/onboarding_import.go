// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package store

import (
	"context"
	"crypto/sha256"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

const (
	OnboardingImportMaximumBytes   = 10 * 1024 * 1024
	OnboardingImportMaximumRows    = 50_000
	OnboardingImportMaximumColumns = 64
	OnboardingImportMaximumField   = 16 * 1024
	OnboardingImportPageSize       = 200
)

// OnboardingImport is the safe aggregate projection. Recipient data and the
// frozen row commands remain private to persistence and never enter Jobs,
// audit fields, logs, or administration list responses.
type OnboardingImport struct {
	ID                        model.OnboardingImportID
	Mode                      model.OnboardingImportMode
	State                     model.OnboardingImportState
	ScopeType                 model.RoleScopeType
	ScopeID                   string
	RoleID                    model.RoleID
	SourcePeriodID            model.AcademicPeriodID
	SourceClassID             model.ClassID
	DestinationPeriodID       model.AcademicPeriodID
	DestinationClassID        model.ClassID
	SourcePeriodRevision      int64
	SourceClassRevision       int64
	DestinationPeriodRevision int64
	DestinationClassRevision  int64
	EffectiveAt               time.Time
	ActorUserID               model.UserID
	PreviewDigest             string
	IgnoredHeaders            []string
	TotalRows                 int
	ValidRows                 int
	InvalidRows               int
	SucceededRows             int
	NoOpRows                  int
	FailedRows                int
	SkippedRows               int
	CommitPolicy              model.OnboardingImportCommitPolicy
	ParseJobID                model.JobID
	ExecutionJobID            model.JobID
	CreatedAt                 time.Time
	UpdatedAt                 time.Time
	ExpiresAt                 time.Time
	Revision                  int64
	FailureCode               string
	Principal                 model.Principal
}

// OnboardingImportRow is the detailed seven-day preview/report row. Command
// contains private normalized recipient/package input and must never be
// returned directly by a transport.
type OnboardingImportRow struct {
	ImportID                        model.OnboardingImportID
	RowNumber                       int
	Reference                       string
	Operation                       string
	ScopeType                       model.RoleScopeType
	ScopeID                         string
	TargetRevision                  int64
	RoleID                          model.RoleID
	RoleRevision                    int64
	Email                           string
	UserID                          model.UserID
	RelationshipID                  string
	RelationshipRevision            int64
	DestinationRelationshipID       string
	DestinationRelationshipRevision int64
	AffiliationKind                 model.AffiliationKind
	Username                        string
	DisplayName                     string
	FirstName                       string
	LastName                        string
	Locale                          string
	Timezone                        string
	StartsAt                        int64
	EndsAt                          int64
	PreviewStatus                   model.OnboardingImportRowStatus
	PreviewCode                     string
	Status                          model.OnboardingImportRowStatus
	PublicCode                      string
	InvitationID                    model.InvitationID
	ResourceID                      string
	UpdatedAt                       time.Time
}

type OnboardingImportCreation struct {
	Import             *OnboardingImport
	ParseJob           *model.Job
	AuditEventID       model.AuditEventID
	SourceAuditEventID model.AuditEventID
	AuditAt            int64
}

type OnboardingImportPreviewCompletion struct {
	ID               model.OnboardingImportID
	ExpectedRevision int64
	Digest           string
	IgnoredHeaders   []string
	Rows             []OnboardingImportRow
	At               time.Time
}

type OnboardingImportCommit struct {
	ID                 model.OnboardingImportID
	ActorUserID        model.UserID
	Principal          model.Principal
	ExpectedRevision   int64
	PreviewDigest      string
	Policy             model.OnboardingImportCommitPolicy
	IdempotencyKey     [sha256.Size]byte
	ExecutionJob       *model.Job
	At                 time.Time
	AuditEventID       model.AuditEventID
	SourceAuditEventID model.AuditEventID
	AuditAt            int64
}

type OnboardingImportCancellation struct {
	ID                 model.OnboardingImportID
	ActorUserID        model.UserID
	Principal          model.Principal
	At                 time.Time
	AuditEventID       model.AuditEventID
	SourceAuditEventID model.AuditEventID
	AuditAt            int64
}

type OnboardingImportRowCompletion struct {
	ID           model.OnboardingImportID
	RowNumber    int
	Status       model.OnboardingImportRowStatus
	PublicCode   string
	InvitationID model.InvitationID
	ResourceID   string
	At           time.Time
}

type OnboardingImportPage struct {
	Rows []OnboardingImportRow
	More bool
}

// OnboardingImportStore owns seven-day administrative import and progression
// aggregates, frozen private rows, execution fences, and retention.
type OnboardingImportStore interface {
	CreateOnboardingImport(context.Context, *OnboardingImportCreation) (*OnboardingImport, error)
	GetOnboardingImport(context.Context, model.OnboardingImportID) (*OnboardingImport, error)
	ListStudentProgressionRoster(context.Context, model.ClassID, time.Time, int) ([]*model.ClassMember, error)
	CompleteOnboardingImportPreview(context.Context, *OnboardingImportPreviewCompletion) (*OnboardingImport, error)
	CommitOnboardingImport(context.Context, *OnboardingImportCommit) (*OnboardingImport, error)
	ListOnboardingImportRows(context.Context, model.OnboardingImportID, int, int) (*OnboardingImportPage, error)
	CompleteOnboardingImportRow(context.Context, *OnboardingImportRowCompletion) (*OnboardingImport, error)
	FinishOnboardingImport(context.Context, model.OnboardingImportID, time.Time) (*OnboardingImport, error)
	CancelOnboardingImport(context.Context, *OnboardingImportCancellation) (*OnboardingImport, error)
	FailOnboardingImport(context.Context, model.OnboardingImportID, string, time.Time) (*OnboardingImport, error)
	ListExpiredOnboardingImports(context.Context, int, time.Time) ([]model.OnboardingImportID, error)
	PurgeOnboardingImport(context.Context, model.OnboardingImportID, time.Time) (bool, error)
}
