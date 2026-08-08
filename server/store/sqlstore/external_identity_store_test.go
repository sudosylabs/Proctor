// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestExternalIdentityRowConversion(t *testing.T) {
	identity := &model.ExternalIdentity{
		ID:        model.ExternalIdentityID(model.NewId()),
		CreatedAt: model.TimeFromMillis(1), UpdatedAt: model.TimeFromMillis(2),
		ArchivedAt: model.OptionalTimeFromMillis(3),
		UserID:     model.UserID(model.NewId()), Provider: "campus-cas",
		Subject: "opaque-subject", LastSeenAt: model.OptionalTimeFromMillis(4),
	}
	row := newExternalIdentityRow(identity)
	if got := row.model(); *got != *identity {
		t.Fatalf("row.model() = %#v, want %#v", got, identity)
	}
}
