// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestFileStore(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	at := model.NowUTC()
	entry, err := model.NewFileEntry(model.NewFileEntryID(), model.FileIndexingNone, at)
	requireNoError(t, err)
	revision, err := model.NewFileRevision(model.NewFileRevisionID(), entry.ID, model.FileAvailabilityPending, model.FileIndexingNotRequired, at)
	requireNoError(t, err)
	lease, err := model.NewUploadLease(model.NewUploadLeaseID(), revision.ID, user.ID, at, at.Add(time.Hour))
	requireNoError(t, err)

	nonPristineEntry := *entry
	nonPristineEntry.CurrentRevisionID = revision.ID
	if _, err = ss.File().CreateUpload(ctx, &store.FileUploadCreation{Entry: &nonPristineEntry, Revision: revision, Lease: lease}); err == nil {
		t.Fatal("CreateUpload() accepted an entry with a current revision")
	}
	created, err := ss.File().CreateUpload(ctx, &store.FileUploadCreation{Entry: entry, Revision: revision, Lease: lease})
	requireNoError(t, err)
	if created.Entry.ID != entry.ID || created.Lease.FileRevisionID != revision.ID {
		t.Fatalf("CreateUpload() = %#v", created)
	}
	renewedUntil := model.NowUTC().Add(59 * time.Minute)
	renewed, err := ss.File().RenewUploadLease(ctx, lease.ID, user.ID, lease.Revision, renewedUntil)
	requireNoError(t, err)
	if !renewed.ExpiresAt.Equal(renewedUntil) {
		t.Fatalf("RenewUploadLease() = %#v", renewed)
	}
	renditions := make([]model.FileRendition, 0, 3)
	for _, size := range []int{128, 256, 512} {
		rendition, renditionErr := model.NewFileRendition(model.NewFileRenditionID(), revision.ID, fmt.Sprintf("profile_%d", size), "image/webp", 8, size, size, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", at)
		requireNoError(t, renditionErr)
		renditions = append(renditions, *rendition)
	}
	if _, err = ss.File().PublishProfilePicture(ctx, &store.ProfilePicturePublication{
		ActorID: user.ID, UserID: user.ID, ExpectedUserRevision: user.Revision, EntryID: entry.ID,
		RevisionID: revision.ID, LeaseID: lease.ID, Renditions: renditions[:1], ChangedAt: model.NowUTC(),
	}); err == nil {
		t.Fatal("PublishProfilePicture() accepted an incomplete rendition set")
	}
	published, err := ss.File().PublishProfilePicture(ctx, &store.ProfilePicturePublication{
		ActorID: user.ID, UserID: user.ID, ExpectedUserRevision: user.Revision, EntryID: entry.ID,
		RevisionID: revision.ID, LeaseID: lease.ID, Renditions: renditions, ChangedAt: model.NowUTC(),
	})
	requireNoError(t, err)
	if published.User.CustomProfilePictureFileID != entry.ID || published.Revision.Availability != model.FileAvailabilityAvailable {
		t.Fatalf("PublishProfilePicture() = %#v", published)
	}
	got, err := ss.File().GetProfilePictureRendition(ctx, user.ID, "profile_128")
	requireNoError(t, err)
	if got.ID != renditions[0].ID {
		t.Fatalf("GetProfilePictureRendition() = %#v", got)
	}
}
