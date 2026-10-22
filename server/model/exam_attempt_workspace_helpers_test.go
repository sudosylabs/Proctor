// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"strings"
	"time"
)

// NewAttemptOriginAttemptWorkspaceObject records one immutable content
// selection written by the candidate. It carries no starter-object selector
// and its identity remains independent from the logical Workspace Path.
func NewAttemptOriginAttemptWorkspaceObject(id AttemptWorkspaceObjectID, workspaceID ExamAttemptWorkspaceID,
	version WorkspaceContentVersion, mediaType string, size int64, checksum string, at time.Time,
) (*AttemptWorkspaceObject, error) {
	object := &AttemptWorkspaceObject{ID: id, WorkspaceID: workspaceID, StorageOrigin: AttemptWorkspaceStorageAttempt,
		State: AttemptWorkspaceObjectCurrent, ContentVersion: version, MediaType: strings.TrimSpace(mediaType), SizeBytes: size,
		SHA256: strings.ToLower(checksum), CreatedAt: TimeUTC(at), UpdatedAt: TimeUTC(at)}
	if err := object.Validate(); err != nil {
		return nil, err
	}
	return object, nil
}

// Move changes only logical metadata; stable Entry and content-object
// identities deliberately survive a rename or subtree move.
func (e *AttemptWorkspaceEntry) Move(path string, at time.Time) error {
	if e == nil {
		return invalidModelError("AttemptWorkspaceEntry.Move", "attempt_workspace_entry", "", "nil Attempt Workspace entry", "")
	}
	normalized, err := NormalizeAttemptWorkspacePath(path)
	if err != nil || normalized == e.Path {
		return invalidModelError("AttemptWorkspaceEntry.Move", "attempt_workspace_entry", "", "invalid Attempt Workspace move", "")
	}
	candidate := *e
	candidate.Path, candidate.UpdatedAt = normalized, TimeUTC(at)
	if candidate.UpdatedAt.Before(e.UpdatedAt) {
		return invalidModelError("AttemptWorkspaceEntry.Move", "attempt_workspace_entry", "", "Attempt Workspace entry time regressed", "")
	}
	if err = candidate.Validate(); err != nil {
		return err
	}
	*e = candidate
	return nil
}

// ReplaceCurrentObject selects one newly acknowledged immutable object while
// preserving the stable file Entry identity.
func (e *AttemptWorkspaceEntry) ReplaceCurrentObject(objectID AttemptWorkspaceObjectID, at time.Time) error {
	if e == nil || e.Kind != StarterWorkspaceEntryFile || !objectID.IsValid() || objectID == e.CurrentObjectID {
		return invalidModelError("AttemptWorkspaceEntry.ReplaceCurrentObject", "attempt_workspace_entry", "", "invalid Attempt Workspace file replacement", "")
	}
	candidate := *e
	candidate.CurrentObjectID, candidate.UpdatedAt = objectID, TimeUTC(at)
	if candidate.UpdatedAt.Before(e.UpdatedAt) {
		return invalidModelError("AttemptWorkspaceEntry.ReplaceCurrentObject", "attempt_workspace_entry", "", "Attempt Workspace entry time regressed", "")
	}
	if err := candidate.Validate(); err != nil {
		return err
	}
	*e = candidate
	return nil
}
