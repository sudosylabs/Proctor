package vfs

import (
	"errors"
	"fmt"
)

var (
	// ErrNotFound indicates that a requested path does not exist.
	ErrNotFound = errors.New("vfs: not found")
	// ErrAlreadyExists indicates that an operation required a new path.
	ErrAlreadyExists = errors.New("vfs: already exists")
	// ErrConflict indicates that a supplied revision does not match.
	ErrConflict = errors.New("vfs: revision conflict")
	// ErrInvalidPath indicates that a path is empty, absolute, or unsafe.
	ErrInvalidPath = errors.New("vfs: invalid path")
	// ErrInvalidRange indicates that a read range is outside the file.
	ErrInvalidRange = errors.New("vfs: invalid range")
	// ErrIsDirectory indicates that a file operation targeted a directory.
	ErrIsDirectory = errors.New("vfs: path is a directory")
	// ErrUnsupported indicates that a backend cannot provide an operation or
	// requested guarantee.
	ErrUnsupported = errors.New("vfs: unsupported operation")
)

// OpError adds operation and path context while preserving errors.Is and
// errors.As behavior for the underlying error.
type OpError struct {
	Op   string
	Path string
	Err  error
}

func (e *OpError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Path == "" {
		return fmt.Sprintf("vfs %s: %v", e.Op, e.Err)
	}
	return fmt.Sprintf("vfs %s %q: %v", e.Op, e.Path, e.Err)
}

func (e *OpError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Error annotates err with a VFS operation and path. A nil error remains nil.
func Error(op, path string, err error) error {
	if err == nil {
		return nil
	}
	return &OpError{Op: op, Path: path, Err: err}
}
