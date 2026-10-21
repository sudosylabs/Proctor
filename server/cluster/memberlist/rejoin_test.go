// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package memberlist

import (
	"testing"

	"github.com/stretchr/testify/require"
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
	require.Equal(t, []string{"127.0.0.1:7946", "127.0.0.1:7947", "127.0.0.1:7948"}, first, "first batch")
	second, next := nextJoinBatch(seeds, next)
	require.Equal(t, []string{"127.0.0.1:7949", "127.0.0.1:7946", "127.0.0.1:7947"}, second, "second batch")
	require.Equal(t, 2, next, "next cursor")
}
