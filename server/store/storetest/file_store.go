// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"context"
	"errors"
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
	renewedUntil := model.NowUTC().Add(30 * time.Minute)
	renewed, err := ss.File().RenewUploadLease(ctx, lease.ID, user.ID, lease.Revision, 1, renewedUntil)
	if err != nil {
		t.Fatalf("RenewUploadLease() error = %v (cause: %v)", err, errors.Unwrap(err))
	}
	if !renewed.ExpiresAt.Equal(renewedUntil) {
		t.Fatalf("RenewUploadLease() = %#v", renewed)
	}
	if _, err = ss.File().RenewUploadLease(ctx, lease.ID, user.ID, renewed.Revision, 1, renewedUntil); !store.IsConflict(err) {
		t.Fatalf("RenewUploadLease(without progress) error = %v", err)
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
	changeAt := model.NowUTC()
	auditAttempt := saveUserProfileAuditAttempt(t, ctx, ss, user.ID.String())
	published, err := ss.File().PublishProfilePicture(ctx, &store.ProfilePicturePublication{
		ActorID: user.ID, UserID: user.ID, ExpectedUserRevision: user.Revision, EntryID: entry.ID,
		RevisionID: revision.ID, LeaseID: lease.ID, Renditions: renditions, ChangedAt: changeAt,
		AuditEventID: auditAttempt.ID.String(), AuditAt: changeAt.UnixMilli(),
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

	replacementAt := model.NowUTC()
	replacementRevision, err := model.NewFileRevision(model.NewFileRevisionID(), entry.ID, model.FileAvailabilityPending, model.FileIndexingNotRequired, replacementAt)
	requireNoError(t, err)
	replacementLease, err := model.NewUploadLease(model.NewUploadLeaseID(), replacementRevision.ID, user.ID, replacementAt, replacementAt.Add(time.Hour))
	requireNoError(t, err)
	_, err = ss.File().CreateRevisionUpload(ctx, &store.FileRevisionUploadCreation{EntryID: entry.ID, Revision: replacementRevision, Lease: replacementLease})
	requireNoError(t, err)
	replacementRenditions := make([]model.FileRendition, 0, 3)
	for _, size := range []int{128, 256, 512} {
		rendition, renditionErr := model.NewFileRendition(model.NewFileRenditionID(), replacementRevision.ID, fmt.Sprintf("profile_%d", size), "image/webp", 9, size, size, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", replacementAt)
		requireNoError(t, renditionErr)
		replacementRenditions = append(replacementRenditions, *rendition)
	}
	replacementAudit := saveUserProfileAuditAttempt(t, ctx, ss, user.ID.String())
	replaced, err := ss.File().PublishProfilePicture(ctx, &store.ProfilePicturePublication{ActorID: user.ID, UserID: user.ID, ExpectedUserRevision: published.User.Revision, EntryID: entry.ID, RevisionID: replacementRevision.ID, LeaseID: replacementLease.ID, Renditions: replacementRenditions, ChangedAt: replacementAt, AuditEventID: replacementAudit.ID.String(), AuditAt: replacementAt.UnixMilli()})
	requireNoError(t, err)
	if replaced.User.CustomProfilePictureFileID != entry.ID || replaced.Revision.ID != replacementRevision.ID {
		t.Fatalf("replacement = %#v", replaced)
	}
	state, err := ss.File().GetProfilePictureState(ctx, user.ID)
	requireNoError(t, err)
	if state.EntryID != entry.ID || state.RevisionID != replacementRevision.ID {
		t.Fatalf("replacement state = %#v", state)
	}
	discardAt := model.NowUTC()
	discardRevision, err := model.NewFileRevision(model.NewFileRevisionID(), entry.ID, model.FileAvailabilityPending, model.FileIndexingNotRequired, discardAt)
	requireNoError(t, err)
	discardLease, err := model.NewUploadLease(model.NewUploadLeaseID(), discardRevision.ID, user.ID, discardAt, discardAt.Add(time.Hour))
	requireNoError(t, err)
	_, err = ss.File().CreateRevisionUpload(ctx, &store.FileRevisionUploadCreation{EntryID: entry.ID, Revision: discardRevision, Lease: discardLease})
	requireNoError(t, err)
	err = ss.File().DiscardProfilePictureUpload(ctx, &store.ProfilePictureUploadDiscard{ActorID: user.ID, UserID: user.ID, ExpectedUserRevision: replaced.User.Revision, ExpectedActiveEntryID: entry.ID, ExpectedCurrentRevisionID: replacementRevision.ID, UploadEntryID: entry.ID, RevisionID: discardRevision.ID, LeaseID: discardLease.ID})
	requireNoError(t, err)
	state, err = ss.File().GetProfilePictureState(ctx, user.ID)
	requireNoError(t, err)
	if state.RevisionID != replacementRevision.ID {
		t.Fatalf("discard changed visible state = %#v", state)
	}
	rollbackAt := model.NowUTC()
	rollbackRevision, err := model.NewFileRevision(model.NewFileRevisionID(), entry.ID, model.FileAvailabilityPending, model.FileIndexingNotRequired, rollbackAt)
	requireNoError(t, err)
	rollbackLease, err := model.NewUploadLease(model.NewUploadLeaseID(), rollbackRevision.ID, user.ID, rollbackAt, rollbackAt.Add(time.Hour))
	requireNoError(t, err)
	_, err = ss.File().CreateRevisionUpload(ctx, &store.FileRevisionUploadCreation{EntryID: entry.ID, Revision: rollbackRevision, Lease: rollbackLease})
	requireNoError(t, err)
	rollbackRenditions := make([]model.FileRendition, 0, 3)
	for _, size := range []int{128, 256, 512} {
		rendition, renditionErr := model.NewFileRendition(model.NewFileRenditionID(), rollbackRevision.ID, fmt.Sprintf("profile_%d", size), "image/webp", 10, size, size, "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", rollbackAt)
		requireNoError(t, renditionErr)
		rollbackRenditions = append(rollbackRenditions, *rendition)
	}
	if _, err = ss.File().PublishProfilePicture(ctx, &store.ProfilePicturePublication{ActorID: user.ID, UserID: user.ID, ExpectedUserRevision: replaced.User.Revision, EntryID: entry.ID, RevisionID: rollbackRevision.ID, LeaseID: rollbackLease.ID, Renditions: rollbackRenditions, ChangedAt: rollbackAt, AuditEventID: model.NewId(), AuditAt: rollbackAt.UnixMilli()}); err == nil {
		t.Fatal("PublishProfilePicture() succeeded without its audit attempt")
	}
	state, err = ss.File().GetProfilePictureState(ctx, user.ID)
	requireNoError(t, err)
	if state.RevisionID != replacementRevision.ID {
		t.Fatalf("audit failure changed visible state = %#v", state)
	}

	removalAt := model.NowUTC()
	removalAudit := saveUserProfileAuditAttempt(t, ctx, ss, user.ID.String())
	removed, err := ss.File().RemoveProfilePictureWithAudit(ctx, &store.ProfilePictureRemoval{ActorID: user.ID, UserID: user.ID, ExpectedUserRevision: replaced.User.Revision, EntryID: entry.ID, ExpectedCurrentRevisionID: replacementRevision.ID, ExpectedSHA256: replacementRenditions[0].SHA256, ChangedAt: removalAt, AuditEventID: removalAudit.ID.String(), AuditAt: removalAt.UnixMilli()})
	requireNoError(t, err)
	if !removed.CustomProfilePictureFileID.IsZero() || removed.Revision != replaced.User.Revision+1 {
		t.Fatalf("removal = %#v", removed)
	}
	defaultAt := model.NowUTC()
	defaultEntry, err := model.NewFileEntry(model.NewFileEntryID(), model.FileIndexingNone, defaultAt)
	requireNoError(t, err)
	defaultRevision, err := model.NewFileRevision(model.NewFileRevisionID(), defaultEntry.ID, model.FileAvailabilityPending, model.FileIndexingNotRequired, defaultAt)
	requireNoError(t, err)
	defaultLease, err := model.NewUploadLease(model.NewUploadLeaseID(), defaultRevision.ID, user.ID, defaultAt, defaultAt.Add(time.Hour))
	requireNoError(t, err)
	_, err = ss.File().CreateUpload(ctx, &store.FileUploadCreation{Entry: defaultEntry, Revision: defaultRevision, Lease: defaultLease})
	requireNoError(t, err)
	defaultRenditions := make([]model.FileRendition, 0, 3)
	for _, size := range []int{128, 256, 512} {
		rendition, renditionErr := model.NewFileRendition(model.NewFileRenditionID(), defaultRevision.ID, fmt.Sprintf("profile_%d", size), "image/webp", 11, size, size, "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd", defaultAt)
		requireNoError(t, renditionErr)
		defaultRenditions = append(defaultRenditions, *rendition)
	}
	withDefault, err := ss.File().PublishDefaultProfilePicture(ctx, &store.DefaultProfilePicturePublication{UserID: user.ID, ExpectedUserRevision: removed.Revision, EntryID: defaultEntry.ID, RevisionID: defaultRevision.ID, LeaseID: defaultLease.ID, Renditions: defaultRenditions, AttachedAt: defaultAt})
	requireNoError(t, err)
	if withDefault.User.DefaultProfilePictureFileID != defaultEntry.ID || withDefault.User.Revision != removed.Revision+1 || !withDefault.User.ProfilePictureChangedAt.Time.Equal(removed.ProfilePictureChangedAt.Time) {
		t.Fatalf("default publication = %#v", withDefault)
	}
	got, err = ss.File().GetProfilePictureRendition(ctx, user.ID, "profile_128")
	requireNoError(t, err)
	if got.ID != defaultRenditions[0].ID {
		t.Fatalf("fallback rendition = %#v", got)
	}
	archivedAt := model.NowUTC()
	archivedRevision, err := model.NewFileRevision(model.NewFileRevisionID(), entry.ID, model.FileAvailabilityPending, model.FileIndexingNotRequired, archivedAt)
	requireNoError(t, err)
	archivedLease, err := model.NewUploadLease(model.NewUploadLeaseID(), archivedRevision.ID, user.ID, archivedAt, archivedAt.Add(time.Hour))
	requireNoError(t, err)
	if _, err = ss.File().CreateRevisionUpload(ctx, &store.FileRevisionUploadCreation{EntryID: entry.ID, Revision: archivedRevision, Lease: archivedLease}); !store.IsNotFound(err) {
		t.Fatalf("CreateRevisionUpload(archived) error = %v", err)
	}
	expiredAt := model.NowUTC().Add(-48 * time.Hour)
	expiredEntry, err := model.NewFileEntry(model.NewFileEntryID(), model.FileIndexingNone, expiredAt.Add(-time.Hour))
	requireNoError(t, err)
	expiredRevision, err := model.NewFileRevision(model.NewFileRevisionID(), expiredEntry.ID, model.FileAvailabilityPending, model.FileIndexingNotRequired, expiredAt.Add(-time.Hour))
	requireNoError(t, err)
	expiredLease, err := model.NewUploadLease(model.NewUploadLeaseID(), expiredRevision.ID, user.ID, expiredAt.Add(-time.Hour), expiredAt)
	requireNoError(t, err)
	_, err = ss.File().CreateUpload(ctx, &store.FileUploadCreation{Entry: expiredEntry, Revision: expiredRevision, Lease: expiredLease})
	requireNoError(t, err)
	expiredRenditions := make([]model.FileRendition, 0, 3)
	for _, size := range []int{128, 256, 512} {
		rendition, renditionErr := model.NewFileRendition(model.NewFileRenditionID(), expiredRevision.ID, fmt.Sprintf("profile_%d", size), "image/webp", 8, size, size, "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", expiredAt)
		requireNoError(t, renditionErr)
		expiredRenditions = append(expiredRenditions, *rendition)
	}
	expiredAudit := saveUserProfileAuditAttempt(t, ctx, ss, user.ID.String())
	if _, err = ss.File().PublishProfilePicture(ctx, &store.ProfilePicturePublication{ActorID: user.ID, UserID: user.ID, ExpectedUserRevision: withDefault.User.Revision, EntryID: expiredEntry.ID, RevisionID: expiredRevision.ID, LeaseID: expiredLease.ID, Renditions: expiredRenditions, ChangedAt: model.NowUTC(), AuditEventID: expiredAudit.ID.String(), AuditAt: model.NowUTC().UnixMilli()}); !store.IsConflict(err) {
		t.Fatalf("PublishProfilePicture(expired lease) error = %v", err)
	}
	page, err := ss.File().ListPurgeCandidates(ctx, &store.FilePurgeCandidateRequest{Limit: 10})
	requireNoError(t, err)
	var expiredCandidate *store.FilePurgeCandidate
	for index := range page.Candidates {
		if page.Candidates[index].LeaseID == expiredLease.ID {
			expiredCandidate = &page.Candidates[index]
		}
	}
	if expiredCandidate == nil {
		t.Fatal("expired lease was not selected from authoritative metadata")
	}
	claim, err := ss.File().ClaimPurgeCandidate(ctx, expiredCandidate)
	requireNoError(t, err)
	if claim.ID == "" || claim.Candidate.RevisionID != expiredCandidate.RevisionID {
		t.Fatalf("ClaimPurgeCandidate() = %#v", claim)
	}
	if _, err = ss.File().RenewUploadLease(ctx, expiredLease.ID, user.ID, expiredLease.Revision, 1, model.NowUTC().Add(time.Hour)); !store.IsConflict(err) && !store.IsNotFound(err) {
		t.Fatalf("RenewUploadLease(claimed) error = %v", err)
	}
	if _, err = ss.File().PublishProfilePicture(ctx, &store.ProfilePicturePublication{ActorID: user.ID, UserID: user.ID, ExpectedUserRevision: withDefault.User.Revision, EntryID: expiredEntry.ID, RevisionID: expiredRevision.ID, LeaseID: expiredLease.ID, Renditions: expiredRenditions, ChangedAt: model.NowUTC(), AuditEventID: expiredAudit.ID.String(), AuditAt: model.NowUTC().UnixMilli()}); !store.IsConflict(err) && !store.IsNotFound(err) {
		t.Fatalf("PublishProfilePicture(claimed) error = %v", err)
	}
	requireNoError(t, ss.File().CompletePurge(ctx, claim))
	if err = ss.File().CompletePurge(ctx, claim); !store.IsConflict(err) && !store.IsNotFound(err) {
		t.Fatalf("CompletePurge(idempotent) error = %v", err)
	}
}
