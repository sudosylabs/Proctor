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
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/packages/mail"
	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/testlib"
)

func TestEmailTransitionsUseRealServerGraphAndFrozenRecipients(t *testing.T) {
	dataSource := requireAuthenticationDatabase(t)
	persistence := openAuthenticationStore(t, dataSource)
	helper := testlib.Setup(t,
		testlib.WithStore(persistence),
		testlib.WithConfig(func(cfg *config.Config) {
			cfg.Server.PublicURL = "https://proctor.example.edu"
			cfg.Authentication.MFA.Enabled = true
			cfg.Authentication.MFA.EncryptionKey = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{29}, 32))
		}),
	)
	ctx := context.Background()
	const password = "correct horse battery staple"
	bootstrap, appErr := helper.App.BootstrapInstallation(ctx, app.Invocation{}, app.BootstrapInstallationCommand{
		InstitutionName: "email-transition-university", InstitutionDisplayName: "Email Transition University",
		AdministratorUsername: "email-transition-admin", AdministratorEmail: "email-transition-admin@example.edu",
		AdministratorDisplayName: "Email Transition Admin", AdministratorLocale: "en", AdministratorTimezone: "UTC",
		Password: password, BootstrapSecret: testlib.BootstrapSecret, Source: "127.0.0.1:1",
	})
	require.NoError(t, appErr)
	first, appErr := helper.App.CreateLocalUser(ctx, app.CreateLocalUserCommand{User: &model.User{
		Username: "email-transition-first", Email: "first-old@example.edu", DisplayName: "First User",
	}, Password: password})
	require.NoError(t, appErr)
	second, appErr := helper.App.CreateLocalUser(ctx, app.CreateLocalUserCommand{User: &model.User{
		Username: "email-transition-second", Email: "second-old@example.edu", DisplayName: "Second User",
	}, Password: password})
	require.NoError(t, appErr)

	unprivilegedLogin := loginIntegrationUser(t, helper.Handler(), first.Username, password, model.SessionClientCLI, "email-transition-unprivileged")
	strengthenRoleAdministratorSession(t, helper.Handler(), unprivilegedLogin.Tokens.AccessToken)
	denied := performJSONRequest(helper.Handler(), http.MethodPut, "/api/v1/users/"+second.ID.String()+"/email",
		map[string]any{"email": "denied@example.edu"}, unprivilegedLogin.Tokens.AccessToken)
	require.Equal(t, http.StatusForbidden, denied.Code, "unprivileged email change status = %d: %s", denied.Code, denied.Body.String())

	login := loginIntegrationUser(t, helper.Handler(), bootstrap.Administrator.Username, password, model.SessionClientCLI, "email-transition-admin")
	strengthenRoleAdministratorSession(t, helper.Handler(), login.Tokens.AccessToken)
	changeFirst := performJSONRequest(helper.Handler(), http.MethodPut, "/api/v1/users/"+first.ID.String()+"/email",
		map[string]any{"email": "first-new@example.edu"}, login.Tokens.AccessToken)
	require.Equal(t, http.StatusOK, changeFirst.Code, "first email change status = %d: %s", changeFirst.Code, changeFirst.Body.String())
	assertNarrowEmailTransitionResponse(t, changeFirst.Body.Bytes(), first.ID, false)
	changeFirstReplay := performJSONRequest(helper.Handler(), http.MethodPut, "/api/v1/users/"+first.ID.String()+"/email",
		map[string]any{"email": " FIRST-NEW@EXAMPLE.EDU "}, login.Tokens.AccessToken)
	require.Equal(t, http.StatusOK, changeFirstReplay.Code, "first email change replay status = %d: %s", changeFirstReplay.Code, changeFirstReplay.Body.String())
	assertNarrowEmailTransitionResponse(t, changeFirstReplay.Body.Bytes(), first.ID, false)
	changeSecond := performJSONRequest(helper.Handler(), http.MethodPut, "/api/v1/users/"+second.ID.String()+"/email",
		map[string]any{"email": "second-new@example.edu"}, login.Tokens.AccessToken)
	require.Equal(t, http.StatusOK, changeSecond.Code, "second email change status = %d: %s", changeSecond.Code, changeSecond.Body.String())
	assertNarrowEmailTransitionResponse(t, changeSecond.Body.Bytes(), second.ID, false)
	privileged := performJSONRequest(helper.Handler(), http.MethodPost, "/api/v1/users/"+second.ID.String()+"/email/verify", nil, login.Tokens.AccessToken)
	require.Equal(t, http.StatusOK, privileged.Code, "privileged verification status = %d: %s", privileged.Code, privileged.Body.String())
	assertNarrowEmailTransitionResponse(t, privileged.Body.Bytes(), second.ID, true)

	startIntegrationServer(t, helper)
	deliveries := waitForEmailTransitionDeliveries(t, helper)
	firstVerification := deliveryToMatching(t, deliveries, "first-new@example.edu", func(message string) bool {
		return credentialPattern.MatchString(message)
	})
	verificationToken := credentialFromDelivery(t, firstVerification)
	require.NotContains(t, helper.Logs.String(), verificationToken, "email-change verification token appeared in logs")
	require.False(t, credentialPattern.Match(deliveryToMatching(t, deliveries, "first-old@example.edu", func(message string) bool {
		return strings.Contains(message, "email address")
	}).Data), "old-address warning contains a credential or lacks expected copy")
	require.False(t, credentialPattern.Match(deliveryToMatching(t, deliveries, "second-new@example.edu", func(message string) bool {
		return strings.Contains(message, "verified")
	}).Data), "privileged-verification notice contains a credential or lacks expected copy")
	require.False(t, credentialPattern.Match(deliveryToMatching(t, deliveries, "second-old@example.edu", func(message string) bool {
		return strings.Contains(message, "email address")
	}).Data), "second old-address warning contains a credential")

	completed := performJSONRequest(helper.Handler(), http.MethodPost, "/api/v1/auth/email-verification/complete",
		map[string]any{"token": verificationToken}, "")
	require.Equal(t, http.StatusNoContent, completed.Code, "email-change verification completion status = %d: %s", completed.Code, completed.Body.String())
	for _, expected := range []struct {
		id       model.UserID
		email    string
		verified bool
	}{{first.ID, "first-new@example.edu", true}, {second.ID, "second-new@example.edu", true}} {
		t.Run(fmt.Sprintf("id=%v", expected.id), func(t *testing.T) {
			user, err := persistence.User().Get(ctx, expected.id)
			require.NoError(t, err, "persisted email transition user = %#v", user)
			require.Equal(t, expected.email, user.Email, "persisted email transition user = %#v", user)
			require.Equal(t, expected.verified, user.EmailVerified, "persisted email transition user = %#v", user)
		})
	}
	durable, err := persistence.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{
		TemplateKeys: []model.MailTemplateKey{model.MailTemplateIdentityEmailChangeVerifyNew}, Limit: 10,
	})
	require.NoError(t, err, "durable verification deliveries")
	require.Len(t, durable, 2, "durable verification deliveries")
	suppressed := 0
	for _, delivery := range durable {
		if delivery.TargetUserID == second.ID {
			require.Equal(t, model.MailDeliverySuppressed, delivery.State)
			require.Empty(t, delivery.EncryptedPayload)
			suppressed++
		}
	}
	require.Equal(t, 1, suppressed, "suppressed privileged verification deliveries")
	audits, err := persistence.Audit().List(ctx, store.AuditListOptions{
		Action: string(model.ActionUserManage), Limit: 100,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	require.NoError(t, err)
	encodedAudits, err := json.Marshal(audits)
	require.NoError(t, err)
	for _, mailbox := range []string{"first-old@example.edu", "first-new@example.edu", "second-old@example.edu", "second-new@example.edu"} {
		require.NotContains(t, string(encodedAudits), mailbox, "email-transition audit exposed mailbox")
	}
}

func assertNarrowEmailTransitionResponse(t *testing.T, body []byte, userID model.UserID, verified bool) {
	t.Helper()
	var response struct {
		ID            string `json:"id"`
		EmailVerified bool   `json:"email_verified"`
	}
	require.NoError(t, json.Unmarshal(body, &response), "email-transition response = %s", body)
	require.Equal(t, userID.String(), response.ID, "email-transition response = %s", body)
	require.Equal(t, verified, response.EmailVerified, "email-transition response = %s", body)
	for _, forbidden := range []string{"@", "username", "display_name", "locale", "timezone", "last_login_at", "last_activity_at", "disabled_at"} {
		require.False(t, bytes.Contains(body, []byte(forbidden)), "email-transition response exposed %q: %s", forbidden, body)
	}
}

func waitForEmailTransitionDeliveries(t *testing.T, helper *testlib.Helper) []mail.Delivery {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		deliveries := helper.Mailer.Deliveries()
		if hasMatchingDelivery(deliveries, "first-new@example.edu", credentialPattern.MatchString) &&
			hasMatchingDelivery(deliveries, "first-old@example.edu", func(message string) bool { return strings.Contains(message, "email address") }) &&
			hasMatchingDelivery(deliveries, "second-new@example.edu", func(message string) bool { return strings.Contains(message, "verified") }) &&
			hasMatchingDelivery(deliveries, "second-old@example.edu", func(message string) bool { return strings.Contains(message, "email address") }) {
			return deliveries
		}
		require.False(t, time.Now().After(deadline), "email transition deliveries did not reach every expected recipient and semantic template")
		time.Sleep(20 * time.Millisecond)
	}
}

func hasMatchingDelivery(deliveries []mail.Delivery, recipient string, matches func(string) bool) bool {
	for _, delivery := range deliveries {
		if len(delivery.Recipients) == 1 && delivery.Recipients[0] == recipient && matches(string(delivery.Data)) {
			return true
		}
	}
	return false
}

func deliveryToMatching(t *testing.T, deliveries []mail.Delivery, recipient string, matches func(string) bool) mail.Delivery {
	t.Helper()
	for _, delivery := range deliveries {
		if len(delivery.Recipients) == 1 && delivery.Recipients[0] == recipient && matches(string(delivery.Data)) {
			return delivery
		}
	}
	require.FailNow(t, fmt.Sprintf("matching delivery to %s not found", recipient))
	return mail.Delivery{}
}
