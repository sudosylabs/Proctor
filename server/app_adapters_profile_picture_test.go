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
	"testing"
	"time"

	"github.com/HugoSmits86/nativewebp"
	"golang.org/x/image/webp"

	memoryvfs "github.com/sudosylabs/proctor/packages/vfs/memory"
	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

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
	adapter := fileContentAdapter{filesystem: memoryvfs.New()}
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
		path := profilePictureRenditionPath(revisionID, rendition.ID)
		if bytes.Contains([]byte(path), []byte("student")) || bytes.Contains([]byte(path), []byte("profile_")) {
			t.Fatalf("path exposes domain/user naming: %q", path)
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
			adapter := fileContentAdapter{filesystem: memoryvfs.New()}
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
	adapter := fileContentAdapter{filesystem: memoryvfs.New()}
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
	adapter := fileContentAdapter{filesystem: memoryvfs.New()}
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
