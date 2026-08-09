// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

const UploadLeaseMaximumLifetime = time.Hour

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
}

func NewFileEntry(id FileEntryID, indexing FileIndexingPolicy, at time.Time) (*FileEntry, error) {
	entry := &FileEntry{ID: id, CreatedAt: TimeUTC(at), UpdatedAt: TimeUTC(at), Revision: 1, IndexingPolicy: indexing}
	if err := entry.Validate(); err != nil {
		return nil, err
	}
	return entry, nil
}

func (f *FileEntry) Validate() error {
	if f == nil || !f.ID.IsValid() || f.CreatedAt.IsZero() || f.UpdatedAt.Before(f.CreatedAt) || f.Revision <= 0 || !validFileIndexingPolicy(f.IndexingPolicy) {
		return fmt.Errorf("model: invalid file entry")
	}
	if !f.CurrentRevisionID.IsZero() && !f.CurrentRevisionID.IsValid() {
		return fmt.Errorf("model: invalid file entry current revision")
	}
	return nil
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
	if f == nil || !f.ID.IsValid() || !f.RevisionID.IsValid() || f.CreatedAt.IsZero() || strings.TrimSpace(f.Name) == "" || f.MediaType == "" || f.Size < 0 || f.Width <= 0 || f.Height <= 0 || len(f.SHA256) != 64 {
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
}

func NewUploadLease(id UploadLeaseID, revisionID FileRevisionID, createdBy UserID, at, expiresAt time.Time) (*UploadLease, error) {
	lease := &UploadLease{ID: id, FileRevisionID: revisionID, CreatedByUserID: createdBy, CreatedAt: TimeUTC(at), UpdatedAt: TimeUTC(at), ExpiresAt: TimeUTC(expiresAt), Revision: 1}
	if err := lease.Validate(); err != nil {
		return nil, err
	}
	return lease, nil
}

func (u *UploadLease) Validate() error {
	if u == nil || !u.ID.IsValid() || !u.FileRevisionID.IsValid() || !u.CreatedByUserID.IsValid() || u.CreatedAt.IsZero() || u.UpdatedAt.Before(u.CreatedAt) || !u.ExpiresAt.After(u.CreatedAt) || u.ExpiresAt.After(u.UpdatedAt.Add(UploadLeaseMaximumLifetime)) || u.Revision <= 0 {
		return fmt.Errorf("model: invalid upload lease")
	}
	if u.ConsumedAt.Valid && u.ConsumedAt.Time.Before(u.CreatedAt) {
		return fmt.Errorf("model: invalid upload lease consumption time")
	}
	return nil
}

func (u *UploadLease) Renew(now, expiresAt time.Time) (*UploadLease, error) {
	if u == nil || u.ConsumedAt.Valid || !TimeUTC(now).Before(u.ExpiresAt) || !TimeUTC(expiresAt).After(TimeUTC(now)) || TimeUTC(expiresAt).After(TimeUTC(now).Add(UploadLeaseMaximumLifetime)) {
		return nil, fmt.Errorf("model: upload lease cannot be renewed")
	}
	result := *u
	result.UpdatedAt = TimeUTC(now)
	result.ExpiresAt = TimeUTC(expiresAt)
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
