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
var errDefaultProfilePictureUserNotFound = errors.New("default profile-picture user not found")
var errDefaultProfilePictureInvariant = errors.New("default profile-picture invariant failed")

// ProfilePictureUploadFiles is the content capability used by interactive
// profile-picture uploads.
type ProfilePictureUploadFiles interface {
	NormalizeAndStoreProfilePicture(context.Context, model.FileRevisionID, io.Reader, int64, time.Time) ([]model.FileRendition, error)
	RemoveProfilePictureRenditions(context.Context, model.FileRevisionID, []model.FileRendition) error
}

// ProfilePictureReadFiles opens the one rendition selected by authoritative
// profile-picture metadata.
type ProfilePictureReadFiles interface {
	OpenProfilePictureRendition(context.Context, model.FileRevisionID, model.FileRenditionID) (io.ReadCloser, error)
}

// DefaultProfilePictureRenderFiles renders an unpersisted fallback read.
type DefaultProfilePictureRenderFiles interface {
	RenderDefaultProfilePicture(context.Context, string, int) (*RenderedProfilePicture, error)
}

// DefaultProfilePictureGenerationFiles creates the complete persisted default
// rendition set for the durable generation workflow.
type DefaultProfilePictureGenerationFiles interface {
	GenerateAndStoreDefaultProfilePicture(context.Context, model.FileRevisionID, string, time.Time) ([]model.FileRendition, error)
}

// FileContent is the bounded composition contract. Individual workflows keep
// only the narrower consumer-owned capabilities above and in file_purge_job.go.
type FileContent interface {
	ProfilePictureUploadFiles
	ProfilePictureReadFiles
	DefaultProfilePictureRenderFiles
	DefaultProfilePictureGenerationFiles
	FileRevisionContentPurger
}

type RenderedProfilePicture struct {
	Body      io.ReadCloser
	MediaType string
	Size      int64
	SHA256    string
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
	PublishDefaultProfilePicture(context.Context, *store.DefaultProfilePicturePublication) (*store.ProfilePicturePublicationResult, error)
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

type profilePictureDefaultJobs interface {
	ProposeDefaultProfilePicture(context.Context, model.UserID, time.Time) error
}

type profilePictureService struct {
	uploads  *profilePictureUploadService
	removals *profilePictureRemovalService
	reads    *profilePictureReadService
	defaults *defaultProfilePictureService
}

type profilePictureUploadService struct {
	users          profilePictureUserStore
	files          profilePictureFileStore
	content        ProfilePictureUploadFiles
	authorization  profilePictureAuthorizer
	audit          mutationAuditor
	effects        profilePictureEffects
	effectFailures profilePictureEffectFailures
	now            func() time.Time
}

type profilePictureRemovalService struct {
	users          profilePictureUserStore
	files          profilePictureFileStore
	authorization  profilePictureAuthorizer
	audit          mutationAuditor
	effects        profilePictureEffects
	effectFailures profilePictureEffectFailures
	now            func() time.Time
}

type profilePictureReadService struct {
	users          profilePictureUserStore
	files          profilePictureFileStore
	content        ProfilePictureReadFiles
	fallback       DefaultProfilePictureRenderFiles
	authorization  profilePictureAuthorizer
	effectFailures profilePictureEffectFailures
	defaultJobs    profilePictureDefaultJobs
	now            func() time.Time
}

type defaultProfilePictureService struct {
	users   profilePictureUserStore
	files   profilePictureFileStore
	content DefaultProfilePictureGenerationFiles
	now     func() time.Time
}

func newProfilePictureService(users profilePictureUserStore, files profilePictureFileStore, uploads ProfilePictureUploadFiles, reads ProfilePictureReadFiles, fallback DefaultProfilePictureRenderFiles, defaults DefaultProfilePictureGenerationFiles, authorization profilePictureAuthorizer, audit mutationAuditor, effects profilePictureEffects, effectFailures profilePictureEffectFailures, defaultJobs profilePictureDefaultJobs, now func() time.Time) *profilePictureService {
	return &profilePictureService{
		uploads:  &profilePictureUploadService{users: users, files: files, content: uploads, authorization: authorization, audit: audit, effects: effects, effectFailures: effectFailures, now: now},
		removals: &profilePictureRemovalService{users: users, files: files, authorization: authorization, audit: audit, effects: effects, effectFailures: effectFailures, now: now},
		reads:    &profilePictureReadService{users: users, files: files, content: reads, fallback: fallback, authorization: authorization, effectFailures: effectFailures, defaultJobs: defaultJobs, now: now},
		defaults: &defaultProfilePictureService{users: users, files: files, content: defaults, now: now},
	}
}

func (a *App) UploadProfilePicture(ctx context.Context, invocation Invocation, command UploadProfilePictureCommand) (*model.User, error) {
	return a.profilePictures.Upload(ctx, invocation, command)
}

func (s *profilePictureService) Upload(ctx context.Context, invocation Invocation, command UploadProfilePictureCommand) (*model.User, error) {
	return s.uploads.Upload(ctx, invocation, command)
}

func (s *profilePictureUploadService) Upload(ctx context.Context, invocation Invocation, command UploadProfilePictureCommand) (*model.User, error) {
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
	published, err := runAuditedMutation(
		ctx,
		s.audit,
		mutationAttempt{
			Invocation: invocation,
			Action:     action,
			Resource:   model.Resource{Type: model.ResourceUser, ID: current.ID.String()},
			Operation:  "set_profile_picture",
			Value:      map[string]any{"user_id": current.ID.String(), "active_file_entry_id": entryID.String()},
			Prior:      map[string]any{"user": current.Auditable(), "active_file_entry_id": current.CustomProfilePictureFileID.String()},
		},
		func() time.Time { return changedAt },
		func(ctx context.Context, reference mutationAttemptReference) (*store.ProfilePicturePublicationResult, error) {
			return s.files.PublishProfilePicture(ctx, &store.ProfilePicturePublication{
				ActorID: invocation.Principal().UserID, UserID: current.ID,
				ExpectedUserRevision: current.Revision, EntryID: entryID, RevisionID: revision.ID,
				LeaseID: lease.ID, Renditions: renditions, ChangedAt: changedAt,
				AuditEventID: reference.ID, AuditAt: reference.AtMillis,
			})
		},
		profilePictureError,
	)
	if err != nil {
		// Publication may have committed even when the commit acknowledgement was
		// lost. Leave staged bytes for authoritative reconciliation/lease cleanup.
		return nil, err
	}
	change := profilePictureChanged{UserID: published.User.ID, ActiveFileEntryID: published.User.CustomProfilePictureFileID, Revision: published.User.Revision, ChangedAt: published.User.ProfilePictureChangedAt.Time}
	if err := s.effects.Changed(ctx, change); err != nil {
		s.effectFailures.Report(ctx, "user_profile_picture_changed", err)
	}
	return published.User, nil
}

type profilePictureRealtimeEffects struct{ realtime *realtimeService }

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

type profilePictureEffectReporter struct{ realtime *realtimeService }

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
	currentByName := make(map[string]model.FileRendition, len(state.Renditions))
	for _, rendition := range state.Renditions {
		if _, duplicate := currentByName[rendition.Name]; duplicate {
			return false
		}
		currentByName[rendition.Name] = rendition
	}
	for _, rendition := range renditions {
		current, found := currentByName[rendition.Name]
		if !found || current.MediaType != rendition.MediaType || current.Size != rendition.Size ||
			current.Width != rendition.Width || current.Height != rendition.Height || current.SHA256 != rendition.SHA256 {
			return false
		}
		delete(currentByName, rendition.Name)
	}
	return len(currentByName) == 0
}

func (a *App) GetProfilePicture(ctx context.Context, invocation Invocation, query GetProfilePictureQuery) (*ProfilePictureContent, error) {
	return a.profilePictures.Get(ctx, invocation, query)
}

func (a *App) RemoveProfilePicture(ctx context.Context, invocation Invocation, command RemoveProfilePictureCommand) (*model.User, error) {
	return a.profilePictures.Remove(ctx, invocation, command)
}

func (s *profilePictureService) Remove(ctx context.Context, invocation Invocation, command RemoveProfilePictureCommand) (*model.User, error) {
	return s.removals.Remove(ctx, invocation, command)
}

func (s *profilePictureRemovalService) Remove(ctx context.Context, invocation Invocation, command RemoveProfilePictureCommand) (*model.User, error) {
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
	var changedAt time.Time
	updated, err := runAuditedMutation(
		ctx,
		s.audit,
		mutationAttempt{
			Invocation: invocation,
			Action:     action,
			Resource:   model.Resource{Type: model.ResourceUser, ID: current.ID.String()},
			Operation:  "remove_profile_picture",
			Value:      map[string]any{"user_id": current.ID.String()},
			Prior:      map[string]any{"user": current.Auditable(), "active_file_entry_id": current.CustomProfilePictureFileID.String()},
		},
		func() time.Time {
			changedAt = model.TimeUTC(s.now())
			return changedAt
		},
		func(ctx context.Context, reference mutationAttemptReference) (*model.User, error) {
			return s.files.RemoveProfilePictureWithAudit(ctx, &store.ProfilePictureRemoval{
				ActorID: invocation.Principal().UserID, UserID: current.ID,
				ExpectedUserRevision: current.Revision, EntryID: state.EntryID,
				ExpectedCurrentRevisionID: state.RevisionID, ExpectedSHA256: expectedSHA256,
				ChangedAt: changedAt, AuditEventID: reference.ID, AuditAt: reference.AtMillis,
			})
		},
		profilePictureError,
	)
	if err != nil {
		return nil, err
	}
	change := profilePictureChanged{UserID: updated.ID, ActiveFileEntryID: updated.DefaultProfilePictureFileID, Revision: updated.Revision, ChangedAt: updated.ProfilePictureChangedAt.Time}
	if err := s.effects.Changed(ctx, change); err != nil {
		s.effectFailures.Report(ctx, "user_profile_picture_changed", err)
	}
	return updated, nil
}

func (s *profilePictureService) Get(ctx context.Context, invocation Invocation, query GetProfilePictureQuery) (*ProfilePictureContent, error) {
	return s.reads.Get(ctx, invocation, query)
}

func (s *profilePictureReadService) Get(ctx context.Context, invocation Invocation, query GetProfilePictureQuery) (*ProfilePictureContent, error) {
	id := strings.TrimSpace(query.UserID)
	if !model.IsValidId(id) || (query.Size != 128 && query.Size != 256 && query.Size != 512) {
		return nil, NewError("request.invalid")
	}
	if err := s.authorization.AuthorizeRead(ctx, invocation, id); err != nil {
		return nil, err
	}
	user, err := s.users.Get(ctx, id)
	if err != nil {
		return nil, userProfileError(err)
	}
	rendition, err := s.files.GetProfilePictureRendition(ctx, model.UserID(id), fmt.Sprintf("profile_%d", query.Size))
	if err != nil {
		if store.IsNotFound(err) && user.CustomProfilePictureFileID.IsZero() && user.DefaultProfilePictureFileID.IsZero() {
			rendered, renderErr := s.fallback.RenderDefaultProfilePicture(ctx, user.DefaultProfilePictureSeed, query.Size)
			if renderErr != nil {
				return nil, NewError("profile_picture.unavailable").Wrap(renderErr)
			}
			if s.defaultJobs != nil {
				if proposeErr := s.defaultJobs.ProposeDefaultProfilePicture(ctx, user.ID, model.TimeUTC(s.now())); proposeErr != nil {
					s.effectFailures.Report(ctx, "propose_default_profile_picture", proposeErr)
				}
			}
			return &ProfilePictureContent{Body: rendered.Body, MediaType: rendered.MediaType, Size: rendered.Size, ETag: `"` + rendered.SHA256 + `"`}, nil
		}
		return nil, profilePictureError(err)
	}
	body, err := s.content.OpenProfilePictureRendition(ctx, rendition.RevisionID, rendition.ID)
	if err != nil {
		return nil, NewError("profile_picture.unavailable").Wrap(err)
	}
	return &ProfilePictureContent{Body: body, MediaType: rendition.MediaType, Size: rendition.Size, ETag: `"` + rendition.SHA256 + `"`}, nil
}

// EnsureDefaultProfilePicture is the system-owned application use case used by
// the durable handler. It is idempotent across retries and publishes the
// complete default relationship in one named persistence operation.
func (s *profilePictureService) EnsureDefaultProfilePicture(ctx context.Context, userID model.UserID) (model.FileEntryID, error) {
	return s.defaults.Ensure(ctx, userID)
}

func (s *defaultProfilePictureService) Ensure(ctx context.Context, userID model.UserID) (model.FileEntryID, error) {
	user, err := s.users.Get(ctx, userID.String())
	if err != nil {
		if store.IsNotFound(err) {
			return "", errDefaultProfilePictureUserNotFound
		}
		return "", err
	}
	if !user.DefaultProfilePictureFileID.IsZero() {
		return user.DefaultProfilePictureFileID, nil
	}
	at := model.TimeUTC(s.now())
	entry, err := model.NewFileEntryForPurpose(model.NewFileEntryID(), model.FilePurposeProfilePictureDefault, model.FileIndexingNone, at)
	if err != nil {
		return "", fmt.Errorf("%w: %v", errDefaultProfilePictureInvariant, err)
	}
	revision, err := model.NewFileRevision(model.NewFileRevisionID(), entry.ID, model.FileAvailabilityPending, model.FileIndexingNotRequired, at)
	if err != nil {
		return "", fmt.Errorf("%w: %v", errDefaultProfilePictureInvariant, err)
	}
	lease, err := model.NewUploadLease(model.NewUploadLeaseID(), revision.ID, user.ID, at, at.Add(time.Hour))
	if err != nil {
		return "", fmt.Errorf("%w: %v", errDefaultProfilePictureInvariant, err)
	}
	if _, err = s.files.CreateUpload(ctx, &store.FileUploadCreation{Entry: entry, Revision: revision, Lease: lease}); err != nil {
		return "", err
	}
	renditions, err := s.content.GenerateAndStoreDefaultProfilePicture(ctx, revision.ID, user.DefaultProfilePictureSeed, at)
	if err != nil {
		return "", err
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	// Publication is the non-cancelable atomic commit section. A cancellation
	// observed before it prevents entry; once entered, the transaction reaches
	// one authoritative outcome and is never partially interrupted.
	commitCtx, cancelCommit := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancelCommit()
	published, err := s.files.PublishDefaultProfilePicture(commitCtx, &store.DefaultProfilePicturePublication{UserID: user.ID, ExpectedUserRevision: user.Revision, EntryID: entry.ID, RevisionID: revision.ID, LeaseID: lease.ID, Renditions: renditions, AttachedAt: model.TimeUTC(s.now())})
	if err != nil {
		if store.IsConflict(err) {
			current, getErr := s.users.Get(ctx, userID.String())
			if getErr == nil && !current.DefaultProfilePictureFileID.IsZero() {
				return current.DefaultProfilePictureFileID, nil
			}
		}
		return "", err
	}
	return published.User.DefaultProfilePictureFileID, nil
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
