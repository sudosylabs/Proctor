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
			if err != nil {
				t.Fatal(err)
			}
			revisionID := model.NewFileRevisionID()
			renditions, err := content.GenerateAndStoreDefaultProfilePicture(
				context.Background(),
				revisionID,
				tc.seed,
				time.Unix(1, 0),
			)
			if err != nil {
				t.Fatal(err)
			}
			if len(renditions) != 3 {
				t.Fatalf("got %d renditions, want 3", len(renditions))
			}
			images := make(map[int]*image.NRGBA, 3)
			for _, rendition := range renditions {
				body, err := content.OpenProfilePictureRendition(context.Background(), revisionID, rendition.ID)
				if err != nil {
					t.Fatal(err)
				}
				decoded := decodeDefaultPicture(t, body)
				if decoded.Bounds() != image.Rect(0, 0, rendition.Width, rendition.Width) {
					t.Fatalf("unexpected image dimensions: %v", decoded.Bounds())
				}
				images[rendition.Width] = decoded
			}
			master := images[512]
			if master == nil {
				t.Fatal("512 px master missing")
			}
			assertDefaultPictureSilhouette(t, master)
			for _, size := range []int{128, 256} {
				actual := images[size]
				if actual == nil {
					t.Fatalf("%d px rendition missing", size)
				}
				want := imaging.Resize(
					master,
					size,
					size,
					imaging.Lanczos,
				)
				if !bytes.Equal(actual.Pix, want.Pix) {
					t.Fatalf("%d px rendition does not match downsampled master", size)
				}
			}
		})
	}
}

func TestDefaultProfilePictureRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	content, err := filecontent.New(memoryvfs.New(), filecontent.Policy{MaximumConcurrentOperations: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, seed string }{
		{name: "empty"},
		{name: "short", seed: strings.Repeat("a", 62)},
		{name: "long", seed: strings.Repeat("a", 66)},
		{name: "invalid hex", seed: strings.Repeat("g", 64)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			picture, err := content.RenderDefaultProfilePicture(context.Background(), tc.seed, 128)
			if err == nil || picture != nil {
				t.Fatalf("invalid seed rendered: picture=%v error=%v", picture, err)
			}
			renditions, err := content.GenerateAndStoreDefaultProfilePicture(
				context.Background(),
				model.NewFileRevisionID(),
				tc.seed,
				time.Unix(1, 0),
			)
			if err == nil || len(renditions) != 0 {
				t.Fatalf("invalid seed stored: renditions=%v error=%v", renditions, err)
			}
		})
	}
	for _, size := range []int{-1, 0, 64, 129, 1024} {
		t.Run(fmt.Sprintf("size %d", size), func(t *testing.T) {
			picture, err := content.RenderDefaultProfilePicture(context.Background(), defaultProfilePictureSeed, size)
			if err == nil || picture != nil {
				t.Fatalf("invalid size rendered: picture=%v error=%v", picture, err)
			}
		})
	}
}

func TestDefaultProfilePictureSeedHexCaseDoesNotChangeImage(t *testing.T) {
	t.Parallel()

	content, err := filecontent.New(memoryvfs.New(), filecontent.Policy{MaximumConcurrentOperations: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	picture, err := content.RenderDefaultProfilePicture(
		context.Background(), strings.ToUpper(defaultProfilePictureSeed), 128,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer picture.Body.Close()
	if picture.SHA256 != defaultProfilePictureChecksum128 {
		t.Fatalf("hex case changed the rendered image: %s", picture.SHA256)
	}
}

func decodeDefaultPicture(t *testing.T, body io.ReadCloser) *image.NRGBA {
	t.Helper()
	defer body.Close()
	decoded, err := webp.Decode(body)
	if err != nil {
		t.Fatal(err)
	}
	return imaging.Clone(decoded)
}

func assertDefaultPictureSilhouette(t *testing.T, img *image.NRGBA) {
	t.Helper()
	background := img.NRGBAAt(0, 0)
	counts := make(map[color.NRGBA]int)
	for y := range 512 {
		for x := range 512 {
			pixel := img.NRGBAAt(x, y)
			if pixel.A != 255 {
				t.Fatalf("transparent pixel at (%d, %d)", x, y)
			}
			mirror := img.NRGBAAt(511-x, y)
			// Mirrored Lanczos weights can round a channel to adjacent byte values.
			difference := max(
				math.Abs(float64(pixel.R)-float64(mirror.R)),
				math.Abs(float64(pixel.G)-float64(mirror.G)),
				math.Abs(float64(pixel.B)-float64(mirror.B)),
			)
			if difference > 1 {
				t.Fatalf(
					"asymmetric pixels at (%d, %d): %v vs %v",
					x,
					y,
					pixel,
					mirror,
				)
			}
			dx, dy := float64(x)-255.5, float64(y)-255.5
			if dx*dx+dy*dy >= 256*256 && pixel != background {
				t.Fatalf("circular cropping cuts the glyph at (%d, %d)", x, y)
			}
			counts[pixel]++
		}
	}
	if len(counts) <= 2 {
		t.Fatal("glyph edges are not antialiased")
	}
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
	if ratio := (luminance(background) + 0.05) / (luminance(foreground) + 0.05); ratio < 4.5 {
		t.Fatalf("foreground/background contrast = %f, want at least 4.5", ratio)
	}
	// Read the glyph's occupied cells from the image, then flood-fill them to
	// verify one connected silhouette without relying on generator internals.
	remaining := make(map[image.Point]bool)
	for row := range 5 {
		for col := range 5 {
			if img.NRGBAAt(120+col*68, 120+row*68) == foreground {
				if img.NRGBAAt(120+(4-col)*68, 120+row*68) != foreground {
					t.Fatal("glyph cells are not mirrored")
				}
				remaining[image.Pt(col, row)] = true
			}
		}
	}
	if len(remaining) < 11 || len(remaining) > 17 {
		t.Fatalf("glyph has %d occupied cells, want 11 through 17", len(remaining))
	}
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
	if len(remaining) != 0 {
		t.Fatal("glyph has disconnected cells")
	}
}
