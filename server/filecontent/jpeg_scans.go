// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package filecontent

import (
	"bufio"
	"errors"
	"io"
)

// maximumJPEGScans bounds how many start-of-scan segments an uploaded JPEG may
// carry. Go's image/jpeg walks the whole coefficient buffer once per scan and
// has no scan limit of its own, so a small progressive file with thousands of
// scans costs minutes of CPU while it holds a content permit. Encoders emit
// one scan for baseline images and at most 18 for libjpeg's progressive
// scripts (10 for colour, 6 for greyscale, 18 for CMYK).
const maximumJPEGScans = 32

var errJPEGTooManyScans = errors.New("JPEG has too many scans")

// checkJPEGScanCount walks the JPEG marker stream the way image/jpeg does and
// fails as soon as it has seen more than maximumJPEGScans scans. It reads only
// marker segments and skips entropy-coded data byte by byte, so its cost is
// linear in the file size. Malformed or truncated streams are left for the
// decoder to reject.
func checkJPEGScanCount(r io.Reader) error {
	reader := bufio.NewReader(r)
	var start [2]byte
	if _, err := io.ReadFull(reader, start[:]); err != nil || start != [2]byte{0xff, 0xd8} {
		return nil
	}
	scans := 0
	for {
		marker, err := nextJPEGMarker(reader)
		if err != nil || marker == 0xd9 {
			return nil
		}
		var length [2]byte
		if _, err := io.ReadFull(reader, length[:]); err != nil {
			return nil
		}
		n := int(length[0])<<8 | int(length[1])
		if n < 2 {
			return nil
		}
		if marker == 0xda {
			scans++
			if scans > maximumJPEGScans {
				return errJPEGTooManyScans
			}
		}
		if _, err := reader.Discard(n - 2); err != nil {
			return nil
		}
	}
}

// nextJPEGMarker returns the next marker that image/jpeg would act on. Like
// the decoder it skips stray bytes, 0xff fill bytes, stuffed 0xff00 pairs and
// restart markers, which also skips a scan's entropy-coded data.
func nextJPEGMarker(reader *bufio.Reader) (byte, error) {
	for {
		value, err := reader.ReadByte()
		if err != nil {
			return 0, err
		}
		if value != 0xff {
			continue
		}
		for value == 0xff {
			if value, err = reader.ReadByte(); err != nil {
				return 0, err
			}
		}
		if value == 0x00 || (value >= 0xd0 && value <= 0xd7) {
			continue
		}
		return value, nil
	}
}
