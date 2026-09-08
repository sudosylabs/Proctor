// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package execution

import (
	"context"
	"errors"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// controlEnvironment runs under the cross-node lifecycle lease. Prepared intent
// survives a failed/lost host response; exact retries do not reset the epoch or
// invent another revision. Current authority is checked again after host I/O.
func (s *Service) controlEnvironment(ctx context.Context, grant *model.ExecutionGrant, environment ControlledEnvironment, lease store.ExecutionLifecycleLease) (*model.ExecutionGrant, error) {
	for attempt := 0; attempt < 3; attempt++ {
		if err := lease.Validate(ctx); err != nil {
			return grant, s.projectionFailed(ctx, grant, err)
		}
		prepared, err := s.grants.PrepareControl(ctx, grant.ID, environment.Epoch(), s.now())
		if err != nil {
			return grant, err
		}
		grant = prepared
		if grant.ControlAcknowledgedRevision == grant.ControlRevision {
			return grant, nil
		}
		receipt, err := environment.Control(ctx, grant.Fence(), grant.DesiredControlState)
		if err != nil {
			return grant, err
		}
		if !receipt.Confirmed || receipt.Fence != grant.Fence() || receipt.State != grant.DesiredControlState {
			return grant, s.projectionFailed(ctx, grant, ErrConflict)
		}
		if err := lease.Validate(ctx); err != nil {
			return grant, s.projectionFailed(ctx, grant, err)
		}
		acknowledged, err := s.grants.AcknowledgeControl(ctx, receipt.Fence, receipt.State, s.now())
		if err == nil {
			return acknowledged, nil
		}
		if !store.IsConflict(err) {
			return grant, err
		}
		// A newer authority decision won during host I/O. Its new intent supersedes
		// this acknowledgement before any interaction is returned to the caller.
	}
	return grant, errors.Join(ErrConflict, s.releaseGrant(ctx, grant))
}

// ReconcileAttempt drives a post-commit control signal. It rereads durable
// authority instead of applying the state carried by a delayed event.
func (s *Service) ReconcileAttempt(ctx context.Context, attemptID model.ExamAttemptID) error {
	if !attemptID.IsValid() {
		return ErrInvalid
	}
	grant, err := s.grants.Current(ctx, attemptID)
	if store.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.convergeGrant(ctx, grant.ID)
}
