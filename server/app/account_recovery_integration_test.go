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
	"net/http"
	"os"
	"regexp"
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

func TestAccountRecoveryIntegration(t *testing.T) {
	dataSource := requireAuthenticationDatabase(t)
	persistence := openAuthenticationStore(t, dataSource)
	helper := testlib.Setup(
		t,
		testlib.WithConfig(func(cfg *config.Config) {
			cfg.Server.PublicURL = "https://proctor.example.edu"
			cfg.Authentication.AccountRecovery.RateLimit.MaximumAttempts = 20
			cfg.Authentication.AccountRecovery.RateLimit.MaximumSourceAttempts = 100
		}),
		testlib.WithStore(persistence),
	)
	bootstrap, appErr := helper.App.BootstrapInstallation(context.Background(), application.Invocation{}, application.BootstrapInstallationCommand{
		InstitutionName: "recovery-university", InstitutionDisplayName: "Recovery University",
		AdministratorUsername: "recovery-admin", AdministratorEmail: "recovery-admin@example.edu",
		AdministratorDisplayName: "Recovery Admin", AdministratorLocale: "en", AdministratorTimezone: "UTC",
		Password: "bootstrap correct horse battery staple", BootstrapSecret: testlib.BootstrapSecret, Source: "127.0.0.1:1",
	})
	if appErr != nil {
		t.Fatal(appErr)
	}
	institution := bootstrap.Institution
	const oldPassword = "correct horse battery staple"
	const newPassword = "new correct horse battery staple"
	user, appErr := helper.App.CreateLocalUser(context.Background(), &model.User{
		Username: "recovery-user", Email: "recovery-user@example.edu",
		DisplayName: "Recovery User",
	}, oldPassword)
	if appErr != nil {
		t.Fatal(appErr)
	}
	startIntegrationServer(t, helper)
	login := loginIntegrationUser(
		t,
		helper.Handler(),
		user.Email,
		oldPassword,
		model.SessionClientCLI,
		"recovery-device",
	)

	verificationRequest := performJSONRequest(
		helper.Handler(),
		http.MethodPost,
		"/api/v1/auth/email-verification/request",
		nil,
		login.Tokens.AccessToken,
	)
	if verificationRequest.Code != http.StatusAccepted {
		t.Fatalf(
			"verification request status = %d: %s",
			verificationRequest.Code,
			verificationRequest.Body.String(),
		)
	}
	deliveries := waitForRecoveryDeliveries(t, helper, 1)
	if len(deliveries) != 1 {
		t.Fatalf("verification deliveries = %d", len(deliveries))
	}
	verificationToken := credentialFromDelivery(t, deliveries[0])
	if strings.Contains(helper.Logs.String(), verificationToken) {
		t.Fatal("verification token appeared in logs")
	}
	verificationComplete := performJSONRequest(
		helper.Handler(),
		http.MethodPost,
		"/api/v1/auth/email-verification/complete",
		map[string]any{"token": verificationToken},
		"",
	)
	if verificationComplete.Code != http.StatusNoContent {
		t.Fatalf(
			"verification completion status = %d: %s",
			verificationComplete.Code,
			verificationComplete.Body.String(),
		)
	}
	verified, err := persistence.User().Get(context.Background(), user.ID.String())
	if err != nil || !verified.EmailVerified {
		t.Fatalf("verified user = %#v, %v", verified, err)
	}
	verificationReplay := performJSONRequest(
		helper.Handler(),
		http.MethodPost,
		"/api/v1/auth/email-verification/complete",
		map[string]any{"token": verificationToken},
		"",
	)
	if verificationReplay.Code != http.StatusBadRequest {
		t.Fatalf("verification replay status = %d", verificationReplay.Code)
	}

	unknownReset := performJSONRequest(
		helper.Handler(),
		http.MethodPost,
		"/api/v1/auth/password-reset/request",
		map[string]any{"email": "unknown@example.edu"},
		"",
	)
	knownReset := performJSONRequest(
		helper.Handler(),
		http.MethodPost,
		"/api/v1/auth/password-reset/request",
		map[string]any{"email": user.Email},
		"",
	)
	if unknownReset.Code != http.StatusAccepted ||
		knownReset.Code != http.StatusAccepted {
		t.Fatalf(
			"reset request statuses unknown=%d known=%d",
			unknownReset.Code,
			knownReset.Code,
		)
	}
	deliveries = waitForRecoveryDeliveries(t, helper, 2)
	if len(deliveries) != 2 {
		t.Fatalf("password reset deliveries = %d, want 2 total", len(deliveries))
	}
	resetToken := credentialFromDelivery(t, deliveries[1])
	if strings.Contains(helper.Logs.String(), resetToken) {
		t.Fatal("password reset token appeared in logs")
	}
	resetComplete := performJSONRequest(
		helper.Handler(),
		http.MethodPost,
		"/api/v1/auth/password-reset/complete",
		map[string]any{"token": resetToken, "password": newPassword},
		"",
	)
	if resetComplete.Code != http.StatusNoContent {
		t.Fatalf(
			"password reset completion status = %d: %s",
			resetComplete.Code,
			resetComplete.Body.String(),
		)
	}
	deliveries = waitForRecoveryDeliveries(t, helper, 3)
	changedMessage := strings.ReplaceAll(string(deliveries[2].Data), "=\r\n", "")
	if credentialPattern.MatchString(changedMessage) || !strings.Contains(changedMessage, "Password changed") {
		t.Fatalf("password-changed notice contains a credential or lacks expected copy")
	}
	oldSession := performJSONRequest(
		helper.Handler(),
		http.MethodGet,
		"/api/v1/users/me",
		nil,
		login.Tokens.AccessToken,
	)
	if oldSession.Code != http.StatusUnauthorized {
		t.Fatalf("pre-reset session status = %d", oldSession.Code)
	}
	oldLogin := performJSONRequest(
		helper.Handler(),
		http.MethodPost,
		"/api/v1/auth/login",
		map[string]any{
			"login_id": user.Email, "password": oldPassword,
			"client_type": model.SessionClientCLI,
		},
		"",
	)
	if oldLogin.Code != http.StatusUnauthorized {
		t.Fatalf("old password login status = %d", oldLogin.Code)
	}
	newLogin := performJSONRequest(
		helper.Handler(),
		http.MethodPost,
		"/api/v1/auth/login",
		map[string]any{
			"login_id": user.Email, "password": newPassword,
			"client_type": model.SessionClientCLI,
		},
		"",
	)
	if newLogin.Code != http.StatusOK {
		t.Fatalf("new password login status = %d: %s", newLogin.Code, newLogin.Body.String())
	}
	resetReplay := performJSONRequest(
		helper.Handler(),
		http.MethodPost,
		"/api/v1/auth/password-reset/complete",
		map[string]any{"token": resetToken, "password": newPassword},
		"",
	)
	if resetReplay.Code != http.StatusBadRequest {
		t.Fatalf("password reset replay status = %d", resetReplay.Code)
	}
	audits, err := persistence.Audit().List(context.Background(), store.AuditListOptions{
		Action:     "authentication.password_reset.complete",
		Limit:      10,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	if err != nil ||
		len(audits) != 1 ||
		audits[0].Resource.ID != user.ID.String() ||
		audits[0].ScopeID != institution.ID.String() {
		t.Fatalf("password reset audits = %#v, %v", audits, err)
	}
	encodedAudits, err := json.Marshal(audits)
	if err != nil {
		t.Fatal(err)
	}
	for _, password := range []string{oldPassword, newPassword} {
		if strings.Contains(string(encodedAudits), password) {
			t.Fatal("password appeared in password-reset audit")
		}
	}
}

func TestPasswordResetRequestRateLimitDoesNotDependOnAccountExistence(
	t *testing.T,
) {
	dataSource := requireAuthenticationDatabase(t)
	persistence := openAuthenticationStore(t, dataSource)
	seedInitialAuthenticationAccessPolicy(t, persistence)
	helper := testlib.Setup(
		t,
		testlib.WithConfig(func(cfg *config.Config) {
			cfg.Authentication.AccountRecovery.RateLimit.MaximumAttempts = 1
			cfg.Authentication.AccountRecovery.RateLimit.MaximumSourceAttempts = 100
		}),
		testlib.WithStore(persistence),
	)
	for attempt := 1; attempt <= 2; attempt++ {
		response := performJSONRequest(
			helper.Handler(),
			http.MethodPost,
			"/api/v1/auth/password-reset/request",
			map[string]any{"email": "unknown@example.edu"},
			"",
		)
		want := http.StatusAccepted
		if attempt == 2 {
			want = http.StatusTooManyRequests
		}
		if response.Code != want {
			t.Fatalf(
				"unknown reset attempt %d status = %d, want %d",
				attempt,
				response.Code,
				want,
			)
		}
	}
}

func TestCurrentAccessPolicyGatesLocalLoginAndPasswordRecoveryRealGraph(t *testing.T) {
	dataSource := requireAuthenticationDatabase(t)
	persistence := openAuthenticationStore(t, dataSource)
	helper := testlib.Setup(t, testlib.WithConfig(func(cfg *config.Config) {
		cfg.Server.PublicURL = "https://proctor.example.edu"
		cfg.Authentication.AccountRecovery.RateLimit.MaximumAttempts = 20
		cfg.Authentication.AccountRecovery.RateLimit.MaximumSourceAttempts = 100
	}), testlib.WithStore(persistence))
	bootstrap, appErr := helper.App.BootstrapInstallation(context.Background(), application.Invocation{}, application.BootstrapInstallationCommand{
		InstitutionName: "policy-recovery", InstitutionDisplayName: "Policy Recovery",
		AdministratorUsername: "policy-recovery-admin", AdministratorEmail: "policy-recovery-admin@example.edu",
		AdministratorLocale: "en", AdministratorTimezone: "UTC",
		Password: "bootstrap correct horse battery staple", BootstrapSecret: testlib.BootstrapSecret, Source: "127.0.0.1:1",
	})
	if appErr != nil || bootstrap == nil {
		t.Fatalf("bootstrap = %#v, %v", bootstrap, appErr)
	}
	const password = "correct horse battery staple"
	user, appErr := helper.App.CreateLocalUser(context.Background(), &model.User{
		Username: "policy-recovery-user", Email: "policy-recovery-user@example.edu",
	}, password)
	if appErr != nil {
		t.Fatal(appErr)
	}
	startIntegrationServer(t, helper)
	issued := performJSONRequest(helper.Handler(), http.MethodPost, "/api/v1/auth/password-reset/request",
		map[string]any{"email": user.Email}, "")
	if issued.Code != http.StatusAccepted {
		t.Fatalf("initial reset status=%d deliveries=%d", issued.Code, len(helper.Mailer.Deliveries()))
	}
	resetToken := credentialFromDelivery(t, waitForRecoveryDeliveries(t, helper, 1)[0])
	if _, err := persistence.GetMaster().Exec(context.Background(),
		`UPDATE access_policies SET local_login_enabled=FALSE,
		 invitation_local_credential_enabled=FALSE WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	login := performJSONRequest(helper.Handler(), http.MethodPost, "/api/v1/auth/login", map[string]any{
		"login_id": user.Email, "password": password, "client_type": model.SessionClientWeb,
	}, "")
	if login.Code != http.StatusUnauthorized {
		t.Fatalf("disabled local login status=%d: %s", login.Code, login.Body.String())
	}
	hidden := performJSONRequest(helper.Handler(), http.MethodPost, "/api/v1/auth/password-reset/request",
		map[string]any{"email": user.Email}, "")
	if hidden.Code != http.StatusAccepted || len(helper.Mailer.Deliveries()) != 1 {
		t.Fatalf("disabled reset status=%d deliveries=%d", hidden.Code, len(helper.Mailer.Deliveries()))
	}
	completed := performJSONRequest(helper.Handler(), http.MethodPost, "/api/v1/auth/password-reset/complete",
		map[string]any{"token": resetToken, "password": "new correct horse battery staple"}, "")
	if completed.Code != http.StatusBadRequest {
		t.Fatalf("disabled reset completion status=%d: %s", completed.Code, completed.Body.String())
	}
	retained, err := persistence.UserToken().GetByHash(context.Background(), model.HashToken(resetToken), model.UserTokenPasswordReset)
	if err != nil || retained.ConsumedAt.Valid {
		t.Fatalf("disabled reset retained token=%#v err=%v", retained, err)
	}
}

func requireAuthenticationDatabase(t *testing.T) string {
	t.Helper()
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	if dataSource == "" {
		t.Fatal("PROCTOR_TEST_DATABASE_URL is not set")
	}
	return dataSource
}

var credentialPattern = regexp.MustCompile(
	`token(?:=3D|=)([A-Za-z0-9_-]{43})`,
)

func credentialFromDelivery(t *testing.T, delivery mail.Delivery) string {
	t.Helper()
	message := strings.ReplaceAll(string(delivery.Data), "=\r\n", "")
	match := credentialPattern.FindStringSubmatch(message)
	if len(match) != 2 {
		t.Fatalf("credential link missing from delivery")
	}
	return match[1]
}

func waitForRecoveryDeliveries(t *testing.T, helper *testlib.Helper, count int) []mail.Delivery {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		deliveries := helper.Mailer.Deliveries()
		if len(deliveries) >= count {
			return deliveries
		}
		if time.Now().After(deadline) {
			t.Fatalf("mail deliveries = %d, want at least %d", len(deliveries), count)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
