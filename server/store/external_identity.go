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

type ExternalIdentityResolution struct {
	Identity    *model.ExternalIdentity
	User        *model.User
	Provisioned bool
}

type ExternalIdentityResolutionRequest struct {
	Identity                 *model.ExternalIdentity
	User                     *model.User
	Settings                 *model.UserSettingsDocument
	Capabilities             AccessDeploymentCapabilities
	AuditEvent               *model.AuditEvent
	DefaultProfilePictureJob *model.Job
}

// ExternalIdentityLink is the proof-completed, application-prepared command
// that attaches one immutable provider subject to the exact current User.
type ExternalIdentityLink struct {
	ExternalLoginStateID model.ExternalLoginStateID
	Identity             *model.ExternalIdentity
	Capabilities         AccessDeploymentCapabilities
	AuditEventID         model.AuditEventID
	AuditAt              int64
}

// ExternalIdentityUnlink removes one exact provider identity and revokes only
// Sessions that authenticated through that provider.
type ExternalIdentityUnlink struct {
	ID               model.ExternalIdentityID
	UserID           model.UserID
	Capabilities     AccessDeploymentCapabilities
	ChangedAt        int64
	RevocationReason model.SessionRevocationReason
	AuditEventID     model.AuditEventID
	AuditAt          int64
}

type AuthenticationMethodMutationResult struct {
	Identity           *model.ExternalIdentity
	PasswordCredential *model.PasswordCredential
	RevokedSessions    []*model.Session
	RevokedTokenHashes []string
}

// ExternalIdentityStore persists provider-subject links and owns the
// transaction that either resolves an existing link or provisions a new user
// and link without email-based account merging.
type ExternalIdentityStore interface {
	Save(context.Context, *model.ExternalIdentity) (*model.ExternalIdentity, error)
	Get(context.Context, model.ExternalIdentityID) (*model.ExternalIdentity, error)
	GetByProviderSubject(context.Context, string, string) (*model.ExternalIdentity, error)
	ListByUser(context.Context, model.UserID) ([]*model.ExternalIdentity, error)
	ResolveOrProvision(context.Context, *ExternalIdentityResolutionRequest) (*ExternalIdentityResolution, error)
	Link(context.Context, *ExternalIdentityLink) (*AuthenticationMethodMutationResult, error)
	Unlink(context.Context, *ExternalIdentityUnlink) (*AuthenticationMethodMutationResult, error)
}

// ExternalLoginStateStore persists hashed, browser-bound, one-use login
// transactions so any node may receive the provider callback.
type ExternalLoginStateStore interface {
	// Save applies lifetime to one authoritative database timestamp. Callers
	// provide no absolute creation or expiry deadline.
	Save(context.Context, *model.ExternalLoginState, time.Duration) (*model.ExternalLoginState, error)
	// SaveInvitationAdmission resolves a raw-claim digest against one currently
	// pending Invitation and atomically binds its identity to the new browser
	// transaction. The digest is used only for the lookup and is not copied into
	// the external-login state.
	SaveInvitationAdmission(context.Context, *model.ExternalLoginState, time.Duration, string) (*model.ExternalLoginState, error)
	GetByStateHash(context.Context, string) (*model.ExternalLoginState, error)
	Consume(
		context.Context,
		string,
		string,
		string,
	) (*model.ExternalLoginState, error)
	Maintain(context.Context, int) (*ExternalLoginStateMaintenanceResult, error)
}

// ExternalLoginStateMaintenanceResult reports one bounded database-time pass.
// Terminalized is the number of abandoned provider-connection audit attempts
// completed as failures; Purged is safe state metadata removed after retention.
type ExternalLoginStateMaintenanceResult struct {
	Terminalized int
	Purged       int
	More         bool
}
