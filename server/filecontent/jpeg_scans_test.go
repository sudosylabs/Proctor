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
	"encoding/binary"
	"image/jpeg"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	memoryvfs "github.com/sudosylabs/proctor/packages/vfs/memory"
	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

func TestJPEGScanCountAllowsEncoderScanCountsAndRejectsMore(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		scans int
		want  error
	}{
		{name: "one scan", scans: 1},
		{name: "at the bound", scans: maximumJPEGScans},
		{name: "above the bound", scans: maximumJPEGScans + 1, want: errJPEGTooManyScans},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			encoded := progressiveJPEGWithScans(64, 64, test.scans)
			_, err := jpeg.Decode(bytes.NewReader(encoded))
			require.NoError(t, err, "fixture must be a JPEG the standard decoder accepts")
			require.Equal(t, test.want, checkJPEGScanCount(bytes.NewReader(encoded)))
		})
	}
}

func TestJPEGScanBombIsRejectedBeforeDecoding(t *testing.T) {
	t.Parallel()

	// Without the guard, image/jpeg spends tens of milliseconds per scan on
	// this 4096x4096 image, so these 5,000 scans would take minutes.
	bomb := progressiveJPEGWithScans(4096, 4096, 5000)
	require.Less(t, len(bomb), 256<<10)
	content, err := New(memoryvfs.New(), Policy{MaximumConcurrentOperations: 2}, nil)
	require.NoError(t, err)

	t.Run("profile picture", func(t *testing.T) {
		started := time.Now()
		_, err := content.NormalizeAndStoreProfilePicture(context.Background(), model.NewFileRevisionID(), bytes.NewReader(bomb), int64(len(bomb)), time.Unix(1, 0))
		require.ErrorIs(t, err, app.ErrInvalidProfilePicture)
		require.Less(t, time.Since(started), 5*time.Second)
	})
	t.Run("exam resource", func(t *testing.T) {
		started := time.Now()
		_, err := content.StoreExamResource(context.Background(), model.NewFileRevisionID(), model.ExamResourceMediaJPEG, bytes.NewReader(bomb), int64(len(bomb)), time.Now().UTC())
		require.ErrorIs(t, err, ErrInvalidExamResourceContent)
		require.Less(t, time.Since(started), 5*time.Second)
	})
}

// progressiveJPEGWithScans builds a valid greyscale progressive JPEG whose
// scans each send one AC band of all-zero coefficients as end-of-band runs, so
// every scan costs the decoder a full pass while adding only a few bytes.
func progressiveJPEGWithScans(width, height, scans int) []byte {
	var encoded bytes.Buffer
	segment := func(marker byte, payload []byte) {
		encoded.Write([]byte{0xff, marker})
		encoded.Write(binary.BigEndian.AppendUint16(nil, uint16(len(payload)+2))) //nolint:gosec // Test segments are far below 64 KiB.
		encoded.Write(payload)
	}
	encoded.Write([]byte{0xff, 0xd8})
	quantization := bytes.Repeat([]byte{1}, 65)
	quantization[0] = 0
	segment(0xdb, quantization)
	frame := binary.BigEndian.AppendUint16([]byte{8}, uint16(height)) //nolint:gosec // Test dimensions fit JPEG's 16-bit fields.
	frame = binary.BigEndian.AppendUint16(frame, uint16(width))       //nolint:gosec // Test dimensions fit JPEG's 16-bit fields.
	segment(0xc2, append(frame, 1, 1, 0x11, 0))
	// One AC Huffman code, the single bit 0, for symbol 0xe0: an end-of-band
	// run of 2^14 blocks plus 14 extra bits.
	huffman := append([]byte{0x10, 1}, make([]byte, 15)...)
	segment(0xc4, append(huffman, 0xe0))
	blocks := ((width + 7) / 8) * ((height + 7) / 8)
	runs := (blocks + 1<<14 - 1) / (1 << 14)
	entropy := make([]byte, (runs*15+7)/8)
	for range scans {
		segment(0xda, []byte{1, 1, 0x00, 1, 63, 0})
		encoded.Write(entropy)
	}
	encoded.Write([]byte{0xff, 0xd9})
	return encoded.Bytes()
}
