// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package store

import (
	"context"
	"crypto/sha256"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

const (
	CommandOutcomeMaxBytes = 64 * 1024
)

// CommandIdempotency is the bounded, transport-neutral identity of one
// retryable application command. Raw client keys and commands never cross the
// Store boundary.
type CommandIdempotency struct {
	UserID             model.UserID
	Operation          string
	KeyDigest          [sha256.Size]byte
	FingerprintVersion int
	Fingerprint        [sha256.Size]byte
	OutcomeVersion     int
	Retention          time.Duration
	Wait               time.Duration
	// OnboardingImportID and OnboardingImportRowNumber bind an administrative
	// import or progression command to the parent/row fence that must commit
	// with its ordinary mutation. They are absent for other commands.
	OnboardingImportID        model.OnboardingImportID
	OnboardingImportRowNumber int
	// Authorization is present only for closed academic-administration batch
	// commands. PostgreSQL rechecks these current permissions in the same
	// transaction as the ordinary aggregate mutation.
	Authorization *CommandAuthorization
	Batch         *CommandBatch
}

// CommandBatch binds one retained item outcome to its semantic duplicate
// group. DuplicateOfKeyDigest is absent for the canonical item. Duplicate is
// set by the Store when the retained disposition is non-canonical.
type CommandBatch struct {
	GroupDigest          [sha256.Size]byte
	DuplicateOfKeyDigest [sha256.Size]byte
	Duplicate            bool
}

// CommandAuthorization is the bounded terminal authority required by one
// academic-administration batch row. It contains no derived Role state;
// PostgreSQL resolves every action from current durable bindings and Roles.
type CommandAuthorization struct {
	Principal        model.Principal
	ScopeType        model.RoleScopeType
	ScopeID          string
	Actions          []model.Action
	DelegatedActions []model.Action
	// ClassMemberID binds a Class end or transfer to its relationship so
	// PostgreSQL can lock the affected User before hierarchy state, then resolve
	// and authorize the relationship's current Class.
	ClassMemberID model.ClassMemberID
	// RecipientUserID lets PostgreSQL preserve the canonical User-before-
	// hierarchy lock order for Class enrollment.
	RecipientUserID model.UserID
}

// CommandOutcomeStore owns bounded retention of completed client-command
// outcomes. Creation and replay remain part of each named aggregate mutation.
type CommandOutcomeStore interface {
	Has(context.Context, *CommandIdempotency) (bool, error)
	DeleteExpired(context.Context, int) (int64, error)
}
