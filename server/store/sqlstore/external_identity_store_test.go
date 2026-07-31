// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestExternalIdentityRowConversion(t *testing.T) {
	identity := &model.ExternalIdentity{
		Id: model.NewId(), CreateAt: 1, UpdateAt: 2, DeleteAt: 3,
		UserId: model.NewId(), Provider: "campus-cas",
		Subject: "opaque-subject", LastSeenAt: 4,
	}
	row := newExternalIdentityRow(identity)
	if got := row.model(); *got != *identity {
		t.Fatalf("row.model() = %#v, want %#v", got, identity)
	}
}
