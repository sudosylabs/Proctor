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

// ExamAuthoringSnapshot is the bounded authoring view returned by the Exam
// aggregate store. Managers are counted but never hydrated as an unbounded set.
type ExamAuthoringSnapshot struct {
	Exam                *model.Exam
	Draft               *model.ExamDraft
	OwnerUserID         model.UserID
	ManagerCount        int
	ActorIsManager      bool
	Capacity            model.ExamCapacityPolicy
	ResourceCount       int
	HasStarterWorkspace bool
}

// ExamAccessSnapshot is the minimal projection needed to authorize an Exam
// operation before authored Draft content is loaded.
type ExamAccessSnapshot struct {
	Exam           *model.Exam
	ActorIsManager bool
}

type ExamAuthoringCommandResult struct {
	Value    *ExamAuthoringSnapshot
	Replayed bool
}

type ExamArchiveFilter string

const (
	ExamArchiveActive   ExamArchiveFilter = "active"
	ExamArchiveArchived ExamArchiveFilter = "archived"
	ExamArchiveAll      ExamArchiveFilter = "all"
)

// ExamListVisibility is the bounded, persistence-ready result of current role
// authorization. OrdinaryMembershipAt is the same authorization decision time
// at which persistence must require the actor's current exact Academic Unit
// membership and Exam Manager relationship. Override visibility requires
// neither relationship and does not manufacture either one.
type ExamListVisibility struct {
	ActorUserID                 model.UserID
	OrdinaryMembershipAt        time.Time
	OrdinaryInstitutionWide     bool
	OrdinaryAcademicUnitRootIDs []model.AcademicUnitID
	OverrideInstitutionWide     bool
	OverrideAcademicUnitRootIDs []model.AcademicUnitID
}

type ExamListOptions struct {
	AcademicUnitID  model.AcademicUnitID
	Query           string
	ArchiveFilter   ExamArchiveFilter
	SittingStates   []model.ExamSittingState
	EndsAfter       time.Time
	StartsBefore    time.Time
	BeforeUpdatedAt time.Time
	BeforeExamID    model.ExamID
	Limit           int
	Visibility      ExamListVisibility
}

// ExamSummary is the bounded catalog projection. Authored Markdown, policy,
// resources, and Starter Workspace state are deliberately absent.
type ExamSummary struct {
	ID                      model.ExamID
	AcademicUnitID          model.AcademicUnitID
	AcademicUnitDisplayName string
	CreatorUserID           model.UserID
	OwnerUserID             model.UserID
	Title                   string
	UpdatedAt               time.Time
	ArchivedAt              model.OptionalTime
	Revision                int64
	ManagerCount            int
	SittingCount            int
	MatchingSittingCount    int
	SittingSummary          *ExamCatalogSittingSummary
}

type ExamCatalogSittingSummary struct {
	ID               model.ExamSittingID
	ExamRevisionID   model.ExamRevisionID
	ClassID          model.ClassID
	ClassDisplayName string
	ScheduledStartAt time.Time
	ScheduledEndAt   time.Time
	State            model.ExamSittingState
	Revision         int64
}

// ExamManagerSummary is the bounded management projection. It deliberately
// contains relationship provenance rather than a User profile.
type ExamManagerSummary struct {
	Manager   model.ExamManager
	IsCreator bool
	IsOwner   bool
}

type ExamManagerListOptions struct {
	ExamID          model.ExamID
	BeforeGrantedAt time.Time
	BeforeUserID    model.UserID
	Limit           int
}

// ExamManagerMutation carries the common revision fence, authorization result,
// and audit attempt for one named relationship or ownership transition.
type ExamManagerMutation struct {
	ExamID           model.ExamID
	ActorUserID      model.UserID
	TargetUserID     model.UserID
	ManagerOverride  bool
	ExpectedRevision int64
	ChangedAt        time.Time
	AuditEventID     model.AuditEventID
	AuditAt          time.Time
	Notices          []ExamManagerMail
}

// ExamManagerMail is one fully prepared direct notification inserted with an
// Exam management transition. Ownership transfer carries exactly two entries;
// addition and removal carry exactly one.
type ExamManagerMail struct {
	Occurrence *model.MailOccurrence
	Delivery   *model.MailDelivery
	Job        *model.Job
}

type ExamManagerCommandResult struct {
	Exam     *model.Exam
	Manager  *model.ExamManager
	Replayed bool
}

type ExamArchive struct {
	ExamID           model.ExamID
	ActorUserID      model.UserID
	ManagerOverride  bool
	ExpectedRevision int64
	ArchivedAt       time.Time
	AuditEventID     model.AuditEventID
	AuditAt          time.Time
}

type ExamArchiveCommandResult struct {
	Value    *model.Exam
	Replayed bool
}

// ExamAuthoringCreation is the complete first Exam aggregate and the durable
// audit attempt that its successful transaction must complete.
type ExamAuthoringCreation struct {
	Exam         *model.Exam
	Draft        *model.ExamDraft
	Manager      *model.ExamManager
	AuditEventID model.AuditEventID
	AuditAt      time.Time
}

// ExamDraftTextUpdate changes only the authored text fields of one Draft.
// Presence is retained so later Draft fields remain outside this operation.
type ExamDraftTextUpdate struct {
	ExamID               model.ExamID
	ActorUserID          model.UserID
	ManagerOverride      bool
	ExpectedRevision     int64
	Title                *string
	InstructionsMarkdown *string
	UpdatedAt            time.Time
	AuditEventID         model.AuditEventID
	AuditAt              time.Time
}

// ExamDraftFocusLossUpdate replaces only the typed Focus Loss rule. The Store
// reconstructs and validates the complete policy so Connection Loss cannot be
// supplied or weakened by a caller.
type ExamDraftFocusLossUpdate struct {
	ExamID           model.ExamID
	ActorUserID      model.UserID
	ManagerOverride  bool
	ExpectedRevision int64
	FocusLoss        model.FocusLossPolicy
	UpdatedAt        time.Time
	AuditEventID     model.AuditEventID
	AuditAt          time.Time
}

// ExamDraftNativePolicyUpdate atomically replaces the exact native union under
// current Manager and Draft revision fences, preserving Focus/Connection Loss.
type ExamDraftNativePolicyUpdate struct {
	ExamID           model.ExamID
	ActorUserID      model.UserID
	ManagerOverride  bool
	ExpectedRevision int64
	NativePolicy     model.NativeSecurityPolicy
	UpdatedAt        time.Time
	AuditEventID     model.AuditEventID
	AuditAt          time.Time
}

// ExamDraftBrowserPolicyUpdate replaces the complete canonical Browser Policy
// value. Individual rules are never independently mutable Store entities.
type ExamDraftBrowserPolicyUpdate struct {
	// InstitutionOrigin is installation configuration supplied by the application, never request data.
	InstitutionOrigin string
	ExamID            model.ExamID
	ActorUserID       model.UserID
	ManagerOverride   bool
	ExpectedRevision  int64
	Policy            model.BrowserPolicy
	UpdatedAt         time.Time
	AuditEventID      model.AuditEventID
	AuditAt           time.Time
}

// ExamAuthoringStore owns atomic Exam authoring mutations and bounded reads.
// Create commits the Exam, its one Draft, creator-manager relation, audit
// success, and retry outcome together. Draft updates recheck the current
// manager relation unless an already-authorized override is explicit, reject
// archived or stale/no-op Drafts, and atomically commit only their supported
// fields, the Draft revision, audit success, and retry outcome. Exact replays
// return the committed projection without repeating the mutation. Reads remain
// bounded regardless of manager or future resource cardinality.
type ExamAuthoringStore interface {
	Create(context.Context, *ExamAuthoringCreation, *CommandIdempotency) (*ExamAuthoringCommandResult, error)
	UpdateDraftText(context.Context, *ExamDraftTextUpdate, *CommandIdempotency) (*ExamAuthoringCommandResult, error)
	UpdateDraftFocusLoss(context.Context, *ExamDraftFocusLossUpdate, *CommandIdempotency) (*ExamAuthoringCommandResult, error)
	UpdateDraftNativePolicy(context.Context, *ExamDraftNativePolicyUpdate, *CommandIdempotency) (*ExamAuthoringCommandResult, error)
	UpdateDraftBrowserPolicy(context.Context, *ExamDraftBrowserPolicyUpdate, *CommandIdempotency) (*ExamAuthoringCommandResult, error)
	// List returns at most Limit summaries in descending (UpdatedAt, ExamID)
	// order, strictly before the optional complete cursor pair. The adapter must
	// apply exact-unit, archive-state, role-scope, current exact Academic Unit
	// membership, and current Manager predicates inside the same bounded query;
	// override scope is a separate branch that requires neither relationship and
	// never creates one. Manager counts and Draft titles are part of that query,
	// not follow-up reads. List never returns an unrestricted intermediate set.
	List(context.Context, ExamListOptions) ([]ExamSummary, error)
	// Archive first resolves a matching committed idempotent outcome. Otherwise
	// one transaction locks the Exam, rechecks Manager membership unless an
	// explicit override was authorized, rejects archived or stale state, records
	// the immutable archive time and next revision without deleting rows,
	// completes the supplied audit attempt, and commits the replayable outcome.
	// Concurrent new commands yield one commit; losers return a stable conflict.
	// Transient effects are outside this operation and run only after commit.
	Archive(context.Context, *ExamArchive, *CommandIdempotency) (*ExamArchiveCommandResult, error)
	// ListManagers returns at most Limit provenance-only relationships in
	// descending (GrantedAt, UserID) order, strictly before the optional complete
	// cursor pair.
	ListManagers(context.Context, ExamManagerListOptions) ([]ExamManagerSummary, error)
	// AddManager, RemoveManager, and TransferOwner each lock the Exam and recheck
	// its active state, revision, actor relationship unless override is explicit,
	// and target eligibility where required. The relationship/owner transition,
	// next Exam revision, successful audit, and replayable outcome commit in one
	// transaction. Owner membership is also protected by a deferred database
	// constraint. Exact replays do not repeat the transition.
	AddManager(context.Context, *ExamManagerMutation, *CommandIdempotency) (*ExamManagerCommandResult, error)
	RemoveManager(context.Context, *ExamManagerMutation, *CommandIdempotency) (*ExamManagerCommandResult, error)
	TransferOwner(context.Context, *ExamManagerMutation, *CommandIdempotency) (*ExamManagerCommandResult, error)
	Access(context.Context, model.ExamID, model.UserID) (*ExamAccessSnapshot, error)
	Get(context.Context, model.ExamID, model.UserID) (*ExamAuthoringSnapshot, error)
	Resolve(context.Context, model.ExamID) (*model.Exam, error)
}
