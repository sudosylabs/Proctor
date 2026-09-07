// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type sessionRevocationStore interface {
	RevokeWithAudit(context.Context, *store.SessionRevocation) (*store.SessionRevocationResult, error)
	RevokeAllForUserWithAudit(context.Context, *store.UserSessionsRevocation) (*store.UserSessionsRevocationResult, error)
}

type sessionRevocationEffects interface {
	SessionsRevoked(context.Context, string, []string, []string)
}

// sessionRevocationCoordinator commits an already-authorized revocation and
// publishes only its new durable effects. Callers retain ownership checks,
// audit intent, public errors, administrative mail and command preparation.
// Account and password transitions keep their separate atomic Store operations.
type sessionRevocationCoordinator struct {
	sessions sessionRevocationStore
	audit    mutationAuditor
	effects  sessionRevocationEffects
	now      func() time.Time
}

func (c sessionRevocationCoordinator) revokeOne(
	ctx context.Context,
	attempt mutationAttempt,
	input *store.SessionRevocation,
	mapError func(error) error,
) error {
	result, err := runAuditedMutation(ctx, c.audit, attempt, c.now,
		func(ctx context.Context, reference mutationAttemptReference) (*store.SessionRevocationResult, error) {
			input.RevokedAt = reference.MutationAtMillis
			input.AuditEventID, input.AuditAt = reference.ID, reference.MutationAtMillis
			return c.sessions.RevokeWithAudit(ctx, input)
		}, mapError)
	if err != nil {
		return err
	}
	// The Store represents an already-absent logout with no affected Session;
	// other single revocations retain their owning use case's not-found error.
	if result != nil && result.Session != nil {
		c.publish(ctx, input.UserID, []*model.Session{result.Session}, result.TokenHashes)
	}
	return nil
}

func (c sessionRevocationCoordinator) revokeAll(
	ctx context.Context,
	attempt mutationAttempt,
	input *store.UserSessionsRevocation,
	mapError func(error) error,
	unchanged *bool,
) error {
	result, err := runAuditedMutation(ctx, c.audit, attempt, c.now,
		func(ctx context.Context, reference mutationAttemptReference) (*store.UserSessionsRevocationResult, error) {
			input.RevokedAt = reference.MutationAtMillis
			input.AuditEventID, input.AuditAt = reference.ID, reference.MutationAtMillis
			value, err := c.sessions.RevokeAllForUserWithAudit(ctx, input)
			// Batch callers retain the Store's replay/no-op observation even when
			// the mutation fails; an unavailable audit attempt leaves it untouched.
			if unchanged != nil {
				*unchanged = input.Replayed || input.NoOp
			}
			return value, err
		}, mapError)
	if err != nil {
		return err
	}
	if result != nil && !input.Replayed && !input.NoOp {
		c.publish(ctx, input.UserID, result.Sessions, result.TokenHashes)
	}
	return nil
}

func (c sessionRevocationCoordinator) publish(
	ctx context.Context,
	userID string,
	sessions []*model.Session,
	hashes []string,
) {
	if len(sessions) != 0 {
		c.effects.SessionsRevoked(ctx, userID, sessionIds(sessions), hashes)
	}
}
