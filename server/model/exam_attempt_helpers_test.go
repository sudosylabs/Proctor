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

func NewStarterOriginAttemptWorkspaceObject(id AttemptWorkspaceObjectID, workspaceID ExamAttemptWorkspaceID, starterObjectID StarterWorkspaceObjectID, version WorkspaceContentVersion, mediaType string, size int64, checksum string, at time.Time) (*AttemptWorkspaceObject, error) {
	object := &AttemptWorkspaceObject{ID: id, WorkspaceID: workspaceID, StorageOrigin: AttemptWorkspaceStorageStarter,
		StarterObjectID: starterObjectID, State: AttemptWorkspaceObjectCurrent, ContentVersion: version, MediaType: strings.TrimSpace(mediaType),
		SizeBytes: size, SHA256: strings.ToLower(checksum), CreatedAt: TimeUTC(at), UpdatedAt: TimeUTC(at)}
	if err := object.Validate(); err != nil {
		return nil, err
	}
	return object, nil
}

func NewAttemptWorkspaceFile(id AttemptWorkspaceEntryID, workspaceID ExamAttemptWorkspaceID, revisionID ExamRevisionID, sourceID StarterWorkspaceEntryID, path string, objectID AttemptWorkspaceObjectID, at time.Time) (*AttemptWorkspaceEntry, error) {
	return newAttemptWorkspaceEntry(id, workspaceID, revisionID, sourceID, StarterWorkspaceEntryFile, path, objectID, at)
}

func NewAttemptWorkspaceDirectory(id AttemptWorkspaceEntryID, workspaceID ExamAttemptWorkspaceID, revisionID ExamRevisionID, sourceID StarterWorkspaceEntryID, path string, at time.Time) (*AttemptWorkspaceEntry, error) {
	return newAttemptWorkspaceEntry(id, workspaceID, revisionID, sourceID, StarterWorkspaceEntryDirectory, path, "", at)
}

func newAttemptWorkspaceEntry(id AttemptWorkspaceEntryID, workspaceID ExamAttemptWorkspaceID, revisionID ExamRevisionID, sourceID StarterWorkspaceEntryID, kind StarterWorkspaceEntryKind, path string, objectID AttemptWorkspaceObjectID, at time.Time) (*AttemptWorkspaceEntry, error) {
	if !sourceID.IsValid() {
		return nil, invalidModelError("newAttemptWorkspaceEntry", "exam_attempt", "", "Attempt Workspace bootstrap requires source entry", "")
	}
	normalized, err := NormalizeStarterWorkspacePath(path)
	if err != nil {
		return nil, err
	}
	at = TimeUTC(at)
	entry := &AttemptWorkspaceEntry{ID: id, WorkspaceID: workspaceID, AdmissionRevisionID: revisionID,
		SourceStarterEntryID: sourceID, Kind: kind, Path: normalized, CurrentObjectID: objectID, CreatedAt: at, UpdatedAt: at}
	if err := entry.Validate(); err != nil {
		return nil, err
	}
	return entry, nil
}
