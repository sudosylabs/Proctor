// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"math"
	"strings"
	"time"
)

const (
	AttemptWorkspaceMaximumEntries      = ExamWorkspaceMaximumEntries
	AttemptWorkspaceMaximumDepth        = 16
	AttemptWorkspaceMaximumSegmentBytes = 255
	AttemptWorkspaceMaximumPathBytes    = 1024
	AttemptWorkspaceMaximumFileBytes    = ExamWorkspaceMaximumFileBytes
	AttemptWorkspaceMaximumTotalBytes   = ExamWorkspaceMaximumTotalBytes
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

func (o *AttemptWorkspaceObject) HasContent() bool {
	return o != nil && o.ContentVersion.IsValid() && o.MediaType != "" &&
		o.SizeBytes >= 0 && validLowerSHA256(o.SHA256)
}

func (c AttemptWorkspaceContent) Validate() error {
	if c.MediaType == "" || strings.TrimSpace(c.MediaType) != c.MediaType || len(c.MediaType) > 255 ||
		c.SizeBytes < 0 || c.SizeBytes > AttemptWorkspaceMaximumFileBytes || !validLowerSHA256(c.SHA256) {
		return invalidModelError("AttemptWorkspaceContent.Validate", "attempt_workspace_content", "", "invalid Attempt Workspace content", "")
	}
	return nil
}

func validateAttemptWorkspaceObject(object *AttemptWorkspaceObject) error {
	if object == nil || !object.ID.IsValid() || !object.WorkspaceID.IsValid() || object.CreatedAt.IsZero() || object.UpdatedAt.IsZero() ||
		object.UpdatedAt.Before(object.CreatedAt) {
		return invalidModelError("validateAttemptWorkspaceObject", "exam_attempt_workspace", "", "invalid Attempt Workspace object", "")
	}
	hasAnyContent := !object.ContentVersion.IsZero() || object.MediaType != "" || object.SizeBytes != 0 || object.SHA256 != ""
	if hasAnyContent && (!object.ContentVersion.IsValid() || (AttemptWorkspaceContent{MediaType: object.MediaType,
		SizeBytes: object.SizeBytes, SHA256: object.SHA256}).Validate() != nil) {
		return invalidModelError("validateAttemptWorkspaceObject", "exam_attempt_workspace", "", "invalid Attempt Workspace object content", "")
	}
	if object.StorageOrigin == AttemptWorkspaceStorageStarter {
		if !object.StarterObjectID.IsValid() || object.State != AttemptWorkspaceObjectCurrent || !hasAnyContent ||
			!object.ExpiresAt.IsZero() || object.ReclaimAfter.Valid || object.ClaimedAt.Valid || object.ClaimToken != "" {
			return invalidModelError("validateAttemptWorkspaceObject", "exam_attempt_workspace", "", "invalid starter-origin Attempt Workspace object", "")
		}
		return nil
	}
	if object.StorageOrigin != AttemptWorkspaceStorageAttempt || !object.StarterObjectID.IsZero() {
		return invalidModelError("validateAttemptWorkspaceObject", "exam_attempt_workspace", "", "invalid attempt-origin Attempt Workspace object", "")
	}
	switch object.State {
	case AttemptWorkspaceObjectStaged:
		if !object.ExpiresAt.After(object.CreatedAt) || object.ReclaimAfter.Valid || object.ClaimedAt.Valid || object.ClaimToken != "" {
			return invalidModelError("validateAttemptWorkspaceObject", "exam_attempt_workspace", "", "invalid staged Attempt Workspace object", "")
		}
	case AttemptWorkspaceObjectCurrent:
		if !hasAnyContent || !object.ExpiresAt.IsZero() || object.ReclaimAfter.Valid || object.ClaimedAt.Valid || object.ClaimToken != "" {
			return invalidModelError("validateAttemptWorkspaceObject", "exam_attempt_workspace", "", "invalid current Attempt Workspace object", "")
		}
	case AttemptWorkspaceObjectReclaimable:
		if !object.ReclaimAfter.Valid || object.ReclaimAfter.Time.Before(object.CreatedAt) || object.ClaimedAt.Valid || object.ClaimToken != "" {
			return invalidModelError("validateAttemptWorkspaceObject", "exam_attempt_workspace", "", "invalid reclaimable Attempt Workspace object", "")
		}
	case AttemptWorkspaceObjectClaimed:
		if !object.ReclaimAfter.Valid || !object.ClaimedAt.Valid || object.ClaimedAt.Time != object.UpdatedAt ||
			object.ClaimToken == "" || strings.TrimSpace(object.ClaimToken) != object.ClaimToken || len(object.ClaimToken) > AttemptWorkspaceClaimTokenMaxBytes {
			return invalidModelError("validateAttemptWorkspaceObject", "exam_attempt_workspace", "", "invalid claimed Attempt Workspace object", "")
		}
	default:
		return invalidModelError("validateAttemptWorkspaceObject", "exam_attempt_workspace", "", "invalid Attempt Workspace object state", "")
	}
	return nil
}

func (o *AttemptWorkspaceObject) MarkContentReady(version WorkspaceContentVersion, mediaType string, size int64,
	checksum string, at time.Time,
) error {
	if o == nil || o.State != AttemptWorkspaceObjectStaged || o.HasContent() {
		return invalidModelError("AttemptWorkspaceObject.MarkContentReady", "attempt_workspace_object", "", "Attempt Workspace object cannot accept content", "")
	}
	candidate := *o
	candidate.ContentVersion, candidate.MediaType, candidate.SizeBytes, candidate.SHA256 = version,
		strings.TrimSpace(mediaType), size, strings.ToLower(checksum)
	candidate.UpdatedAt = TimeUTC(at)
	if candidate.UpdatedAt.Before(o.UpdatedAt) || !candidate.UpdatedAt.Before(candidate.ExpiresAt) {
		return invalidModelError("AttemptWorkspaceObject.MarkContentReady", "attempt_workspace_object", "", "Attempt Workspace staged content time is invalid", "")
	}
	if err := candidate.Validate(); err != nil {
		return err
	}
	*o = candidate
	return nil
}

func (o *AttemptWorkspaceObject) MarkCurrent(at time.Time) error {
	if o == nil || o.State != AttemptWorkspaceObjectStaged || !o.HasContent() {
		return invalidModelError("AttemptWorkspaceObject.MarkCurrent", "attempt_workspace_object", "", "Attempt Workspace object cannot become current", "")
	}
	candidate := *o
	candidate.State, candidate.UpdatedAt, candidate.ExpiresAt = AttemptWorkspaceObjectCurrent, TimeUTC(at), time.Time{}
	if candidate.UpdatedAt.Before(o.UpdatedAt) {
		return invalidModelError("AttemptWorkspaceObject.MarkCurrent", "attempt_workspace_object", "", "Attempt Workspace object time regressed", "")
	}
	if err := candidate.Validate(); err != nil {
		return err
	}
	*o = candidate
	return nil
}

func (o *AttemptWorkspaceObject) MarkReclaimable(reclaimAfter, at time.Time) error {
	if o == nil || (o.State != AttemptWorkspaceObjectStaged && o.State != AttemptWorkspaceObjectCurrent) {
		return invalidModelError("AttemptWorkspaceObject.MarkReclaimable", "attempt_workspace_object", "", "Attempt Workspace object cannot become reclaimable", "")
	}
	candidate := *o
	candidate.State, candidate.UpdatedAt = AttemptWorkspaceObjectReclaimable, TimeUTC(at)
	candidate.ReclaimAfter = OptionalTimeFrom(reclaimAfter)
	if candidate.UpdatedAt.Before(o.UpdatedAt) || !candidate.ReclaimAfter.Time.After(candidate.UpdatedAt) {
		return invalidModelError("AttemptWorkspaceObject.MarkReclaimable", "attempt_workspace_object", "", "Attempt Workspace reclaim time is invalid", "")
	}
	if err := candidate.Validate(); err != nil {
		return err
	}
	*o = candidate
	return nil
}

func (o *AttemptWorkspaceObject) ClaimForCleanup(token string, at time.Time) error {
	at = TimeUTC(at)
	if o == nil || o.State != AttemptWorkspaceObjectReclaimable || at.Before(o.ReclaimAfter.Time) || at.Before(o.UpdatedAt) {
		return invalidModelError("AttemptWorkspaceObject.ClaimForCleanup", "attempt_workspace_object", "", "Attempt Workspace object cannot be claimed", "")
	}
	candidate := *o
	candidate.State, candidate.UpdatedAt, candidate.ClaimedAt, candidate.ClaimToken = AttemptWorkspaceObjectClaimed,
		at, OptionalTimeFrom(at), token
	if err := candidate.Validate(); err != nil {
		return err
	}
	*o = candidate
	return nil
}

func (o *AttemptWorkspaceObject) ReleaseCleanup(token string, at time.Time) error {
	at = TimeUTC(at)
	if o == nil || o.State != AttemptWorkspaceObjectClaimed || token != o.ClaimToken || at.Before(o.UpdatedAt) {
		return invalidModelError("AttemptWorkspaceObject.ReleaseCleanup", "attempt_workspace_object", "", "Attempt Workspace cleanup claim does not match", "")
	}
	candidate := *o
	candidate.State, candidate.UpdatedAt, candidate.ClaimedAt, candidate.ClaimToken = AttemptWorkspaceObjectReclaimable,
		at, OptionalTime{}, ""
	if err := candidate.Validate(); err != nil {
		return err
	}
	*o = candidate
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

// AdvanceCursor appends exactly one acknowledged mutation to the ordered
// Workspace stream. Persistence supplies the expected cursor while holding
// the aggregate lock; callers do not use it to serialize unrelated entries.
func (w *ExamAttemptWorkspace) AdvanceCursor(expected int64, at time.Time) error {
	if w == nil || expected != w.Cursor || expected == math.MaxInt64 {
		return invalidModelError("ExamAttemptWorkspace.AdvanceCursor", "exam_attempt_workspace", "", "stale Attempt Workspace cursor", "")
	}
	candidate := *w
	candidate.Cursor, candidate.UpdatedAt = expected+1, TimeUTC(at)
	if candidate.UpdatedAt.Before(w.UpdatedAt) {
		return invalidModelError("ExamAttemptWorkspace.AdvanceCursor", "exam_attempt_workspace", "", "Attempt Workspace time regressed", "")
	}
	if err := candidate.Validate(); err != nil {
		return err
	}
	*w = candidate
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

func (k AttemptWorkspaceMutationKind) IsValid() bool {
	switch k {
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
	// Recursive deletes OldPath and every descendant separated by a slash.
	Recursive bool
}

func (e AttemptWorkspaceJournalEntry) Validate() error {
	if !e.WorkspaceID.IsValid() || e.Cursor < 1 || !e.EntryID.IsValid() ||
		!e.Operation.IsValid() || e.ChangedAt.IsZero() {
		return invalidModelError("AttemptWorkspaceJournalEntry.Validate", "attempt_workspace_journal_entry", "", "invalid Attempt Workspace journal entry", "")
	}
	if e.Recursive && (e.Operation != AttemptWorkspaceMutationDeleteEntry || e.EntryKind != StarterWorkspaceEntryDirectory) {
		return invalidModelError("AttemptWorkspaceJournalEntry.Validate", "attempt_workspace_journal_entry", "", "recursive Workspace journal entry must delete a directory", "")
	}
	validOld := func() bool {
		normalized, err := NormalizeAttemptWorkspacePath(e.OldPath)
		return err == nil && normalized == e.OldPath
	}
	validNew := func() bool {
		normalized, err := NormalizeAttemptWorkspacePath(e.NewPath)
		return err == nil && normalized == e.NewPath
	}
	switch e.Operation {
	case AttemptWorkspaceMutationCreateFile:
		if e.EntryKind != StarterWorkspaceEntryFile || e.OldPath != "" || !validNew() || !e.ContentVersion.IsValid() {
			return invalidModelError("AttemptWorkspaceJournalEntry.Validate", "attempt_workspace_journal_entry", "", "invalid Attempt Workspace file creation journal", "")
		}
	case AttemptWorkspaceMutationCreateDirectory:
		if e.EntryKind != StarterWorkspaceEntryDirectory || e.OldPath != "" || !validNew() || !e.ContentVersion.IsZero() {
			return invalidModelError("AttemptWorkspaceJournalEntry.Validate", "attempt_workspace_journal_entry", "", "invalid Attempt Workspace directory creation journal", "")
		}
	case AttemptWorkspaceMutationReplaceFile:
		if e.EntryKind != StarterWorkspaceEntryFile || !validOld() || e.NewPath != e.OldPath || !e.ContentVersion.IsValid() {
			return invalidModelError("AttemptWorkspaceJournalEntry.Validate", "attempt_workspace_journal_entry", "", "invalid Attempt Workspace file replacement journal", "")
		}
	case AttemptWorkspaceMutationMoveEntry:
		if (e.EntryKind != StarterWorkspaceEntryFile && e.EntryKind != StarterWorkspaceEntryDirectory) ||
			!validOld() || !validNew() || e.OldPath == e.NewPath ||
			(e.EntryKind == StarterWorkspaceEntryFile && !e.ContentVersion.IsValid()) ||
			(e.EntryKind == StarterWorkspaceEntryDirectory && !e.ContentVersion.IsZero()) {
			return invalidModelError("AttemptWorkspaceJournalEntry.Validate", "attempt_workspace_journal_entry", "", "invalid Attempt Workspace move journal", "")
		}
	case AttemptWorkspaceMutationDeleteEntry:
		if (e.EntryKind != StarterWorkspaceEntryFile && e.EntryKind != StarterWorkspaceEntryDirectory) ||
			!validOld() || e.NewPath != "" || !e.ContentVersion.IsZero() {
			return invalidModelError("AttemptWorkspaceJournalEntry.Validate", "attempt_workspace_journal_entry", "", "invalid Attempt Workspace deletion journal", "")
		}
	}
	return nil
}
