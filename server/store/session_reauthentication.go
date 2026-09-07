// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package store

import (
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

// SessionPasswordReauthentication refreshes primary proof for the exact active
// password Session and access credential. The operation rechecks current local
// login policy, active User, credential validity and password revision under
// the same per-User fence as password replacement and Session revocation.
// PostgreSQL establishes the fresh proof instant after those fences. Initial
// authentication and MFA provenance, Session identity and expiry are unchanged.
// The fresh proof and required success audit commit atomically.
type SessionPasswordReauthentication struct {
	SessionID     model.SessionID
	CredentialID  model.SessionCredentialID
	UserID        model.UserID
	PasswordProof PasswordCredentialProof
	AuditEventID  model.AuditEventID
}

// SessionReauthenticationResult contains the current committed Session and the
// access hashes to invalidate only after successful audit and proof commit.
type SessionReauthenticationResult struct {
	Session           *model.Session
	AccessTokenHashes []string
}

// SessionExternalReauthentication consumes verified fresh provider proof for
// the exact Session, access credential and External Identity already bound to
// a consumed reauthentication state. The subject never enters audit output.
type SessionExternalReauthentication struct {
	StateID         model.ExternalLoginStateID
	ProviderID      string
	Subject         string
	AuthenticatedAt time.Time
}
