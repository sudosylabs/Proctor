// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

const (
	ExamSubmissionSealOperation          = "exam.attempt.submit.v1"
	ExamSubmissionAutomaticSealOperation = "exam.attempt.seal_for_sitting_close.v1"
)

// ExamSubmissionSealAccess is the complete hash-only causal selector for a
// voluntary candidate seal. Persistence rechecks the current exact-Class
// membership, Open Sitting, active Attempt, exact active Participation and
// generation, owning Session-bound open Connection, expected acknowledged
// Workspace Cursor, and final client Focus Loss high-water under its locks.
// Sequence zero is valid when the client has emitted no Focus Loss claim.
type ExamSubmissionSealAccess struct {
	AttemptID                model.ExamAttemptID
	ParticipationID          model.AttemptParticipationID
	Generation               int64
	ConnectionID             model.AttemptConnectionID
	CandidateUserID          model.UserID
	SessionID                model.SessionID
	ContinuityCredentialHash string
	ExpectedWorkspaceCursor  int64
	FinalFocusLossSequence   int64
}

// ExamSubmissionSealTarget is the bounded preflight projection used to begin
// the candidate's Class-scoped audit attempt and address post-commit effects.
// It exposes no path, content, credential, Session, policy, or evidence.
type ExamSubmissionSealTarget struct {
	ExamID          model.ExamID
	SittingID       model.ExamSittingID
	ClassID         model.ClassID
	CandidateUserID model.UserID
	WorkspaceID     model.ExamAttemptWorkspaceID
	Replayed        bool
	SealAt          time.Time
}

// ExamSubmissionSeal supplies the server-proposed identity and safe audit
// attempt to the named atomic transition. SubmissionID is not part of the
// semantic command fingerprint: an outcome-unknown retry returns the single
// retained Submission rather than creating a second one.
type ExamSubmissionSeal struct {
	SubmissionID              model.SubmissionID
	Access                    ExamSubmissionSealAccess
	AuditEventID              string
	AuditAt                   int64
	Notice                    *PreparedMail
	ExpectedRecipientRevision int64
}

// ExamSubmissionReceipt is the complete candidate-safe immutable response. It
// intentionally omits the manifest, paths, content selectors, source signals,
// integrity gaps, credential, Session, and private review state.
type ExamSubmissionReceipt struct {
	SubmissionID    model.SubmissionID
	AttemptID       model.ExamAttemptID
	State           model.ExamAttemptState
	WorkspaceCursor int64
	ManifestDigest  string
	SubmittedAt     time.Time
}

// ExamSubmissionSealResult combines the safe receipt with exact safe scope
// needed for post-commit unbinding and targeted effects. Transport serializers
// expose Receipt only. Replayed suppresses duplicate effects.
type ExamSubmissionSealResult struct {
	Receipt         ExamSubmissionReceipt
	ExamID          model.ExamID
	SittingID       model.ExamSittingID
	ClassID         model.ClassID
	CandidateUserID model.UserID
	ParticipationID model.AttemptParticipationID
	Generation      int64
	ConnectionID    model.AttemptConnectionID
	Replayed        bool
}

// ExamSubmissionAutomaticSealListOptions selects one deterministic bounded
// page of unfinished Attempts for a Closing Sitting. AttemptID is the complete
// keyset cursor; paths, credentials, and candidate-authored content never
// leave persistence.
type ExamSubmissionAutomaticSealListOptions struct {
	SittingID      model.ExamSittingID
	AfterAttemptID model.ExamAttemptID
	Limit          int
}

// ExamSubmissionAutomaticSealTarget is the safe system-work identity for one
// already-created Attempt. Persistence resolves the latest Participation and
// Connection so active and previously suspended/disconnected work share the
// same terminal sealing operation.
type ExamSubmissionAutomaticSealTarget struct {
	ExamID          model.ExamID
	SittingID       model.ExamSittingID
	ClassID         model.ClassID
	AcademicUnitID  model.AcademicUnitID
	CandidateUserID model.UserID
	AttemptID       model.ExamAttemptID
	WorkspaceID     model.ExamAttemptWorkspaceID
	ParticipationID model.AttemptParticipationID
	Generation      int64
	ConnectionID    model.AttemptConnectionID
}

// ExamSubmissionAutomaticSeal supplies a server-proposed Submission identity
// and actor-less audit attempt. AttemptID is the natural idempotency key: a
// concurrent or crash replay returns the one retained Submission and completes
// the new audit without duplicating the immutable manifest.
type ExamSubmissionAutomaticSeal struct {
	Target                    ExamSubmissionAutomaticSealTarget
	SubmissionID              model.SubmissionID
	AuditEventID              string
	AuditAt                   int64
	Notice                    *PreparedMail
	ExpectedRecipientRevision int64
}

// ExamSubmissionAutomaticSealResult is the safe post-commit projection used
// for progress, realtime publication, and exact Connection unbinding.
type ExamSubmissionAutomaticSealResult struct {
	ExamSubmissionSealResult
	ConnectionClosed bool
}

// ExamSubmissionAutomaticSealPreparation reserves the PostgreSQL action time
// used by a fresh automatic receipt and reports whether the terminal aggregate
// already exists. The terminal mutation still rechecks all lifecycle state.
type ExamSubmissionAutomaticSealPreparation struct {
	Replayed bool
	SealAt   time.Time
}

// ExamSubmissionAuthorization is the minimal immutable ownership projection
// required to authorize one Submission resource without hydrating its
// protected manifest.
type ExamSubmissionAuthorization struct {
	SubmissionID   model.SubmissionID
	ExamID         model.ExamID
	SittingID      model.ExamSittingID
	AttemptID      model.ExamAttemptID
	AcademicUnitID model.AcademicUnitID
}

// ExamSubmissionManifestItem is manager-visible protected metadata for one
// sealed logical Entry. It deliberately omits opaque VFS object selectors.
type ExamSubmissionManifestItem struct {
	EntryID        model.AttemptWorkspaceEntryID
	Kind           model.StarterWorkspaceEntryKind
	Path           string
	ContentVersion model.WorkspaceContentVersion
	MediaType      string
	SizeBytes      int64
	SHA256         string
}

// ExamSubmissionManifestListOptions requests one immutable Entry-ID-keyset
// page. Limit is 1..model.ExamSubmissionManifestReadMaximum. The opaque public
// cursor contains AfterEntryID only; a protected Workspace Path never enters a
// URL, cursor, access log, or audit.
type ExamSubmissionManifestListOptions struct {
	SubmissionID model.SubmissionID
	AfterEntryID model.AttemptWorkspaceEntryID
	Limit        int
}

type ExamSubmissionManifestPage struct {
	SubmissionID    model.SubmissionID
	WorkspaceCursor int64
	ManifestDigest  string
	Items           []ExamSubmissionManifestItem
	HasMore         bool
}

// ExamSubmissionFileSelector is an internal post-authorization selector for
// protected in-application rendering. Exactly one origin ID is present. No API
// serializer or realtime event may expose either opaque identifier.
type ExamSubmissionFileSelector struct {
	Entry           ExamSubmissionManifestItem
	StorageOrigin   model.AttemptWorkspaceObjectStorage
	StarterObjectID model.StarterWorkspaceObjectID
	AttemptObjectID model.AttemptWorkspaceObjectID
	ContentVersion  model.WorkspaceContentVersion
}

// ExamSubmissionStore is the deep seam for voluntary sealing and immutable
// manager inspection.
//
// ResolveSealTarget performs no transition. It validates the full active
// selector for fresh work and also resolves only the exact retained causal
// selector of a committed Submission for replay-audit preflight. It cannot
// authorize a different command through terminal state.
//
// ResolveSealTarget reserves one millisecond PostgreSQL action time for the
// command. Seal is one named atomic operation. It first locks and revalidates
// the receipt User, including the expected recipient revision and active or
// ineligible lifecycle represented by the prepared notice, then follows the
// canonical Class/Attempt/Workspace order so no new mutation can pass. Under
// those locks it rechecks the Open Sitting, active Attempt, current exact-Class
// membership, active unexpired Participation generation, owning open
// Connection and credential, exact Workspace Cursor, and final Focus Loss
// high-water. It reconciles accepted Workspace and integrity tails; builds the
// canonical manifest from authoritative current Entry/object rows, never a
// client list; creates the sole Submission and its retained content references
// at the reserved action time; marks the Attempt Submitted; ends Participation
// and Connection; terminates integrity collection as Settled or Gapped;
// completes the supplied audit; inserts exactly one semantic receipt
// occurrence and queued or terminally suppressed delivery; and retains the
// bounded command outcome in the same transaction. Encrypted receipt payloads
// hold the durable primary-key fence through commit. A failed transaction
// changes none of those facts.
//
// An exact outcome replay rechecks the exact retained causal Access selector
// and returns the original receipt even though the committed transition is
// terminal. A changed selector or semantic input conflicts and can neither
// reopen the Attempt nor create another Submission. Expected cursor, final
// high-water, and the command idempotency fingerprint are nonnegative/bounded;
// credentials and raw idempotency keys never persist. Stable conflicts reuse
// attempt_workspace_cursor, focus_loss_sequence, exam_attempt_state,
// exam_sitting_state, attempt_participation_credential,
// attempt_participation_generation, attempt_participation_expired, and
// attempt_connection_closed.
// Fresh voluntary and automatic seals require the matching prepared notice
// and expected recipient revision; exact replay requires neither and never
// inserts another occurrence, delivery, or Job. PrepareAutomaticSeal performs
// no transition: it locks and resolves the exact automatic target, reports a
// retained replay, and reserves the PostgreSQL action time used by a fresh
// automatic seal. SealForSittingClose repeats the User-first recipient and
// lifecycle checks before the Attempt/Workspace transition, uses that same
// action time for Submission, audit, and receipt, and otherwise has the same
// atomic mail, audit, rollback, rekey-fence, and replay guarantees as Seal.
//
// Manager callers authorize externally through Resolve before Get,
// ListManifest, or ResolveFile. Get returns only the immutable aggregate
// header. ListManifest sorts ascending by EntryID and is bounded by the model
// manifest read maximum.
// ResolveFile accepts only a sealed file Entry and returns an internal opaque
// selector; no method creates a public or signed object URL.
type ExamSubmissionStore interface {
	ResolveSealTarget(context.Context, ExamSubmissionSealAccess) (*ExamSubmissionSealTarget, error)
	Seal(context.Context, *ExamSubmissionSeal, *CommandIdempotency) (*ExamSubmissionSealResult, error)
	ListAutomaticSealTargets(context.Context, ExamSubmissionAutomaticSealListOptions) ([]ExamSubmissionAutomaticSealTarget, error)
	PrepareAutomaticSeal(context.Context, ExamSubmissionAutomaticSealTarget) (*ExamSubmissionAutomaticSealPreparation, error)
	SealForSittingClose(context.Context, *ExamSubmissionAutomaticSeal) (*ExamSubmissionAutomaticSealResult, error)
	Resolve(context.Context, model.SubmissionID) (*ExamSubmissionAuthorization, error)
	Get(context.Context, model.SubmissionID) (*model.ExamSubmission, error)
	ListManifest(context.Context, ExamSubmissionManifestListOptions) (*ExamSubmissionManifestPage, error)
	ResolveFile(context.Context, model.SubmissionID, model.AttemptWorkspaceEntryID) (*ExamSubmissionFileSelector, error)
}
