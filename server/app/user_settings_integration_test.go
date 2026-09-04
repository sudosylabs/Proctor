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

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/testlib"
)

func TestUserSettingsReadOwnIntegrationPreservesExactSource(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	if dataSource == "" {
		t.Fatal("PROCTOR_TEST_DATABASE_URL is not set")
	}
	persistence := openAuthenticationStore(t, dataSource)
	seedInitialAuthenticationAccessPolicy(t, persistence)
	helper := testlib.Setup(t, testlib.WithStore(persistence))
	ctx := context.Background()
	if _, err := persistence.Institution().Save(ctx, &model.Institution{
		Name: "settings-institution", DisplayName: "Settings Institution",
	}); err != nil {
		t.Fatal(err)
	}
	const password = "correct horse battery staple"
	user, err := helper.App.CreateLocalUser(ctx, &model.User{
		Username: "settings-read-user",
		Email:    "settings-read-user@example.edu",
	}, password)
	if err != nil {
		t.Fatal(err)
	}
	const source = "{\n  // keep comments and spacing\n  \"editor.fontFamily\": \"A\\\\B\",\n  \"future.unknown\": {\"nested\": true,},\n}\n"
	if _, err := persistence.GetMaster().Exec(
		ctx,
		"UPDATE user_settings_documents SET source = $1 WHERE user_id = $2",
		source,
		user.ID.String(),
	); err != nil {
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
	view, err := helper.App.ReadOwnUserSettings(
		ctx,
		application.NewInvocation(*principal, model.RequestMetadata{RequestID: "settings-read-integration"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if view.Source != source || view.FormatVersion != model.UserSettingsFormatVersion1 || !view.Writable {
		t.Fatalf("ReadOwnUserSettings() = %#v", view)
	}

	replacementSource := "{\n  // byte changes create revisions\n  \"editor.fontFamily\": \"A\\\\B\",\n  \"future.unknown\": {\"nested\": false,},\n}\n"
	command := application.ReplaceOwnUserSettingsCommand{
		Source: replacementSource, FormatVersion: model.UserSettingsFormatVersion1,
		ExpectedRevision: view.Revision, IdempotencyKey: "settings-integration-save",
	}
	replaced, err := helper.App.ReplaceOwnUserSettings(
		ctx,
		application.NewInvocation(*principal, model.RequestMetadata{RequestID: "settings-replace-integration"}),
		command,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !replaced.Changed || replaced.Revision == view.Revision {
		t.Fatalf("ReplaceOwnUserSettings() = %#v", replaced)
	}
	replay, err := helper.App.ReplaceOwnUserSettings(
		ctx,
		application.NewInvocation(*principal, model.RequestMetadata{RequestID: "settings-replay-integration"}),
		command,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !replay.Replayed || replay.Revision != replaced.Revision || !replay.UpdatedAt.Equal(replaced.UpdatedAt) {
		t.Fatalf("replacement replay = %#v, want %#v", replay, replaced)
	}
	noOp, err := helper.App.ReplaceOwnUserSettings(
		ctx,
		application.NewInvocation(*principal, model.RequestMetadata{RequestID: "settings-noop-integration"}),
		application.ReplaceOwnUserSettingsCommand{
			Source: replacementSource, FormatVersion: model.UserSettingsFormatVersion1,
			ExpectedRevision: replaced.Revision, IdempotencyKey: "settings-integration-noop",
		},
	)
	if err != nil || noOp.Changed || noOp.Revision != replaced.Revision || !noOp.UpdatedAt.Equal(replaced.UpdatedAt) {
		t.Fatalf("replacement no-op = %#v, %v", noOp, err)
	}
	_, err = helper.App.ReplaceOwnUserSettings(
		ctx,
		application.NewInvocation(*principal, model.RequestMetadata{RequestID: "settings-conflict-integration"}),
		application.ReplaceOwnUserSettingsCommand{
			Source: "{\"editor.fontSize\": 19}\n", FormatVersion: model.UserSettingsFormatVersion1,
			ExpectedRevision: view.Revision, IdempotencyKey: "settings-integration-conflict",
		},
	)
	if !application.Is(err, "user_settings.revision_conflict") {
		t.Fatalf("stale replacement error = %v", err)
	}
	after, err := helper.App.ReadOwnUserSettings(
		ctx,
		application.NewInvocation(*principal, model.RequestMetadata{RequestID: "settings-after-integration"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if after.Source != replacementSource || after.Revision != replaced.Revision {
		t.Fatalf("ReadOwnUserSettings(after replacement) = %#v", after)
	}
	audits, err := persistence.Audit().List(ctx, store.AuditListOptions{
		Action: "user.settings.replace", Limit: 10,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) != 1 || audits[0].Status != model.AuditStatusSuccess ||
		bytesContain(audits[0].Parameters, []byte("fontFamily")) ||
		bytesContain(audits[0].Parameters, []byte("future.unknown")) {
		t.Fatalf("settings replacement audits = %#v", audits)
	}
	var safeParameters map[string]any
	if err := json.Unmarshal(audits[0].Parameters, &safeParameters); err != nil {
		t.Fatal(err)
	}
	if safeParameters["source_bytes"] != float64(len(replacementSource)) ||
		safeParameters["previous_revision"] != view.Revision.String() ||
		safeParameters["resulting_revision"] != replaced.Revision.String() {
		t.Fatalf("settings audit parameters = %#v", safeParameters)
	}
}

func bytesContain(value, fragment []byte) bool {
	for index := 0; index+len(fragment) <= len(value); index++ {
		if string(value[index:index+len(fragment)]) == string(fragment) {
			return true
		}
	}
	return false
}
