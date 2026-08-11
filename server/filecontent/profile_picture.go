// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package filecontent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
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
	raw, err := io.ReadAll(io.LimitReader(body, maximumProfilePictureBytes+1))
	if err != nil || int64(len(raw)) > maximumProfilePictureBytes || (size >= 0 && int64(len(raw)) != size) {
		return nil, app.ErrInvalidProfilePicture
	}
	configuration, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || (format != "png" && format != "jpeg" && format != "webp") || configuration.Width <= 0 || configuration.Height <= 0 || configuration.Width > 4096 || configuration.Height > 4096 {
		return nil, app.ErrInvalidProfilePicture
	}
	imageValue, err := imaging.Decode(bytes.NewReader(raw), imaging.AutoOrientation(true))
	if err != nil || imageValue.Bounds().Dx() <= 0 || imageValue.Bounds().Dy() <= 0 || imageValue.Bounds().Dx() > maximumProfilePicturePixels/imageValue.Bounds().Dy() {
		return nil, app.ErrInvalidProfilePicture
	}
	squareSize := min(imageValue.Bounds().Dx(), imageValue.Bounds().Dy())
	square := imaging.CropCenter(imageValue, squareSize, squareSize)
	renditions := make([]model.FileRendition, 0, len(profilePictureSizes))
	for _, target := range profilePictureSizes {
		dimension := min(target, squareSize)
		normalized := square
		if dimension < squareSize {
			normalized = imaging.Resize(square, dimension, dimension, imaging.Lanczos)
		}
		var encoded bytes.Buffer
		if err = nativewebp.Encode(&encoded, normalized, &nativewebp.Options{CompressionLevel: nativewebp.DefaultCompression}); err != nil {
			_ = c.RemoveProfilePictureRenditions(ctx, revisionID, renditions)
			return nil, err
		}
		checksum := fmt.Sprintf("%x", sha256.Sum256(encoded.Bytes()))
		rendition, modelErr := model.NewFileRendition(model.NewFileRenditionID(), revisionID, fmt.Sprintf("profile_%d", target), "image/webp", int64(encoded.Len()), dimension, dimension, checksum, at)
		if modelErr != nil {
			_ = c.RemoveProfilePictureRenditions(ctx, revisionID, renditions)
			return nil, modelErr
		}
		if err = c.StageProfilePictureRendition(ctx, revisionID, rendition.ID, bytes.NewReader(encoded.Bytes()), int64(encoded.Len())); err != nil {
			_ = c.RemoveProfilePictureRenditions(ctx, revisionID, renditions)
			return nil, err
		}
		renditions = append(renditions, *rendition)
	}
	return renditions, nil
}

// GenerateAndStoreDefaultProfilePicture stores version-one deterministic
// default-picture renditions for a stable per-user seed.
func (c *Content) GenerateAndStoreDefaultProfilePicture(ctx context.Context, revisionID model.FileRevisionID, seed string, at time.Time) ([]model.FileRendition, error) {
	renditions := make([]model.FileRendition, 0, len(profilePictureSizes))
	for _, target := range profilePictureSizes {
		encoded, checksum, err := renderDefaultProfilePictureV1(seed, target)
		if err != nil {
			_ = c.RemoveProfilePictureRenditions(ctx, revisionID, renditions)
			return nil, err
		}
		rendition, err := model.NewFileRendition(model.NewFileRenditionID(), revisionID, fmt.Sprintf("profile_%d", target), "image/webp", int64(len(encoded)), target, target, checksum, at)
		if err != nil {
			_ = c.RemoveProfilePictureRenditions(ctx, revisionID, renditions)
			return nil, err
		}
		if err = c.StageProfilePictureRendition(ctx, revisionID, rendition.ID, bytes.NewReader(encoded), int64(len(encoded))); err != nil {
			_ = c.RemoveProfilePictureRenditions(ctx, revisionID, renditions)
			return nil, err
		}
		renditions = append(renditions, *rendition)
	}
	return renditions, nil
}

// RenderDefaultProfilePicture renders an unpersisted version-one fallback.
func (c *Content) RenderDefaultProfilePicture(_ context.Context, seed string, size int) (*app.RenderedProfilePicture, error) {
	encoded, checksum, err := renderDefaultProfilePictureV1(seed, size)
	if err != nil {
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
	return c.RemoveRenditions(ctx, revisionID, ids)
}

func renderDefaultProfilePictureV1(seed string, size int) ([]byte, string, error) {
	if size != 128 && size != 256 && size != 512 {
		return nil, "", fmt.Errorf("unsupported default profile-picture size %d", size)
	}
	seedBytes, err := hex.DecodeString(seed)
	if err != nil || len(seedBytes) != model.ProfilePictureSeedLength/2 {
		return nil, "", fmt.Errorf("invalid default profile-picture seed")
	}
	palette := sha256.Sum256(seedBytes)
	canvas := image.NewNRGBA(image.Rect(0, 0, size, size))
	background := color.NRGBA{R: 48 + palette[0]%128, G: 48 + palette[1]%128, B: 48 + palette[2]%128, A: 255}
	foreground := color.NRGBA{R: 96 + palette[3]%160, G: 96 + palette[4]%160, B: 96 + palette[5]%160, A: 220}
	accent := color.NRGBA{R: 64 + palette[6]%192, G: 64 + palette[7]%192, B: 64 + palette[8]%192, A: 210}
	centerX := size/3 + int(palette[9])*(size/3)/255
	centerY := size/3 + int(palette[10])*(size/3)/255
	radius := size/5 + int(palette[11])*(size/8)/255
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			pixel := background
			dx, dy := x-centerX, y-centerY
			if dx*dx+dy*dy <= radius*radius {
				pixel = foreground
			}
			if (x+y+int(palette[12]))%(max(2, size/5)) < max(1, size/18) {
				pixel = accent
			}
			canvas.SetNRGBA(x, y, pixel)
		}
	}
	var output bytes.Buffer
	if err = nativewebp.Encode(&output, canvas, &nativewebp.Options{CompressionLevel: nativewebp.DefaultCompression}); err != nil {
		return nil, "", err
	}
	encoded := output.Bytes()
	checksum := fmt.Sprintf("%x", sha256.Sum256(encoded))
	return append([]byte(nil), encoded...), checksum, nil
}
