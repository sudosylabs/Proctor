// Package memory provides an in-memory VFS backend for tests and ephemeral
// workloads.
package memory

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sudosylabs/proctor/packages/vfs"
)

type object struct {
	data []byte
	info vfs.Info
}

// FS is a concurrency-safe in-memory filesystem.
type FS struct {
	mu      sync.RWMutex
	objects map[string]object
}

// New creates an empty in-memory filesystem.
func New() *FS {
	return &FS{objects: make(map[string]object)}
}

func (f *FS) Capabilities() vfs.Capabilities {
	return vfs.Capabilities{
		AtomicMove:       true,
		ConditionalWrite: true,
		RangeRead:        true,
	}
}

func (f *FS) Open(ctx context.Context, name string, options vfs.OpenOptions) (*vfs.File, error) {
	const op = "open"
	name, err := vfs.NormalizePath(name)
	if err != nil {
		return nil, vfs.Error(op, name, err)
	}
	if err := options.Validate(); err != nil {
		return nil, vfs.Error(op, name, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, vfs.Error(op, name, err)
	}

	f.mu.RLock()
	obj, ok := f.objects[name]
	if ok {
		obj.data = bytes.Clone(obj.data)
	}
	f.mu.RUnlock()
	if !ok {
		return nil, vfs.Error(op, name, vfs.ErrNotFound)
	}

	start := options.Offset
	if start > int64(len(obj.data)) {
		return nil, vfs.Error(op, name, vfs.ErrInvalidRange)
	}
	end := int64(len(obj.data))
	if options.Length > 0 && start+options.Length < end {
		end = start + options.Length
	}

	return &vfs.File{
		Info: obj.info,
		Body: io.NopCloser(bytes.NewReader(obj.data[start:end])),
	}, nil
}

func (f *FS) Write(ctx context.Context, name string, body io.Reader, options vfs.WriteOptions) (vfs.Info, error) {
	const op = "write"
	name, err := vfs.NormalizePath(name)
	if err != nil {
		return vfs.Info{}, vfs.Error(op, name, err)
	}
	if body == nil {
		return vfs.Info{}, vfs.Error(op, name, fmt.Errorf("body is nil"))
	}
	if err := options.Validate(); err != nil {
		return vfs.Info{}, vfs.Error(op, name, err)
	}
	if err := ctx.Err(); err != nil {
		return vfs.Info{}, vfs.Error(op, name, err)
	}

	data, err := readAll(ctx, body)
	if err != nil {
		return vfs.Info{}, vfs.Error(op, name, err)
	}
	if options.Size != nil && int64(len(data)) != *options.Size {
		return vfs.Info{}, vfs.Error(op, name, fmt.Errorf("size mismatch: expected %d bytes, received %d", *options.Size, len(data)))
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	current, exists := f.objects[name]
	if options.NoOverwrite && exists {
		return vfs.Info{}, vfs.Error(op, name, vfs.ErrAlreadyExists)
	}
	if options.ExpectedRevision != "" {
		if !exists || current.info.Revision != options.ExpectedRevision {
			return vfs.Info{}, vfs.Error(op, name, vfs.ErrConflict)
		}
	}

	now := time.Now().UTC()
	info := vfs.Info{
		Path:       name,
		Size:       int64(len(data)),
		ModifiedAt: now,
		Revision:   revision(data),
	}
	f.objects[name] = object{data: bytes.Clone(data), info: info}
	return info, nil
}

func (f *FS) Stat(ctx context.Context, name string) (vfs.Info, error) {
	const op = "stat"
	name, err := vfs.NormalizePath(name)
	if err != nil {
		return vfs.Info{}, vfs.Error(op, name, err)
	}
	if err := ctx.Err(); err != nil {
		return vfs.Info{}, vfs.Error(op, name, err)
	}

	f.mu.RLock()
	obj, ok := f.objects[name]
	f.mu.RUnlock()
	if !ok {
		return vfs.Info{}, vfs.Error(op, name, vfs.ErrNotFound)
	}
	return obj.info, nil
}

func (f *FS) Remove(ctx context.Context, name string, options vfs.RemoveOptions) error {
	const op = "remove"
	name, err := vfs.NormalizePath(name)
	if err != nil {
		return vfs.Error(op, name, err)
	}
	if err := ctx.Err(); err != nil {
		return vfs.Error(op, name, err)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	obj, ok := f.objects[name]
	if !ok {
		return vfs.Error(op, name, vfs.ErrNotFound)
	}
	if options.ExpectedRevision != "" && obj.info.Revision != options.ExpectedRevision {
		return vfs.Error(op, name, vfs.ErrConflict)
	}
	delete(f.objects, name)
	return nil
}

func (f *FS) List(ctx context.Context, options vfs.ListOptions) (vfs.Page, error) {
	const op = "list"
	options, err := options.Normalize()
	if err != nil {
		return vfs.Page{}, vfs.Error(op, options.Prefix, err)
	}
	if err := ctx.Err(); err != nil {
		return vfs.Page{}, vfs.Error(op, options.Prefix, err)
	}

	f.mu.RLock()
	entries := make(map[string]vfs.Info)
	for name, obj := range f.objects {
		if !strings.HasPrefix(name, options.Prefix) {
			continue
		}
		if options.Delimiter == "/" {
			remainder := strings.TrimPrefix(name, options.Prefix)
			if index := strings.Index(remainder, "/"); index >= 0 {
				directory := options.Prefix + remainder[:index+1]
				entries[directory] = vfs.Info{Path: directory, IsDir: true}
				continue
			}
		}
		entries[name] = obj.info
	}
	f.mu.RUnlock()

	return makePage(entries, options), nil
}

func (f *FS) Copy(ctx context.Context, source, destination string, options vfs.TransferOptions) (vfs.Info, error) {
	return f.transfer(ctx, "copy", source, destination, options, false)
}

func (f *FS) Move(ctx context.Context, source, destination string, options vfs.TransferOptions) (vfs.Info, error) {
	return f.transfer(ctx, "move", source, destination, options, true)
}

func (f *FS) transfer(ctx context.Context, op, source, destination string, options vfs.TransferOptions, removeSource bool) (vfs.Info, error) {
	source, err := vfs.NormalizePath(source)
	if err != nil {
		return vfs.Info{}, vfs.Error(op, source, err)
	}
	destination, err = vfs.NormalizePath(destination)
	if err != nil {
		return vfs.Info{}, vfs.Error(op, destination, err)
	}
	if source == destination {
		return vfs.Info{}, vfs.Error(op, source, fmt.Errorf("%w: source and destination are equal", vfs.ErrConflict))
	}
	if err := options.Validate(); err != nil {
		return vfs.Info{}, vfs.Error(op, source+" -> "+destination, err)
	}
	if err := ctx.Err(); err != nil {
		return vfs.Info{}, vfs.Error(op, source+" -> "+destination, err)
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	src, ok := f.objects[source]
	if !ok {
		return vfs.Info{}, vfs.Error(op, source, vfs.ErrNotFound)
	}
	if options.SourceRevision != "" && src.info.Revision != options.SourceRevision {
		return vfs.Info{}, vfs.Error(op, source, vfs.ErrConflict)
	}
	dst, destinationExists := f.objects[destination]
	if options.NoOverwrite && destinationExists {
		return vfs.Info{}, vfs.Error(op, destination, vfs.ErrAlreadyExists)
	}
	if options.DestinationRevision != "" && (!destinationExists || dst.info.Revision != options.DestinationRevision) {
		return vfs.Info{}, vfs.Error(op, destination, vfs.ErrConflict)
	}

	info := src.info
	info.Path = destination
	info.ModifiedAt = time.Now().UTC()
	f.objects[destination] = object{data: bytes.Clone(src.data), info: info}
	if removeSource {
		delete(f.objects, source)
	}
	return info, nil
}

func readAll(ctx context.Context, reader io.Reader) ([]byte, error) {
	var buffer bytes.Buffer
	chunk := make([]byte, 32*1024)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, err := reader.Read(chunk)
		if n > 0 {
			_, _ = buffer.Write(chunk[:n])
		}
		if err == io.EOF {
			return buffer.Bytes(), nil
		}
		if err != nil {
			return nil, err
		}
	}
}

func revision(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func makePage(entries map[string]vfs.Info, options vfs.ListOptions) vfs.Page {
	names := make([]string, 0, len(entries))
	for name := range entries {
		if options.Cursor == "" || name > options.Cursor {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	hasMore := len(names) > options.Limit
	if hasMore {
		names = names[:options.Limit]
	}
	page := vfs.Page{Entries: make([]vfs.Info, 0, len(names))}
	for _, name := range names {
		page.Entries = append(page.Entries, entries[name])
	}
	if hasMore {
		page.NextCursor = names[len(names)-1]
	}
	return page
}
