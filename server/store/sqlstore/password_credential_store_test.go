// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

func TestPasswordCredentialRowConversion(t *testing.T) {
	credential := &model.PasswordCredential{
		ID:        model.PasswordCredentialID(model.NewID()),
		CreatedAt: model.TimeFromMillis(1), UpdatedAt: model.TimeFromMillis(2),
		ArchivedAt: model.OptionalTimeFromMillis(3),
		UserID:     model.UserID(model.NewID()), PasswordHash: "$argon2id$test",
		PasswordChangedAt: model.TimeFromMillis(4), Revision: 3,
	}
	row := newPasswordCredentialRow(credential)
	got, err := row.model()
	require.NoError(t, err)
	require.Equal(t, *credential, *got, "row.model() = %#v, want %#v", got, credential)
}

func TestPasswordCredentialRowRehydrationRejectsInvalidPersistedState(t *testing.T) {
	t.Parallel()
	valid := passwordCredentialRow{
		ID: model.NewPasswordCredentialID().String(), CreatedAt: model.TimeFromMillis(1),
		UpdatedAt: model.TimeFromMillis(2), UserID: model.NewUserID().String(),
		PasswordHash: "$argon2id$test", PasswordChangedAt: model.TimeFromMillis(2), Revision: 1,
	}
	tests := []struct {
		name, field string
		mutate      func(*passwordCredentialRow)
	}{
		{name: "credential id", field: "id", mutate: func(row *passwordCredentialRow) { row.ID = "bad" }},
		{name: "user id", field: "user_id", mutate: func(row *passwordCredentialRow) { row.UserID = "bad" }},
		{name: "domain state", field: "password_changed_at", mutate: func(row *passwordCredentialRow) { row.PasswordChangedAt = model.TimeFromMillis(0) }},
		{name: "revision", field: "revision", mutate: func(row *passwordCredentialRow) { row.Revision = 0 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			row := valid
			test.mutate(&row)
			_, err := row.model()
			var persisted *persistedStateError
			require.ErrorAs(t, err, &persisted, "model() error")
			require.Equal(t, "password_credential", persisted.Entity, "model() error")
			require.Equal(t, test.field, persisted.Field, "model() error")
		})
	}
}
