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

type PersonalAccessTokenResolution struct {
	Token *model.PersonalAccessToken
	User  *model.User
}

// PersonalAccessTokenSecurityNotice is the complete ordinary-mail intent for
// one successful PAT transition. It deliberately carries neither the
// one-time credential nor the persisted token hash or complete scope list.
type PersonalAccessTokenSecurityNotice struct {
	Occurrence *model.MailOccurrence
	Delivery   *model.MailDelivery
	Job        *model.Job
	// ExpiresAt is the bounded safe expiry rendered into the frozen payload.
	// Persistence verifies it equals the committed token deadline.
	ExpiresAt time.Time
}

type PersonalAccessTokenMutationKind string

const (
	PersonalAccessTokenMutationCreate  PersonalAccessTokenMutationKind = "create"
	PersonalAccessTokenMutationEnable  PersonalAccessTokenMutationKind = "enable"
	PersonalAccessTokenMutationDisable PersonalAccessTokenMutationKind = "disable"
	PersonalAccessTokenMutationRevoke  PersonalAccessTokenMutationKind = "revoke"
)

// PersonalAccessTokenMutationPreparation is the bounded durable attempt that
// precedes rendering. PostgreSQL assigns its ActionAt. The Audit draft contains
// only safe bounded metadata and is inserted as one terminal event only when
// the preparation succeeds, explicitly fails, or is reconciled after expiry.
type PersonalAccessTokenMutationPreparation struct {
	UserID     model.UserID
	TokenID    model.PersonalAccessTokenID
	Kind       PersonalAccessTokenMutationKind
	AuditEvent *model.AuditEvent
	Lifetime   time.Duration
}

type PreparedPersonalAccessTokenMutation struct {
	ID        model.PersonalAccessTokenPreparationID
	ActionAt  time.Time
	ExpiresAt time.Time
}

type PersonalAccessTokenMutationFailure struct {
	PreparationID model.PersonalAccessTokenPreparationID
	ErrorCode     string
}

type PersonalAccessTokenPreparationMaintenanceResult struct {
	Failed int
	More   bool
}

type PersonalAccessTokenMutationResult struct {
	Token *model.PersonalAccessToken
	Fresh bool
}

// PersonalAccessTokenCreationMutation couples the new hashed credential,
// terminal audit, and security notice in one transaction.
type PersonalAccessTokenCreationMutation struct {
	Token           *model.PersonalAccessToken
	MaximumActive   int
	MinimumLifetime time.Duration
	MaximumLifetime time.Duration
	PreparationID   model.PersonalAccessTokenPreparationID
	Notice          PersonalAccessTokenSecurityNotice
}

// PersonalAccessTokenStateMutation couples one fresh enable/disable state
// change, terminal audit, and matching security notice in one transaction.
type PersonalAccessTokenStateMutation struct {
	ID            model.PersonalAccessTokenID
	UserID        model.UserID
	Disabled      bool
	MaximumActive int
	PreparationID model.PersonalAccessTokenPreparationID
	Notice        PersonalAccessTokenSecurityNotice
}

// PersonalAccessTokenRevocation couples one fresh revocation, terminal audit,
// and matching security notice in one transaction.
type PersonalAccessTokenRevocation struct {
	ID            model.PersonalAccessTokenID
	UserID        model.UserID
	PreparationID model.PersonalAccessTokenPreparationID
	Notice        PersonalAccessTokenSecurityNotice
}

// PersonalAccessTokenStore persists hashed, explicitly scoped credentials.
// Resolve is authoritative and also performs the debounced last-used update.
// Decision instants use UTC microsecond precision; expiry is exclusive.
type PersonalAccessTokenStore interface {
	PrepareMutation(context.Context, *PersonalAccessTokenMutationPreparation) (*PreparedPersonalAccessTokenMutation, error)
	FailMutation(context.Context, *PersonalAccessTokenMutationFailure) error
	MaintainMutationPreparations(context.Context, int) (*PersonalAccessTokenPreparationMaintenanceResult, error)
	Create(context.Context, *PersonalAccessTokenCreationMutation) (*PersonalAccessTokenMutationResult, error)
	Get(context.Context, model.PersonalAccessTokenID) (*model.PersonalAccessToken, error)
	ListByUser(context.Context, model.UserID) ([]*model.PersonalAccessToken, error)
	Resolve(context.Context, string, time.Time, time.Duration) (*PersonalAccessTokenResolution, error)
	ChangeState(context.Context, *PersonalAccessTokenStateMutation) (*PersonalAccessTokenMutationResult, error)
	Revoke(context.Context, *PersonalAccessTokenRevocation) (*PersonalAccessTokenMutationResult, error)
}
