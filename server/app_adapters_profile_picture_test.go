// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
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
	"github.com/sudosylabs/proctor/server/model"
)

func TestFileContentAdapterPurgesBoundedRevisionPrefixIdempotentlyOnLocalVFS(t *testing.T) {
	filesystem, err := localvfs.New(filepath.Join(t.TempDir(), "vfs"))
	if err != nil {
		t.Fatal(err)
	}
	adapter := mustFileContentAdapter(t, filesystem)
	generatedRevisionID := model.NewFileRevisionID()
	generated, err := adapter.GenerateAndStoreDefaultProfilePicture(context.Background(), generatedRevisionID, "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", time.Now())
	if err != nil {
		t.Fatalf("generate complete local rendition set: %v", err)
	}
	if len(generated) != 3 {
		t.Fatalf("generated local renditions = %d", len(generated))
	}
	revisionID, referencedRevisionID := model.NewFileRevisionID(), model.NewFileRevisionID()
	firstID, secondID := model.NewFileRenditionID(), model.NewFileRenditionID()
	for _, renditionID := range []model.FileRenditionID{firstID, secondID} {
		body := []byte("partial")
		if err = adapter.content.StageProfilePictureRendition(context.Background(), revisionID, renditionID, bytes.NewReader(body), int64(len(body))); err != nil {
			t.Fatal(err)
		}
	}
	referencedID := model.NewFileRenditionID()
	referencedBody := []byte("referenced")
	if err = adapter.content.StageProfilePictureRendition(context.Background(), referencedRevisionID, referencedID, bytes.NewReader(referencedBody), int64(len(referencedBody))); err != nil {
		t.Fatal(err)
	}
	if err = adapter.RemoveFileRevisionContent(context.Background(), revisionID, nil); err != nil {
		t.Fatal(err)
	}
	if err = adapter.RemoveFileRevisionContent(context.Background(), revisionID, []model.FileRenditionID{firstID, secondID}); err != nil {
		t.Fatal(err)
	}
	if body, openErr := adapter.OpenProfilePictureRendition(context.Background(), revisionID, firstID); !errors.Is(openErr, vfspkg.ErrNotFound) {
		if body != nil {
			_ = body.Close()
		}
		t.Fatalf("open removed rendition error = %v", openErr)
	}
	if body, openErr := adapter.OpenProfilePictureRendition(context.Background(), referencedRevisionID, referencedID); openErr != nil {
		t.Fatalf("referenced rendition was removed: %v", openErr)
	} else {
		_ = body.Close()
	}
}

func TestFileContentAdapterNormalizesWithoutUpscalingAndUsesPrivateIDKeys(t *testing.T) {
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
	filesystem := memoryvfs.New()
	adapter := mustFileContentAdapter(t, filesystem)
	revisionID := model.NewFileRevisionID()
	renditions, err := adapter.NormalizeAndStoreProfilePicture(context.Background(), revisionID, bytes.NewReader(input.Bytes()), int64(input.Len()), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(renditions) != 3 {
		t.Fatalf("renditions = %d", len(renditions))
	}
	for _, rendition := range renditions {
		body, openErr := adapter.OpenProfilePictureRendition(context.Background(), revisionID, rendition.ID)
		if openErr != nil {
			t.Fatal(openErr)
		}
		decoded, decodeErr := webp.Decode(body)
		_ = body.Close()
		if decodeErr != nil {
			t.Fatalf("decode %s: %v", rendition.Name, decodeErr)
		}
		if decoded.Bounds().Dx() != 20 || decoded.Bounds().Dy() != 20 {
			t.Fatalf("%s dimensions = %v", rendition.Name, decoded.Bounds())
		}
	}
	page, err := filesystem.List(context.Background(), vfspkg.ListOptions{Prefix: "files/", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range page.Entries {
		if bytes.Contains([]byte(entry.Path), []byte("student")) || bytes.Contains([]byte(entry.Path), []byte("profile_")) {
			t.Fatalf("path exposes domain/user naming: %q", entry.Path)
		}
	}
}

func TestDefaultProfilePictureRenderingIsDeterministicAndMatchesStoredRenditions(t *testing.T) {
	adapter := mustFileContentAdapter(t, memoryvfs.New())
	seed := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	first, err := adapter.RenderDefaultProfilePicture(context.Background(), seed, 256)
	if err != nil {
		t.Fatal(err)
	}
	firstBytes, err := io.ReadAll(first.Body)
	if err != nil {
		t.Fatal(err)
	}
	_ = first.Body.Close()
	second, err := adapter.RenderDefaultProfilePicture(context.Background(), seed, 256)
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := io.ReadAll(second.Body)
	if err != nil {
		t.Fatal(err)
	}
	_ = second.Body.Close()
	if !bytes.Equal(firstBytes, secondBytes) || first.SHA256 != second.SHA256 {
		t.Fatal("same seed did not render the same fallback")
	}
	revisionID := model.NewFileRevisionID()
	renditions, err := adapter.GenerateAndStoreDefaultProfilePicture(context.Background(), revisionID, seed, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, rendition := range renditions {
		if rendition.Name != "profile_256" {
			continue
		}
		if rendition.SHA256 != first.SHA256 || rendition.Width != 256 || rendition.Height != 256 {
			t.Fatalf("stored rendition does not match fallback: %#v", rendition)
		}
	}
}

func TestFileContentAdapterAcceptsSupportedFormatsAndRejectsOversizedDimensions(t *testing.T) {
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
			adapter := mustFileContentAdapter(t, memoryvfs.New())
			if _, err := adapter.NormalizeAndStoreProfilePicture(context.Background(), model.NewFileRevisionID(), bytes.NewReader(input.Bytes()), int64(input.Len()), time.Now()); err != nil {
				t.Fatalf("normalize: %v", err)
			}
		})
	}
	oversized := image.NewNRGBA(image.Rect(0, 0, 4097, 1))
	var input bytes.Buffer
	if err := png.Encode(&input, oversized); err != nil {
		t.Fatal(err)
	}
	adapter := mustFileContentAdapter(t, memoryvfs.New())
	if _, err := adapter.NormalizeAndStoreProfilePicture(context.Background(), model.NewFileRevisionID(), bytes.NewReader(input.Bytes()), int64(input.Len()), time.Now()); !errors.Is(err, app.ErrInvalidProfilePicture) {
		t.Fatalf("oversized dimensions error = %v", err)
	}
}

func TestFileContentAdapterAppliesEXIFOrientationBeforeCropping(t *testing.T) {
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
	adapter := mustFileContentAdapter(t, memoryvfs.New())
	revisionID := model.NewFileRevisionID()
	renditions, err := adapter.NormalizeAndStoreProfilePicture(context.Background(), revisionID, bytes.NewReader(oriented), int64(len(oriented)), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	body, err := adapter.OpenProfilePictureRendition(context.Background(), revisionID, renditions[0].ID)
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

func mustFileContentAdapter(t *testing.T, filesystem vfspkg.FileSystem) fileContentAdapter {
	t.Helper()
	adapter, err := newFileContentAdapter(filesystem)
	if err != nil {
		t.Fatal(err)
	}
	return adapter
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
