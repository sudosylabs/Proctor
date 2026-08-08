// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

func TestPersonalAccessTokenRowConversion(t *testing.T) {
	unitID := model.NewId()
	createdAt := model.TimeFromMillis(1)
	row := personalAccessTokenRow{
		ID: model.NewId(), CreatedAt: createdAt, UpdatedAt: createdAt.Add(time.Millisecond), UserID: model.NewId(),
		Description: "token", TokenHash: model.HashToken(model.NewCredentialToken()),
		Scopes: []string{string(model.ActionClassView)}, AcademicUnitID: &unitID,
		ExpiresAt: createdAt.Add(2 * time.Millisecond),
	}
	token := row.model()
	if token.AcademicUnitID.String() != unitID || len(token.Scopes) != 1 {
		t.Fatalf("row.model() = %#v", token)
	}
	token.Scopes[0] = "mutated"
	if row.Scopes[0] != string(model.ActionClassView) {
		t.Fatal("row.model() exposed mutable scopes")
	}
}
