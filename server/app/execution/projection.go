// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package execution

import (
	"bytes"
	"context"
	"errors"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// projectEnvironment owns one bounded journal page while holding the lifecycle
// lease. A durable pending request is replayed byte-for-byte after interruption.
// The caller converges current control first; frozen guests retain occupancy.
func (s *Service) projectEnvironment(ctx context.Context, grant *model.ExecutionGrant, environment ProjectionEnvironment, lease store.ExecutionLifecycleLease) (*model.ExecutionGrant, error) {
	if environment.Epoch() != grant.EnvironmentEpoch || grant.LifecyclePending {
		return grant, s.projectionFailed(ctx, grant, ErrConflict)
	}
	if grant.DesiredControlState != model.ExecutionControlRunning || grant.ControlAcknowledgedRevision != grant.ControlRevision {
		return grant, ErrProjectionPending
	}
	pending, err := s.grants.PendingProjection(ctx, grant.ID)
	if err != nil && !store.IsNotFound(err) {
		return grant, err
	}
	if pending != nil {
		if pending.Fence != grant.Fence() {
			return grant, s.projectionFailed(ctx, grant, ErrConflict)
		}
		return s.applyProjectionRequest(ctx, grant, environment, lease, *pending)
	}
	if grant.WorkspacePending {
		return grant, s.projectionFailed(ctx, grant, ErrConflict)
	}
	request := store.ExecutionProjectionRequest{Fence: grant.Fence(), MutationID: model.NewId(), FromWorkspaceCursor: grant.AppliedWorkspaceCursor, ExpectedHostCursor: grant.ProcessedHostSequence}
	if grant.State == model.ExecutionGrantReserved {
		snapshot, err := s.grants.WorkspaceSnapshot(ctx, grant.AttemptID)
		if err != nil {
			return grant, err
		}
		if snapshot == nil || len(snapshot.Nodes) > model.AttemptWorkspaceMaximumEntries {
			return grant, ErrInvalid
		}
		request.Initial, request.ThroughWorkspaceCursor = true, snapshot.Cursor
		var total int64
		for _, node := range snapshot.Nodes {
			entry := store.ExecutionProjectionEntry{EntryID: node.EntryID, Kind: node.Kind, Path: node.Path, ContentVersion: node.ContentVersion}
			if node.Kind == model.StarterWorkspaceEntryFile {
				total += node.SizeBytes
				if total > model.AttemptWorkspaceMaximumTotalBytes {
					return grant, ErrInvalid
				}
				content, err := s.uploadProjectionNode(ctx, environment, grant.Fence(), node)
				if err != nil {
					return grant, err
				}
				entry.Content = content
			}
			request.Entries = append(request.Entries, entry)
		}
	} else {
		page, err := s.grants.WorkspaceChanges(ctx, grant.Fence(), 128)
		if err != nil {
			return grant, err
		}
		if page == nil || page.RefreshRequired {
			return grant, s.projectionFailed(ctx, grant, ErrObservationLost)
		}
		if len(page.Changes) == 0 {
			return grant, nil
		}
		var total int64
		for _, change := range page.Changes {
			mutation := store.ExecutionProjectionMutation{Change: change.Change, ExpectedContentVersion: change.ExpectedContentVersion}
			if change.Content != nil {
				if total+change.Content.SizeBytes > model.AttemptWorkspaceMaximumTotalBytes {
					break
				}
				content, err := s.uploadProjectionNode(ctx, environment, grant.Fence(), *change.Content)
				if err != nil {
					return grant, err
				}
				mutation.Content = content
				total += change.Content.SizeBytes
			}
			request.Mutations = append(request.Mutations, mutation)
			request.ThroughWorkspaceCursor = change.Change.Cursor
		}
	}
	return s.applyProjectionRequest(ctx, grant, environment, lease, request)
}

func (s *Service) uploadProjectionNode(ctx context.Context, environment ProjectionEnvironment, fence model.ExecutionFence, node store.ExecutionWorkspaceNode) (*store.ExecutionProjectionContent, error) {
	// projectNode verifies the pinned immutable object's size and hash. No live
	// Workspace lookup or guest Open(path) can substitute a newer version here.
	value, err := s.projectNode(ctx, node)
	if err != nil {
		return nil, err
	}
	content, err := environment.UploadProjectionContent(ctx, fence, bytes.NewReader(value.Data))
	if err != nil {
		return nil, err
	}
	if !model.ValidExecutionEnvironmentEpoch(content.TransferID) || content.Size != node.SizeBytes || content.SHA256 != node.SHA256 {
		return nil, ErrConflict
	}
	return &content, nil
}

func (s *Service) applyProjectionRequest(ctx context.Context, grant *model.ExecutionGrant, environment ProjectionEnvironment, lease store.ExecutionLifecycleLease, request store.ExecutionProjectionRequest) (*model.ExecutionGrant, error) {
	if err := lease.Validate(ctx); err != nil {
		return grant, s.projectionFailed(ctx, grant, err)
	}
	effect, err := s.grants.PrepareProjection(ctx, request)
	if err != nil {
		return grant, err
	}
	if effect == nil || (effect.Request == nil) == (effect.Receipt == nil) {
		return grant, s.projectionFailed(ctx, grant, ErrConflict)
	}
	var receipt store.ExecutionProjectionReceipt
	if effect.Receipt != nil {
		receipt = *effect.Receipt
	} else {
		receipt, err = environment.ApplyProjection(ctx, *effect.Request)
		if err != nil {
			if errors.Is(err, ErrHostCursorConflict) {
				if err := lease.Validate(ctx); err != nil {
					return grant, s.projectionFailed(ctx, grant, err)
				}
				refused, refusalErr := s.grants.RejectProjection(ctx, request.Fence, request.MutationID)
				if refusalErr != nil {
					return grant, refusalErr
				}
				return refused, ErrProjectionPending
			}
			if errors.Is(err, ErrConflict) || errors.Is(err, ErrRevoked) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrInvalid) || errors.Is(err, ErrObservationLost) {
				return grant, s.projectionFailed(ctx, grant, err)
			}
			// Cursor/version conflicts require observation reconciliation; uncertain
			// transport errors retain the exact request. Neither overwrites guest work.
			return grant, err
		}
	}
	if receipt.Fence != request.Fence || receipt.MutationID != request.MutationID || receipt.AppliedWorkspaceCursor != request.ThroughWorkspaceCursor || receipt.HostCursor != request.ExpectedHostCursor {
		return grant, s.projectionFailed(ctx, grant, ErrConflict)
	}
	if err := lease.Validate(ctx); err != nil {
		return grant, s.projectionFailed(ctx, grant, err)
	}
	completed, err := s.grants.CompleteProjection(ctx, receipt)
	if err != nil {
		return grant, err
	}
	return completed, nil
}

// resumeProjection keeps a healthy existing epoch and process. It never calls
// Ensure on a bound epoch: a missing original handle cannot create a successor.
func (s *Service) resumeProjection(ctx context.Context, id model.ExecutionGrantID) (result *model.ExecutionGrant, resultErr error) {
	lease, err := s.grants.AcquireLifecycleLease(ctx, id)
	if err != nil {
		return nil, err
	}
	defer func() { resultErr = errors.Join(resultErr, releaseLifecycleLease(ctx, lease)) }()
	convergence, err := s.grants.CurrentForReconciliation(ctx, id)
	if err != nil {
		return nil, err
	}
	grant := convergence.Grant
	environment, err := s.existingProjection(ctx, grant)
	if err != nil {
		return grant, errors.Join(err, s.releaseGrant(ctx, grant))
	}
	grant, err = s.controlEnvironment(ctx, grant, environment, lease)
	if err != nil {
		return grant, err
	}
	if grant.DesiredControlState == model.ExecutionControlRevoked {
		return grant, errors.Join(ErrRevoked, s.releaseGrant(ctx, grant))
	}
	if grant.DesiredControlState != model.ExecutionControlRunning {
		return grant, ErrInteractionBlocked
	}
	grant, err = s.projectEnvironment(ctx, grant, environment, lease)
	if err != nil {
		return grant, err
	}
	convergence, err = s.grants.CurrentForReconciliation(ctx, id)
	if err != nil {
		return grant, err
	}
	if convergence.Grant.WorkspacePending || convergence.WorkspaceCursor > grant.AppliedWorkspaceCursor {
		return grant, ErrProjectionPending
	}
	return grant, nil
}

func (s *Service) existingProjection(ctx context.Context, grant *model.ExecutionGrant) (ProjectionEnvironment, error) {
	if grant == nil || grant.Validate() != nil || grant.State == model.ExecutionGrantReleased || grant.EnvironmentEpoch == "" {
		return nil, ErrUnavailable
	}
	environment, err := s.hosts.Existing(ctx, grant.HostID, Spec{ID: grant.ID.String(), Image: grant.Image, Network: Network(grant.Network)})
	if err != nil {
		return nil, err
	}
	projection, ok := environment.(ProjectionEnvironment)
	if !ok || projection.Epoch() != grant.EnvironmentEpoch {
		return nil, ErrUnavailable
	}
	return projection, nil
}

// AcquireObservationLease serializes one semantic event's durable acceptance
// and host confirmation against projection. The caller must release the lease.
func (s *Service) AcquireObservationLease(ctx context.Context, attemptID model.ExamAttemptID, id model.ExecutionGrantID) (store.ExecutionLifecycleLease, error) {
	if !attemptID.IsValid() || !id.IsValid() {
		return nil, ErrInvalid
	}
	lease, err := s.grants.AcquireLifecycleLease(ctx, id)
	if err != nil {
		return nil, err
	}
	grant, err := s.grants.Current(ctx, attemptID)
	if err != nil || grant == nil || grant.ID != id || grant.State != model.ExecutionGrantReady || grant.EnvironmentEpoch == "" {
		return nil, errors.Join(ErrUnavailable, err, releaseLifecycleLease(ctx, lease))
	}
	return lease, nil
}

func (s *Service) observeProjection(ctx context.Context, grant *model.ExecutionGrant) (result SemanticObservation, resultErr error) {
	lease, err := s.AcquireObservationLease(ctx, grant.AttemptID, grant.ID)
	if err != nil {
		return nil, err
	}
	defer func() {
		resultErr = errors.Join(resultErr, releaseLifecycleLease(ctx, lease))
		if resultErr != nil && result != nil {
			_ = result.Close()
			result = nil
		}
	}()
	current, err := s.grants.PrepareControl(ctx, grant.ID, grant.EnvironmentEpoch, s.now())
	if err != nil {
		return nil, err
	}
	if current.DesiredControlState != model.ExecutionControlRunning || current.ControlAcknowledgedRevision != current.ControlRevision || current.LifecyclePending {
		return nil, ErrInteractionBlocked
	}
	environment, err := s.existingProjection(ctx, current)
	if err != nil {
		return nil, err
	}
	observer, ok := environment.(ObservationEnvironment)
	if !ok {
		return nil, ErrUnavailable
	}
	if err = lease.Validate(ctx); err != nil {
		return nil, err
	}
	result, err = observer.Observe(ctx, current.Fence(), current.ProcessedHostSequence)
	if err != nil {
		return result, err
	}
	if err = lease.Validate(ctx); err != nil {
		return result, err
	}
	latest, err := s.grants.PrepareControl(ctx, grant.ID, grant.EnvironmentEpoch, s.now())
	if err != nil {
		return result, err
	}
	if latest.Fence() != current.Fence() || latest.ControlAcknowledgedRevision != latest.ControlRevision || latest.DesiredControlState != model.ExecutionControlRunning {
		return result, ErrUnavailable
	}
	return result, nil
}

// ValidateTerminalInteraction checks current durable authority without requiring
// Workspace catch-up on each byte: healthy guest processes keep running while
// unrelated acknowledged saves are projected.
func (s *Service) ValidateTerminalInteraction(ctx context.Context, attemptID model.ExamAttemptID, id model.ExecutionGrantID, epoch string) error {
	if !attemptID.IsValid() || !id.IsValid() || !model.ValidExecutionEnvironmentEpoch(epoch) {
		return ErrInvalid
	}
	grant, err := s.grants.Current(ctx, attemptID)
	if err != nil {
		return err
	}
	if grant == nil || grant.ID != id || grant.EnvironmentEpoch != epoch || grant.State != model.ExecutionGrantReady {
		return ErrUnavailable
	}
	current, err := s.grants.PrepareControl(ctx, id, epoch, s.now())
	if err != nil {
		return err
	}
	if current.DesiredControlState != model.ExecutionControlRunning || current.ControlAcknowledgedRevision != current.ControlRevision || current.LifecyclePending {
		return ErrInteractionBlocked
	}
	return nil
}
