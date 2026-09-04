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
	"testing"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/testlib"
)

func TestAccountAndAdministrativeSessionNoticesUseRealServerGraph(t *testing.T) {
	dataSource := requireAuthenticationDatabase(t)
	persistence := openAuthenticationStore(t, dataSource)
	helper := testlib.Setup(t, testlib.WithStore(persistence), testlib.WithConfig(func(cfg *config.Config) {
		cfg.Server.ListenAddress = "127.0.0.1:0"
	}))
	ctx := context.Background()
	const password = "correct horse battery staple"
	bootstrap, appErr := helper.App.BootstrapInstallation(ctx, application.Invocation{}, application.BootstrapInstallationCommand{
		InstitutionName: "security-notice-university", InstitutionDisplayName: "Security Notice University",
		AdministratorUsername: "mail11-admin", AdministratorEmail: "mail11-admin@example.edu", AdministratorDisplayName: "Mail 11 Admin",
		AdministratorLocale: "en", AdministratorTimezone: "UTC", Password: password,
		BootstrapSecret: testlib.BootstrapSecret, Source: "203.0.113.42",
	})
	if appErr != nil {
		t.Fatal(appErr)
	}
	target, appErr := helper.App.CreateLocalUser(ctx, &model.User{
		Username: "mail11-target", Email: "mail11-target@example.edu", DisplayName: "Mail 11 Target", Locale: "en", Timezone: "UTC",
	}, password)
	if appErr != nil {
		t.Fatal(appErr)
	}
	adminLogin, appErr := helper.App.Login(ctx, application.Invocation{}, application.LoginCommand{
		LoginID: bootstrap.Administrator.Username, Password: password, ClientType: model.SessionClientCLI,
		Source: "203.0.113.42", DeviceName: "mail11-admin-device",
	})
	if appErr != nil {
		t.Fatal(appErr)
	}
	adminPrincipal, appErr := helper.App.AuthenticateAccess(ctx, adminLogin.Tokens.AccessToken)
	if appErr != nil {
		t.Fatal(appErr)
	}
	adminInvocation := application.NewInvocation(*adminPrincipal, model.RequestMetadata{RequestID: "mail11-admin", IPAddress: "203.0.113.42"})

	initialTargetLogin := loginIntegrationUser(t, helper.Handler(), target.Username, password, model.SessionClientCLI, "mail11-initial")
	if _, appErr = helper.App.SetUserEnabled(ctx, adminInvocation, application.SetUserEnabledCommand{ID: target.ID.String(), Enabled: false}); appErr != nil {
		t.Fatal(appErr)
	}
	if _, appErr = helper.App.SetUserEnabled(ctx, adminInvocation, application.SetUserEnabledCommand{ID: target.ID.String(), Enabled: true}); appErr != nil {
		t.Fatal(appErr)
	}
	first := loginIntegrationUser(t, helper.Handler(), target.Username, password, model.SessionClientCLI, "mail11-first")
	second := loginIntegrationUser(t, helper.Handler(), target.Username, password, model.SessionClientCLI, "mail11-second")
	if err := helper.App.RevokeUserSession(ctx, adminInvocation, application.RevokeUserSessionCommand{UserID: target.ID.String(), SessionID: first.Session.ID.String()}); err != nil {
		t.Fatal(err)
	}
	if err := helper.App.RevokeUserSessions(ctx, adminInvocation, application.RevokeUserSessionsCommand{UserID: target.ID.String()}); err != nil {
		t.Fatal(err)
	}
	self := loginIntegrationUser(t, helper.Handler(), target.Username, password, model.SessionClientCLI, "mail11-self")
	selfPrincipal, appErr := helper.App.AuthenticateAccess(ctx, self.Tokens.AccessToken)
	if appErr != nil {
		t.Fatal(appErr)
	}
	selfInvocation := application.NewInvocation(*selfPrincipal, model.RequestMetadata{RequestID: "mail11-self", IPAddress: "198.51.100.9"})
	if err := helper.App.RevokeSession(ctx, selfInvocation, application.RevokeSessionCommand{SessionID: self.Session.ID.String()}); err != nil {
		t.Fatal(err)
	}
	logout := loginIntegrationUser(t, helper.Handler(), target.Username, password, model.SessionClientCLI, "mail11-logout")
	logoutPrincipal, appErr := helper.App.AuthenticateAccess(ctx, logout.Tokens.AccessToken)
	if appErr != nil {
		t.Fatal(appErr)
	}
	if err := helper.App.Logout(ctx, application.NewInvocation(*logoutPrincipal, model.RequestMetadata{}), application.LogoutCommand{}); err != nil {
		t.Fatal(err)
	}

	durable, err := persistence.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{TemplateKeys: []model.MailTemplateKey{
		model.MailTemplateIdentityAccountDisabled, model.MailTemplateIdentityAccountEnabled, model.MailTemplateIdentitySessionsRevokedByAdmin,
	}, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	counts := map[model.MailTemplateKey]int{}
	for _, delivery := range durable {
		counts[delivery.TemplateKey]++
	}
	if len(durable) != 4 || counts[model.MailTemplateIdentityAccountDisabled] != 1 || counts[model.MailTemplateIdentityAccountEnabled] != 1 ||
		counts[model.MailTemplateIdentitySessionsRevokedByAdmin] != 2 {
		t.Fatalf("durable security notices = %#v", durable)
	}
	if initialTargetLogin.Session == nil || second.Session == nil {
		t.Fatal("integration session fixture was not established")
	}

	startIntegrationServer(t, helper)
	deliveries := waitForRecoveryDeliveries(t, helper, 4)
	if len(deliveries) != 4 {
		t.Fatalf("captured deliveries = %d, want 4", len(deliveries))
	}
	for _, delivery := range deliveries {
		if len(delivery.Recipients) != 1 || delivery.Recipients[0] != target.Email {
			t.Fatalf("security notice recipient = %#v", delivery.Recipients)
		}
		for _, forbidden := range [][]byte{
			[]byte("mail11-admin"), []byte("account disabled by administrator"), []byte("session revoked by administrator"),
			[]byte("203.0.113.42"), []byte(adminLogin.Tokens.AccessToken),
		} {
			if bytes.Contains(delivery.Data, forbidden) {
				t.Fatalf("security notice exposed private value %q", forbidden)
			}
		}
	}
}
