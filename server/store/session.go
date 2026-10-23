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

// SessionRevocation is the complete durable input for revoking one session
// under an already-persisted audit attempt. AuditEventID must identify an
// attempt that is completed successfully before the revocation may commit.
// UserSession and UserLogout reasons forbid administrative mail; other reasons
// require the existing security-notice bundle. UserLogout completes a successful
// no-op audit if the caller's Session is already absent or revoked. Other single
// revocations return not found for an inactive or non-owned Session.
type SessionRevocation struct {
	SessionID    model.SessionID
	UserID       model.UserID
	Occurrence   *model.MailOccurrence
	Delivery     *model.MailDelivery
	DeliveryJob  *model.Job
	RevokedAt    time.Time
	Reason       model.SessionRevocationReason
	AuditEventID model.AuditEventID
	AuditAt      time.Time
}

// SessionRevocationResult contains the revoked session and token hashes needed
// for post-commit cache and realtime effects. Session is nil for a successful
// UserLogout no-op, which has no new post-commit effects.
type SessionRevocationResult struct {
	Session     *model.Session
	TokenHashes []string
}

// SessionExpiryEnforcementResult reports the authoritative outcome of checking
// one active Session against both of its durable expiry clocks. Expired is true
// only when this call committed the Session and credential-family revocation.
type SessionExpiryEnforcementResult struct {
	Session     *model.Session
	TokenHashes []string
	Expired     bool
}

// UserSessionsRevocation is the complete durable input for revoking every
// active session belonging to one user under an already-persisted audit
// attempt. UserAllSessions forbids administrative mail and command idempotency;
// other reasons retain the administrative notice contract. An empty set completes
// a successful audit with zero revoked sessions and no notice or transient effect.
type UserSessionsRevocation struct {
	UserID       model.UserID
	Occurrence   *model.MailOccurrence
	Delivery     *model.MailDelivery
	DeliveryJob  *model.Job
	RevokedAt    time.Time
	Reason       model.SessionRevocationReason
	AuditEventID model.AuditEventID
	AuditAt      time.Time
	Command      *CommandIdempotency
	Replayed     bool
	NoOp         bool
}

// UserSessionsRevocationResult contains the revoked sessions and token hashes
// needed for post-commit cache and realtime effects.
type UserSessionsRevocationResult struct {
	Sessions    []*model.Session
	TokenHashes []string
}

// SessionStore persists sessions and owns atomic session lifecycle changes.
// Active reads, activity updates, and expiry enforcement share UTC microsecond
// decision instants. A Session is inactive at either expiry deadline.
type SessionStore interface {
	ReauthenticateExternal(context.Context, *SessionExternalReauthentication) (*SessionReauthenticationResult, error)
	ReauthenticatePassword(context.Context, *SessionPasswordReauthentication) (*SessionReauthenticationResult, error)
	Save(context.Context, *SessionCreation) (*model.Session, []*model.SessionCredential, error)
	Get(context.Context, model.SessionID) (*model.Session, error)
	ListByUser(context.Context, *SessionPageQuery) ([]*model.Session, error)
	ListActiveByUser(context.Context, model.UserID, time.Time) ([]*model.Session, error)
	UpdateActivity(context.Context, *SessionActivityUpdate) error
	EnforceExpiry(context.Context, *SessionExpiryEnforcement) (*SessionExpiryEnforcementResult, error)
	// Withdraw revokes one Session the system itself just issued or refreshed
	// and must take back: the Desktop nonce could not be stored after the
	// audited exchange committed, or the User became inactive at refresh. It
	// has no actor and is a compensation rather than a decision, so the
	// Session's closed revocation reason is its durable record. Any other
	// reason is rejected.
	Withdraw(context.Context, *SessionWithdrawal) ([]string, error)
	Revoke(context.Context, *SessionRevocation) (*SessionRevocationResult, error)
	RevokeAllForUser(context.Context, *UserSessionsRevocation) (*UserSessionsRevocationResult, error)
	// PurgeRetired deletes retired Sessions and their credentials once their
	// grace has passed and nothing else references them.
	PurgeRetired(context.Context, *SessionPurge) (*SessionPurgeResult, error)
}

// SessionPageQuery pages one User's Sessions newest first by creation, with
// the Session ID as the tie-break. A non-zero ActiveAt keeps only Sessions
// that are unrevoked and unexpired at that UTC microsecond instant. The page
// continues after BeforeCreatedAt and BeforeID when both are set.
type SessionPageQuery struct {
	UserID          model.UserID
	ActiveAt        time.Time
	BeforeCreatedAt time.Time
	BeforeID        model.SessionID
	Limit           int
}

// MaximumSessionPageSize bounds one Session read: a page of at most 200
// Sessions and the probe row that says another page follows.
const MaximumSessionPageSize = 201

// MaximumSessionPurgeBatch bounds one Session or credential purge pass.
const MaximumSessionPurgeBatch = 1000

// SessionPurge examines Sessions retired (revoked, expired, or idle-expired)
// longer than Grace ago by PostgreSQL time, oldest first after After, and
// deletes those that no other durable record references, with their
// credentials.
type SessionPurge struct {
	Grace time.Duration
	Limit int
	After SessionPurgePosition
}

// SessionPurgePosition is the retirement order position of the last Session a
// purge pass examined.
type SessionPurgePosition struct {
	RetiredAt time.Time
	SessionID model.SessionID
}

// SessionPurgeResult reports one purge pass. Examined below the pass limit
// means no retired Session remains after Next.
type SessionPurgeResult struct {
	Examined    int
	Sessions    int64
	Credentials int64
	Next        SessionPurgePosition
}

// SessionCredentialPurge deletes credentials that expired or were revoked
// longer than Grace ago, including superseded credentials of live Sessions.
type SessionCredentialPurge struct {
	Grace time.Duration
	Limit int
}

// SessionCreation creates one Session and its credentials atomically. Password
// authentication requires current PasswordProof; external authentication forbids
// it. Creation and password reset/removal serialize on the same per-User lock:
// either creation commits first and reset revokes it, or stale proof is rejected
// with ErrPasswordCredentialChanged. Credential generation and password hashing
// occur before this operation. The login audit attempt completes atomically
// with the Session, so no issued Session exists without its success record.
type SessionCreation struct {
	ExternalLoginStateID model.ExternalLoginStateID
	Session              *model.Session
	Credentials          []*model.SessionCredential
	MaximumActive        int
	PasswordProof        PasswordCredentialProof
	// AuditEventID is the issuing login's persisted attempt. Creation completes
	// it with the Session projection in the same transaction.
	AuditEventID model.AuditEventID
	AuditAt      time.Time
}

type SessionRotation struct {
	Session             *model.Session
	AccessCredential    *model.SessionCredential
	RefreshCredential   *model.SessionCredential
	RevokedAccessHashes []string
	ReplayDetected      bool
	Expired             bool
}

// SessionCredentialStore resolves bearer credentials and atomically rotates
// refresh credentials with replay detection. Rotation compares the supplied
// UTC microsecond instant against exclusive credential and Session deadlines.
type SessionCredentialStore interface {
	GetSessionByTokenHash(
		context.Context,
		string,
		model.SessionCredentialKind,
	) (*model.SessionCredential, *model.Session, error)
	RotateRefresh(context.Context, *SessionRefreshRotation) (*SessionRotation, error)
	// PurgeRetired deletes retired credentials, including superseded
	// credentials of live Sessions, once their grace has passed.
	PurgeRetired(context.Context, *SessionCredentialPurge) (int64, error)
}

// SessionRefreshRotation is the input to SessionCredentialStore.RotateRefresh.
type SessionRefreshRotation struct {
	TokenHash     string
	Access        *model.SessionCredential
	Refresh       *model.SessionCredential
	Now           time.Time
	IdleExpiresAt time.Time
}

// SessionExpiryEnforcement is the input to SessionStore.EnforceExpiry.
type SessionExpiryEnforcement struct {
	ID     model.SessionID
	UserID model.UserID
	At     time.Time
}

// SessionActivityUpdate is the input to SessionStore.UpdateActivity.
type SessionActivityUpdate struct {
	ID             model.SessionID
	LastActivityAt time.Time
	IdleExpiresAt  time.Time
}

// SessionWithdrawal is the input to SessionStore.Withdraw.
type SessionWithdrawal struct {
	ID        model.SessionID
	UserID    model.UserID
	RevokedAt time.Time
	Reason    model.SessionRevocationReason
}
