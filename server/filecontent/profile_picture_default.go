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
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"

	"github.com/HugoSmits86/nativewebp"
	"github.com/disintegration/imaging"

	"github.com/sudosylabs/proctor/server/model"
)

const (
	defaultProfilePictureSize        = 512
	defaultProfilePictureSupersample = 4
)

type defaultProfilePictureGlyph [5][5]bool

// renderDefaultProfilePicture builds one antialiased master for every rendition.
func renderDefaultProfilePicture(ctx context.Context, seed string) (*image.NRGBA, error) {
	if len(seed) != model.ProfilePictureSeedLength {
		return nil, fmt.Errorf("invalid default profile-picture seed")
	}
	seedBytes, err := hex.DecodeString(seed)
	if err != nil || len(seedBytes) != model.ProfilePictureSeedLength/2 {
		return nil, fmt.Errorf("invalid default profile-picture seed")
	}
	glyph, err := defaultProfilePictureShape(ctx, seedBytes)
	if err != nil {
		return nil, err
	}
	foreground, background := defaultProfilePictureColors(seedBytes)
	dimension := defaultProfilePictureSize * defaultProfilePictureSupersample
	canvas := image.NewNRGBA(image.Rect(0, 0, dimension, dimension))
	draw.Draw(
		canvas,
		canvas.Bounds(),
		image.NewUniform(background),
		image.Point{},
		draw.Src,
	)
	for row := range glyph {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for col, filled := range glyph[row] {
			if filled {
				glyph.drawCell(
					canvas,
					image.Pt(col, row),
					foreground,
					background,
				)
			}
		}
	}
	// Downsample a single antialiased master so the geometry and edge treatment
	// agree for persisted renditions and transient reads at every supported size.
	master := imaging.Resize(
		canvas,
		defaultProfilePictureSize,
		defaultProfilePictureSize,
		imaging.Lanczos,
	)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return master, nil
}

func encodeDefaultProfilePicture(master *image.NRGBA, size int) ([]byte, string, error) {
	normalized := master
	if size != defaultProfilePictureSize {
		normalized = imaging.Resize(
			master,
			size,
			size,
			imaging.Lanczos,
		)
	}
	var output bytes.Buffer
	if err := nativewebp.Encode(&output, normalized, &nativewebp.Options{
		CompressionLevel: nativewebp.DefaultCompression,
	}); err != nil {
		return nil, "", err
	}
	encoded := output.Bytes()
	return encoded, fmt.Sprintf("%x", sha256.Sum256(encoded)), nil
}

func defaultProfilePictureColors(seed []byte) (foreground, background color.NRGBA) {
	digest := sha256.Sum256(append([]byte("proctor-avatar-prototype/palette/"), seed...))
	hue := float64(int(digest[0])*256+int(digest[1])) / 65536 * 360
	saturation := 0.55 + float64(digest[2])/255*0.18
	background = defaultProfilePictureHSL(hue, 0.38, 0.94)
	lightness := 0.36
	foreground = defaultProfilePictureHSL(hue, saturation, lightness)
	backgroundLuminance := defaultProfilePictureLuminance(background)
	for (backgroundLuminance+0.05)/(defaultProfilePictureLuminance(foreground)+0.05) < 4.5 {
		lightness -= 0.01
		foreground = defaultProfilePictureHSL(hue, saturation, lightness)
	}
	return foreground, background
}

func defaultProfilePictureHSL(hue, saturation, lightness float64) color.NRGBA {
	chroma := (1 - math.Abs(2*lightness-1)) * saturation
	x := chroma * (1 - math.Abs(math.Mod(hue/60, 2)-1))
	var red, green, blue float64
	switch {
	case hue < 60:
		red, green = chroma, x
	case hue < 120:
		red, green = x, chroma
	case hue < 180:
		green, blue = chroma, x
	case hue < 240:
		green, blue = x, chroma
	case hue < 300:
		red, blue = x, chroma
	default:
		red, blue = chroma, x
	}
	offset := lightness - chroma/2
	return color.NRGBA{
		R: uint8(math.Round((red + offset) * 255)),
		G: uint8(math.Round((green + offset) * 255)),
		B: uint8(math.Round((blue + offset) * 255)),
		A: 255,
	}
}

func defaultProfilePictureLuminance(value color.NRGBA) float64 {
	linear := func(channel uint8) float64 {
		v := float64(channel) / 255
		if v <= 0.04045 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	return 0.2126*linear(value.R) + 0.7152*linear(value.G) + 0.0722*linear(value.B)
}

func defaultProfilePictureShape(ctx context.Context, seed []byte) (defaultProfilePictureGlyph, error) {
	for attempt := range 10000 {
		if err := ctx.Err(); err != nil {
			return defaultProfilePictureGlyph{}, err
		}
		digest := sha256.Sum256([]byte(fmt.Sprintf("proctor-avatar-prototype/shape/%x/%d", seed, attempt)))
		var glyph defaultProfilePictureGlyph
		var count, rows int
		var outer bool
		for row := range glyph {
			for col := range 3 {
				index := row*3 + col
				filled := digest[index/8]&(1<<(index%8)) != 0
				glyph[row][col], glyph[row][4-col] = filled, filled
			}
			rowCount := 0
			for _, filled := range glyph[row] {
				if filled {
					rowCount++
				}
			}
			count += rowCount
			if rowCount > 0 {
				rows++
			}
			outer = outer || glyph[row][0]
		}
		// Bound density and require a full-height, full-width connected silhouette
		// so small circular crops retain a substantial, recognizable shape.
		densityOK := count >= 11 && count <= 17
		extentOK := rows == 5 && outer
		if densityOK && extentOK && glyph.connected(count) {
			return glyph, nil
		}
	}
	// A bounded search must still produce a valid image for every seed.
	return defaultProfilePictureGlyph{
		{false, false, true, false, false},
		{false, true, true, true, false},
		{true, true, true, true, true},
		{false, true, true, true, false},
		{false, false, true, false, false},
	}, nil
}

func (g defaultProfilePictureGlyph) contains(row, col int) bool {
	inside := row >= 0 && row < 5 && col >= 0 && col < 5
	return inside && g[row][col]
}

func (g defaultProfilePictureGlyph) connected(want int) bool {
	queue := make([]image.Point, 0, 25)
	var seen [5][5]bool
	for row := range g {
		for col, filled := range g[row] {
			if filled && len(queue) == 0 {
				queue = append(queue, image.Pt(col, row))
				seen[row][col] = true
			}
		}
	}
	for index := 0; index < len(queue); index++ {
		point := queue[index]
		for _, delta := range [...]image.Point{{X: 1}, {X: -1}, {Y: 1}, {Y: -1}} {
			x, y := point.X+delta.X, point.Y+delta.Y
			if g.contains(y, x) && !seen[y][x] {
				seen[y][x] = true
				queue = append(queue, image.Pt(x, y))
			}
		}
	}
	return len(queue) == want
}

func (g defaultProfilePictureGlyph) drawCell(
	canvas *image.NRGBA,
	cell image.Point,
	foreground, background color.NRGBA,
) {
	const (
		scale  = defaultProfilePictureSupersample
		width  = 68 * scale
		inset  = 86 * scale
		radius = 13 * scale
	)
	left, top := inset+cell.X*width, inset+cell.Y*width
	draw.Draw(
		canvas,
		image.Rect(left, top, left+width, top+width),
		image.NewUniform(foreground),
		image.Point{},
		draw.Src,
	)
	for _, corner := range [...]image.Point{
		{X: -1, Y: -1}, {X: 1, Y: -1},
		{X: -1, Y: 1}, {X: 1, Y: 1},
	} {
		// Only convex outer corners are rounded; shared cell edges stay joined.
		if g.contains(cell.Y+corner.Y, cell.X) || g.contains(cell.Y, cell.X+corner.X) {
			continue
		}
		for y := range radius {
			for x := range radius {
				dx, dy := float64(x)+0.5-radius, float64(y)+0.5-radius
				if dx*dx+dy*dy <= radius*radius {
					continue
				}
				px, py := x, y
				if corner.X > 0 {
					px = width - 1 - x
				}
				if corner.Y > 0 {
					py = width - 1 - y
				}
				canvas.SetNRGBA(left+px, top+py, background)
			}
		}
	}
}
