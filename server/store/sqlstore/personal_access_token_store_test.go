// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"testing"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store/storetest"
)

func TestPersonalAccessTokenStore(t *testing.T) {
	StoreTest(t, storetest.TestPersonalAccessTokenStore)
}

func TestPersonalAccessTokenRowConversion(t *testing.T) {
	unitID := model.NewId()
	row := personalAccessTokenRow{
		ID: model.NewId(), CreateAt: 1, UpdateAt: 2, UserID: model.NewId(),
		Description: "token", TokenHash: model.HashToken(model.NewCredentialToken()),
		Scopes: []string{string(model.ActionClassView)}, AcademicUnitID: &unitID,
		ExpiresAt: 3,
	}
	token := row.model()
	if token.AcademicUnitId != unitID || len(token.Scopes) != 1 {
		t.Fatalf("row.model() = %#v", token)
	}
	token.Scopes[0] = "mutated"
	if row.Scopes[0] != string(model.ActionClassView) {
		t.Fatal("row.model() exposed mutable scopes")
	}
}
