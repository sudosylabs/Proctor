# VFS

`vfs` is a portable, streaming virtual file storage contract for Go. It is
designed for applications that need the same file operations over local,
in-memory, and object-storage backends without leaking application models into
the storage layer.

The module is currently pre-v1. Its API may change while the local and object
storage semantics are being established.

## Guarantees

- Portable slash-separated paths rooted inside the selected backend.
- Streaming reads and writes.
- Opaque revisions for conditional replacement and removal.
- Range reads.
- Stable lexicographic listing and pagination.
- Explicit backend capabilities.
- Typed errors compatible with `errors.Is` and `errors.As`.
- Conformance tests reusable by backend implementations.

VFS stores files. It does not manage users, authorization, retention,
application metadata, templates, database records, or caching.

## Backends

- `memory`: concurrency-safe ephemeral storage for tests and short-lived data.
- `local`: storage rooted in a local directory with atomic temporary-file
  replacement and observed-symlink rejection.
- `s3`: Amazon S3 and compatible object storage using server-side copy.

The S3 backend reports conditional destination writes and atomic moves as
unsupported. An S3 move is a copy followed by deletion, so a failed deletion
can leave both paths present.

## Example

```go
package main

import (
	"bytes"
	"context"
	"io"

	"github.com/sudosylabs/proctor/packages/vfs"
	"github.com/sudosylabs/proctor/packages/vfs/memory"
)

func example() error {
	ctx := context.Background()
	filesystem := memory.New()

	info, err := filesystem.Write(
		ctx,
		"exams/example/answer.txt",
		bytes.NewBufferString("answer"),
		vfs.WriteOptions{NoOverwrite: true},
	)
	if err != nil {
		return err
	}

	file, err := filesystem.Open(
		ctx,
		info.Path,
		vfs.OpenOptions{},
	)
	if err != nil {
		return err
	}
	defer file.Body.Close()

	_, err = io.ReadAll(file.Body)
	return err
}
```

## Path model

Paths are relative and use `/` on every operating system. Absolute paths,
backslashes, NUL bytes, and parent traversal are rejected. Directory entries
returned by delimited listings are synthesized; the core contract does not
require persistent empty directories.

## Revisions

`Info.Revision` is an opaque backend value. Consumers must not parse it. It may
be compared for equality or passed back through conditional operation options.
The memory backend uses a SHA-256 content revision, the local backend uses file
metadata, and S3 uses a provider version ID or entity tag. Revisions are
concurrency tokens, not portable content checksums.

## Local backend security

The local backend rejects symbolic links observed beneath its root. The root
must still be writable only by the application. Portable Go filesystem APIs
cannot eliminate every time-of-check to time-of-use race caused by another
hostile local process.

## Backend conformance

Third-party backends can run the exported suite:

```go
func TestConformance(t *testing.T) {
	vfstest.Run(t, func(t *testing.T) vfs.FileSystem {
		return newBackend(t)
	})
}
```

The S3 adapter runs the same suite when the following integration-test
environment variables are present:

```text
VFS_S3_ENDPOINT
VFS_S3_BUCKET
VFS_S3_ACCESS_KEY
VFS_S3_SECRET_KEY
VFS_S3_SESSION_TOKEN  (optional)
VFS_S3_REGION         (optional)
VFS_S3_SECURE         (default: true)
```

## License

Apache License 2.0.
