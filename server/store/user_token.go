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

type EmailVerificationResult struct {
	Token *model.UserToken
	User  *model.User
}

type PasswordResetResult struct {
	Token               *model.UserToken
	User                *model.User
	PasswordCredential  *model.PasswordCredential
	RevokedSessions     []*model.Session
	RevokedAccessHashes []string
}

// UserTokenMailIssue is the named aggregate that replaces an active
// purpose-specific token and commits its successful audit and frozen recovery
// delivery intent as one durable transition.
type UserTokenMailIssue struct {
	Token      *model.UserToken
	Occurrence *model.MailOccurrence
	Delivery   *model.MailDelivery
	Job        *model.Job
	AuditEvent *model.AuditEvent
}

// UserEmailChange is the named transition that replaces the account address,
// invalidates prior verification credentials, and commits the new verification
// credential, the undo credential for the previous address, and both frozen
// credential deliveries atomically. UndoToken targets the previous address,
// the warning occurrence is the undo token, and both the undo token and the
// warning delivery live for WarningLifetime. Earlier undo tokens stay usable,
// so every address in a chain of changes can still restore itself.
// New credential, mail, and Job timestamps and deadlines use one database
// instant. Existing User and superseded token lifecycle timestamps never move
// backward; their creation times and superseded token expiries are preserved.
// Those lifecycle bounds do not shift the new credential or delivery deadlines.
type UserEmailChange struct {
	UserID                                    model.UserID
	ExpectedRevision                          int64
	NewEmail                                  string
	Token                                     *model.UserToken
	UndoToken                                 *model.UserToken
	TokenLifetime                             time.Duration
	WarningLifetime                           time.Duration
	WarningOccurrence, VerificationOccurrence *model.MailOccurrence
	WarningDelivery, VerificationDelivery     *model.MailDelivery
	WarningJob, VerificationJob               *model.Job
	AuditEventID                              model.AuditEventID
	AuditAt                                   time.Time
}

type UserEmailChangeResult struct {
	User      *model.User
	Token     *model.UserToken
	UndoToken *model.UserToken
}

// EmailChangeUndo is the named aggregate that redeems one email-change undo
// credential. In one transaction it takes the per-User Session lock, rechecks
// the token against the database clock, restores the token's target as the
// User's verified address, archives outstanding verification and reset
// credentials and suppresses their unsent mail, consumes every outstanding
// undo credential of the User, revokes all of the User's Sessions with
// RevocationReason, and inserts the success audit. An unknown, expired,
// consumed, or archived token, or an inactive User, returns ErrNotFound; a
// target address another active User now holds returns a conflict.
type EmailChangeUndo struct {
	TokenHash        string
	RevocationReason model.SessionRevocationReason
	AuditEvent       *model.AuditEvent
}

type EmailChangeUndoResult struct {
	User                *model.User
	RevokedSessions     []*model.Session
	RevokedAccessHashes []string
}

// PrivilegedEmailVerification records a high-assurance administrator override
// and its frozen user notice in one transaction.
type PrivilegedEmailVerification struct {
	UserID           model.UserID
	ExpectedRevision int64
	Occurrence       *model.MailOccurrence
	Delivery         *model.MailDelivery
	Job              *model.Job
	AuditEventID     model.AuditEventID
	AuditAt          time.Time
}

// PasswordResetCompletion is the named aggregate that consumes one reset
// credential, changes the password, revokes Sessions, completes the security
// audit, and records the password-changed notification atomically.
type PasswordResetCompletion struct {
	TokenHash        string
	PasswordHash     string
	Reuse            PasswordReuseCheck
	At               time.Time
	RevocationReason model.SessionRevocationReason
	AuditEvent       *model.AuditEvent
	Occurrence       *model.MailOccurrence
	Delivery         *model.MailDelivery
	Job              *model.Job
}

// UserTokenStore owns issuance and single-use consumption of purpose-specific
// account credentials. Consumption methods include their account mutation,
// session revocation where applicable, and terminal audit in one transaction.
type UserTokenStore interface {
	Issue(
		context.Context,
		*UserTokenMailIssue,
	) (*model.UserToken, error)
	ChangeEmail(context.Context, *UserEmailChange) (*UserEmailChangeResult, error)
	VerifyEmailPrivileged(context.Context, *PrivilegedEmailVerification) (*model.User, error)
	Get(context.Context, model.UserTokenID) (*model.UserToken, error)
	GetByHash(
		context.Context,
		string,
		model.UserTokenPurpose,
	) (*model.UserToken, error)
	ConsumeEmailVerification(context.Context, *EmailVerificationConsumption) (*EmailVerificationResult, error)
	ConsumeEmailChangeUndo(context.Context, *EmailChangeUndo) (*EmailChangeUndoResult, error)
	ConsumePasswordReset(
		context.Context,
		*PasswordResetCompletion,
	) (*PasswordResetResult, error)
}

// EmailVerificationConsumption is the input to UserTokenStore.ConsumeEmailVerification.
type EmailVerificationConsumption struct {
	TokenHash  string
	Now        time.Time
	AuditEvent *model.AuditEvent
}
