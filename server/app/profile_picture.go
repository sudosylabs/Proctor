// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"encoding/json"
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
	ExpectedSHA256   string
	Body             io.Reader
	Size             int64
}

type GetProfilePictureQuery struct {
	UserID string
	Size   int
}
type RemoveProfilePictureCommand struct {
	UserID         string
	ExpectedSHA256 string
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
	CreateRevisionUpload(context.Context, *store.FileRevisionUploadCreation) (*store.FileUpload, error)
	DiscardProfilePictureUpload(context.Context, *store.ProfilePictureUploadDiscard) error
	PublishProfilePicture(context.Context, *store.ProfilePicturePublication) (*store.ProfilePicturePublicationResult, error)
	GetProfilePictureState(context.Context, model.UserID) (*store.ProfilePictureState, error)
	GetProfilePictureRendition(context.Context, model.UserID, string) (*model.FileRendition, error)
	RemoveProfilePictureWithAudit(context.Context, *store.ProfilePictureRemoval) (*model.User, error)
}

type profilePictureAuthorizer interface {
	AuthorizeRead(context.Context, Invocation, string) error
	AuthorizeProfilePictureWrite(context.Context, Invocation, string) error
}

type profilePictureChanged struct {
	UserID            model.UserID
	ActiveFileEntryID model.FileEntryID
	Revision          int64
	ChangedAt         time.Time
}

type profilePictureEffects interface {
	Changed(context.Context, profilePictureChanged) error
}

type profilePictureEffectFailures interface {
	Report(context.Context, string, error)
}

type profilePictureService struct {
	users          profilePictureUserStore
	files          profilePictureFileStore
	content        FileContent
	authorization  profilePictureAuthorizer
	audit          mutationAuditor
	effects        profilePictureEffects
	effectFailures profilePictureEffectFailures
	now            func() time.Time
}

func newProfilePictureService(users profilePictureUserStore, files profilePictureFileStore, content FileContent, authorization profilePictureAuthorizer, audit mutationAuditor, effects profilePictureEffects, effectFailures profilePictureEffectFailures, now func() time.Time) *profilePictureService {
	return &profilePictureService{users: users, files: files, content: content, authorization: authorization, audit: audit, effects: effects, effectFailures: effectFailures, now: now}
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
	if (!current.CustomProfilePictureFileID.IsZero() || !current.DefaultProfilePictureFileID.IsZero()) && strings.TrimSpace(command.ExpectedSHA256) == "" {
		return nil, NewError("request.invalid").WithField("field", "if_match")
	}
	var currentState *store.ProfilePictureState
	activeState, stateErr := s.files.GetProfilePictureState(ctx, current.ID)
	if stateErr == nil {
		currentState = activeState
		expectedEntryID := current.DefaultProfilePictureFileID
		if !current.CustomProfilePictureFileID.IsZero() {
			expectedEntryID = current.CustomProfilePictureFileID
		}
		if strings.TrimSpace(command.ExpectedSHA256) == "" {
			return nil, NewError("request.invalid").WithField("field", "if_match")
		}
		if activeState.EntryID != expectedEntryID || !profilePictureChecksumMatches(activeState, command.ExpectedSHA256) {
			return nil, NewError("user.conflict").WithField("resource", "profile_picture")
		}
	} else if !store.IsNotFound(stateErr) || !current.CustomProfilePictureFileID.IsZero() || !current.DefaultProfilePictureFileID.IsZero() {
		return nil, profilePictureError(stateErr)
	}
	at := model.TimeUTC(s.now())
	entryID := current.CustomProfilePictureFileID
	var entry *model.FileEntry
	if entryID.IsZero() {
		entry, err = model.NewFileEntry(model.NewFileEntryID(), model.FileIndexingNone, at)
		if err != nil {
			return nil, profilePictureError(err)
		}
		entryID = entry.ID
	}
	revision, err := model.NewFileRevision(model.NewFileRevisionID(), entryID, model.FileAvailabilityPending, model.FileIndexingNotRequired, at)
	if err != nil {
		return nil, profilePictureError(err)
	}
	lease, err := model.NewUploadLease(model.NewUploadLeaseID(), revision.ID, invocation.Principal().UserID, at, at.Add(time.Hour))
	if err != nil {
		return nil, profilePictureError(err)
	}
	if entry != nil {
		_, err = s.files.CreateUpload(ctx, &store.FileUploadCreation{Entry: entry, Revision: revision, Lease: lease})
	} else {
		_, err = s.files.CreateRevisionUpload(ctx, &store.FileRevisionUploadCreation{EntryID: entryID, Revision: revision, Lease: lease})
	}
	if err != nil {
		return nil, profilePictureError(err)
	}
	renditions, err := s.content.NormalizeAndStoreProfilePicture(ctx, revision.ID, command.Body, command.Size, at)
	if err != nil {
		if errors.Is(err, ErrInvalidProfilePicture) {
			return nil, NewError("profile_picture.invalid").Wrap(err)
		}
		return nil, NewError("profile_picture.unavailable").Wrap(err)
	}
	if currentState != nil && sameNormalizedProfilePicture(currentState, renditions) {
		if err = s.content.RemoveProfilePictureRenditions(ctx, revision.ID, renditions); err != nil {
			return nil, NewError("profile_picture.unavailable").Wrap(err)
		}
		err = s.files.DiscardProfilePictureUpload(ctx, &store.ProfilePictureUploadDiscard{ActorID: invocation.Principal().UserID, UserID: current.ID, ExpectedUserRevision: current.Revision, ExpectedActiveEntryID: currentState.EntryID, ExpectedCurrentRevisionID: currentState.RevisionID, UploadEntryID: entryID, RevisionID: revision.ID, LeaseID: lease.ID})
		if err != nil {
			return nil, profilePictureError(err)
		}
		return current, nil
	}
	changedAt := model.TimeUTC(s.now())
	if changedAt.Before(at) {
		changedAt = at
	}
	action := model.ActionUserManage
	if invocation.Principal().UserID == current.ID {
		action = model.ActionUserProfilePictureManage
	}
	auditID, err := s.audit.Begin(ctx, invocation, action, model.Resource{Type: model.ResourceUser, ID: current.ID.String()}, "set_profile_picture", map[string]any{"user_id": current.ID.String(), "active_file_entry_id": entryID.String()}, map[string]any{"user": current.Auditable(), "active_file_entry_id": current.CustomProfilePictureFileID.String()})
	if err != nil {
		return nil, err
	}
	published, err := s.files.PublishProfilePicture(ctx, &store.ProfilePicturePublication{ActorID: invocation.Principal().UserID, UserID: current.ID, ExpectedUserRevision: current.Revision, EntryID: entryID, RevisionID: revision.ID, LeaseID: lease.ID, Renditions: renditions, ChangedAt: changedAt, AuditEventID: auditID, AuditAt: changedAt.UnixMilli()})
	if err != nil {
		// Publication may have committed even when the commit acknowledgement was
		// lost. Leave staged bytes for authoritative reconciliation/lease cleanup.
		mapped := profilePictureError(err)
		failure, _ := As(mapped)
		if auditErr := s.audit.Fail(ctx, auditID, failure.Code()); auditErr != nil {
			return nil, auditErr
		}
		return nil, mapped
	}
	change := profilePictureChanged{UserID: published.User.ID, ActiveFileEntryID: published.User.CustomProfilePictureFileID, Revision: published.User.Revision, ChangedAt: published.User.ProfilePictureChangedAt.Time}
	if err := s.effects.Changed(ctx, change); err != nil {
		s.effectFailures.Report(ctx, "user_profile_picture_changed", err)
	}
	return published.User, nil
}

type profilePictureRealtimeEffects struct{ realtime *RealtimeService }

func (e profilePictureRealtimeEffects) Changed(ctx context.Context, change profilePictureChanged) error {
	data, err := json.Marshal(struct {
		ActiveFileEntryID string `json:"active_file_entry_id"`
		Revision          int64  `json:"revision"`
		ChangedAt         int64  `json:"changed_at"`
	}{ActiveFileEntryID: change.ActiveFileEntryID.String(), Revision: change.Revision, ChangedAt: change.ChangedAt.UnixMilli()})
	if err != nil {
		return err
	}
	return e.realtime.Publish(ctx, RealtimeEvent{Name: "user_profile_picture_changed", UserID: change.UserID.String(), Data: data})
}

type profilePictureEffectReporter struct{ realtime *RealtimeService }

func (r profilePictureEffectReporter) Report(ctx context.Context, operation string, err error) {
	r.realtime.reportTransientFailure(ctx, operation, err)
}

func profilePictureChecksumMatches(state *store.ProfilePictureState, expected string) bool {
	if state == nil {
		return false
	}
	expected = strings.TrimSpace(expected)
	for _, rendition := range state.Renditions {
		if rendition.SHA256 == expected {
			return true
		}
	}
	return false
}

func sameNormalizedProfilePicture(state *store.ProfilePictureState, renditions []model.FileRendition) bool {
	if state == nil || len(state.Renditions) != len(renditions) {
		return false
	}
	checksums := make(map[string]string, len(state.Renditions))
	for _, rendition := range state.Renditions {
		checksums[rendition.Name] = rendition.SHA256
	}
	for _, rendition := range renditions {
		if checksums[rendition.Name] != rendition.SHA256 {
			return false
		}
	}
	return true
}

func (a *App) GetProfilePicture(ctx context.Context, invocation Invocation, query GetProfilePictureQuery) (*ProfilePictureContent, error) {
	return a.profilePictures.Get(ctx, invocation, query)
}

func (a *App) RemoveProfilePicture(ctx context.Context, invocation Invocation, command RemoveProfilePictureCommand) (*model.User, error) {
	return a.profilePictures.Remove(ctx, invocation, command)
}

func (s *profilePictureService) Remove(ctx context.Context, invocation Invocation, command RemoveProfilePictureCommand) (*model.User, error) {
	userID := strings.TrimSpace(command.UserID)
	expectedSHA256 := strings.TrimSpace(command.ExpectedSHA256)
	if !model.IsValidId(userID) || expectedSHA256 == "" {
		return nil, NewError("request.invalid").WithField("field", "if_match")
	}
	if err := s.authorization.AuthorizeProfilePictureWrite(ctx, invocation, userID); err != nil {
		return nil, err
	}
	current, err := s.users.Get(ctx, userID)
	if err != nil {
		return nil, userProfileError(err)
	}
	if current.CustomProfilePictureFileID.IsZero() {
		return nil, NewError("user.conflict").WithField("resource", "profile_picture")
	}
	state, err := s.files.GetProfilePictureState(ctx, current.ID)
	if err != nil {
		return nil, profilePictureError(err)
	}
	if state.EntryID != current.CustomProfilePictureFileID || !profilePictureChecksumMatches(state, expectedSHA256) {
		return nil, NewError("user.conflict").WithField("resource", "profile_picture")
	}
	action := model.ActionUserManage
	if invocation.Principal().UserID == current.ID {
		action = model.ActionUserProfilePictureManage
	}
	auditID, err := s.audit.Begin(ctx, invocation, action, model.Resource{Type: model.ResourceUser, ID: current.ID.String()}, "remove_profile_picture", map[string]any{"user_id": current.ID.String()}, map[string]any{"user": current.Auditable(), "active_file_entry_id": current.CustomProfilePictureFileID.String()})
	if err != nil {
		return nil, err
	}
	changedAt := model.TimeUTC(s.now())
	updated, err := s.files.RemoveProfilePictureWithAudit(ctx, &store.ProfilePictureRemoval{ActorID: invocation.Principal().UserID, UserID: current.ID, ExpectedUserRevision: current.Revision, EntryID: state.EntryID, ExpectedCurrentRevisionID: state.RevisionID, ExpectedSHA256: expectedSHA256, ChangedAt: changedAt, AuditEventID: auditID, AuditAt: changedAt.UnixMilli()})
	if err != nil {
		mapped := profilePictureError(err)
		failure, _ := As(mapped)
		if auditErr := s.audit.Fail(ctx, auditID, failure.Code()); auditErr != nil {
			return nil, auditErr
		}
		return nil, mapped
	}
	change := profilePictureChanged{UserID: updated.ID, ActiveFileEntryID: updated.DefaultProfilePictureFileID, Revision: updated.Revision, ChangedAt: updated.ProfilePictureChangedAt.Time}
	if err := s.effects.Changed(ctx, change); err != nil {
		s.effectFailures.Report(ctx, "user_profile_picture_changed", err)
	}
	return updated, nil
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
