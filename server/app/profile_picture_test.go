// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"strings"
	"testing"
	"time"

	jobengine "github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type pictureStoreFake struct {
	user                *model.User
	publication         *store.ProfilePicturePublication
	defaultPublication  *store.DefaultProfilePicturePublication
	defaultPublications int
	rendition           *model.FileRendition
	state               *store.ProfilePictureState
	events              *[]string
	created             bool
	lookupName          string
	discarded           bool
	removal             *store.ProfilePictureRemoval
	publishErr          error
	removeErr           error
	stateErr            error
}

func TestNormalizedProfilePictureEqualityRequiresTheCompleteCanonicalManifest(t *testing.T) {
	t.Parallel()

	current := model.FileRendition{Name: "profile_128", MediaType: "image/webp", Size: 42, Width: 128, Height: 128, SHA256: strings.Repeat("a", 64)}
	state := &store.ProfilePictureState{Renditions: []model.FileRendition{current}}
	if !sameNormalizedProfilePicture(state, []model.FileRendition{current}) {
		t.Fatal("identical canonical manifest was not a no-op")
	}
	for _, changed := range []model.FileRendition{
		{Name: current.Name, MediaType: "image/png", Size: current.Size, Width: current.Width, Height: current.Height, SHA256: current.SHA256},
		{Name: current.Name, MediaType: current.MediaType, Size: current.Size + 1, Width: current.Width, Height: current.Height, SHA256: current.SHA256},
		{Name: current.Name, MediaType: current.MediaType, Size: current.Size, Width: current.Width + 1, Height: current.Height, SHA256: current.SHA256},
		{Name: current.Name, MediaType: current.MediaType, Size: current.Size, Width: current.Width, Height: current.Height + 1, SHA256: current.SHA256},
	} {
		if sameNormalizedProfilePicture(state, []model.FileRendition{changed}) {
			t.Fatalf("noncanonical manifest treated as no-op: %#v", changed)
		}
	}
}

func (s *pictureStoreFake) Get(context.Context, string) (*model.User, error) {
	copy := *s.user
	return &copy, nil
}
func (s *pictureStoreFake) CreateUpload(_ context.Context, input *store.FileUploadCreation) (*store.FileUpload, error) {
	s.created = true
	if s.events != nil {
		*s.events = append(*s.events, "create-upload")
	}
	return &store.FileUpload{Entry: input.Entry, Revision: input.Revision, Lease: input.Lease}, nil
}
func (s *pictureStoreFake) CreateRevisionUpload(_ context.Context, input *store.FileRevisionUploadCreation) (*store.FileUpload, error) {
	s.created = true
	if s.events != nil {
		*s.events = append(*s.events, "create-revision-upload")
	}
	return &store.FileUpload{Revision: input.Revision, Lease: input.Lease}, nil
}
func (s *pictureStoreFake) PublishProfilePicture(_ context.Context, input *store.ProfilePicturePublication) (*store.ProfilePicturePublicationResult, error) {
	s.publication = input
	if s.events != nil {
		*s.events = append(*s.events, "publish")
	}
	if s.publishErr != nil {
		return nil, s.publishErr
	}
	copy := *s.user
	copy.CustomProfilePictureFileID = input.EntryID
	copy.ProfilePictureChangedAt = model.OptionalTimeFrom(input.ChangedAt)
	copy.Revision++
	return &store.ProfilePicturePublicationResult{User: &copy, Revision: &model.FileRevision{ID: input.RevisionID, Availability: model.FileAvailabilityAvailable, Renditions: input.Renditions}}, nil
}
func (s *pictureStoreFake) PublishDefaultProfilePicture(_ context.Context, input *store.DefaultProfilePicturePublication) (*store.ProfilePicturePublicationResult, error) {
	s.defaultPublication = input
	s.defaultPublications++
	copy := *s.user
	copy.DefaultProfilePictureFileID = input.EntryID
	copy.Revision++
	return &store.ProfilePicturePublicationResult{User: &copy, Revision: &model.FileRevision{ID: input.RevisionID, Availability: model.FileAvailabilityAvailable, Renditions: input.Renditions}}, nil
}
func (s *pictureStoreFake) GetProfilePictureRendition(_ context.Context, _ model.UserID, name string) (*model.FileRendition, error) {
	s.lookupName = name
	if s.events != nil {
		*s.events = append(*s.events, "get-rendition")
	}
	if s.rendition == nil {
		return nil, store.NewErrNotFound("profile_picture", s.user.ID.String())
	}
	return s.rendition, nil
}
func (s *pictureStoreFake) GetProfilePictureState(context.Context, model.UserID) (*store.ProfilePictureState, error) {
	if s.stateErr != nil {
		return nil, s.stateErr
	}
	if s.state == nil {
		return nil, store.NewErrNotFound("profile_picture", s.user.ID.String())
	}
	return s.state, nil
}
func (s *pictureStoreFake) DiscardProfilePictureUpload(context.Context, *store.ProfilePictureUploadDiscard) error {
	s.discarded = true
	if s.events != nil {
		*s.events = append(*s.events, "discard-upload")
	}
	return nil
}
func (s *pictureStoreFake) RemoveProfilePictureWithAudit(_ context.Context, input *store.ProfilePictureRemoval) (*model.User, error) {
	s.removal = input
	if s.events != nil {
		*s.events = append(*s.events, "remove-with-audit")
	}
	if s.removeErr != nil {
		return nil, s.removeErr
	}
	updated := *s.user
	updated.CustomProfilePictureFileID = ""
	updated.ProfilePictureChangedAt = model.OptionalTimeFrom(input.ChangedAt)
	updated.Revision++
	return &updated, nil
}

type pictureContentFake struct {
	normalized      bool
	events          *[]string
	openedRevision  model.FileRevisionID
	openedRendition model.FileRenditionID
	removed         bool
}

type profilePictureAuditorFake struct {
	events    *[]string
	beginID   string
	failCode  string
	beginErr  error
	action    model.Action
	resource  model.Resource
	operation string
	value     map[string]any
	prior     map[string]any
}

func (a *profilePictureAuditorFake) Begin(_ context.Context, _ Invocation, action model.Action, resource model.Resource, operation string, value map[string]any, prior map[string]any) (string, error) {
	a.action, a.resource, a.operation, a.value, a.prior = action, resource, operation, value, prior
	if a.events != nil {
		*a.events = append(*a.events, "audit-begin")
	}
	return a.beginID, a.beginErr
}
func (a *profilePictureAuditorFake) Fail(_ context.Context, _ string, code string) error {
	a.failCode = code
	if a.events != nil {
		*a.events = append(*a.events, "audit-fail")
	}
	return nil
}

type profilePictureEffectsFake struct {
	events *[]string
	change profilePictureChanged
	err    error
}

func (e *profilePictureEffectsFake) Changed(_ context.Context, change profilePictureChanged) error {
	e.change = change
	if e.events != nil {
		*e.events = append(*e.events, "effect")
	}
	return e.err
}

type profilePictureEffectFailuresFake struct{ events *[]string }

func (r *profilePictureEffectFailuresFake) Report(context.Context, string, error) {
	if r.events != nil {
		*r.events = append(*r.events, "effect-report")
	}
}

type profilePictureDefaultJobsFake struct {
	userID model.UserID
	count  int
	err    error
}

func (p *profilePictureDefaultJobsFake) ProposeDefaultProfilePicture(_ context.Context, userID model.UserID, _ time.Time) error {
	p.userID = userID
	p.count++
	return p.err
}

func (s *pictureContentFake) NormalizeAndStoreProfilePicture(_ context.Context, revisionID model.FileRevisionID, _ io.Reader, _ int64, at time.Time) ([]model.FileRendition, error) {
	s.normalized = true
	if s.events != nil {
		*s.events = append(*s.events, "normalize")
	}
	result := make([]model.FileRendition, 0, 3)
	for _, size := range []int{128, 256, 512} {
		rendition, err := model.NewFileRendition(model.NewFileRenditionID(), revisionID, fmt.Sprintf("profile_%d", size), "image/webp", 10, 20, 20, strings.Repeat("a", 64), at)
		if err != nil {
			return nil, err
		}
		result = append(result, *rendition)
	}
	return result, nil
}
func (s *pictureContentFake) GenerateAndStoreDefaultProfilePicture(_ context.Context, revisionID model.FileRevisionID, _ string, at time.Time) ([]model.FileRendition, error) {
	result := make([]model.FileRendition, 0, 3)
	for _, size := range []int{128, 256, 512} {
		rendition, err := model.NewFileRendition(model.NewFileRenditionID(), revisionID, fmt.Sprintf("profile_%d", size), "image/webp", 10, size, size, strings.Repeat("d", 64), at)
		if err != nil {
			return nil, err
		}
		result = append(result, *rendition)
	}
	return result, nil
}
func (s *pictureContentFake) RenderDefaultProfilePicture(context.Context, string, int) (*RenderedProfilePicture, error) {
	return &RenderedProfilePicture{Body: io.NopCloser(strings.NewReader("default")), MediaType: "image/webp", Size: 7, SHA256: strings.Repeat("d", 64)}, nil
}
func (s *pictureContentFake) OpenProfilePictureRendition(_ context.Context, revisionID model.FileRevisionID, renditionID model.FileRenditionID) (io.ReadCloser, error) {
	s.openedRevision = revisionID
	s.openedRendition = renditionID
	if s.events != nil {
		*s.events = append(*s.events, "open")
	}
	return io.NopCloser(strings.NewReader("webp")), nil
}
func (s *pictureContentFake) RemoveProfilePictureRenditions(context.Context, model.FileRevisionID, []model.FileRendition) error {
	s.removed = true
	if s.events != nil {
		*s.events = append(*s.events, "remove-staged")
	}
	return nil
}

func (s *pictureContentFake) RemoveFileRevisionContent(context.Context, model.FileRevisionID, []model.FileRenditionID) error {
	return nil
}

func TestUploadProfilePictureNormalizesPrivateRenditionsBeforePublishing(t *testing.T) {
	at := time.Date(2026, time.August, 9, 12, 0, 0, 0, time.UTC)
	user := &model.User{Username: "student", Email: "student@example.test"}
	user.PrepareCreate(model.NewUserID(), at.Add(-time.Hour))
	events := []string{}
	persistence := &pictureStoreFake{user: user, events: &events}
	content := &pictureContentFake{events: &events}
	nowCalls := 0
	service := newProfilePictureService(persistence, persistence, content, &userProfileAuthorizerFake{events: &events}, &profilePictureAuditorFake{events: &events, beginID: model.NewId()}, &profilePictureEffectsFake{events: &events}, &profilePictureEffectFailuresFake{events: &events}, nil, func() time.Time {
		nowCalls++
		return at.Add(time.Duration(nowCalls-1) * time.Minute)
	})

	source := image.NewNRGBA(image.Rect(0, 0, 40, 20))
	for y := 0; y < 20; y++ {
		for x := 0; x < 40; x++ {
			source.Set(x, y, color.NRGBA{R: 200, G: 10, B: 20, A: 255})
		}
	}
	var input bytes.Buffer
	if err := png.Encode(&input, source); err != nil {
		t.Fatal(err)
	}
	updated, err := service.Upload(context.Background(), NewInvocation(model.Principal{UserID: user.ID}, model.RequestMetadata{}), UploadProfilePictureCommand{UserID: user.ID.String(), ExpectedRevision: user.Revision, Body: bytes.NewReader(input.Bytes()), Size: int64(input.Len())})
	if err != nil {
		t.Fatalf("Upload() error = %v", err)
	}
	if updated.CustomProfilePictureFileID.IsZero() || len(persistence.publication.Renditions) != 3 || !content.normalized {
		t.Fatalf("upload result: user=%#v publication=%#v normalized=%v", updated, persistence.publication, content.normalized)
	}
	if !updated.ProfilePictureChangedAt.Time.Equal(at.Add(time.Minute)) {
		t.Fatalf("picture changed at = %v", updated.ProfilePictureChangedAt.Time)
	}
	wantEvents := []string{"authorize-profile-picture-write", "create-upload", "normalize", "audit-begin", "publish", "effect"}
	if fmt.Sprint(events) != fmt.Sprint(wantEvents) {
		t.Fatalf("events = %v, want %v", events, wantEvents)
	}
	for _, rendition := range persistence.publication.Renditions {
		if rendition.Width != 20 || rendition.Height != 20 {
			t.Fatalf("%s dimensions = %dx%d; small images must not be upscaled", rendition.Name, rendition.Width, rendition.Height)
		}
	}
}

func TestReplaceProfilePictureKeepsTheActiveFileEntry(t *testing.T) {
	at := time.Date(2026, time.August, 9, 12, 0, 0, 0, time.UTC)
	user := &model.User{Username: "student", Email: "student@example.test"}
	user.PrepareCreate(model.NewUserID(), at.Add(-time.Hour))
	user.CustomProfilePictureFileID = model.NewFileEntryID()
	events := []string{}
	persistence := &pictureStoreFake{user: user, state: profilePictureState(user.CustomProfilePictureFileID, strings.Repeat("b", 64)), events: &events}
	content := &pictureContentFake{events: &events}
	auditor := &profilePictureAuditorFake{events: &events, beginID: model.NewId()}
	effects := &profilePictureEffectsFake{events: &events}
	service := newProfilePictureService(persistence, persistence, content, &userProfileAuthorizerFake{events: &events}, auditor, effects, &profilePictureEffectFailuresFake{events: &events}, nil, func() time.Time { return at })

	updated, err := service.Upload(context.Background(), NewInvocation(model.Principal{UserID: user.ID}, model.RequestMetadata{}), UploadProfilePictureCommand{UserID: user.ID.String(), ExpectedRevision: user.Revision, ExpectedSHA256: strings.Repeat("b", 64), Body: strings.NewReader("image"), Size: 5})
	if err != nil {
		t.Fatalf("Upload() replacement error = %v", err)
	}
	if updated.CustomProfilePictureFileID != user.CustomProfilePictureFileID || persistence.publication.EntryID != user.CustomProfilePictureFileID {
		t.Fatalf("replacement changed file entry: user=%s publication=%s", updated.CustomProfilePictureFileID, persistence.publication.EntryID)
	}
	wantEvents := []string{"authorize-profile-picture-write", "create-revision-upload", "normalize", "audit-begin", "publish", "effect"}
	if fmt.Sprint(events) != fmt.Sprint(wantEvents) || effects.change.UserID != user.ID || effects.change.ActiveFileEntryID != user.CustomProfilePictureFileID {
		t.Fatalf("replacement events/change = %v / %#v", events, effects.change)
	}
	auditData, err := json.Marshal([]map[string]any{auditor.value, auditor.prior})
	if err != nil {
		t.Fatal(err)
	}
	if auditor.action != model.ActionUserProfilePictureManage || auditor.resource.Type != model.ResourceUser || auditor.resource.ID != user.ID.String() || strings.Contains(string(auditData), strings.Repeat("b", 64)) || strings.Contains(string(auditData), "storage") {
		t.Fatalf("unsafe or incorrect audit projection: action=%s resource=%#v data=%s", auditor.action, auditor.resource, auditData)
	}
}

func TestReplaceProfilePictureRequiresCurrentETag(t *testing.T) {
	user := &model.User{Username: "student", Email: "student@example.test"}
	user.PrepareCreate(model.NewUserID(), time.Now())
	user.CustomProfilePictureFileID = model.NewFileEntryID()
	persistence := &pictureStoreFake{user: user, state: profilePictureState(user.DefaultProfilePictureFileID, strings.Repeat("d", 64))}
	events := []string{}
	service := newProfilePictureService(persistence, persistence, &pictureContentFake{}, &userProfileAuthorizerFake{events: &events}, &profilePictureAuditorFake{}, &profilePictureEffectsFake{}, &profilePictureEffectFailuresFake{}, nil, time.Now)

	_, err := service.Upload(context.Background(), NewInvocation(model.Principal{UserID: user.ID}, model.RequestMetadata{}), UploadProfilePictureCommand{UserID: user.ID.String(), Body: strings.NewReader("image"), Size: 5})
	if !Is(err, "request.invalid") {
		t.Fatalf("Upload() error = %v, want request.invalid", err)
	}
	if persistence.created {
		t.Fatal("replacement created durable upload state without If-Match")
	}
}

func TestReplaceProfilePictureRejectsAStaleETagBeforeCreatingUploadState(t *testing.T) {
	user := &model.User{Username: "student", Email: "student@example.test"}
	user.PrepareCreate(model.NewUserID(), time.Now())
	user.CustomProfilePictureFileID = model.NewFileEntryID()
	persistence := &pictureStoreFake{user: user, state: profilePictureState(user.CustomProfilePictureFileID, strings.Repeat("a", 64))}
	events := []string{}
	service := newProfilePictureService(persistence, persistence, &pictureContentFake{}, &userProfileAuthorizerFake{events: &events}, &profilePictureAuditorFake{}, &profilePictureEffectsFake{}, &profilePictureEffectFailuresFake{}, nil, time.Now)

	_, err := service.Upload(context.Background(), NewInvocation(model.Principal{UserID: user.ID}, model.RequestMetadata{}), UploadProfilePictureCommand{UserID: user.ID.String(), ExpectedSHA256: strings.Repeat("b", 64), Body: strings.NewReader("image"), Size: 5})
	if !Is(err, "user.conflict") {
		t.Fatalf("Upload() error = %v, want user.conflict", err)
	}
	if persistence.created {
		t.Fatal("stale replacement created durable upload state")
	}
}

func TestReplaceProfilePictureWithIdenticalNormalizedContentIsANoOp(t *testing.T) {
	at := time.Now()
	user := &model.User{Username: "student", Email: "student@example.test"}
	user.PrepareCreate(model.NewUserID(), at.Add(-time.Hour))
	user.CustomProfilePictureFileID = model.NewFileEntryID()
	events := []string{}
	state := profilePictureState(user.CustomProfilePictureFileID, strings.Repeat("a", 64))
	persistence := &pictureStoreFake{user: user, state: state, events: &events}
	content := &pictureContentFake{events: &events}
	effects := &profilePictureEffectsFake{events: &events}
	service := newProfilePictureService(persistence, persistence, content, &userProfileAuthorizerFake{events: &events}, &profilePictureAuditorFake{events: &events, beginID: model.NewId()}, effects, &profilePictureEffectFailuresFake{events: &events}, nil, func() time.Time { return at })

	result, err := service.Upload(context.Background(), NewInvocation(model.Principal{UserID: user.ID}, model.RequestMetadata{}), UploadProfilePictureCommand{UserID: user.ID.String(), ExpectedSHA256: strings.Repeat("a", 64), Body: strings.NewReader("image"), Size: 5})
	if err != nil {
		t.Fatal(err)
	}
	if result.ID != user.ID || result.Revision != user.Revision || !persistence.discarded || !content.removed || persistence.publication != nil || !effects.change.UserID.IsZero() {
		t.Fatalf("identical replacement changed state: result=%#v store=%#v effects=%#v", result, persistence, effects.change)
	}
	want := []string{"authorize-profile-picture-write", "create-revision-upload", "normalize", "remove-staged", "discard-upload"}
	if fmt.Sprint(events) != fmt.Sprint(want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestUploadIdenticalToGeneratedDefaultIsANoOp(t *testing.T) {
	at := time.Now()
	user := &model.User{Username: "student", Email: "student@example.test"}
	user.PrepareCreate(model.NewUserID(), at.Add(-time.Hour))
	user.DefaultProfilePictureFileID = model.NewFileEntryID()
	events := []string{}
	persistence := &pictureStoreFake{user: user, state: profilePictureState(user.DefaultProfilePictureFileID, strings.Repeat("a", 64)), events: &events}
	content := &pictureContentFake{events: &events}
	effects := &profilePictureEffectsFake{events: &events}
	service := newProfilePictureService(persistence, persistence, content, &userProfileAuthorizerFake{events: &events}, &profilePictureAuditorFake{events: &events, beginID: model.NewId()}, effects, &profilePictureEffectFailuresFake{events: &events}, nil, func() time.Time { return at })

	result, err := service.Upload(context.Background(), NewInvocation(model.Principal{UserID: user.ID}, model.RequestMetadata{}), UploadProfilePictureCommand{UserID: user.ID.String(), ExpectedSHA256: strings.Repeat("a", 64), Body: strings.NewReader("image"), Size: 5})
	if err != nil {
		t.Fatal(err)
	}
	if result.Revision != user.Revision || !persistence.discarded || persistence.publication != nil || !effects.change.UserID.IsZero() {
		t.Fatalf("default-identical upload changed visible state: result=%#v store=%#v effect=%#v", result, persistence, effects.change)
	}
	want := []string{"authorize-profile-picture-write", "create-upload", "normalize", "remove-staged", "discard-upload"}
	if fmt.Sprint(events) != fmt.Sprint(want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestRemoveProfilePictureArchivesCustomAndPublishesDefaultAfterCommit(t *testing.T) {
	at := time.Date(2026, time.August, 9, 15, 0, 0, 0, time.UTC)
	user := &model.User{Username: "student", Email: "student@example.test"}
	user.PrepareCreate(model.NewUserID(), at.Add(-time.Hour))
	user.CustomProfilePictureFileID = model.NewFileEntryID()
	user.DefaultProfilePictureFileID = model.NewFileEntryID()
	events := []string{}
	state := profilePictureState(user.CustomProfilePictureFileID, strings.Repeat("a", 64))
	persistence := &pictureStoreFake{user: user, state: state, events: &events}
	effects := &profilePictureEffectsFake{events: &events}
	service := newProfilePictureService(persistence, persistence, &pictureContentFake{}, &userProfileAuthorizerFake{events: &events}, &profilePictureAuditorFake{events: &events, beginID: model.NewId()}, effects, &profilePictureEffectFailuresFake{events: &events}, nil, func() time.Time { return at })

	updated, err := service.Remove(context.Background(), NewInvocation(model.Principal{UserID: user.ID}, model.RequestMetadata{}), RemoveProfilePictureCommand{UserID: user.ID.String(), ExpectedSHA256: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	if !updated.CustomProfilePictureFileID.IsZero() || updated.DefaultProfilePictureFileID != user.DefaultProfilePictureFileID || persistence.removal.EntryID != user.CustomProfilePictureFileID || effects.change.ActiveFileEntryID != user.DefaultProfilePictureFileID {
		t.Fatalf("removal result/input/effect = %#v / %#v / %#v", updated, persistence.removal, effects.change)
	}
	want := []string{"authorize-profile-picture-write", "audit-begin", "remove-with-audit", "effect"}
	if fmt.Sprint(events) != fmt.Sprint(want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestUploadAfterRemovalCreatesANewCustomFileEntry(t *testing.T) {
	user := &model.User{Username: "student", Email: "student@example.test"}
	user.PrepareCreate(model.NewUserID(), time.Now())
	user.DefaultProfilePictureFileID = model.NewFileEntryID()
	events := []string{}
	persistence := &pictureStoreFake{user: user, state: profilePictureState(user.DefaultProfilePictureFileID, strings.Repeat("d", 64))}
	service := newProfilePictureService(persistence, persistence, &pictureContentFake{}, &userProfileAuthorizerFake{events: &events}, &profilePictureAuditorFake{beginID: model.NewId()}, &profilePictureEffectsFake{}, &profilePictureEffectFailuresFake{}, nil, time.Now)

	updated, err := service.Upload(context.Background(), NewInvocation(model.Principal{UserID: user.ID}, model.RequestMetadata{}), UploadProfilePictureCommand{UserID: user.ID.String(), ExpectedSHA256: strings.Repeat("d", 64), Body: strings.NewReader("image"), Size: 5})
	if err != nil {
		t.Fatal(err)
	}
	if updated.CustomProfilePictureFileID.IsZero() || updated.CustomProfilePictureFileID == user.DefaultProfilePictureFileID || persistence.publication.EntryID == user.DefaultProfilePictureFileID {
		t.Fatalf("upload reused the retained default entry: %#v / %#v", updated, persistence.publication)
	}
}

func TestProfilePictureAuditFailurePreventsVisibleMutationAndEvent(t *testing.T) {
	user := &model.User{Username: "student", Email: "student@example.test"}
	user.PrepareCreate(model.NewUserID(), time.Now())
	events := []string{}
	persistence := &pictureStoreFake{user: user, events: &events}
	effects := &profilePictureEffectsFake{events: &events}
	auditor := &profilePictureAuditorFake{events: &events, beginErr: NewError("audit.unavailable")}
	service := newProfilePictureService(persistence, persistence, &pictureContentFake{events: &events}, &userProfileAuthorizerFake{events: &events}, auditor, effects, &profilePictureEffectFailuresFake{events: &events}, nil, time.Now)

	_, err := service.Upload(context.Background(), NewInvocation(model.Principal{UserID: user.ID}, model.RequestMetadata{}), UploadProfilePictureCommand{UserID: user.ID.String(), Body: strings.NewReader("image"), Size: 5})
	if !Is(err, "audit.unavailable") || persistence.publication != nil || !effects.change.UserID.IsZero() {
		t.Fatalf("audit failure result: err=%v publication=%#v effect=%#v", err, persistence.publication, effects.change)
	}
	want := []string{"authorize-profile-picture-write", "create-upload", "normalize", "audit-begin"}
	if fmt.Sprint(events) != fmt.Sprint(want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestProfilePictureCommitFailureFailsAuditAndPublishesNoEvent(t *testing.T) {
	user := &model.User{Username: "student", Email: "student@example.test"}
	user.PrepareCreate(model.NewUserID(), time.Now())
	events := []string{}
	persistence := &pictureStoreFake{user: user, events: &events, publishErr: store.NewErrConflict("profile_picture", "changed", nil)}
	auditor := &profilePictureAuditorFake{events: &events, beginID: model.NewId()}
	effects := &profilePictureEffectsFake{events: &events}
	service := newProfilePictureService(persistence, persistence, &pictureContentFake{events: &events}, &userProfileAuthorizerFake{events: &events}, auditor, effects, &profilePictureEffectFailuresFake{events: &events}, nil, time.Now)

	_, err := service.Upload(context.Background(), NewInvocation(model.Principal{UserID: user.ID}, model.RequestMetadata{}), UploadProfilePictureCommand{UserID: user.ID.String(), Body: strings.NewReader("image"), Size: 5})
	if !Is(err, "user.conflict") || auditor.failCode != "user.conflict" || !effects.change.UserID.IsZero() {
		t.Fatalf("commit failure result: err=%v audit=%#v effect=%#v", err, auditor, effects.change)
	}
	want := []string{"authorize-profile-picture-write", "create-upload", "normalize", "audit-begin", "publish", "audit-fail"}
	if fmt.Sprint(events) != fmt.Sprint(want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestProfilePictureRealtimeEffectContainsOnlySafeChangeMetadata(t *testing.T) {
	sink := &recordingRealtimeSink{}
	cluster := &recordingRealtimeCluster{}
	realtime := newRealtimeService(nil, nil)
	if err := realtime.SetSink(sink); err != nil {
		t.Fatal(err)
	}
	if err := realtime.SetClusterFanout(cluster); err != nil {
		t.Fatal(err)
	}
	change := profilePictureChanged{UserID: model.NewUserID(), ActiveFileEntryID: model.NewFileEntryID(), Revision: 4, ChangedAt: time.UnixMilli(500)}
	if err := (profilePictureRealtimeEffects{realtime: realtime}).Changed(context.Background(), change); err != nil {
		t.Fatal(err)
	}
	if len(sink.events) != 1 || sink.events[0].Name != "user_profile_picture_changed" || sink.events[0].UserID != change.UserID.String() {
		t.Fatalf("events = %#v", sink.events)
	}
	var data map[string]any
	if err := json.Unmarshal(sink.events[0].Data, &data); err != nil {
		t.Fatal(err)
	}
	if len(data) != 3 || data["active_file_entry_id"] != change.ActiveFileEntryID.String() || data["revision"] != float64(4) || data["changed_at"] != float64(500) {
		t.Fatalf("event data = %#v", data)
	}
}

func profilePictureState(entryID model.FileEntryID, checksum string) *store.ProfilePictureState {
	revisionID := model.NewFileRevisionID()
	renditions := make([]model.FileRendition, 0, 3)
	for _, size := range []int{128, 256, 512} {
		renditions = append(renditions, model.FileRendition{RevisionID: revisionID, Name: fmt.Sprintf("profile_%d", size), MediaType: "image/webp", Size: 10, Width: 20, Height: 20, SHA256: checksum})
	}
	return &store.ProfilePictureState{EntryID: entryID, RevisionID: revisionID, Renditions: renditions}
}

func TestUploadProfilePictureAuthorizationDenialHasNoSideEffects(t *testing.T) {
	at := time.Now()
	user := &model.User{Username: "student", Email: "student@example.test"}
	user.PrepareCreate(model.NewUserID(), at)
	events := []string{}
	persistence := &pictureStoreFake{user: user, events: &events}
	content := &pictureContentFake{events: &events}
	service := newProfilePictureService(persistence, persistence, content, &userProfileAuthorizerFake{events: &events, writeErr: NewError("authorization.denied")}, &profilePictureAuditorFake{}, &profilePictureEffectsFake{}, &profilePictureEffectFailuresFake{}, nil, func() time.Time { return at })

	_, err := service.Upload(context.Background(), NewInvocation(model.Principal{UserID: user.ID}, model.RequestMetadata{}), UploadProfilePictureCommand{UserID: user.ID.String(), Body: strings.NewReader("image"), Size: 5})
	if !Is(err, "authorization.denied") {
		t.Fatalf("Upload() error = %v", err)
	}
	if persistence.created || content.normalized || fmt.Sprint(events) != fmt.Sprint([]string{"authorize-profile-picture-write"}) {
		t.Fatalf("denied upload side effects: created=%v normalized=%v events=%v", persistence.created, content.normalized, events)
	}
}

func TestGetProfilePictureAuthorizesAndMapsRendition(t *testing.T) {
	at := time.Now()
	userID := model.NewUserID()
	user := &model.User{Username: "student", Email: "student@example.test"}
	user.PrepareCreate(userID, at)
	revisionID := model.NewFileRevisionID()
	rendition, err := model.NewFileRendition(model.NewFileRenditionID(), revisionID, "profile_256", "image/webp", 4, 256, 256, strings.Repeat("a", 64), at)
	if err != nil {
		t.Fatal(err)
	}
	events := []string{}
	persistence := &pictureStoreFake{user: user, rendition: rendition, events: &events}
	contentAdapter := &pictureContentFake{events: &events}
	service := newProfilePictureService(persistence, persistence, contentAdapter, &userProfileAuthorizerFake{events: &events}, &profilePictureAuditorFake{}, &profilePictureEffectsFake{}, &profilePictureEffectFailuresFake{}, nil, func() time.Time { return at })

	content, err := service.Get(context.Background(), NewInvocation(model.Principal{UserID: userID}, model.RequestMetadata{}), GetProfilePictureQuery{UserID: userID.String(), Size: 256})
	if err != nil {
		t.Fatal(err)
	}
	defer content.Body.Close()
	wantEvents := []string{"authorize-read", "get-rendition", "open"}
	if persistence.lookupName != "profile_256" || contentAdapter.openedRevision != revisionID || contentAdapter.openedRendition != rendition.ID || content.ETag != `"`+rendition.SHA256+`"` || content.MediaType != "image/webp" || content.Size != 4 || fmt.Sprint(events) != fmt.Sprint(wantEvents) {
		t.Fatalf("Get() mapping: content=%#v lookup=%q events=%v", content, persistence.lookupName, events)
	}
}

func TestGetMissingDefaultRendersImmediatelyAndProposesDurableGeneration(t *testing.T) {
	at := time.Now()
	user := &model.User{Username: "student", Email: "student@example.test"}
	user.PrepareCreate(model.NewUserID(), at)
	events := []string{}
	persistence := &pictureStoreFake{user: user, events: &events}
	jobs := &profilePictureDefaultJobsFake{}
	service := newProfilePictureService(persistence, persistence, &pictureContentFake{}, &userProfileAuthorizerFake{events: &events}, &profilePictureAuditorFake{}, &profilePictureEffectsFake{}, &profilePictureEffectFailuresFake{}, jobs, func() time.Time { return at })

	content, err := service.Get(context.Background(), NewInvocation(model.Principal{UserID: user.ID}, model.RequestMetadata{}), GetProfilePictureQuery{UserID: user.ID.String(), Size: 256})
	if err != nil {
		t.Fatal(err)
	}
	defer content.Body.Close()
	if content.ETag != `"`+strings.Repeat("d", 64)+`"` || content.Size != 7 || jobs.count != 1 || jobs.userID != user.ID || user.ProfilePictureChangedAt.Valid {
		t.Fatalf("fallback = %#v jobs=%#v user=%#v", content, jobs, user)
	}
}

func TestDefaultProfilePictureHandlerAttachesGeneratedRenditionsIdempotently(t *testing.T) {
	at := time.Now()
	user := &model.User{Username: "student", Email: "student@example.test"}
	user.PrepareCreate(model.NewUserID(), at.Add(-time.Hour))
	persistence := &pictureStoreFake{user: user}
	command, err := model.EncodeDefaultProfilePictureCommand(model.DefaultProfilePictureCommandV1{UserID: user.ID})
	if err != nil {
		t.Fatal(err)
	}
	job, err := model.NewJob(model.NewJobID(), model.JobTypeProfilePictureGenerateDefault, 1, command, user.ID.String(), at, at, 8)
	if err != nil {
		t.Fatal(err)
	}
	generator := newProfilePictureService(persistence, persistence, &pictureContentFake{}, nil, nil, nil, nil, nil, func() time.Time { return at })
	handler := defaultProfilePictureHandler{generator: generator}
	outcome := handler.Run(context.Background(), jobengine.Execution{Job: job})
	if outcome.Kind != jobengine.OutcomeSucceeded || outcome.Err != nil || persistence.defaultPublication == nil || len(persistence.defaultPublication.Renditions) != 3 || persistence.defaultPublication.UserID != user.ID {
		t.Fatalf("handler outcome/store = %#v / %#v", outcome, persistence)
	}
	if !persistence.defaultPublication.AttachedAt.Equal(at) || user.ProfilePictureChangedAt.Valid {
		t.Fatalf("default attachment changed visible timestamp: %#v", persistence.defaultPublication)
	}
	attached := *user
	attached.DefaultProfilePictureFileID = persistence.defaultPublication.EntryID
	persistence.user = &attached
	second := handler.Run(context.Background(), jobengine.Execution{Job: job})
	if second.Kind != jobengine.OutcomeSucceeded || persistence.defaultPublication.EntryID != attached.DefaultProfilePictureFileID || persistence.defaultPublications != 1 {
		t.Fatalf("idempotent outcome = %#v", second)
	}
}
