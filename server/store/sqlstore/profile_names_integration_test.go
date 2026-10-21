//go:build integration

// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"context"
	"strings"
	"testing"

	"github.com/lib/pq"
	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

// TestProfileNameConstraintsRejectControlCharacters proves the baseline holds
// the same profile-name rule as model.User.Validate, so no persisted name can
// later fail mail preparation for its recipient.
func TestProfileNameConstraintsRejectControlCharacters(t *testing.T) {
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	ctx := context.Background()
	userID := model.NewUserID()
	_, err := persistence.GetMaster().Exec(ctx, `INSERT INTO users (id,created_at,updated_at,username,email,display_name,locale,timezone,default_profile_picture_seed)
		VALUES (?,clock_timestamp(),clock_timestamp(),'names.user','names.user@example.edu','Names User','en','UTC',?)`,
		userID.String(), strings.Repeat("a", model.ProfilePictureSeedLength))
	require.NoError(t, err)
	for _, value := range []string{"Names\nUser", "Names\rUser", "Names\tUser", "Names\x7fUser", "Names\u0085User"} {
		for _, column := range []string{"display_name", "first_name", "last_name"} {
			_, err = persistence.GetMaster().Exec(ctx, `UPDATE users SET `+column+`=? WHERE id=?`, value, userID.String())
			var postgresErr *pq.Error
			require.ErrorAs(t, err, &postgresErr, "%s=%q", column, value)
			require.Equal(t, "23514", string(postgresErr.Code), "%s=%q", column, value)
			require.Equal(t, "users_profile_names_check", postgresErr.Constraint, "%s=%q", column, value)
		}
	}
	_, err = persistence.GetMaster().Exec(ctx, `UPDATE users SET display_name=? WHERE id=?`, "\u0645\u06cc\u200c\u0631\u0648\u0645", userID.String())
	require.NoError(t, err, "a zero-width non-joiner is a legal format character")
}
