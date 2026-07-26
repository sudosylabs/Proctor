// Package vfstest provides a conformance suite for VFS implementations.
package vfstest

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

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
		if err != nil {
			t.Fatalf("write: %v", err)
		}
		if written.Path != "school/exam.txt" || written.Size != size || written.Revision == "" {
			t.Fatalf("unexpected write info: %#v", written)
		}

		stat, err := filesystem.Stat(ctx, "school/exam.txt")
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if stat.Revision != written.Revision {
			t.Fatalf("revision mismatch: %q != %q", stat.Revision, written.Revision)
		}

		file, err := filesystem.Open(ctx, "school/exam.txt", vfs.OpenOptions{})
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		defer file.Body.Close()
		got, err := io.ReadAll(file.Body)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if !bytes.Equal(got, content) {
			t.Fatalf("content mismatch: %q", got)
		}
	})

	t.Run("normalizes portable paths", func(t *testing.T) {
		filesystem := factory(t)
		info, err := filesystem.Write(context.Background(), "one/./two.txt", bytes.NewBufferString("data"), vfs.WriteOptions{})
		if err != nil {
			t.Fatalf("write normalized path: %v", err)
		}
		if info.Path != "one/two.txt" {
			t.Fatalf("path was not normalized: %q", info.Path)
		}
	})

	t.Run("rejects unsafe paths", func(t *testing.T) {
		filesystem := factory(t)
		for _, name := range []string{"", "/absolute", "../escape", "one/../../escape", `windows\path`} {
			_, err := filesystem.Write(context.Background(), name, bytes.NewBufferString("data"), vfs.WriteOptions{})
			if !errors.Is(err, vfs.ErrInvalidPath) {
				t.Errorf("write %q: expected invalid path, got %v", name, err)
			}
		}
	})

	t.Run("range reads", func(t *testing.T) {
		filesystem := factory(t)
		ctx := context.Background()
		_, err := filesystem.Write(ctx, "range.txt", bytes.NewBufferString("0123456789"), vfs.WriteOptions{})
		if err != nil {
			t.Fatalf("write: %v", err)
		}

		file, err := filesystem.Open(ctx, "range.txt", vfs.OpenOptions{Offset: 3, Length: 4})
		if err != nil {
			t.Fatalf("open range: %v", err)
		}
		got, readErr := io.ReadAll(file.Body)
		closeErr := file.Body.Close()
		if readErr != nil || closeErr != nil {
			t.Fatalf("read/close range: %v / %v", readErr, closeErr)
		}
		if string(got) != "3456" {
			t.Fatalf("unexpected range: %q", got)
		}

		_, err = filesystem.Open(ctx, "range.txt", vfs.OpenOptions{Offset: 11})
		if !errors.Is(err, vfs.ErrInvalidRange) {
			t.Fatalf("expected invalid range, got %v", err)
		}
	})

	t.Run("conditional operations", func(t *testing.T) {
		filesystem := factory(t)
		ctx := context.Background()
		first, err := filesystem.Write(ctx, "conditional.txt", bytes.NewBufferString("first"), vfs.WriteOptions{})
		if err != nil {
			t.Fatalf("initial write: %v", err)
		}
		if !filesystem.Capabilities().ConditionalWrite {
			_, err = filesystem.Write(ctx, "conditional.txt", bytes.NewBufferString("second"), vfs.WriteOptions{NoOverwrite: true})
			if !errors.Is(err, vfs.ErrUnsupported) {
				t.Fatalf("expected unsupported conditional write, got %v", err)
			}
			return
		}

		_, err = filesystem.Write(ctx, "conditional.txt", bytes.NewBufferString("second"), vfs.WriteOptions{NoOverwrite: true})
		if !errors.Is(err, vfs.ErrAlreadyExists) {
			t.Fatalf("expected already exists, got %v", err)
		}
		_, err = filesystem.Write(ctx, "conditional.txt", bytes.NewBufferString("second"), vfs.WriteOptions{ExpectedRevision: "wrong"})
		if !errors.Is(err, vfs.ErrConflict) {
			t.Fatalf("expected revision conflict, got %v", err)
		}
		second, err := filesystem.Write(ctx, "conditional.txt", bytes.NewBufferString("second"), vfs.WriteOptions{ExpectedRevision: first.Revision})
		if err != nil {
			t.Fatalf("conditional replacement: %v", err)
		}

		err = filesystem.Remove(ctx, "conditional.txt", vfs.RemoveOptions{ExpectedRevision: first.Revision})
		if !errors.Is(err, vfs.ErrConflict) {
			t.Fatalf("expected remove conflict, got %v", err)
		}
		if err := filesystem.Remove(ctx, "conditional.txt", vfs.RemoveOptions{ExpectedRevision: second.Revision}); err != nil {
			t.Fatalf("conditional remove: %v", err)
		}
	})

	t.Run("copy and move", func(t *testing.T) {
		filesystem := factory(t)
		ctx := context.Background()
		source, err := filesystem.Write(ctx, "source.txt", bytes.NewBufferString("content"), vfs.WriteOptions{})
		if err != nil {
			t.Fatalf("write source: %v", err)
		}

		copyOptions := vfs.TransferOptions{SourceRevision: source.Revision}
		if filesystem.Capabilities().ConditionalWrite {
			copyOptions.NoOverwrite = true
		}
		copied, err := filesystem.Copy(ctx, "source.txt", "copies/copied.txt", copyOptions)
		if err != nil {
			t.Fatalf("copy: %v", err)
		}
		if copied.Path != "copies/copied.txt" || copied.Revision == "" {
			t.Fatalf("unexpected copied info: %#v", copied)
		}

		moveOptions := vfs.TransferOptions{}
		if filesystem.Capabilities().ConditionalWrite {
			moveOptions.NoOverwrite = true
		}
		moved, err := filesystem.Move(ctx, "copies/copied.txt", "moved.txt", moveOptions)
		if err != nil {
			t.Fatalf("move: %v", err)
		}
		if moved.Path != "moved.txt" {
			t.Fatalf("unexpected moved path: %q", moved.Path)
		}
		if _, err := filesystem.Stat(ctx, "copies/copied.txt"); !errors.Is(err, vfs.ErrNotFound) {
			t.Fatalf("source still exists after move: %v", err)
		}
	})

	t.Run("recursive and delimited listing", func(t *testing.T) {
		filesystem := factory(t)
		ctx := context.Background()
		for _, name := range []string{"a/1.txt", "a/2.txt", "a/nested/3.txt", "b/4.txt"} {
			if _, err := filesystem.Write(ctx, name, bytes.NewBufferString(name), vfs.WriteOptions{}); err != nil {
				t.Fatalf("write %q: %v", name, err)
			}
		}

		recursive, err := filesystem.List(ctx, vfs.ListOptions{Prefix: "a/"})
		if err != nil {
			t.Fatalf("recursive list: %v", err)
		}
		assertPaths(t, recursive.Entries, []string{"a/1.txt", "a/2.txt", "a/nested/3.txt"})

		delimited, err := filesystem.List(ctx, vfs.ListOptions{Prefix: "a/", Delimiter: "/"})
		if err != nil {
			t.Fatalf("delimited list: %v", err)
		}
		assertPaths(t, delimited.Entries, []string{"a/1.txt", "a/2.txt", "a/nested/"})
		if !delimited.Entries[2].IsDir {
			t.Fatalf("expected synthesized directory: %#v", delimited.Entries[2])
		}
	})

	t.Run("stable pagination", func(t *testing.T) {
		filesystem := factory(t)
		ctx := context.Background()
		for _, name := range []string{"1.txt", "2.txt", "3.txt"} {
			if _, err := filesystem.Write(ctx, name, bytes.NewBufferString(name), vfs.WriteOptions{}); err != nil {
				t.Fatalf("write %q: %v", name, err)
			}
		}

		first, err := filesystem.List(ctx, vfs.ListOptions{Limit: 2})
		if err != nil {
			t.Fatalf("first page: %v", err)
		}
		assertPaths(t, first.Entries, []string{"1.txt", "2.txt"})
		if first.NextCursor != "2.txt" {
			t.Fatalf("unexpected cursor: %q", first.NextCursor)
		}

		second, err := filesystem.List(ctx, vfs.ListOptions{Cursor: first.NextCursor, Limit: 2})
		if err != nil {
			t.Fatalf("second page: %v", err)
		}
		assertPaths(t, second.Entries, []string{"3.txt"})
		if second.NextCursor != "" {
			t.Fatalf("unexpected final cursor: %q", second.NextCursor)
		}
	})

	t.Run("honors canceled contexts", func(t *testing.T) {
		filesystem := factory(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := filesystem.Write(ctx, "canceled.txt", bytes.NewBufferString("data"), vfs.WriteOptions{})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected canceled context, got %v", err)
		}
	})
}

func assertPaths(t *testing.T, entries []vfs.Info, expected []string) {
	t.Helper()
	if len(entries) != len(expected) {
		t.Fatalf("entry count: got %d, expected %d (%#v)", len(entries), len(expected), entries)
	}
	for index := range expected {
		if entries[index].Path != expected[index] {
			t.Fatalf("entry %d: got %q, expected %q", index, entries[index].Path, expected[index])
		}
	}
}
