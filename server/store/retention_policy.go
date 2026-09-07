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

type RetentionPolicyReplacement struct {
	RetentionMutation
	ExpectedRevision int64
	Settings         model.RetentionPolicySettings
}

type RetentionPolicyReplacementResult struct {
	Policy   *model.RetentionPolicy
	Changed  bool
	Replayed bool
}

// RetentionPolicyStore owns the active Institution's Retention Policy. Archiving
// an Institution preserves its policy; creating a replacement initializes a new
// policy for that identity. Get never returns the archived Institution's policy.
// Get returns ErrNotFound before Institution creation; it never invents defaults.
// Replace serializes policy edits and administrator revocation, checks the
// actor's active system-administrator binding, current access credential and
// strong recent Session assurance at the post-lock database time, and commits the
// revision-fenced policy, successful audit completion, and idempotent outcome.
// A missing audit attempt rolls everything back. Exact retries replay the
// recorded result before the stale-revision check and complete a fresh audit;
// application authorization and the current credential, binding and assurance
// checks are required on every attempt, including replay and exact no-ops.
// Exact no-ops keep the revision and timestamp but still complete audit.
// This Store never deletes records or enqueues cleanup work.
type RetentionPolicyStore interface {
	Get(context.Context) (*model.RetentionPolicy, error)
	Replace(context.Context, *RetentionPolicyReplacement, *CommandIdempotency) (*RetentionPolicyReplacementResult, error)
}

type ErrRetentionPolicyRevisionConflict struct {
	CurrentRevision int64
}

func (e *ErrRetentionPolicyRevisionConflict) Error() string {
	return "retention policy revision conflict"
}
