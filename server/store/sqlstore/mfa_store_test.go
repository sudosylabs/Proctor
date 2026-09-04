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
	"strings"
	"testing"
	"time"

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
			if !errors.As(err, &persisted) {
				t.Fatalf("model() error = %v, want persisted-state error", err)
			}
			if persisted.Entity != "mfa_credential" || persisted.Field != test.field {
				t.Fatalf("persisted-state context = %s.%s, want mfa_credential.%s", persisted.Entity, persisted.Field, test.field)
			}
			if strings.Contains(err.Error(), valid.EncryptedSecret) {
				t.Fatalf("model() error exposed encrypted secret: %v", err)
			}
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
	if err != nil {
		t.Fatal(err)
	}
	if credential.ID != id ||
		credential.UserID != userID ||
		credential.EncryptionKeyID != row.EncryptionKeyID ||
		!credential.PendingExpiresAt.Valid || !credential.PendingExpiresAt.Time.Equal(expires) ||
		credential.ActivatedAt.Valid {
		t.Fatalf("row.model() = %#v", credential)
	}

	activeRow := row
	activeRow.State = model.MFAStateActive
	activeRow.PendingExpiresAt = sql.NullTime{}
	activeRow.ActivatedAt = sql.NullTime{Time: created, Valid: true}
	activeRow.LastUsedTimeStep = 42
	active, err := activeRow.model()
	if err != nil {
		t.Fatal(err)
	}
	if !active.IsActive() ||
		active.ActivatedAt.Millis() != model.MillisFromTime(created) ||
		active.LastUsedTimeStep != 42 {
		t.Fatalf("active row.model() = %#v", active)
	}
}

func replaceMFACredentialRow(row mfaCredentialRow, replace func(*mfaCredentialRow)) mfaCredentialRow {
	replace(&row)
	return row
}
