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
	root    string
	mu      sync.RWMutex
	staging map[string]fs.FileInfo
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

func (f *FS) Write(ctx context.Context, name string, body io.Reader, options vfs.WriteOptions) (_ vfs.Info, resultErr error) {
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

	temporary, err := f.prepareWrite(ctx, name, options)
	if err != nil {
		return vfs.Info{}, vfs.Error(op, name, err)
	}
	defer func() {
		if err := f.discardTemporary(temporary.Name()); err != nil {
			resultErr = errors.Join(resultErr, vfs.Error(op, name, err))
		}
	}()
	if err := writeTemporary(ctx, temporary, body, options.Size); err != nil {
		return vfs.Info{}, vfs.Error(op, name, err)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return vfs.Info{}, vfs.Error(op, name, err)
	}
	fullPath, err := f.writeDestination(name, options)
	if err != nil {
		return vfs.Info{}, vfs.Error(op, name, err)
	}
	info, err := f.publishTemporary(name, fullPath, temporary.Name(), options.NoOverwrite)
	return info, vfs.Error(op, name, err)
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

	root, exists, err := f.listRoot(ctx, options.Prefix)
	if err != nil {
		return vfs.Page{}, vfs.Error(op, options.Prefix, err)
	}
	if !exists {
		return vfs.Page{Entries: []vfs.Info{}}, nil
	}
	entries := make(map[string]vfs.Info)
	err = filepath.WalkDir(root, func(fullPath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if fullPath == root {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if _, staged := f.staging[fullPath]; staged {
			return nil
		}
		if len(f.staging) > 0 && !entry.IsDir() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if f.isStaged(info) {
				return nil
			}
		}

		relative, err := filepath.Rel(f.root, fullPath)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(relative)
		if entry.IsDir() {
			// A lexical prefix can end in the middle of a directory name.
			// Descend only if this directory can contain a matching file.
			directory := name + "/"
			if !strings.HasPrefix(directory, options.Prefix) && !strings.HasPrefix(options.Prefix, directory) {
				return filepath.SkipDir
			}
			return nil
		}
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

func (f *FS) copy(ctx context.Context, op, source, destination string, options vfs.TransferOptions) (_ vfs.Info, resultErr error) {
	source, destination, err := normalizeTransfer(source, destination, options)
	if err != nil {
		return vfs.Info{}, vfs.Error(op, source+" -> "+destination, err)
	}
	if err := ctx.Err(); err != nil {
		return vfs.Info{}, vfs.Error(op, source+" -> "+destination, err)
	}

	sourceFile, temporary, sourceInfo, err := f.prepareCopy(ctx, source, destination, options)
	if err != nil {
		return vfs.Info{}, vfs.Error(op, source+" -> "+destination, err)
	}
	defer sourceFile.Close()
	defer func() {
		if err := f.discardTemporary(temporary.Name()); err != nil {
			resultErr = errors.Join(resultErr, vfs.Error(op, destination, err))
		}
	}()
	if err := writeTemporary(ctx, temporary, sourceFile, &sourceInfo.Size); err != nil {
		return vfs.Info{}, vfs.Error(op, destination, err)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return vfs.Info{}, vfs.Error(op, destination, err)
	}
	_, destinationPath, _, err := f.prepareTransfer(source, destination, options)
	if err != nil {
		return vfs.Info{}, vfs.Error(op, source+" -> "+destination, err)
	}
	info, err := f.publishTemporary(destination, destinationPath, temporary.Name(), options.NoOverwrite)
	return info, vfs.Error(op, destination, err)
}

func (f *FS) prepareWrite(ctx context.Context, name string, options vfs.WriteOptions) (*os.File, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	destination, err := f.writeDestination(name, options)
	if err != nil {
		return nil, err
	}
	return f.newTemporary(destination)
}

func (f *FS) writeDestination(name string, options vfs.WriteOptions) (string, error) {
	fullPath, err := f.resolve(name)
	if err != nil {
		return "", err
	}
	current, exists, err := localInfo(name, fullPath)
	if err != nil {
		return "", err
	}
	if exists && current.IsDir {
		return "", vfs.ErrIsDirectory
	}
	if options.NoOverwrite && exists {
		return "", vfs.ErrAlreadyExists
	}
	if options.ExpectedRevision != "" && (!exists || current.Revision != options.ExpectedRevision) {
		return "", vfs.ErrConflict
	}
	return fullPath, nil
}

func (f *FS) prepareCopy(ctx context.Context, source, destination string, options vfs.TransferOptions) (*os.File, *os.File, vfs.Info, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, nil, vfs.Info{}, err
	}
	sourcePath, destinationPath, sourceInfo, err := f.prepareTransfer(source, destination, options)
	if err != nil {
		return nil, nil, vfs.Info{}, err
	}
	sourceFile, err := os.Open(sourcePath)
	if err != nil {
		return nil, nil, vfs.Info{}, err
	}
	temporary, err := f.newTemporary(destinationPath)
	if err != nil {
		return nil, nil, vfs.Info{}, errors.Join(err, sourceFile.Close())
	}
	return sourceFile, temporary, sourceInfo, nil
}

// newTemporary and publishTemporary run with the publication lock held. Exact
// temporary paths are hidden, without reserving any otherwise valid VFS name.
func (f *FS) newTemporary(destination string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(destination), directoryMode); err != nil {
		return nil, err
	}
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".vfs-*")
	if err != nil {
		return nil, err
	}
	info, err := temporary.Stat()
	if err != nil {
		return nil, errors.Join(err, temporary.Close(), os.Remove(temporary.Name()))
	}
	if f.staging == nil {
		f.staging = make(map[string]fs.FileInfo)
	}
	f.staging[temporary.Name()] = info
	return temporary, nil
}

// Filesystem aliases (for example, on case-insensitive volumes) must not expose
// an in-progress object whose registered path uses a different spelling.
func (f *FS) isStaged(info fs.FileInfo) bool {
	for _, staged := range f.staging {
		if os.SameFile(staged, info) {
			return true
		}
	}
	return false
}

func (f *FS) publishTemporary(name, destination, temporary string, noOverwrite bool) (vfs.Info, error) {
	if noOverwrite {
		if err := os.Link(temporary, destination); err != nil {
			if errors.Is(err, fs.ErrExist) {
				return vfs.Info{}, vfs.ErrAlreadyExists
			}
			return vfs.Info{}, err
		}
		if err := os.Remove(temporary); err != nil {
			return vfs.Info{}, err
		}
	} else if err := os.Rename(temporary, destination); err != nil {
		return vfs.Info{}, err
	}
	delete(f.staging, temporary)
	info, _, err := localInfo(name, destination)
	return info, err
}

// Cleanup does not inherit cancellation from the interrupted write. If removal
// itself fails, retain the registration so the unfinished bytes stay invisible.
func (f *FS) discardTemporary(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, staged := f.staging[name]; !staged {
		return nil
	}
	if err := os.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	delete(f.staging, name)
	return nil
}

// Start inside the fully specified directory portion of the lexical prefix.
// Missing directories, files, and observed symlinks contain no listable matches.
func (f *FS) listRoot(ctx context.Context, prefix string) (string, bool, error) {
	lastSlash := strings.LastIndexByte(prefix, '/')
	if lastSlash < 0 {
		return f.root, true, nil
	}
	current := f.root
	for _, part := range strings.Split(prefix[:lastSlash], "/") {
		if err := ctx.Err(); err != nil {
			return "", false, err
		}
		// Match directory names byte-for-byte, as a walk from the root would.
		// Opening the prefix directly could follow a filesystem case alias and
		// change the VFS's lexical-prefix results.
		entries, err := os.ReadDir(current)
		if err != nil {
			return "", false, err
		}
		index := sort.Search(len(entries), func(i int) bool { return entries[i].Name() >= part })
		if index == len(entries) || entries[index].Name() != part || !entries[index].IsDir() || entries[index].Type()&os.ModeSymlink != 0 {
			return "", false, nil
		}
		current = filepath.Join(current, part)
	}
	return current, true, nil
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
		if _, staged := f.staging[current]; staged {
			return "", vfs.ErrNotFound
		}
		info, err := os.Lstat(current)
		if errors.Is(err, fs.ErrNotExist) {
			break
		}
		if err != nil {
			return "", err
		}
		if !info.IsDir() && f.isStaged(info) {
			return "", vfs.ErrNotFound
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

func writeTemporary(ctx context.Context, temporary *os.File, body io.Reader, expectedSize *int64) (resultErr error) {
	defer func() { resultErr = errors.Join(resultErr, temporary.Close()) }()
	if err := temporary.Chmod(fileMode); err != nil {
		return err
	}
	written, err := io.Copy(temporary, &contextReader{ctx: ctx, reader: body})
	if err != nil {
		return err
	}
	if expectedSize != nil && written != *expectedSize {
		return fmt.Errorf("size mismatch: expected %d bytes, received %d", *expectedSize, written)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return temporary.Sync()
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
