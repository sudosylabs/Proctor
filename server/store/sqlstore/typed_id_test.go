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

func TestNullEntityIDScanAndValue(t *testing.T) {
	t.Parallel()

	var n NullEntityID
	require.NoError(t, n.Scan(nil))
	require.False(t, n.Valid, "nil scan should be invalid")
	value, err := n.Value()
	require.NoError(t, err, "Value()")
	require.Nil(t, value, "Value()")

	id := model.NewID()
	require.NoError(t, n.Scan(id))
	require.True(t, n.Valid)
	require.Equal(t, id, n.ID)
	value, err = n.Value()
	require.NoError(t, err, "Value()")
	require.Equal(t, id, value, "Value()")
	require.Error(t, n.Scan("bad"), "invalid id should fail scan")
}

func TestScanUserID(t *testing.T) {
	t.Parallel()

	raw := model.NewID()
	id, err := ScanUserID(raw)
	require.NoError(t, err)
	require.Equal(t, raw, id.String())
	_, err = ScanUserID(nil)
	require.Error(t, err, "null required id should fail")
	value, err := UserIDValue(id)
	require.NoError(t, err, "UserIDValue()")
	require.Equal(t, raw, value, "UserIDValue()")
	_, err = UserIDValue("")
	require.Error(t, err, "invalid typed id must fail Value")
}

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
