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
	"image"
	"image/color"
	"image/png"
	"testing"
	"time"

	memoryvfs "github.com/sudosylabs/proctor/packages/vfs/memory"
	"github.com/sudosylabs/proctor/server/model"
)

func BenchmarkNormalizeAndStoreProfilePicture512(b *testing.B) {
	input := benchmarkContentPNG512(b)
	content, err := New(memoryvfs.New(), Policy{MaximumConcurrentOperations: 2}, nil)
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	b.ReportAllocs()
	b.SetBytes(int64(len(input)))
	for b.Loop() {
		revisionID := model.NewFileRevisionID()
		renditions, err := content.NormalizeAndStoreProfilePicture(ctx, revisionID, bytes.NewReader(input), int64(len(input)), time.Unix(1, 0))
		if err != nil {
			b.Fatal(err)
		}
		b.StopTimer()
		if err := content.RemoveProfilePictureRenditions(ctx, revisionID, renditions); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
	}
}

func BenchmarkRenderDefaultProfilePicture512(b *testing.B) {
	content, err := New(memoryvfs.New(), Policy{MaximumConcurrentOperations: 2}, nil)
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		picture, err := content.RenderDefaultProfilePicture(ctx, workTestSeed, 512)
		if err != nil {
			b.Fatal(err)
		}
		_ = picture.Body.Close()
	}
}

func benchmarkContentPNG512(b *testing.B) []byte {
	b.Helper()
	source := image.NewNRGBA(image.Rect(0, 0, 512, 512))
	for y := 0; y < 512; y++ {
		for x := 0; x < 512; x++ {
			source.SetNRGBA(x, y, color.NRGBA{R: byte(x), G: byte(y), B: byte(x ^ y), A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, source); err != nil {
		b.Fatal(err)
	}
	return encoded.Bytes()
}
