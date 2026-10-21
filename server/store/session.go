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
	RevokedAt    int64
	Reason       model.SessionRevocationReason
	AuditEventID model.AuditEventID
	AuditAt      int64
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
	RevokedAt    int64
	Reason       model.SessionRevocationReason
	AuditEventID model.AuditEventID
	AuditAt      int64
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
	ListByUser(context.Context, model.UserID) ([]*model.Session, error)
	ListActiveByUser(context.Context, model.UserID, time.Time) ([]*model.Session, error)
	UpdateActivity(context.Context, model.SessionID, time.Time, time.Time) error
	EnforceExpiry(context.Context, model.SessionID, model.UserID, time.Time) (*SessionExpiryEnforcementResult, error)
	// Withdraw revokes one Session the system itself just issued or refreshed
	// and must take back: the Desktop nonce could not be stored after the
	// audited exchange committed, or the User became inactive at refresh. It
	// has no actor and is a compensation rather than a decision, so the
	// Session's closed revocation reason is its durable record. Any other
	// reason is rejected.
	Withdraw(context.Context, model.SessionID, model.UserID, int64, model.SessionRevocationReason) ([]string, error)
	Revoke(context.Context, *SessionRevocation) (*SessionRevocationResult, error)
	RevokeAllForUser(context.Context, *UserSessionsRevocation) (*UserSessionsRevocationResult, error)
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
	AuditAt      int64
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
	RotateRefresh(
		context.Context,
		string,
		*model.SessionCredential,
		*model.SessionCredential,
		time.Time,
		time.Time,
	) (*SessionRotation, error)
}
