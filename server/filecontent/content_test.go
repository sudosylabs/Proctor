// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package filecontent

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/packages/vfs"
	"github.com/sudosylabs/proctor/packages/vfs/local"
	"github.com/sudosylabs/proctor/packages/vfs/memory"
	"github.com/sudosylabs/proctor/server/model"
)

func TestContentStoresAndOpensAnExactRenditionAtTheCompatiblePrivateKey(t *testing.T) {
	for _, backend := range contentTestBackends() {
		t.Run(backend.name, func(t *testing.T) {
			filesystem := backend.open(t)
			content, err := New(filesystem, Policy{MaximumConcurrentOperations: 2}, nil)
			require.NoError(t, err)
			revisionID := model.FileRevisionID("yyyyyyyyyyyyyyyyyyyyyyyyyy")
			renditionID := model.FileRenditionID("bbbbbbbbbbbbbbbbbbbbbbbbbb")
			body := []byte("normalized-webp")

			err = content.storeRendition(context.Background(), revisionID, renditionID, bytes.NewReader(body), int64(len(body)))
			require.NoError(t, err)
			opened, err := content.OpenProfilePictureRendition(context.Background(), revisionID, renditionID)
			require.NoError(t, err)
			actual, err := io.ReadAll(opened)
			_ = opened.Close()
			require.NoError(t, err)
			require.True(t, bytes.Equal(actual, body), "opened bytes = %q, want %q", actual, body)

			const compatibleKey = "files/yy/yy/revisions/yyyyyyyyyyyyyyyyyyyyyyyyyy/renditions/bbbbbbbbbbbbbbbbbbbbbbbbbb.webp"
			_, err = filesystem.Stat(context.Background(), compatibleKey)
			require.NoError(t, err)
		})
	}
}

func TestContentClassifiesStorageConflictsWithoutExposingPrivateKeys(t *testing.T) {
	t.Parallel()

	content, err := New(memory.New(), Policy{MaximumConcurrentOperations: 2}, nil)
	require.NoError(t, err)
	revisionID, renditionID := model.NewFileRevisionID(), model.NewFileRenditionID()
	err = content.storeRendition(context.Background(), revisionID, renditionID, bytes.NewReader([]byte("first")), 5)
	require.NoError(t, err)
	err = content.storeRendition(context.Background(), revisionID, renditionID, bytes.NewReader([]byte("other")), 5)
	require.True(t, IsConflict(err), "second stage error")
	require.NotContains(t, err.Error(), revisionID.String())
	require.NotContains(t, err.Error(), renditionID.String())
	require.NotContains(t, err.Error(), "files/")
}

func TestContentLeavesAnOversizedAbandonedRevisionUntouched(t *testing.T) {
	for _, backend := range contentTestBackends() {
		t.Run(backend.name, func(t *testing.T) {
			content, err := New(backend.open(t), Policy{MaximumConcurrentOperations: 2}, nil)
			require.NoError(t, err)
			revisionID := model.NewFileRevisionID()
			firstID := model.NewFileRenditionID()
			for index := 0; index < 101; index++ {
				renditionID := model.NewFileRenditionID()
				if index == 0 {
					firstID = renditionID
				}
				err = content.storeRendition(context.Background(), revisionID, renditionID, bytes.NewReader([]byte{byte(index)}), 1)
				require.NoError(t, err)
			}

			err = content.purgeAbandonedRevision(context.Background(), revisionID)
			require.ErrorIs(t, err, ErrPurgeLimit, "purge error")
			retained, err := content.OpenProfilePictureRendition(context.Background(), revisionID, firstID)
			require.NoError(t, err)
			_ = retained.Close()
		})
	}
}

func TestContentPurgesOnlyOneAbandonedRevisionPrefixIdempotently(t *testing.T) {
	for _, backend := range contentTestBackends() {
		t.Run(backend.name, func(t *testing.T) {
			content, err := New(backend.open(t), Policy{MaximumConcurrentOperations: 2}, nil)
			require.NoError(t, err)
			abandonedRevisionID, retainedRevisionID := model.NewFileRevisionID(), model.NewFileRevisionID()
			abandonedIDs := []model.FileRenditionID{model.NewFileRenditionID(), model.NewFileRenditionID()}
			retainedID := model.NewFileRenditionID()
			for _, renditionID := range abandonedIDs {
				err = content.storeRendition(context.Background(), abandonedRevisionID, renditionID, bytes.NewReader([]byte("partial")), 7)
				require.NoError(t, err)
			}
			err = content.storeRendition(context.Background(), retainedRevisionID, retainedID, bytes.NewReader([]byte("retained")), 8)
			require.NoError(t, err)

			err = content.purgeAbandonedRevision(context.Background(), abandonedRevisionID)
			require.NoError(t, err)
			err = content.purgeAbandonedRevision(context.Background(), abandonedRevisionID)
			require.NoError(t, err)
			retained, err := content.OpenProfilePictureRendition(context.Background(), retainedRevisionID, retainedID)
			require.NoError(t, err)
			_ = retained.Close()
		})
	}
}

func TestContentRemovesAKnownRenditionManifestIdempotently(t *testing.T) {
	for _, backend := range contentTestBackends() {
		t.Run(backend.name, func(t *testing.T) {
			content, err := New(backend.open(t), Policy{MaximumConcurrentOperations: 2}, nil)
			require.NoError(t, err)
			revisionID := model.NewFileRevisionID()
			firstID, secondID, retainedID := model.NewFileRenditionID(), model.NewFileRenditionID(), model.NewFileRenditionID()
			for _, renditionID := range []model.FileRenditionID{firstID, secondID, retainedID} {
				err = content.storeRendition(context.Background(), revisionID, renditionID, bytes.NewReader([]byte("content")), 7)
				require.NoError(t, err, "stage %s", renditionID)
			}

			manifest := []model.FileRenditionID{firstID, secondID}
			err = content.removeRenditions(context.Background(), revisionID, manifest)
			require.NoError(t, err)
			err = content.removeRenditions(context.Background(), revisionID, manifest)
			require.NoError(t, err)
			_, err = content.OpenProfilePictureRendition(context.Background(), revisionID, firstID)
			require.True(t, IsNotFound(err), "removed rendition error")
			retained, err := content.OpenProfilePictureRendition(context.Background(), revisionID, retainedID)
			require.NoError(t, err)
			_ = retained.Close()
		})
	}
}

func TestContentRetriesAKnownManifestAfterAPartialBackendFailure(t *testing.T) {
	for _, backend := range contentTestBackends() {
		t.Run(backend.name, func(t *testing.T) {
			filesystem := &removeFailureVFS{FileSystem: backend.open(t), failOnCall: 2}
			content, err := New(filesystem, Policy{MaximumConcurrentOperations: 2}, nil)
			require.NoError(t, err)
			revisionID := model.NewFileRevisionID()
			firstID, secondID := model.NewFileRenditionID(), model.NewFileRenditionID()
			for _, renditionID := range []model.FileRenditionID{firstID, secondID} {
				err = content.storeRendition(context.Background(), revisionID, renditionID, bytes.NewReader([]byte("content")), 7)
				require.NoError(t, err)
			}

			err = content.removeRenditions(context.Background(), revisionID, []model.FileRenditionID{firstID, secondID})
			require.True(t, IsUnavailable(err), "partial removal error")
			filesystem.failOnCall = 0
			err = content.removeRenditions(context.Background(), revisionID, []model.FileRenditionID{firstID, secondID})
			require.NoError(t, err)
			for _, renditionID := range []model.FileRenditionID{firstID, secondID} {
				_, err = content.OpenProfilePictureRendition(context.Background(), revisionID, renditionID)
				require.True(t, IsNotFound(err), "rendition %s survived retry", renditionID)
			}
		})
	}
}

type removeFailureVFS struct {
	vfs.FileSystem
	failOnCall int
	removeCall int
}

func (f *removeFailureVFS) Remove(ctx context.Context, path string, options vfs.RemoveOptions) error {
	f.removeCall++
	if f.failOnCall > 0 && f.removeCall == f.failOnCall {
		return errors.New("backend unavailable")
	}
	return f.FileSystem.Remove(ctx, path, options)
}

func contentTestBackends() []struct {
	name string
	open func(*testing.T) vfs.FileSystem
} {
	return []struct {
		name string
		open func(*testing.T) vfs.FileSystem
	}{
		{name: "memory", open: func(*testing.T) vfs.FileSystem { return memory.New() }},
		{name: "local", open: func(t *testing.T) vfs.FileSystem {
			filesystem, err := local.New(filepath.Join(t.TempDir(), "vfs"))
			require.NoError(t, err)
			return filesystem
		}},
	}
}
