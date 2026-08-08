// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestExternalLoginStateRowConversion(t *testing.T) {
	state := &model.ExternalLoginState{
		ID: model.ExternalLoginStateID(model.NewId()),
		CreatedAt: model.TimeFromMillis(1), UpdatedAt: model.TimeFromMillis(2),
		Provider:    "campus-cas",
		StateHash:   model.HashToken(model.NewCredentialToken()),
		BindingHash: model.HashToken(model.NewCredentialToken()),
		ReturnTo:    "/exams", ClientType: model.SessionClientDesktop,
		DeviceID: "device", DeviceName: "Desktop",
		ExpiresAt: model.TimeFromMillis(3), ConsumedAt: model.OptionalTimeFromMillis(2),
	}
	row := newExternalLoginStateRow(state)
	if got := row.model(); *got != *state {
		t.Fatalf("row.model() = %#v, want %#v", got, state)
	}
}
