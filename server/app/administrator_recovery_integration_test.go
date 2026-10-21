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
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	server "github.com/sudosylabs/proctor/server"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/sqlstore"
	"github.com/sudosylabs/proctor/server/testlib"
)

func TestOfflineAdministratorRecoveryRejectsAnotherServingNode(t *testing.T) {
	dataSource := requireAuthenticationDatabase(t)
	primaryStore := openAuthenticationStore(t, dataSource)
	configureNode := func(nodeID string) func(*config.Config) {
		return func(cfg *config.Config) {
			cfg.Server.ListenAddress = "127.0.0.1:0"
			cfg.Cluster.NodeID = nodeID
		}
	}
	primary := testlib.Setup(t, testlib.WithStore(primaryStore), testlib.WithConfig(configureNode("serving-node-primary")))
	const oldPassword = "live node old correct horse battery staple"
	bootstrap := performJSONRequest(primary.Handler(), http.MethodPost, "/api/v1/bootstrap", map[string]any{
		"bootstrap_secret": testlib.BootstrapSecret,
		"institution":      map[string]any{"name": "live-node-recovery", "display_name": "Live Node Recovery"},
		"administrator":    map[string]any{"username": "live-node-admin", "email": "live-node-admin@example.edu"},
		"password":         oldPassword,
	}, "")
	require.Equal(t, http.StatusCreated, bootstrap.Code, "bootstrap = %d: %s", bootstrap.Code, bootstrap.Body.String())
	var created struct {
		Institution struct {
			ID string `json:"id"`
		} `json:"institution"`
		Administrator struct {
			ID string `json:"id"`
		} `json:"administrator"`
	}
	require.NoError(t, json.Unmarshal(bootstrap.Body.Bytes(), &created))
	before, err := primaryStore.PasswordCredential().GetByUser(context.Background(), model.UserID(created.Administrator.ID))
	require.NoError(t, err)

	serveCtx, cancelServing := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)
	go func() { serveDone <- primary.Server.Run(serveCtx) }()
	waitForReady := time.Now().Add(5 * time.Second)
	for !primary.Server.Ready() && time.Now().Before(waitForReady) {
		time.Sleep(5 * time.Millisecond)
	}
	if !primary.Server.Ready() {
		cancelServing()
		require.FailNow(t, fmt.Sprintf("primary server did not become ready; logs=%s", primary.Logs.String()))
	}

	database := config.Default().Database
	database.DataSource = dataSource
	secondaryStore, err := sqlstore.New(context.Background(), sqlstore.SettingsFromConfig(database))
	if err != nil {
		cancelServing()
		require.NoError(t, err)
	}
	secondary := testlib.Setup(t, testlib.WithStore(secondaryStore), testlib.WithConfig(configureNode("recovery-node-inert")))

	const rejectedPassword = "must not commit while another node serves"
	if _, err = secondary.Server.RecoverAdministratorAccess(context.Background(), server.AdministratorRecoveryCommand{
		InstitutionID: created.Institution.ID, UserID: created.Administrator.ID, Password: rejectedPassword,
	}); err == nil {
		cancelServing()
		require.FailNow(t, "offline recovery succeeded while another node was serving")
	}
	after, err := secondaryStore.PasswordCredential().GetByUser(context.Background(), model.UserID(created.Administrator.ID))
	if err != nil || after.PasswordHash != before.PasswordHash {
		cancelServing()
		require.FailNow(t, fmt.Sprintf("credential after live-node rejection = %#v, %v", after, err))
	}
	reconciled, err := secondaryStore.Installation().ReconcileAdministratorRecovery(context.Background(), &store.AdministratorRecoveryReconciliation{NodeID: "recovery-node-inert"})
	if err != nil || reconciled == nil || reconciled.Reconciled != 0 {
		cancelServing()
		require.FailNow(t, fmt.Sprintf("rejected recovery pending record = %#v, %v", reconciled, err))
	}

	cancelServing()
	select {
	case err := <-serveDone:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		require.FailNow(t, "primary server did not stop")
	}
	result, err := secondary.Server.RecoverAdministratorAccess(context.Background(), server.AdministratorRecoveryCommand{
		InstitutionID: created.Institution.ID, UserID: created.Administrator.ID, Password: rejectedPassword,
	})
	require.NoError(t, err, "recovery after serving node stopped")
	require.NotNil(t, result, "recovery after serving node stopped")
	require.True(t, result.PasswordRotated, "recovery after serving node stopped")
}

func TestOfflineAdministratorRecoveryReconcilesBeforeNormalAuthentication(t *testing.T) {
	dataSource := requireAuthenticationDatabase(t)
	persistence := openAuthenticationStore(t, dataSource)
	helper := testlib.Setup(t, testlib.WithStore(persistence), testlib.WithConfig(func(cfg *config.Config) {
		cfg.Server.ListenAddress = "127.0.0.1:0"
	}))
	const oldPassword = "old correct horse battery staple"
	const newPassword = "new correct horse battery staple"
	bootstrap := performJSONRequest(helper.Handler(), http.MethodPost, "/api/v1/bootstrap", map[string]any{
		"bootstrap_secret": testlib.BootstrapSecret,
		"institution":      map[string]any{"name": "offline-recovery", "display_name": "Offline Recovery"},
		"administrator":    map[string]any{"username": "offline-admin", "email": "offline-admin@example.edu"},
		"password":         oldPassword,
	}, "")
	require.Equal(t, http.StatusCreated, bootstrap.Code, "bootstrap = %d: %s", bootstrap.Code, bootstrap.Body.String())
	var created struct {
		Institution struct {
			ID string `json:"id"`
		} `json:"institution"`
		Administrator struct {
			ID string `json:"id"`
		} `json:"administrator"`
	}
	require.NoError(t, json.Unmarshal(bootstrap.Body.Bytes(), &created))
	result, err := helper.Server.RecoverAdministratorAccess(context.Background(), server.AdministratorRecoveryCommand{
		InstitutionID: created.Institution.ID, UserID: created.Administrator.ID, Password: newPassword,
	})
	require.NoError(t, err, "RecoverAdministratorAccess()")
	require.NotNil(t, result, "RecoverAdministratorAccess()")
	require.True(t, result.PasswordRotated, "RecoverAdministratorAccess()")
	require.False(t, result.LocalLoginEnabled, "RecoverAdministratorAccess()")
	_, err = helper.Server.RecoverAdministratorAccess(context.Background(), server.AdministratorRecoveryCommand{
		InstitutionID: created.Institution.ID, UserID: created.Administrator.ID, Password: newPassword,
	})
	require.Error(t, err, "repeated pending recovery succeeded")
	events, err := persistence.Audit().List(context.Background(), store.AuditListOptions{
		Action: "authentication.administrator_recovery", Limit: 10,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	require.NoError(t, err, "pre-start audit events")
	require.Empty(t, events, "pre-start audit events")

	ctx, cancel := context.WithCancel(context.Background())
	startDone := make(chan error, 1)
	go func() { startDone <- helper.Server.Run(ctx) }()
	deadline := time.Now().Add(5 * time.Second)
	for !helper.Server.Ready() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !helper.Server.Ready() {
		cancel()
		require.FailNow(t, fmt.Sprintf("server did not become ready; logs=%s", helper.Logs.String()))
	}
	login := loginIntegrationUser(t, helper.Handler(), "offline-admin", newPassword, model.SessionClientCLI, "offline-recovery-cli")
	require.Equal(t, created.Administrator.ID, login.User.ID.String())
	require.NotNil(t, login.Session)
	oldLogin := performJSONRequest(helper.Handler(), http.MethodPost, "/api/v1/auth/login", map[string]any{
		"login_id": "offline-admin", "password": oldPassword, "client_type": model.SessionClientCLI,
	}, "")
	require.NotEqual(t, http.StatusOK, oldLogin.Code, "old password authenticated after rotation")
	events, err = persistence.Audit().List(context.Background(), store.AuditListOptions{
		Action: "authentication.administrator_recovery", Limit: 10,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	require.NoError(t, err, "reconciled audit events")
	require.Len(t, events, 1, "reconciled audit events")
	require.True(t, events[0].ActorID.IsZero(), "reconciled audit events")
	require.Equal(t, model.AuditStatusSuccess, events[0].Status, "reconciled audit events")
	for _, secret := range []string{oldPassword, newPassword} {
		require.NotContains(t, helper.Logs.String(), secret, "administrator recovery secret appeared in logs or audit")
		require.NotContains(t, string(events[0].Result), secret, "administrator recovery secret appeared in logs or audit")
	}
	cancel()
	select {
	case err := <-startDone:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		require.FailNow(t, "server did not stop")
	}
}
