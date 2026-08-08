// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestUserTokenRowConversion(t *testing.T) {
	token := &model.UserToken{
		ID: model.UserTokenID(model.NewId()),
		CreatedAt: model.TimeFromMillis(1), UpdatedAt: model.TimeFromMillis(2),
		ArchivedAt: model.OptionalTimeFromMillis(3),
		UserID: model.UserID(model.NewId()), Purpose: model.UserTokenPasswordReset,
		TokenHash: model.HashToken(model.NewCredentialToken()),
		Target:    "student@example.edu",
		ExpiresAt:  model.TimeFromMillis(4),
		ConsumedAt: model.OptionalTimeFromMillis(3),
	}
	row := newUserTokenRow(token)
	if got := row.model(); *got != *token {
		t.Fatalf("row.model() = %#v, want %#v", got, token)
	}
}
