// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestPersistedStateErrorsRemainInternalAndSafe(t *testing.T) {
	t.Parallel()

	_, err := parsePersistedID("affiliation", "user_id", "raw-invalid-id", model.ParseUserID)
	var persisted *persistedStateError
	require.ErrorAs(t, err, &persisted)
	var invalidInput *store.ErrInvalidInput
	require.NotErrorAs(t, err, &invalidInput, "persisted corruption was classified as caller input: %v", err)
	require.NotContains(t, err.Error(), "raw-invalid-id")
	require.ErrorIs(t, err, store.ErrInvalidState)
	require.Error(t, errors.Unwrap(err), "persisted-state error discarded its cause")
}

func TestParseNullablePersistedIDDistinguishesAbsentAndMalformed(t *testing.T) {
	t.Parallel()

	absent, err := parseNullablePersistedID("session", "replaced_by_id", sql.NullString{}, model.ParseSessionCredentialID)
	require.NoError(t, err, "absent ID = %q", absent)
	require.True(t, absent.IsZero(), "absent ID = %q", absent)
	presentID := model.NewSessionCredentialID()
	present, err := parseNullablePersistedID("session", "replaced_by_id", sql.NullString{String: presentID.String(), Valid: true}, model.ParseSessionCredentialID)
	require.NoError(t, err, "present ID")
	require.Equal(t, presentID, present, "present ID")
	_, err = parseNullablePersistedID("session", "replaced_by_id", sql.NullString{Valid: true}, model.ParseSessionCredentialID)
	var persisted *persistedStateError
	require.ErrorAs(t, err, &persisted)
	require.Equal(t, "replaced_by_id", persisted.Field)
}
