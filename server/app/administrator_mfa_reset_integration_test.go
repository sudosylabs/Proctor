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
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	server "github.com/sudosylabs/proctor/server"
	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/testlib"
)

func TestOfflineAdministratorMFAResetReconcilesAndRequiresReenrollment(t *testing.T) {
	ctx := context.Background()
	persistence := openAuthenticationStore(t, requireAuthenticationDatabase(t))
	helper := testlib.Setup(t, testlib.WithStore(persistence), testlib.WithConfig(func(cfg *config.Config) {
		cfg.Server.ListenAddress = "127.0.0.1:0"
		cfg.Authentication.MFA.Enabled = true
		cfg.Authentication.MFA.EncryptionKey = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{29}, 32))
	}))
	const password = "offline MFA existing correct horse battery staple"
	bootstrap := performJSONRequest(helper.Handler(), http.MethodPost, "/api/v1/bootstrap", map[string]any{
		"bootstrap_secret": testlib.BootstrapSecret,
		"institution":      map[string]any{"name": "offline-mfa", "display_name": "Offline MFA"},
		"administrator":    map[string]any{"username": "offline-mfa-admin", "email": "offline-mfa-admin@example.edu"},
		"password":         password,
	}, "")
	require.Equal(t, http.StatusCreated, bootstrap.Code, "bootstrap=%d %s", bootstrap.Code, bootstrap.Body.String())
	installation, err := persistence.Installation().Get(ctx)
	require.NoError(t, err)
	before, err := helper.App.Login(ctx, invocation.Invocation{}, app.LoginCommand{LoginID: "offline-mfa-admin", Password: password, ClientType: model.SessionClientCLI, DeviceID: "offline-old", Source: "127.0.0.1:1"})
	require.NoError(t, err)
	strengthenRoleAdministratorSession(t, helper.Handler(), before.Tokens.AccessToken)
	reset, err := helper.Server.ResetAdministratorMFA(ctx, server.AdministratorMFAResetCommand{InstitutionID: installation.InstitutionID.String(), UserID: installation.AdministratorUserID.String()})
	require.NoError(t, err, "reset")
	require.NotNil(t, reset, "reset")
	require.True(t, reset.ReenrollmentRequired, "reset")
	require.Equal(t, int64(1), reset.RecoveryGeneration, "reset")
	events, err := persistence.Audit().List(ctx, store.AuditListOptions{Action: "authentication.administrator_mfa_reset", Limit: 10, Visibility: store.AuditVisibilityScope{InstitutionWide: true}})
	require.NoError(t, err, "pre-start recovery audit")
	require.Empty(t, events, "pre-start recovery audit")
	startIntegrationServer(t, helper)
	events, err = persistence.Audit().List(ctx, store.AuditListOptions{Action: "authentication.administrator_mfa_reset", Limit: 10, Visibility: store.AuditVisibilityScope{InstitutionWide: true}})
	require.NoError(t, err, "reconciled recovery audit")
	require.Len(t, events, 1, "reconciled recovery audit")
	require.True(t, events[0].ActorID.IsZero(), "reconciled recovery audit")
	require.Equal(t, model.AuditStatusSuccess, events[0].Status, "reconciled recovery audit")
	old := performJSONRequest(helper.Handler(), http.MethodGet, "/api/v1/users/me", nil, before.Tokens.AccessToken)
	require.NotEqual(t, http.StatusOK, old.Code, "old Session remained usable after offline reset")
	_, err = helper.App.Login(ctx, invocation.Invocation{}, app.LoginCommand{LoginID: "offline-mfa-admin", Password: password, ClientType: model.SessionClientCLI, DeviceID: "offline-cli", Source: "127.0.0.1:2"})
	require.Error(t, err, "CLI login bypassed mandatory reenrollment")
	fresh, err := helper.App.Login(ctx, invocation.Invocation{}, app.LoginCommand{LoginID: "offline-mfa-admin", Password: password, ClientType: model.SessionClientWeb, DeviceID: "offline-web", Source: "127.0.0.1:3"})
	require.NoError(t, err, "restricted fresh Session")
	require.NotNil(t, fresh, "restricted fresh Session")
	require.NotNil(t, fresh.Session, "restricted fresh Session")
	require.True(t, fresh.Session.MFARecoveryRequired, "restricted fresh Session")
	require.Equal(t, int64(1), fresh.Session.AuthenticationGeneration, "restricted fresh Session")
	ordinary := performJSONRequest(helper.Handler(), http.MethodGet, "/api/v1/users/me", nil, fresh.Tokens.AccessToken)
	require.NotEqual(t, http.StatusOK, ordinary.Code, "restricted browser Session accessed ordinary account endpoint")
	status := performJSONRequest(helper.Handler(), http.MethodGet, "/api/v1/users/me/mfa", nil, fresh.Tokens.AccessToken)
	require.Equal(t, http.StatusOK, status.Code, "recovery status=%d %s", status.Code, status.Body.String())
	var state struct {
		Required bool `json:"mfa_recovery_required"`
	}
	err = json.Unmarshal(status.Body.Bytes(), &state)
	require.NoError(t, err, "recovery status=%s error", status.Body.String())
	require.True(t, state.Required, "recovery status=%s error", status.Body.String())
	strengthenRoleAdministratorSession(t, helper.Handler(), fresh.Tokens.AccessToken)
	recovery, err := persistence.MFA().GetRecoveryState(ctx, installation.AdministratorUserID)
	require.NoError(t, err, "activated recovery=%#v error", recovery)
	require.False(t, recovery.ReenrollmentRequired, "activated recovery=%#v error", recovery)
	require.Equal(t, int64(1), recovery.Generation, "activated recovery=%#v error", recovery)
	ordinary = performJSONRequest(helper.Handler(), http.MethodGet, "/api/v1/users/me", nil, fresh.Tokens.AccessToken)
	require.Equal(t, http.StatusOK, ordinary.Code, "post-enrollment account=%d %s", ordinary.Code, ordinary.Body.String())
}
