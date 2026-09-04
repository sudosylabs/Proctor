// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"encoding/hex"
	"fmt"
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
		return "", fmt.Errorf("model: invalid Starter Workspace path")
	}
	segments := strings.Split(value, "/")
	if len(segments) > StarterWorkspaceMaximumDepth || segments[0] == ".proctor" {
		return "", fmt.Errorf("model: invalid Starter Workspace path")
	}
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." || len(segment) > StarterWorkspaceMaximumSegmentBytes {
			return "", fmt.Errorf("model: invalid Starter Workspace path")
		}
		for _, character := range segment {
			if unicode.IsControl(character) {
				return "", fmt.Errorf("model: invalid Starter Workspace path")
			}
		}
	}
	return value, nil
}

func (version WorkspaceContentVersion) IsZero() bool { return version == "" }

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

func (entry *StarterWorkspaceEntry) Validate() error {
	if entry == nil || !entry.ID.IsValid() || !entry.ExamID.IsValid() || entry.CreatedAt.IsZero() ||
		entry.UpdatedAt.IsZero() || entry.UpdatedAt.Before(entry.CreatedAt) {
		return fmt.Errorf("model: invalid Starter Workspace entry")
	}
	if normalized, err := NormalizeStarterWorkspacePath(entry.Path); err != nil || normalized != entry.Path {
		return fmt.Errorf("model: invalid Starter Workspace entry path")
	}
	if entry.ArchivedAt.Valid && entry.ArchivedAt.Time.Before(entry.CreatedAt) {
		return fmt.Errorf("model: invalid Starter Workspace entry lifecycle")
	}
	switch entry.Kind {
	case StarterWorkspaceEntryFile:
		if !entry.ArchivedAt.Valid && !entry.CurrentObjectID.IsValid() {
			return fmt.Errorf("model: Starter Workspace file requires a current object")
		}
		if entry.ArchivedAt.Valid && !entry.CurrentObjectID.IsZero() && !entry.CurrentObjectID.IsValid() {
			return fmt.Errorf("model: Starter Workspace archived file has an invalid object")
		}
	case StarterWorkspaceEntryDirectory:
		if !entry.CurrentObjectID.IsZero() {
			return fmt.Errorf("model: Starter Workspace directory cannot have content")
		}
	default:
		return fmt.Errorf("model: invalid Starter Workspace entry kind")
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

func (object *StarterWorkspaceObject) MarkCurrent(version WorkspaceContentVersion, mediaType string, size int64, checksum string, at time.Time) error {
	if object == nil || object.State != StarterWorkspaceObjectStaged {
		return fmt.Errorf("model: only a staged Starter Workspace object can become current")
	}
	candidate := *object
	candidate.State = StarterWorkspaceObjectCurrent
	candidate.ContentVersion = version
	candidate.MediaType = strings.TrimSpace(mediaType)
	candidate.SizeBytes = size
	candidate.SHA256 = strings.ToLower(checksum)
	candidate.UpdatedAt = TimeUTC(at)
	if err := candidate.Validate(); err != nil {
		return err
	}
	*object = candidate
	return nil
}

func (object *StarterWorkspaceObject) Validate() error {
	if object == nil || !object.ID.IsValid() || !object.ExamID.IsValid() || !object.CreatedByUserID.IsValid() ||
		object.CreatedAt.IsZero() || object.UpdatedAt.IsZero() || object.ExpiresAt.IsZero() ||
		object.UpdatedAt.Before(object.CreatedAt) || !object.ExpiresAt.After(object.CreatedAt) {
		return fmt.Errorf("model: invalid Starter Workspace object")
	}
	if object.State == StarterWorkspaceObjectStaged {
		if !object.ContentVersion.IsZero() || object.MediaType != "" || object.SizeBytes != 0 || object.SHA256 != "" || object.ReclaimAfter.Valid || object.ClaimedAt.Valid || object.ClaimToken != "" {
			return fmt.Errorf("model: invalid staged Starter Workspace object")
		}
		return nil
	}
	if object.State != StarterWorkspaceObjectCurrent && object.State != StarterWorkspaceObjectReclaimable && object.State != StarterWorkspaceObjectClaimed {
		return fmt.Errorf("model: invalid Starter Workspace object state")
	}
	hasContent := !object.ContentVersion.IsZero() || object.MediaType != "" || object.SizeBytes != 0 || object.SHA256 != ""
	if hasContent {
		if !object.ContentVersion.IsValid() || object.MediaType == "" || strings.TrimSpace(object.MediaType) != object.MediaType ||
			len(object.MediaType) > 255 || object.SizeBytes < 0 || object.SizeBytes > StarterWorkspaceMaximumFileBytes || len(object.SHA256) != 64 {
			return fmt.Errorf("model: invalid Starter Workspace object content")
		}
		if _, err := hex.DecodeString(object.SHA256); err != nil {
			return fmt.Errorf("model: invalid Starter Workspace checksum")
		}
	}
	if object.State == StarterWorkspaceObjectCurrent {
		if !hasContent {
			return fmt.Errorf("model: current Starter Workspace object requires content")
		}
		if object.ReclaimAfter.Valid || object.ClaimedAt.Valid || object.ClaimToken != "" {
			return fmt.Errorf("model: current Starter Workspace object cannot be claimed")
		}
		return nil
	}
	if !object.ReclaimAfter.Valid {
		return fmt.Errorf("model: reclaimable Starter Workspace object requires a deadline")
	}
	if object.State == StarterWorkspaceObjectReclaimable && (object.ClaimedAt.Valid || object.ClaimToken != "") {
		return fmt.Errorf("model: unclaimed Starter Workspace object has claim metadata")
	}
	if object.State == StarterWorkspaceObjectClaimed && (!object.ClaimedAt.Valid || strings.TrimSpace(object.ClaimToken) == "") {
		return fmt.Errorf("model: claimed Starter Workspace object requires claim metadata")
	}
	return nil
}
