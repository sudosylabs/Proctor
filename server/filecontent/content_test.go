// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package filecontent_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	vfspkg "github.com/sudosylabs/proctor/packages/vfs"
	localvfs "github.com/sudosylabs/proctor/packages/vfs/local"
	memoryvfs "github.com/sudosylabs/proctor/packages/vfs/memory"
	"github.com/sudosylabs/proctor/server/filecontent"
	"github.com/sudosylabs/proctor/server/model"
)

func TestContentStoresAndOpensAnExactRenditionAtTheCompatiblePrivateKey(t *testing.T) {
	for _, backend := range contentTestBackends() {
		t.Run(backend.name, func(t *testing.T) {
			filesystem := backend.open(t)
			content, err := filecontent.New(filesystem)
			if err != nil {
				t.Fatal(err)
			}
			revisionID := model.FileRevisionID("yyyyyyyyyyyyyyyyyyyyyyyyyy")
			renditionID := model.FileRenditionID("bbbbbbbbbbbbbbbbbbbbbbbbbb")
			body := []byte("normalized-webp")

			if err = content.StageProfilePictureRendition(context.Background(), revisionID, renditionID, bytes.NewReader(body), int64(len(body))); err != nil {
				t.Fatalf("stage rendition: %v", err)
			}
			opened, err := content.OpenRendition(context.Background(), revisionID, renditionID)
			if err != nil {
				t.Fatalf("open rendition: %v", err)
			}
			actual, err := io.ReadAll(opened)
			_ = opened.Close()
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(actual, body) {
				t.Fatalf("opened bytes = %q, want %q", actual, body)
			}

			const compatibleKey = "files/yy/yy/revisions/yyyyyyyyyyyyyyyyyyyyyyyyyy/renditions/bbbbbbbbbbbbbbbbbbbbbbbbbb.webp"
			if _, err = filesystem.Stat(context.Background(), compatibleKey); err != nil {
				t.Fatalf("compatible private key missing: %v", err)
			}
		})
	}
}

func TestContentClassifiesStorageConflictsWithoutExposingPrivateKeys(t *testing.T) {
	t.Parallel()

	content, err := filecontent.New(memoryvfs.New())
	if err != nil {
		t.Fatal(err)
	}
	revisionID, renditionID := model.NewFileRevisionID(), model.NewFileRenditionID()
	if err = content.StageProfilePictureRendition(context.Background(), revisionID, renditionID, bytes.NewReader([]byte("first")), 5); err != nil {
		t.Fatal(err)
	}
	err = content.StageProfilePictureRendition(context.Background(), revisionID, renditionID, bytes.NewReader([]byte("other")), 5)
	if !filecontent.IsConflict(err) {
		t.Fatalf("second stage error = %v, want storage conflict", err)
	}
	if strings.Contains(err.Error(), revisionID.String()) || strings.Contains(err.Error(), renditionID.String()) || strings.Contains(err.Error(), "files/") {
		t.Fatalf("error exposed private storage identity: %v", err)
	}
}

func TestContentLeavesAnOversizedAbandonedRevisionUntouched(t *testing.T) {
	for _, backend := range contentTestBackends() {
		t.Run(backend.name, func(t *testing.T) {
			content, err := filecontent.New(backend.open(t))
			if err != nil {
				t.Fatal(err)
			}
			revisionID := model.NewFileRevisionID()
			firstID := model.NewFileRenditionID()
			for index := 0; index < 101; index++ {
				renditionID := model.NewFileRenditionID()
				if index == 0 {
					firstID = renditionID
				}
				if err = content.StageProfilePictureRendition(context.Background(), revisionID, renditionID, bytes.NewReader([]byte{byte(index)}), 1); err != nil {
					t.Fatal(err)
				}
			}

			if err = content.PurgeAbandonedRevision(context.Background(), revisionID); !errors.Is(err, filecontent.ErrPurgeLimit) {
				t.Fatalf("purge error = %v, want ErrPurgeLimit", err)
			}
			retained, err := content.OpenRendition(context.Background(), revisionID, firstID)
			if err != nil {
				t.Fatalf("bounded rejection removed content: %v", err)
			}
			_ = retained.Close()
		})
	}
}

func TestContentPurgesOnlyOneAbandonedRevisionPrefixIdempotently(t *testing.T) {
	for _, backend := range contentTestBackends() {
		t.Run(backend.name, func(t *testing.T) {
			content, err := filecontent.New(backend.open(t))
			if err != nil {
				t.Fatal(err)
			}
			abandonedRevisionID, retainedRevisionID := model.NewFileRevisionID(), model.NewFileRevisionID()
			abandonedIDs := []model.FileRenditionID{model.NewFileRenditionID(), model.NewFileRenditionID()}
			retainedID := model.NewFileRenditionID()
			for _, renditionID := range abandonedIDs {
				if err = content.StageProfilePictureRendition(context.Background(), abandonedRevisionID, renditionID, bytes.NewReader([]byte("partial")), 7); err != nil {
					t.Fatal(err)
				}
			}
			if err = content.StageProfilePictureRendition(context.Background(), retainedRevisionID, retainedID, bytes.NewReader([]byte("retained")), 8); err != nil {
				t.Fatal(err)
			}

			if err = content.PurgeAbandonedRevision(context.Background(), abandonedRevisionID); err != nil {
				t.Fatalf("purge abandoned revision: %v", err)
			}
			if err = content.PurgeAbandonedRevision(context.Background(), abandonedRevisionID); err != nil {
				t.Fatalf("repeat abandoned revision purge: %v", err)
			}
			retained, err := content.OpenRendition(context.Background(), retainedRevisionID, retainedID)
			if err != nil {
				t.Fatalf("sibling revision was removed: %v", err)
			}
			_ = retained.Close()
		})
	}
}

func TestContentRemovesAKnownRenditionManifestIdempotently(t *testing.T) {
	for _, backend := range contentTestBackends() {
		t.Run(backend.name, func(t *testing.T) {
			content, err := filecontent.New(backend.open(t))
			if err != nil {
				t.Fatal(err)
			}
			revisionID := model.NewFileRevisionID()
			firstID, secondID, retainedID := model.NewFileRenditionID(), model.NewFileRenditionID(), model.NewFileRenditionID()
			for _, renditionID := range []model.FileRenditionID{firstID, secondID, retainedID} {
				if err = content.StageProfilePictureRendition(context.Background(), revisionID, renditionID, bytes.NewReader([]byte("content")), 7); err != nil {
					t.Fatalf("stage %s: %v", renditionID, err)
				}
			}

			manifest := []model.FileRenditionID{firstID, secondID}
			if err = content.RemoveRenditions(context.Background(), revisionID, manifest); err != nil {
				t.Fatalf("remove manifest: %v", err)
			}
			if err = content.RemoveRenditions(context.Background(), revisionID, manifest); err != nil {
				t.Fatalf("repeat manifest removal: %v", err)
			}
			if _, err = content.OpenRendition(context.Background(), revisionID, firstID); !filecontent.IsNotFound(err) {
				t.Fatalf("removed rendition error = %v", err)
			}
			retained, err := content.OpenRendition(context.Background(), revisionID, retainedID)
			if err != nil {
				t.Fatalf("retained rendition unavailable: %v", err)
			}
			_ = retained.Close()
		})
	}
}

func TestContentRetriesAKnownManifestAfterAPartialBackendFailure(t *testing.T) {
	for _, backend := range contentTestBackends() {
		t.Run(backend.name, func(t *testing.T) {
			filesystem := &removeFailureVFS{FileSystem: backend.open(t), failOnCall: 2}
			content, err := filecontent.New(filesystem)
			if err != nil {
				t.Fatal(err)
			}
			revisionID := model.NewFileRevisionID()
			firstID, secondID := model.NewFileRenditionID(), model.NewFileRenditionID()
			for _, renditionID := range []model.FileRenditionID{firstID, secondID} {
				if err = content.StageProfilePictureRendition(context.Background(), revisionID, renditionID, bytes.NewReader([]byte("content")), 7); err != nil {
					t.Fatal(err)
				}
			}

			err = content.RemoveRenditions(context.Background(), revisionID, []model.FileRenditionID{firstID, secondID})
			if !filecontent.IsUnavailable(err) {
				t.Fatalf("partial removal error = %v, want unavailable", err)
			}
			filesystem.failOnCall = 0
			if err = content.RemoveRenditions(context.Background(), revisionID, []model.FileRenditionID{firstID, secondID}); err != nil {
				t.Fatalf("retry removal: %v", err)
			}
			for _, renditionID := range []model.FileRenditionID{firstID, secondID} {
				if _, err = content.OpenRendition(context.Background(), revisionID, renditionID); !filecontent.IsNotFound(err) {
					t.Fatalf("rendition %s survived retry: %v", renditionID, err)
				}
			}
		})
	}
}

type removeFailureVFS struct {
	vfspkg.FileSystem
	failOnCall int
	removeCall int
}

func (f *removeFailureVFS) Remove(ctx context.Context, path string, options vfspkg.RemoveOptions) error {
	f.removeCall++
	if f.failOnCall > 0 && f.removeCall == f.failOnCall {
		return errors.New("backend unavailable")
	}
	return f.FileSystem.Remove(ctx, path, options)
}

func contentTestBackends() []struct {
	name string
	open func(*testing.T) vfspkg.FileSystem
} {
	return []struct {
		name string
		open func(*testing.T) vfspkg.FileSystem
	}{
		{name: "memory", open: func(*testing.T) vfspkg.FileSystem { return memoryvfs.New() }},
		{name: "local", open: func(t *testing.T) vfspkg.FileSystem {
			filesystem, err := localvfs.New(filepath.Join(t.TempDir(), "vfs"))
			if err != nil {
				t.Fatal(err)
			}
			return filesystem
		}},
	}
}
