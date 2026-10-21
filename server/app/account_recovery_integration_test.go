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

	"github.com/stretchr/testify/require"

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
		testlib.WithStore(persistence),
	)
	bootstrap, appErr := helper.App.BootstrapInstallation(context.Background(), app.Invocation{}, app.BootstrapInstallationCommand{
		InstitutionName: "recovery-university", InstitutionDisplayName: "Recovery University",
		AdministratorUsername: "recovery-admin", AdministratorEmail: "recovery-admin@example.edu",
		AdministratorDisplayName: "Recovery Admin", AdministratorLocale: "en", AdministratorTimezone: "UTC",
		Password: "bootstrap correct horse battery staple", BootstrapSecret: testlib.BootstrapSecret, Source: "127.0.0.1:1",
	})
	require.NoError(t, appErr)
	institution := bootstrap.Institution
	const oldPassword = "correct horse battery staple"
	const newPassword = "new correct horse battery staple"
	user, appErr := helper.App.CreateLocalUser(context.Background(), app.CreateLocalUserCommand{User: &model.User{
		Username: "recovery-user", Email: "recovery-user@example.edu",
		DisplayName: "Recovery User",
	}, Password: oldPassword})
	require.NoError(t, appErr)
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
	require.Equal(t, http.StatusAccepted, verificationRequest.Code, "verification request status = %d: %s", verificationRequest.Code, verificationRequest.Body.String())
	deliveries := waitForRecoveryDeliveries(t, helper, 1)
	require.Len(t, deliveries, 1, "verification deliveries")
	verificationToken := credentialFromDelivery(t, deliveries[0])
	require.NotContains(t, helper.Logs.String(), verificationToken, "verification token appeared in logs")
	verificationComplete := performJSONRequest(
		helper.Handler(),
		http.MethodPost,
		"/api/v1/auth/email-verification/complete",
		map[string]any{"token": verificationToken},
		"",
	)
	require.Equal(t, http.StatusNoContent, verificationComplete.Code, "verification completion status = %d: %s", verificationComplete.Code, verificationComplete.Body.String())
	verified, err := persistence.User().Get(context.Background(), user.ID)
	require.NoError(t, err, "verified user = %#v", verified)
	require.True(t, verified.EmailVerified, "verified user = %#v", verified)
	verificationReplay := performJSONRequest(
		helper.Handler(),
		http.MethodPost,
		"/api/v1/auth/email-verification/complete",
		map[string]any{"token": verificationToken},
		"",
	)
	require.Equal(t, http.StatusBadRequest, verificationReplay.Code, "verification replay status")

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
	require.Equal(t, http.StatusAccepted, unknownReset.Code, "reset request statuses unknown")
	require.Equal(t, http.StatusAccepted, knownReset.Code, "reset request statuses unknown")
	deliveries = waitForRecoveryDeliveries(t, helper, 2)
	require.Len(t, deliveries, 2, "password reset deliveries")
	resetToken := credentialFromDelivery(t, deliveries[1])
	require.NotContains(t, helper.Logs.String(), resetToken, "password reset token appeared in logs")
	resetComplete := performJSONRequest(
		helper.Handler(),
		http.MethodPost,
		"/api/v1/auth/password-reset/complete",
		map[string]any{"token": resetToken, "password": newPassword},
		"",
	)
	require.Equal(t, http.StatusNoContent, resetComplete.Code, "password reset completion status = %d: %s", resetComplete.Code, resetComplete.Body.String())
	deliveries = waitForRecoveryDeliveries(t, helper, 3)
	changedMessage := strings.ReplaceAll(string(deliveries[2].Data), "=\r\n", "")
	require.False(t, credentialPattern.MatchString(changedMessage), "password-changed notice contains a credential or lacks expected copy")
	require.Contains(t, changedMessage, "Password changed", "password-changed notice contains a credential or lacks expected copy")
	oldSession := performJSONRequest(
		helper.Handler(),
		http.MethodGet,
		"/api/v1/users/me",
		nil,
		login.Tokens.AccessToken,
	)
	require.Equal(t, http.StatusUnauthorized, oldSession.Code, "pre-reset session status")
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
	require.Equal(t, http.StatusUnauthorized, oldLogin.Code, "old password login status")
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
	require.Equal(t, http.StatusOK, newLogin.Code, "new password login status = %d: %s", newLogin.Code, newLogin.Body.String())
	resetReplay := performJSONRequest(
		helper.Handler(),
		http.MethodPost,
		"/api/v1/auth/password-reset/complete",
		map[string]any{"token": resetToken, "password": newPassword},
		"",
	)
	require.Equal(t, http.StatusBadRequest, resetReplay.Code, "password reset replay status")
	audits, err := persistence.Audit().List(context.Background(), store.AuditListOptions{
		Action:     "authentication.password_reset.complete",
		Limit:      10,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	require.NoError(t, err, "password reset audits")
	require.Len(t, audits, 1, "password reset audits")
	require.Equal(t, user.ID.String(), audits[0].Resource.ID, "password reset audits")
	require.Equal(t, institution.ID.String(), audits[0].ScopeID, "password reset audits")
	encodedAudits, err := json.Marshal(audits)
	require.NoError(t, err)
	for _, password := range []string{oldPassword, newPassword} {
		require.NotContains(t, string(encodedAudits), password, "password appeared in password-reset audit")
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
		require.Equal(t, want, response.Code, "unknown reset attempt %d status", attempt)
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
	bootstrap, appErr := helper.App.BootstrapInstallation(context.Background(), app.Invocation{}, app.BootstrapInstallationCommand{
		InstitutionName: "policy-recovery", InstitutionDisplayName: "Policy Recovery",
		AdministratorUsername: "policy-recovery-admin", AdministratorEmail: "policy-recovery-admin@example.edu",
		AdministratorLocale: "en", AdministratorTimezone: "UTC",
		Password: "bootstrap correct horse battery staple", BootstrapSecret: testlib.BootstrapSecret, Source: "127.0.0.1:1",
	})
	require.NoError(t, appErr, "bootstrap")
	require.NotNil(t, bootstrap, "bootstrap")
	const password = "correct horse battery staple"
	user, appErr := helper.App.CreateLocalUser(context.Background(), app.CreateLocalUserCommand{User: &model.User{
		Username: "policy-recovery-user", Email: "policy-recovery-user@example.edu",
	}, Password: password})
	require.NoError(t, appErr)
	startIntegrationServer(t, helper)
	issued := performJSONRequest(helper.Handler(), http.MethodPost, "/api/v1/auth/password-reset/request",
		map[string]any{"email": user.Email}, "")
	require.Equal(t, http.StatusAccepted, issued.Code, "initial reset status")
	resetToken := credentialFromDelivery(t, waitForRecoveryDeliveries(t, helper, 1)[0])
	_, err := persistence.GetMaster().Exec(context.Background(),
		`UPDATE access_policies SET local_login_enabled=FALSE,
		 invitation_local_credential_enabled=FALSE WHERE singleton=1`)
	require.NoError(t, err)
	login := performJSONRequest(helper.Handler(), http.MethodPost, "/api/v1/auth/login", map[string]any{
		"login_id": user.Email, "password": password, "client_type": model.SessionClientWeb,
	}, "")
	require.Equal(t, http.StatusUnauthorized, login.Code, "disabled local login status=%d: %s", login.Code, login.Body.String())
	hidden := performJSONRequest(helper.Handler(), http.MethodPost, "/api/v1/auth/password-reset/request",
		map[string]any{"email": user.Email}, "")
	require.Equal(t, http.StatusAccepted, hidden.Code, "disabled reset status")
	require.Len(t, helper.Mailer.Deliveries(), 1, "disabled reset status")
	completed := performJSONRequest(helper.Handler(), http.MethodPost, "/api/v1/auth/password-reset/complete",
		map[string]any{"token": resetToken, "password": "new correct horse battery staple"}, "")
	require.Equal(t, http.StatusBadRequest, completed.Code, "disabled reset completion status=%d: %s", completed.Code, completed.Body.String())
	retained, err := persistence.UserToken().GetByHash(context.Background(), model.HashToken(resetToken), model.UserTokenPasswordReset)
	require.NoError(t, err, "disabled reset retained token=%#v err", retained)
	require.False(t, retained.ConsumedAt.Valid, "disabled reset retained token=%#v err", retained)
}

func requireAuthenticationDatabase(t *testing.T) string {
	t.Helper()
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	require.NotEmpty(t, dataSource, "PROCTOR_TEST_DATABASE_URL is not set")
	return dataSource
}

var credentialPattern = regexp.MustCompile(
	`token(?:=3D|=)([A-Za-z0-9_-]{43})`,
)

func credentialFromDelivery(t *testing.T, delivery mail.Delivery) string {
	t.Helper()
	message := strings.ReplaceAll(string(delivery.Data), "=\r\n", "")
	match := credentialPattern.FindStringSubmatch(message)
	require.Len(t, match, 2, "credential link missing from delivery")
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
		require.False(t, time.Now().After(deadline), "mail deliveries = %d, want at least %d", len(deliveries), count)
		time.Sleep(20 * time.Millisecond)
	}
}
