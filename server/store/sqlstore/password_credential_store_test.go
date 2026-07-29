// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"testing"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store/storetest"
)

func TestPasswordCredentialStore(t *testing.T) {
	StoreTest(t, storetest.TestPasswordCredentialStore)
}

func TestPasswordCredentialRowConversion(t *testing.T) {
	credential := &model.PasswordCredential{
		Id: model.NewId(), CreateAt: 1, UpdateAt: 2, DeleteAt: 3,
		UserId: model.NewId(), PasswordHash: "$argon2id$test", PasswordChangedAt: 4,
	}
	row := newPasswordCredentialRow(credential)
	if got := row.model(); *got != *credential {
		t.Fatalf("row.model() = %#v, want %#v", got, credential)
	}
}
