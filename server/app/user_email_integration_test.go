//go:build integration

// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/packages/mail"
	application "github.com/sudosylabs/proctor/server/app"
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
	bootstrap, appErr := helper.App.BootstrapInstallation(ctx, application.Invocation{}, application.BootstrapInstallationCommand{
		InstitutionName: "email-transition-university", InstitutionDisplayName: "Email Transition University",
		AdministratorUsername: "email-transition-admin", AdministratorEmail: "email-transition-admin@example.edu",
		AdministratorDisplayName: "Email Transition Admin", AdministratorLocale: "en", AdministratorTimezone: "UTC",
		Password: password, BootstrapSecret: testlib.BootstrapSecret, Source: "127.0.0.1:1",
	})
	if appErr != nil {
		t.Fatal(appErr)
	}
	first, appErr := helper.App.CreateLocalUser(ctx, &model.User{
		Username: "email-transition-first", Email: "first-old@example.edu", DisplayName: "First User",
	}, password)
	if appErr != nil {
		t.Fatal(appErr)
	}
	second, appErr := helper.App.CreateLocalUser(ctx, &model.User{
		Username: "email-transition-second", Email: "second-old@example.edu", DisplayName: "Second User",
	}, password)
	if appErr != nil {
		t.Fatal(appErr)
	}

	unprivilegedLogin := loginIntegrationUser(t, helper.Handler(), first.Username, password, model.SessionClientCLI, "email-transition-unprivileged")
	strengthenRoleAdministratorSession(t, helper.Handler(), unprivilegedLogin.Tokens.AccessToken)
	denied := performJSONRequest(helper.Handler(), http.MethodPut, "/api/v1/users/"+second.ID.String()+"/email",
		map[string]any{"email": "denied@example.edu"}, unprivilegedLogin.Tokens.AccessToken)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("unprivileged email change status = %d: %s", denied.Code, denied.Body.String())
	}

	login := loginIntegrationUser(t, helper.Handler(), bootstrap.Administrator.Username, password, model.SessionClientCLI, "email-transition-admin")
	strengthenRoleAdministratorSession(t, helper.Handler(), login.Tokens.AccessToken)
	changeFirst := performJSONRequest(helper.Handler(), http.MethodPut, "/api/v1/users/"+first.ID.String()+"/email",
		map[string]any{"email": "first-new@example.edu"}, login.Tokens.AccessToken)
	if changeFirst.Code != http.StatusOK {
		t.Fatalf("first email change status = %d: %s", changeFirst.Code, changeFirst.Body.String())
	}
	assertNarrowEmailTransitionResponse(t, changeFirst.Body.Bytes(), first.ID, false)
	changeFirstReplay := performJSONRequest(helper.Handler(), http.MethodPut, "/api/v1/users/"+first.ID.String()+"/email",
		map[string]any{"email": " FIRST-NEW@EXAMPLE.EDU "}, login.Tokens.AccessToken)
	if changeFirstReplay.Code != http.StatusOK {
		t.Fatalf("first email change replay status = %d: %s", changeFirstReplay.Code, changeFirstReplay.Body.String())
	}
	assertNarrowEmailTransitionResponse(t, changeFirstReplay.Body.Bytes(), first.ID, false)
	changeSecond := performJSONRequest(helper.Handler(), http.MethodPut, "/api/v1/users/"+second.ID.String()+"/email",
		map[string]any{"email": "second-new@example.edu"}, login.Tokens.AccessToken)
	if changeSecond.Code != http.StatusOK {
		t.Fatalf("second email change status = %d: %s", changeSecond.Code, changeSecond.Body.String())
	}
	assertNarrowEmailTransitionResponse(t, changeSecond.Body.Bytes(), second.ID, false)
	privileged := performJSONRequest(helper.Handler(), http.MethodPost, "/api/v1/users/"+second.ID.String()+"/email/verify", nil, login.Tokens.AccessToken)
	if privileged.Code != http.StatusOK {
		t.Fatalf("privileged verification status = %d: %s", privileged.Code, privileged.Body.String())
	}
	assertNarrowEmailTransitionResponse(t, privileged.Body.Bytes(), second.ID, true)

	startIntegrationServer(t, helper)
	deliveries := waitForEmailTransitionDeliveries(t, helper)
	firstVerification := deliveryToMatching(t, deliveries, "first-new@example.edu", func(message string) bool {
		return credentialPattern.MatchString(message)
	})
	verificationToken := credentialFromDelivery(t, firstVerification)
	if strings.Contains(helper.Logs.String(), verificationToken) {
		t.Fatal("email-change verification token appeared in logs")
	}
	if message := deliveryToMatching(t, deliveries, "first-old@example.edu", func(message string) bool {
		return strings.Contains(message, "email address")
	}); credentialPattern.Match(message.Data) {
		t.Fatal("old-address warning contains a credential or lacks expected copy")
	}
	if message := deliveryToMatching(t, deliveries, "second-new@example.edu", func(message string) bool {
		return strings.Contains(message, "verified")
	}); credentialPattern.Match(message.Data) {
		t.Fatal("privileged-verification notice contains a credential or lacks expected copy")
	}
	if message := deliveryToMatching(t, deliveries, "second-old@example.edu", func(message string) bool {
		return strings.Contains(message, "email address")
	}); credentialPattern.Match(message.Data) {
		t.Fatal("second old-address warning contains a credential")
	}

	completed := performJSONRequest(helper.Handler(), http.MethodPost, "/api/v1/auth/email-verification/complete",
		map[string]any{"token": verificationToken}, "")
	if completed.Code != http.StatusNoContent {
		t.Fatalf("email-change verification completion status = %d: %s", completed.Code, completed.Body.String())
	}
	for _, expected := range []struct {
		id       model.UserID
		email    string
		verified bool
	}{{first.ID, "first-new@example.edu", true}, {second.ID, "second-new@example.edu", true}} {
		user, err := persistence.User().Get(ctx, expected.id.String())
		if err != nil || user.Email != expected.email || user.EmailVerified != expected.verified {
			t.Fatalf("persisted email transition user = %#v, %v", user, err)
		}
	}
	durable, err := persistence.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{
		TemplateKeys: []model.MailTemplateKey{model.MailTemplateIdentityEmailChangeVerifyNew}, Limit: 10,
	})
	if err != nil || len(durable) != 2 {
		t.Fatalf("durable verification deliveries = %#v, %v", durable, err)
	}
	suppressed := 0
	for _, delivery := range durable {
		if delivery.TargetUserID == second.ID {
			if delivery.State != model.MailDeliverySuppressed || len(delivery.EncryptedPayload) != 0 {
				t.Fatalf("privileged verification retained credential delivery = %#v", delivery)
			}
			suppressed++
		}
	}
	if suppressed != 1 {
		t.Fatalf("suppressed privileged verification deliveries = %d, want 1", suppressed)
	}
	audits, err := persistence.Audit().List(ctx, store.AuditListOptions{
		Action: string(model.ActionUserManage), Limit: 100,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	encodedAudits, err := json.Marshal(audits)
	if err != nil {
		t.Fatal(err)
	}
	for _, mailbox := range []string{"first-old@example.edu", "first-new@example.edu", "second-old@example.edu", "second-new@example.edu"} {
		if strings.Contains(string(encodedAudits), mailbox) {
			t.Fatalf("email-transition audit exposed mailbox %q: %s", mailbox, encodedAudits)
		}
	}
}

func assertNarrowEmailTransitionResponse(t *testing.T, body []byte, userID model.UserID, verified bool) {
	t.Helper()
	var response struct {
		ID            string `json:"id"`
		EmailVerified bool   `json:"email_verified"`
	}
	if err := json.Unmarshal(body, &response); err != nil || response.ID != userID.String() || response.EmailVerified != verified {
		t.Fatalf("email-transition response = %s, %v", body, err)
	}
	for _, forbidden := range []string{"@", "username", "display_name", "locale", "timezone", "last_login_at", "last_activity_at", "disabled_at"} {
		if bytes.Contains(body, []byte(forbidden)) {
			t.Fatalf("email-transition response exposed %q: %s", forbidden, body)
		}
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
		if time.Now().After(deadline) {
			t.Fatal("email transition deliveries did not reach every expected recipient and semantic template")
		}
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
	t.Fatalf("matching delivery to %s not found", recipient)
	return mail.Delivery{}
}

func deliveryTo(t *testing.T, deliveries []mail.Delivery, recipient string) mail.Delivery {
	t.Helper()
	for _, delivery := range deliveries {
		if len(delivery.Recipients) == 1 && delivery.Recipients[0] == recipient {
			return delivery
		}
	}
	t.Fatalf("delivery to %s not found in %#v", recipient, deliveries)
	return mail.Delivery{}
}
