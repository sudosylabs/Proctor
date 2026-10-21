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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/packages/vfs"
	"github.com/sudosylabs/proctor/packages/vfs/local"
	"github.com/sudosylabs/proctor/packages/vfs/vfstest"
)

func TestConformance(t *testing.T) {
	vfstest.Run(t, func(t *testing.T) vfs.FileSystem {
		t.Helper()
		filesystem, err := local.New(t.TempDir())
		require.NoError(t, err)
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
	require.NoError(t, err)
	_, err = filesystem.Stat(t.Context(), "escape/file.txt")
	require.Error(t, err, "expected symlink path to be rejected")
}

func TestPausedWriteAllowsUnrelatedOperations(t *testing.T) {
	for _, operation := range []string{"open", "stat", "list", "write"} {
		t.Run(operation, func(t *testing.T) {
			filesystem := newFilesystem(t)
			_, err := filesystem.Write(t.Context(), "existing.txt", strings.NewReader("existing"), vfs.WriteOptions{})
			require.NoError(t, err)
			write := startPausedWrite(t, filesystem, t.Context(), "upload.txt", vfs.WriteOptions{})
			completed := make(chan error, 1)
			go func() { completed <- independentOperation(t.Context(), filesystem, operation) }()
			select {
			case err := <-completed:
				require.NoError(t, err)
			case <-time.After(2 * time.Second):
				write.reader.resume()
				<-completed
				require.FailNow(t, "unrelated operation waited for the paused upload")
			}
			write.reader.resume()
			require.NoError(t, write.wait())
		})
	}
}

func TestStagingIsInvisibleThroughFilesystem(t *testing.T) {
	root := t.TempDir()
	filesystem, err := local.New(root)
	require.NoError(t, err)
	for _, name := range []string{"existing.txt", "upload/.vfs-visible"} {
		_, err = filesystem.Write(t.Context(), name, strings.NewReader("existing"), vfs.WriteOptions{})
		require.NoError(t, err)
	}
	write := startPausedWrite(t, filesystem, t.Context(), "upload/final.txt", vfs.WriteOptions{})
	entries, err := os.ReadDir(filepath.Join(root, "upload"))
	require.NoError(t, err)
	var staged string
	for _, entry := range entries {
		if entry.Name() != ".vfs-visible" {
			require.Empty(t, staged, "expected one temporary object")
			staged = "upload/" + entry.Name()
		}
	}
	require.NotEmpty(t, staged, "paused write did not stage bytes")
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
			require.ErrorIs(t, operation.run(staged), vfs.ErrNotFound, "staging access error")
		})
	}
	t.Run("filesystem case alias", func(t *testing.T) {
		alias := strings.ToUpper(staged)
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(alias))); errors.Is(err, os.ErrNotExist) {
			t.Skip("filesystem does not provide case aliases")
		} else if err != nil {
			require.NoError(t, err)
		}
		for _, operation := range operations {
			assert.ErrorIs(t, operation.run(alias), vfs.ErrNotFound, "staging %s through case alias", operation.name)
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
	require.NoError(t, write.wait())
	assertContent(t, filesystem, "upload/final.txt", "partial complete")
	assertContent(t, filesystem, "existing.txt", "existing")
	assertListing(t, filesystem, vfs.ListOptions{}, []string{"existing.txt", "upload/.vfs-visible", "upload/final.txt"})
	_, err = os.Stat(filepath.Join(root, filepath.FromSlash(staged)))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestStagingFromAliasedDirectoryIsInvisible(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "directory"), 0o750))
	if _, err := os.Stat(filepath.Join(root, "DIRECTORY")); errors.Is(err, os.ErrNotExist) {
		t.Skip("filesystem does not provide case aliases")
	} else if err != nil {
		require.NoError(t, err)
	}
	filesystem, err := local.New(root)
	require.NoError(t, err)
	write := startPausedWrite(t, filesystem, t.Context(), "DIRECTORY/new.txt", vfs.WriteOptions{})
	assertListing(t, filesystem, vfs.ListOptions{}, []string{})
	assertListing(t, filesystem, vfs.ListOptions{Prefix: "directory/"}, []string{})
	write.reader.resume()
	require.NoError(t, write.wait())
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
				require.NoError(t, err)
				options = vfs.WriteOptions{ExpectedRevision: first.Revision}
			}
			write := startPausedWrite(t, filesystem, t.Context(), "target.txt", options)
			if condition == "revision removed" {
				require.NoError(t, filesystem.Remove(t.Context(), "target.txt", vfs.RemoveOptions{}))
			} else {
				_, err := filesystem.Write(t.Context(), "target.txt", strings.NewReader("concurrent winner"), vfs.WriteOptions{})
				require.NoError(t, err)
			}
			write.reader.resume()
			wantErr := vfs.ErrConflict
			if condition == "no overwrite" {
				wantErr = vfs.ErrAlreadyExists
			}
			require.ErrorIs(t, write.wait(), wantErr, "write error")
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
			require.NoError(t, err)
			first, err := filesystem.Write(t.Context(), "target.txt", strings.NewReader("original"), vfs.WriteOptions{})
			require.NoError(t, err)
			options := vfs.WriteOptions{ExpectedRevision: first.Revision}
			if failure == "reader failure" {
				failureErr := errors.New("reader stopped")
				_, err = filesystem.Write(t.Context(), "target.txt", io.MultiReader(strings.NewReader("partial"), failingReader{failureErr}), options)
				require.ErrorIs(t, err, failureErr, "write error")
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
				require.Error(t, err, "write error = %v, want %s", err, failure)
				require.False(t, failure == "canceled at EOF" && !errors.Is(err, context.Canceled), "write error = %v, want %s", err, failure)
			}
			assertContent(t, filesystem, "target.txt", "original")
			entries, err := os.ReadDir(root)
			require.NoError(t, err, "unfinished file remained on disk")
			require.Len(t, entries, 1, "unfinished file remained on disk")
			require.Equal(t, "target.txt", entries[0].Name(), "unfinished file remained on disk")
		})
	}
}

func TestWriteRejectsSymlinkIntroducedBeforePublication(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	require.NoError(t, os.WriteFile(outside, []byte("outside"), 0o600))
	filesystem, err := local.New(root)
	require.NoError(t, err)
	write := startPausedWrite(t, filesystem, t.Context(), "target.txt", vfs.WriteOptions{})
	if err := os.Symlink(outside, filepath.Join(root, "target.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	write.reader.resume()
	require.ErrorIs(t, write.wait(), vfs.ErrInvalidPath, "publication error")
	content, err := os.ReadFile(outside)
	require.NoError(t, err, "outside file changed")
	require.Equal(t, "outside", string(content), "outside file changed")
	entries, err := os.ReadDir(root)
	require.NoError(t, err, "temporary file remained")
	require.Len(t, entries, 1, "temporary file remained")
	require.Equal(t, "target.txt", entries[0].Name(), "temporary file remained")
}

func TestListLexicalPrefixesAndPagination(t *testing.T) {
	filesystem := newFilesystem(t)
	for _, name := range []string{
		"alpha.txt", "alpha/one.txt", "alphabet/two.txt", "alpine/three.txt",
		"a/b/c.txt", "a/bb.txt", "a/bc/file.txt", "a/bc/deeper/z.txt", "other/hidden.txt",
	} {
		_, err := filesystem.Write(t.Context(), name, strings.NewReader(name), vfs.WriteOptions{})
		require.NoError(t, err)
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
				require.NoError(t, err)
				for _, entry := range page.Entries {
					got = append(got, entry.Path)
				}
				if page.NextCursor == "" {
					break
				}
				require.Greater(t, page.NextCursor, test.options.Cursor)
				require.LessOrEqual(t, len(got), len(test.want))
				test.options.Cursor = page.NextCursor
			}
			require.True(t, slices.Equal(got, test.want), "paginated paths = %v, want %v", got, test.want)
		})
	}
}

func TestListExcludesSymlinksAndEmptyDirectories(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "private.txt"), []byte("outside"), 0o600))
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	require.NoError(t, os.Mkdir(filepath.Join(root, "empty"), 0o750))
	filesystem, err := local.New(root)
	require.NoError(t, err)
	for _, prefix := range []string{"", "esc", "escape/", "escape/private", "empty/"} {
		assertListing(t, filesystem, vfs.ListOptions{Prefix: prefix}, []string{})
		assertListing(t, filesystem, vfs.ListOptions{Prefix: prefix, Delimiter: "/"}, []string{})
	}
}

func TestConcurrentConditionalCopiesPreserveOneWinner(t *testing.T) {
	filesystem := newFilesystem(t)
	destination, err := filesystem.Write(t.Context(), "destination.txt", strings.NewReader("initial"), vfs.WriteOptions{})
	require.NoError(t, err)
	const copies = 12
	for i := range copies {
		content := strings.Repeat(fmt.Sprintf("source %d\n", i), 1024)
		_, err = filesystem.Write(t.Context(), fmt.Sprintf("source-%d.txt", i), strings.NewReader(content), vfs.WriteOptions{})
		require.NoError(t, err)
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
			assert.Equal(t, -1, winner, "multiple copies replaced the same revision: %d, %d", winner, result.index)
			winner = result.index
			continue
		}
		assert.ErrorIs(t, result.err, vfs.ErrConflict, "copy failed unexpectedly")
	}
	require.NotEqual(t, -1, winner, "no copy succeeded")
	assertContent(t, filesystem, "destination.txt", strings.Repeat(fmt.Sprintf("source %d\n", winner), 1024))
	page, err := filesystem.List(t.Context(), vfs.ListOptions{})
	require.NoError(t, err, "listing after conditional copies = %#v, error", page)
	require.Len(t, page.Entries, copies+1, "listing after conditional copies = %#v, error", page)
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
		require.FailNow(t, fmt.Sprintf("write did not reach paused reader: %v", write.err))
	case <-time.After(2 * time.Second):
		require.FailNow(t, "write did not reach paused reader")
	}
	return write
}

func (w *pendingWrite) wait() error { <-w.done; return w.err }

type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }

func assertListing(t *testing.T, filesystem vfs.FileSystem, options vfs.ListOptions, want []string) {
	t.Helper()
	page, err := filesystem.List(t.Context(), options)
	require.NoError(t, err)
	var got []string
	for _, entry := range page.Entries {
		got = append(got, entry.Path)
		require.Equal(t, strings.HasSuffix(entry.Path, "/"), entry.IsDir)
	}
	require.True(t, slices.Equal(got, want), "list %#v = %v, want %v", options, got, want)
}

func assertContent(t *testing.T, filesystem vfs.FileSystem, name, want string) {
	t.Helper()
	file, err := filesystem.Open(t.Context(), name, vfs.OpenOptions{})
	require.NoError(t, err)
	content, readErr := io.ReadAll(file.Body)
	closeErr := file.Body.Close()
	require.NoError(t, readErr, "read %q: content = %q, errors = %v", name, content, readErr)
	require.NoError(t, closeErr, "read %q: content = %q, errors = %v", name, content, readErr)
	require.Equal(t, want, string(content), "read %q: content = %q, errors = %v", name, content, readErr)
}

func newFilesystem(t testing.TB) *local.FS {
	t.Helper()
	filesystem, err := local.New(t.TempDir())
	require.NoError(t, err)
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
