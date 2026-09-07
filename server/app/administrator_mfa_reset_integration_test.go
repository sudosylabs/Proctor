//go:build integration

// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"

	server "github.com/sudosylabs/proctor/server"
	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/testlib"
)

func TestOfflineAdministratorMFAResetReconcilesAndRequiresReenrollment(t *testing.T) {
	ctx := context.Background()
	persistence := openAuthenticationStore(t, requireAuthenticationDatabase(t))
	helper := testlib.Setup(t, testlib.WithStore(persistence), testlib.WithConfig(func(cfg *config.Config) {
		cfg.Server.ListenAddress = "127.0.0.1:0"
		cfg.Authentication.MFA.Enabled = true
		cfg.Authentication.MFA.EncryptionKey = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{29}, 32))
	}))
	const password = "offline MFA existing correct horse battery staple"
	bootstrap := performJSONRequest(helper.Handler(), http.MethodPost, "/api/v1/bootstrap", map[string]any{
		"bootstrap_secret": testlib.BootstrapSecret,
		"institution":      map[string]any{"name": "offline-mfa", "display_name": "Offline MFA"},
		"administrator":    map[string]any{"username": "offline-mfa-admin", "email": "offline-mfa-admin@example.edu"},
		"password":         password,
	}, "")
	if bootstrap.Code != http.StatusCreated {
		t.Fatalf("bootstrap=%d %s", bootstrap.Code, bootstrap.Body.String())
	}
	installation, err := persistence.Installation().Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	before, err := helper.App.Login(ctx, application.Invocation{}, application.LoginCommand{LoginID: "offline-mfa-admin", Password: password, ClientType: model.SessionClientCLI, DeviceID: "offline-old", Source: "127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	strengthenRoleAdministratorSession(t, helper.Handler(), before.Tokens.AccessToken)
	reset, err := helper.Server.ResetAdministratorMFA(ctx, server.AdministratorMFAResetCommand{InstitutionID: installation.InstitutionID.String(), UserID: installation.AdministratorUserID.String()})
	if err != nil || reset == nil || !reset.ReenrollmentRequired || reset.RecoveryGeneration != 1 {
		t.Fatalf("reset=%#v error=%v", reset, err)
	}
	events, err := persistence.Audit().List(ctx, store.AuditListOptions{Action: "authentication.administrator_mfa_reset", Limit: 10, Visibility: store.AuditVisibilityScope{InstitutionWide: true}})
	if err != nil || len(events) != 0 {
		t.Fatalf("pre-start recovery audit=%#v error=%v", events, err)
	}
	startIntegrationServer(t, helper)
	events, err = persistence.Audit().List(ctx, store.AuditListOptions{Action: "authentication.administrator_mfa_reset", Limit: 10, Visibility: store.AuditVisibilityScope{InstitutionWide: true}})
	if err != nil || len(events) != 1 || !events[0].ActorID.IsZero() || events[0].Status != model.AuditStatusSuccess {
		t.Fatalf("reconciled recovery audit=%#v error=%v", events, err)
	}
	old := performJSONRequest(helper.Handler(), http.MethodGet, "/api/v1/users/me", nil, before.Tokens.AccessToken)
	if old.Code == http.StatusOK {
		t.Fatal("old Session remained usable after offline reset")
	}
	_, err = helper.App.Login(ctx, application.Invocation{}, application.LoginCommand{LoginID: "offline-mfa-admin", Password: password, ClientType: model.SessionClientCLI, DeviceID: "offline-cli", Source: "127.0.0.1:2"})
	if err == nil {
		t.Fatal("CLI login bypassed mandatory reenrollment")
	}
	fresh, err := helper.App.Login(ctx, application.Invocation{}, application.LoginCommand{LoginID: "offline-mfa-admin", Password: password, ClientType: model.SessionClientWeb, DeviceID: "offline-web", Source: "127.0.0.1:3"})
	if err != nil || fresh == nil || fresh.Session == nil || !fresh.Session.MFARecoveryRequired || fresh.Session.AuthenticationGeneration != 1 {
		t.Fatalf("restricted fresh Session=%#v error=%v", fresh, err)
	}
	ordinary := performJSONRequest(helper.Handler(), http.MethodGet, "/api/v1/users/me", nil, fresh.Tokens.AccessToken)
	if ordinary.Code == http.StatusOK {
		t.Fatal("restricted browser Session accessed ordinary account endpoint")
	}
	status := performJSONRequest(helper.Handler(), http.MethodGet, "/api/v1/users/me/mfa", nil, fresh.Tokens.AccessToken)
	if status.Code != http.StatusOK {
		t.Fatalf("recovery status=%d %s", status.Code, status.Body.String())
	}
	var state struct {
		Required bool `json:"mfa_recovery_required"`
	}
	if err = json.Unmarshal(status.Body.Bytes(), &state); err != nil || !state.Required {
		t.Fatalf("recovery status=%s error=%v", status.Body.String(), err)
	}
	strengthenRoleAdministratorSession(t, helper.Handler(), fresh.Tokens.AccessToken)
	recovery, err := persistence.MFA().GetRecoveryState(ctx, installation.AdministratorUserID)
	if err != nil || recovery.ReenrollmentRequired || recovery.Generation != 1 {
		t.Fatalf("activated recovery=%#v error=%v", recovery, err)
	}
	ordinary = performJSONRequest(helper.Handler(), http.MethodGet, "/api/v1/users/me", nil, fresh.Tokens.AccessToken)
	if ordinary.Code != http.StatusOK {
		t.Fatalf("post-enrollment account=%d %s", ordinary.Code, ordinary.Body.String())
	}
}
