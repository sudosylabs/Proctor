package vfs

import (
	"context"
	"fmt"
	"io"
	"time"
)

const (
	// DefaultListLimit is applied when ListOptions.Limit is zero.
	DefaultListLimit = 1000
	// MaximumListLimit bounds memory use consistently across backends.
	MaximumListLimit = 10000
)

// Capabilities describes guarantees that are not uniformly available across
// local filesystems and object stores.
type Capabilities struct {
	AtomicMove       bool
	ConditionalWrite bool
	RangeRead        bool
}

// Info describes a path at a point in time. Revision is opaque and may only be
// compared for equality or passed back in conditional operation options.
type Info struct {
	Path       string
	Size       int64
	ModifiedAt time.Time
	Revision   string
	IsDir      bool
}

// File is a streaming read result. The caller must close Body.
type File struct {
	Info Info
	Body io.ReadCloser
}

// OpenOptions selects an optional byte range. A zero Length means read to EOF.
type OpenOptions struct {
	Offset int64
	Length int64
}

func (o OpenOptions) Validate() error {
	if o.Offset < 0 || o.Length < 0 {
		return ErrInvalidRange
	}
	return nil
}

// WriteOptions controls file replacement. ExpectedRevision and NoOverwrite
// are mutually exclusive. Size, when non-nil, is verified while streaming.
type WriteOptions struct {
	Size             *int64
	ExpectedRevision string
	NoOverwrite      bool
}

func (o WriteOptions) Validate() error {
	if o.Size != nil && *o.Size < 0 {
		return fmt.Errorf("size must not be negative")
	}
	if o.ExpectedRevision != "" && o.NoOverwrite {
		return fmt.Errorf("expected revision and no-overwrite are mutually exclusive")
	}
	return nil
}

// RemoveOptions controls conditional removal.
type RemoveOptions struct {
	ExpectedRevision string
}

// TransferOptions controls Copy and Move. SourceRevision protects the source
// from concurrent modification. DestinationRevision protects replacement of
// an existing destination and is mutually exclusive with NoOverwrite.
type TransferOptions struct {
	SourceRevision      string
	DestinationRevision string
	NoOverwrite         bool
}

func (o TransferOptions) Validate() error {
	if o.DestinationRevision != "" && o.NoOverwrite {
		return fmt.Errorf("destination revision and no-overwrite are mutually exclusive")
	}
	return nil
}

// ListOptions selects files lexicographically after Cursor. Delimiter may be
// empty for a recursive listing or "/" to synthesize immediate directories.
type ListOptions struct {
	Prefix    string
	Cursor    string
	Delimiter string
	Limit     int
}

// Normalize validates options and applies DefaultListLimit.
func (o ListOptions) Normalize() (ListOptions, error) {
	var err error
	o.Prefix, err = NormalizePrefix(o.Prefix)
	if err != nil {
		return ListOptions{}, err
	}
	if o.Cursor != "" {
		o.Cursor, err = NormalizePrefix(o.Cursor)
		if err != nil {
			return ListOptions{}, err
		}
	}
	if o.Delimiter != "" && o.Delimiter != "/" {
		return ListOptions{}, fmt.Errorf("delimiter must be empty or slash")
	}
	if o.Limit == 0 {
		o.Limit = DefaultListLimit
	}
	if o.Limit < 1 || o.Limit > MaximumListLimit {
		return ListOptions{}, fmt.Errorf("limit must be between 1 and %d", MaximumListLimit)
	}
	return o, nil
}

// Page is a stable lexicographic page. NextCursor is empty on the final page.
type Page struct {
	Entries    []Info
	NextCursor string
}

// FileSystem is the backend-independent VFS contract.
type FileSystem interface {
	Capabilities() Capabilities
	Open(ctx context.Context, path string, options OpenOptions) (*File, error)
	Write(ctx context.Context, path string, body io.Reader, options WriteOptions) (Info, error)
	Stat(ctx context.Context, path string) (Info, error)
	Remove(ctx context.Context, path string, options RemoveOptions) error
	List(ctx context.Context, options ListOptions) (Page, error)
	Copy(ctx context.Context, source, destination string, options TransferOptions) (Info, error)
	Move(ctx context.Context, source, destination string, options TransferOptions) (Info, error)
}
