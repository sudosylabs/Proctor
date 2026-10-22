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

type PasswordCredentialEnrollment struct {
	Credential   *model.PasswordCredential
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
