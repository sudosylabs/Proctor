// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	examattempt "github.com/sudosylabs/proctor/server/app/exam/attempt"
	appexecution "github.com/sudosylabs/proctor/server/app/execution"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type semanticTerminalAttempts interface {
	RecordIgnoredExecutionObservation(context.Context, examattempt.Call, examattempt.WorkspaceMutationAccess) (*store.ExecutionObservationTarget, error)
	ResolveExecutionObservation(context.Context, examattempt.Call, examattempt.WorkspaceMutationAccess) (*store.ExecutionObservationTarget, error)
}
type semanticTerminalExecution interface {
	AcquireObservationLease(context.Context, model.ExamAttemptID, model.ExecutionGrantID) (store.ExecutionLifecycleLease, error)
}

func (service *examAttemptTerminalService) applySemanticExecutionEvent(ctx context.Context, invocation Invocation, command OpenCandidateExamTerminalCommand, grantID model.ExecutionGrantID, event store.ExecutionObservation, observation appexecution.SemanticObservation) (resultErr error) {
	if event.Validate() != nil || event.Fence.GrantID != grantID {
		return appexecution.ErrInvalid
	}
	attempts, ok := service.attempts.(semanticTerminalAttempts)
	execution, supported := service.execution.(semanticTerminalExecution)
	if !ok || !supported {
		return appexecution.ErrUnavailable
	}
	lease, err := execution.AcquireObservationLease(ctx, command.Access.AttemptID, grantID)
	if err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		resultErr = errors.Join(resultErr, lease.Release(cleanup))
	}()
	access := examattempt.WorkspaceMutationAccess{CandidateAccess: command.Access, ParticipationID: command.ParticipationID, Generation: command.Generation, SourceGrantID: grantID, SourceObservation: &event}
	call := examattempt.NewCall(invocation.Principal(), invocation.RequestMetadata())
	target, err := attempts.ResolveExecutionObservation(ctx, call, access)
	if err != nil {
		return mapTerminalAttemptError(err)
	}
	if target == nil {
		return appexecution.ErrInvalid
	}
	if target.Ignored {
		if !target.Processed {
			if _, err := attempts.RecordIgnoredExecutionObservation(ctx, call, access); err != nil {
				return mapTerminalAttemptError(err)
			}
		}
		if err := lease.Validate(ctx); err != nil {
			return err
		}
		return observation.Acknowledge(ctx, event.HostSequence)
	}
	if target.Outcome == nil {
		keyDigest := sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%d", grantID.String(), event.Fence.EnvironmentEpoch, event.HostSequence)))
		key := "execution-" + hex.EncodeToString(keyDigest[:])
		switch event.Operation {
		case model.AttemptWorkspaceMutationCreateDirectory:
			_, err = service.attempts.CreateWorkspaceDirectory(ctx, call, examattempt.CreateWorkspaceDirectoryCommand{Access: access, Origin: examattempt.WorkspaceMutationOriginExecutionHost, Path: event.Path, IdempotencyKey: key})
		case model.AttemptWorkspaceMutationCreateFile, model.AttemptWorkspaceMutationReplaceFile:
			body, openErr := observation.OpenContent(ctx, event)
			if openErr != nil {
				return openErr
			}
			if body == nil {
				return appexecution.ErrInvalid
			}
			if event.Operation == model.AttemptWorkspaceMutationCreateFile {
				_, err = service.attempts.CreateWorkspaceFile(ctx, call, examattempt.CreateWorkspaceFileCommand{Access: access, Origin: examattempt.WorkspaceMutationOriginExecutionHost, Path: event.Path, MediaType: "application/octet-stream", ExpectedSHA256: event.Content.SHA256, Size: event.Content.Size, Body: body, IdempotencyKey: key})
			} else {
				_, err = service.attempts.ReplaceWorkspaceFile(ctx, call, examattempt.ReplaceWorkspaceFileCommand{Access: access, Origin: examattempt.WorkspaceMutationOriginExecutionHost, EntryID: target.EntryID, ExpectedPath: event.Path, ExpectedContentVersion: target.ExpectedContentVersion, MediaType: "application/octet-stream", ExpectedSHA256: event.Content.SHA256, Size: event.Content.Size, Body: body, IdempotencyKey: key})
			}
			err = errors.Join(err, body.Close())
		case model.AttemptWorkspaceMutationMoveEntry:
			_, err = service.attempts.MoveWorkspaceEntry(ctx, call, examattempt.MoveWorkspaceEntryCommand{Access: access, Origin: examattempt.WorkspaceMutationOriginExecutionHost, EntryID: target.EntryID, ExpectedPath: event.Path, DestinationPath: event.DestinationPath, IdempotencyKey: key})
		case model.AttemptWorkspaceMutationDeleteEntry:
			remove := examattempt.DeleteWorkspaceEntryCommand{Access: access, Origin: examattempt.WorkspaceMutationOriginExecutionHost, EntryID: target.EntryID, ExpectedPath: event.Path, ExpectedContentVersion: target.ExpectedContentVersion, IdempotencyKey: key}
			if event.Recursive != nil && *event.Recursive {
				remove.Recursive = true
				remove.ExpectedWorkspaceCursor = &target.WorkspaceCursor
			}
			_, err = service.attempts.DeleteWorkspaceEntry(ctx, call, remove)
		default:
			return appexecution.ErrInvalid
		}
		if err != nil {
			return mapTerminalAttemptError(err)
		}
		// The retained outcome is the single source for confirmation, including when
		// an earlier call committed but its response never reached this worker.
		target, err = attempts.ResolveExecutionObservation(ctx, call, access)
		if err != nil {
			return mapTerminalAttemptError(err)
		}
	}
	if target == nil || target.Outcome == nil || target.Outcome.Change.Validate() != nil {
		return appexecution.ErrInvalid
	}
	if err := lease.Validate(ctx); err != nil {
		return err
	}
	mutation := store.ExecutionProjectionMutation{Change: target.Outcome.Change, ExpectedContentVersion: target.ExpectedContentVersion, Content: event.Content}
	if err := observation.Confirm(ctx, event, mutation); err != nil {
		return err
	}
	if err := lease.Validate(ctx); err != nil {
		return err
	}
	return observation.Acknowledge(ctx, event.HostSequence)
}

func (service *examAttemptTerminalService) drainSemanticObservations(ctx context.Context, invocation Invocation, command OpenCandidateExamTerminalCommand, grantID model.ExecutionGrantID, observation appexecution.PendingSemanticObservation) error {
	// Bound one open attempt. A client may retry while the same epoch continues
	// processing; it must not force an unbounded synchronous catch-up loop.
	for range 128 {
		event, available, err := observation.NextPending(ctx)
		if err != nil {
			return err
		}
		if !available {
			return nil
		}
		if event.Semantic == nil {
			return appexecution.ErrInvalid
		}
		if err = service.applySemanticExecutionEvent(ctx, invocation, command, grantID, *event.Semantic, observation); err != nil {
			return err
		}
	}
	return appexecution.ErrProjectionPending
}
