//go:build integration

// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app_test

import (
	"context"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/sudosylabs/proctor/packages/mail"
	"github.com/sudosylabs/proctor/server/app"
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
		testlib.WithServerOptions(app.WithStore(persistence)),
	)
	institution, err := persistence.Institution().Save(context.Background(), &model.Institution{
		Name: "recovery-university", DisplayName: "Recovery University",
	})
	if err != nil {
		t.Fatal(err)
	}
	const oldPassword = "correct horse battery staple"
	const newPassword = "new correct horse battery staple"
	user, appErr := helper.App.CreateLocalUser(context.Background(), &model.User{
		Username: "recovery-user", Email: "recovery-user@example.edu",
		DisplayName: "Recovery User",
	}, oldPassword)
	if appErr != nil {
		t.Fatal(appErr)
	}
	login := loginIntegrationUser(
		t,
		helper.Server.Handler(),
		user.Email,
		oldPassword,
		model.SessionClientCLI,
		"recovery-device",
	)

	verificationRequest := performJSONRequest(
		helper.Server.Handler(),
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
	deliveries := helper.Mailer.Deliveries()
	if len(deliveries) != 1 {
		t.Fatalf("verification deliveries = %d", len(deliveries))
	}
	verificationToken := credentialFromDelivery(t, deliveries[0])
	if strings.Contains(helper.Logs.String(), verificationToken) {
		t.Fatal("verification token appeared in logs")
	}
	verificationComplete := performJSONRequest(
		helper.Server.Handler(),
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
	verified, err := persistence.User().Get(context.Background(), user.Id)
	if err != nil || !verified.EmailVerified {
		t.Fatalf("verified user = %#v, %v", verified, err)
	}
	verificationReplay := performJSONRequest(
		helper.Server.Handler(),
		http.MethodPost,
		"/api/v1/auth/email-verification/complete",
		map[string]any{"token": verificationToken},
		"",
	)
	if verificationReplay.Code != http.StatusBadRequest {
		t.Fatalf("verification replay status = %d", verificationReplay.Code)
	}

	unknownReset := performJSONRequest(
		helper.Server.Handler(),
		http.MethodPost,
		"/api/v1/auth/password-reset/request",
		map[string]any{"email": "unknown@example.edu"},
		"",
	)
	knownReset := performJSONRequest(
		helper.Server.Handler(),
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
	deliveries = helper.Mailer.Deliveries()
	if len(deliveries) != 2 {
		t.Fatalf("password reset deliveries = %d, want 2 total", len(deliveries))
	}
	resetToken := credentialFromDelivery(t, deliveries[1])
	if strings.Contains(helper.Logs.String(), resetToken) {
		t.Fatal("password reset token appeared in logs")
	}
	resetComplete := performJSONRequest(
		helper.Server.Handler(),
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
	oldSession := performJSONRequest(
		helper.Server.Handler(),
		http.MethodGet,
		"/api/v1/users/me",
		nil,
		login.Tokens.AccessToken,
	)
	if oldSession.Code != http.StatusUnauthorized {
		t.Fatalf("pre-reset session status = %d", oldSession.Code)
	}
	oldLogin := performJSONRequest(
		helper.Server.Handler(),
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
		helper.Server.Handler(),
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
		helper.Server.Handler(),
		http.MethodPost,
		"/api/v1/auth/password-reset/complete",
		map[string]any{"token": resetToken, "password": newPassword},
		"",
	)
	if resetReplay.Code != http.StatusBadRequest {
		t.Fatalf("password reset replay status = %d", resetReplay.Code)
	}
	audits, err := persistence.Audit().List(context.Background(), store.AuditListOptions{
		Action: "authentication.password_reset.complete",
		Limit:  10,
	})
	if err != nil ||
		len(audits) != 1 ||
		audits[0].Resource.Id != user.Id ||
		audits[0].ScopeId != institution.Id {
		t.Fatalf("password reset audits = %#v, %v", audits, err)
	}
}

func TestPasswordResetRequestRateLimitDoesNotDependOnAccountExistence(
	t *testing.T,
) {
	dataSource := requireAuthenticationDatabase(t)
	persistence := openAuthenticationStore(t, dataSource)
	helper := testlib.Setup(
		t,
		testlib.WithConfig(func(cfg *config.Config) {
			cfg.Authentication.AccountRecovery.RateLimit.MaximumAttempts = 1
			cfg.Authentication.AccountRecovery.RateLimit.MaximumSourceAttempts = 100
		}),
		testlib.WithServerOptions(app.WithStore(persistence)),
	)
	for attempt := 1; attempt <= 2; attempt++ {
		response := performJSONRequest(
			helper.Server.Handler(),
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
