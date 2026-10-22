//go:build integration

// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/testlib"
)

func TestUserSettingsUnsupportedFormatIsOpaqueAndReadOnlyAfterRollback(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	require.NotEmpty(t, dataSource, "PROCTOR_TEST_DATABASE_URL is not set")
	persistence := openAuthenticationStore(t, dataSource)
	seedInitialAuthenticationAccessPolicy(t, persistence)
	helper := testlib.Setup(t, testlib.WithStore(persistence))
	ctx := context.Background()
	_, err := persistence.Institution().Save(ctx, &model.Institution{
		Name: "settings-compatibility-institution", DisplayName: "Settings Compatibility Institution",
	})
	require.NoError(t, err)
	const password = "correct horse battery staple"
	user, err := helper.App.CreateLocalUser(ctx, app.CreateLocalUserCommand{User: &model.User{
		Username: "settings-compatibility-user",
		Email:    "settings-compatibility-user@example.edu",
	}, Password: password})
	require.NoError(t, err)
	login, err := helper.App.Login(ctx, invocation.Invocation{}, app.LoginCommand{
		LoginID: user.Username, Password: password, ClientType: model.SessionClientWeb,
		Source: "127.0.0.1:1",
	})
	require.NoError(t, err)
	principal, err := helper.App.AuthenticateAccess(ctx, login.Tokens.AccessToken)
	require.NoError(t, err)

	const source = "format-2(source => private.preference.must.remain.exact);\n"
	_, err = persistence.GetMaster().Exec(ctx, `
		UPDATE user_settings_documents SET source = $1, format_version = 2 WHERE user_id = $2`,
		source, user.ID.String(),
	)
	require.NoError(t, err)
	type persistedState struct {
		Source        string    `db:"source"`
		FormatVersion int       `db:"format_version"`
		Revision      string    `db:"revision"`
		UpdatedAt     time.Time `db:"updated_at"`
	}
	var before persistedState
	require.NoError(t, persistence.GetMaster().Get(ctx, &before, `
		SELECT source, format_version, revision, updated_at
		FROM user_settings_documents WHERE user_id = $1`, user.ID.String()))
	invocation := invocation.New(*principal, model.RequestMetadata{RequestID: "settings-compatibility"})
	view, err := helper.App.ReadOwnUserSettings(ctx, invocation)
	require.NoError(t, err)
	require.Equal(t, source, view.Source, "ReadOwnUserSettings() = %#v, persisted = %#v", view, before)
	require.Equal(t, 2, view.FormatVersion, "ReadOwnUserSettings() = %#v, persisted = %#v", view, before)
	require.False(t, view.Writable, "ReadOwnUserSettings() = %#v, persisted = %#v", view, before)
	require.Equal(t, before.Revision, view.Revision.String(), "ReadOwnUserSettings() = %#v, persisted = %#v", view, before)
	require.True(t, view.UpdatedAt.Equal(model.TimeUTC(before.UpdatedAt)), "ReadOwnUserSettings() = %#v, persisted = %#v", view, before)

	for name, targetVersion := range map[string]int{
		"replace unsupported current with version one": 1,
		"submit unsupported target version":            2,
	} {
		t.Run(name, func(t *testing.T) {
			_, replaceErr := helper.App.ReplaceOwnUserSettings(ctx, invocation, app.ReplaceOwnUserSettingsCommand{
				Source: "{}\n", FormatVersion: targetVersion, ExpectedRevision: view.Revision,
				IdempotencyKey: "settings-compatibility-" + string(rune('0'+targetVersion)),
			})
			require.True(t, apperror.Is(replaceErr, "user_settings.format_unsupported"), "ReplaceOwnUserSettings() error = %v", replaceErr)
		})
	}

	var after persistedState
	require.NoError(t, persistence.GetMaster().Get(ctx, &after, `
		SELECT source, format_version, revision, updated_at
		FROM user_settings_documents WHERE user_id = $1`, user.ID.String()))
	require.Equal(t, before.Source, after.Source, "rejected replacements changed persisted state: before=%#v after=%#v", before, after)
	require.Equal(t, before.FormatVersion, after.FormatVersion, "rejected replacements changed persisted state: before=%#v after=%#v", before, after)
	require.Equal(t, before.Revision, after.Revision, "rejected replacements changed persisted state: before=%#v after=%#v", before, after)
	require.True(t, after.UpdatedAt.Equal(before.UpdatedAt), "rejected replacements changed persisted state: before=%#v after=%#v", before, after)
	audits, err := persistence.Audit().List(ctx, store.AuditListOptions{
		Action: "user.settings.replace", Limit: 10,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	require.NoError(t, err)
	require.Empty(t, audits)

	// A second read proves that the first read did not attempt a hidden repair.
	again, err := helper.App.ReadOwnUserSettings(ctx, invocation)
	require.NoError(t, err)
	require.Equal(t, source, again.Source)
	require.False(t, again.Writable)
	require.Equal(t, view.Revision, again.Revision)
}
