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
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app"
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
	_, err := helper.App.BootstrapInstallation(context.Background(), app.Invocation{}, app.BootstrapInstallationCommand{
		InstitutionName: "registration-university", InstitutionDisplayName: "Registration University",
		AdministratorUsername: "registration-admin", AdministratorEmail: "registration-admin@example.edu",
		Password: "bootstrap correct horse battery staple", BootstrapSecret: testlib.BootstrapSecret, Source: "127.0.0.1:1",
	})
	require.NoError(t, err)
	startIntegrationServer(t, helper)

	const email = "new.public.student@example.edu"
	const password = "registration correct horse battery staple"
	body := map[string]any{
		"username": "new.public.student", "email": email, "first_name": "New",
		"last_name": "Student", "password": password,
	}
	disabled := performJSONRequest(helper.Handler(), http.MethodPost, "/api/v1/auth/register", body, "")
	require.Equal(t, http.StatusForbidden, disabled.Code, "disabled registration")
	require.Contains(t, disabled.Body.String(), "authentication.registration.invitation_required", "disabled registration")
	require.NotContains(t, disabled.Body.String(), email, "disabled registration")
	_, getErr := persistence.User().GetByEmail(context.Background(), email)
	require.Error(t, getErr, "disabled registration persisted a User")
	_, err = persistence.GetMaster().Exec(context.Background(), `
		UPDATE access_policies
		   SET public_registration_enabled=TRUE, revision=revision+1, updated_at=clock_timestamp()
		 WHERE singleton=1 AND local_login_enabled=TRUE`)
	require.NoError(t, err)

	accepted := performJSONRequest(helper.Handler(), http.MethodPost, "/api/v1/auth/register", body, "")
	replay := performJSONRequest(helper.Handler(), http.MethodPost, "/api/v1/auth/register", body, "")
	require.Equal(t, http.StatusAccepted, accepted.Code, "registration statuses accepted=%d replay=%d bodies=%q/%q", accepted.Code, replay.Code, accepted.Body.String(), replay.Body.String())
	require.Equal(t, http.StatusAccepted, replay.Code, "registration statuses accepted=%d replay=%d bodies=%q/%q", accepted.Code, replay.Code, accepted.Body.String(), replay.Body.String())
	require.Equal(t, 0, accepted.Body.Len(), "registration statuses accepted=%d replay=%d bodies=%q/%q", accepted.Code, replay.Code, accepted.Body.String(), replay.Body.String())
	require.Equal(t, 0, replay.Body.Len(), "registration statuses accepted=%d replay=%d bodies=%q/%q", accepted.Code, replay.Code, accepted.Body.String(), replay.Body.String())
	user, err := persistence.User().GetByEmail(context.Background(), email)
	require.NoError(t, err, "registered User = %#v", user)
	require.False(t, user.EmailVerified, "registered User = %#v", user)
	require.Equal(t, "New", user.FirstName, "registered User = %#v", user)
	require.Equal(t, "Student", user.LastName, "registered User = %#v", user)
	require.Empty(t, user.DisplayName, "registered User = %#v", user)
	var relationshipCount int
	err = persistence.GetMaster().Get(context.Background(), &relationshipCount, `
		SELECT (SELECT count(*) FROM affiliations WHERE user_id=$1) +
		       (SELECT count(*) FROM academic_unit_members WHERE user_id=$1) +
		       (SELECT count(*) FROM class_members WHERE user_id=$1) +
		       (SELECT count(*) FROM role_bindings WHERE user_id=$1)`, user.ID.String())
	require.NoError(t, err)
	require.Equal(t, 0, relationshipCount, "public registration granted")

	deliveries := waitForRecoveryDeliveries(t, helper, 1)
	require.Len(t, deliveries, 1, "registration deliveries")
	token := credentialFromDelivery(t, deliveries[0])
	logs := helper.Logs.String()
	require.NotContains(t, logs, email)
	require.NotContains(t, logs, password)
	require.NotContains(t, logs, token)
	verification := performJSONRequest(helper.Handler(), http.MethodPost, "/api/v1/auth/email-verification/complete",
		map[string]any{"token": token}, "")
	require.Equal(t, http.StatusNoContent, verification.Code, "verification = %d %s", verification.Code, verification.Body.String())
	login := performJSONRequest(helper.Handler(), http.MethodPost, "/api/v1/auth/login", map[string]any{
		"login_id": email, "password": password, "client_type": model.SessionClientCLI,
	}, "")
	require.Equal(t, http.StatusOK, login.Code, "registered login = %d %s", login.Code, login.Body.String())
}
