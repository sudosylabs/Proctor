// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

func TestMFACredentialRowRehydrationRejectsInvalidPersistedState(t *testing.T) {
	t.Parallel()

	created := model.TimeFromMillis(1_700_000_000_000)
	valid := mfaCredentialRow{
		ID:               model.NewMFACredentialID().String(),
		CreatedAt:        created,
		UpdatedAt:        created,
		UserID:           model.NewUserID().String(),
		State:            model.MFAStatePending,
		EncryptedSecret:  "ciphertext-must-not-appear",
		EncryptionKeyID:  "0123456789abcdef0123456789abcdef",
		PendingExpiresAt: sql.NullTime{Time: created.Add(time.Minute), Valid: true},
	}
	tests := []struct {
		name  string
		row   mfaCredentialRow
		field string
	}{
		{name: "credential id", row: replaceMFACredentialRow(valid, func(row *mfaCredentialRow) { row.ID = "bad" }), field: "id"},
		{name: "user id", row: replaceMFACredentialRow(valid, func(row *mfaCredentialRow) { row.UserID = "bad" }), field: "user_id"},
		{name: "domain state", row: replaceMFACredentialRow(valid, func(row *mfaCredentialRow) { row.State = "unknown" }), field: "state"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.row.model()
			var persisted *persistedStateError
			require.ErrorAs(t, err, &persisted, "model() error")
			require.Equal(t, "mfa_credential", persisted.Entity, "persisted-state context")
			require.Equal(t, test.field, persisted.Field, "persisted-state context")
			require.NotContains(t, err.Error(), valid.EncryptedSecret)
		})
	}
}

func TestMFACredentialRowConversion(t *testing.T) {
	id := model.NewMFACredentialID()
	userID := model.NewUserID()
	created := model.TimeFromMillis(1_700_000_000_000)
	expires := created.Add(10 * time.Minute)
	row := mfaCredentialRow{
		ID:               id.String(),
		CreatedAt:        UTCTime(created),
		UpdatedAt:        UTCTime(created),
		UserID:           userID.String(),
		State:            model.MFAStatePending,
		EncryptedSecret:  "ciphertext",
		EncryptionKeyID:  "0123456789abcdef0123456789abcdef",
		PendingExpiresAt: sql.NullTime{Time: expires, Valid: true},
	}
	credential, err := row.model()
	require.NoError(t, err)
	require.Equal(t, id, credential.ID)
	require.Equal(t, userID, credential.UserID)
	require.Equal(t, row.EncryptionKeyID, credential.EncryptionKeyID)
	require.True(t, credential.PendingExpiresAt.Valid)
	require.True(t, credential.PendingExpiresAt.Time.Equal(expires))
	require.False(t, credential.ActivatedAt.Valid)

	activeRow := row
	activeRow.State = model.MFAStateActive
	activeRow.PendingExpiresAt = sql.NullTime{}
	activeRow.ActivatedAt = sql.NullTime{Time: created, Valid: true}
	activeRow.LastUsedTimeStep = 42
	active, err := activeRow.model()
	require.NoError(t, err)
	require.True(t, active.IsActive())
	require.Equal(t, model.MillisFromTime(created), active.ActivatedAt.Millis())
	require.Equal(t, int64(42), active.LastUsedTimeStep)
}

func replaceMFACredentialRow(row mfaCredentialRow, replace func(*mfaCredentialRow)) mfaCredentialRow {
	replace(&row)
	return row
}
