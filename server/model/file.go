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
	"regexp"
	"strings"
	"time"
)

const UploadLeaseMaximumLifetime = time.Hour

type FilePurpose string

const (
	FilePurposeProfilePictureCustom  FilePurpose = "profile_picture_custom"
	FilePurposeProfilePictureDefault FilePurpose = "profile_picture_default"
	FilePurposeSubmission            FilePurpose = "submission"
	FilePurposeExamResource          FilePurpose = "exam_resource"
)

// WorkspaceContentVersion is an opaque comparison token for one acknowledged
// Starter or Attempt Workspace file content state. It is deliberately not an
// entity identifier and must not be used as a persistence relationship.
type WorkspaceContentVersion string

var workspaceContentVersionPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{26}$`)

func NewWorkspaceContentVersion() WorkspaceContentVersion {
	return WorkspaceContentVersion(NewId())
}

func ParseWorkspaceContentVersion(value string) (WorkspaceContentVersion, error) {
	if !workspaceContentVersionPattern.MatchString(value) {
		return "", fmt.Errorf("model: invalid workspace content version")
	}
	return WorkspaceContentVersion(value), nil
}

func (version WorkspaceContentVersion) IsValid() bool {
	return workspaceContentVersionPattern.MatchString(string(version))
}

func (version WorkspaceContentVersion) String() string { return string(version) }

// FileIndexingPolicy controls which server-maintained search material may be derived.
type FileIndexingPolicy string

const (
	FileIndexingNone     FileIndexingPolicy = "none"
	FileIndexingMetadata FileIndexingPolicy = "metadata"
	FileIndexingContent  FileIndexingPolicy = "content"
)

// FileAvailability is the publication state of an immutable revision.
type FileAvailability string

const (
	FileAvailabilityPending     FileAvailability = "pending"
	FileAvailabilityAvailable   FileAvailability = "available"
	FileAvailabilityQuarantined FileAvailability = "quarantined"
	FileAvailabilityRejected    FileAvailability = "rejected"
)

// FileIndexingState records processing independently from publication.
type FileIndexingState string

const (
	FileIndexingNotRequired FileIndexingState = "not_required"
	FileIndexingPending     FileIndexingState = "pending"
	FileIndexingReady       FileIndexingState = "ready"
	FileIndexingFailed      FileIndexingState = "failed"
)

// FileEntry is the stable identity referenced by an owning domain model.
type FileEntry struct {
	ID                FileEntryID
	CreatedAt         time.Time
	UpdatedAt         time.Time
	ArchivedAt        OptionalTime
	Revision          int64
	CurrentRevisionID FileRevisionID
	IndexingPolicy    FileIndexingPolicy
	Purpose           FilePurpose
}

func NewFileEntry(id FileEntryID, indexing FileIndexingPolicy, at time.Time) (*FileEntry, error) {
	return NewFileEntryForPurpose(id, FilePurposeProfilePictureCustom, indexing, at)
}

func NewFileEntryForPurpose(id FileEntryID, purpose FilePurpose, indexing FileIndexingPolicy, at time.Time) (*FileEntry, error) {
	entry := &FileEntry{ID: id, CreatedAt: TimeUTC(at), UpdatedAt: TimeUTC(at), Revision: 1, IndexingPolicy: indexing, Purpose: purpose}
	if err := entry.Validate(); err != nil {
		return nil, err
	}
	return entry, nil
}

func (f *FileEntry) Validate() error {
	if f == nil || !f.ID.IsValid() || f.CreatedAt.IsZero() || f.UpdatedAt.Before(f.CreatedAt) || f.Revision <= 0 || !validFileIndexingPolicy(f.IndexingPolicy) || !validFilePurpose(f.Purpose) {
		return fmt.Errorf("model: invalid file entry")
	}
	if !f.CurrentRevisionID.IsZero() && !f.CurrentRevisionID.IsValid() {
		return fmt.Errorf("model: invalid file entry current revision")
	}
	return nil
}

func validFilePurpose(value FilePurpose) bool {
	return value == FilePurposeProfilePictureCustom || value == FilePurposeProfilePictureDefault || value == FilePurposeSubmission || value == FilePurposeExamResource
}

// FileRevision is an immutable content generation below a FileEntry.
type FileRevision struct {
	ID            FileRevisionID
	FileEntryID   FileEntryID
	CreatedAt     time.Time
	Availability  FileAvailability
	IndexingState FileIndexingState
	Renditions    []FileRendition
}

func NewFileRevision(id FileRevisionID, entryID FileEntryID, availability FileAvailability, indexing FileIndexingState, at time.Time) (*FileRevision, error) {
	revision := &FileRevision{ID: id, FileEntryID: entryID, CreatedAt: TimeUTC(at), Availability: availability, IndexingState: indexing}
	if err := revision.Validate(); err != nil {
		return nil, err
	}
	return revision, nil
}

func (f *FileRevision) Validate() error {
	if f == nil || !f.ID.IsValid() || !f.FileEntryID.IsValid() || f.CreatedAt.IsZero() || !validFileAvailability(f.Availability) || !validFileIndexingState(f.IndexingState) {
		return fmt.Errorf("model: invalid file revision")
	}
	if f.Availability == FileAvailabilityAvailable && len(f.Renditions) == 0 {
		return fmt.Errorf("model: available file revision must have renditions")
	}
	if f.Availability == FileAvailabilityPending && len(f.Renditions) != 0 {
		return fmt.Errorf("model: pending file revision cannot have renditions")
	}
	for index := range f.Renditions {
		if err := f.Renditions[index].Validate(); err != nil || f.Renditions[index].RevisionID != f.ID {
			return fmt.Errorf("model: invalid file revision rendition")
		}
	}
	return nil
}

// MakeAvailable returns a published copy after every supplied rendition validates.
func (f *FileRevision) MakeAvailable(renditions []FileRendition) (*FileRevision, error) {
	if f == nil || f.Availability != FileAvailabilityPending || len(renditions) == 0 {
		return nil, fmt.Errorf("model: only a pending revision with renditions can become available")
	}
	result := *f
	result.Availability = FileAvailabilityAvailable
	result.Renditions = append([]FileRendition(nil), renditions...)
	if err := result.Validate(); err != nil {
		return nil, err
	}
	return &result, nil
}

// FileRendition describes immutable bytes stored outside PostgreSQL.
type FileRendition struct {
	ID         FileRenditionID
	RevisionID FileRevisionID
	CreatedAt  time.Time
	Name       string
	MediaType  string
	Size       int64
	Width      int
	Height     int
	SHA256     string
}

func NewFileRendition(id FileRenditionID, revisionID FileRevisionID, name, mediaType string, size int64, width, height int, checksum string, at time.Time) (*FileRendition, error) {
	rendition := &FileRendition{ID: id, RevisionID: revisionID, CreatedAt: TimeUTC(at), Name: name, MediaType: strings.ToLower(strings.TrimSpace(mediaType)), Size: size, Width: width, Height: height, SHA256: strings.ToLower(checksum)}
	if err := rendition.Validate(); err != nil {
		return nil, err
	}
	return rendition, nil
}

func (f *FileRendition) Validate() error {
	validDimensions := f != nil && ((f.Width == 0 && f.Height == 0) || (f.Width > 0 && f.Height > 0))
	if f == nil || !f.ID.IsValid() || !f.RevisionID.IsValid() || f.CreatedAt.IsZero() || strings.TrimSpace(f.Name) == "" || f.MediaType == "" || f.Size < 0 || !validDimensions || len(f.SHA256) != 64 {
		return fmt.Errorf("model: invalid file rendition")
	}
	if _, err := hex.DecodeString(f.SHA256); err != nil {
		return fmt.Errorf("model: invalid file rendition checksum")
	}
	return nil
}

// UploadLease is a renewable, bounded reservation used while bytes are staged.
type UploadLease struct {
	ID              UploadLeaseID
	FileRevisionID  FileRevisionID
	CreatedByUserID UserID
	CreatedAt       time.Time
	UpdatedAt       time.Time
	ExpiresAt       time.Time
	ConsumedAt      OptionalTime
	Revision        int64
	BytesReceived   int64
}

func NewUploadLease(id UploadLeaseID, revisionID FileRevisionID, createdBy UserID, at, expiresAt time.Time) (*UploadLease, error) {
	if !TimeUTC(expiresAt).Equal(TimeUTC(at).Add(UploadLeaseMaximumLifetime)) {
		return nil, fmt.Errorf("model: upload lease must initially expire after one hour")
	}
	lease := &UploadLease{ID: id, FileRevisionID: revisionID, CreatedByUserID: createdBy, CreatedAt: TimeUTC(at), UpdatedAt: TimeUTC(at), ExpiresAt: TimeUTC(expiresAt), Revision: 1}
	if err := lease.Validate(); err != nil {
		return nil, err
	}
	return lease, nil
}

func (u *UploadLease) Validate() error {
	if u == nil || !u.ID.IsValid() || !u.FileRevisionID.IsValid() || !u.CreatedByUserID.IsValid() || u.CreatedAt.IsZero() || u.UpdatedAt.Before(u.CreatedAt) || !u.ExpiresAt.After(u.CreatedAt) || u.ExpiresAt.After(u.UpdatedAt.Add(UploadLeaseMaximumLifetime)) || u.Revision <= 0 || u.BytesReceived < 0 {
		return fmt.Errorf("model: invalid upload lease")
	}
	if u.ConsumedAt.Valid && u.ConsumedAt.Time.Before(u.CreatedAt) {
		return fmt.Errorf("model: invalid upload lease consumption time")
	}
	return nil
}

func (u *UploadLease) Renew(now, expiresAt time.Time, bytesReceived int64) (*UploadLease, error) {
	if u == nil || u.ConsumedAt.Valid || bytesReceived <= u.BytesReceived || !TimeUTC(now).Before(u.ExpiresAt) || !TimeUTC(expiresAt).After(TimeUTC(now)) || TimeUTC(expiresAt).After(TimeUTC(now).Add(UploadLeaseMaximumLifetime)) {
		return nil, fmt.Errorf("model: upload lease cannot be renewed")
	}
	result := *u
	result.UpdatedAt = TimeUTC(now)
	result.ExpiresAt = TimeUTC(expiresAt)
	result.BytesReceived = bytesReceived
	result.Revision++
	return &result, result.Validate()
}

func (u *UploadLease) Consume(now time.Time) (*UploadLease, error) {
	if u == nil || u.ConsumedAt.Valid || TimeUTC(now).Before(u.CreatedAt) || !TimeUTC(now).Before(u.ExpiresAt) {
		return nil, fmt.Errorf("model: upload lease cannot be consumed")
	}
	result := *u
	result.UpdatedAt = TimeUTC(now)
	result.ConsumedAt = OptionalTimeFrom(now)
	result.Revision++
	return &result, result.Validate()
}

func validFileIndexingPolicy(value FileIndexingPolicy) bool {
	return value == FileIndexingNone || value == FileIndexingMetadata || value == FileIndexingContent
}

func validFileAvailability(value FileAvailability) bool {
	return value == FileAvailabilityPending || value == FileAvailabilityAvailable || value == FileAvailabilityQuarantined || value == FileAvailabilityRejected
}

func validFileIndexingState(value FileIndexingState) bool {
	return value == FileIndexingNotRequired || value == FileIndexingPending || value == FileIndexingReady || value == FileIndexingFailed
}
