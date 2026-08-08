// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestPasswordCredentialRowConversion(t *testing.T) {
	credential := &model.PasswordCredential{
		ID:        model.PasswordCredentialID(model.NewId()),
		CreatedAt: model.TimeFromMillis(1), UpdatedAt: model.TimeFromMillis(2),
		ArchivedAt: model.OptionalTimeFromMillis(3),
		UserID:     model.UserID(model.NewId()), PasswordHash: "$argon2id$test",
		PasswordChangedAt: model.TimeFromMillis(4),
	}
	row := newPasswordCredentialRow(credential)
	if got := row.model(); *got != *credential {
		t.Fatalf("row.model() = %#v, want %#v", got, credential)
	}
}
