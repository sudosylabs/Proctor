// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"fmt"
	"math"
	"strings"
	"time"
)

const (
	AttemptWorkspaceMaximumEntries      = 500
	AttemptWorkspaceMaximumDepth        = 16
	AttemptWorkspaceMaximumSegmentBytes = 255
	AttemptWorkspaceMaximumPathBytes    = 1024
	AttemptWorkspaceMaximumFileBytes    = int64(10 << 20)
	AttemptWorkspaceMaximumTotalBytes   = int64(50 << 20)
	// AttemptWorkspaceMaximumRequestBytes leaves 64 KiB for the bounded
	// multipart/JSON envelope around one maximum-size staged file.
	AttemptWorkspaceMaximumRequestBytes = AttemptWorkspaceMaximumFileBytes + (64 << 10)
	AttemptWorkspaceJournalRetention    = 4096
	AttemptWorkspaceJournalReadMaximum  = 200
	AttemptWorkspaceStageLifetime       = time.Hour
	AttemptWorkspaceReclaimSafetyWindow = 24 * time.Hour
	AttemptWorkspaceCleanupClaimLease   = 5 * time.Minute
	AttemptWorkspaceClaimTokenMaxBytes  = 128
)

type AttemptWorkspaceObjectState string

const (
	AttemptWorkspaceObjectStaged      AttemptWorkspaceObjectState = "staged"
	AttemptWorkspaceObjectCurrent     AttemptWorkspaceObjectState = "current"
	AttemptWorkspaceObjectReclaimable AttemptWorkspaceObjectState = "reclaimable"
	AttemptWorkspaceObjectClaimed     AttemptWorkspaceObjectState = "claimed"
)

// AttemptWorkspaceContent is verified bounded metadata derived while the
// private object is streamed. It contains no logical path or storage selector.
type AttemptWorkspaceContent struct {
	MediaType string
	SizeBytes int64
	SHA256    string
}

// NormalizeAttemptWorkspacePath accepts an already-canonical, case-sensitive
// POSIX-relative logical path. Workspace Paths are PostgreSQL metadata, never
// VFS object keys or authorization selectors.
func NormalizeAttemptWorkspacePath(value string) (string, error) {
	return NormalizeStarterWorkspacePath(value)
}

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

// NewStagedAttemptWorkspaceObject reserves invisible attempt-owned object
// metadata before bytes are sent to VFS. Staged objects are never selectable
// by a visible Workspace Entry.
func NewStagedAttemptWorkspaceObject(id AttemptWorkspaceObjectID, workspaceID ExamAttemptWorkspaceID,
	at, expiresAt time.Time,
) (*AttemptWorkspaceObject, error) {
	object := &AttemptWorkspaceObject{ID: id, WorkspaceID: workspaceID, StorageOrigin: AttemptWorkspaceStorageAttempt,
		State: AttemptWorkspaceObjectStaged, CreatedAt: TimeUTC(at), UpdatedAt: TimeUTC(at), ExpiresAt: TimeUTC(expiresAt)}
	if err := object.Validate(); err != nil {
		return nil, err
	}
	return object, nil
}

func (object *AttemptWorkspaceObject) HasContent() bool {
	return object != nil && object.ContentVersion.IsValid() && object.MediaType != "" &&
		object.SizeBytes >= 0 && validLowerSHA256(object.SHA256)
}

func (content AttemptWorkspaceContent) Validate() error {
	if content.MediaType == "" || strings.TrimSpace(content.MediaType) != content.MediaType || len(content.MediaType) > 255 ||
		content.SizeBytes < 0 || content.SizeBytes > AttemptWorkspaceMaximumFileBytes || !validLowerSHA256(content.SHA256) {
		return fmt.Errorf("model: invalid Attempt Workspace content")
	}
	return nil
}

func validateAttemptWorkspaceObject(object *AttemptWorkspaceObject) error {
	if object == nil || !object.ID.IsValid() || !object.WorkspaceID.IsValid() || object.CreatedAt.IsZero() || object.UpdatedAt.IsZero() ||
		object.UpdatedAt.Before(object.CreatedAt) {
		return fmt.Errorf("model: invalid Attempt Workspace object")
	}
	hasAnyContent := !object.ContentVersion.IsZero() || object.MediaType != "" || object.SizeBytes != 0 || object.SHA256 != ""
	if hasAnyContent && (!object.ContentVersion.IsValid() || (AttemptWorkspaceContent{MediaType: object.MediaType,
		SizeBytes: object.SizeBytes, SHA256: object.SHA256}).Validate() != nil) {
		return fmt.Errorf("model: invalid Attempt Workspace object content")
	}
	if object.StorageOrigin == AttemptWorkspaceStorageStarter {
		if !object.StarterObjectID.IsValid() || object.State != AttemptWorkspaceObjectCurrent || !hasAnyContent ||
			!object.ExpiresAt.IsZero() || object.ReclaimAfter.Valid || object.ClaimedAt.Valid || object.ClaimToken != "" {
			return fmt.Errorf("model: invalid starter-origin Attempt Workspace object")
		}
		return nil
	}
	if object.StorageOrigin != AttemptWorkspaceStorageAttempt || !object.StarterObjectID.IsZero() {
		return fmt.Errorf("model: invalid attempt-origin Attempt Workspace object")
	}
	switch object.State {
	case AttemptWorkspaceObjectStaged:
		if !object.ExpiresAt.After(object.CreatedAt) || object.ReclaimAfter.Valid || object.ClaimedAt.Valid || object.ClaimToken != "" {
			return fmt.Errorf("model: invalid staged Attempt Workspace object")
		}
	case AttemptWorkspaceObjectCurrent:
		if !hasAnyContent || !object.ExpiresAt.IsZero() || object.ReclaimAfter.Valid || object.ClaimedAt.Valid || object.ClaimToken != "" {
			return fmt.Errorf("model: invalid current Attempt Workspace object")
		}
	case AttemptWorkspaceObjectReclaimable:
		if !object.ReclaimAfter.Valid || object.ReclaimAfter.Time.Before(object.CreatedAt) || object.ClaimedAt.Valid || object.ClaimToken != "" {
			return fmt.Errorf("model: invalid reclaimable Attempt Workspace object")
		}
	case AttemptWorkspaceObjectClaimed:
		if !object.ReclaimAfter.Valid || !object.ClaimedAt.Valid || object.ClaimedAt.Time != object.UpdatedAt ||
			object.ClaimToken == "" || strings.TrimSpace(object.ClaimToken) != object.ClaimToken || len(object.ClaimToken) > AttemptWorkspaceClaimTokenMaxBytes {
			return fmt.Errorf("model: invalid claimed Attempt Workspace object")
		}
	default:
		return fmt.Errorf("model: invalid Attempt Workspace object state")
	}
	return nil
}

func (object *AttemptWorkspaceObject) MarkContentReady(version WorkspaceContentVersion, mediaType string, size int64,
	checksum string, at time.Time,
) error {
	if object == nil || object.State != AttemptWorkspaceObjectStaged || object.HasContent() {
		return fmt.Errorf("model: Attempt Workspace object cannot accept content")
	}
	candidate := *object
	candidate.ContentVersion, candidate.MediaType, candidate.SizeBytes, candidate.SHA256 = version,
		strings.TrimSpace(mediaType), size, strings.ToLower(checksum)
	candidate.UpdatedAt = TimeUTC(at)
	if candidate.UpdatedAt.Before(object.UpdatedAt) || !candidate.UpdatedAt.Before(candidate.ExpiresAt) {
		return fmt.Errorf("model: Attempt Workspace staged content time is invalid")
	}
	if err := candidate.Validate(); err != nil {
		return err
	}
	*object = candidate
	return nil
}

func (object *AttemptWorkspaceObject) MarkCurrent(at time.Time) error {
	if object == nil || object.State != AttemptWorkspaceObjectStaged || !object.HasContent() {
		return fmt.Errorf("model: Attempt Workspace object cannot become current")
	}
	candidate := *object
	candidate.State, candidate.UpdatedAt, candidate.ExpiresAt = AttemptWorkspaceObjectCurrent, TimeUTC(at), time.Time{}
	if candidate.UpdatedAt.Before(object.UpdatedAt) {
		return fmt.Errorf("model: Attempt Workspace object time regressed")
	}
	if err := candidate.Validate(); err != nil {
		return err
	}
	*object = candidate
	return nil
}

func (object *AttemptWorkspaceObject) MarkReclaimable(reclaimAfter, at time.Time) error {
	if object == nil || (object.State != AttemptWorkspaceObjectStaged && object.State != AttemptWorkspaceObjectCurrent) {
		return fmt.Errorf("model: Attempt Workspace object cannot become reclaimable")
	}
	candidate := *object
	candidate.State, candidate.UpdatedAt = AttemptWorkspaceObjectReclaimable, TimeUTC(at)
	candidate.ReclaimAfter = OptionalTimeFrom(reclaimAfter)
	if candidate.UpdatedAt.Before(object.UpdatedAt) || !candidate.ReclaimAfter.Time.After(candidate.UpdatedAt) {
		return fmt.Errorf("model: Attempt Workspace reclaim time is invalid")
	}
	if err := candidate.Validate(); err != nil {
		return err
	}
	*object = candidate
	return nil
}

func (object *AttemptWorkspaceObject) ClaimForCleanup(token string, at time.Time) error {
	at = TimeUTC(at)
	if object == nil || object.State != AttemptWorkspaceObjectReclaimable || at.Before(object.ReclaimAfter.Time) || at.Before(object.UpdatedAt) {
		return fmt.Errorf("model: Attempt Workspace object cannot be claimed")
	}
	candidate := *object
	candidate.State, candidate.UpdatedAt, candidate.ClaimedAt, candidate.ClaimToken = AttemptWorkspaceObjectClaimed,
		at, OptionalTimeFrom(at), token
	if err := candidate.Validate(); err != nil {
		return err
	}
	*object = candidate
	return nil
}

func (object *AttemptWorkspaceObject) ReleaseCleanup(token string, at time.Time) error {
	at = TimeUTC(at)
	if object == nil || object.State != AttemptWorkspaceObjectClaimed || token != object.ClaimToken || at.Before(object.UpdatedAt) {
		return fmt.Errorf("model: Attempt Workspace cleanup claim does not match")
	}
	candidate := *object
	candidate.State, candidate.UpdatedAt, candidate.ClaimedAt, candidate.ClaimToken = AttemptWorkspaceObjectReclaimable,
		at, OptionalTime{}, ""
	if err := candidate.Validate(); err != nil {
		return err
	}
	*object = candidate
	return nil
}

// NewCandidateAttemptWorkspaceFile creates a file with no Starter Workspace
// provenance while retaining the Attempt's admission Revision provenance.
func NewCandidateAttemptWorkspaceFile(id AttemptWorkspaceEntryID, workspaceID ExamAttemptWorkspaceID,
	revisionID ExamRevisionID, path string, objectID AttemptWorkspaceObjectID, at time.Time,
) (*AttemptWorkspaceEntry, error) {
	normalized, err := NormalizeAttemptWorkspacePath(path)
	if err != nil {
		return nil, err
	}
	entry := &AttemptWorkspaceEntry{ID: id, WorkspaceID: workspaceID, AdmissionRevisionID: revisionID,
		Kind: StarterWorkspaceEntryFile, Path: normalized, CurrentObjectID: objectID,
		CreatedAt: TimeUTC(at), UpdatedAt: TimeUTC(at)}
	if err = entry.Validate(); err != nil {
		return nil, err
	}
	return entry, nil
}

// NewCandidateAttemptWorkspaceDirectory creates metadata-only empty directory
// state with no Starter Workspace provenance.
func NewCandidateAttemptWorkspaceDirectory(id AttemptWorkspaceEntryID, workspaceID ExamAttemptWorkspaceID,
	revisionID ExamRevisionID, path string, at time.Time,
) (*AttemptWorkspaceEntry, error) {
	normalized, err := NormalizeAttemptWorkspacePath(path)
	if err != nil {
		return nil, err
	}
	entry := &AttemptWorkspaceEntry{ID: id, WorkspaceID: workspaceID, AdmissionRevisionID: revisionID,
		Kind: StarterWorkspaceEntryDirectory, Path: normalized, CreatedAt: TimeUTC(at), UpdatedAt: TimeUTC(at)}
	if err = entry.Validate(); err != nil {
		return nil, err
	}
	return entry, nil
}

// Move changes only logical metadata; stable Entry and content-object
// identities deliberately survive a rename or subtree move.
func (entry *AttemptWorkspaceEntry) Move(path string, at time.Time) error {
	if entry == nil {
		return fmt.Errorf("model: nil Attempt Workspace entry")
	}
	normalized, err := NormalizeAttemptWorkspacePath(path)
	if err != nil || normalized == entry.Path {
		return fmt.Errorf("model: invalid Attempt Workspace move")
	}
	candidate := *entry
	candidate.Path, candidate.UpdatedAt = normalized, TimeUTC(at)
	if candidate.UpdatedAt.Before(entry.UpdatedAt) {
		return fmt.Errorf("model: Attempt Workspace entry time regressed")
	}
	if err = candidate.Validate(); err != nil {
		return err
	}
	*entry = candidate
	return nil
}

// ReplaceCurrentObject selects one newly acknowledged immutable object while
// preserving the stable file Entry identity.
func (entry *AttemptWorkspaceEntry) ReplaceCurrentObject(objectID AttemptWorkspaceObjectID, at time.Time) error {
	if entry == nil || entry.Kind != StarterWorkspaceEntryFile || !objectID.IsValid() || objectID == entry.CurrentObjectID {
		return fmt.Errorf("model: invalid Attempt Workspace file replacement")
	}
	candidate := *entry
	candidate.CurrentObjectID, candidate.UpdatedAt = objectID, TimeUTC(at)
	if candidate.UpdatedAt.Before(entry.UpdatedAt) {
		return fmt.Errorf("model: Attempt Workspace entry time regressed")
	}
	if err := candidate.Validate(); err != nil {
		return err
	}
	*entry = candidate
	return nil
}

// AdvanceCursor appends exactly one acknowledged mutation to the ordered
// Workspace stream. Persistence supplies the expected cursor while holding
// the aggregate lock; callers do not use it to serialize unrelated entries.
func (workspace *ExamAttemptWorkspace) AdvanceCursor(expected int64, at time.Time) error {
	if workspace == nil || expected != workspace.Cursor || expected == math.MaxInt64 {
		return fmt.Errorf("model: stale Attempt Workspace cursor")
	}
	candidate := *workspace
	candidate.Cursor, candidate.UpdatedAt = expected+1, TimeUTC(at)
	if candidate.UpdatedAt.Before(workspace.UpdatedAt) {
		return fmt.Errorf("model: Attempt Workspace time regressed")
	}
	if err := candidate.Validate(); err != nil {
		return err
	}
	*workspace = candidate
	return nil
}

type AttemptWorkspaceMutationKind string

const (
	AttemptWorkspaceMutationCreateFile      AttemptWorkspaceMutationKind = "create_file"
	AttemptWorkspaceMutationCreateDirectory AttemptWorkspaceMutationKind = "create_directory"
	AttemptWorkspaceMutationReplaceFile     AttemptWorkspaceMutationKind = "replace_file"
	AttemptWorkspaceMutationMoveEntry       AttemptWorkspaceMutationKind = "move_entry"
	AttemptWorkspaceMutationDeleteEntry     AttemptWorkspaceMutationKind = "delete_entry"
)

func (kind AttemptWorkspaceMutationKind) IsValid() bool {
	switch kind {
	case AttemptWorkspaceMutationCreateFile, AttemptWorkspaceMutationCreateDirectory,
		AttemptWorkspaceMutationReplaceFile, AttemptWorkspaceMutationMoveEntry, AttemptWorkspaceMutationDeleteEntry:
		return true
	default:
		return false
	}
}

// AttemptWorkspaceJournalEntry is one safe acknowledged change in the
// Attempt-scoped ordered recovery stream. It contains logical metadata only:
// no object selector, backend revision, credential, Session, or content body.
type AttemptWorkspaceJournalEntry struct {
	WorkspaceID    ExamAttemptWorkspaceID
	Cursor         int64
	EntryID        AttemptWorkspaceEntryID
	EntryKind      StarterWorkspaceEntryKind
	Operation      AttemptWorkspaceMutationKind
	OldPath        string
	NewPath        string
	ContentVersion WorkspaceContentVersion
	ChangedAt      time.Time
}

func (entry AttemptWorkspaceJournalEntry) Validate() error {
	if !entry.WorkspaceID.IsValid() || entry.Cursor < 1 || !entry.EntryID.IsValid() ||
		!entry.Operation.IsValid() || entry.ChangedAt.IsZero() {
		return fmt.Errorf("model: invalid Attempt Workspace journal entry")
	}
	validOld := func() bool {
		normalized, err := NormalizeAttemptWorkspacePath(entry.OldPath)
		return err == nil && normalized == entry.OldPath
	}
	validNew := func() bool {
		normalized, err := NormalizeAttemptWorkspacePath(entry.NewPath)
		return err == nil && normalized == entry.NewPath
	}
	switch entry.Operation {
	case AttemptWorkspaceMutationCreateFile:
		if entry.EntryKind != StarterWorkspaceEntryFile || entry.OldPath != "" || !validNew() || !entry.ContentVersion.IsValid() {
			return fmt.Errorf("model: invalid Attempt Workspace file creation journal")
		}
	case AttemptWorkspaceMutationCreateDirectory:
		if entry.EntryKind != StarterWorkspaceEntryDirectory || entry.OldPath != "" || !validNew() || !entry.ContentVersion.IsZero() {
			return fmt.Errorf("model: invalid Attempt Workspace directory creation journal")
		}
	case AttemptWorkspaceMutationReplaceFile:
		if entry.EntryKind != StarterWorkspaceEntryFile || !validOld() || entry.NewPath != entry.OldPath || !entry.ContentVersion.IsValid() {
			return fmt.Errorf("model: invalid Attempt Workspace file replacement journal")
		}
	case AttemptWorkspaceMutationMoveEntry:
		if (entry.EntryKind != StarterWorkspaceEntryFile && entry.EntryKind != StarterWorkspaceEntryDirectory) ||
			!validOld() || !validNew() || entry.OldPath == entry.NewPath ||
			(entry.EntryKind == StarterWorkspaceEntryFile && !entry.ContentVersion.IsValid()) ||
			(entry.EntryKind == StarterWorkspaceEntryDirectory && !entry.ContentVersion.IsZero()) {
			return fmt.Errorf("model: invalid Attempt Workspace move journal")
		}
	case AttemptWorkspaceMutationDeleteEntry:
		if (entry.EntryKind != StarterWorkspaceEntryFile && entry.EntryKind != StarterWorkspaceEntryDirectory) ||
			!validOld() || entry.NewPath != "" || !entry.ContentVersion.IsZero() {
			return fmt.Errorf("model: invalid Attempt Workspace deletion journal")
		}
	}
	return nil
}
