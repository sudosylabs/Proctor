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

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/testlib"
)

func TestUserSettingsUnsupportedFormatIsOpaqueAndReadOnlyAfterRollback(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	if dataSource == "" {
		t.Fatal("PROCTOR_TEST_DATABASE_URL is not set")
	}
	persistence := openAuthenticationStore(t, dataSource)
	seedInitialAuthenticationAccessPolicy(t, persistence)
	helper := testlib.Setup(t, testlib.WithStore(persistence))
	ctx := context.Background()
	if _, err := persistence.Institution().Save(ctx, &model.Institution{
		Name: "settings-compatibility-institution", DisplayName: "Settings Compatibility Institution",
	}); err != nil {
		t.Fatal(err)
	}
	const password = "correct horse battery staple"
	user, err := helper.App.CreateLocalUser(ctx, &model.User{
		Username: "settings-compatibility-user",
		Email:    "settings-compatibility-user@example.edu",
	}, password)
	if err != nil {
		t.Fatal(err)
	}
	login, err := helper.App.Login(ctx, application.Invocation{}, application.LoginCommand{
		LoginID: user.Username, Password: password, ClientType: model.SessionClientWeb,
		Source: "127.0.0.1:1",
	})
	if err != nil {
		t.Fatal(err)
	}
	principal, err := helper.App.AuthenticateAccess(ctx, login.Tokens.AccessToken)
	if err != nil {
		t.Fatal(err)
	}

	const source = "format-2(source => private.preference.must.remain.exact);\n"
	if _, err := persistence.GetMaster().Exec(ctx, `
		UPDATE user_settings_documents SET source = $1, format_version = 2 WHERE user_id = $2`,
		source, user.ID.String(),
	); err != nil {
		t.Fatal(err)
	}
	type persistedState struct {
		Source        string    `db:"source"`
		FormatVersion int       `db:"format_version"`
		Revision      string    `db:"revision"`
		UpdatedAt     time.Time `db:"updated_at"`
	}
	var before persistedState
	if err := persistence.GetMaster().Get(ctx, &before, `
		SELECT source, format_version, revision, updated_at
		FROM user_settings_documents WHERE user_id = $1`, user.ID.String()); err != nil {
		t.Fatal(err)
	}
	invocation := application.NewInvocation(*principal, model.RequestMetadata{RequestID: "settings-compatibility"})
	view, err := helper.App.ReadOwnUserSettings(ctx, invocation)
	if err != nil {
		t.Fatal(err)
	}
	if view.Source != source || view.FormatVersion != 2 || view.Writable ||
		view.Revision.String() != before.Revision || !view.UpdatedAt.Equal(model.TimeUTC(before.UpdatedAt)) {
		t.Fatalf("ReadOwnUserSettings() = %#v, persisted = %#v", view, before)
	}

	for name, targetVersion := range map[string]int{
		"replace unsupported current with version one": 1,
		"submit unsupported target version":            2,
	} {
		t.Run(name, func(t *testing.T) {
			_, replaceErr := helper.App.ReplaceOwnUserSettings(ctx, invocation, application.ReplaceOwnUserSettingsCommand{
				Source: "{}\n", FormatVersion: targetVersion, ExpectedRevision: view.Revision,
				IdempotencyKey: "settings-compatibility-" + string(rune('0'+targetVersion)),
			})
			if !application.Is(replaceErr, "user_settings.format_unsupported") {
				t.Fatalf("ReplaceOwnUserSettings() error = %v", replaceErr)
			}
		})
	}

	var after persistedState
	if err := persistence.GetMaster().Get(ctx, &after, `
		SELECT source, format_version, revision, updated_at
		FROM user_settings_documents WHERE user_id = $1`, user.ID.String()); err != nil {
		t.Fatal(err)
	}
	if after.Source != before.Source || after.FormatVersion != before.FormatVersion ||
		after.Revision != before.Revision || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("rejected replacements changed persisted state: before=%#v after=%#v", before, after)
	}
	audits, err := persistence.Audit().List(ctx, store.AuditListOptions{
		Action: "user.settings.replace", Limit: 10,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) != 0 {
		t.Fatalf("rejected replacements wrote audits: %#v", audits)
	}

	// A second read proves that the first read did not attempt a hidden repair.
	again, err := helper.App.ReadOwnUserSettings(ctx, invocation)
	if err != nil {
		t.Fatal(err)
	}
	if again.Source != source || again.Writable || again.Revision != view.Revision {
		t.Fatalf("second ReadOwnUserSettings() = %#v", again)
	}
}
