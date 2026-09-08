// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package store

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sudosylabs/proctor/server/model"
)

// ExecutionObservation is authenticated host evidence, not a candidate command.
// All fields preserve the original capture, including versions and transfer IDs.
// No path in this value may enter an ordinary log or an audit detail.
type ExecutionObservation struct {
	Fence                      model.ExecutionFence
	HostSequence               int64
	BasedOnWorkspaceCursor     int64
	Operation                  model.AttemptWorkspaceMutationKind
	NodeIdentity               string
	Kind                       model.StarterWorkspaceEntryKind
	Path                       string
	DestinationPath            string
	ExpectedContentVersion     model.WorkspaceContentVersion
	Recursive                  *bool
	Content                    *ExecutionProjectionContent
	OriginProjectionMutationID string
}

func (e ExecutionObservation) Validate() error {
	invalid := func() error { return fmt.Errorf("invalid execution observation") }
	validPath := func(path string) bool {
		normalized, err := model.NormalizeAttemptWorkspacePath(path)
		return err == nil && path == normalized
	}
	if e.Fence.Validate() != nil || e.HostSequence < 1 || e.HostSequence > 1<<53-1 || e.BasedOnWorkspaceCursor < 0 || e.BasedOnWorkspaceCursor > 1<<53-1 || !model.ValidExecutionEnvironmentEpoch(e.NodeIdentity) || !validPath(e.Path) {
		return invalid()
	}
	if e.Kind != model.StarterWorkspaceEntryFile && e.Kind != model.StarterWorkspaceEntryDirectory {
		return invalid()
	}
	if !e.ExpectedContentVersion.IsZero() && !e.ExpectedContentVersion.IsValid() {
		return invalid()
	}
	if e.OriginProjectionMutationID != "" && !model.IsValidId(e.OriginProjectionMutationID) {
		return invalid()
	}
	if e.Content != nil && (!model.ValidExecutionEnvironmentEpoch(e.Content.TransferID) || e.Content.Size < 0 || e.Content.Size > 10<<20 || len(e.Content.SHA256) != 64 || strings.Trim(e.Content.SHA256, "0123456789abcdef") != "") {
		return invalid()
	}
	switch e.Operation {
	case model.AttemptWorkspaceMutationCreateFile:
		if e.Kind != model.StarterWorkspaceEntryFile || e.Content == nil || !e.ExpectedContentVersion.IsZero() || e.DestinationPath != "" || e.Recursive != nil {
			return invalid()
		}
	case model.AttemptWorkspaceMutationCreateDirectory:
		if e.Kind != model.StarterWorkspaceEntryDirectory || e.Content != nil || !e.ExpectedContentVersion.IsZero() || e.DestinationPath != "" || e.Recursive != nil {
			return invalid()
		}
	case model.AttemptWorkspaceMutationReplaceFile:
		if e.Kind != model.StarterWorkspaceEntryFile || e.Content == nil || e.DestinationPath != "" || e.Recursive != nil {
			return invalid()
		}
	case model.AttemptWorkspaceMutationMoveEntry:
		if !validPath(e.DestinationPath) || e.Path == e.DestinationPath || e.Content != nil || e.Recursive != nil || (e.Kind == model.StarterWorkspaceEntryDirectory && !e.ExpectedContentVersion.IsZero()) {
			return invalid()
		}
	case model.AttemptWorkspaceMutationDeleteEntry:
		if e.Content != nil || e.DestinationPath != "" || (e.Kind == model.StarterWorkspaceEntryFile && e.Recursive != nil) || (e.Kind == model.StarterWorkspaceEntryDirectory && !e.ExpectedContentVersion.IsZero()) {
			return invalid()
		}
	default:
		return invalid()
	}
	body, err := json.Marshal(e)
	if err != nil || len(body) > 8192 {
		return invalid()
	}
	return nil
}

// ExecutionObservationTarget resolves only the event's original baseline or an
// exact preceding accepted outcome for this node. It never substitutes a newer
// competing Workspace version. ApplyMutation repeats this resolution atomically.
type ExecutionObservationTarget struct {
	Ignored                bool
	Processed              bool
	EntryID                model.AttemptWorkspaceEntryID
	ExpectedContentVersion model.WorkspaceContentVersion
	WorkspaceCursor        int64
	Outcome                *ExamAttemptWorkspaceMutationResult
}
