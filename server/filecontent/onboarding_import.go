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
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	vfspkg "github.com/sudosylabs/proctor/packages/vfs"
	"github.com/sudosylabs/proctor/server/model"
)

var ErrOnboardingImportTooLarge = errors.New("file content: onboarding import is too large")

func (c *Content) IsOnboardingImportTooLarge(err error) bool {
	return errors.Is(err, ErrOnboardingImportTooLarge)
}

// StageOnboardingImport streams one private immutable CSV object. Callers must
// provide the domain limit; an over-limit object is removed before returning.
func (c *Content) StageOnboardingImport(ctx context.Context, id model.OnboardingImportID, body io.Reader, maximum int64) (string, int64, error) {
	if c == nil || c.filesystem == nil || !id.IsValid() || body == nil || maximum < 1 {
		return "", 0, errors.New("invalid onboarding import content")
	}
	hash := sha256.New()
	limited := &io.LimitedReader{R: io.TeeReader(body, hash), N: maximum + 1}
	info, err := c.filesystem.Write(ctx, onboardingImportKey(id), limited, vfspkg.WriteOptions{NoOverwrite: c.filesystem.Capabilities().ConditionalWrite})
	if err != nil {
		return "", 0, sanitize("stage onboarding import", err)
	}
	if info.Size > maximum {
		_ = c.filesystem.Remove(ctx, onboardingImportKey(id), vfspkg.RemoveOptions{})
		return "", info.Size, ErrOnboardingImportTooLarge
	}
	return hex.EncodeToString(hash.Sum(nil)), info.Size, nil
}

func (c *Content) OpenOnboardingImport(ctx context.Context, id model.OnboardingImportID) (io.ReadCloser, error) {
	if c == nil || c.filesystem == nil || !id.IsValid() {
		return nil, errors.New("invalid onboarding import identity")
	}
	file, err := c.filesystem.Open(ctx, onboardingImportKey(id), vfspkg.OpenOptions{})
	if err != nil {
		return nil, sanitize("open onboarding import", err)
	}
	return file.Body, nil
}

func (c *Content) RemoveOnboardingImport(ctx context.Context, id model.OnboardingImportID) error {
	if c == nil || c.filesystem == nil || !id.IsValid() {
		return errors.New("invalid onboarding import identity")
	}
	err := c.filesystem.Remove(ctx, onboardingImportKey(id), vfspkg.RemoveOptions{})
	if errors.Is(err, vfspkg.ErrNotFound) {
		return nil
	}
	return sanitize("remove onboarding import", err)
}

// ListOnboardingImportFiles pages private staged objects old enough for
// orphan reconciliation. The caller checks PostgreSQL ownership before any
// removal, so ordinary active uploads remain untouched.
func (c *Content) ListOnboardingImportFiles(ctx context.Context, cursor string, limit int, before time.Time) ([]model.OnboardingImportID, string, error) {
	if c == nil || c.filesystem == nil || limit < 1 || limit > vfspkg.MaximumListLimit || before.IsZero() {
		return nil, "", errors.New("invalid onboarding import listing")
	}
	page, err := c.filesystem.List(ctx, vfspkg.ListOptions{Prefix: "onboarding/imports/", Cursor: cursor, Limit: limit})
	if err != nil {
		return nil, "", sanitize("list onboarding imports", err)
	}
	result := make([]model.OnboardingImportID, 0, len(page.Entries))
	for _, entry := range page.Entries {
		if entry.IsDir || entry.ModifiedAt.After(before) || !strings.HasSuffix(entry.Path, ".csv") {
			continue
		}
		name := entry.Path[strings.LastIndex(entry.Path, "/")+1:]
		id := model.OnboardingImportID(strings.TrimSuffix(name, ".csv"))
		if id.IsValid() {
			result = append(result, id)
		}
	}
	return result, page.NextCursor, nil
}

func onboardingImportKey(id model.OnboardingImportID) string {
	value := id.String()
	return fmt.Sprintf("onboarding/imports/%s/%s/%s.csv", value[:2], value[2:4], value)
}
