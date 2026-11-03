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

// PasswordCredentialStore persists one encoded password hash per local user.
type PasswordCredentialStore interface {
	Save(context.Context, *model.PasswordCredential) (*model.PasswordCredential, error)
	GetByUser(context.Context, model.UserID) (*model.PasswordCredential, error)
	Rehash(context.Context, *PasswordCredentialRehash) error
	Enroll(context.Context, *PasswordCredentialEnrollment) (*AuthenticationMethodMutationResult, error)
	Remove(context.Context, *PasswordCredentialRemoval) (*AuthenticationMethodMutationResult, error)
	Change(context.Context, *PasswordCredentialChange) (*AuthenticationMethodMutationResult, error)
}

// PasswordCredentialRehash upgrades only the active credential whose encoded
// hash was verified. It preserves PasswordChangedAt and the password revision.
// A competing rehash, reset, removal, or replacement returns
// ErrPasswordCredentialChanged without writing anything. Unknown commit outcomes
// must not be retried automatically. Hashing occurs before this operation.
type PasswordCredentialRehash struct {
	ID               model.PasswordCredentialID
	UserID           model.UserID
	ExpectedHash     string
	ExpectedRevision int64
	PasswordHash     string
}

// PasswordCredentialProof identifies the password that was verified without
// transporting its encoded hash. It is valid only while ID and Revision still
// identify the User's active credential at the consuming transaction's commit.
type PasswordCredentialProof struct {
	ID       model.PasswordCredentialID
	Revision int64
}

func (p PasswordCredentialProof) IsValid() bool { return p.ID.IsValid() && p.Revision > 0 }

// PasswordMatcher reports whether the password being set matches one encoded
// hash. Implementations verify with the password hasher; an error aborts the
// credential change.
type PasswordMatcher interface {
	Matches(context.Context, string) (bool, error)
}

// PasswordReuseCheck refuses a new password that matches one of the User's
// Limit most recent passwords: the active and archived credentials and the
// retained history of superseded hashes, newest first. The Store reads them
// under the credential transaction's per-User locks and asks Matcher about
// each, so a concurrent change cannot slip between check and write; a match
// fails the operation with ErrPasswordReused. A credential replaced in place
// moves its old hash into history, and history is pruned to Limit entries per
// User in the same transaction. Limit zero disables the check and discards
// the User's history.
type PasswordReuseCheck struct {
	Limit   int
	Matcher PasswordMatcher
}

// Valid reports whether the check is complete enough to run.
func (c PasswordReuseCheck) Valid() bool {
	return c.Limit >= 0 && c.Limit <= MaximumPasswordHistory && (c.Limit == 0 || c.Matcher != nil)
}

// MaximumPasswordHistory bounds the retained password history per User.
const MaximumPasswordHistory = 24

type PasswordCredentialEnrollment struct {
	Credential   *model.PasswordCredential
	Reuse        PasswordReuseCheck
	Capabilities AccessDeploymentCapabilities
	AuditEventID model.AuditEventID
	AuditAt      time.Time
}

type PasswordCredentialRemoval struct {
	UserID           model.UserID
	Capabilities     AccessDeploymentCapabilities
	ChangedAt        time.Time
	RevocationReason model.SessionRevocationReason
	AuditEventID     model.AuditEventID
	AuditAt          time.Time
}

// PasswordCredentialChange is the named self-service transition that replaces
// an active User's existing password while local login is enabled. In one
// transaction it takes the per-User Session lock, advances the credential
// revision and change time at one database instant, invalidates outstanding
// password-reset tokens, revokes every other live Session of the User with
// RevocationReason (KeepSessionID stays live), queues the frozen
// password-changed notice, and completes the audit attempt. A User without an
// active credential returns ErrNotFound; an ineligible User returns a
// conflict. Hashing occurs before this operation.
type PasswordCredentialChange struct {
	UserID           model.UserID
	KeepSessionID    model.SessionID
	PasswordHash     string
	Reuse            PasswordReuseCheck
	RevocationReason model.SessionRevocationReason
	Occurrence       *model.MailOccurrence
	Delivery         *model.MailDelivery
	Job              *model.Job
	AuditEventID     model.AuditEventID
	AuditAt          time.Time
}
