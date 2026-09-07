// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package filecontent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"image"
	"io"
	"time"

	"github.com/HugoSmits86/nativewebp"
	"github.com/disintegration/imaging"
	_ "golang.org/x/image/webp"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

const (
	maximumProfilePicturePixels = 25_000_000
	maximumProfilePictureBytes  = int64(5 << 20)
)

var profilePictureSizes = [...]int{128, 256, 512}

// NormalizeAndStoreProfilePicture validates and converts one bounded upload
// into the complete canonical profile-picture rendition set.
func (c *Content) NormalizeAndStoreProfilePicture(ctx context.Context, revisionID model.FileRevisionID, body io.Reader, size int64, at time.Time) ([]model.FileRendition, error) {
	if c == nil || c.filesystem == nil || !revisionID.IsValid() || body == nil || size == 0 || size < -1 || size > maximumProfilePictureBytes {
		return nil, app.ErrInvalidProfilePicture
	}
	finish, err := c.beginWork(ctx)
	if err != nil {
		return nil, err
	}
	defer finish()
	raw, err := io.ReadAll(io.LimitReader(workReader{ctx: ctx, reader: body}, maximumProfilePictureBytes+1))
	if cancellation := ctx.Err(); cancellation != nil {
		return nil, cancellation
	}
	if err != nil || int64(len(raw)) > maximumProfilePictureBytes || (size >= 0 && int64(len(raw)) != size) {
		return nil, app.ErrInvalidProfilePicture
	}
	configuration, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || (format != "png" && format != "jpeg" && format != "webp") || configuration.Width <= 0 || configuration.Height <= 0 || configuration.Width > 4096 || configuration.Height > 4096 {
		return nil, app.ErrInvalidProfilePicture
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	imageValue, err := imaging.Decode(bytes.NewReader(raw), imaging.AutoOrientation(true))
	if cancellation := ctx.Err(); cancellation != nil {
		return nil, cancellation
	}
	if err != nil || imageValue.Bounds().Dx() <= 0 || imageValue.Bounds().Dy() <= 0 || imageValue.Bounds().Dx() > maximumProfilePicturePixels/imageValue.Bounds().Dy() {
		return nil, app.ErrInvalidProfilePicture
	}
	squareSize := min(imageValue.Bounds().Dx(), imageValue.Bounds().Dy())
	square := imaging.CropCenter(imageValue, squareSize, squareSize)
	renditions := make([]model.FileRendition, 0, len(profilePictureSizes))
	for _, target := range profilePictureSizes {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		dimension := min(target, squareSize)
		normalized := square
		if dimension < squareSize {
			normalized = imaging.Resize(square, dimension, dimension, imaging.Lanczos)
		}
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		var encoded bytes.Buffer
		if err = nativewebp.Encode(&encoded, normalized, &nativewebp.Options{CompressionLevel: nativewebp.DefaultCompression}); err != nil {
			_ = c.RemoveProfilePictureRenditions(ctx, revisionID, renditions)
			return nil, err
		}
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		checksum := fmt.Sprintf("%x", sha256.Sum256(encoded.Bytes()))
		rendition, modelErr := model.NewFileRendition(model.NewFileRenditionID(), revisionID, fmt.Sprintf("profile_%d", target), "image/webp", int64(encoded.Len()), dimension, dimension, checksum, at)
		if modelErr != nil {
			_ = c.RemoveProfilePictureRenditions(ctx, revisionID, renditions)
			return nil, modelErr
		}
		if err = c.storeRendition(ctx, revisionID, rendition.ID, bytes.NewReader(encoded.Bytes()), int64(encoded.Len())); err != nil {
			_ = c.RemoveProfilePictureRenditions(ctx, revisionID, renditions)
			return nil, err
		}
		renditions = append(renditions, *rendition)
	}
	return renditions, nil
}

// GenerateAndStoreDefaultProfilePicture stores deterministic default-picture
// renditions for a stable per-user seed.
func (c *Content) GenerateAndStoreDefaultProfilePicture(ctx context.Context, revisionID model.FileRevisionID, seed string, at time.Time) ([]model.FileRendition, error) {
	if c == nil || c.filesystem == nil || !revisionID.IsValid() {
		return nil, app.ErrInvalidProfilePicture
	}
	finish, err := c.beginWork(ctx)
	if err != nil {
		return nil, err
	}
	defer finish()
	master, err := renderDefaultProfilePicture(ctx, seed)
	if err != nil {
		return nil, err
	}
	renditions := make([]model.FileRendition, 0, len(profilePictureSizes))
	for _, target := range profilePictureSizes {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		encoded, checksum, err := encodeDefaultProfilePicture(master, target)
		if err != nil {
			_ = c.RemoveProfilePictureRenditions(ctx, revisionID, renditions)
			return nil, err
		}
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		rendition, err := model.NewFileRendition(model.NewFileRenditionID(), revisionID, fmt.Sprintf("profile_%d", target), "image/webp", int64(len(encoded)), target, target, checksum, at)
		if err != nil {
			_ = c.RemoveProfilePictureRenditions(ctx, revisionID, renditions)
			return nil, err
		}
		if err = c.storeRendition(ctx, revisionID, rendition.ID, bytes.NewReader(encoded), int64(len(encoded))); err != nil {
			_ = c.RemoveProfilePictureRenditions(ctx, revisionID, renditions)
			return nil, err
		}
		renditions = append(renditions, *rendition)
	}
	return renditions, nil
}

// RenderDefaultProfilePicture renders an unpersisted deterministic fallback.
func (c *Content) RenderDefaultProfilePicture(ctx context.Context, seed string, size int) (*app.RenderedProfilePicture, error) {
	if c == nil || c.filesystem == nil {
		return nil, app.ErrInvalidProfilePicture
	}
	finish, err := c.beginWork(ctx)
	if err != nil {
		return nil, err
	}
	defer finish()
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if size != 128 && size != 256 && size != 512 {
		return nil, fmt.Errorf("unsupported default profile-picture size %d", size)
	}
	master, err := renderDefaultProfilePicture(ctx, seed)
	if err != nil {
		return nil, err
	}
	encoded, checksum, err := encodeDefaultProfilePicture(master, size)
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return &app.RenderedProfilePicture{Body: io.NopCloser(bytes.NewReader(encoded)), MediaType: "image/webp", Size: int64(len(encoded)), SHA256: checksum}, nil
}

// RemoveProfilePictureRenditions idempotently removes an exact profile-picture
// rendition manifest.
func (c *Content) RemoveProfilePictureRenditions(ctx context.Context, revisionID model.FileRevisionID, renditions []model.FileRendition) error {
	ids := make([]model.FileRenditionID, 0, len(renditions))
	for _, rendition := range renditions {
		ids = append(ids, rendition.ID)
	}
	return c.removeRenditions(ctx, revisionID, ids)
}
