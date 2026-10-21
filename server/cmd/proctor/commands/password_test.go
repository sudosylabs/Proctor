// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package commands

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReadPrivatePasswordBoundsAndNormalizesInput(t *testing.T) {
	t.Parallel()

	value, err := readPrivatePassword(strings.NewReader("private-value\r\n"), englishCommandText())
	require.NoError(t, err, "value")
	require.Equal(t, "private-value", value, "value")
	for _, input := range []string{"", strings.Repeat("x", 4097)} {
		_, err = readPrivatePassword(strings.NewReader(input), englishCommandText())
		require.Error(t, err, "readPrivatePassword(%d bytes) error = nil", len(input))
	}
	_, err = readPrivatePassword(nil, englishCommandText())
	require.Error(t, err, "readPrivatePassword(nil) error = nil")
}
