// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package memberlist

import (
	"reflect"
	"testing"
)

func TestNextJoinBatchDeduplicatesBoundsAndRotatesCandidates(t *testing.T) {
	t.Parallel()

	seeds := []string{
		" 127.0.0.1:7946 ",
		"127.0.0.1:7947",
		"127.0.0.1:7946",
		"127.0.0.1:7948",
		"127.0.0.1:7949",
	}
	first, next := nextJoinBatch(seeds, 0)
	if want := []string{"127.0.0.1:7946", "127.0.0.1:7947", "127.0.0.1:7948"}; !reflect.DeepEqual(first, want) {
		t.Fatalf("first batch = %v, want %v", first, want)
	}
	second, next := nextJoinBatch(seeds, next)
	if want := []string{"127.0.0.1:7949", "127.0.0.1:7946", "127.0.0.1:7947"}; !reflect.DeepEqual(second, want) {
		t.Fatalf("second batch = %v, want %v", second, want)
	}
	if next != 2 {
		t.Fatalf("next cursor = %d, want 2", next)
	}
}
