// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type pictureStoreFake struct {
	user        *model.User
	publication *store.ProfilePicturePublication
	rendition   *model.FileRendition
	events      *[]string
	created     bool
	lookupName  string
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
func (s *pictureStoreFake) PublishProfilePicture(_ context.Context, input *store.ProfilePicturePublication) (*store.ProfilePicturePublicationResult, error) {
	s.publication = input
	if s.events != nil {
		*s.events = append(*s.events, "publish")
	}
	copy := *s.user
	copy.CustomProfilePictureFileID = input.EntryID
	copy.ProfilePictureChangedAt = model.OptionalTimeFrom(input.ChangedAt)
	copy.Revision++
	return &store.ProfilePicturePublicationResult{User: &copy, Revision: &model.FileRevision{ID: input.RevisionID, Availability: model.FileAvailabilityAvailable, Renditions: input.Renditions}}, nil
}
func (s *pictureStoreFake) GetProfilePictureRendition(_ context.Context, _ model.UserID, name string) (*model.FileRendition, error) {
	s.lookupName = name
	if s.events != nil {
		*s.events = append(*s.events, "get-rendition")
	}
	return s.rendition, nil
}

type pictureContentFake struct {
	normalized      bool
	events          *[]string
	openedRevision  model.FileRevisionID
	openedRendition model.FileRenditionID
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
func (s *pictureContentFake) OpenProfilePictureRendition(_ context.Context, revisionID model.FileRevisionID, renditionID model.FileRenditionID) (io.ReadCloser, error) {
	s.openedRevision = revisionID
	s.openedRendition = renditionID
	if s.events != nil {
		*s.events = append(*s.events, "open")
	}
	return io.NopCloser(strings.NewReader("webp")), nil
}
func (s *pictureContentFake) RemoveProfilePictureRenditions(context.Context, model.FileRevisionID, []model.FileRendition) error {
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
	service := newProfilePictureService(persistence, persistence, content, &userProfileAuthorizerFake{events: &events}, func() time.Time {
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
	wantEvents := []string{"authorize-profile-picture-write", "create-upload", "normalize", "publish"}
	if fmt.Sprint(events) != fmt.Sprint(wantEvents) {
		t.Fatalf("events = %v, want %v", events, wantEvents)
	}
	for _, rendition := range persistence.publication.Renditions {
		if rendition.Width != 20 || rendition.Height != 20 {
			t.Fatalf("%s dimensions = %dx%d; small images must not be upscaled", rendition.Name, rendition.Width, rendition.Height)
		}
	}
}

func TestUploadProfilePictureAuthorizationDenialHasNoSideEffects(t *testing.T) {
	at := time.Now()
	user := &model.User{Username: "student", Email: "student@example.test"}
	user.PrepareCreate(model.NewUserID(), at)
	events := []string{}
	persistence := &pictureStoreFake{user: user, events: &events}
	content := &pictureContentFake{events: &events}
	service := newProfilePictureService(persistence, persistence, content, &userProfileAuthorizerFake{events: &events, writeErr: NewError("authorization.denied")}, func() time.Time { return at })

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
	revisionID := model.NewFileRevisionID()
	rendition, err := model.NewFileRendition(model.NewFileRenditionID(), revisionID, "profile_256", "image/webp", 4, 256, 256, strings.Repeat("a", 64), at)
	if err != nil {
		t.Fatal(err)
	}
	events := []string{}
	persistence := &pictureStoreFake{rendition: rendition, events: &events}
	contentAdapter := &pictureContentFake{events: &events}
	service := newProfilePictureService(persistence, persistence, contentAdapter, &userProfileAuthorizerFake{events: &events}, func() time.Time { return at })

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
