//go:build integration

// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package server

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	vfspkg "github.com/sudosylabs/proctor/packages/vfs"
	localvfs "github.com/sudosylabs/proctor/packages/vfs/local"
	memoryvfs "github.com/sudosylabs/proctor/packages/vfs/memory"
	s3vfs "github.com/sudosylabs/proctor/packages/vfs/s3"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/filecontent"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/sqlstore"
)

func TestFilePurgeStorageIntegrationOnLocalVFS(t *testing.T) {
	filesystem, err := localvfs.New(filepath.Join(t.TempDir(), "vfs"))
	require.NoError(t, err)
	provePostgreSQLReferencedRenditionsSurvivePurge(t, filesystem)
}

func TestFilePurgeStorageIntegrationOnS3(t *testing.T) {
	endpoint, bucket := os.Getenv("VFS_S3_ENDPOINT"), os.Getenv("VFS_S3_BUCKET")
	if endpoint == "" || bucket == "" {
		t.Skip("set VFS_S3_ENDPOINT and VFS_S3_BUCKET to run S3 purge integration")
	}
	secure, err := strconv.ParseBool(environmentDefault(os.Getenv("VFS_S3_SECURE"), "true"))
	require.NoError(t, err)
	filesystem, err := s3vfs.New(s3vfs.Config{
		Endpoint: endpoint, AccessKey: os.Getenv("VFS_S3_ACCESS_KEY"), SecretKey: os.Getenv("VFS_S3_SECRET_KEY"),
		SessionToken: os.Getenv("VFS_S3_SESSION_TOKEN"), Bucket: bucket, Region: os.Getenv("VFS_S3_REGION"),
		Secure: secure, Prefix: fmt.Sprintf("proctor-file-purge/%d", time.Now().UnixNano()),
	})
	require.NoError(t, err)
	provePostgreSQLReferencedRenditionsSurvivePurge(t, filesystem)
}

func provePostgreSQLReferencedRenditionsSurvivePurge(t *testing.T, filesystem vfspkg.FileSystem) {
	t.Helper()
	ctx := context.Background()
	persistence := openFilePurgeStorageIntegrationStore(t)
	content, err := filecontent.New(filesystem, filecontent.Policy{MaximumConcurrentOperations: 2}, nil)
	require.NoError(t, err)
	proveCustomProfilePicturePipelineMatchesMemory(t, content)
	now := model.NowUTC()
	user := &model.User{
		Username: "purge-storage-" + model.NewID(),
		Email:    model.NewID() + "@example.test",
	}
	user.PrepareCreate(model.NewUserID(), now)
	command, err := model.EncodeDefaultProfilePictureCommand(model.DefaultProfilePictureCommandV1{UserID: user.ID})
	require.NoError(t, err)
	job, err := model.NewJob(model.NewJobID(), model.JobTypeProfilePictureGenerateDefault, 1, command, user.ID.String(), now, now, 8)
	require.NoError(t, err)
	settings, err := model.NewUserSettingsDocument(user.ID, model.NewUserSettingsRevision(), now)
	require.NoError(t, err)
	created, err := persistence.User().Create(ctx, &store.UserCreation{User: user, Settings: settings, DefaultProfilePictureJob: job})
	require.NoError(t, err)

	referencedEntry, err := model.NewFileEntryForPurpose(model.NewFileEntryID(), model.FilePurposeProfilePictureDefault, model.FileIndexingNone, now)
	require.NoError(t, err)
	referencedRevision, err := model.NewFileRevision(model.NewFileRevisionID(), referencedEntry.ID, model.FileAvailabilityPending, model.FileIndexingNotRequired, now)
	require.NoError(t, err)
	referencedLease, err := model.NewUploadLease(model.NewUploadLeaseID(), referencedRevision.ID, user.ID, now, now.Add(time.Hour))
	require.NoError(t, err)
	_, err = persistence.File().CreateUpload(ctx, &store.FileUploadCreation{Entry: referencedEntry, Revision: referencedRevision, Lease: referencedLease})
	require.NoError(t, err)
	referencedRenditions, err := content.GenerateAndStoreDefaultProfilePicture(ctx, referencedRevision.ID, user.DefaultProfilePictureSeed, now)
	require.NoError(t, err)
	published, err := persistence.File().PublishDefaultProfilePicture(ctx, &store.DefaultProfilePicturePublication{
		UserID: user.ID, ExpectedUserRevision: created.User.Revision, EntryID: referencedEntry.ID,
		RevisionID: referencedRevision.ID, LeaseID: referencedLease.ID, Renditions: referencedRenditions, AttachedAt: now,
	})
	require.NoError(t, err)

	staleAt := now.Add(-25 * time.Hour)
	purgeEntry, err := model.NewFileEntryForPurpose(model.NewFileEntryID(), model.FilePurposeProfilePictureCustom, model.FileIndexingNone, staleAt)
	require.NoError(t, err)
	purgeRevision, err := model.NewFileRevision(model.NewFileRevisionID(), purgeEntry.ID, model.FileAvailabilityPending, model.FileIndexingNotRequired, staleAt)
	require.NoError(t, err)
	purgeLease, err := model.NewUploadLease(model.NewUploadLeaseID(), purgeRevision.ID, user.ID, staleAt, staleAt.Add(time.Hour))
	require.NoError(t, err)
	_, err = persistence.File().CreateUpload(ctx, &store.FileUploadCreation{Entry: purgeEntry, Revision: purgeRevision, Lease: purgeLease})
	require.NoError(t, err)
	purgeRenditions, err := content.GenerateAndStoreDefaultProfilePicture(ctx, purgeRevision.ID, user.DefaultProfilePictureSeed, staleAt)
	require.NoError(t, err)

	state, err := persistence.File().GetProfilePictureState(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, referencedEntry.ID, state.EntryID)
	require.Equal(t, referencedRevision.ID, state.RevisionID)
	page, err := persistence.File().ListPurgeCandidates(ctx, &store.FilePurgeCandidateRequest{Limit: 100})
	require.NoError(t, err)
	var candidate *store.FilePurgeCandidate
	for index := range page.Candidates {
		value := &page.Candidates[index]
		require.NotEqual(t, referencedRevision.ID, value.RevisionID)
		if value.RevisionID == purgeRevision.ID {
			candidate = value
		}
	}
	require.NotNil(t, candidate, "expired pending revision was not selected: %#v", page.Candidates)
	claim, err := persistence.File().ClaimPurgeCandidate(ctx, candidate)
	require.NoError(t, err)
	err = content.PurgeAbandonedFileRevision(ctx, claim.Candidate.RevisionID)
	require.NoError(t, err)
	err = persistence.File().CompletePurge(ctx, claim)
	require.NoError(t, err)

	visible, err := persistence.File().GetProfilePictureRendition(ctx, published.User.ID, "profile_128")
	require.NoError(t, err)
	body, err := content.OpenProfilePictureRendition(ctx, visible.RevisionID, visible.ID)
	require.NoError(t, err)
	_ = body.Close()
	for _, rendition := range purgeRenditions {
		body, openErr := content.OpenProfilePictureRendition(ctx, purgeRevision.ID, rendition.ID)
		if body != nil {
			_ = body.Close()
		}
		require.ErrorIs(t, openErr, vfspkg.ErrNotFound, "purged rendition %s still exists", rendition.ID)
	}
	var remainingMetadata int
	err = persistence.GetMaster().Get(ctx, &remainingMetadata, `SELECT count(*) FROM file_revisions WHERE id = $1`, purgeRevision.ID.String())
	require.NoError(t, err)
	require.Equal(t, 0, remainingMetadata, "purged revision metadata count")
}

func proveCustomProfilePicturePipelineMatchesMemory(t *testing.T, actual *filecontent.Content) {
	t.Helper()
	source := image.NewNRGBA(image.Rect(0, 0, 40, 20))
	for y := 0; y < 20; y++ {
		for x := 0; x < 40; x++ {
			source.Set(x, y, color.NRGBA{R: 200, G: 10, B: 20, A: 255})
		}
	}
	var input bytes.Buffer
	require.NoError(t, png.Encode(&input, source))
	reference, err := filecontent.New(memoryvfs.New(), filecontent.Policy{MaximumConcurrentOperations: 2}, nil)
	require.NoError(t, err)
	createdAt := time.Date(2026, time.August, 11, 0, 0, 0, 0, time.UTC)
	referenceRevisionID, actualRevisionID := model.NewFileRevisionID(), model.NewFileRevisionID()
	want, err := reference.NormalizeAndStoreProfilePicture(context.Background(), referenceRevisionID, bytes.NewReader(input.Bytes()), int64(input.Len()), createdAt)
	require.NoError(t, err)
	got, err := actual.NormalizeAndStoreProfilePicture(context.Background(), actualRevisionID, bytes.NewReader(input.Bytes()), int64(input.Len()), createdAt)
	require.NoError(t, err)
	require.Len(t, got, len(want), "custom rendition count")
	wantByName := make(map[string]model.FileRendition, len(want))
	for _, rendition := range want {
		wantByName[rendition.Name] = rendition
	}
	for _, rendition := range got {
		expected, found := wantByName[rendition.Name]
		require.True(t, found, "backend rendition differs from memory: got=%#v want=%#v", rendition, expected)
		require.Equal(t, expected.MediaType, rendition.MediaType, "backend rendition differs from memory: got=%#v want=%#v", rendition, expected)
		require.Equal(t, expected.Size, rendition.Size, "backend rendition differs from memory: got=%#v want=%#v", rendition, expected)
		require.Equal(t, expected.Width, rendition.Width, "backend rendition differs from memory: got=%#v want=%#v", rendition, expected)
		require.Equal(t, expected.Height, rendition.Height, "backend rendition differs from memory: got=%#v want=%#v", rendition, expected)
		require.Equal(t, expected.SHA256, rendition.SHA256, "backend rendition differs from memory: got=%#v want=%#v", rendition, expected)
		actualBody, openErr := actual.OpenProfilePictureRendition(context.Background(), actualRevisionID, rendition.ID)
		require.NoError(t, openErr)
		actualBytes, readErr := io.ReadAll(actualBody)
		_ = actualBody.Close()
		require.NoError(t, readErr)
		referenceBody, openErr := reference.OpenProfilePictureRendition(context.Background(), referenceRevisionID, expected.ID)
		require.NoError(t, openErr)
		referenceBytes, readErr := io.ReadAll(referenceBody)
		_ = referenceBody.Close()
		require.NoError(t, readErr)
		require.True(t, bytes.Equal(actualBytes, referenceBytes), "backend bytes differ for %s", rendition.Name)
	}
	ids := make([]model.FileRenditionID, 0, len(got))
	for _, rendition := range got {
		ids = append(ids, rendition.ID)
	}
	err = actual.RemoveFileRevisionRenditions(context.Background(), actualRevisionID, ids)
	require.NoError(t, err)
}

func openFilePurgeStorageIntegrationStore(t *testing.T) *sqlstore.SQLStore {
	t.Helper()
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	require.NotEmpty(t, dataSource, "PROCTOR_TEST_DATABASE_URL is not set")
	database := config.Default().Database
	database.DataSource = dataSource
	settings := sqlstore.SettingsFromConfig(database)
	migrator, err := sqlstore.NewMigrator(context.Background(), settings)
	require.NoError(t, err)
	if err = migrator.Up(); err != nil {
		_ = migrator.Close()
		require.NoError(t, err)
	}
	err = migrator.Close()
	require.NoError(t, err)
	persistence, err := sqlstore.New(context.Background(), settings)
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, persistence.Close())
	})
	_, err = persistence.GetMaster().Exec(context.Background(), `TRUNCATE TABLE users, file_entries, job_permanent_occurrences, jobs CASCADE`)
	require.NoError(t, err)
	return persistence
}

func environmentDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
