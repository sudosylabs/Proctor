// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

const maximumProfilePictureBytes int64 = 5 << 20

var ErrInvalidProfilePicture = errors.New("invalid profile picture")

type FileContent interface {
	NormalizeAndStoreProfilePicture(context.Context, model.FileRevisionID, io.Reader, int64, time.Time) ([]model.FileRendition, error)
	OpenProfilePictureRendition(context.Context, model.FileRevisionID, model.FileRenditionID) (io.ReadCloser, error)
	RemoveProfilePictureRenditions(context.Context, model.FileRevisionID, []model.FileRendition) error
}

type UploadProfilePictureCommand struct {
	UserID           string
	ExpectedRevision int64
	Body             io.Reader
	Size             int64
}

type GetProfilePictureQuery struct {
	UserID string
	Size   int
}
type ProfilePictureContent struct {
	Body      io.ReadCloser
	MediaType string
	Size      int64
	ETag      string
}

type profilePictureUserStore interface {
	Get(context.Context, string) (*model.User, error)
}
type profilePictureFileStore interface {
	CreateUpload(context.Context, *store.FileUploadCreation) (*store.FileUpload, error)
	PublishProfilePicture(context.Context, *store.ProfilePicturePublication) (*store.ProfilePicturePublicationResult, error)
	GetProfilePictureRendition(context.Context, model.UserID, string) (*model.FileRendition, error)
}

type profilePictureAuthorizer interface {
	AuthorizeRead(context.Context, Invocation, string) error
	AuthorizeProfilePictureWrite(context.Context, Invocation, string) error
}

type profilePictureService struct {
	users         profilePictureUserStore
	files         profilePictureFileStore
	content       FileContent
	authorization profilePictureAuthorizer
	now           func() time.Time
}

func newProfilePictureService(users profilePictureUserStore, files profilePictureFileStore, content FileContent, authorization profilePictureAuthorizer, now func() time.Time) *profilePictureService {
	return &profilePictureService{users: users, files: files, content: content, authorization: authorization, now: now}
}

func (a *App) UploadProfilePicture(ctx context.Context, invocation Invocation, command UploadProfilePictureCommand) (*model.User, error) {
	return a.profilePictures.Upload(ctx, invocation, command)
}

func (s *profilePictureService) Upload(ctx context.Context, invocation Invocation, command UploadProfilePictureCommand) (*model.User, error) {
	userID := strings.TrimSpace(command.UserID)
	if !model.IsValidId(userID) || command.ExpectedRevision < 0 || command.Body == nil || command.Size == 0 || command.Size < -1 || command.Size > maximumProfilePictureBytes {
		return nil, NewError("request.invalid").WithField("field", "profile_picture")
	}
	if err := s.authorization.AuthorizeProfilePictureWrite(ctx, invocation, userID); err != nil {
		return nil, err
	}
	current, err := s.users.Get(ctx, userID)
	if err != nil {
		return nil, userProfileError(err)
	}
	if command.ExpectedRevision > 0 && current.Revision != command.ExpectedRevision {
		return nil, NewError("user.conflict").WithField("resource", "user")
	}
	if !current.CustomProfilePictureFileID.IsZero() {
		return nil, NewError("user.conflict").WithField("resource", "profile_picture")
	}
	at := model.TimeUTC(s.now())
	entry, err := model.NewFileEntry(model.NewFileEntryID(), model.FileIndexingNone, at)
	if err != nil {
		return nil, profilePictureError(err)
	}
	revision, err := model.NewFileRevision(model.NewFileRevisionID(), entry.ID, model.FileAvailabilityPending, model.FileIndexingNotRequired, at)
	if err != nil {
		return nil, profilePictureError(err)
	}
	lease, err := model.NewUploadLease(model.NewUploadLeaseID(), revision.ID, invocation.Principal().UserID, at, at.Add(time.Hour))
	if err != nil {
		return nil, profilePictureError(err)
	}
	if _, err = s.files.CreateUpload(ctx, &store.FileUploadCreation{Entry: entry, Revision: revision, Lease: lease}); err != nil {
		return nil, profilePictureError(err)
	}
	renditions, err := s.content.NormalizeAndStoreProfilePicture(ctx, revision.ID, command.Body, command.Size, at)
	if err != nil {
		if errors.Is(err, ErrInvalidProfilePicture) {
			return nil, NewError("profile_picture.invalid").Wrap(err)
		}
		return nil, NewError("profile_picture.unavailable").Wrap(err)
	}
	changedAt := model.TimeUTC(s.now())
	if changedAt.Before(at) {
		changedAt = at
	}
	published, err := s.files.PublishProfilePicture(ctx, &store.ProfilePicturePublication{ActorID: invocation.Principal().UserID, UserID: current.ID, ExpectedUserRevision: current.Revision, EntryID: entry.ID, RevisionID: revision.ID, LeaseID: lease.ID, Renditions: renditions, ChangedAt: changedAt})
	if err != nil {
		// Publication may have committed even when the commit acknowledgement was
		// lost. Leave staged bytes for authoritative reconciliation/lease cleanup.
		return nil, profilePictureError(err)
	}
	return published.User, nil
}

func (a *App) GetProfilePicture(ctx context.Context, invocation Invocation, query GetProfilePictureQuery) (*ProfilePictureContent, error) {
	return a.profilePictures.Get(ctx, invocation, query)
}

func (s *profilePictureService) Get(ctx context.Context, invocation Invocation, query GetProfilePictureQuery) (*ProfilePictureContent, error) {
	id := strings.TrimSpace(query.UserID)
	if !model.IsValidId(id) || (query.Size != 128 && query.Size != 256 && query.Size != 512) {
		return nil, NewError("request.invalid")
	}
	if err := s.authorization.AuthorizeRead(ctx, invocation, id); err != nil {
		return nil, err
	}
	rendition, err := s.files.GetProfilePictureRendition(ctx, model.UserID(id), fmt.Sprintf("profile_%d", query.Size))
	if err != nil {
		return nil, profilePictureError(err)
	}
	body, err := s.content.OpenProfilePictureRendition(ctx, rendition.RevisionID, rendition.ID)
	if err != nil {
		return nil, NewError("profile_picture.unavailable").Wrap(err)
	}
	return &ProfilePictureContent{Body: body, MediaType: rendition.MediaType, Size: rendition.Size, ETag: `"` + rendition.SHA256 + `"`}, nil
}

func profilePictureError(err error) error {
	if store.IsNotFound(err) {
		return NewError("resource.not_found").Wrap(err)
	}
	if store.IsConflict(err) {
		return NewError("user.conflict").Wrap(err)
	}
	return NewError("profile_picture.unavailable").Wrap(err)
}
