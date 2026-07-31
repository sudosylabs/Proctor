// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"testing"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store/storetest"
)

func TestUserTokenStore(t *testing.T) {
	StoreTest(t, storetest.TestUserTokenStore)
}

func TestUserTokenRowConversion(t *testing.T) {
	token := &model.UserToken{
		Id: model.NewId(), CreateAt: 1, UpdateAt: 2, DeleteAt: 3,
		UserId: model.NewId(), Purpose: model.UserTokenPasswordReset,
		TokenHash: model.HashToken(model.NewCredentialToken()),
		Target:    "student@example.edu", ExpiresAt: 4, ConsumedAt: 3,
	}
	row := newUserTokenRow(token)
	if got := row.model(); *got != *token {
		t.Fatalf("row.model() = %#v, want %#v", got, token)
	}
}
