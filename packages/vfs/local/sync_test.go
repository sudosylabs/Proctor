package local

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/packages/vfs"
)

// recordSyncs makes filesystem record, relative to its root, every directory
// it syncs.
func recordSyncs(t *testing.T, filesystem *FS) *[]string {
	t.Helper()
	synced := &[]string{}
	filesystem.syncDirectory = func(path string) error {
		relative, err := filepath.Rel(filesystem.root, path)
		require.NoError(t, err)
		*synced = append(*synced, filepath.ToSlash(relative))
		return syncDirectory(path)
	}
	return synced
}

func newSyncFilesystem(t *testing.T) (*FS, *[]string) {
	t.Helper()
	filesystem, err := New(t.TempDir())
	require.NoError(t, err)
	return filesystem, recordSyncs(t, filesystem)
}

// A rename, link or removal is durable only once its directory is synced; a
// crash could otherwise lose a published file or resurrect a removed one.
func TestDirectoryEntriesAreSyncedAfterEachMutation(t *testing.T) {
	ctx := context.Background()

	t.Run("write creates and syncs each new directory", func(t *testing.T) {
		filesystem, synced := newSyncFilesystem(t)
		_, err := filesystem.Write(ctx, "a/b/file.txt", strings.NewReader("data"), vfs.WriteOptions{})
		require.NoError(t, err)
		require.Equal(t, []string{".", "a", "a/b"}, *synced)
	})

	t.Run("no-overwrite write publishes by link", func(t *testing.T) {
		filesystem, synced := newSyncFilesystem(t)
		_, err := filesystem.Write(ctx, "file.txt", strings.NewReader("data"), vfs.WriteOptions{NoOverwrite: true})
		require.NoError(t, err)
		require.Equal(t, []string{"."}, *synced)
	})

	t.Run("copy", func(t *testing.T) {
		filesystem, synced := newSyncFilesystem(t)
		_, err := filesystem.Write(ctx, "source.txt", strings.NewReader("data"), vfs.WriteOptions{})
		require.NoError(t, err)
		*synced = nil
		_, err = filesystem.Copy(ctx, "source.txt", "copies/copy.txt", vfs.TransferOptions{})
		require.NoError(t, err)
		require.Equal(t, []string{".", "copies"}, *synced)
	})

	for _, noOverwrite := range []bool{false, true} {
		name := "move by rename"
		if noOverwrite {
			name = "move by link"
		}
		t.Run(name, func(t *testing.T) {
			filesystem, synced := newSyncFilesystem(t)
			_, err := filesystem.Write(ctx, "from/source.txt", strings.NewReader("data"), vfs.WriteOptions{})
			require.NoError(t, err)
			_, err = filesystem.Write(ctx, "to/keep.txt", strings.NewReader("data"), vfs.WriteOptions{})
			require.NoError(t, err)
			*synced = nil
			_, err = filesystem.Move(ctx, "from/source.txt", "to/moved.txt", vfs.TransferOptions{NoOverwrite: noOverwrite})
			require.NoError(t, err)
			require.Equal(t, []string{"to", "from"}, *synced)
		})
	}

	t.Run("remove", func(t *testing.T) {
		filesystem, synced := newSyncFilesystem(t)
		_, err := filesystem.Write(ctx, "dir/file.txt", strings.NewReader("data"), vfs.WriteOptions{})
		require.NoError(t, err)
		*synced = nil
		require.NoError(t, filesystem.Remove(ctx, "dir/file.txt", vfs.RemoveOptions{}))
		require.Equal(t, []string{"dir"}, *synced)
	})
}

func TestDirectorySyncFailureIsReported(t *testing.T) {
	filesystem, err := New(t.TempDir())
	require.NoError(t, err)
	failure := errors.New("sync failed")
	filesystem.syncDirectory = func(string) error { return failure }

	_, err = filesystem.Write(context.Background(), "file.txt", strings.NewReader("data"), vfs.WriteOptions{})
	require.ErrorIs(t, err, failure)
	require.Empty(t, filesystem.staging, "a failed sync leaves no staging registration")
}
