// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEntityIDsAreDistinctTypes(t *testing.T) {
	t.Parallel()

	// Compile-time guard: distinct entity IDs must not be freely assignable.
	// The following would fail to compile if uncommented:
	//   var user UserID
	//   var class ClassID = user
	user := NewUserID()
	class := NewClassID()
	require.NotEqual(t, class.String(), user.String(), "generated user and class IDs collided")
	require.True(t, user.IsValid(), "generated IDs must be valid")
	require.True(t, class.IsValid(), "generated IDs must be valid")
	require.False(t, user.IsZero(), "generated IDs must not be zero")
	require.False(t, class.IsZero(), "generated IDs must not be zero")
}

func TestSubmissionIDUsesCanonicalTypedIDContract(t *testing.T) {
	t.Parallel()

	id := NewSubmissionID()
	require.True(t, id.IsValid())
	require.False(t, id.IsZero())
	parsed, err := ParseSubmissionID(id.String())
	require.NoError(t, err, "ParseSubmissionID")
	require.Equal(t, id, parsed, "ParseSubmissionID")
}

func TestParseEntityIDRejectsInvalid(t *testing.T) {
	t.Parallel()

	_, err := ParseUserID("")
	require.Error(t, err, "empty user id accepted")
	_, err = ParseUserID("not-a-valid-id")
	require.Error(t, err, "short user id accepted")
	raw := NewID()
	got, err := ParseUserID(raw)
	require.NoError(t, err)
	require.Equal(t, raw, got.String(), "ParseUserID() = %q", got)
}

func TestEntityIDJSONRoundTrip(t *testing.T) {
	t.Parallel()

	id := NewSessionID()
	data, err := json.Marshal(id)
	require.NoError(t, err)
	var decoded SessionID
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.Equal(t, id, decoded, "round trip")

	require.NoError(t, json.Unmarshal([]byte(`""`), &decoded))
	require.True(t, decoded.IsZero())
	require.Error(t, json.Unmarshal([]byte(`"bad"`), &decoded), "invalid id must fail UnmarshalJSON")
}

func TestLegacyStringIDStillCompilesAlongsideTypedIDs(t *testing.T) {
	t.Parallel()

	// Expand phase: plain NewID()/IsValidOpaqueID remain for unmigrated aggregates.
	legacy := NewID()
	require.True(t, IsValidOpaqueID(legacy))
	typed := UserID(legacy)
	require.True(t, typed.IsValid(), typed)
}
