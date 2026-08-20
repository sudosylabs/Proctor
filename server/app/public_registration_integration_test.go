//go:build integration

// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/testlib"
)

func TestPublicLocalRegistrationRealGraph(t *testing.T) {
	dataSource := requireAuthenticationDatabase(t)
	persistence := openAuthenticationStore(t, dataSource)
	helper := testlib.Setup(t,
		testlib.WithStore(persistence),
		testlib.WithConfig(func(cfg *config.Config) {
			cfg.Server.PublicURL = "https://proctor.example.edu"
			cfg.Authentication.AccountRecovery.RateLimit.MaximumAttempts = 20
			cfg.Authentication.AccountRecovery.RateLimit.MaximumSourceAttempts = 100
		}),
	)
	_, err := helper.App.BootstrapInstallation(context.Background(), application.Invocation{}, application.BootstrapInstallationCommand{
		InstitutionName: "registration-university", InstitutionDisplayName: "Registration University",
		AdministratorUsername: "registration-admin", AdministratorEmail: "registration-admin@example.edu",
		Password: "bootstrap correct horse battery staple", BootstrapSecret: testlib.BootstrapSecret, Source: "127.0.0.1:1",
	})
	if err != nil {
		t.Fatal(err)
	}
	startIntegrationServer(t, helper)

	const email = "new.public.student@example.edu"
	const password = "registration correct horse battery staple"
	body := map[string]any{"username": "new.public.student", "email": email, "password": password}
	disabled := performJSONRequest(helper.Handler(), http.MethodPost, "/api/v1/auth/register", body, "")
	if disabled.Code != http.StatusForbidden || !strings.Contains(disabled.Body.String(), "authentication.registration.invitation_required") ||
		strings.Contains(disabled.Body.String(), email) {
		t.Fatalf("disabled registration = %d %s", disabled.Code, disabled.Body.String())
	}
	if _, getErr := persistence.User().GetByEmail(context.Background(), email); getErr == nil {
		t.Fatal("disabled registration persisted a User")
	}
	if _, err = persistence.GetMaster().Exec(context.Background(), `
		UPDATE access_policies
		   SET public_registration_enabled=TRUE, revision=revision+1, updated_at=clock_timestamp()
		 WHERE singleton=1 AND local_login_enabled=TRUE`); err != nil {
		t.Fatal(err)
	}

	accepted := performJSONRequest(helper.Handler(), http.MethodPost, "/api/v1/auth/register", body, "")
	replay := performJSONRequest(helper.Handler(), http.MethodPost, "/api/v1/auth/register", body, "")
	if accepted.Code != http.StatusAccepted || replay.Code != http.StatusAccepted || accepted.Body.Len() != 0 || replay.Body.Len() != 0 {
		t.Fatalf("registration statuses accepted=%d replay=%d bodies=%q/%q", accepted.Code, replay.Code, accepted.Body.String(), replay.Body.String())
	}
	user, err := persistence.User().GetByEmail(context.Background(), email)
	if err != nil || user.EmailVerified {
		t.Fatalf("registered User = %#v, %v", user, err)
	}
	var relationshipCount int
	if err = persistence.GetMaster().Get(context.Background(), &relationshipCount, `
		SELECT (SELECT count(*) FROM affiliations WHERE user_id=$1) +
		       (SELECT count(*) FROM academic_unit_members WHERE user_id=$1) +
		       (SELECT count(*) FROM class_members WHERE user_id=$1) +
		       (SELECT count(*) FROM role_bindings WHERE user_id=$1)`, user.ID.String()); err != nil {
		t.Fatal(err)
	}
	if relationshipCount != 0 {
		t.Fatalf("public registration granted %d relationships", relationshipCount)
	}

	deliveries := waitForRecoveryDeliveries(t, helper, 1)
	if len(deliveries) != 1 {
		t.Fatalf("registration deliveries = %d, want exactly one", len(deliveries))
	}
	token := credentialFromDelivery(t, deliveries[0])
	if logs := helper.Logs.String(); strings.Contains(logs, email) || strings.Contains(logs, password) || strings.Contains(logs, token) {
		t.Fatalf("registration secret or mailbox appeared in logs: %s", logs)
	}
	verification := performJSONRequest(helper.Handler(), http.MethodPost, "/api/v1/auth/email-verification/complete",
		map[string]any{"token": token}, "")
	if verification.Code != http.StatusNoContent {
		t.Fatalf("verification = %d %s", verification.Code, verification.Body.String())
	}
	login := performJSONRequest(helper.Handler(), http.MethodPost, "/api/v1/auth/login", map[string]any{
		"login_id": email, "password": password, "client_type": model.SessionClientCLI,
	}, "")
	if login.Code != http.StatusOK {
		t.Fatalf("registered login = %d %s", login.Code, login.Body.String())
	}
}
