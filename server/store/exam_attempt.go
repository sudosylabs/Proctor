// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

const ExamAttemptConnectOperation = "exam.attempt.connect.v1"

// ExamAttemptConnect carries proposed identities for an atomic first
// admission. Persistence checks exact replay before it locks and materializes
// the current Sitting Revision's Starter Workspace. It derives authoritative
// membership, state, generation, database time, and bootstrap child identities.
// The raw continuity credential never crosses this boundary: the caller
// validates the canonical 32-byte base64url value and supplies only its digest.
type ExamAttemptConnect struct {
	SittingID                model.ExamSittingID
	CandidateUserID          model.UserID
	SessionID                model.SessionID
	AttemptID                model.ExamAttemptID
	WorkspaceID              model.ExamAttemptWorkspaceID
	ParticipationID          model.AttemptParticipationID
	ConnectionID             model.AttemptConnectionID
	ContinuityCredentialHash string
	AuditEventID             string
	AuditAt                  int64
}

// ExamAttemptConnectResult is the committed connection aggregate. Replayed
// denotes an exact command replay; FirstAdmission denotes creation of the
// stable Attempt and Workspace in this transaction.
type ExamAttemptConnectResult struct {
	Attempt        *model.ExamAttempt
	Workspace      *model.ExamAttemptWorkspace
	Participation  *ExamAttemptParticipationView
	Connection     *model.AttemptConnection
	ClassID        model.ClassID
	FirstAdmission bool
	// ConnectionOpened is true only when this command committed a new durable
	// Connection. It is false for exact replay and different-key convergence on
	// an already-open same-Session Connection, so transient effects remain
	// post-commit and non-duplicating.
	ConnectionOpened bool
	Replayed         bool
}

// ExamAttemptParticipationView is safe for application results and command
// outcomes. It deliberately omits the continuity credential hash.
type ExamAttemptParticipationView struct {
	ID              model.AttemptParticipationID
	AttemptID       model.ExamAttemptID
	State           model.AttemptParticipationState
	Generation      int64
	RenewalSequence int64
	StartedAt       time.Time
	UpdatedAt       time.Time
	LeaseExpiresAt  time.Time
	EndedAt         model.OptionalTime
	EndReason       model.AttemptParticipationEndReason
}

type ExamAttemptConnectionClose struct {
	ConnectionID    model.AttemptConnectionID
	CandidateUserID model.UserID
	SessionID       model.SessionID
	Reason          model.AttemptConnectionCloseReason
	AuditEventID    string
	AuditAt         int64
}

type ExamAttemptConnectionCloseResult struct {
	AttemptID       model.ExamAttemptID
	SittingID       model.ExamSittingID
	CandidateUserID model.UserID
	Connection      *model.AttemptConnection
	Changed         bool
}

// CandidateAttemptAccess is the sensitive selector shared by protected reads.
// The application hashes the presented raw credential before calling Store.
// Reads verify the owning Attempt, active unexpired Participation, open
// Connection, and readable Sitting state. They do not continuously poll Class
// membership after connection admission.
type CandidateAttemptAccess struct {
	AttemptID                model.ExamAttemptID
	CandidateUserID          model.UserID
	SessionID                model.SessionID
	ConnectionID             model.AttemptConnectionID
	ContinuityCredentialHash string
}

// CandidateExamResource omits managed-file identities and storage selectors.
type CandidateExamResource struct {
	ResourceID          model.ExamResourceID
	DisplayName         string
	DescriptionMarkdown string
	Position            int
	MediaType           model.ExamResourceMediaType
	SizeBytes           int64
	SHA256              string
}

// CandidateExamPresentation is the bounded protected projection resolved from
// the Sitting's current Revision. AdmissionRevisionID remains provenance only.
// The projection deliberately omits policy JSON and object selectors.
type CandidateExamPresentation struct {
	AttemptID            model.ExamAttemptID
	SittingID            model.ExamSittingID
	AdmissionRevisionID  model.ExamRevisionID
	CurrentRevisionID    model.ExamRevisionID
	Title                string
	InstructionsMarkdown string
	Resources            []CandidateExamResource
}

// CandidateAttemptWorkspaceItem is safe logical metadata; it exposes neither
// starter object identity nor a VFS key.
type CandidateAttemptWorkspaceItem struct {
	EntryID        model.AttemptWorkspaceEntryID
	Kind           model.StarterWorkspaceEntryKind
	Path           string
	ContentVersion model.WorkspaceContentVersion
	MediaType      string
	SizeBytes      int64
	SHA256         string
}

// CandidateWorkspaceListOptions defines the bounded canonical path catalog.
// Results sort ascending by (Path, EntryID); AfterPath and AfterEntryID are
// either both empty or the complete exclusive keyset cursor. Limit is 1..200.
type CandidateWorkspaceListOptions struct {
	Access       CandidateAttemptAccess
	AfterPath    string
	AfterEntryID model.AttemptWorkspaceEntryID
	Limit        int
}

type CandidateAttemptWorkspacePage struct {
	Items   []CandidateAttemptWorkspaceItem
	HasMore bool
}

// CandidateResourceContent is an internal, post-authorization selector for the
// file service. API serializers must never expose its managed-file identities.
type CandidateResourceContent struct {
	Resource       CandidateExamResource
	FileRevisionID model.FileRevisionID
	RenditionID    model.FileRenditionID
}

// CandidateWorkspaceContent is an internal, post-authorization selector for
// the VFS service. API serializers must never expose either object identity.
type CandidateWorkspaceContent struct {
	Entry           CandidateAttemptWorkspaceItem
	StorageOrigin   model.AttemptWorkspaceObjectStorage
	StarterObjectID model.StarterWorkspaceObjectID
	AttemptObjectID model.AttemptWorkspaceObjectID
	ContentVersion  model.WorkspaceContentVersion
}

type ExamAttemptManagerConnection struct {
	ID          model.AttemptConnectionID
	State       model.AttemptConnectionState
	OpenedAt    time.Time
	ClosedAt    model.OptionalTime
	CloseReason model.AttemptConnectionCloseReason
}

// ExamAttemptManagerSnapshot is the bounded authoritative refetch projection
// used after realtime hints. It contains no credential hash or authored data.
type ExamAttemptManagerSnapshot struct {
	Attempt             *model.ExamAttempt
	Workspace           *model.ExamAttemptWorkspace
	LatestParticipation *ExamAttemptParticipationView
	CurrentConnection   *ExamAttemptManagerConnection
}

// ExamAttemptManagerListOptions is Sitting-scoped and keyset bounded. Results
// sort descending by (CreatedAt, AttemptID). Limit accepts at most 201 for one
// manager-page look-ahead row.
type ExamAttemptManagerListOptions struct {
	ExamID          model.ExamID
	SittingID       model.ExamSittingID
	States          []model.ExamAttemptState
	BeforeCreatedAt time.Time
	BeforeAttemptID model.ExamAttemptID
	Limit           int
}

// ExamAttemptStore owns admission's deep atomic boundary: authoritative
// membership/state checks, first Attempt and logical Workspace bootstrap,
// generation fencing, Connection creation, audit, and idempotent outcome commit
// occur together. Exact command replays recover the stored outcome only after
// rechecking current session, Sitting capability, and exact-Class membership;
// every fresh connection performs the same current eligibility checks.
type ExamAttemptStore interface {
	Connect(context.Context, *ExamAttemptConnect, *CommandIdempotency) (*ExamAttemptConnectResult, error)
	CloseConnection(context.Context, *ExamAttemptConnectionClose) (*ExamAttemptConnectionCloseResult, error)
	Get(context.Context, model.ExamID, model.ExamAttemptID) (*ExamAttemptManagerSnapshot, error)
	List(context.Context, ExamAttemptManagerListOptions) ([]ExamAttemptManagerSnapshot, error)
	GetCandidatePresentation(context.Context, CandidateAttemptAccess) (*CandidateExamPresentation, error)
	ListCandidateWorkspace(context.Context, CandidateWorkspaceListOptions) (*CandidateAttemptWorkspacePage, error)
	ResolveCandidateResource(context.Context, CandidateAttemptAccess, model.ExamResourceID) (*CandidateResourceContent, error)
	ResolveCandidateWorkspaceFile(context.Context, CandidateAttemptAccess, model.AttemptWorkspaceEntryID) (*CandidateWorkspaceContent, error)
}
