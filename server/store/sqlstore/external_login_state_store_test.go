// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"errors"
	"strings"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestExternalLoginStateRowConversion(t *testing.T) {
	state := &model.ExternalLoginState{
		ID:        model.ExternalLoginStateID(model.NewId()),
		CreatedAt: model.TimeFromMillis(1), UpdatedAt: model.TimeFromMillis(2),
		Provider: "campus-cas", Purpose: model.ExternalAuthenticationPurposeLogin,
		StateHash:   model.HashToken(model.NewCredentialToken()),
		BindingHash: model.HashToken(model.NewCredentialToken()),
		ReturnTo:    "/exams", ClientType: model.SessionClientDesktop,
		DeviceID: "device", DeviceName: "Desktop",
		ExpiresAt: model.TimeFromMillis(3), ConsumedAt: model.OptionalTimeFromMillis(2),
	}
	row := newExternalLoginStateRow(state)
	got, err := row.model()
	if err != nil {
		t.Fatal(err)
	}
	if *got != *state {
		t.Fatalf("row.model() = %#v, want %#v", got, state)
	}
}

func TestExternalLoginStateRowRehydrationRejectsInvalidPersistedState(t *testing.T) {
	t.Parallel()
	valid := externalLoginStateRow{
		ID: model.NewExternalLoginStateID().String(), CreatedAt: model.TimeFromMillis(1),
		UpdatedAt: model.TimeFromMillis(1), Provider: "campus-cas", Purpose: string(model.ExternalAuthenticationPurposeLogin),
		StateHash:   model.HashToken(model.NewCredentialToken()),
		BindingHash: model.HashToken(model.NewCredentialToken()), ReturnTo: "/exams",
		ClientType: string(model.SessionClientWeb), ExpiresAt: model.TimeFromMillis(3),
	}
	tests := []struct {
		name, field string
		mutate      func(*externalLoginStateRow)
	}{
		{name: "state id", field: "id", mutate: func(row *externalLoginStateRow) { row.ID = "bad" }},
		{name: "domain state", field: "token_hash", mutate: func(row *externalLoginStateRow) { row.StateHash = "secret-value-must-not-appear" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			row := valid
			test.mutate(&row)
			_, err := row.model()
			var persisted *persistedStateError
			if !errors.As(err, &persisted) || persisted.Entity != "external_login_state" || persisted.Field != test.field {
				t.Fatalf("model() error = %v, want external_login_state.%s persisted-state error", err, test.field)
			}
			if test.name == "domain state" && errors.Unwrap(err) == nil {
				t.Fatal("persisted-state error discarded the validation cause")
			}
			if strings.Contains(err.Error(), "secret-value-must-not-appear") {
				t.Fatalf("persisted-state error exposed credential material: %v", err)
			}
		})
	}
}
