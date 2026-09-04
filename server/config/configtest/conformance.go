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
	"errors"
	"testing"

	"github.com/sudosylabs/proctor/server/config"
)

type Factory func(testing.TB) config.BackingStore

func Run(tb testing.TB, factory Factory) {
	tb.Helper()

	backing := factory(tb)
	tb.Cleanup(func() {
		if err := backing.Close(); err != nil {
			tb.Errorf("Close() error = %v", err)
		}
	})

	input := []byte("first")
	if err := backing.Save(context.Background(), input); err != nil {
		tb.Fatalf("Save() error = %v", err)
	}
	input[0] = 'X'
	loaded, err := backing.Load(context.Background())
	if err != nil {
		tb.Fatalf("Load() error = %v", err)
	}
	if string(loaded) != "first" {
		tb.Fatalf("Load() = %q", loaded)
	}
	loaded[0] = 'Y'
	reloaded, err := backing.Load(context.Background())
	if err != nil {
		tb.Fatalf("second Load() error = %v", err)
	}
	if string(reloaded) != "first" {
		tb.Fatalf("Load exposed mutable state: %q", reloaded)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := backing.Save(ctx, []byte("second")); !errors.Is(err, context.Canceled) {
		tb.Fatalf("canceled Save() error = %v", err)
	}
}
