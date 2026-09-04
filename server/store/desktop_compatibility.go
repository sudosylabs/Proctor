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

// DesktopCompatibilityPolicyReplacement is the named atomic mutation for the
// Institution-owned compatibility policy and its required audit completion.
type DesktopCompatibilityPolicyReplacement struct {
	ActorID          model.UserID
	ExpectedRevision int64
	Settings         model.DesktopCompatibilityPolicySettings
	AuditEventID     string
	AuditAt          int64
}

// DesktopCompatibilityPolicyReplacementResult reports the authoritative
// policy and whether this invocation executed or replayed the mutation.
type DesktopCompatibilityPolicyReplacementResult struct {
	Policy   *model.DesktopCompatibilityPolicy
	Changed  bool
	Replayed bool
}

// DesktopCompatibilityPolicyStore owns the singleton policy and its
// revision-fenced idempotent replacement.
type DesktopCompatibilityPolicyStore interface {
	Get(context.Context) (*model.DesktopCompatibilityPolicy, error)
	Replace(
		context.Context,
		*DesktopCompatibilityPolicyReplacement,
		*CommandIdempotency,
	) (*DesktopCompatibilityPolicyReplacementResult, error)
}

// ErrDesktopCompatibilityPolicyRevisionConflict reports the current revision
// without exposing any build policy data.
type ErrDesktopCompatibilityPolicyRevisionConflict struct {
	CurrentRevision int64
}

func (e *ErrDesktopCompatibilityPolicyRevisionConflict) Error() string {
	return "desktop compatibility policy revision conflict"
}
