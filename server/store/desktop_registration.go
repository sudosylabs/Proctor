// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package store

import (
	"context"

	"github.com/sudosylabs/proctor/server/model"
)

type DesktopRegistrationRevocation struct {
	RegistrationID model.DesktopRegistrationID
	UserID         model.UserID
	RevokedAt      int64
	AuditEventID   model.AuditEventID
	AuditAt        int64
}

type DesktopRegistrationRevocationResult struct {
	Registration   *model.DesktopRegistration
	Sessions       []*model.Session
	TokenHashes    []string
	AlreadyRevoked bool
}

// DesktopRegistrationStore owns safe self-listing and the atomic irreversible
// key, Session, and token-family revocation transition.
type DesktopRegistrationStore interface {
	Get(context.Context, model.DesktopRegistrationID) (*model.DesktopRegistration, error)
	ListByUser(context.Context, model.UserID) ([]*model.DesktopRegistration, error)
	Revoke(context.Context, *DesktopRegistrationRevocation) (*DesktopRegistrationRevocationResult, error)
}
