// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

// Package configtest provides reusable configuration-backing conformance tests.
package configtest

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/config"
)

type Factory func(testing.TB) config.BackingStore

func Run(tb testing.TB, factory Factory) {
	tb.Helper()

	backing := factory(tb)
	tb.Cleanup(func() {
		assert.NoError(tb, backing.Close())
	})

	input := []byte("first")
	require.NoError(tb, backing.Save(context.Background(), input))
	input[0] = 'X'
	loaded, err := backing.Load(context.Background())
	require.NoError(tb, err)
	require.Equal(tb, "first", string(loaded), "Load()")
	loaded[0] = 'Y'
	reloaded, err := backing.Load(context.Background())
	require.NoError(tb, err)
	require.Equal(tb, "first", string(reloaded), "Load exposed mutable state")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(tb, backing.Save(ctx, []byte("second")), context.Canceled)
}
