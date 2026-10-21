// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package commands

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDevSeedHelpAndArgumentValidation(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, Execute(context.Background(), []string{"dev", "seed", "--help"}, &out, &out))
	for _, name := range []string{"--profile", "--seed", "--state-dir", "--refresh-sittings"} {
		assert.Contains(t, out.String(), name)
	}
	require.Error(t, Execute(context.Background(), []string{"dev", "seed", "extra"}, &out, &out), "accepted positional arguments")
}
