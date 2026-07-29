// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"testing"

	"github.com/sudosylabs/proctor/server/store/storetest"
)

func TestInstallationStore(t *testing.T) {
	StoreTest(t, storetest.TestInstallationStore)
}
