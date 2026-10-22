// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

func TestTimeBoundaryHelpers(t *testing.T) {
	t.Parallel()

	absent := model.OptionalTime{}
	require.False(t, absent.Valid, "zero optional time is absent")
	present := model.OptionalTimeFromMillis(1_700_000_000_000)
	require.True(t, present.Valid)
	require.Equal(t, int64(1_700_000_000_000), present.Millis())
	nullTime := NullTimeFromOptional(present)
	require.True(t, nullTime.Valid, "expected valid NullTime")
	back := OptionalTimeFromNullTime(nullTime)
	require.True(t, back.Valid)
	require.Equal(t, present.Millis(), back.Millis())
}
