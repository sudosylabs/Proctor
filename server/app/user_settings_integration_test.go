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
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/testlib"
)

func TestUserSettingsReadOwnIntegrationPreservesExactSource(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	require.NotEmpty(t, dataSource, "PROCTOR_TEST_DATABASE_URL is not set")
	persistence := openAuthenticationStore(t, dataSource)
	seedInitialAuthenticationAccessPolicy(t, persistence)
	helper := testlib.Setup(t, testlib.WithStore(persistence))
	ctx := context.Background()
	_, err := persistence.Institution().Save(ctx, &model.Institution{
		Name: "settings-institution", DisplayName: "Settings Institution",
	})
	require.NoError(t, err)
	const password = "correct horse battery staple"
	user, err := helper.App.CreateLocalUser(ctx, app.CreateLocalUserCommand{User: &model.User{
		Username: "settings-read-user",
		Email:    "settings-read-user@example.edu",
	}, Password: password})
	require.NoError(t, err)
	const source = "{\n  // keep comments and spacing\n  \"editor.fontFamily\": \"A\\\\B\",\n  \"future.unknown\": {\"nested\": true,},\n}\n"
	_, err = persistence.GetMaster().Exec(
		ctx,
		"UPDATE user_settings_documents SET source = $1 WHERE user_id = $2",
		source,
		user.ID.String(),
	)
	require.NoError(t, err)
	login, err := helper.App.Login(ctx, app.Invocation{}, app.LoginCommand{
		LoginID: user.Username, Password: password, ClientType: model.SessionClientWeb,
		Source: "127.0.0.1:1",
	})
	require.NoError(t, err)
	principal, err := helper.App.AuthenticateAccess(ctx, login.Tokens.AccessToken)
	require.NoError(t, err)
	view, err := helper.App.ReadOwnUserSettings(
		ctx,
		app.NewInvocation(*principal, model.RequestMetadata{RequestID: "settings-read-integration"}),
	)
	require.NoError(t, err)
	require.Equal(t, source, view.Source)
	require.Equal(t, model.UserSettingsFormatVersion1, view.FormatVersion)
	require.True(t, view.Writable)

	replacementSource := "{\n  // byte changes create revisions\n  \"editor.fontFamily\": \"A\\\\B\",\n  \"future.unknown\": {\"nested\": false,},\n}\n"
	command := app.ReplaceOwnUserSettingsCommand{
		Source: replacementSource, FormatVersion: model.UserSettingsFormatVersion1,
		ExpectedRevision: view.Revision, IdempotencyKey: "settings-integration-save",
	}
	replaced, err := helper.App.ReplaceOwnUserSettings(
		ctx,
		app.NewInvocation(*principal, model.RequestMetadata{RequestID: "settings-replace-integration"}),
		command,
	)
	require.NoError(t, err)
	require.True(t, replaced.Changed)
	require.NotEqual(t, view.Revision, replaced.Revision)
	replay, err := helper.App.ReplaceOwnUserSettings(
		ctx,
		app.NewInvocation(*principal, model.RequestMetadata{RequestID: "settings-replay-integration"}),
		command,
	)
	require.NoError(t, err)
	require.True(t, replay.Replayed, "replacement replay = %#v, want %#v", replay, replaced)
	require.Equal(t, replaced.Revision, replay.Revision, "replacement replay = %#v, want %#v", replay, replaced)
	require.True(t, replay.UpdatedAt.Equal(replaced.UpdatedAt), "replacement replay = %#v, want %#v", replay, replaced)
	noOp, err := helper.App.ReplaceOwnUserSettings(
		ctx,
		app.NewInvocation(*principal, model.RequestMetadata{RequestID: "settings-noop-integration"}),
		app.ReplaceOwnUserSettingsCommand{
			Source: replacementSource, FormatVersion: model.UserSettingsFormatVersion1,
			ExpectedRevision: replaced.Revision, IdempotencyKey: "settings-integration-noop",
		},
	)
	require.NoError(t, err, "replacement no-op = %#v", noOp)
	require.False(t, noOp.Changed, "replacement no-op = %#v", noOp)
	require.Equal(t, replaced.Revision, noOp.Revision, "replacement no-op = %#v", noOp)
	require.True(t, noOp.UpdatedAt.Equal(replaced.UpdatedAt), "replacement no-op = %#v", noOp)
	_, err = helper.App.ReplaceOwnUserSettings(
		ctx,
		app.NewInvocation(*principal, model.RequestMetadata{RequestID: "settings-conflict-integration"}),
		app.ReplaceOwnUserSettingsCommand{
			Source: "{\"editor.fontSize\": 19}\n", FormatVersion: model.UserSettingsFormatVersion1,
			ExpectedRevision: view.Revision, IdempotencyKey: "settings-integration-conflict",
		},
	)
	require.True(t, apperror.Is(err, "user_settings.revision_conflict"), "stale replacement error = %v", err)
	after, err := helper.App.ReadOwnUserSettings(
		ctx,
		app.NewInvocation(*principal, model.RequestMetadata{RequestID: "settings-after-integration"}),
	)
	require.NoError(t, err)
	require.Equal(t, replacementSource, after.Source)
	require.Equal(t, replaced.Revision, after.Revision)
	audits, err := persistence.Audit().List(ctx, store.AuditListOptions{
		Action: "user.settings.replace", Limit: 10,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	require.NoError(t, err)
	require.Len(t, audits, 1)
	require.Equal(t, model.AuditStatusSuccess, audits[0].Status)
	require.False(t, bytesContain(audits[0].Parameters, []byte("fontFamily")))
	require.False(t, bytesContain(audits[0].Parameters, []byte("future.unknown")))
	var safeParameters map[string]any
	require.NoError(t, json.Unmarshal(audits[0].Parameters, &safeParameters))
	require.Equal(t, float64(len(replacementSource)), safeParameters["source_bytes"])
	require.Equal(t, view.Revision.String(), safeParameters["previous_revision"])
	require.Equal(t, replaced.Revision.String(), safeParameters["resulting_revision"])
}

func bytesContain(value, fragment []byte) bool {
	for index := 0; index+len(fragment) <= len(value); index++ {
		if string(value[index:index+len(fragment)]) == string(fragment) {
			return true
		}
	}
	return false
}
