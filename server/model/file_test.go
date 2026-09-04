// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model_test

import (
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

func TestProfilePictureFileModelsPreserveIdentityAndAvailability(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, time.August, 9, 12, 0, 0, 0, time.UTC)
	entry, err := model.NewFileEntry(model.NewFileEntryID(), model.FileIndexingNone, at)
	if err != nil {
		t.Fatalf("NewFileEntry() error = %v", err)
	}
	revision, err := model.NewFileRevision(model.NewFileRevisionID(), entry.ID, model.FileAvailabilityPending, model.FileIndexingNotRequired, at)
	if err != nil {
		t.Fatalf("NewFileRevision() error = %v", err)
	}
	rendition, err := model.NewFileRendition(model.NewFileRenditionID(), revision.ID, "profile_128", "image/webp", 1234, 128, 128, strings.Repeat("a", 64), at)
	if err != nil {
		t.Fatalf("NewFileRendition() error = %v", err)
	}
	if rendition.RevisionID != revision.ID || rendition.Width != 128 || rendition.Height != 128 {
		t.Fatalf("rendition = %#v", rendition)
	}

	available, err := revision.MakeAvailable([]model.FileRendition{*rendition})
	if err != nil {
		t.Fatalf("MakeAvailable() error = %v", err)
	}
	if available.Availability != model.FileAvailabilityAvailable || len(available.Renditions) != 1 {
		t.Fatalf("available revision = %#v", available)
	}
	if revision.Availability != model.FileAvailabilityPending {
		t.Fatal("MakeAvailable() mutated the immutable source revision")
	}
}

func TestFileRevisionCannotStartAvailableWithoutRenditions(t *testing.T) {
	t.Parallel()

	_, err := model.NewFileRevision(
		model.NewFileRevisionID(),
		model.NewFileEntryID(),
		model.FileAvailabilityAvailable,
		model.FileIndexingNotRequired,
		time.Now(),
	)
	if err == nil {
		t.Fatal("NewFileRevision() accepted an available revision without renditions")
	}
}

func TestExamResourceFilePurposeAndNonImageRendition(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, time.August, 14, 12, 0, 0, 0, time.UTC)
	entry, err := model.NewFileEntryForPurpose(
		model.NewFileEntryID(),
		model.FilePurposeExamResource,
		model.FileIndexingNone,
		at,
	)
	if err != nil {
		t.Fatalf("NewFileEntryForPurpose() error = %v", err)
	}
	if entry.Purpose != model.FilePurposeExamResource {
		t.Fatalf("Purpose = %q", entry.Purpose)
	}

	revisionID := model.NewFileRevisionID()
	rendering, err := model.NewFileRendition(
		model.NewFileRenditionID(), revisionID, "original", "application/pdf",
		1024, 0, 0, strings.Repeat("a", 64), at,
	)
	if err != nil {
		t.Fatalf("NewFileRendition() non-image error = %v", err)
	}
	if rendering.Width != 0 || rendering.Height != 0 {
		t.Fatalf("non-image dimensions = %dx%d", rendering.Width, rendering.Height)
	}

	if _, err := model.NewFileRendition(
		model.NewFileRenditionID(), revisionID, "invalid", "application/pdf",
		1024, 1, 0, strings.Repeat("a", 64), at,
	); err == nil {
		t.Fatal("NewFileRendition() accepted only one zero dimension")
	}
}

func TestWorkspaceContentVersionIsOpaqueValidatedScalar(t *testing.T) {
	t.Parallel()

	generated := model.NewWorkspaceContentVersion()
	if !generated.IsValid() {
		t.Fatalf("NewWorkspaceContentVersion() = %q", generated)
	}
	const raw = "Abcdefghijklmnopqrstu_1234"
	version, err := model.ParseWorkspaceContentVersion(raw)
	if err != nil {
		t.Fatalf("ParseWorkspaceContentVersion() error = %v", err)
	}
	if version.String() != raw || !version.IsValid() {
		t.Fatalf("version = %q", version)
	}
	for _, invalid := range []string{"", "short", "abcdefghijklmnopqrstuvwxyz0", "abcdefghijklmnopqrstuvwx+!"} {
		if _, err := model.ParseWorkspaceContentVersion(invalid); err == nil {
			t.Errorf("ParseWorkspaceContentVersion(%q) succeeded", invalid)
		}
	}
}

func TestUploadLeaseRenewsAndConsumesWithoutSentinels(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, time.August, 9, 12, 0, 0, 0, time.UTC)
	lease, err := model.NewUploadLease(
		model.NewUploadLeaseID(),
		model.NewFileRevisionID(),
		model.NewUserID(),
		at,
		at.Add(time.Hour),
	)
	if err != nil {
		t.Fatalf("NewUploadLease() error = %v", err)
	}
	if _, err := model.NewUploadLease(model.NewUploadLeaseID(), model.NewFileRevisionID(), model.NewUserID(), at, at.Add(59*time.Minute)); err == nil {
		t.Fatal("NewUploadLease() accepted a nonstandard initial lifetime")
	}
	renewed, err := lease.Renew(at.Add(30*time.Minute), at.Add(90*time.Minute), 1)
	if err != nil {
		t.Fatalf("Renew() error = %v", err)
	}
	if !renewed.ExpiresAt.Equal(at.Add(90 * time.Minute)) {
		t.Fatalf("ExpiresAt = %v", renewed.ExpiresAt)
	}
	consumed, err := renewed.Consume(at.Add(31 * time.Minute))
	if err != nil {
		t.Fatalf("Consume() error = %v", err)
	}
	if !consumed.ConsumedAt.IsSet() {
		t.Fatal("ConsumedAt is not set")
	}
	if _, err := consumed.Renew(at.Add(32*time.Minute), at.Add(2*time.Hour), 2); err == nil {
		t.Fatal("Renew() succeeded for consumed lease")
	}
	if _, err := lease.Renew(at.Add(30*time.Minute), at.Add(91*time.Minute), 1); err == nil {
		t.Fatal("Renew() accepted an expiry beyond the maximum renewal horizon")
	}
}

func TestUserProfilePictureStateUsesExplicitReferences(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, time.August, 9, 12, 0, 0, 0, time.UTC)
	user := model.User{Username: "student", Email: "student@example.test"}
	user.PrepareCreate(model.NewUserID(), at)
	if len(user.DefaultProfilePictureSeed) != model.ProfilePictureSeedLength {
		t.Fatalf("default picture seed length = %d", len(user.DefaultProfilePictureSeed))
	}
	if !user.DefaultProfilePictureFileID.IsZero() || !user.CustomProfilePictureFileID.IsZero() || user.ProfilePictureChangedAt.IsSet() {
		t.Fatalf("new user picture state = %#v", user)
	}
	if err := user.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}
