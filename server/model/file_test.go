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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

func TestProfilePictureFileModelsPreserveIdentityAndAvailability(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, time.August, 9, 12, 0, 0, 0, time.UTC)
	entry, err := model.NewFileEntry(model.NewFileEntryID(), model.FileIndexingNone, at)
	require.NoError(t, err)
	revision, err := model.NewFileRevision(model.NewFileRevisionID(), entry.ID, model.FileAvailabilityPending, model.FileIndexingNotRequired, at)
	require.NoError(t, err)
	rendition, err := model.NewFileRendition(model.NewFileRenditionID(), revision.ID, "profile_128", "image/webp", 1234, 128, 128, strings.Repeat("a", 64), at)
	require.NoError(t, err)
	require.Equal(t, revision.ID, rendition.RevisionID)
	require.Equal(t, 128, rendition.Width)
	require.Equal(t, 128, rendition.Height)

	available, err := revision.MakeAvailable([]model.FileRendition{*rendition})
	require.NoError(t, err)
	require.Equal(t, model.FileAvailabilityAvailable, available.Availability)
	require.Len(t, available.Renditions, 1)
	require.Equal(t, model.FileAvailabilityPending, revision.Availability, "MakeAvailable() mutated the immutable source revision")
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
	require.Error(t, err, "NewFileRevision() accepted an available revision without renditions")
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
	require.NoError(t, err)
	require.Equal(t, model.FilePurposeExamResource, entry.Purpose)

	revisionID := model.NewFileRevisionID()
	rendering, err := model.NewFileRendition(
		model.NewFileRenditionID(), revisionID, "original", "application/pdf",
		1024, 0, 0, strings.Repeat("a", 64), at,
	)
	require.NoError(t, err)
	require.Equal(t, 0, rendering.Width, "non-image dimensions")
	require.Equal(t, 0, rendering.Height, "non-image dimensions")

	_, err = model.NewFileRendition(
		model.NewFileRenditionID(), revisionID, "invalid", "application/pdf",
		1024, 1, 0, strings.Repeat("a", 64), at,
	)
	require.Error(t, err, "NewFileRendition() accepted only one zero dimension")
}

func TestWorkspaceContentVersionIsOpaqueValidatedScalar(t *testing.T) {
	t.Parallel()

	generated := model.NewWorkspaceContentVersion()
	require.True(t, generated.IsValid())
	const raw = "Abcdefghijklmnopqrstu_1234"
	version, err := model.ParseWorkspaceContentVersion(raw)
	require.NoError(t, err)
	require.Equal(t, raw, version.String())
	require.True(t, version.IsValid())
	for _, invalid := range []string{"", "short", "abcdefghijklmnopqrstuvwxyz0", "abcdefghijklmnopqrstuvwx+!"} {
		_, err := model.ParseWorkspaceContentVersion(invalid)
		assert.Error(t, err, "ParseWorkspaceContentVersion(%q) succeeded", invalid)
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
	require.NoError(t, err)
	_, err = model.NewUploadLease(model.NewUploadLeaseID(), model.NewFileRevisionID(), model.NewUserID(), at, at.Add(59*time.Minute))
	require.Error(t, err, "NewUploadLease() accepted a nonstandard initial lifetime")
	renewed, err := lease.Renew(at.Add(30*time.Minute), at.Add(90*time.Minute), 1)
	require.NoError(t, err)
	require.True(t, renewed.ExpiresAt.Equal(at.Add(90*time.Minute)))
	consumed, err := renewed.Consume(at.Add(31 * time.Minute))
	require.NoError(t, err)
	require.True(t, consumed.ConsumedAt.IsSet(), "ConsumedAt is not set")
	_, err = consumed.Renew(at.Add(32*time.Minute), at.Add(2*time.Hour), 2)
	require.Error(t, err, "Renew() succeeded for consumed lease")
	_, err = lease.Renew(at.Add(30*time.Minute), at.Add(91*time.Minute), 1)
	require.Error(t, err, "Renew() accepted an expiry beyond the maximum renewal horizon")
}

func TestUserProfilePictureStateUsesExplicitReferences(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, time.August, 9, 12, 0, 0, 0, time.UTC)
	user := model.User{Username: "student", Email: "student@example.test"}
	user.PrepareCreate(model.NewUserID(), at)
	require.Len(t, user.DefaultProfilePictureSeed, model.ProfilePictureSeedLength, "default picture seed length")
	require.True(t, user.DefaultProfilePictureFileID.IsZero())
	require.True(t, user.CustomProfilePictureFileID.IsZero())
	require.False(t, user.ProfilePictureChangedAt.IsSet())
	require.NoError(t, user.Validate())
}
