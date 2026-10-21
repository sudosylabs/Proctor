package local_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/packages/vfs"
	"github.com/sudosylabs/proctor/packages/vfs/local"
)

// This benchmark measures the unrelated operation, excluding upload setup and
// completion. A controlled 20 ms upload pause makes lock interference visible;
// it does not model production upload throughput or a candidate workload.
func BenchmarkReadDuringPausedWrite(b *testing.B) {
	for _, operation := range []string{"open", "list"} {
		b.Run(operation, func(b *testing.B) {
			filesystem := newFilesystem(b)
			_, err := filesystem.Write(b.Context(), "existing.txt", strings.NewReader("existing"), vfs.WriteOptions{})
			require.NoError(b, err)
			b.ReportAllocs()
			for b.Loop() {
				b.StopTimer()
				reader := newPausedReader()
				written := make(chan error, 1)
				go func() {
					_, err := filesystem.Write(b.Context(), "upload.txt", reader, vfs.WriteOptions{})
					written <- err
				}()
				<-reader.paused
				timer := time.AfterFunc(20*time.Millisecond, reader.resume)
				b.StartTimer()
				var err error
				if operation == "open" {
					err = independentOperation(b.Context(), filesystem, operation)
				} else {
					_, err = filesystem.List(b.Context(), vfs.ListOptions{Prefix: "existing"})
				}
				b.StopTimer()
				timer.Stop()
				reader.resume()
				writeErr := <-written
				require.NoError(b, err, "operation")
				require.NoError(b, writeErr, "operation")
				b.StartTimer()
			}
		})
	}
}

func BenchmarkListPrefix(b *testing.B) {
	for _, unrelated := range []int{0, 2000} {
		b.Run(fmt.Sprintf("unrelated=%d", unrelated), func(b *testing.B) {
			root := b.TempDir()
			for i := range 20 + unrelated {
				name := fmt.Sprintf("target/%04d.txt", i)
				if i >= 20 {
					name = fmt.Sprintf("other/%03d/%04d.txt", i/20, i)
				}
				full := filepath.Join(root, filepath.FromSlash(name))
				require.NoError(b, os.MkdirAll(filepath.Dir(full), 0o750))
				require.NoError(b, os.WriteFile(full, []byte("content"), 0o600))
			}
			filesystem, err := local.New(root)
			require.NoError(b, err)
			b.ReportAllocs()
			for b.Loop() {
				page, err := filesystem.List(b.Context(), vfs.ListOptions{Prefix: "target/", Limit: 10})
				require.NoError(b, err, "page = %#v, error", page)
				require.Len(b, page.Entries, 10, "page = %#v, error", page)
				require.NotEmpty(b, page.NextCursor, "page = %#v, error", page)
			}
		})
	}
}

// A first page costs a few directory reads however large the tree is; the
// whole-tree walk this replaced grew linearly with the file count.
func BenchmarkListFirstPage(b *testing.B) {
	for _, files := range []int{100, 20000} {
		b.Run(fmt.Sprintf("files=%d", files), func(b *testing.B) {
			root := b.TempDir()
			for i := range files {
				full := filepath.Join(root, fmt.Sprintf("%03d", i/100), fmt.Sprintf("%03d.txt", i%100))
				require.NoError(b, os.MkdirAll(filepath.Dir(full), 0o750))
				require.NoError(b, os.WriteFile(full, []byte("content"), 0o600))
			}
			filesystem, err := local.New(root)
			require.NoError(b, err)
			b.ReportAllocs()
			for b.Loop() {
				page, err := filesystem.List(b.Context(), vfs.ListOptions{Limit: 1})
				require.NoError(b, err)
				require.Len(b, page.Entries, 1)
			}
		})
	}
}
