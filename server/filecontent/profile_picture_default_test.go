// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package filecontent_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"image"
	"image/color"
	"io"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/disintegration/imaging"
	"github.com/stretchr/testify/require"
	"golang.org/x/image/webp"

	memoryvfs "github.com/sudosylabs/proctor/packages/vfs/memory"
	"github.com/sudosylabs/proctor/server/filecontent"
	"github.com/sudosylabs/proctor/server/model"
)

func TestDefaultProfilePictureVisualPropertiesAndConsistentRenditions(t *testing.T) {
	t.Parallel()

	cases := []struct{ name, seed string }{
		{name: "zero seed", seed: strings.Repeat("0", 64)},
		{name: "maximum seed", seed: strings.Repeat("f", 64)},
		{name: "golden", seed: defaultProfilePictureSeed},
	}
	for index := range 8 {
		name := fmt.Sprintf("sample %d", index)
		cases = append(cases, struct{ name, seed string }{
			name: name, seed: fmt.Sprintf("%x", sha256.Sum256([]byte(name))),
		})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			content, err := filecontent.New(memoryvfs.New(), filecontent.Policy{MaximumConcurrentOperations: 1}, nil)
			require.NoError(t, err)
			revisionID := model.NewFileRevisionID()
			renditions, err := content.GenerateAndStoreDefaultProfilePicture(
				context.Background(),
				revisionID,
				tc.seed,
				time.Unix(1, 0),
			)
			require.NoError(t, err)
			require.Len(t, renditions, 3)
			images := make(map[int]*image.NRGBA, 3)
			for _, rendition := range renditions {
				body, err := content.OpenProfilePictureRendition(context.Background(), revisionID, rendition.ID)
				require.NoError(t, err)
				decoded := decodeDefaultPicture(t, body)
				require.Equal(t, image.Rect(0, 0, rendition.Width, rendition.Width), decoded.Bounds())
				images[rendition.Width] = decoded
			}
			master := images[512]
			require.NotNil(t, master, "512 px master missing")
			assertDefaultPictureSilhouette(t, master)
			for _, size := range []int{128, 256} {
				actual := images[size]
				require.NotNil(t, actual, "%d px rendition missing", size)
				want := imaging.Resize(
					master,
					size,
					size,
					imaging.Lanczos,
				)
				require.True(t, bytes.Equal(actual.Pix, want.Pix), "%d px rendition does not match downsampled master", size)
			}
		})
	}
}

func TestDefaultProfilePictureRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	content, err := filecontent.New(memoryvfs.New(), filecontent.Policy{MaximumConcurrentOperations: 1}, nil)
	require.NoError(t, err)
	for _, tc := range []struct{ name, seed string }{
		{name: "empty"},
		{name: "short", seed: strings.Repeat("a", 62)},
		{name: "long", seed: strings.Repeat("a", 66)},
		{name: "invalid hex", seed: strings.Repeat("g", 64)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			picture, err := content.RenderDefaultProfilePicture(context.Background(), tc.seed, 128)
			require.Error(t, err, "invalid seed rendered: picture")
			require.Nil(t, picture, "invalid seed rendered: picture")
			renditions, err := content.GenerateAndStoreDefaultProfilePicture(
				context.Background(),
				model.NewFileRevisionID(),
				tc.seed,
				time.Unix(1, 0),
			)
			require.Error(t, err, "invalid seed stored: renditions")
			require.Empty(t, renditions, "invalid seed stored: renditions")
		})
	}
	for _, size := range []int{-1, 0, 64, 129, 1024} {
		t.Run(fmt.Sprintf("size %d", size), func(t *testing.T) {
			picture, err := content.RenderDefaultProfilePicture(context.Background(), defaultProfilePictureSeed, size)
			require.Error(t, err, "invalid size rendered: picture")
			require.Nil(t, picture, "invalid size rendered: picture")
		})
	}
}

func TestDefaultProfilePictureSeedHexCaseDoesNotChangeImage(t *testing.T) {
	t.Parallel()

	content, err := filecontent.New(memoryvfs.New(), filecontent.Policy{MaximumConcurrentOperations: 1}, nil)
	require.NoError(t, err)
	picture, err := content.RenderDefaultProfilePicture(
		context.Background(), strings.ToUpper(defaultProfilePictureSeed), 128,
	)
	require.NoError(t, err)
	defer picture.Body.Close()
	require.Equal(t, defaultProfilePictureChecksum128, picture.SHA256)
}

func decodeDefaultPicture(t *testing.T, body io.ReadCloser) *image.NRGBA {
	t.Helper()
	defer body.Close()
	decoded, err := webp.Decode(body)
	require.NoError(t, err)
	return imaging.Clone(decoded)
}

func assertDefaultPictureSilhouette(t *testing.T, img *image.NRGBA) {
	t.Helper()
	background := img.NRGBAAt(0, 0)
	counts := make(map[color.NRGBA]int)
	for y := range 512 {
		for x := range 512 {
			pixel := img.NRGBAAt(x, y)
			require.Equal(t, uint8(255), pixel.A, "transparent pixel at (%d, %d)", x, y)
			mirror := img.NRGBAAt(511-x, y)
			// Mirrored Lanczos weights can round a channel to adjacent byte values.
			difference := max(
				math.Abs(float64(pixel.R)-float64(mirror.R)),
				math.Abs(float64(pixel.G)-float64(mirror.G)),
				math.Abs(float64(pixel.B)-float64(mirror.B)),
			)
			require.LessOrEqual(t, difference, float64(1), "asymmetric pixels at (%d, %d): %v vs %v", x, y, pixel, mirror)
			dx, dy := float64(x)-255.5, float64(y)-255.5
			if dx*dx+dy*dy >= 256*256 {
				require.Equal(t, background, pixel, "circular cropping cuts the glyph at (%d, %d)", x, y)
			}
			counts[pixel]++
		}
	}
	require.Greater(t, len(counts), 2, "glyph edges are not antialiased")
	var foreground color.NRGBA
	var most int
	for pixel, count := range counts {
		if pixel != background && count > most {
			foreground, most = pixel, count
		}
	}
	luminance := func(pixel color.NRGBA) float64 {
		channels := [3]uint8{pixel.R, pixel.G, pixel.B}
		weights := [3]float64{0.2126, 0.7152, 0.0722}
		var total float64
		for index, channel := range channels {
			value := float64(channel) / 255
			linear := value / 12.92
			if value > 0.04045 {
				linear = math.Pow((value+0.055)/1.055, 2.4)
			}
			total += linear * weights[index]
		}
		return total
	}
	require.GreaterOrEqual(t, ((luminance(background) + 0.05) / (luminance(foreground) + 0.05)), 4.5, "foreground/background contrast")
	// Read the glyph's occupied cells from the image, then flood-fill them to
	// verify one connected silhouette without relying on generator internals.
	remaining := make(map[image.Point]bool)
	for row := range 5 {
		for col := range 5 {
			if img.NRGBAAt(120+col*68, 120+row*68) == foreground {
				require.Equal(t, foreground, img.NRGBAAt(120+(4-col)*68, 120+row*68), "glyph cells are not mirrored")
				remaining[image.Pt(col, row)] = true
			}
		}
	}
	require.GreaterOrEqual(t, len(remaining), 11, "glyph has")
	require.LessOrEqual(t, len(remaining), 17, "glyph has")
	queue := make([]image.Point, 0, len(remaining))
	for point := range remaining {
		queue = append(queue, point)
		delete(remaining, point)
		break
	}
	for len(queue) > 0 {
		point := queue[0]
		queue = queue[1:]
		for _, next := range []image.Point{
			{X: point.X - 1, Y: point.Y}, {X: point.X + 1, Y: point.Y},
			{X: point.X, Y: point.Y - 1}, {X: point.X, Y: point.Y + 1},
		} {
			if remaining[next] {
				delete(remaining, next)
				queue = append(queue, next)
			}
		}
	}
	require.Empty(t, remaining, "glyph has disconnected cells")
}
