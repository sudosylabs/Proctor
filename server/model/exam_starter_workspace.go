// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"encoding/hex"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	StarterWorkspaceMaximumEntries      = ExamWorkspaceMaximumEntries
	StarterWorkspaceMaximumDepth        = 16
	StarterWorkspaceMaximumSegmentBytes = 255
	StarterWorkspaceMaximumPathBytes    = 1024
	StarterWorkspaceMaximumFileBytes    = ExamWorkspaceMaximumFileBytes
	StarterWorkspaceMaximumTotalBytes   = ExamWorkspaceMaximumTotalBytes
	StarterWorkspaceUploadLease         = time.Hour
	StarterWorkspaceReclaimSafetyWindow = 24 * time.Hour
	StarterWorkspaceCleanupClaimLease   = 5 * time.Minute
)

// NormalizeStarterWorkspacePath accepts only an already-canonical,
// case-sensitive POSIX-relative logical path. It never cleans or case-folds a
// caller's input because doing so could silently select a different entry.
func NormalizeStarterWorkspacePath(value string) (string, error) {
	if value == "" || !utf8.ValidString(value) || len(value) > StarterWorkspaceMaximumPathBytes ||
		strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") || strings.Contains(value, "\\") {
		return "", invalidModelError("NormalizeStarterWorkspacePath", "exam_starter_workspace", "", "invalid Starter Workspace path", "")
	}
	segments := strings.Split(value, "/")
	if len(segments) > StarterWorkspaceMaximumDepth || segments[0] == ".proctor" {
		return "", invalidModelError("NormalizeStarterWorkspacePath", "exam_starter_workspace", "", "invalid Starter Workspace path", "")
	}
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." || len(segment) > StarterWorkspaceMaximumSegmentBytes {
			return "", invalidModelError("NormalizeStarterWorkspacePath", "exam_starter_workspace", "", "invalid Starter Workspace path", "")
		}
		for _, character := range segment {
			if unicode.IsControl(character) {
				return "", invalidModelError("NormalizeStarterWorkspacePath", "exam_starter_workspace", "", "invalid Starter Workspace path", "")
			}
		}
	}
	return value, nil
}

func (v WorkspaceContentVersion) IsZero() bool { return v == "" }

type StarterWorkspaceEntryKind string

const (
	StarterWorkspaceEntryFile      StarterWorkspaceEntryKind = "file"
	StarterWorkspaceEntryDirectory StarterWorkspaceEntryKind = "directory"
)

// StarterWorkspaceEntry is one stable logical file or directory in an Exam
// Draft. Path is mutable metadata; it is never a VFS key.
type StarterWorkspaceEntry struct {
	ID              StarterWorkspaceEntryID
	ExamID          ExamID
	Kind            StarterWorkspaceEntryKind
	Path            string
	CurrentObjectID StarterWorkspaceObjectID
	CreatedAt       time.Time
	UpdatedAt       time.Time
	ArchivedAt      OptionalTime
}

func NewStarterWorkspaceDirectory(id StarterWorkspaceEntryID, examID ExamID, path string, at time.Time) (*StarterWorkspaceEntry, error) {
	return newStarterWorkspaceEntry(id, examID, StarterWorkspaceEntryDirectory, path, "", at)
}

func NewStarterWorkspaceFile(id StarterWorkspaceEntryID, examID ExamID, path string, objectID StarterWorkspaceObjectID, at time.Time) (*StarterWorkspaceEntry, error) {
	return newStarterWorkspaceEntry(id, examID, StarterWorkspaceEntryFile, path, objectID, at)
}

func newStarterWorkspaceEntry(id StarterWorkspaceEntryID, examID ExamID, kind StarterWorkspaceEntryKind, path string, objectID StarterWorkspaceObjectID, at time.Time) (*StarterWorkspaceEntry, error) {
	normalized, err := NormalizeStarterWorkspacePath(path)
	if err != nil {
		return nil, err
	}
	entry := &StarterWorkspaceEntry{ID: id, ExamID: examID, Kind: kind, Path: normalized,
		CurrentObjectID: objectID, CreatedAt: TimeUTC(at), UpdatedAt: TimeUTC(at)}
	if err := entry.Validate(); err != nil {
		return nil, err
	}
	return entry, nil
}

func (e *StarterWorkspaceEntry) Validate() error {
	if e == nil || !e.ID.IsValid() || !e.ExamID.IsValid() || e.CreatedAt.IsZero() ||
		e.UpdatedAt.IsZero() || e.UpdatedAt.Before(e.CreatedAt) {
		return invalidModelError("StarterWorkspaceEntry.Validate", "starter_workspace_entry", "", "invalid Starter Workspace entry", "")
	}
	if normalized, err := NormalizeStarterWorkspacePath(e.Path); err != nil || normalized != e.Path {
		return invalidModelError("StarterWorkspaceEntry.Validate", "starter_workspace_entry", "", "invalid Starter Workspace entry path", "")
	}
	if e.ArchivedAt.Valid && e.ArchivedAt.Time.Before(e.CreatedAt) {
		return invalidModelError("StarterWorkspaceEntry.Validate", "starter_workspace_entry", "", "invalid Starter Workspace entry lifecycle", "")
	}
	switch e.Kind {
	case StarterWorkspaceEntryFile:
		if !e.ArchivedAt.Valid && !e.CurrentObjectID.IsValid() {
			return invalidModelError("StarterWorkspaceEntry.Validate", "starter_workspace_entry", "", "Starter Workspace file requires a current object", "")
		}
		if e.ArchivedAt.Valid && !e.CurrentObjectID.IsZero() && !e.CurrentObjectID.IsValid() {
			return invalidModelError("StarterWorkspaceEntry.Validate", "starter_workspace_entry", "", "Starter Workspace archived file has an invalid object", "")
		}
	case StarterWorkspaceEntryDirectory:
		if !e.CurrentObjectID.IsZero() {
			return invalidModelError("StarterWorkspaceEntry.Validate", "starter_workspace_entry", "", "Starter Workspace directory cannot have content", "")
		}
	default:
		return invalidModelError("StarterWorkspaceEntry.Validate", "starter_workspace_entry", "", "invalid Starter Workspace entry kind", "")
	}
	return nil
}

type StarterWorkspaceObjectState string

const (
	StarterWorkspaceObjectStaged      StarterWorkspaceObjectState = "staged"
	StarterWorkspaceObjectCurrent     StarterWorkspaceObjectState = "current"
	StarterWorkspaceObjectReclaimable StarterWorkspaceObjectState = "reclaimable"
	StarterWorkspaceObjectClaimed     StarterWorkspaceObjectState = "claimed"
)

// StarterWorkspaceObject is one opaque content object. Only Current objects
// may be selected by visible file entries; staged and reclaimable objects are
// cleanup bookkeeping and are not discoverable workspace content.
type StarterWorkspaceObject struct {
	ID              StarterWorkspaceObjectID
	ExamID          ExamID
	CreatedByUserID UserID
	CreatedAt       time.Time
	UpdatedAt       time.Time
	ExpiresAt       time.Time
	State           StarterWorkspaceObjectState
	ContentVersion  WorkspaceContentVersion
	MediaType       string
	SizeBytes       int64
	SHA256          string
	ReclaimAfter    OptionalTime
	ClaimToken      string
	ClaimedAt       OptionalTime
}

// StarterWorkspaceContent is bounded metadata derived while an opaque object
// is streamed. It contains no logical path or storage key.
type StarterWorkspaceContent struct {
	MediaType string
	SizeBytes int64
	SHA256    string
}

func NewStagedStarterWorkspaceObject(id StarterWorkspaceObjectID, examID ExamID, actorID UserID, at, expiresAt time.Time) (*StarterWorkspaceObject, error) {
	object := &StarterWorkspaceObject{ID: id, ExamID: examID, CreatedByUserID: actorID,
		CreatedAt: TimeUTC(at), UpdatedAt: TimeUTC(at), ExpiresAt: TimeUTC(expiresAt), State: StarterWorkspaceObjectStaged}
	if err := object.Validate(); err != nil {
		return nil, err
	}
	return object, nil
}

func (o *StarterWorkspaceObject) MarkCurrent(version WorkspaceContentVersion, mediaType string, size int64, checksum string, at time.Time) error {
	if o == nil || o.State != StarterWorkspaceObjectStaged {
		return invalidModelError("StarterWorkspaceObject.MarkCurrent", "starter_workspace_object", "", "only a staged Starter Workspace object can become current", "")
	}
	candidate := *o
	candidate.State = StarterWorkspaceObjectCurrent
	candidate.ContentVersion = version
	candidate.MediaType = strings.TrimSpace(mediaType)
	candidate.SizeBytes = size
	candidate.SHA256 = strings.ToLower(checksum)
	candidate.UpdatedAt = TimeUTC(at)
	if err := candidate.Validate(); err != nil {
		return err
	}
	*o = candidate
	return nil
}

func (o *StarterWorkspaceObject) Validate() error {
	if o == nil || !o.ID.IsValid() || !o.ExamID.IsValid() || !o.CreatedByUserID.IsValid() ||
		o.CreatedAt.IsZero() || o.UpdatedAt.IsZero() || o.ExpiresAt.IsZero() ||
		o.UpdatedAt.Before(o.CreatedAt) || !o.ExpiresAt.After(o.CreatedAt) {
		return invalidModelError("StarterWorkspaceObject.Validate", "starter_workspace_object", "", "invalid Starter Workspace object", "")
	}
	if o.State == StarterWorkspaceObjectStaged {
		if !o.ContentVersion.IsZero() || o.MediaType != "" || o.SizeBytes != 0 || o.SHA256 != "" || o.ReclaimAfter.Valid || o.ClaimedAt.Valid || o.ClaimToken != "" {
			return invalidModelError("StarterWorkspaceObject.Validate", "starter_workspace_object", "", "invalid staged Starter Workspace object", "")
		}
		return nil
	}
	if o.State != StarterWorkspaceObjectCurrent && o.State != StarterWorkspaceObjectReclaimable && o.State != StarterWorkspaceObjectClaimed {
		return invalidModelError("StarterWorkspaceObject.Validate", "starter_workspace_object", "", "invalid Starter Workspace object state", "")
	}
	hasContent := !o.ContentVersion.IsZero() || o.MediaType != "" || o.SizeBytes != 0 || o.SHA256 != ""
	if hasContent {
		if !o.ContentVersion.IsValid() || o.MediaType == "" || strings.TrimSpace(o.MediaType) != o.MediaType ||
			len(o.MediaType) > 255 || o.SizeBytes < 0 || o.SizeBytes > StarterWorkspaceMaximumFileBytes || len(o.SHA256) != 64 {
			return invalidModelError("StarterWorkspaceObject.Validate", "starter_workspace_object", "", "invalid Starter Workspace object content", "")
		}
		if _, err := hex.DecodeString(o.SHA256); err != nil {
			return invalidModelError("StarterWorkspaceObject.Validate", "starter_workspace_object", "", "invalid Starter Workspace checksum", "")
		}
	}
	if o.State == StarterWorkspaceObjectCurrent {
		if !hasContent {
			return invalidModelError("StarterWorkspaceObject.Validate", "starter_workspace_object", "", "current Starter Workspace object requires content", "")
		}
		if o.ReclaimAfter.Valid || o.ClaimedAt.Valid || o.ClaimToken != "" {
			return invalidModelError("StarterWorkspaceObject.Validate", "starter_workspace_object", "", "current Starter Workspace object cannot be claimed", "")
		}
		return nil
	}
	if !o.ReclaimAfter.Valid {
		return invalidModelError("StarterWorkspaceObject.Validate", "starter_workspace_object", "", "reclaimable Starter Workspace object requires a deadline", "")
	}
	if o.State == StarterWorkspaceObjectReclaimable && (o.ClaimedAt.Valid || o.ClaimToken != "") {
		return invalidModelError("StarterWorkspaceObject.Validate", "starter_workspace_object", "", "unclaimed Starter Workspace object has claim metadata", "")
	}
	if o.State == StarterWorkspaceObjectClaimed && (!o.ClaimedAt.Valid || strings.TrimSpace(o.ClaimToken) == "") {
		return invalidModelError("StarterWorkspaceObject.Validate", "starter_workspace_object", "", "claimed Starter Workspace object requires claim metadata", "")
	}
	return nil
}
