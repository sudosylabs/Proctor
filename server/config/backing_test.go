// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/config/configtest"
)

func TestMemoryStoreConformance(t *testing.T) {
	t.Parallel()
	configtest.Run(t, func(testing.TB) config.BackingStore {
		return config.NewMemoryStore(nil)
	})
}

func TestFileStoreConformance(t *testing.T) {
	t.Parallel()
	configtest.Run(t, func(tb testing.TB) config.BackingStore {
		path := filepath.Join(tb.TempDir(), "config.json")
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			tb.Fatal(err)
		}
		store, err := config.NewFileStore(path)
		if err != nil {
			tb.Fatal(err)
		}
		return store
	})
}
