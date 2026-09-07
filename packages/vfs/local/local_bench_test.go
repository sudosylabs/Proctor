package local_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
			if _, err := filesystem.Write(b.Context(), "existing.txt", strings.NewReader("existing"), vfs.WriteOptions{}); err != nil {
				b.Fatal(err)
			}
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
				if err != nil || writeErr != nil {
					b.Fatalf("operation %v, upload %v", err, writeErr)
				}
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
				if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
					b.Fatal(err)
				}
				if err := os.WriteFile(full, []byte("content"), 0o640); err != nil {
					b.Fatal(err)
				}
			}
			filesystem, err := local.New(root)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				page, err := filesystem.List(b.Context(), vfs.ListOptions{Prefix: "target/", Limit: 10})
				if err != nil || len(page.Entries) != 10 || page.NextCursor == "" {
					b.Fatalf("page = %#v, error = %v", page, err)
				}
			}
		})
	}
}
