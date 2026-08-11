// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

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

const defaultProfilePictureV1Seed = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
const defaultProfilePictureV1Checksum128 = "151a000b4cf6b649c48d10ed4bf7166aa53408b966756993e6ce98781ce10ad1"
const defaultProfilePictureV1Checksum256 = "bc67a0955fc59e501da0af710330ad542fbc40a4df8467688e23e7a30be824bc"
const defaultProfilePictureV1Checksum512 = "c2fc3e7daa78437f44033a6397b5b23b99920ce446f4d82bcd98c3b947ac61c2"
const defaultProfilePictureV1Base64_128 = "UklGRkgDAABXRUJQVlA4TDwDAAAvf8AfEE0gIMA0+g4AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA1toGAIjoPwICaLM9L++LAQAAcHsAVXoafLk8f/UPppTTVNDfTKM15kYS99ODKSZkDbPLNK+Zs1wQCEAOHAEAAAAAAEALAAAAKAAAAEABAFAAAAAAAAAAgAcAAAAAAACud7nszszk7v4QAAAAAAAAAAAAAAAAAAAAAHAAAAAAAAAAAAAAAAAAAHAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAKAAAAAAAAAAAAAAAAAAACAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAECCQAgAAAAAAAAGAAABgAGAAAAAAAMAAAAAAAAAAAAABAAAABAgkAAAAAAAgAAAAAAAAYAAAGAAAMwAAAAAAAAEAAAAAAAECAQBKAAQAABjAAAAAAAAAAAAAAAAAAAAAAAAAAABhAAAAAADggACus2rP67Xu9rXbXrQDAI6WXjif/PIQ/s2Io8xA0Aqq42ZNcIAtEh2PsZ/i+xQV+g22MsfRXDEGGMErXNBmq4He1RqWZ+tL3IUyFWFjNpPO0ypo5Fk2/pL0VP31jHobquLAbZq36COjA2QbgAANZE6qA80oP8MvQAjz3D34E1yNZkuyg/jda96i8VfqezYPOVpj/BVwRy461/o4kFQMjPwVtgKmvCY7AXQc+vbowAE/7cX8X8k7zhw6eOewjDwFxXuazWJ627AE+BVOWuf/JMfcHVtGIfgdVBlrgtFzZQZpkhNd2XIa4ejWOUOiLtLHh4DNySoEdpNx9SRehiMDN6dBDpUly0zUdXZ2Vd6Ewbjcvs+z7Qvaax3WXY2sDsL7sYN73ddbbR///grbyuufu+sLr8AOqE6DuAQA="
const defaultProfilePictureV1Base64_256 = "UklGRg4EAABXRUJQVlA4TAIEAAAv/8A/EE0gIMA0qgAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAADAW9uCA4joPwICbCLjevXtewAAEBH1Fr5kkTijdPw2c7hsCcdswd6+MRQfk4m7lx76GkVEo55bIZU16uUuSA6sG6sQ1OEHFCIS3rVjahfqeDK5HawNRKYGMiWjgDKZHYUXzbkYU3LZbxkPVGBieNzUCBwn0elo5rWHc7wmbOqgmSqrfNIWkOa7TfwaSI99++kgyb9Pfpblk6RyyKKBN7gcEGAjOAAAAAAAAGAAAACAAQBgADAAAAwAAAAAAAAAYAAAAAAAAMCb3btrk7TX/Q0AAAAAAAAAAAAAAAAAAAAAPAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAPAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAECCQAAACAAAAAAAAAMBgAGAAAAAAAMAAAAAAAAAAAAAAAAEABAgkAAAgAADAAAAAAAAAwAAAAAAAMAADAAAAAAAAAQAAAAAAASIIAAMAAAxgAAAAAAAAAAAAAAAAAAAAAAAAAAAQgAEAAABwgABK4OwX9fJr7T9LT3MBAPAI3nb/LseNa7SNcFP7PJoFdNs6oRnMtPsSmF7Ln7ekwdt7F2Ega9b9sNENL/qd8toO/oRImrTYjyIKlpA/OSt3TIFJCPQcZI1d+IticIk2u4mSxJZH2RGL85QCuGwmo7I0OAistYy+u4uiirYEtmLN0RsYzhvK00AoINp/BkKm/lcAyPQXucJlkFZUPE+A0wlwcQFwnEmDSum7UzJPE8APxekpf8Q191/oPZcMSGjRjxckA87FX9pPO2c7VVD26EsC9KN4KRPELCtMkgFddzMZVQCmcz2O4Y7+lQKYnf1yugCWNxRvz78+AUyvGLLwKx3hz8Pvc1yC8lMA7cvv4A+jlhSwevjkgGxcb+6PC2pntIXa8r8zCqAwYfDGyOdoVMFHN1FT6a+H2jqLEXTMINXXuhsDBQLj7eACidlmYIHJXDuEQEfQ6F4gL2ZRo+yu9puIQrHePbf2M/HktbMNTk2COE5EgbLAxwdeZG/l8ydSuT9+2XdZ/YQWEmrfZet/h7r1P+6QrrjD+m/dDQA="
const defaultProfilePictureV1Base64_512 = "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"

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
			content, err := filecontent.New(backend.open(t))
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
	content, err := filecontent.New(backend)
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
			content, err := filecontent.New(memoryvfs.New())
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
	content, err := filecontent.New(memoryvfs.New())
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
	content, err := filecontent.New(memoryvfs.New())
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

func TestDefaultProfilePictureVersionOneMatchesGoldenAndStoredBytes(t *testing.T) {
	t.Parallel()

	content, err := filecontent.New(memoryvfs.New())
	if err != nil {
		t.Fatal(err)
	}
	revisionID := model.NewFileRevisionID()
	renditions, err := content.GenerateAndStoreDefaultProfilePicture(context.Background(), revisionID, defaultProfilePictureV1Seed, time.Unix(1, 0))
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
		{size: 128, checksum: defaultProfilePictureV1Checksum128, encoded: defaultProfilePictureV1Base64_128},
		{size: 256, checksum: defaultProfilePictureV1Checksum256, encoded: defaultProfilePictureV1Base64_256},
		{size: 512, checksum: defaultProfilePictureV1Checksum512, encoded: defaultProfilePictureV1Base64_512},
	} {
		want, decodeErr := base64.StdEncoding.DecodeString(golden.encoded)
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		rendered, renderErr := content.RenderDefaultProfilePicture(context.Background(), defaultProfilePictureV1Seed, golden.size)
		if renderErr != nil {
			t.Fatal(renderErr)
		}
		actual, readErr := io.ReadAll(rendered.Body)
		_ = rendered.Body.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !bytes.Equal(actual, want) || rendered.SHA256 != golden.checksum {
			t.Fatalf("version-one %d default changed: bytes_equal=%t checksum=%s", golden.size, bytes.Equal(actual, want), rendered.SHA256)
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
			t.Fatalf("persisted version-one %d default differs from transient rendering", golden.size)
		}
	}
}
