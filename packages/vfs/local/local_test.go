package local_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sudosylabs/proctor/packages/vfs"
	"github.com/sudosylabs/proctor/packages/vfs/local"
	"github.com/sudosylabs/proctor/packages/vfs/vfstest"
)

func TestConformance(t *testing.T) {
	vfstest.Run(t, func(t *testing.T) vfs.FileSystem {
		t.Helper()
		filesystem, err := local.New(t.TempDir())
		if err != nil {
			t.Fatalf("new local filesystem: %v", err)
		}
		return filesystem
	})
}

func TestRejectsSymlinksBelowRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	filesystem, err := local.New(root)
	if err != nil {
		t.Fatalf("new local filesystem: %v", err)
	}
	_, err = filesystem.Stat(t.Context(), "escape/file.txt")
	if err == nil {
		t.Fatal("expected symlink path to be rejected")
	}
}
