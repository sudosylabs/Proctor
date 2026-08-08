// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"database/sql"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

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
		EncryptionKeyID:  "0123456789abcdef",
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
