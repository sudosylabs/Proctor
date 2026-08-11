// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package filecontent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	vfspkg "github.com/sudosylabs/proctor/packages/vfs"
	"github.com/sudosylabs/proctor/server/model"
)

const maximumRevisionObjects = 100

// ErrPurgeLimit indicates that an abandoned revision exceeded the bounded
// cleanup contract and was left untouched for operator investigation.
var ErrPurgeLimit = errors.New("file content: abandoned revision exceeds purge limit")

// Content stores and opens immutable File Revision renditions over VFS.
type Content struct {
	filesystem vfspkg.FileSystem
}

// New constructs stateless File Content over a root-owned VFS.
func New(filesystem vfspkg.FileSystem) (*Content, error) {
	if filesystem == nil {
		return nil, errors.New("file content VFS is required")
	}
	return &Content{filesystem: filesystem}, nil
}

func (c *Content) storeRendition(ctx context.Context, revisionID model.FileRevisionID, renditionID model.FileRenditionID, body io.Reader, size int64) error {
	if c == nil || c.filesystem == nil || !revisionID.IsValid() || !renditionID.IsValid() || body == nil || size < 0 {
		return errors.New("invalid profile-picture rendition")
	}
	_, err := c.filesystem.Write(ctx, renditionKey(revisionID, renditionID), body, vfspkg.WriteOptions{
		Size:        &size,
		NoOverwrite: c.filesystem.Capabilities().ConditionalWrite,
	})
	return sanitize("stage rendition", err)
}

// OpenProfilePictureRendition opens one exact rendition selected by
// authoritative profile-picture metadata.
func (c *Content) OpenProfilePictureRendition(ctx context.Context, revisionID model.FileRevisionID, renditionID model.FileRenditionID) (io.ReadCloser, error) {
	if c == nil || c.filesystem == nil || !revisionID.IsValid() || !renditionID.IsValid() {
		return nil, errors.New("invalid file rendition identity")
	}
	file, err := c.filesystem.Open(ctx, renditionKey(revisionID, renditionID), vfspkg.OpenOptions{})
	if err != nil {
		return nil, sanitize("open rendition", err)
	}
	return file.Body, nil
}

func (c *Content) removeRenditions(ctx context.Context, revisionID model.FileRevisionID, renditionIDs []model.FileRenditionID) error {
	if c == nil || c.filesystem == nil || !revisionID.IsValid() {
		return errors.New("invalid file revision identity")
	}
	for _, renditionID := range renditionIDs {
		if !renditionID.IsValid() {
			return errors.New("invalid file rendition identity")
		}
	}
	var joined error
	for _, renditionID := range renditionIDs {
		err := c.filesystem.Remove(ctx, renditionKey(revisionID, renditionID), vfspkg.RemoveOptions{})
		if errors.Is(err, vfspkg.ErrNotFound) {
			continue
		}
		joined = errors.Join(joined, sanitize("remove rendition", err))
	}
	return joined
}

// RemoveFileRevisionRenditions idempotently removes an exact authoritative
// rendition manifest without discovering sibling content.
func (c *Content) RemoveFileRevisionRenditions(ctx context.Context, revisionID model.FileRevisionID, renditionIDs []model.FileRenditionID) error {
	return c.removeRenditions(ctx, revisionID, renditionIDs)
}

// PurgeAbandonedFileRevision bounds discovery and deletion to one revision
// prefix whose expired upload lease has been durably claimed.
func (c *Content) PurgeAbandonedFileRevision(ctx context.Context, revisionID model.FileRevisionID) error {
	return c.purgeAbandonedRevision(ctx, revisionID)
}

func (c *Content) purgeAbandonedRevision(ctx context.Context, revisionID model.FileRevisionID) error {
	if c == nil || c.filesystem == nil || !revisionID.IsValid() {
		return errors.New("invalid file revision identity")
	}
	prefix := revisionPrefix(revisionID)
	page, err := c.filesystem.List(ctx, vfspkg.ListOptions{Prefix: prefix, Limit: maximumRevisionObjects})
	if err != nil {
		return sanitize("list abandoned revision", err)
	}
	if page.NextCursor != "" {
		return ErrPurgeLimit
	}
	for _, entry := range page.Entries {
		if entry.IsDir {
			continue
		}
		if !strings.HasPrefix(entry.Path, prefix) {
			return errors.New("file content: backend returned object outside revision prefix")
		}
		err = c.filesystem.Remove(ctx, entry.Path, vfspkg.RemoveOptions{})
		if errors.Is(err, vfspkg.ErrNotFound) {
			continue
		}
		if err != nil {
			return sanitize("purge abandoned revision", err)
		}
	}
	return nil
}

func renditionKey(revisionID model.FileRevisionID, renditionID model.FileRenditionID) string {
	return revisionPrefix(revisionID) + renditionID.String() + ".webp"
}

func revisionPrefix(revisionID model.FileRevisionID) string {
	id := revisionID.String()
	return fmt.Sprintf("files/%s/%s/revisions/%s/renditions/", id[:2], id[2:4], id)
}

type operationError struct {
	operation string
	kind      errorKind
	cause     error
}

func (e *operationError) Error() string { return "file content: " + e.operation + " failed" }
func (e *operationError) Unwrap() error { return e.cause }

type errorKind uint8

const (
	errorUnavailable errorKind = iota
	errorNotFound
	errorConflict
)

func sanitize(operation string, err error) error {
	if err == nil {
		return nil
	}
	kind := errorUnavailable
	if errors.Is(err, vfspkg.ErrNotFound) {
		kind = errorNotFound
	} else if errors.Is(err, vfspkg.ErrAlreadyExists) || errors.Is(err, vfspkg.ErrConflict) {
		kind = errorConflict
	}
	return &operationError{operation: operation, kind: kind, cause: err}
}

// IsNotFound reports whether stored content does not exist.
func IsNotFound(err error) bool {
	return hasKind(err, errorNotFound)
}

// IsConflict reports whether immutable content already occupies the selected
// storage identity or a backend concurrency condition rejected the operation.
func IsConflict(err error) bool {
	return hasKind(err, errorConflict)
}

// IsUnavailable reports whether a backend dependency could not complete an
// otherwise valid File Content operation.
func IsUnavailable(err error) bool {
	return hasKind(err, errorUnavailable)
}

func hasKind(err error, kind errorKind) bool {
	var operation *operationError
	return errors.As(err, &operation) && operation.kind == kind
}
