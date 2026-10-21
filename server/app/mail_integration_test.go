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
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/testlib"
)

func TestControlledMailUsesRealApplicationGraphAndDurableWorker(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	require.NotEmpty(t, dataSource, "PROCTOR_TEST_DATABASE_URL is not set")
	persistence := openAuthenticationStore(t, dataSource)
	helper := testlib.Setup(t,
		testlib.WithStore(persistence),
		testlib.WithConfig(func(cfg *config.Config) {
			cfg.Server.ListenAddress = "127.0.0.1:0"
		}),
	)
	ctx := context.Background()
	const password = "correct horse battery staple"
	bootstrap, appErr := helper.App.BootstrapInstallation(ctx, app.Invocation{}, app.BootstrapInstallationCommand{
		InstitutionName: "mail-university", InstitutionDisplayName: "Mail University",
		AdministratorUsername: "mail-operator", AdministratorEmail: "mail-operator@example.edu",
		AdministratorDisplayName: "Mail Operator", AdministratorLocale: "en", AdministratorTimezone: "UTC",
		Password: password, BootstrapSecret: testlib.BootstrapSecret, Source: "127.0.0.1:1",
	})
	require.NoError(t, appErr)
	operator := bootstrap.Administrator
	// This fixture needs a verified mail operator. Production mailbox state is
	// changed only through the named UserToken aggregates.
	_, err := persistence.GetMaster().Exec(ctx, `UPDATE users SET email_verified = TRUE WHERE id = ?`, operator.ID.String())
	require.NoError(t, err)
	operator, err = persistence.User().Get(ctx, operator.ID)
	require.NoError(t, err)
	login, appErr := helper.App.Login(ctx, app.Invocation{}, app.LoginCommand{
		LoginID: operator.Username, Password: password, ClientType: model.SessionClientWeb,
		Source: "127.0.0.1:1", DeviceName: "mail-integration",
	})
	require.NoError(t, appErr)
	principal, appErr := helper.App.AuthenticateAccess(ctx, login.Tokens.AccessToken)
	require.NoError(t, appErr)
	invocation := app.NewInvocation(*principal, model.RequestMetadata{RequestID: "mail-integration"})
	queued, appErr := helper.App.SendTestMail(ctx, invocation)
	require.NoError(t, appErr)
	require.Equal(t, operator.ID, queued.TargetUserID)
	require.Equal(t, model.MailDeliveryQueued, queued.State)
	require.Equal(t, "m***@example.edu", queued.MaskedRecipient)
	page, appErr := helper.App.ListMailDeliveries(ctx, invocation, app.ListMailDeliveriesQuery{States: []model.MailDeliveryState{model.MailDeliveryQueued}, TemplateKeys: []model.MailTemplateKey{model.MailTemplateSystemTest}, Limit: 20})
	require.NoError(t, appErr, "queued delivery page = %#v", page)
	require.Len(t, page.Items, 1, "queued delivery page = %#v", page)
	require.Equal(t, queued.ID, page.Items[0].ID, "queued delivery page = %#v", page)
	canceled, appErr := helper.App.CancelMailDelivery(ctx, invocation, queued.ID)
	require.NoError(t, appErr, "canceled delivery = %#v", canceled)
	require.Equal(t, model.MailDeliveryCanceled, canceled.State, "canceled delivery = %#v", canceled)
	require.Equal(t, queued.ID, canceled.ID, "canceled delivery = %#v", canceled)
	require.Equal(t, queued.MessageID, canceled.MessageID, "canceled delivery = %#v", canceled)
	durableCanceled, err := persistence.Mail().GetDelivery(ctx, queued.ID)
	require.NoError(t, err, "canceled durable delivery = %#v", durableCanceled)
	require.Empty(t, durableCanceled.EncryptedPayload, "canceled durable delivery = %#v", durableCanceled)
	_, appErr = helper.App.RetryMailDelivery(ctx, invocation, queued.ID)
	require.True(t, apperror.Is(appErr, "mail.conflict"), "RetryMailDelivery(canceled) error = %v", appErr)
	queued, appErr = helper.App.SendTestMail(ctx, invocation)
	require.NoError(t, appErr)

	startIntegrationServer(t, helper)
	deadline := time.Now().Add(10 * time.Second)
	for {
		view, readErr := helper.App.GetMailDelivery(ctx, invocation, queued.ID)
		require.NoError(t, readErr)
		if view.State == model.MailDeliveryAccepted {
			require.Equal(t, queued.MessageID, view.MessageID)
			require.Equal(t, 1, view.AttemptCount)
			break
		}
		require.False(t, time.Now().After(deadline), "delivery did not reach Accepted: %#v", view)
		time.Sleep(20 * time.Millisecond)
	}
	deliveries := helper.Mailer.Deliveries()
	require.Len(t, deliveries, 1)
	require.Equal(t, queued.MessageID, deliveries[0].MessageID)
	require.Len(t, deliveries[0].Recipients, 1)
	require.Equal(t, operator.Email, deliveries[0].Recipients[0])
	durable, err := persistence.Mail().GetDelivery(ctx, queued.ID)
	require.NoError(t, err)
	require.Empty(t, durable.EncryptedPayload)
	require.True(t, durable.AcceptedAt.Valid)
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
	require.NoError(t, err, "mail audit events = %#v", audits)
	require.True(t, foundMutationAudit, "mail audit events = %#v", audits)
}
