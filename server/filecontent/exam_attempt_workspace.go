// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package filecontent

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	vfspkg "github.com/sudosylabs/proctor/packages/vfs"
	"github.com/sudosylabs/proctor/server/model"
)

// AttemptWorkspaceInvalidContentError identifies caller-controlled content
// that failed bounded validation without exposing backend failure details.
type AttemptWorkspaceInvalidContentError struct{ cause error }

func (err *AttemptWorkspaceInvalidContentError) Error() string {
	return "invalid Attempt Workspace content"
}

func (err *AttemptWorkspaceInvalidContentError) Unwrap() error { return err.cause }

func (err *AttemptWorkspaceInvalidContentError) InvalidAttemptWorkspaceContent() bool {
	return true
}

func invalidAttemptWorkspaceContent(cause error) error {
	return &AttemptWorkspaceInvalidContentError{cause: cause}
}

// StageAttemptWorkspaceObject streams one bounded private file to a new exact
// opaque identity. It does not make the object authoritative or discoverable;
// PostgreSQL publication remains a separate fenced mutation.
func (c *Content) StageAttemptWorkspaceObject(ctx context.Context, objectID model.AttemptWorkspaceObjectID,
	body io.Reader, declaredSize int64, mediaType string,
) (*model.AttemptWorkspaceContent, error) {
	mediaType = strings.TrimSpace(mediaType)
	if c == nil || c.filesystem == nil {
		return nil, errors.New("Attempt Workspace content dependency is unavailable")
	}
	if !objectID.IsValid() || body == nil || declaredSize < -1 ||
		declaredSize > model.AttemptWorkspaceMaximumFileBytes || mediaType == "" || len(mediaType) > 255 {
		return nil, invalidAttemptWorkspaceContent(errors.New("invalid metadata"))
	}
	spool, err := os.CreateTemp("", "proctor-attempt-workspace-*")
	if err != nil {
		return nil, sanitize("create Attempt Workspace spool", err)
	}
	spoolPath := spool.Name()
	defer func() {
		_ = spool.Close()
		_ = os.Remove(spoolPath)
	}()
	hash := sha256.New()
	limited := &io.LimitedReader{R: body, N: model.AttemptWorkspaceMaximumFileBytes + 1}
	size, err := io.CopyBuffer(io.MultiWriter(spool, hash), limited, make([]byte, 32<<10))
	if err != nil || size > model.AttemptWorkspaceMaximumFileBytes || limited.N == 0 || declaredSize >= 0 && size != declaredSize {
		return nil, invalidAttemptWorkspaceContent(errors.New("content size mismatch"))
	}
	content := &model.AttemptWorkspaceContent{MediaType: mediaType, SizeBytes: size,
		SHA256: fmt.Sprintf("%x", hash.Sum(nil))}
	if err = content.Validate(); err != nil {
		return nil, invalidAttemptWorkspaceContent(err)
	}
	key := attemptWorkspaceObjectKey(objectID)
	conditional := c.filesystem.Capabilities().ConditionalWrite
	if !conditional {
		matching, openErr := c.openMatchingAttemptWorkspaceObject(ctx, objectID, content.SizeBytes, content.SHA256)
		switch {
		case openErr == nil && matching:
			return content, nil
		case openErr == nil:
			return nil, sanitize("stage Attempt Workspace object", vfspkg.ErrConflict)
		case !errors.Is(openErr, vfspkg.ErrNotFound):
			return nil, sanitize("stage Attempt Workspace object", openErr)
		}
	}
	if _, err = spool.Seek(0, io.SeekStart); err != nil {
		return nil, sanitize("rewind Attempt Workspace spool", err)
	}
	info, err := c.filesystem.Write(ctx, key, spool, vfspkg.WriteOptions{Size: &size, NoOverwrite: conditional})
	if err != nil {
		if matching, verifyErr := c.openMatchingAttemptWorkspaceObject(ctx, objectID, content.SizeBytes, content.SHA256); verifyErr == nil && matching {
			return content, nil
		}
		return nil, sanitize("stage Attempt Workspace object", err)
	}
	if info.Size != size {
		return nil, sanitize("stage Attempt Workspace object", errors.New("backend size mismatch"))
	}
	return content, nil
}

func (c *Content) openMatchingAttemptWorkspaceObject(ctx context.Context, objectID model.AttemptWorkspaceObjectID,
	size int64, checksum string,
) (bool, error) {
	file, err := c.filesystem.Open(ctx, attemptWorkspaceObjectKey(objectID), vfspkg.OpenOptions{})
	if err != nil {
		return false, err
	}
	defer file.Body.Close()
	if file.Info.Size != size {
		return false, nil
	}
	digest := sha256.New()
	read, err := io.Copy(digest, io.LimitReader(file.Body, model.AttemptWorkspaceMaximumFileBytes+1))
	if err != nil {
		return false, err
	}
	return read == size && fmt.Sprintf("%x", digest.Sum(nil)) == checksum, nil
}

// OpenAttemptWorkspaceObject opens only an exact opaque object selected by
// authorized PostgreSQL metadata.
func (c *Content) OpenAttemptWorkspaceObject(ctx context.Context,
	objectID model.AttemptWorkspaceObjectID,
) (io.ReadCloser, error) {
	if c == nil || c.filesystem == nil || !objectID.IsValid() {
		return nil, errors.New("invalid Attempt Workspace object identity")
	}
	file, err := c.filesystem.Open(ctx, attemptWorkspaceObjectKey(objectID), vfspkg.OpenOptions{})
	if err != nil {
		return nil, sanitize("open Attempt Workspace object", err)
	}
	return file.Body, nil
}

// RemoveAttemptWorkspaceObject idempotently removes one exact opaque object
// after persistence has durably claimed it for cleanup.
func (c *Content) RemoveAttemptWorkspaceObject(ctx context.Context,
	objectID model.AttemptWorkspaceObjectID,
) error {
	if c == nil || c.filesystem == nil || !objectID.IsValid() {
		return errors.New("invalid Attempt Workspace object identity")
	}
	err := c.filesystem.Remove(ctx, attemptWorkspaceObjectKey(objectID), vfspkg.RemoveOptions{})
	if errors.Is(err, vfspkg.ErrNotFound) {
		return nil
	}
	return sanitize("remove Attempt Workspace object", err)
}

func attemptWorkspaceObjectKey(objectID model.AttemptWorkspaceObjectID) string {
	id := objectID.String()
	return fmt.Sprintf("exam-attempt-workspace/%s/%s/objects/%s", id[:2], id[2:4], id)
}
