// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
package model

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNativeConditionInventoryBindsOrderAndReview(t *testing.T) {
	empty := NewNativeConditionInventory()
	require.NoError(t, empty.Validate(), "invalid empty inventory")
	corruptEmpty := empty
	corruptEmpty.Digest = SHA256Fingerprint([]byte("wrong"))
	require.Error(t, corruptEmpty.Validate(), "accepted noncanonical empty inventory")
	one, err := empty.Append([]byte("first"))
	require.NoError(t, err)
	two, err := one.Append([]byte("second"))
	require.NoError(t, err)
	reversed, err := empty.Append([]byte("second"))
	require.NoError(t, err)
	reversed, err = reversed.Append([]byte("first"))
	require.NoError(t, err)
	require.Equal(t, int64(2), two.Records, "inventory does not bind transition order")
	require.NotEqual(t, reversed.Digest, two.Digest, "inventory does not bind transition order")
	base := strings.Repeat("a", 64)
	legacy, err := IntegrityReviewNativeDigest(base, empty)
	require.NoError(t, err, "empty native inventory changed legacy digest")
	require.Equal(t, base, legacy, "empty native inventory changed legacy digest")
	revised, err := IntegrityReviewNativeDigest(base, two)
	require.NoError(t, err, "native inventory absent from finalization digest")
	require.True(t, validLowerSHA256(revised), "native inventory absent from finalization digest")
	require.NotEqual(t, base, revised, "native inventory absent from finalization digest")
	_, err = IntegrityReviewNativeDigest("sha256:"+base, two)
	require.Error(t, err, "accepted incompatible review digest codec")
}
