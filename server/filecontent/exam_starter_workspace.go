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

	vfspkg "github.com/sudosylabs/proctor/packages/vfs"
	"github.com/sudosylabs/proctor/server/model"
)

// StarterWorkspaceInvalidContentError identifies caller-controlled content
// that failed bounded validation. The application can distinguish it from an
// opaque storage failure without depending on this adapter package.
type StarterWorkspaceInvalidContentError struct{ cause error }

func (err *StarterWorkspaceInvalidContentError) Error() string {
	return "invalid Starter Workspace content"
}
func (err *StarterWorkspaceInvalidContentError) Unwrap() error { return err.cause }
func (err *StarterWorkspaceInvalidContentError) InvalidStarterWorkspaceContent() bool {
	return true
}

func invalidStarterWorkspaceContent(cause error) error {
	return &StarterWorkspaceInvalidContentError{cause: cause}
}

// StageStarterWorkspaceObject streams one bounded file to a new exact opaque
// object identity. PostgreSQL publication remains a separate authoritative
// step; successfully staged bytes are not application-visible by themselves.
func (c *Content) StageStarterWorkspaceObject(ctx context.Context, objectID model.StarterWorkspaceObjectID, body io.Reader, declaredSize int64, mediaType string) (*model.StarterWorkspaceContent, error) {
	mediaType = strings.TrimSpace(mediaType)
	if c == nil || c.filesystem == nil {
		return nil, errors.New("Starter Workspace content dependency is unavailable")
	}
	if !objectID.IsValid() || body == nil || declaredSize < -1 ||
		declaredSize > model.StarterWorkspaceMaximumFileBytes || mediaType == "" || len(mediaType) > 255 {
		return nil, invalidStarterWorkspaceContent(errors.New("invalid metadata"))
	}
	hash := sha256.New()
	limited := &io.LimitedReader{R: body, N: model.StarterWorkspaceMaximumFileBytes + 1}
	reader := io.TeeReader(limited, hash)
	options := vfspkg.WriteOptions{NoOverwrite: c.filesystem.Capabilities().ConditionalWrite}
	info, err := c.filesystem.Write(ctx, starterWorkspaceObjectKey(objectID), reader, options)
	if err != nil {
		return nil, sanitize("stage Starter Workspace object", err)
	}
	if info.Size > model.StarterWorkspaceMaximumFileBytes || limited.N == 0 || declaredSize >= 0 && info.Size != declaredSize {
		_ = c.filesystem.Remove(ctx, starterWorkspaceObjectKey(objectID), vfspkg.RemoveOptions{})
		return nil, invalidStarterWorkspaceContent(errors.New("content size mismatch"))
	}
	return &model.StarterWorkspaceContent{MediaType: mediaType, SizeBytes: info.Size, SHA256: fmt.Sprintf("%x", hash.Sum(nil))}, nil
}

// OpenStarterWorkspaceObject opens only an exact opaque object selected by
// authorized PostgreSQL metadata.
func (c *Content) OpenStarterWorkspaceObject(ctx context.Context, objectID model.StarterWorkspaceObjectID) (io.ReadCloser, error) {
	if c == nil || c.filesystem == nil || !objectID.IsValid() {
		return nil, fmt.Errorf("invalid Starter Workspace object identity")
	}
	file, err := c.filesystem.Open(ctx, starterWorkspaceObjectKey(objectID), vfspkg.OpenOptions{})
	if err != nil {
		return nil, sanitize("open Starter Workspace object", err)
	}
	return file.Body, nil
}

// RemoveStarterWorkspaceObject idempotently removes one exact opaque object
// after persistence has durably established cleanup eligibility.
func (c *Content) RemoveStarterWorkspaceObject(ctx context.Context, objectID model.StarterWorkspaceObjectID) error {
	if c == nil || c.filesystem == nil || !objectID.IsValid() {
		return fmt.Errorf("invalid Starter Workspace object identity")
	}
	err := c.filesystem.Remove(ctx, starterWorkspaceObjectKey(objectID), vfspkg.RemoveOptions{})
	if errors.Is(err, vfspkg.ErrNotFound) {
		return nil
	}
	return sanitize("remove Starter Workspace object", err)
}

func starterWorkspaceObjectKey(objectID model.StarterWorkspaceObjectID) string {
	id := objectID.String()
	return fmt.Sprintf("exam-starter-workspace/%s/%s/objects/%s", id[:2], id[2:4], id)
}
