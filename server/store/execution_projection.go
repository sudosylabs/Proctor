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

// ExecutionProjectionContent is a single-purpose host upload receipt, never a
// VFS key or URL. It may be replayed only at its request's exact host fence.
type ExecutionProjectionContent struct {
	TransferID string
	Size       int64
	SHA256     string
}

type ExecutionProjectionEntry struct {
	EntryID        model.AttemptWorkspaceEntryID
	Kind           model.StarterWorkspaceEntryKind
	Path           string
	ContentVersion model.WorkspaceContentVersion
	Content        *ExecutionProjectionContent
}

type ExecutionProjectionMutation struct {
	Change                 model.AttemptWorkspaceJournalEntry
	ExpectedContentVersion model.WorkspaceContentVersion
	Content                *ExecutionProjectionContent
}

// ExecutionProjectionRequest is the complete bounded effect retained before
// host mutation. Initial requests have Entries and no Mutations; incremental
// requests have consecutive Mutations and no Entries. Immutable upload handles
// are retained unchanged on retry, even when the caller lost the host receipt.
type ExecutionProjectionRequest struct {
	Fence                  model.ExecutionFence
	MutationID             string
	FromWorkspaceCursor    int64
	ThroughWorkspaceCursor int64
	ExpectedHostCursor     int64
	Initial                bool
	Entries                []ExecutionProjectionEntry
	Mutations              []ExecutionProjectionMutation
}

type ExecutionProjectionReceipt struct {
	Fence                  model.ExecutionFence
	MutationID             string
	AppliedWorkspaceCursor int64
	HostCursor             int64
}

type ExecutionProjectionEffect struct {
	Rejected bool
	Request  *ExecutionProjectionRequest
	Receipt  *ExecutionProjectionReceipt
}

func (request ExecutionProjectionRequest) Validate() error {
	invalid := func() error { return fmt.Errorf("invalid execution projection request") }
	if request.Fence.Validate() != nil || !model.IsValidId(request.MutationID) || request.FromWorkspaceCursor < 0 || request.ThroughWorkspaceCursor < 0 ||
		request.FromWorkspaceCursor > 1<<53-1 || request.ThroughWorkspaceCursor > 1<<53-1 || request.ExpectedHostCursor < 0 || request.ExpectedHostCursor > 1<<53-1 {
		return invalid()
	}
	contentValid := func(content *ExecutionProjectionContent) bool {
		return content != nil && model.ValidExecutionEnvironmentEpoch(content.TransferID) && content.Size >= 0 && content.Size <= 10<<20 && len(content.SHA256) == 64 && strings.Trim(content.SHA256, "0123456789abcdef") == ""
	}
	pathValid := func(path string) bool {
		normalized, err := model.NormalizeAttemptWorkspacePath(path)
		return err == nil && normalized == path
	}
	var total int64
	if request.Initial {
		if request.FromWorkspaceCursor != 0 || request.ExpectedHostCursor != 0 || len(request.Mutations) != 0 || len(request.Entries) > 500 {
			return invalid()
		}
		identities, paths := map[model.AttemptWorkspaceEntryID]bool{}, map[string]model.StarterWorkspaceEntryKind{}
		for _, entry := range request.Entries {
			if !entry.EntryID.IsValid() || !pathValid(entry.Path) || identities[entry.EntryID] || paths[entry.Path] != "" {
				return invalid()
			}
			if slash := strings.LastIndexByte(entry.Path, '/'); slash >= 0 && paths[entry.Path[:slash]] != model.StarterWorkspaceEntryDirectory {
				return invalid()
			}
			if entry.Kind == model.StarterWorkspaceEntryFile {
				if !entry.ContentVersion.IsValid() || !contentValid(entry.Content) {
					return invalid()
				}
				total += entry.Content.Size
			} else if entry.Kind != model.StarterWorkspaceEntryDirectory || !entry.ContentVersion.IsZero() || entry.Content != nil {
				return invalid()
			}
			identities[entry.EntryID] = true
			paths[entry.Path] = entry.Kind
		}
	} else {
		if len(request.Entries) != 0 || len(request.Mutations) < 1 || len(request.Mutations) > 128 || request.ThroughWorkspaceCursor-request.FromWorkspaceCursor != int64(len(request.Mutations)) {
			return invalid()
		}
		for i, mutation := range request.Mutations {
			change := mutation.Change
			if change.Validate() != nil || change.Cursor != request.FromWorkspaceCursor+int64(i)+1 {
				return invalid()
			}
			expected := change.EntryKind == model.StarterWorkspaceEntryFile && change.Operation != model.AttemptWorkspaceMutationCreateFile
			if (expected && !mutation.ExpectedContentVersion.IsValid()) || (!expected && !mutation.ExpectedContentVersion.IsZero()) {
				return invalid()
			}
			if change.Operation == model.AttemptWorkspaceMutationCreateFile || change.Operation == model.AttemptWorkspaceMutationReplaceFile {
				if !contentValid(mutation.Content) {
					return invalid()
				}
				total += mutation.Content.Size
			} else if mutation.Content != nil {
				return invalid()
			}
		}
	}
	if total > 50<<20 {
		return invalid()
	}
	body, err := json.Marshal(request)
	if err != nil || len(body) > 256<<10 {
		return invalid()
	}
	return nil
}
