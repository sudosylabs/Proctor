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
	"strings"

	"github.com/sudosylabs/proctor/packages/vfs"
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

// StageAttemptWorkspaceObject streams one bounded private file of exactly
// declaredSize bytes to a new exact opaque identity while hashing it. The
// bytes never touch a local spool: candidate answers reach only the VFS. A
// body whose length differs from declaredSize fails the write or is removed
// before an error is returned. The object is not authoritative or
// discoverable; PostgreSQL publication remains a separate fenced mutation,
// and a caller that rejects the returned digest reclaims the reserved
// identity.
func (c *Content) StageAttemptWorkspaceObject(ctx context.Context, objectID model.AttemptWorkspaceObjectID,
	body io.Reader, declaredSize int64, mediaType string,
) (*model.AttemptWorkspaceContent, error) {
	mediaType = strings.TrimSpace(mediaType)
	if c == nil || c.filesystem == nil {
		return nil, errors.New("Attempt Workspace content dependency is unavailable")
	}
	if !objectID.IsValid() || body == nil || declaredSize < 0 ||
		declaredSize > model.AttemptWorkspaceMaximumFileBytes || mediaType == "" || len(mediaType) > 255 {
		return nil, invalidAttemptWorkspaceContent(errors.New("invalid metadata"))
	}
	stream := newExactSizeReader(body, declaredSize)
	content := func() (*model.AttemptWorkspaceContent, error) {
		if err := stream.finish(); err != nil {
			return nil, invalidAttemptWorkspaceContent(err)
		}
		staged := &model.AttemptWorkspaceContent{MediaType: mediaType, SizeBytes: declaredSize, SHA256: stream.checksum()}
		if err := staged.Validate(); err != nil {
			return nil, invalidAttemptWorkspaceContent(err)
		}
		return staged, nil
	}
	key := attemptWorkspaceObjectKey(objectID)
	conditional := c.filesystem.Capabilities().ConditionalWrite
	if !conditional {
		// Without conditional writes, an existing object is compared with
		// the incoming bytes instead of being overwritten.
		_, statErr := c.filesystem.Stat(ctx, key)
		switch {
		case statErr == nil:
			staged, err := content()
			if err != nil {
				return nil, err
			}
			matching, openErr := c.openMatchingAttemptWorkspaceObject(ctx, objectID, staged.SizeBytes, staged.SHA256)
			switch {
			case openErr != nil:
				return nil, sanitize("stage Attempt Workspace object", openErr)
			case !matching:
				return nil, sanitize("stage Attempt Workspace object", vfs.ErrConflict)
			}
			return staged, nil
		case !errors.Is(statErr, vfs.ErrNotFound):
			return nil, sanitize("stage Attempt Workspace object", statErr)
		}
	}
	size := declaredSize
	info, writeErr := c.filesystem.Write(ctx, key, stream, vfs.WriteOptions{Size: &size, NoOverwrite: conditional})
	staged, contentErr := content()
	if contentErr != nil {
		// An object already present under a conditional write belongs to an
		// earlier stage and is left untouched.
		occupied := errors.Is(writeErr, vfs.ErrAlreadyExists) || errors.Is(writeErr, vfs.ErrConflict)
		if !occupied {
			if removeErr := c.filesystem.Remove(ctx, key, vfs.RemoveOptions{}); removeErr != nil && !errors.Is(removeErr, vfs.ErrNotFound) {
				return nil, errors.Join(contentErr, sanitize("remove invalid Attempt Workspace object", removeErr))
			}
		}
		return nil, contentErr
	}
	if writeErr != nil {
		if matching, verifyErr := c.openMatchingAttemptWorkspaceObject(ctx, objectID, staged.SizeBytes, staged.SHA256); verifyErr == nil && matching {
			return staged, nil
		}
		return nil, sanitize("stage Attempt Workspace object", writeErr)
	}
	if info.Size != size {
		return nil, sanitize("stage Attempt Workspace object", errors.New("backend size mismatch"))
	}
	return staged, nil
}

func (c *Content) openMatchingAttemptWorkspaceObject(ctx context.Context, objectID model.AttemptWorkspaceObjectID,
	size int64, checksum string,
) (bool, error) {
	file, err := c.filesystem.Open(ctx, attemptWorkspaceObjectKey(objectID), vfs.OpenOptions{})
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
	file, err := c.filesystem.Open(ctx, attemptWorkspaceObjectKey(objectID), vfs.OpenOptions{})
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
	err := c.filesystem.Remove(ctx, attemptWorkspaceObjectKey(objectID), vfs.RemoveOptions{})
	if errors.Is(err, vfs.ErrNotFound) {
		return nil
	}
	return sanitize("remove Attempt Workspace object", err)
}

// PurgeRetiredAttemptWorkspaceObject removes the exact immutable key, then
// observes its absence independently. A backend acknowledgement alone cannot
// be reported as verified purge. This says nothing about already-open streams,
// media erasure, backup copies, or historical object versions.
func (c *Content) PurgeRetiredAttemptWorkspaceObject(ctx context.Context, objectID model.AttemptWorkspaceObjectID) error {
	if err := c.RemoveAttemptWorkspaceObject(ctx, objectID); err != nil {
		return err
	}
	_, err := c.filesystem.Stat(ctx, attemptWorkspaceObjectKey(objectID))
	if errors.Is(err, vfs.ErrNotFound) {
		return nil
	}
	if err != nil {
		return sanitize("verify retired Attempt Workspace object absence", err)
	}
	return errors.New("retired Attempt Workspace object remains present")
}

func attemptWorkspaceObjectKey(objectID model.AttemptWorkspaceObjectID) string {
	id := objectID.String()
	return fmt.Sprintf("exam-attempt-workspace/%s/%s/objects/%s", id[:2], id[2:4], id)
}
