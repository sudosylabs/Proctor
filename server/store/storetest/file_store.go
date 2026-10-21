// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package storetest

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestFileStore(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	at := model.NowUTC()
	entry, err := model.NewFileEntry(model.NewFileEntryID(), model.FileIndexingNone, at)
	require.NoError(t, err)
	revision, err := model.NewFileRevision(model.NewFileRevisionID(), entry.ID, model.FileAvailabilityPending, model.FileIndexingNotRequired, at)
	require.NoError(t, err)
	lease, err := model.NewUploadLease(model.NewUploadLeaseID(), revision.ID, user.ID, at, at.Add(time.Hour))
	require.NoError(t, err)

	nonPristineEntry := *entry
	nonPristineEntry.CurrentRevisionID = revision.ID
	_, err = ss.File().CreateUpload(ctx, &store.FileUploadCreation{Entry: &nonPristineEntry, Revision: revision, Lease: lease})
	require.Error(t, err, "CreateUpload() accepted an entry with a current revision")
	created, err := ss.File().CreateUpload(ctx, &store.FileUploadCreation{Entry: entry, Revision: revision, Lease: lease})
	require.NoError(t, err)
	require.Equal(t, entry.ID, created.Entry.ID)
	require.Equal(t, revision.ID, created.Lease.FileRevisionID)
	renewedUntil := model.NowUTC().Add(30 * time.Minute)
	renewed, err := ss.File().RenewUploadLease(ctx, lease.ID, user.ID, lease.Revision, 1, renewedUntil)
	require.NoError(t, err, "RenewUploadLease() error")
	require.True(t, renewed.ExpiresAt.Equal(renewedUntil))
	_, err = ss.File().RenewUploadLease(ctx, lease.ID, user.ID, renewed.Revision, 1, renewedUntil)
	require.True(t, store.IsConflict(err), "RenewUploadLease(without progress) error")
	// Creation timestamps arrive from an application node, while renewal leases
	// are fenced by the primary database clock. Small forward node skew must not
	// make a valid, progressing lease fail its monotonic domain validation.
	futureAt := model.NowUTC().Add(5 * time.Minute)
	futureEntry, err := model.NewFileEntry(model.NewFileEntryID(), model.FileIndexingNone, futureAt)
	require.NoError(t, err)
	futureRevision, err := model.NewFileRevision(model.NewFileRevisionID(), futureEntry.ID, model.FileAvailabilityPending, model.FileIndexingNotRequired, futureAt)
	require.NoError(t, err)
	futureLease, err := model.NewUploadLease(model.NewUploadLeaseID(), futureRevision.ID, user.ID, futureAt, futureAt.Add(time.Hour))
	require.NoError(t, err)
	_, err = ss.File().CreateUpload(ctx, &store.FileUploadCreation{Entry: futureEntry, Revision: futureRevision, Lease: futureLease})
	require.NoError(t, err)
	_, err = ss.File().RenewUploadLease(ctx, futureLease.ID, user.ID, futureLease.Revision, 1, futureAt.Add(30*time.Minute))
	require.NoError(t, err)
	renditions := make([]model.FileRendition, 0, 3)
	for _, size := range []int{128, 256, 512} {
		rendition, renditionErr := model.NewFileRendition(model.NewFileRenditionID(), revision.ID, fmt.Sprintf("profile_%d", size), "image/webp", 8, size, size, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", at)
		require.NoError(t, renditionErr)
		renditions = append(renditions, *rendition)
	}
	_, err = ss.File().PublishProfilePicture(ctx, &store.ProfilePicturePublication{
		ActorID: user.ID, UserID: user.ID, ExpectedUserRevision: user.Revision, EntryID: entry.ID,
		RevisionID: revision.ID, LeaseID: lease.ID, Renditions: renditions[:1], ChangedAt: model.NowUTC(),
	})
	require.Error(t, err, "PublishProfilePicture() accepted an incomplete rendition set")
	changeAt := model.NowUTC()
	auditAttempt := saveUserProfileAuditAttempt(t, ctx, ss, user.ID.String())
	published, err := ss.File().PublishProfilePicture(ctx, &store.ProfilePicturePublication{
		ActorID: user.ID, UserID: user.ID, ExpectedUserRevision: user.Revision, EntryID: entry.ID,
		RevisionID: revision.ID, LeaseID: lease.ID, Renditions: renditions, ChangedAt: changeAt,
		AuditEventID: auditAttempt.ID, AuditAt: changeAt.UnixMilli(),
	})
	require.NoError(t, err)
	require.Equal(t, entry.ID, published.User.CustomProfilePictureFileID)
	require.Equal(t, model.FileAvailabilityAvailable, published.Revision.Availability)
	got, err := ss.File().GetProfilePictureRendition(ctx, user.ID, "profile_128")
	require.NoError(t, err)
	require.Equal(t, renditions[0].ID, got.ID)

	replacementAt := model.NowUTC()
	replacementRevision, err := model.NewFileRevision(model.NewFileRevisionID(), entry.ID, model.FileAvailabilityPending, model.FileIndexingNotRequired, replacementAt)
	require.NoError(t, err)
	replacementLease, err := model.NewUploadLease(model.NewUploadLeaseID(), replacementRevision.ID, user.ID, replacementAt, replacementAt.Add(time.Hour))
	require.NoError(t, err)
	_, err = ss.File().CreateRevisionUpload(ctx, &store.FileRevisionUploadCreation{EntryID: entry.ID, Revision: replacementRevision, Lease: replacementLease})
	require.NoError(t, err)
	replacementRenditions := make([]model.FileRendition, 0, 3)
	for _, size := range []int{128, 256, 512} {
		rendition, renditionErr := model.NewFileRendition(model.NewFileRenditionID(), replacementRevision.ID, fmt.Sprintf("profile_%d", size), "image/webp", 9, size, size, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", replacementAt)
		require.NoError(t, renditionErr)
		replacementRenditions = append(replacementRenditions, *rendition)
	}
	replacementAudit := saveUserProfileAuditAttempt(t, ctx, ss, user.ID.String())
	replaced, err := ss.File().PublishProfilePicture(ctx, &store.ProfilePicturePublication{ActorID: user.ID, UserID: user.ID, ExpectedUserRevision: published.User.Revision, EntryID: entry.ID, RevisionID: replacementRevision.ID, LeaseID: replacementLease.ID, Renditions: replacementRenditions, ChangedAt: replacementAt, AuditEventID: replacementAudit.ID, AuditAt: replacementAt.UnixMilli()})
	require.NoError(t, err)
	require.Equal(t, entry.ID, replaced.User.CustomProfilePictureFileID)
	require.Equal(t, replacementRevision.ID, replaced.Revision.ID)
	state, err := ss.File().GetProfilePictureState(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, entry.ID, state.EntryID)
	require.Equal(t, replacementRevision.ID, state.RevisionID)
	discardAt := model.NowUTC()
	discardRevision, err := model.NewFileRevision(model.NewFileRevisionID(), entry.ID, model.FileAvailabilityPending, model.FileIndexingNotRequired, discardAt)
	require.NoError(t, err)
	discardLease, err := model.NewUploadLease(model.NewUploadLeaseID(), discardRevision.ID, user.ID, discardAt, discardAt.Add(time.Hour))
	require.NoError(t, err)
	_, err = ss.File().CreateRevisionUpload(ctx, &store.FileRevisionUploadCreation{EntryID: entry.ID, Revision: discardRevision, Lease: discardLease})
	require.NoError(t, err)
	err = ss.File().DiscardProfilePictureUpload(ctx, &store.ProfilePictureUploadDiscard{ActorID: user.ID, UserID: user.ID, ExpectedUserRevision: replaced.User.Revision, ExpectedActiveEntryID: entry.ID, ExpectedCurrentRevisionID: replacementRevision.ID, UploadEntryID: entry.ID, RevisionID: discardRevision.ID, LeaseID: discardLease.ID})
	require.NoError(t, err)
	state, err = ss.File().GetProfilePictureState(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, replacementRevision.ID, state.RevisionID)
	rollbackAt := model.NowUTC()
	rollbackRevision, err := model.NewFileRevision(model.NewFileRevisionID(), entry.ID, model.FileAvailabilityPending, model.FileIndexingNotRequired, rollbackAt)
	require.NoError(t, err)
	rollbackLease, err := model.NewUploadLease(model.NewUploadLeaseID(), rollbackRevision.ID, user.ID, rollbackAt, rollbackAt.Add(time.Hour))
	require.NoError(t, err)
	_, err = ss.File().CreateRevisionUpload(ctx, &store.FileRevisionUploadCreation{EntryID: entry.ID, Revision: rollbackRevision, Lease: rollbackLease})
	require.NoError(t, err)
	rollbackRenditions := make([]model.FileRendition, 0, 3)
	for _, size := range []int{128, 256, 512} {
		rendition, renditionErr := model.NewFileRendition(model.NewFileRenditionID(), rollbackRevision.ID, fmt.Sprintf("profile_%d", size), "image/webp", 10, size, size, "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", rollbackAt)
		require.NoError(t, renditionErr)
		rollbackRenditions = append(rollbackRenditions, *rendition)
	}
	_, err = ss.File().PublishProfilePicture(ctx, &store.ProfilePicturePublication{ActorID: user.ID, UserID: user.ID, ExpectedUserRevision: replaced.User.Revision, EntryID: entry.ID, RevisionID: rollbackRevision.ID, LeaseID: rollbackLease.ID, Renditions: rollbackRenditions, ChangedAt: rollbackAt, AuditEventID: model.NewAuditEventID(), AuditAt: rollbackAt.UnixMilli()})
	require.Error(t, err, "PublishProfilePicture() succeeded without its audit attempt")
	state, err = ss.File().GetProfilePictureState(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, replacementRevision.ID, state.RevisionID)

	removalAt := model.NowUTC()
	removalAudit := saveUserProfileAuditAttempt(t, ctx, ss, user.ID.String())
	removed, err := ss.File().RemoveProfilePicture(ctx, &store.ProfilePictureRemoval{ActorID: user.ID, UserID: user.ID, ExpectedUserRevision: replaced.User.Revision, EntryID: entry.ID, ExpectedCurrentRevisionID: replacementRevision.ID, ExpectedSHA256: replacementRenditions[0].SHA256, ChangedAt: removalAt, AuditEventID: removalAudit.ID, AuditAt: removalAt.UnixMilli()})
	require.NoError(t, err)
	require.True(t, removed.CustomProfilePictureFileID.IsZero())
	require.Equal(t, replaced.User.Revision+1, removed.Revision)
	defaultAt := model.NowUTC()
	defaultEntry, err := model.NewFileEntry(model.NewFileEntryID(), model.FileIndexingNone, defaultAt)
	require.NoError(t, err)
	defaultRevision, err := model.NewFileRevision(model.NewFileRevisionID(), defaultEntry.ID, model.FileAvailabilityPending, model.FileIndexingNotRequired, defaultAt)
	require.NoError(t, err)
	defaultLease, err := model.NewUploadLease(model.NewUploadLeaseID(), defaultRevision.ID, user.ID, defaultAt, defaultAt.Add(time.Hour))
	require.NoError(t, err)
	_, err = ss.File().CreateUpload(ctx, &store.FileUploadCreation{Entry: defaultEntry, Revision: defaultRevision, Lease: defaultLease})
	require.NoError(t, err)
	defaultRenditions := make([]model.FileRendition, 0, 3)
	for _, size := range []int{128, 256, 512} {
		rendition, renditionErr := model.NewFileRendition(model.NewFileRenditionID(), defaultRevision.ID, fmt.Sprintf("profile_%d", size), "image/webp", 11, size, size, "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd", defaultAt)
		require.NoError(t, renditionErr)
		defaultRenditions = append(defaultRenditions, *rendition)
	}
	withDefault, err := ss.File().PublishDefaultProfilePicture(ctx, &store.DefaultProfilePicturePublication{UserID: user.ID, ExpectedUserRevision: removed.Revision, EntryID: defaultEntry.ID, RevisionID: defaultRevision.ID, LeaseID: defaultLease.ID, Renditions: defaultRenditions, AttachedAt: defaultAt})
	require.NoError(t, err)
	require.Equal(t, defaultEntry.ID, withDefault.User.DefaultProfilePictureFileID)
	require.Equal(t, removed.Revision+1, withDefault.User.Revision)
	require.True(t, withDefault.User.ProfilePictureChangedAt.Time.Equal(removed.ProfilePictureChangedAt.Time))
	got, err = ss.File().GetProfilePictureRendition(ctx, user.ID, "profile_128")
	require.NoError(t, err)
	require.Equal(t, defaultRenditions[0].ID, got.ID)
	archivedAt := model.NowUTC()
	archivedRevision, err := model.NewFileRevision(model.NewFileRevisionID(), entry.ID, model.FileAvailabilityPending, model.FileIndexingNotRequired, archivedAt)
	require.NoError(t, err)
	archivedLease, err := model.NewUploadLease(model.NewUploadLeaseID(), archivedRevision.ID, user.ID, archivedAt, archivedAt.Add(time.Hour))
	require.NoError(t, err)
	_, err = ss.File().CreateRevisionUpload(ctx, &store.FileRevisionUploadCreation{EntryID: entry.ID, Revision: archivedRevision, Lease: archivedLease})
	require.True(t, store.IsNotFound(err), "CreateRevisionUpload(archived) error")
	expiredAt := model.NowUTC().Add(-48 * time.Hour)
	expiredEntry, err := model.NewFileEntry(model.NewFileEntryID(), model.FileIndexingNone, expiredAt.Add(-time.Hour))
	require.NoError(t, err)
	expiredRevision, err := model.NewFileRevision(model.NewFileRevisionID(), expiredEntry.ID, model.FileAvailabilityPending, model.FileIndexingNotRequired, expiredAt.Add(-time.Hour))
	require.NoError(t, err)
	expiredLease, err := model.NewUploadLease(model.NewUploadLeaseID(), expiredRevision.ID, user.ID, expiredAt.Add(-time.Hour), expiredAt)
	require.NoError(t, err)
	_, err = ss.File().CreateUpload(ctx, &store.FileUploadCreation{Entry: expiredEntry, Revision: expiredRevision, Lease: expiredLease})
	require.NoError(t, err)
	expiredRenditions := make([]model.FileRendition, 0, 3)
	for _, size := range []int{128, 256, 512} {
		rendition, renditionErr := model.NewFileRendition(model.NewFileRenditionID(), expiredRevision.ID, fmt.Sprintf("profile_%d", size), "image/webp", 8, size, size, "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", expiredAt)
		require.NoError(t, renditionErr)
		expiredRenditions = append(expiredRenditions, *rendition)
	}
	expiredAudit := saveUserProfileAuditAttempt(t, ctx, ss, user.ID.String())
	_, err = ss.File().PublishProfilePicture(ctx, &store.ProfilePicturePublication{ActorID: user.ID, UserID: user.ID, ExpectedUserRevision: withDefault.User.Revision, EntryID: expiredEntry.ID, RevisionID: expiredRevision.ID, LeaseID: expiredLease.ID, Renditions: expiredRenditions, ChangedAt: model.NowUTC(), AuditEventID: expiredAudit.ID, AuditAt: model.NowUTC().UnixMilli()})
	require.True(t, store.IsConflict(err), "PublishProfilePicture(expired lease) error")
	page, err := ss.File().ListPurgeCandidates(ctx, &store.FilePurgeCandidateRequest{Limit: 10})
	require.NoError(t, err)
	var expiredCandidate *store.FilePurgeCandidate
	for index := range page.Candidates {
		if page.Candidates[index].LeaseID == expiredLease.ID {
			expiredCandidate = &page.Candidates[index]
		}
	}
	require.NotNil(t, expiredCandidate, "expired lease was not selected from authoritative metadata")
	claim, err := ss.File().ClaimPurgeCandidate(ctx, expiredCandidate)
	require.NoError(t, err)
	require.NotEmpty(t, claim.ID)
	require.Equal(t, expiredCandidate.RevisionID, claim.Candidate.RevisionID)
	_, err = ss.File().RenewUploadLease(ctx, expiredLease.ID, user.ID, expiredLease.Revision, 1, model.NowUTC().Add(time.Hour))
	if !store.IsConflict(err) {
		require.True(t, store.IsNotFound(err), "RenewUploadLease(claimed) error")
	}
	_, err = ss.File().PublishProfilePicture(ctx, &store.ProfilePicturePublication{ActorID: user.ID, UserID: user.ID, ExpectedUserRevision: withDefault.User.Revision, EntryID: expiredEntry.ID, RevisionID: expiredRevision.ID, LeaseID: expiredLease.ID, Renditions: expiredRenditions, ChangedAt: model.NowUTC(), AuditEventID: expiredAudit.ID, AuditAt: model.NowUTC().UnixMilli()})
	if !store.IsConflict(err) {
		require.True(t, store.IsNotFound(err), "PublishProfilePicture(claimed) error")
	}
	require.NoError(t, ss.File().CompletePurge(ctx, claim))
	err = ss.File().CompletePurge(ctx, claim)
	if !store.IsConflict(err) {
		require.True(t, store.IsNotFound(err), "CompletePurge(idempotent) error")
	}
}
