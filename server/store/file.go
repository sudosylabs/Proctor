// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package store

import (
	"context"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

type FileUploadCreation struct {
	Entry    *model.FileEntry
	Revision *model.FileRevision
	Lease    *model.UploadLease
}

type FileUpload struct {
	Entry    *model.FileEntry
	Revision *model.FileRevision
	Lease    *model.UploadLease
}

type FileRevisionUploadCreation struct {
	EntryID  model.FileEntryID
	Revision *model.FileRevision
	Lease    *model.UploadLease
}

type ProfilePicturePublication struct {
	ActorID              model.UserID
	UserID               model.UserID
	ExpectedUserRevision int64
	EntryID              model.FileEntryID
	RevisionID           model.FileRevisionID
	LeaseID              model.UploadLeaseID
	Renditions           []model.FileRendition
	ChangedAt            time.Time
	AuditEventID         model.AuditEventID
	AuditAt              time.Time
}

type ProfilePicturePublicationResult struct {
	User     *model.User
	Revision *model.FileRevision
}

type DefaultProfilePicturePublication struct {
	UserID               model.UserID
	ExpectedUserRevision int64
	EntryID              model.FileEntryID
	RevisionID           model.FileRevisionID
	LeaseID              model.UploadLeaseID
	Renditions           []model.FileRendition
	AttachedAt           time.Time
}

type ProfilePictureState struct {
	EntryID    model.FileEntryID
	RevisionID model.FileRevisionID
	Renditions []model.FileRendition
}

type ProfilePictureUploadDiscard struct {
	ActorID                   model.UserID
	UserID                    model.UserID
	ExpectedUserRevision      int64
	ExpectedActiveEntryID     model.FileEntryID
	ExpectedCurrentRevisionID model.FileRevisionID
	UploadEntryID             model.FileEntryID
	RevisionID                model.FileRevisionID
	LeaseID                   model.UploadLeaseID
}

type ProfilePictureRemoval struct {
	ActorID                   model.UserID
	UserID                    model.UserID
	ExpectedUserRevision      int64
	EntryID                   model.FileEntryID
	ExpectedCurrentRevisionID model.FileRevisionID
	ExpectedSHA256            string
	ChangedAt                 time.Time
	AuditEventID              model.AuditEventID
	AuditAt                   time.Time
}

type FilePurgeCandidateKind string

const (
	FilePurgeCandidateExpiredLease   FilePurgeCandidateKind = "expired_lease"
	FilePurgeCandidateArchivedCustom FilePurgeCandidateKind = "archived_custom"
)

// FilePurgeCandidate is an authoritative metadata-derived unit of physical
// cleanup. Cursor and IDs are safe operational values; storage paths never
// cross this boundary.
type FilePurgeCandidate struct {
	Cursor       string
	Kind         FilePurgeCandidateKind
	LeaseID      model.UploadLeaseID
	EntryID      model.FileEntryID
	RevisionID   model.FileRevisionID
	RenditionIDs []model.FileRenditionID
}

type FilePurgeCandidateRequest struct {
	After string
	Limit int
}

type FilePurgeCandidatePage struct {
	Candidates []FilePurgeCandidate
}

// FilePurgeClaim is the durable tombstone created before physical content is
// removed. Reusing a claim makes cleanup recoverable after a worker crash.
type FilePurgeClaim struct {
	ID        string
	Candidate FilePurgeCandidate
}

// FileStore owns file metadata and the atomic publication of domain references.
type FileStore interface {
	// CreateUpload atomically creates a pristine entry, its first pending revision,
	// and an active bounded lease owned by the named actor.
	CreateUpload(context.Context, *FileUploadCreation) (*FileUpload, error)
	// CreateRevisionUpload atomically appends a pending revision and active lease
	// to an existing, unarchived entry without changing its current revision.
	CreateRevisionUpload(context.Context, *FileRevisionUploadCreation) (*FileUpload, error)
	// DiscardProfilePictureUpload removes only the named pending revision and lease
	// when the actor, user revision, active entry, and current revision still match.
	// If the upload created a distinct pristine entry, that entry is removed too;
	// the operation never changes the visible picture.
	DiscardProfilePictureUpload(context.Context, *ProfilePictureUploadDiscard) error
	RenewUploadLease(context.Context, *UploadLeaseRenewal) (*model.UploadLease, error)
	// PublishProfilePicture atomically consumes the actor-owned, unexpired lease,
	// persists the complete rendition set, makes the revision current, updates the
	// user's active custom entry under optimistic concurrency, and completes the
	// pre-existing audit attempt. Any failed precondition rolls back every change.
	PublishProfilePicture(context.Context, *ProfilePicturePublication) (*ProfilePicturePublicationResult, error)
	// PublishDefaultProfilePicture atomically consumes the target-user-owned,
	// unexpired upload lease, publishes the complete rendition set, and attaches
	// the entry only while that User still has no default and its revision matches.
	// It advances User revision without changing visible-picture time.
	PublishDefaultProfilePicture(context.Context, *DefaultProfilePicturePublication) (*ProfilePicturePublicationResult, error)
	GetProfilePictureState(context.Context, model.UserID) (*ProfilePictureState, error)
	GetProfilePictureRendition(context.Context, model.UserID, string) (*model.FileRendition, error)
	// RemoveProfilePicture atomically verifies the user's revision, active
	// custom entry, current file revision, and expected normalized checksum; it then
	// archives that entry, clears the custom relationship, advances the user, and
	// completes the pre-existing audit attempt. Races fail with no partial change.
	RemoveProfilePicture(context.Context, *ProfilePictureRemoval) (*model.User, error)
	// ListPurgeCandidates returns a stable bounded page derived from leases and
	// semantic metadata using the database clock. It never discovers ownership
	// by listing VFS objects.
	ListPurgeCandidates(context.Context, *FilePurgeCandidateRequest) (*FilePurgeCandidatePage, error)
	// ClaimPurgeCandidate atomically revalidates eligibility against the database
	// clock and writes a durable tombstone before any physical content is removed.
	ClaimPurgeCandidate(context.Context, *FilePurgeCandidate) (*FilePurgeClaim, error)
	// CompletePurge deletes metadata only after the caller has idempotently
	// removed the claimed physical content.
	CompletePurge(context.Context, *FilePurgeClaim) error
}

// UploadLeaseRenewal is the input to FileStore.RenewUploadLease.
type UploadLeaseRenewal struct {
	ID               model.UploadLeaseID
	ActorID          model.UserID
	ExpectedRevision int64
	BytesReceived    int64
	ExpiresAt        time.Time
}
