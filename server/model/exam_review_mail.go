// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
package model

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
)

// ResultReleaseOccurrenceID identifies one approved Review revision. Releasing
// a newly reviewed inventory is a different transition; retries are the same.
func ResultReleaseOccurrenceID(review SubmissionReviewID, revision int64) (MailOccurrenceID, error) {
	if !review.IsValid() || revision < 1 {
		return "", errors.New("model: invalid result release identity")
	}
	h := sha256.New()
	h.Write([]byte("proctor.result-release.v1\x00"))
	h.Write([]byte(review.String()))
	var raw [8]byte
	binary.BigEndian.PutUint64(raw[:], uint64(revision))
	h.Write(raw[:])
	digest := h.Sum(nil)
	return MailOccurrenceID(idEncoding.EncodeToString(digest[:16])), nil
}
