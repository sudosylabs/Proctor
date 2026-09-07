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
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/HugoSmits86/nativewebp"
	"golang.org/x/image/webp"

	vfspkg "github.com/sudosylabs/proctor/packages/vfs"
	localvfs "github.com/sudosylabs/proctor/packages/vfs/local"
	memoryvfs "github.com/sudosylabs/proctor/packages/vfs/memory"
	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/filecontent"
	"github.com/sudosylabs/proctor/server/model"
)

const defaultProfilePictureV2Seed = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
const defaultProfilePictureV2Checksum128 = "9a1b3589985b0a36f6ae0d68f0326d25b65f433653542c955083e20569d6bd56"
const defaultProfilePictureV2Checksum256 = "e83e294a13692f32dfb57413eaa57122406d054d99197e6147b0d43bf1389c48"
const defaultProfilePictureV2Checksum512 = "68fc144e3c3ad3ce4954fe940d253632375c45b10a83b6486a9a1919fedbc97f"
const defaultProfilePictureV2Base64_128 = "UklGRu4EAABXRUJQVlA4TOIEAAAvf8AfAE0wIMA2jKRzSAEAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAECa7n4AENF/BASc6Ar+bUsBAADwU5V07tmUuCmAUxBCICuihRCQ3R3/G2fahNoo057bY7r5Sm8ORMpBYfOYWNyqYJZXxQlmzue7uiQQACTG0ebirm0BAPClX1oAAOA9lAIAAAAAAAAAAADgAQAA4B/1AABAv/7LPwCA968XvZD8zUwmu0DUAAAAAAAAAAAAAAAAAADwAAAAAAAAwAMABAAPAAAAANAAgAMADwAAAAAAAACAAwAHDgAAAACAAgAAwAIAAAAAAMADAAAAUAAAAAAAAAAAAACPBzwAwPN4AAAAAA4AAAAAAAAAAAAeAAAAAAAAAAAAACgAAAAAAMABAADAAgAAAHjAw4ECAAAAAAAAAAAAAADwAAAAACgAAAAAAAAAAAAAPAAAAAAAAAAAAAAsAAAAAAAAAABQAAAAAACgAAAAAACABwAAAAAAAAAAAAAAAAAUAAAAgAAAAAAAAAAAAEABAAAAAA8AAADgAQAAAAAAAAAAADjAAzwAAAAAAAAAAAAAAAAAAAAAAAAAAACAAAUAAAAHBCAJyszeCkDaLwA8XwkAAAAAAAAAAAAAAAAAAAAAAAAAhACQKw8A/5cHxbV364AA2LBJ0oQD4LxaAGDg/wwAAAAAAAAAAAAAAAAAAAAAgwGA0TkOAK56Dnzo1jYNBwQlC8r10yGZvap9f2YDgBe/XQ5tu8XRYwAIjAEgIJpHj35BJRRSAUCtinDQdd+LcUfTUXlS0rPtF6MrYF+xCj0A3tpfV82a9uDl/mt7OL9VdIkJfUcQIIp4ZxzEm3Vq1w52uKQSCI2BSuu463BkjTpcSDpzV72uYKEtIXf2E/4eGHANXAmdJ0WOS8POinBbNT2oBtHO3pYXVTCqyYcqMDKa8/aKxu0uEaOaqNVcOg1GRteO89p5efxJD94vjGo2p3vXC//yM4yMBlNGMvKXNzjV7JV02yz754ag/e3w+JJNux5UQ9B+xUtGelBNwZSMPOAxvNp/ywneh3FlvVC9fADXUVbCSzX3mxzp65WKFOaA7nMJylq7S1tZp52wW6DZWePTHA6eK3wdm/8r97lDg5RFUbadw175Yspy3irbqLWo138lqLxSoW1hZNsvQlD2aPkIqizn1Uzgu0h1jpeBK5vVppsDhtlQBdjoGO9qHD/0AelCq2ZgI69Y3+coqYpaeRMSNVpN1+v6ya8ffeRnAGIuI4ZuXcfT9xpFQXY/2sbgVsyFNXNX4e+eH6t260bTKYmxJ79ffv8Y+GR/E6pezd/1CoLyeEqLZGASWWyFH9RVIY6ZIDGPlBOdGk+L+Px/bF2em7fpy1eoryRjJpSikJhHPnz8pJPv0pxsDF6OIKcpHTOhBEFwzCNyrpnvMpZvoO+Sjpn8Qn0FLOZJjJHXf9rJaGU91DMYe5IxEzTEwWOe1JnRzPnAiiCMmRBiHt0hhDRmgsY8RS8IsjEwq2tRV4RNra2yGu0kqfTfZj/fXeNgWQWcaiToq//ad3OkE/n5zb3HAg9CUqR+HvzApGbrDG6apBb9U50EgWnfP8XVIGMWJKkyZeWgRkbQ3AmhCCr3tBMfZ5Q6El4O9BacuyEsFu7DtV5YdgzJ+C6xcO/haf9mAw=="
const defaultProfilePictureV2Base64_256 = "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"
const defaultProfilePictureV2Base64_512 = "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"

func TestContentStoresCanonicalProfilePictureRenditionsWithoutUpscaling(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 40, 20))
	for y := 0; y < 20; y++ {
		for x := 0; x < 40; x++ {
			source.Set(x, y, color.NRGBA{R: 200, G: 10, B: 20, A: 255})
		}
	}
	var input bytes.Buffer
	if err := png.Encode(&input, source); err != nil {
		t.Fatal(err)
	}
	for _, backend := range profileContentBackends() {
		t.Run(backend.name, func(t *testing.T) {
			content, err := filecontent.New(backend.open(t), filecontent.Policy{MaximumConcurrentOperations: 2}, nil)
			if err != nil {
				t.Fatal(err)
			}
			revisionID := model.NewFileRevisionID()
			renditions, err := content.NormalizeAndStoreProfilePicture(context.Background(), revisionID, bytes.NewReader(input.Bytes()), int64(input.Len()), time.Unix(1, 0))
			if err != nil {
				t.Fatal(err)
			}
			if len(renditions) != 3 {
				t.Fatalf("renditions = %d, want 3", len(renditions))
			}
			for _, rendition := range renditions {
				if rendition.Width != 20 || rendition.Height != 20 || rendition.MediaType != "image/webp" {
					t.Fatalf("noncanonical rendition: %#v", rendition)
				}
				body, openErr := content.OpenProfilePictureRendition(context.Background(), revisionID, rendition.ID)
				if openErr != nil {
					t.Fatal(openErr)
				}
				decoded, decodeErr := webp.Decode(body)
				_ = body.Close()
				if decodeErr != nil {
					t.Fatal(decodeErr)
				}
				if decoded.Bounds().Dx() != 20 || decoded.Bounds().Dy() != 20 {
					t.Fatalf("decoded %s dimensions = %v", rendition.Name, decoded.Bounds())
				}
			}
		})
	}
}

func profileContentBackends() []struct {
	name string
	open func(*testing.T) vfspkg.FileSystem
} {
	return []struct {
		name string
		open func(*testing.T) vfspkg.FileSystem
	}{
		{name: "memory", open: func(*testing.T) vfspkg.FileSystem { return memoryvfs.New() }},
		{name: "local", open: func(t *testing.T) vfspkg.FileSystem {
			filesystem, err := localvfs.New(filepath.Join(t.TempDir(), "vfs"))
			if err != nil {
				t.Fatal(err)
			}
			return filesystem
		}},
	}
}

func TestContentLeavesAnUncertainProfilePictureWriteForBoundedRecovery(t *testing.T) {
	t.Parallel()

	backend := &uncertainWriteVFS{FileSystem: memoryvfs.New(), failOnCall: 2}
	content, err := filecontent.New(backend, filecontent.Policy{MaximumConcurrentOperations: 2}, nil)
	if err != nil {
		t.Fatal(err)
	}
	source := image.NewNRGBA(image.Rect(0, 0, 20, 20))
	var input bytes.Buffer
	if err = png.Encode(&input, source); err != nil {
		t.Fatal(err)
	}
	revisionID := model.NewFileRevisionID()
	if _, err = content.NormalizeAndStoreProfilePicture(context.Background(), revisionID, bytes.NewReader(input.Bytes()), int64(input.Len()), time.Unix(1, 0)); !filecontent.IsUnavailable(err) {
		t.Fatalf("uncertain write error = %v, want unavailable", err)
	}
	page, err := backend.FileSystem.List(context.Background(), vfspkg.ListOptions{Prefix: "files/", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 1 {
		t.Fatalf("uncertain invisible objects = %d, want 1", len(page.Entries))
	}
	if err = content.PurgeAbandonedFileRevision(context.Background(), revisionID); err != nil {
		t.Fatalf("purge uncertain revision: %v", err)
	}
	page, err = backend.FileSystem.List(context.Background(), vfspkg.ListOptions{Prefix: "files/", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 0 {
		t.Fatalf("uncertain objects after purge = %d", len(page.Entries))
	}
}

type uncertainWriteVFS struct {
	vfspkg.FileSystem
	failOnCall int
	writes     int
}

func (f *uncertainWriteVFS) Write(ctx context.Context, path string, body io.Reader, options vfspkg.WriteOptions) (vfspkg.Info, error) {
	f.writes++
	info, err := f.FileSystem.Write(ctx, path, body, options)
	if err == nil && f.writes == f.failOnCall {
		return info, errors.New("write acknowledgement lost")
	}
	return info, err
}

func TestContentAcceptsSupportedProfilePictureFormatsAndRejectsOversizedDimensions(t *testing.T) {
	t.Parallel()

	source := image.NewNRGBA(image.Rect(0, 0, 40, 20))
	for _, test := range []struct {
		name   string
		encode func(*bytes.Buffer) error
	}{
		{name: "png", encode: func(output *bytes.Buffer) error { return png.Encode(output, source) }},
		{name: "jpeg", encode: func(output *bytes.Buffer) error { return jpeg.Encode(output, source, nil) }},
		{name: "webp", encode: func(output *bytes.Buffer) error { return nativewebp.Encode(output, source, nil) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			var input bytes.Buffer
			if err := test.encode(&input); err != nil {
				t.Fatal(err)
			}
			content, err := filecontent.New(memoryvfs.New(), filecontent.Policy{MaximumConcurrentOperations: 2}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = content.NormalizeAndStoreProfilePicture(context.Background(), model.NewFileRevisionID(), bytes.NewReader(input.Bytes()), int64(input.Len()), time.Unix(1, 0)); err != nil {
				t.Fatalf("normalize: %v", err)
			}
		})
	}

	oversized := image.NewNRGBA(image.Rect(0, 0, 4097, 1))
	var input bytes.Buffer
	if err := png.Encode(&input, oversized); err != nil {
		t.Fatal(err)
	}
	content, err := filecontent.New(memoryvfs.New(), filecontent.Policy{MaximumConcurrentOperations: 2}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = content.NormalizeAndStoreProfilePicture(context.Background(), model.NewFileRevisionID(), bytes.NewReader(input.Bytes()), int64(input.Len()), time.Unix(1, 0)); !errors.Is(err, app.ErrInvalidProfilePicture) {
		t.Fatalf("oversized dimensions error = %v", err)
	}
	tooLarge := bytes.Repeat([]byte{'x'}, (5<<20)+1)
	if _, err = content.NormalizeAndStoreProfilePicture(context.Background(), model.NewFileRevisionID(), bytes.NewReader(tooLarge), int64(len(tooLarge)), time.Unix(1, 0)); !errors.Is(err, app.ErrInvalidProfilePicture) {
		t.Fatalf("oversized bytes error = %v", err)
	}
	if _, err = content.NormalizeAndStoreProfilePicture(context.Background(), model.NewFileRevisionID(), nil, -1, time.Unix(1, 0)); !errors.Is(err, app.ErrInvalidProfilePicture) {
		t.Fatalf("nil body error = %v", err)
	}
}

func TestContentAppliesEXIFOrientationBeforeProfilePictureCropping(t *testing.T) {
	t.Parallel()

	source := image.NewNRGBA(image.Rect(0, 0, 20, 40))
	for y := 0; y < 40; y++ {
		fill := color.NRGBA{R: 240, A: 255}
		if y >= 20 {
			fill = color.NRGBA{B: 240, A: 255}
		}
		for x := 0; x < 20; x++ {
			source.Set(x, y, fill)
		}
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, source, &jpeg.Options{Quality: 100}); err != nil {
		t.Fatal(err)
	}
	oriented := jpegWithEXIFOrientation(t, encoded.Bytes(), 6)
	content, err := filecontent.New(memoryvfs.New(), filecontent.Policy{MaximumConcurrentOperations: 2}, nil)
	if err != nil {
		t.Fatal(err)
	}
	revisionID := model.NewFileRevisionID()
	renditions, err := content.NormalizeAndStoreProfilePicture(context.Background(), revisionID, bytes.NewReader(oriented), int64(len(oriented)), time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	body, err := content.OpenProfilePictureRendition(context.Background(), revisionID, renditions[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := webp.Decode(body)
	_ = body.Close()
	if err != nil {
		t.Fatal(err)
	}
	topLeft := color.NRGBAModel.Convert(decoded.At(2, 2)).(color.NRGBA)
	bottomLeft := color.NRGBAModel.Convert(decoded.At(2, 17)).(color.NRGBA)
	if topLeft.B < 180 || bottomLeft.B < 180 || topLeft.R > 80 || bottomLeft.R > 80 {
		t.Fatalf("orientation was not applied before crop: top-left=%#v bottom-left=%#v", topLeft, bottomLeft)
	}
}

func jpegWithEXIFOrientation(t *testing.T, jpegBytes []byte, orientation uint16) []byte {
	t.Helper()
	if len(jpegBytes) < 2 || jpegBytes[0] != 0xff || jpegBytes[1] != 0xd8 {
		t.Fatal("invalid JPEG fixture")
	}
	payload := make([]byte, 32)
	copy(payload, []byte{'E', 'x', 'i', 'f', 0, 0, 'I', 'I', 0x2a, 0, 8, 0, 0, 0})
	binary.LittleEndian.PutUint16(payload[14:16], 1)
	binary.LittleEndian.PutUint16(payload[16:18], 0x0112)
	binary.LittleEndian.PutUint16(payload[18:20], 3)
	binary.LittleEndian.PutUint32(payload[20:24], 1)
	binary.LittleEndian.PutUint16(payload[24:26], orientation)
	segment := []byte{0xff, 0xe1, 0, byte(len(payload) + 2)}
	segment = append(segment, payload...)
	result := append([]byte(nil), jpegBytes[:2]...)
	result = append(result, segment...)
	return append(result, jpegBytes[2:]...)
}

func TestDefaultProfilePictureVersionTwoMatchesGoldenAndStoredBytes(t *testing.T) {
	t.Parallel()

	content, err := filecontent.New(memoryvfs.New(), filecontent.Policy{MaximumConcurrentOperations: 2}, nil)
	if err != nil {
		t.Fatal(err)
	}
	revisionID := model.NewFileRevisionID()
	renditions, err := content.GenerateAndStoreDefaultProfilePicture(context.Background(), revisionID, defaultProfilePictureV2Seed, time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	storedByName := make(map[string]model.FileRendition, len(renditions))
	for _, rendition := range renditions {
		storedByName[rendition.Name] = rendition
	}
	for _, golden := range []struct {
		size     int
		checksum string
		encoded  string
	}{
		{size: 128, checksum: defaultProfilePictureV2Checksum128, encoded: defaultProfilePictureV2Base64_128},
		{size: 256, checksum: defaultProfilePictureV2Checksum256, encoded: defaultProfilePictureV2Base64_256},
		{size: 512, checksum: defaultProfilePictureV2Checksum512, encoded: defaultProfilePictureV2Base64_512},
	} {
		want, decodeErr := base64.StdEncoding.DecodeString(golden.encoded)
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		rendered, renderErr := content.RenderDefaultProfilePicture(context.Background(), defaultProfilePictureV2Seed, golden.size)
		if renderErr != nil {
			t.Fatal(renderErr)
		}
		actual, readErr := io.ReadAll(rendered.Body)
		_ = rendered.Body.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !bytes.Equal(actual, want) || rendered.SHA256 != golden.checksum {
			t.Fatalf("version-two %d default changed: bytes_equal=%t checksum=%s", golden.size, bytes.Equal(actual, want), rendered.SHA256)
		}

		rendition, found := storedByName[fmt.Sprintf("profile_%d", golden.size)]
		if !found {
			t.Fatalf("profile_%d rendition missing", golden.size)
		}
		stored, openErr := content.OpenProfilePictureRendition(context.Background(), revisionID, rendition.ID)
		if openErr != nil {
			t.Fatal(openErr)
		}
		storedBytes, readErr := io.ReadAll(stored)
		_ = stored.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !bytes.Equal(storedBytes, want) || rendition.SHA256 != golden.checksum {
			t.Fatalf("persisted version-two %d default differs from transient rendering", golden.size)
		}
	}
}
