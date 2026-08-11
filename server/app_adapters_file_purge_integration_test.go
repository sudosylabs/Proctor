//go:build integration

// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	vfspkg "github.com/sudosylabs/proctor/packages/vfs"
	localvfs "github.com/sudosylabs/proctor/packages/vfs/local"
	s3vfs "github.com/sudosylabs/proctor/packages/vfs/s3"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/sqlstore"
)

func TestFilePurgeStorageIntegrationOnLocalVFS(t *testing.T) {
	filesystem, err := localvfs.New(filepath.Join(t.TempDir(), "vfs"))
	if err != nil {
		t.Fatal(err)
	}
	provePostgreSQLReferencedRenditionsSurvivePurge(t, filesystem)
}

func TestFilePurgeStorageIntegrationOnS3(t *testing.T) {
	endpoint, bucket := os.Getenv("VFS_S3_ENDPOINT"), os.Getenv("VFS_S3_BUCKET")
	if endpoint == "" || bucket == "" {
		t.Skip("set VFS_S3_ENDPOINT and VFS_S3_BUCKET to run S3 purge integration")
	}
	secure, err := strconv.ParseBool(environmentDefault(os.Getenv("VFS_S3_SECURE"), "true"))
	if err != nil {
		t.Fatal(err)
	}
	filesystem, err := s3vfs.New(s3vfs.Config{
		Endpoint: endpoint, AccessKey: os.Getenv("VFS_S3_ACCESS_KEY"), SecretKey: os.Getenv("VFS_S3_SECRET_KEY"),
		SessionToken: os.Getenv("VFS_S3_SESSION_TOKEN"), Bucket: bucket, Region: os.Getenv("VFS_S3_REGION"),
		Secure: secure, Prefix: fmt.Sprintf("proctor-file-purge/%d", time.Now().UnixNano()),
	})
	if err != nil {
		t.Fatal(err)
	}
	provePostgreSQLReferencedRenditionsSurvivePurge(t, filesystem)
}

func provePostgreSQLReferencedRenditionsSurvivePurge(t *testing.T, filesystem vfspkg.FileSystem) {
	t.Helper()
	ctx := context.Background()
	persistence := openFilePurgeStorageIntegrationStore(t)
	adapter := fileContentAdapter{filesystem: filesystem}
	now := model.NowUTC()
	user := &model.User{
		Username: "purge-storage-" + model.NewId(),
		Email:    model.NewId() + "@example.test",
	}
	user.PrepareCreate(model.NewUserID(), now)
	command, err := model.EncodeDefaultProfilePictureCommand(model.DefaultProfilePictureCommandV1{UserID: user.ID})
	if err != nil {
		t.Fatal(err)
	}
	job, err := model.NewJob(model.NewJobID(), model.JobTypeProfilePictureGenerateDefault, 1, command, user.ID.String(), now, now, 8)
	if err != nil {
		t.Fatal(err)
	}
	created, err := persistence.User().Create(ctx, &store.UserCreation{User: user, DefaultProfilePictureJob: job})
	if err != nil {
		t.Fatal(err)
	}

	referencedEntry, err := model.NewFileEntryForPurpose(model.NewFileEntryID(), model.FilePurposeProfilePictureDefault, model.FileIndexingNone, now)
	if err != nil {
		t.Fatal(err)
	}
	referencedRevision, err := model.NewFileRevision(model.NewFileRevisionID(), referencedEntry.ID, model.FileAvailabilityPending, model.FileIndexingNotRequired, now)
	if err != nil {
		t.Fatal(err)
	}
	referencedLease, err := model.NewUploadLease(model.NewUploadLeaseID(), referencedRevision.ID, user.ID, now, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = persistence.File().CreateUpload(ctx, &store.FileUploadCreation{Entry: referencedEntry, Revision: referencedRevision, Lease: referencedLease}); err != nil {
		t.Fatal(err)
	}
	referencedRenditions, err := adapter.GenerateAndStoreDefaultProfilePicture(ctx, referencedRevision.ID, user.DefaultProfilePictureSeed, now)
	if err != nil {
		t.Fatal(err)
	}
	published, err := persistence.File().PublishDefaultProfilePicture(ctx, &store.DefaultProfilePicturePublication{
		UserID: user.ID, ExpectedUserRevision: created.User.Revision, EntryID: referencedEntry.ID,
		RevisionID: referencedRevision.ID, LeaseID: referencedLease.ID, Renditions: referencedRenditions, AttachedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}

	staleAt := now.Add(-25 * time.Hour)
	purgeEntry, err := model.NewFileEntryForPurpose(model.NewFileEntryID(), model.FilePurposeProfilePictureCustom, model.FileIndexingNone, staleAt)
	if err != nil {
		t.Fatal(err)
	}
	purgeRevision, err := model.NewFileRevision(model.NewFileRevisionID(), purgeEntry.ID, model.FileAvailabilityPending, model.FileIndexingNotRequired, staleAt)
	if err != nil {
		t.Fatal(err)
	}
	purgeLease, err := model.NewUploadLease(model.NewUploadLeaseID(), purgeRevision.ID, user.ID, staleAt, staleAt.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = persistence.File().CreateUpload(ctx, &store.FileUploadCreation{Entry: purgeEntry, Revision: purgeRevision, Lease: purgeLease}); err != nil {
		t.Fatal(err)
	}
	purgeRenditions, err := adapter.GenerateAndStoreDefaultProfilePicture(ctx, purgeRevision.ID, user.DefaultProfilePictureSeed, staleAt)
	if err != nil {
		t.Fatal(err)
	}

	state, err := persistence.File().GetProfilePictureState(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state.EntryID != referencedEntry.ID || state.RevisionID != referencedRevision.ID {
		t.Fatalf("pending content became visible through metadata: %#v", state)
	}
	page, err := persistence.File().ListPurgeCandidates(ctx, &store.FilePurgeCandidateRequest{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var candidate *store.FilePurgeCandidate
	for index := range page.Candidates {
		value := &page.Candidates[index]
		if value.RevisionID == referencedRevision.ID {
			t.Fatalf("referenced revision was selected for purge: %#v", value)
		}
		if value.RevisionID == purgeRevision.ID {
			candidate = value
		}
	}
	if candidate == nil {
		t.Fatalf("expired pending revision was not selected: %#v", page.Candidates)
	}
	claim, err := persistence.File().ClaimPurgeCandidate(ctx, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if err = adapter.RemoveFileRevisionContent(ctx, claim.Candidate.RevisionID, claim.Candidate.RenditionIDs); err != nil {
		t.Fatal(err)
	}
	if err = persistence.File().CompletePurge(ctx, claim); err != nil {
		t.Fatal(err)
	}

	visible, err := persistence.File().GetProfilePictureRendition(ctx, published.User.ID, "profile_128")
	if err != nil {
		t.Fatalf("referenced metadata disappeared: %v", err)
	}
	body, err := adapter.OpenProfilePictureRendition(ctx, visible.RevisionID, visible.ID)
	if err != nil {
		t.Fatalf("referenced VFS rendition was deleted: %v", err)
	}
	_ = body.Close()
	for _, rendition := range purgeRenditions {
		if _, statErr := filesystem.Stat(ctx, profilePictureRenditionPath(purgeRevision.ID, rendition.ID)); !errors.Is(statErr, vfspkg.ErrNotFound) {
			t.Fatalf("purged rendition %s still exists: %v", rendition.ID, statErr)
		}
	}
	var remainingMetadata int
	if err = persistence.GetMaster().Get(ctx, &remainingMetadata, `SELECT count(*) FROM file_revisions WHERE id = $1`, purgeRevision.ID.String()); err != nil {
		t.Fatal(err)
	}
	if remainingMetadata != 0 {
		t.Fatalf("purged revision metadata count = %d, want 0", remainingMetadata)
	}
}

func openFilePurgeStorageIntegrationStore(t *testing.T) *sqlstore.SQLStore {
	t.Helper()
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	if dataSource == "" {
		t.Fatal("PROCTOR_TEST_DATABASE_URL is not set")
	}
	database := config.Default().Database
	database.DataSource = dataSource
	settings := sqlstore.SettingsFromConfig(database)
	migrator, err := sqlstore.NewMigrator(context.Background(), settings)
	if err != nil {
		t.Fatal(err)
	}
	if err = migrator.Up(); err != nil {
		_ = migrator.Close()
		t.Fatal(err)
	}
	if err = migrator.Close(); err != nil {
		t.Fatal(err)
	}
	persistence, err := sqlstore.New(context.Background(), settings)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := persistence.Close(); err != nil {
			t.Errorf("close file purge storage integration store: %v", err)
		}
	})
	if _, err = persistence.GetMaster().Exec(context.Background(), `TRUNCATE TABLE users, file_entries, job_permanent_occurrences, jobs CASCADE`); err != nil {
		t.Fatal(err)
	}
	return persistence
}

func environmentDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
