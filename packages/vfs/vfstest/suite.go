// Package vfstest provides a conformance suite for VFS implementations.
package vfstest

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/packages/vfs"
)

// Factory creates an isolated empty filesystem for a test.
type Factory func(t *testing.T) vfs.FileSystem

// Run executes the reusable VFS conformance suite.
func Run(t *testing.T, factory Factory) {
	t.Helper()

	t.Run("write open and stat", func(t *testing.T) {
		filesystem := factory(t)
		ctx := context.Background()
		content := []byte("portable storage")
		size := int64(len(content))

		written, err := filesystem.Write(ctx, "school/exam.txt", bytes.NewReader(content), vfs.WriteOptions{Size: &size})
		require.NoError(t, err)
		require.Equal(t, "school/exam.txt", written.Path)
		require.Equal(t, size, written.Size)
		require.NotEmpty(t, written.Revision)

		stat, err := filesystem.Stat(ctx, "school/exam.txt")
		require.NoError(t, err)
		require.Equal(t, written.Revision, stat.Revision, "revision mismatch")

		file, err := filesystem.Open(ctx, "school/exam.txt", vfs.OpenOptions{})
		require.NoError(t, err)
		defer file.Body.Close()
		got, err := io.ReadAll(file.Body)
		require.NoError(t, err)
		require.True(t, bytes.Equal(got, content), "content mismatch: %q", got)
	})

	t.Run("normalizes portable paths", func(t *testing.T) {
		filesystem := factory(t)
		info, err := filesystem.Write(context.Background(), "one/./two.txt", bytes.NewBufferString("data"), vfs.WriteOptions{})
		require.NoError(t, err)
		require.Equal(t, "one/two.txt", info.Path)
	})

	t.Run("rejects unsafe paths", func(t *testing.T) {
		filesystem := factory(t)
		for _, name := range []string{"", "/absolute", "../escape", "one/../../escape", `windows\path`} {
			_, err := filesystem.Write(context.Background(), name, bytes.NewBufferString("data"), vfs.WriteOptions{})
			assert.ErrorIs(t, err, vfs.ErrInvalidPath, "write %q: expected invalid path", name)
		}
	})

	t.Run("range reads", func(t *testing.T) {
		filesystem := factory(t)
		ctx := context.Background()
		_, err := filesystem.Write(ctx, "range.txt", bytes.NewBufferString("0123456789"), vfs.WriteOptions{})
		require.NoError(t, err)

		file, err := filesystem.Open(ctx, "range.txt", vfs.OpenOptions{Offset: 3, Length: 4})
		require.NoError(t, err)
		got, readErr := io.ReadAll(file.Body)
		closeErr := file.Body.Close()
		require.NoError(t, readErr, "read/close range")
		require.NoError(t, closeErr, "read/close range")
		require.Equal(t, "3456", string(got), "unexpected range")

		_, err = filesystem.Open(ctx, "range.txt", vfs.OpenOptions{Offset: 11})
		require.ErrorIs(t, err, vfs.ErrInvalidRange)
	})

	t.Run("conditional operations", func(t *testing.T) {
		filesystem := factory(t)
		ctx := context.Background()
		first, err := filesystem.Write(ctx, "conditional.txt", bytes.NewBufferString("first"), vfs.WriteOptions{})
		require.NoError(t, err)
		if !filesystem.Capabilities().ConditionalWrite {
			_, err = filesystem.Write(ctx, "conditional.txt", bytes.NewBufferString("second"), vfs.WriteOptions{NoOverwrite: true})
			require.ErrorIs(t, err, vfs.ErrUnsupported)
			return
		}

		_, err = filesystem.Write(ctx, "conditional.txt", bytes.NewBufferString("second"), vfs.WriteOptions{NoOverwrite: true})
		require.ErrorIs(t, err, vfs.ErrAlreadyExists)
		_, err = filesystem.Write(ctx, "conditional.txt", bytes.NewBufferString("second"), vfs.WriteOptions{ExpectedRevision: "wrong"})
		require.ErrorIs(t, err, vfs.ErrConflict)
		second, err := filesystem.Write(ctx, "conditional.txt", bytes.NewBufferString("second"), vfs.WriteOptions{ExpectedRevision: first.Revision})
		require.NoError(t, err)

		err = filesystem.Remove(ctx, "conditional.txt", vfs.RemoveOptions{ExpectedRevision: first.Revision})
		require.ErrorIs(t, err, vfs.ErrConflict)
		require.NoError(t, filesystem.Remove(ctx, "conditional.txt", vfs.RemoveOptions{ExpectedRevision: second.Revision}))
	})

	t.Run("copy and move", func(t *testing.T) {
		filesystem := factory(t)
		ctx := context.Background()
		source, err := filesystem.Write(ctx, "source.txt", bytes.NewBufferString("content"), vfs.WriteOptions{})
		require.NoError(t, err)

		copyOptions := vfs.TransferOptions{SourceRevision: source.Revision}
		if filesystem.Capabilities().ConditionalWrite {
			copyOptions.NoOverwrite = true
		}
		copied, err := filesystem.Copy(ctx, "source.txt", "copies/copied.txt", copyOptions)
		require.NoError(t, err)
		require.Equal(t, "copies/copied.txt", copied.Path)
		require.NotEmpty(t, copied.Revision)

		moveOptions := vfs.TransferOptions{}
		if filesystem.Capabilities().ConditionalWrite {
			moveOptions.NoOverwrite = true
		}
		moved, err := filesystem.Move(ctx, "copies/copied.txt", "moved.txt", moveOptions)
		require.NoError(t, err)
		require.Equal(t, "moved.txt", moved.Path)
		_, err = filesystem.Stat(ctx, "copies/copied.txt")
		require.ErrorIs(t, err, vfs.ErrNotFound)
	})

	t.Run("source revision move", func(t *testing.T) {
		filesystem := factory(t)
		ctx := context.Background()
		source, err := filesystem.Write(ctx, "source.txt", bytes.NewBufferString("content"), vfs.WriteOptions{})
		require.NoError(t, err)
		moved, err := filesystem.Move(ctx, "source.txt", "destination.txt", vfs.TransferOptions{
			SourceRevision: source.Revision,
		})
		if errors.Is(err, vfs.ErrUnsupported) {
			assertRevision(t, filesystem, "source.txt", source.Revision)
			_, statErr := filesystem.Stat(ctx, "destination.txt")
			require.ErrorIs(t, statErr, vfs.ErrNotFound)
			return
		}
		require.NoError(t, err)
		require.Equal(t, "destination.txt", moved.Path)
		_, err = filesystem.Stat(ctx, "source.txt")
		require.ErrorIs(t, err, vfs.ErrNotFound)
		assertRevision(t, filesystem, "destination.txt", moved.Revision)
	})

	t.Run("move rejects stale source revision", func(t *testing.T) {
		filesystem := factory(t)
		ctx := context.Background()
		source, err := filesystem.Write(ctx, "source.txt", bytes.NewBufferString("original"), vfs.WriteOptions{})
		require.NoError(t, err)
		replacement, err := filesystem.Write(ctx, "source.txt", bytes.NewBufferString("replacement"), vfs.WriteOptions{})
		require.NoError(t, err)
		destination, err := filesystem.Write(ctx, "destination.txt", bytes.NewBufferString("destination"), vfs.WriteOptions{})
		require.NoError(t, err)
		_, err = filesystem.Move(ctx, "source.txt", "destination.txt", vfs.TransferOptions{
			SourceRevision: source.Revision,
		})
		if !errors.Is(err, vfs.ErrConflict) {
			require.ErrorIs(t, err, vfs.ErrUnsupported, "Move() error")
		}
		assertRevision(t, filesystem, "source.txt", replacement.Revision)
		assertRevision(t, filesystem, "destination.txt", destination.Revision)
	})

	t.Run("recursive and delimited listing", func(t *testing.T) {
		filesystem := factory(t)
		ctx := context.Background()
		for _, name := range []string{"a/1.txt", "a/2.txt", "a/nested/3.txt", "b/4.txt"} {
			_, err := filesystem.Write(ctx, name, bytes.NewBufferString(name), vfs.WriteOptions{})
			require.NoError(t, err, "write %q", name)
		}

		recursive, err := filesystem.List(ctx, vfs.ListOptions{Prefix: "a/"})
		require.NoError(t, err)
		assertPaths(t, recursive.Entries, []string{"a/1.txt", "a/2.txt", "a/nested/3.txt"})

		delimited, err := filesystem.List(ctx, vfs.ListOptions{Prefix: "a/", Delimiter: "/"})
		require.NoError(t, err)
		assertPaths(t, delimited.Entries, []string{"a/1.txt", "a/2.txt", "a/nested/"})
		require.True(t, delimited.Entries[2].IsDir)
	})

	t.Run("stable pagination", func(t *testing.T) {
		filesystem := factory(t)
		ctx := context.Background()
		for _, name := range []string{"1.txt", "2.txt", "3.txt"} {
			_, err := filesystem.Write(ctx, name, bytes.NewBufferString(name), vfs.WriteOptions{})
			require.NoError(t, err, "write %q", name)
		}

		first, err := filesystem.List(ctx, vfs.ListOptions{Limit: 2})
		require.NoError(t, err)
		assertPaths(t, first.Entries, []string{"1.txt", "2.txt"})
		require.Equal(t, "2.txt", first.NextCursor)

		second, err := filesystem.List(ctx, vfs.ListOptions{Cursor: first.NextCursor, Limit: 2})
		require.NoError(t, err)
		assertPaths(t, second.Entries, []string{"3.txt"})
		require.Empty(t, second.NextCursor)
	})

	t.Run("pagination follows full-path byte order", func(t *testing.T) {
		filesystem := factory(t)
		ctx := context.Background()
		// '-' and '.' sort before '/', so "a-c.txt" precedes everything in "a/".
		for _, name := range []string{"b.txt", "a0.txt", "a/b.txt", "a/a/z.txt", "a.d/x.txt", "a-c.txt"} {
			_, err := filesystem.Write(ctx, name, bytes.NewBufferString(name), vfs.WriteOptions{})
			require.NoError(t, err, "write %q", name)
		}

		assertPaginated(t, filesystem, vfs.ListOptions{},
			[]string{"a-c.txt", "a.d/x.txt", "a/a/z.txt", "a/b.txt", "a0.txt", "b.txt"})
		assertPaginated(t, filesystem, vfs.ListOptions{Delimiter: "/"},
			[]string{"a-c.txt", "a.d/", "a/", "a0.txt", "b.txt"})
		assertPaginated(t, filesystem, vfs.ListOptions{Prefix: "a/", Delimiter: "/"},
			[]string{"a/a/", "a/b.txt"})
	})

	t.Run("check root", func(t *testing.T) {
		filesystem := factory(t)
		require.NoError(t, filesystem.CheckRoot(context.Background()))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		require.ErrorIs(t, filesystem.CheckRoot(ctx), context.Canceled)
	})

	t.Run("honors canceled contexts", func(t *testing.T) {
		filesystem := factory(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := filesystem.Write(ctx, "canceled.txt", bytes.NewBufferString("data"), vfs.WriteOptions{})
		require.ErrorIs(t, err, context.Canceled)
	})
}

func assertRevision(t *testing.T, filesystem vfs.FileSystem, path, want string) {
	t.Helper()
	info, err := filesystem.Stat(context.Background(), path)
	require.NoError(t, err, "stat %q", path)
	require.Equal(t, want, info.Revision, "revision for %q", path)
}

// assertPaginated reads every page of size one and also the whole listing in
// one page, and requires both to equal expected.
func assertPaginated(t *testing.T, filesystem vfs.FileSystem, options vfs.ListOptions, expected []string) {
	t.Helper()
	whole, err := filesystem.List(context.Background(), options)
	require.NoError(t, err)
	assertPaths(t, whole.Entries, expected)
	require.Empty(t, whole.NextCursor)

	options.Limit = 1
	var got []vfs.Info
	for range len(expected) + 1 {
		page, err := filesystem.List(context.Background(), options)
		require.NoError(t, err)
		got = append(got, page.Entries...)
		if page.NextCursor == "" {
			break
		}
		options.Cursor = page.NextCursor
	}
	assertPaths(t, got, expected)
}

func assertPaths(t *testing.T, entries []vfs.Info, expected []string) {
	t.Helper()
	require.Len(t, entries, len(expected), "entry count: got")
	for index := range expected {
		require.Equal(t, expected[index], entries[index].Path, "entry %d", index)
	}
}
