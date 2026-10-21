// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

func TestExternalLoginStateRowConversion(t *testing.T) {
	state := &model.ExternalLoginState{
		ID:        model.ExternalLoginStateID(model.NewID()),
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
	require.NoError(t, err)
	require.Equal(t, *state, *got, "row.model() = %#v, want %#v", got, state)
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
			require.ErrorAs(t, err, &persisted, "model() error")
			require.Equal(t, "external_login_state", persisted.Entity, "model() error")
			require.Equal(t, test.field, persisted.Field, "model() error")
			if test.name == "domain state" {
				require.Error(t, errors.Unwrap(err), "persisted-state error discarded the validation cause")
			}
			require.NotContains(t, err.Error(), "secret-value-must-not-appear")
		})
	}
}
