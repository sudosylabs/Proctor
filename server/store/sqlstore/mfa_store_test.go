// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"testing"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store/storetest"
)

func TestMFAStore(t *testing.T) {
	StoreTest(t, storetest.TestMFAStore)
}

func TestMFACredentialRowConversion(t *testing.T) {
	row := mfaCredentialRow{
		ID: model.NewId(), CreateAt: 1, UpdateAt: 2,
		UserID: model.NewId(), State: model.MFAStatePending,
		EncryptedSecret: "ciphertext", EncryptionKeyID: "0123456789abcdef",
		PendingExpiresAt: 3,
	}
	credential := row.model()
	if credential.Id != row.ID ||
		credential.UserId != row.UserID ||
		credential.EncryptionKeyId != row.EncryptionKeyID {
		t.Fatalf("row.model() = %#v", credential)
	}
}
