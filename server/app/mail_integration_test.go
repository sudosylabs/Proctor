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
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/testlib"
)

func TestControlledMailUsesRealApplicationGraphAndDurableWorker(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	if dataSource == "" {
		t.Fatal("PROCTOR_TEST_DATABASE_URL is not set")
	}
	persistence := openAuthenticationStore(t, dataSource)
	helper := testlib.Setup(t,
		testlib.WithStore(persistence),
		testlib.WithConfig(func(cfg *config.Config) {
			cfg.Server.ListenAddress = "127.0.0.1:0"
		}),
	)
	ctx := context.Background()
	const password = "correct horse battery staple"
	bootstrap, appErr := helper.App.BootstrapInstallation(ctx, application.Invocation{}, application.BootstrapInstallationCommand{
		InstitutionName: "mail-university", InstitutionDisplayName: "Mail University",
		AdministratorUsername: "mail-operator", AdministratorEmail: "mail-operator@example.edu",
		AdministratorDisplayName: "Mail Operator", AdministratorLocale: "en", AdministratorTimezone: "UTC",
		Password: password, BootstrapSecret: testlib.BootstrapSecret, Source: "127.0.0.1:1",
	})
	if appErr != nil {
		t.Fatal(appErr)
	}
	operator := bootstrap.Administrator
	// This fixture needs a verified mail operator. Production mailbox state is
	// changed only through the named UserToken aggregates.
	_, err := persistence.GetMaster().Exec(ctx, `UPDATE users SET email_verified = TRUE WHERE id = ?`, operator.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	operator, err = persistence.User().Get(ctx, operator.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	login, appErr := helper.App.Login(ctx, application.Invocation{}, application.LoginCommand{
		LoginID: operator.Username, Password: password, ClientType: model.SessionClientWeb,
		Source: "127.0.0.1:1", DeviceName: "mail-integration",
	})
	if appErr != nil {
		t.Fatal(appErr)
	}
	principal, appErr := helper.App.AuthenticateAccess(ctx, login.Tokens.AccessToken)
	if appErr != nil {
		t.Fatal(appErr)
	}
	invocation := application.NewInvocation(*principal, model.RequestMetadata{RequestID: "mail-integration"})
	queued, appErr := helper.App.SendTestMail(ctx, invocation)
	if appErr != nil {
		t.Fatal(appErr)
	}
	if queued.TargetUserID != operator.ID || queued.State != model.MailDeliveryQueued || queued.MaskedRecipient != "m***@example.edu" {
		t.Fatalf("queued delivery = %#v", queued)
	}
	page, appErr := helper.App.ListMailDeliveries(ctx, invocation, application.ListMailDeliveriesQuery{States: []model.MailDeliveryState{model.MailDeliveryQueued}, TemplateKeys: []model.MailTemplateKey{model.MailTemplateSystemTest}, Limit: 20})
	if appErr != nil || len(page.Items) != 1 || page.Items[0].ID != queued.ID {
		t.Fatalf("queued delivery page = %#v, %v", page, appErr)
	}
	canceled, appErr := helper.App.CancelMailDelivery(ctx, invocation, queued.ID)
	if appErr != nil || canceled.State != model.MailDeliveryCanceled || canceled.ID != queued.ID || canceled.MessageID != queued.MessageID {
		t.Fatalf("canceled delivery = %#v, %v", canceled, appErr)
	}
	durableCanceled, err := persistence.Mail().GetDelivery(ctx, queued.ID)
	if err != nil || len(durableCanceled.EncryptedPayload) != 0 {
		t.Fatalf("canceled durable delivery = %#v, %v", durableCanceled, err)
	}
	if _, appErr = helper.App.RetryMailDelivery(ctx, invocation, queued.ID); !application.Is(appErr, "mail.conflict") {
		t.Fatalf("RetryMailDelivery(canceled) error = %v", appErr)
	}
	queued, appErr = helper.App.SendTestMail(ctx, invocation)
	if appErr != nil {
		t.Fatal(appErr)
	}

	startIntegrationServer(t, helper)
	deadline := time.Now().Add(10 * time.Second)
	for {
		view, readErr := helper.App.GetMailDelivery(ctx, invocation, queued.ID)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if view.State == model.MailDeliveryAccepted {
			if view.MessageID != queued.MessageID || view.AttemptCount != 1 {
				t.Fatalf("accepted delivery = %#v", view)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("delivery did not reach Accepted: %#v", view)
		}
		time.Sleep(20 * time.Millisecond)
	}
	deliveries := helper.Mailer.Deliveries()
	if len(deliveries) != 1 || deliveries[0].MessageID != queued.MessageID ||
		len(deliveries[0].Recipients) != 1 || deliveries[0].Recipients[0] != operator.Email {
		t.Fatalf("captured deliveries = %#v", deliveries)
	}
	durable, err := persistence.Mail().GetDelivery(ctx, queued.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(durable.EncryptedPayload) != 0 || !durable.AcceptedAt.Valid {
		t.Fatalf("accepted durable delivery retained recoverable payload: %#v", durable)
	}
	audits, err := persistence.Audit().List(ctx, store.AuditListOptions{
		Action: string(model.ActionMailManage), Limit: 10,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	foundMutationAudit := false
	for _, event := range audits {
		if event.Resource.Type == model.ResourceMailDelivery && event.Resource.ID == queued.ID.String() && event.Status == model.AuditStatusSuccess {
			foundMutationAudit = true
		}
	}
	if err != nil || !foundMutationAudit {
		t.Fatalf("mail audit events = %#v, %v", audits, err)
	}
}
