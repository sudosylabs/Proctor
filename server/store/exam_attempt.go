// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

const (
	ExamAttemptConnectOperation             = "exam.attempt.connect.v1"
	ExamAttemptExpireParticipationOperation = "exam.attempt.expire_participation.v1"
	ExamAttemptReallowOperation             = "exam.attempt.reallow.v1"
)

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

// ExamAttemptParticipationRenewal is the complete sensitive selector for one
// application-level lease renewal. The application validates and hashes the
// canonical continuity credential immediately; raw credential material never
// crosses the Store seam. Generation and Sequence are client fences, while
// PostgreSQL supplies the only authoritative decision time.
type ExamAttemptParticipationRenewal struct {
	AttemptID                model.ExamAttemptID
	ParticipationID          model.AttemptParticipationID
	ConnectionID             model.AttemptConnectionID
	CandidateUserID          model.UserID
	SessionID                model.SessionID
	Generation               int64
	Sequence                 int64
	ContinuityCredentialHash string
}

// ExamAttemptParticipationRenewalResult is the bounded hash-free renewal
// acknowledgement. Duplicate is true only when Sequence was already the
// current accepted sequence; that case returns the existing authoritative
// times without extending the lease.
type ExamAttemptParticipationRenewalResult struct {
	AttemptID        model.ExamAttemptID
	ParticipationID  model.AttemptParticipationID
	Generation       int64
	AcceptedSequence int64
	DatabaseTime     time.Time
	LeaseExpiresAt   time.Time
	Duplicate        bool
}

// ExamAttemptParticipationExpiryDue is a bounded, hash-free candidate from
// the recurring database-time scan. LeaseExpiresAt is informational; only the
// conditional ExpireParticipation operation decides that the generation is
// still current and due.
type ExamAttemptParticipationExpiryDue struct {
	ExamID          model.ExamID
	SittingID       model.ExamSittingID
	ClassID         model.ClassID
	CandidateUserID model.UserID
	AttemptID       model.ExamAttemptID
	ParticipationID model.AttemptParticipationID
	Generation      int64
	LeaseExpiresAt  time.Time
}

// ExamAttemptParticipationExpiry supplies proposed append-only identities for
// the one conditional, atomic expiry outcome. PostgreSQL supplies decision and
// record times; application-node clocks never decide expiry.
type ExamAttemptParticipationExpiry struct {
	AttemptID       model.ExamAttemptID
	ParticipationID model.AttemptParticipationID
	Generation      int64
	EvidenceID      model.IntegrityEvidenceID
	FlagID          model.IntegrityFlagID
	SuspensionID    model.AttemptSuspensionID
	AuditEventID    string
	AuditAt         int64
}

// ExamAttemptSuspensionView excludes the manager's private reason while
// retaining enough state to publish bounded post-commit manager events.
type ExamAttemptSuspensionView struct {
	ID                model.AttemptSuspensionID
	AttemptID         model.ExamAttemptID
	ParticipationID   model.AttemptParticipationID
	FlagID            model.IntegrityFlagID
	Generation        int64
	State             model.AttemptSuspensionState
	Source            model.AttemptSuspensionSource
	CandidateReason   model.AttemptSuspensionCandidateReason
	StartedAt         time.Time
	EndedAt           model.OptionalTime
	ReallowedByUserID model.UserID
}

// ExamAttemptParticipationExpiryResult is the retained, safe committed
// aggregate. Replayed means another caller already completed this generation's
// exact expiry episode; callers publish transient effects only when false.
type ExamAttemptParticipationExpiryResult struct {
	ExamID          model.ExamID
	SittingID       model.ExamSittingID
	ClassID         model.ClassID
	CandidateUserID model.UserID
	Attempt         *model.ExamAttempt
	Participation   *ExamAttemptParticipationView
	Connection      *ExamAttemptManagerConnection
	// ConnectionClosed reports whether this expiry transition closed an open
	// Connection. It is false when transport teardown had already closed the
	// latest Connection; the flag and suspension still commit and publish.
	ConnectionClosed bool
	Evidence         *model.IntegrityEvidence
	Flag             *model.IntegrityFlag
	Suspension       *ExamAttemptSuspensionView
	DatabaseTime     time.Time
	Replayed         bool
}

// ExamAttemptReallow is one exact, revision-fenced manager decision. The
// private trimmed UTF-8 reason is retained only with Suspension provenance; it
// must not enter ordinary audit data, command outcomes, logs, or realtime.
type ExamAttemptReallow struct {
	ExamID                  model.ExamID
	SittingID               model.ExamSittingID
	AttemptID               model.ExamAttemptID
	SuspensionID            model.AttemptSuspensionID
	ActorUserID             model.UserID
	ManagerOverride         bool
	ExpectedAttemptRevision int64
	PrivateReason           string
	ChangedAt               time.Time
	AuditEventID            string
	AuditAt                 int64
}

// ExamAttemptReallowResult deliberately uses the private-reason-free
// Suspension view. Re-allow preserves all evidence and creates neither a new
// Participation nor continuity credential.
type ExamAttemptReallowResult struct {
	ExamID          model.ExamID
	SittingID       model.ExamSittingID
	ClassID         model.ClassID
	CandidateUserID model.UserID
	Attempt         *model.ExamAttempt
	Suspension      *ExamAttemptSuspensionView
	Replayed        bool
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

// CandidateWorkspaceListOptions defines one bounded manifest page. Results
// sort ascending by (Path, EntryID), but the opaque public cursor contains only
// ExpectedCursor and AfterEntryID: a Workspace Path must never enter a URL,
// query, access log, or cursor. Persistence resolves AfterEntryID's path under
// the exact expected aggregate cursor. ExpectedCursor=-1 and a zero
// AfterEntryID capture the current cursor for the first page; subsequent pages
// require a nonnegative exact ExpectedCursor and a valid AfterEntryID. An
// advanced aggregate returns RefreshRequired with no Items. Limit is 1..200.
type CandidateWorkspaceListOptions struct {
	Access         CandidateAttemptAccess
	ExpectedCursor int64
	AfterEntryID   model.AttemptWorkspaceEntryID
	Limit          int
}

type CandidateAttemptWorkspacePage struct {
	WorkspaceID     model.ExamAttemptWorkspaceID
	Cursor          int64
	Items           []CandidateAttemptWorkspaceItem
	HasMore         bool
	RefreshRequired bool
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
	ActiveSuspension    *ExamAttemptSuspensionView
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
	// RenewParticipation locks and validates the exact Attempt, active
	// Participation generation, owning candidate, Session-bound open Connection,
	// and credential digest. Sequence equal to the current accepted sequence is
	// an idempotent duplicate; an older sequence conflicts. A greater sequence
	// sets expiry to PostgreSQL decision time plus the fixed 20-second lease.
	// expires_at <= database_now returns the stable expired conflict and never
	// mutates or revives the generation. Stable conflict constraints are
	// attempt_participation_credential, attempt_participation_generation,
	// attempt_participation_sequence, and attempt_participation_expired.
	RenewParticipation(context.Context, *ExamAttemptParticipationRenewal) (*ExamAttemptParticipationRenewalResult, error)
	// ResolveParticipationExpiry returns the exact active generation only when
	// expires_at <= PostgreSQL current time, with the same safe scope projection
	// produced by the scanner. A late renewal uses it to begin actorless scoped
	// audit before invoking ExpireParticipation; it never performs the transition.
	// Exact selector/generation mismatches and a current unexpired lease fail
	// with stable not-found/conflict errors rather than exposing another record.
	ResolveParticipationExpiry(context.Context, model.ExamAttemptID, model.AttemptParticipationID, int64) (*ExamAttemptParticipationExpiryDue, error)
	// ListExpiredParticipations returns at most limit active generations whose
	// expiry is at or before PostgreSQL's current time, ordered by
	// (LeaseExpiresAt, ParticipationID). Limit is 1..200.
	ListExpiredParticipations(context.Context, int) ([]ExamAttemptParticipationExpiryDue, error)
	// ExpireParticipation is the single conditional operation shared by late
	// renewal and the recurring scan. It locks the exact generation and, when
	// expires_at <= PostgreSQL now, atomically ends it as lease_expired, closes
	// its open Connection, creates neutral Connection Loss evidence and the one
	// generation-scoped flag, suspends the Attempt, completes audit, and retains
	// the outcome. Exact later calls rehydrate that outcome. A generation that
	// was renewed before the lock conflicts as attempt_participation_not_expired
	// without partial state; a non-current generation conflicts as
	// attempt_participation_generation.
	ExpireParticipation(context.Context, *ExamAttemptParticipationExpiry) (*ExamAttemptParticipationExpiryResult, error)
	// ReallowAttempt locks the exact active Suspension and Attempt revision,
	// rechecks current Exam management authority unless an authorized override
	// is explicit, closes only that episode, returns the Attempt to active, and
	// commits audit and the idempotent outcome atomically. It preserves all
	// evidence and creates no Participation or credential. Exact retries return
	// the retained result without a second revision change after current
	// authority is rechecked. Stable conflicts are exam_attempt_revision,
	// attempt_suspension_active, exam_attempt_state, and exam_sitting_state.
	ReallowAttempt(context.Context, *ExamAttemptReallow, *CommandIdempotency) (*ExamAttemptReallowResult, error)
	CloseConnection(context.Context, *ExamAttemptConnectionClose) (*ExamAttemptConnectionCloseResult, error)
	Get(context.Context, model.ExamID, model.ExamAttemptID) (*ExamAttemptManagerSnapshot, error)
	List(context.Context, ExamAttemptManagerListOptions) ([]ExamAttemptManagerSnapshot, error)
	GetCandidatePresentation(context.Context, CandidateAttemptAccess) (*CandidateExamPresentation, error)
	ResolveCandidateResource(context.Context, CandidateAttemptAccess, model.ExamResourceID) (*CandidateResourceContent, error)
}
