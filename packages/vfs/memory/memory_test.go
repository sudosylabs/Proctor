package memory_test

import (
	"testing"

	"github.com/sudosylabs/proctor/packages/vfs"
	"github.com/sudosylabs/proctor/packages/vfs/memory"
	"github.com/sudosylabs/proctor/packages/vfs/vfstest"
)

func TestConformance(t *testing.T) {
	vfstest.Run(t, func(t *testing.T) vfs.FileSystem {
		t.Helper()
		return memory.New()
	})
}
