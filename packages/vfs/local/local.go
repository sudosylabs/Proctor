// Package local provides a VFS backend rooted in a local directory.
//
// The backend rejects observed symbolic links beneath its root. The root
// directory should nevertheless be writable only by the application because
// portable Go filesystem APIs cannot eliminate every symlink time-of-check to
// time-of-use race caused by a hostile local process.
package local

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sudosylabs/proctor/packages/vfs"
)

const (
	directoryMode = 0o750
	fileMode      = 0o640
)

// FS is a concurrency-safe filesystem rooted in one local directory.
type FS struct {
	root string
	mu   sync.RWMutex
}

// New creates or opens a local filesystem rooted at root.
func New(root string) (*FS, error) {
	if root == "" {
		return nil, vfs.Error("new", root, fmt.Errorf("%w: root is empty", vfs.ErrInvalidPath))
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, vfs.Error("new", root, err)
	}
	if err := os.MkdirAll(absolute, directoryMode); err != nil {
		return nil, vfs.Error("new", root, err)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, vfs.Error("new", root, err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return nil, vfs.Error("new", root, err)
	}
	if !info.IsDir() {
		return nil, vfs.Error("new", root, fmt.Errorf("%w: root is not a directory", vfs.ErrInvalidPath))
	}
	return &FS{root: canonical}, nil
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
	defer f.mu.RUnlock()

	fullPath, err := f.resolve(name)
	if err != nil {
		return nil, vfs.Error(op, name, err)
	}
	file, err := os.Open(fullPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, vfs.Error(op, name, vfs.ErrNotFound)
	}
	if err != nil {
		return nil, vfs.Error(op, name, err)
	}

	info, err := infoFromOpenFile(name, file)
	if err != nil {
		_ = file.Close()
		return nil, vfs.Error(op, name, err)
	}
	if info.IsDir {
		_ = file.Close()
		return nil, vfs.Error(op, name, vfs.ErrIsDirectory)
	}
	if options.Offset > info.Size {
		_ = file.Close()
		return nil, vfs.Error(op, name, vfs.ErrInvalidRange)
	}
	if _, err := file.Seek(options.Offset, io.SeekStart); err != nil {
		_ = file.Close()
		return nil, vfs.Error(op, name, err)
	}

	var body io.ReadCloser = file
	if options.Length > 0 {
		length := min(options.Length, info.Size-options.Offset)
		body = &limitedReadCloser{Reader: io.LimitReader(file, length), closer: file}
	}
	return &vfs.File{Info: info, Body: body}, nil
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

	f.mu.Lock()
	defer f.mu.Unlock()

	fullPath, err := f.resolve(name)
	if err != nil {
		return vfs.Info{}, vfs.Error(op, name, err)
	}
	current, exists, err := localInfo(name, fullPath)
	if err != nil {
		return vfs.Info{}, vfs.Error(op, name, err)
	}
	if exists && current.IsDir {
		return vfs.Info{}, vfs.Error(op, name, vfs.ErrIsDirectory)
	}
	if options.NoOverwrite && exists {
		return vfs.Info{}, vfs.Error(op, name, vfs.ErrAlreadyExists)
	}
	if options.ExpectedRevision != "" && (!exists || current.Revision != options.ExpectedRevision) {
		return vfs.Info{}, vfs.Error(op, name, vfs.ErrConflict)
	}

	if err := os.MkdirAll(filepath.Dir(fullPath), directoryMode); err != nil {
		return vfs.Info{}, vfs.Error(op, name, err)
	}
	written, err := f.writeTemporary(ctx, fullPath, body, options.Size)
	if err != nil {
		return vfs.Info{}, vfs.Error(op, name, err)
	}

	if options.NoOverwrite {
		if err := os.Link(written.temporaryPath, fullPath); err != nil {
			_ = os.Remove(written.temporaryPath)
			if errors.Is(err, fs.ErrExist) {
				return vfs.Info{}, vfs.Error(op, name, vfs.ErrAlreadyExists)
			}
			return vfs.Info{}, vfs.Error(op, name, err)
		}
		if err := os.Remove(written.temporaryPath); err != nil {
			return vfs.Info{}, vfs.Error(op, name, err)
		}
	} else if err := os.Rename(written.temporaryPath, fullPath); err != nil {
		_ = os.Remove(written.temporaryPath)
		return vfs.Info{}, vfs.Error(op, name, err)
	}

	info, _, err := localInfo(name, fullPath)
	if err != nil {
		return vfs.Info{}, vfs.Error(op, name, err)
	}
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
	defer f.mu.RUnlock()
	fullPath, err := f.resolve(name)
	if err != nil {
		return vfs.Info{}, vfs.Error(op, name, err)
	}
	info, exists, err := localInfo(name, fullPath)
	if err != nil {
		return vfs.Info{}, vfs.Error(op, name, err)
	}
	if !exists {
		return vfs.Info{}, vfs.Error(op, name, vfs.ErrNotFound)
	}
	return info, nil
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
	fullPath, err := f.resolve(name)
	if err != nil {
		return vfs.Error(op, name, err)
	}
	info, exists, err := localInfo(name, fullPath)
	if err != nil {
		return vfs.Error(op, name, err)
	}
	if !exists {
		return vfs.Error(op, name, vfs.ErrNotFound)
	}
	if info.IsDir {
		return vfs.Error(op, name, vfs.ErrIsDirectory)
	}
	if options.ExpectedRevision != "" && info.Revision != options.ExpectedRevision {
		return vfs.Error(op, name, vfs.ErrConflict)
	}
	if err := os.Remove(fullPath); err != nil {
		return vfs.Error(op, name, err)
	}
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
	defer f.mu.RUnlock()

	entries := make(map[string]vfs.Info)
	err = filepath.WalkDir(f.root, func(fullPath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if fullPath == f.root {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}

		relative, err := filepath.Rel(f.root, fullPath)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(relative)
		if !strings.HasPrefix(name, options.Prefix) {
			return nil
		}
		if options.Delimiter == "/" {
			remainder := strings.TrimPrefix(name, options.Prefix)
			if index := strings.Index(remainder, "/"); index >= 0 {
				directory := options.Prefix + remainder[:index+1]
				entries[directory] = vfs.Info{Path: directory, IsDir: true}
				return nil
			}
		}
		info, exists, err := localInfo(name, fullPath)
		if err != nil {
			return err
		}
		if exists {
			entries[name] = info
		}
		return nil
	})
	if err != nil {
		return vfs.Page{}, vfs.Error(op, options.Prefix, err)
	}
	return makePage(entries, options), nil
}

func (f *FS) Copy(ctx context.Context, source, destination string, options vfs.TransferOptions) (vfs.Info, error) {
	return f.copy(ctx, "copy", source, destination, options)
}

func (f *FS) Move(ctx context.Context, source, destination string, options vfs.TransferOptions) (vfs.Info, error) {
	const op = "move"
	source, destination, err := normalizeTransfer(source, destination, options)
	if err != nil {
		return vfs.Info{}, vfs.Error(op, source+" -> "+destination, err)
	}
	if err := ctx.Err(); err != nil {
		return vfs.Info{}, vfs.Error(op, source+" -> "+destination, err)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	sourcePath, destinationPath, sourceInfo, err := f.prepareTransfer(source, destination, options)
	if err != nil {
		return vfs.Info{}, vfs.Error(op, source+" -> "+destination, err)
	}
	if err := os.MkdirAll(filepath.Dir(destinationPath), directoryMode); err != nil {
		return vfs.Info{}, vfs.Error(op, destination, err)
	}

	if options.NoOverwrite {
		if err := os.Link(sourcePath, destinationPath); err != nil {
			if errors.Is(err, fs.ErrExist) {
				return vfs.Info{}, vfs.Error(op, destination, vfs.ErrAlreadyExists)
			}
			return vfs.Info{}, vfs.Error(op, destination, err)
		}
		if err := os.Remove(sourcePath); err != nil {
			_ = os.Remove(destinationPath)
			return vfs.Info{}, vfs.Error(op, source, err)
		}
	} else if err := os.Rename(sourcePath, destinationPath); err != nil {
		return vfs.Info{}, vfs.Error(op, source+" -> "+destination, err)
	}

	sourceInfo.Path = destination
	sourceInfo.ModifiedAt = time.Now().UTC()
	info, _, err := localInfo(destination, destinationPath)
	if err != nil {
		return vfs.Info{}, vfs.Error(op, destination, err)
	}
	return info, nil
}

func (f *FS) copy(ctx context.Context, op, source, destination string, options vfs.TransferOptions) (vfs.Info, error) {
	source, destination, err := normalizeTransfer(source, destination, options)
	if err != nil {
		return vfs.Info{}, vfs.Error(op, source+" -> "+destination, err)
	}
	if err := ctx.Err(); err != nil {
		return vfs.Info{}, vfs.Error(op, source+" -> "+destination, err)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	sourcePath, destinationPath, sourceInfo, err := f.prepareTransfer(source, destination, options)
	if err != nil {
		return vfs.Info{}, vfs.Error(op, source+" -> "+destination, err)
	}
	sourceFile, err := os.Open(sourcePath)
	if err != nil {
		return vfs.Info{}, vfs.Error(op, source, err)
	}
	defer sourceFile.Close()

	if err := os.MkdirAll(filepath.Dir(destinationPath), directoryMode); err != nil {
		return vfs.Info{}, vfs.Error(op, destination, err)
	}
	written, err := f.writeTemporary(ctx, destinationPath, sourceFile, &sourceInfo.Size)
	if err != nil {
		return vfs.Info{}, vfs.Error(op, destination, err)
	}
	if options.NoOverwrite {
		if err := os.Link(written.temporaryPath, destinationPath); err != nil {
			_ = os.Remove(written.temporaryPath)
			if errors.Is(err, fs.ErrExist) {
				return vfs.Info{}, vfs.Error(op, destination, vfs.ErrAlreadyExists)
			}
			return vfs.Info{}, vfs.Error(op, destination, err)
		}
		_ = os.Remove(written.temporaryPath)
	} else if err := os.Rename(written.temporaryPath, destinationPath); err != nil {
		_ = os.Remove(written.temporaryPath)
		return vfs.Info{}, vfs.Error(op, destination, err)
	}
	info, _, err := localInfo(destination, destinationPath)
	if err != nil {
		return vfs.Info{}, vfs.Error(op, destination, err)
	}
	return info, nil
}

func (f *FS) prepareTransfer(source, destination string, options vfs.TransferOptions) (string, string, vfs.Info, error) {
	sourcePath, err := f.resolve(source)
	if err != nil {
		return "", "", vfs.Info{}, err
	}
	destinationPath, err := f.resolve(destination)
	if err != nil {
		return "", "", vfs.Info{}, err
	}
	sourceInfo, sourceExists, err := localInfo(source, sourcePath)
	if err != nil {
		return "", "", vfs.Info{}, err
	}
	if !sourceExists {
		return "", "", vfs.Info{}, vfs.ErrNotFound
	}
	if sourceInfo.IsDir {
		return "", "", vfs.Info{}, vfs.ErrIsDirectory
	}
	if options.SourceRevision != "" && sourceInfo.Revision != options.SourceRevision {
		return "", "", vfs.Info{}, vfs.ErrConflict
	}

	destinationInfo, destinationExists, err := localInfo(destination, destinationPath)
	if err != nil {
		return "", "", vfs.Info{}, err
	}
	if destinationExists && destinationInfo.IsDir {
		return "", "", vfs.Info{}, vfs.ErrIsDirectory
	}
	if options.NoOverwrite && destinationExists {
		return "", "", vfs.Info{}, vfs.ErrAlreadyExists
	}
	if options.DestinationRevision != "" && (!destinationExists || destinationInfo.Revision != options.DestinationRevision) {
		return "", "", vfs.Info{}, vfs.ErrConflict
	}
	return sourcePath, destinationPath, sourceInfo, nil
}

func (f *FS) resolve(name string) (string, error) {
	fullPath := filepath.Join(f.root, filepath.FromSlash(name))
	relative, err := filepath.Rel(f.root, fullPath)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", vfs.ErrInvalidPath
	}

	current := f.root
	parts := strings.Split(filepath.Clean(relative), string(filepath.Separator))
	for index, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, fs.ErrNotExist) {
			break
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("%w: symbolic links are not allowed", vfs.ErrInvalidPath)
		}
		if index < len(parts)-1 && !info.IsDir() {
			return "", fmt.Errorf("%w: parent is not a directory", vfs.ErrInvalidPath)
		}
	}
	return fullPath, nil
}

type temporaryWrite struct {
	temporaryPath string
}

func (f *FS) writeTemporary(ctx context.Context, destination string, body io.Reader, expectedSize *int64) (temporaryWrite, error) {
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".vfs-*")
	if err != nil {
		return temporaryWrite{}, err
	}
	temporaryPath := temporary.Name()
	cleanup := func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}
	if err := temporary.Chmod(fileMode); err != nil {
		cleanup()
		return temporaryWrite{}, err
	}
	written, err := io.Copy(temporary, &contextReader{ctx: ctx, reader: body})
	if err != nil {
		cleanup()
		return temporaryWrite{}, err
	}
	if expectedSize != nil && written != *expectedSize {
		cleanup()
		return temporaryWrite{}, fmt.Errorf("size mismatch: expected %d bytes, received %d", *expectedSize, written)
	}
	if err := temporary.Sync(); err != nil {
		cleanup()
		return temporaryWrite{}, err
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return temporaryWrite{}, err
	}
	return temporaryWrite{temporaryPath: temporaryPath}, nil
}

func localInfo(name, fullPath string) (vfs.Info, bool, error) {
	file, err := os.Open(fullPath)
	if errors.Is(err, fs.ErrNotExist) {
		return vfs.Info{}, false, nil
	}
	if err != nil {
		return vfs.Info{}, false, err
	}
	defer file.Close()
	info, err := infoFromOpenFile(name, file)
	return info, true, err
}

func infoFromOpenFile(name string, file *os.File) (vfs.Info, error) {
	stat, err := file.Stat()
	if err != nil {
		return vfs.Info{}, err
	}
	info := vfs.Info{
		Path:       name,
		Size:       stat.Size(),
		ModifiedAt: stat.ModTime().UTC(),
		IsDir:      stat.IsDir(),
	}
	if stat.IsDir() {
		return info, nil
	}
	info.Revision = "local:" +
		strconv.FormatInt(stat.ModTime().UnixNano(), 36) + ":" +
		strconv.FormatInt(stat.Size(), 36)
	return info, nil
}

func normalizeTransfer(source, destination string, options vfs.TransferOptions) (string, string, error) {
	source, err := vfs.NormalizePath(source)
	if err != nil {
		return source, destination, err
	}
	destination, err = vfs.NormalizePath(destination)
	if err != nil {
		return source, destination, err
	}
	if source == destination {
		return source, destination, fmt.Errorf("%w: source and destination are equal", vfs.ErrConflict)
	}
	if err := options.Validate(); err != nil {
		return source, destination, err
	}
	return source, destination, nil
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

type limitedReadCloser struct {
	io.Reader
	closer io.Closer
}

func (r *limitedReadCloser) Close() error {
	return r.closer.Close()
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}
