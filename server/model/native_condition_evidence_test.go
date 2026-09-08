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
)

func TestNativeConditionInventoryBindsOrderAndReview(t *testing.T) {
	empty := NewNativeConditionInventory()
	if empty.Validate() != nil {
		t.Fatal("invalid empty inventory")
	}
	corruptEmpty := empty
	corruptEmpty.Digest = SHA256Fingerprint([]byte("wrong"))
	if corruptEmpty.Validate() == nil {
		t.Fatal("accepted noncanonical empty inventory")
	}
	one, err := empty.Append([]byte("first"))
	if err != nil {
		t.Fatal(err)
	}
	two, err := one.Append([]byte("second"))
	if err != nil {
		t.Fatal(err)
	}
	reversed, err := empty.Append([]byte("second"))
	if err != nil {
		t.Fatal(err)
	}
	reversed, err = reversed.Append([]byte("first"))
	if err != nil {
		t.Fatal(err)
	}
	if two.Records != 2 || two.Digest == reversed.Digest {
		t.Fatal("inventory does not bind transition order")
	}
	base := strings.Repeat("a", 64)
	legacy, err := IntegrityReviewNativeDigest(base, empty)
	if err != nil || legacy != base {
		t.Fatal("empty native inventory changed legacy digest")
	}
	revised, err := IntegrityReviewNativeDigest(base, two)
	if err != nil || !validLowerSHA256(revised) || revised == base {
		t.Fatal("native inventory absent from finalization digest")
	}
	if _, err := IntegrityReviewNativeDigest("sha256:"+base, two); err == nil {
		t.Fatal("accepted incompatible review digest codec")
	}
}
