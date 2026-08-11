// Package s3 provides a VFS backend for Amazon S3 and compatible object
// storage services.
//
// S3 does not provide atomic rename. Move is implemented as server-side copy
// followed by removal of the source, and a failed removal may leave both
// objects present. Conditional destination writes are rejected rather than
// emulated with a racy stat-then-write sequence.
package s3

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/sudosylabs/proctor/packages/vfs"
)

// Config configures an S3-compatible backend. Prefix is optional and scopes
// every VFS path to a key prefix inside Bucket.
type Config struct {
	Endpoint     string
	AccessKey    string
	SecretKey    string
	SessionToken string
	Bucket       string
	Prefix       string
	Region       string
	Secure       bool
}

// FS stores files in one S3 bucket.
type FS struct {
	client *minio.Client
	bucket string
	prefix string
}

// New creates an S3-compatible filesystem without performing network I/O.
// The bucket must already exist.
func New(config Config) (*FS, error) {
	if config.Endpoint == "" {
		return nil, vfs.Error("new", "", fmt.Errorf("endpoint is required"))
	}
	client, err := minio.New(config.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(config.AccessKey, config.SecretKey, config.SessionToken),
		Secure: config.Secure,
		Region: config.Region,
	})
	if err != nil {
		return nil, vfs.Error("new", "", err)
	}
	return NewWithClient(client, config.Bucket, config.Prefix)
}

// NewWithClient creates a filesystem from an existing MinIO client.
func NewWithClient(client *minio.Client, bucket, prefix string) (*FS, error) {
	if client == nil {
		return nil, vfs.Error("new", "", fmt.Errorf("client is nil"))
	}
	if bucket == "" {
		return nil, vfs.Error("new", "", fmt.Errorf("bucket is required"))
	}
	normalizedPrefix, err := normalizeStoragePrefix(prefix)
	if err != nil {
		return nil, vfs.Error("new", prefix, err)
	}
	return &FS{client: client, bucket: bucket, prefix: normalizedPrefix}, nil
}

func (f *FS) Capabilities() vfs.Capabilities {
	return vfs.Capabilities{
		AtomicMove:       false,
		ConditionalWrite: false,
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

	info, err := f.client.StatObject(ctx, f.bucket, f.key(name), minio.StatObjectOptions{})
	if err != nil {
		return nil, vfs.Error(op, name, translateError(err))
	}
	if options.Offset > info.Size {
		return nil, vfs.Error(op, name, vfs.ErrInvalidRange)
	}
	getOptions := minio.GetObjectOptions{}
	if options.Offset > 0 || options.Length > 0 {
		end := int64(0)
		if options.Length > 0 {
			end = options.Offset + options.Length - 1
		}
		if err := getOptions.SetRange(options.Offset, end); err != nil {
			return nil, vfs.Error(op, name, vfs.ErrInvalidRange)
		}
	}
	object, err := f.client.GetObject(ctx, f.bucket, f.key(name), getOptions)
	if err != nil {
		return nil, vfs.Error(op, name, translateError(err))
	}
	var body io.ReadCloser = object
	if options.Length > 0 {
		body = &limitedReadCloser{Reader: io.LimitReader(object, options.Length), closer: object}
	}
	return &vfs.File{Info: objectInfo(name, info), Body: body}, nil
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
	if options.ExpectedRevision != "" || options.NoOverwrite {
		return vfs.Info{}, vfs.Error(op, name, vfs.ErrUnsupported)
	}
	if err := ctx.Err(); err != nil {
		return vfs.Info{}, vfs.Error(op, name, err)
	}

	size := int64(-1)
	if options.Size != nil {
		size = *options.Size
	}
	uploaded, err := f.client.PutObject(ctx, f.bucket, f.key(name), body, size, minio.PutObjectOptions{})
	if err != nil {
		return vfs.Info{}, vfs.Error(op, name, translateError(err))
	}
	modifiedAt := uploaded.LastModified.UTC()
	if modifiedAt.IsZero() {
		modifiedAt = time.Now().UTC()
	}
	return vfs.Info{
		Path:       name,
		Size:       uploaded.Size,
		ModifiedAt: modifiedAt,
		Revision:   revision(uploaded.VersionID, uploaded.ETag),
	}, nil
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
	info, err := f.client.StatObject(ctx, f.bucket, f.key(name), minio.StatObjectOptions{})
	if err != nil {
		return vfs.Info{}, vfs.Error(op, name, translateError(err))
	}
	return objectInfo(name, info), nil
}

func (f *FS) Remove(ctx context.Context, name string, options vfs.RemoveOptions) error {
	const op = "remove"
	name, err := vfs.NormalizePath(name)
	if err != nil {
		return vfs.Error(op, name, err)
	}
	if options.ExpectedRevision != "" {
		return vfs.Error(op, name, vfs.ErrUnsupported)
	}
	if err := ctx.Err(); err != nil {
		return vfs.Error(op, name, err)
	}
	if _, err := f.client.StatObject(ctx, f.bucket, f.key(name), minio.StatObjectOptions{}); err != nil {
		return vfs.Error(op, name, translateError(err))
	}
	if err := f.client.RemoveObject(ctx, f.bucket, f.key(name), minio.RemoveObjectOptions{}); err != nil {
		return vfs.Error(op, name, translateError(err))
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

	listOptions := minio.ListObjectsOptions{
		Prefix:     f.keyPrefix(options.Prefix),
		Recursive:  options.Delimiter == "",
		MaxKeys:    options.Limit + 1,
		StartAfter: f.keyCursor(options.Cursor),
	}
	listContext, cancel := context.WithCancel(ctx)
	defer cancel()
	entries := make([]vfs.Info, 0, options.Limit+1)
	enough := false
	for info := range f.client.ListObjects(listContext, f.bucket, listOptions) {
		if info.Err != nil {
			if enough && errors.Is(info.Err, context.Canceled) {
				continue
			}
			return vfs.Page{}, vfs.Error(op, options.Prefix, translateError(info.Err))
		}
		if enough {
			continue
		}
		name, ok := f.name(info.Key)
		if !ok || name == "" {
			continue
		}
		entry := objectInfo(name, info)
		if strings.HasSuffix(info.Key, "/") {
			entry.IsDir = true
			entry.Size = 0
			entry.Revision = ""
		}
		entries = append(entries, entry)
		if len(entries) > options.Limit {
			enough = true
			cancel()
		}
	}

	page := vfs.Page{Entries: entries}
	if len(page.Entries) > options.Limit {
		page.Entries = page.Entries[:options.Limit]
		page.NextCursor = page.Entries[len(page.Entries)-1].Path
	}
	return page, nil
}

func (f *FS) Copy(ctx context.Context, source, destination string, options vfs.TransferOptions) (vfs.Info, error) {
	const op = "copy"
	source, destination, err := normalizeTransfer(source, destination, options)
	if err != nil {
		return vfs.Info{}, vfs.Error(op, source+" -> "+destination, err)
	}
	if options.NoOverwrite || options.DestinationRevision != "" {
		return vfs.Info{}, vfs.Error(op, destination, vfs.ErrUnsupported)
	}
	if err := ctx.Err(); err != nil {
		return vfs.Info{}, vfs.Error(op, source+" -> "+destination, err)
	}

	sourceOptions := minio.CopySrcOptions{Bucket: f.bucket, Object: f.key(source)}
	if options.SourceRevision != "" {
		kind, value, ok := parseRevision(options.SourceRevision)
		if !ok {
			return vfs.Info{}, vfs.Error(op, source, vfs.ErrConflict)
		}
		if kind == "version" {
			sourceOptions.VersionID = value
		} else {
			sourceOptions.MatchETag = value
		}
	}
	uploaded, err := f.client.CopyObject(ctx,
		minio.CopyDestOptions{Bucket: f.bucket, Object: f.key(destination)},
		sourceOptions,
	)
	if err != nil {
		return vfs.Info{}, vfs.Error(op, source+" -> "+destination, translateError(err))
	}
	modifiedAt := uploaded.LastModified.UTC()
	if modifiedAt.IsZero() {
		modifiedAt = time.Now().UTC()
	}
	return vfs.Info{
		Path:       destination,
		Size:       uploaded.Size,
		ModifiedAt: modifiedAt,
		Revision:   revision(uploaded.VersionID, uploaded.ETag),
	}, nil
}

func (f *FS) Move(ctx context.Context, source, destination string, options vfs.TransferOptions) (vfs.Info, error) {
	info, err := f.Copy(ctx, source, destination, options)
	if err != nil {
		return vfs.Info{}, err
	}
	if err := f.Remove(ctx, source, vfs.RemoveOptions{}); err != nil {
		return vfs.Info{}, vfs.Error("move", source+" -> "+destination, err)
	}
	return info, nil
}

func (f *FS) key(name string) string {
	return f.prefix + name
}

func (f *FS) keyPrefix(prefix string) string {
	return f.prefix + prefix
}

func (f *FS) keyCursor(cursor string) string {
	if cursor == "" {
		return ""
	}
	return f.prefix + cursor
}

func (f *FS) name(key string) (string, bool) {
	if !strings.HasPrefix(key, f.prefix) {
		return "", false
	}
	return strings.TrimPrefix(key, f.prefix), true
}

func normalizeStoragePrefix(prefix string) (string, error) {
	prefix = strings.TrimPrefix(prefix, "/")
	if prefix == "" {
		return "", nil
	}
	normalized, err := vfs.NormalizePrefix(prefix)
	if err != nil {
		return "", err
	}
	if !strings.HasSuffix(normalized, "/") {
		normalized += "/"
	}
	return normalized, nil
}

func objectInfo(name string, info minio.ObjectInfo) vfs.Info {
	return vfs.Info{
		Path:       name,
		Size:       info.Size,
		ModifiedAt: info.LastModified.UTC(),
		Revision:   revision(info.VersionID, info.ETag),
	}
}

func revision(versionID, etag string) string {
	if versionID != "" {
		return "version:" + versionID
	}
	if etag != "" {
		return "etag:" + strings.Trim(etag, `"`)
	}
	return ""
}

func parseRevision(value string) (kind, revisionValue string, ok bool) {
	kind, revisionValue, ok = strings.Cut(value, ":")
	if !ok || revisionValue == "" || (kind != "version" && kind != "etag") {
		return "", "", false
	}
	return kind, revisionValue, true
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

type limitedReadCloser struct {
	io.Reader
	closer io.Closer
}

func (r *limitedReadCloser) Close() error {
	return r.closer.Close()
}

func translateError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	response := minio.ToErrorResponse(err)
	switch response.Code {
	case "NoSuchKey", "NoSuchObject", "NoSuchBucket", "NotFound":
		return errors.Join(vfs.ErrNotFound, err)
	case "PreconditionFailed", "ConditionalRequestConflict":
		return errors.Join(vfs.ErrConflict, err)
	case "InvalidRange", "RequestedRangeNotSatisfiable":
		return errors.Join(vfs.ErrInvalidRange, err)
	default:
		return err
	}
}
