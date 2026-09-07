package local_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

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

func TestPausedWriteAllowsUnrelatedOperations(t *testing.T) {
	for _, operation := range []string{"open", "stat", "list", "write"} {
		t.Run(operation, func(t *testing.T) {
			filesystem := newFilesystem(t)
			if _, err := filesystem.Write(t.Context(), "existing.txt", strings.NewReader("existing"), vfs.WriteOptions{}); err != nil {
				t.Fatal(err)
			}
			write := startPausedWrite(t, filesystem, t.Context(), "upload.txt", vfs.WriteOptions{})
			completed := make(chan error, 1)
			go func() { completed <- independentOperation(t.Context(), filesystem, operation) }()
			select {
			case err := <-completed:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(2 * time.Second):
				write.reader.resume()
				<-completed
				t.Fatal("unrelated operation waited for the paused upload")
			}
			write.reader.resume()
			if err := write.wait(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestStagingIsInvisibleThroughFilesystem(t *testing.T) {
	root := t.TempDir()
	filesystem, err := local.New(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"existing.txt", "upload/.vfs-visible"} {
		if _, err := filesystem.Write(t.Context(), name, strings.NewReader("existing"), vfs.WriteOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	write := startPausedWrite(t, filesystem, t.Context(), "upload/final.txt", vfs.WriteOptions{})
	entries, err := os.ReadDir(filepath.Join(root, "upload"))
	if err != nil {
		t.Fatal(err)
	}
	var staged string
	for _, entry := range entries {
		if entry.Name() != ".vfs-visible" {
			if staged != "" {
				t.Fatal("expected one temporary object")
			}
			staged = "upload/" + entry.Name()
		}
	}
	if staged == "" {
		t.Fatal("paused write did not stage bytes")
	}
	operations := []struct {
		name string
		run  func(string) error
	}{
		{"open", func(name string) error {
			file, err := filesystem.Open(t.Context(), name, vfs.OpenOptions{})
			if file != nil {
				_ = file.Body.Close()
			}
			return err
		}},
		{"stat", func(name string) error { _, err := filesystem.Stat(t.Context(), name); return err }},
		{"remove", func(name string) error { return filesystem.Remove(t.Context(), name, vfs.RemoveOptions{}) }},
		{"write", func(name string) error {
			_, err := filesystem.Write(t.Context(), name, strings.NewReader("replace staging"), vfs.WriteOptions{})
			return err
		}},
		{"copy source", func(name string) error {
			_, err := filesystem.Copy(t.Context(), name, "copied.txt", vfs.TransferOptions{})
			return err
		}},
		{"copy destination", func(name string) error {
			_, err := filesystem.Copy(t.Context(), "existing.txt", name, vfs.TransferOptions{})
			return err
		}},
		{"move source", func(name string) error {
			_, err := filesystem.Move(t.Context(), name, "moved.txt", vfs.TransferOptions{})
			return err
		}},
		{"move destination", func(name string) error {
			_, err := filesystem.Move(t.Context(), "existing.txt", name, vfs.TransferOptions{})
			return err
		}},
	}
	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			if err := operation.run(staged); !errors.Is(err, vfs.ErrNotFound) {
				t.Fatalf("staging access error = %v, want ErrNotFound", err)
			}
		})
	}
	t.Run("filesystem case alias", func(t *testing.T) {
		alias := strings.ToUpper(staged)
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(alias))); errors.Is(err, os.ErrNotExist) {
			t.Skip("filesystem does not provide case aliases")
		} else if err != nil {
			t.Fatal(err)
		}
		for _, operation := range operations {
			if err := operation.run(alias); !errors.Is(err, vfs.ErrNotFound) {
				t.Errorf("staging %s through case alias: %v", operation.name, err)
			}
		}
	})
	for _, list := range []struct {
		options vfs.ListOptions
		want    []string
	}{
		{vfs.ListOptions{}, []string{"existing.txt", "upload/.vfs-visible"}},
		{vfs.ListOptions{Prefix: "upload/", Delimiter: "/"}, []string{"upload/.vfs-visible"}},
		{vfs.ListOptions{Prefix: staged}, []string{}},
	} {
		assertListing(t, filesystem, list.options, list.want)
	}
	write.reader.resume()
	if err := write.wait(); err != nil {
		t.Fatal(err)
	}
	assertContent(t, filesystem, "upload/final.txt", "partial complete")
	assertContent(t, filesystem, "existing.txt", "existing")
	assertListing(t, filesystem, vfs.ListOptions{}, []string{"existing.txt", "upload/.vfs-visible", "upload/final.txt"})
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(staged))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary file remains after publication: %v", err)
	}
}

func TestStagingFromAliasedDirectoryIsInvisible(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "directory"), 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "DIRECTORY")); errors.Is(err, os.ErrNotExist) {
		t.Skip("filesystem does not provide case aliases")
	} else if err != nil {
		t.Fatal(err)
	}
	filesystem, err := local.New(root)
	if err != nil {
		t.Fatal(err)
	}
	write := startPausedWrite(t, filesystem, t.Context(), "DIRECTORY/new.txt", vfs.WriteOptions{})
	assertListing(t, filesystem, vfs.ListOptions{}, []string{})
	assertListing(t, filesystem, vfs.ListOptions{Prefix: "directory/"}, []string{})
	write.reader.resume()
	if err := write.wait(); err != nil {
		t.Fatal(err)
	}
	assertListing(t, filesystem, vfs.ListOptions{Prefix: "directory/"}, []string{"directory/new.txt"})
	assertListing(t, filesystem, vfs.ListOptions{Prefix: "DIRECTORY/"}, []string{})
}

func TestWriteRechecksPublicationConditions(t *testing.T) {
	for _, condition := range []string{"revision changed", "revision removed", "no overwrite"} {
		t.Run(condition, func(t *testing.T) {
			filesystem := newFilesystem(t)
			options := vfs.WriteOptions{NoOverwrite: true}
			if condition != "no overwrite" {
				first, err := filesystem.Write(t.Context(), "target.txt", strings.NewReader("first"), vfs.WriteOptions{})
				if err != nil {
					t.Fatal(err)
				}
				options = vfs.WriteOptions{ExpectedRevision: first.Revision}
			}
			write := startPausedWrite(t, filesystem, t.Context(), "target.txt", options)
			if condition == "revision removed" {
				if err := filesystem.Remove(t.Context(), "target.txt", vfs.RemoveOptions{}); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := filesystem.Write(t.Context(), "target.txt", strings.NewReader("concurrent winner"), vfs.WriteOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			write.reader.resume()
			wantErr := vfs.ErrConflict
			if condition == "no overwrite" {
				wantErr = vfs.ErrAlreadyExists
			}
			if err := write.wait(); !errors.Is(err, wantErr) {
				t.Fatalf("write error = %v, want %v", err, wantErr)
			}
			if condition == "revision removed" {
				assertListing(t, filesystem, vfs.ListOptions{}, []string{})
			} else {
				assertContent(t, filesystem, "target.txt", "concurrent winner")
				assertListing(t, filesystem, vfs.ListOptions{}, []string{"target.txt"})
			}
		})
	}
}

func TestInterruptedWriteCleansStaging(t *testing.T) {
	for _, failure := range []string{"canceled at EOF", "size mismatch", "reader failure"} {
		t.Run(failure, func(t *testing.T) {
			root := t.TempDir()
			filesystem, err := local.New(root)
			if err != nil {
				t.Fatal(err)
			}
			first, err := filesystem.Write(t.Context(), "target.txt", strings.NewReader("original"), vfs.WriteOptions{})
			if err != nil {
				t.Fatal(err)
			}
			options := vfs.WriteOptions{ExpectedRevision: first.Revision}
			if failure == "reader failure" {
				failureErr := errors.New("reader stopped")
				_, err = filesystem.Write(t.Context(), "target.txt", io.MultiReader(strings.NewReader("partial"), failingReader{failureErr}), options)
				if !errors.Is(err, failureErr) {
					t.Fatalf("write error = %v, want reader failure", err)
				}
			} else {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				if failure == "size mismatch" {
					size := int64(1)
					options.Size = &size
				}
				write := startPausedWrite(t, filesystem, ctx, "target.txt", options)
				assertContent(t, filesystem, "target.txt", "original")
				if failure == "canceled at EOF" {
					cancel()
				}
				write.reader.resume()
				err = write.wait()
				if err == nil || failure == "canceled at EOF" && !errors.Is(err, context.Canceled) {
					t.Fatalf("write error = %v, want %s", err, failure)
				}
			}
			assertContent(t, filesystem, "target.txt", "original")
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 1 || entries[0].Name() != "target.txt" {
				t.Fatalf("unfinished file remained on disk: %v, %v", entries, err)
			}
		})
	}
}

func TestWriteRejectsSymlinkIntroducedBeforePublication(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	filesystem, err := local.New(root)
	if err != nil {
		t.Fatal(err)
	}
	write := startPausedWrite(t, filesystem, t.Context(), "target.txt", vfs.WriteOptions{})
	if err := os.Symlink(outside, filepath.Join(root, "target.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	write.reader.resume()
	if err := write.wait(); !errors.Is(err, vfs.ErrInvalidPath) {
		t.Fatalf("publication error = %v, want ErrInvalidPath", err)
	}
	content, err := os.ReadFile(outside)
	if err != nil || string(content) != "outside" {
		t.Fatalf("outside file changed: %q, %v", content, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 || entries[0].Name() != "target.txt" {
		t.Fatalf("temporary file remained: %v, %v", entries, err)
	}
}

func TestListLexicalPrefixesAndPagination(t *testing.T) {
	filesystem := newFilesystem(t)
	for _, name := range []string{
		"alpha.txt", "alpha/one.txt", "alphabet/two.txt", "alpine/three.txt",
		"a/b/c.txt", "a/bb.txt", "a/bc/file.txt", "a/bc/deeper/z.txt", "other/hidden.txt",
	} {
		if _, err := filesystem.Write(t.Context(), name, strings.NewReader(name), vfs.WriteOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct {
		name    string
		options vfs.ListOptions
		want    []string
	}{
		{"partial directory", vfs.ListOptions{Prefix: "alph"}, []string{"alpha.txt", "alpha/one.txt", "alphabet/two.txt"}},
		{"partial delimited directory", vfs.ListOptions{Prefix: "alpha", Delimiter: "/"}, []string{"alpha.txt", "alpha/", "alphabet/"}},
		{"nested partial directory", vfs.ListOptions{Prefix: "a/b", Delimiter: "/"}, []string{"a/b/", "a/bb.txt", "a/bc/"}},
		{"exact directory", vfs.ListOptions{Prefix: "a/b/"}, []string{"a/b/c.txt"}},
		{"nested file prefix", vfs.ListOptions{Prefix: "a/bc/f"}, []string{"a/bc/file.txt"}},
		{"missing directory", vfs.ListOptions{Prefix: "missing/"}, []string{}},
		{"case-sensitive prefix", vfs.ListOptions{Prefix: "ALPHA/"}, []string{}},
		{"file is not directory", vfs.ListOptions{Prefix: "alpha.txt/"}, []string{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			assertListing(t, filesystem, test.options, test.want)
			test.options.Limit = 1
			var got []string
			for {
				page, err := filesystem.List(t.Context(), test.options)
				if err != nil {
					t.Fatal(err)
				}
				for _, entry := range page.Entries {
					got = append(got, entry.Path)
				}
				if page.NextCursor == "" {
					break
				}
				if page.NextCursor <= test.options.Cursor || len(got) > len(test.want) {
					t.Fatalf("listing did not advance: %#v", page)
				}
				test.options.Cursor = page.NextCursor
			}
			if !slices.Equal(got, test.want) {
				t.Fatalf("paginated paths = %v, want %v", got, test.want)
			}
		})
	}
}

func TestListExcludesSymlinksAndEmptyDirectories(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "private.txt"), []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "empty"), 0o750); err != nil {
		t.Fatal(err)
	}
	filesystem, err := local.New(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, prefix := range []string{"", "esc", "escape/", "escape/private", "empty/"} {
		assertListing(t, filesystem, vfs.ListOptions{Prefix: prefix}, []string{})
		assertListing(t, filesystem, vfs.ListOptions{Prefix: prefix, Delimiter: "/"}, []string{})
	}
}

func TestConcurrentConditionalCopiesPreserveOneWinner(t *testing.T) {
	filesystem := newFilesystem(t)
	destination, err := filesystem.Write(t.Context(), "destination.txt", strings.NewReader("initial"), vfs.WriteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	const copies = 12
	for i := range copies {
		content := strings.Repeat(fmt.Sprintf("source %d\n", i), 1024)
		if _, err := filesystem.Write(t.Context(), fmt.Sprintf("source-%d.txt", i), strings.NewReader(content), vfs.WriteOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	type result struct {
		index int
		err   error
	}
	start := make(chan struct{})
	results := make(chan result, copies)
	for i := range copies {
		go func() {
			<-start
			_, err := filesystem.Copy(t.Context(), fmt.Sprintf("source-%d.txt", i), "destination.txt", vfs.TransferOptions{
				DestinationRevision: destination.Revision,
			})
			results <- result{index: i, err: err}
		}()
	}
	close(start)
	winner := -1
	for range copies {
		result := <-results
		if result.err == nil {
			if winner != -1 {
				t.Errorf("multiple copies replaced the same revision: %d, %d", winner, result.index)
			}
			winner = result.index
		} else if !errors.Is(result.err, vfs.ErrConflict) {
			t.Errorf("copy failed unexpectedly: %v", result.err)
		}
	}
	if winner == -1 {
		t.Fatal("no copy succeeded")
	}
	assertContent(t, filesystem, "destination.txt", strings.Repeat(fmt.Sprintf("source %d\n", winner), 1024))
	page, err := filesystem.List(t.Context(), vfs.ListOptions{})
	if err != nil || len(page.Entries) != copies+1 {
		t.Fatalf("listing after conditional copies = %#v, error = %v", page, err)
	}
}

type pendingWrite struct {
	reader *pausedReader
	done   chan struct{}
	err    error
}

func startPausedWrite(t *testing.T, filesystem vfs.FileSystem, ctx context.Context, name string, options vfs.WriteOptions) *pendingWrite {
	t.Helper()
	write := &pendingWrite{reader: newPausedReader(), done: make(chan struct{})}
	go func() {
		_, write.err = filesystem.Write(ctx, name, write.reader, options)
		close(write.done)
	}()
	t.Cleanup(func() {
		write.reader.resume()
		_ = write.wait()
	})
	select {
	case <-write.reader.paused:
	case <-write.done:
		t.Fatalf("write did not reach paused reader: %v", write.err)
	case <-time.After(2 * time.Second):
		t.Fatal("write did not reach paused reader")
	}
	return write
}

func (w *pendingWrite) wait() error { <-w.done; return w.err }

type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }

func assertListing(t *testing.T, filesystem vfs.FileSystem, options vfs.ListOptions, want []string) {
	t.Helper()
	page, err := filesystem.List(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, entry := range page.Entries {
		got = append(got, entry.Path)
		if entry.IsDir != strings.HasSuffix(entry.Path, "/") {
			t.Fatalf("wrong directory projection: %#v", entry)
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("list %#v = %v, want %v", options, got, want)
	}
}

func assertContent(t *testing.T, filesystem vfs.FileSystem, name, want string) {
	t.Helper()
	file, err := filesystem.Open(t.Context(), name, vfs.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	content, readErr := io.ReadAll(file.Body)
	closeErr := file.Body.Close()
	if readErr != nil || closeErr != nil || string(content) != want {
		t.Fatalf("read %q: content = %q, errors = %v / %v", name, content, readErr, closeErr)
	}
}

func newFilesystem(t testing.TB) *local.FS {
	t.Helper()
	filesystem, err := local.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return filesystem
}

// pausedReader writes a partial body, then waits for an explicit release.
type pausedReader struct {
	paused  chan struct{}
	release chan struct{}
	step    int
	once    sync.Once
}

func newPausedReader() *pausedReader {
	return &pausedReader{paused: make(chan struct{}), release: make(chan struct{})}
}

func (r *pausedReader) Read(p []byte) (int, error) {
	switch r.step {
	case 0:
		r.step++
		return copy(p, "partial"), nil
	case 1:
		r.step++
		close(r.paused)
		<-r.release
		return copy(p, " complete"), io.EOF
	default:
		return 0, io.EOF
	}
}

func (r *pausedReader) resume() { r.once.Do(func() { close(r.release) }) }

func independentOperation(ctx context.Context, filesystem vfs.FileSystem, operation string) error {
	switch operation {
	case "open":
		file, err := filesystem.Open(ctx, "existing.txt", vfs.OpenOptions{})
		if err != nil {
			return err
		}
		content, err := io.ReadAll(file.Body)
		closeErr := file.Body.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if string(content) != "existing" {
			return fmt.Errorf("unexpected content %q", content)
		}
	case "stat":
		_, err := filesystem.Stat(ctx, "existing.txt")
		return err
	case "list":
		page, err := filesystem.List(ctx, vfs.ListOptions{})
		if err != nil {
			return err
		}
		if len(page.Entries) != 1 || page.Entries[0].Path != "existing.txt" {
			return fmt.Errorf("listing exposed unpublished content: %#v", page)
		}
	case "write":
		_, err := filesystem.Write(ctx, "unrelated.txt", strings.NewReader("other"), vfs.WriteOptions{})
		return err
	default:
		return fmt.Errorf("unknown operation %q", operation)
	}
	return nil
}
