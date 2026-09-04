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
	"strings"
	"testing"
	"time"

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
	if bootstrap.Code != http.StatusCreated {
		t.Fatalf("bootstrap = %d: %s", bootstrap.Code, bootstrap.Body.String())
	}
	var created struct {
		Institution struct {
			ID string `json:"id"`
		} `json:"institution"`
		Administrator struct {
			ID string `json:"id"`
		} `json:"administrator"`
	}
	if err := json.Unmarshal(bootstrap.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	before, err := primaryStore.PasswordCredential().GetByUser(context.Background(), created.Administrator.ID)
	if err != nil {
		t.Fatal(err)
	}

	serveCtx, cancelServing := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)
	go func() { serveDone <- primary.Server.Run(serveCtx) }()
	waitForReady := time.Now().Add(5 * time.Second)
	for !primary.Server.Ready() && time.Now().Before(waitForReady) {
		time.Sleep(5 * time.Millisecond)
	}
	if !primary.Server.Ready() {
		cancelServing()
		t.Fatalf("primary server did not become ready; logs=%s", primary.Logs.String())
	}

	database := config.Default().Database
	database.DataSource = dataSource
	secondaryStore, err := sqlstore.New(context.Background(), sqlstore.SettingsFromConfig(database))
	if err != nil {
		cancelServing()
		t.Fatal(err)
	}
	secondary := testlib.Setup(t, testlib.WithStore(secondaryStore), testlib.WithConfig(configureNode("recovery-node-inert")))

	const rejectedPassword = "must not commit while another node serves"
	if _, err = secondary.Server.RecoverAdministratorAccess(context.Background(), server.AdministratorRecoveryCommand{
		InstitutionID: created.Institution.ID, UserID: created.Administrator.ID, Password: rejectedPassword,
	}); err == nil {
		cancelServing()
		t.Fatal("offline recovery succeeded while another node was serving")
	}
	after, err := secondaryStore.PasswordCredential().GetByUser(context.Background(), created.Administrator.ID)
	if err != nil || after.PasswordHash != before.PasswordHash {
		cancelServing()
		t.Fatalf("credential after live-node rejection = %#v, %v", after, err)
	}
	reconciled, err := secondaryStore.Installation().ReconcileAdministratorRecovery(context.Background(), &store.AdministratorRecoveryReconciliation{NodeID: "recovery-node-inert"})
	if err != nil || reconciled == nil || reconciled.Reconciled != 0 {
		cancelServing()
		t.Fatalf("rejected recovery pending record = %#v, %v", reconciled, err)
	}

	cancelServing()
	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatalf("primary Server.Run() shutdown error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("primary server did not stop")
	}
	if result, err := secondary.Server.RecoverAdministratorAccess(context.Background(), server.AdministratorRecoveryCommand{
		InstitutionID: created.Institution.ID, UserID: created.Administrator.ID, Password: rejectedPassword,
	}); err != nil || result == nil || !result.PasswordRotated {
		t.Fatalf("recovery after serving node stopped = %#v, %v", result, err)
	}
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
	if bootstrap.Code != http.StatusCreated {
		t.Fatalf("bootstrap = %d: %s", bootstrap.Code, bootstrap.Body.String())
	}
	var created struct {
		Institution struct {
			ID string `json:"id"`
		} `json:"institution"`
		Administrator struct {
			ID string `json:"id"`
		} `json:"administrator"`
	}
	if err := json.Unmarshal(bootstrap.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	result, err := helper.Server.RecoverAdministratorAccess(context.Background(), server.AdministratorRecoveryCommand{
		InstitutionID: created.Institution.ID, UserID: created.Administrator.ID, Password: newPassword,
	})
	if err != nil || result == nil || !result.PasswordRotated || result.LocalLoginEnabled {
		t.Fatalf("RecoverAdministratorAccess() = %#v, %v", result, err)
	}
	if _, err := helper.Server.RecoverAdministratorAccess(context.Background(), server.AdministratorRecoveryCommand{
		InstitutionID: created.Institution.ID, UserID: created.Administrator.ID, Password: newPassword,
	}); err == nil {
		t.Fatal("repeated pending recovery succeeded")
	}
	events, err := persistence.Audit().List(context.Background(), store.AuditListOptions{
		Action: "authentication.administrator_recovery", Limit: 10,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	if err != nil || len(events) != 0 {
		t.Fatalf("pre-start audit events=%#v error=%v", events, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	startDone := make(chan error, 1)
	go func() { startDone <- helper.Server.Run(ctx) }()
	deadline := time.Now().Add(5 * time.Second)
	for !helper.Server.Ready() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !helper.Server.Ready() {
		cancel()
		t.Fatalf("server did not become ready; logs=%s", helper.Logs.String())
	}
	login := loginIntegrationUser(t, helper.Handler(), "offline-admin", newPassword, model.SessionClientCLI, "offline-recovery-cli")
	if login.User.ID.String() != created.Administrator.ID || login.Session == nil {
		t.Fatalf("normal post-recovery login = %#v", login)
	}
	oldLogin := performJSONRequest(helper.Handler(), http.MethodPost, "/api/v1/auth/login", map[string]any{
		"login_id": "offline-admin", "password": oldPassword, "client_type": model.SessionClientCLI,
	}, "")
	if oldLogin.Code == http.StatusOK {
		t.Fatal("old password authenticated after rotation")
	}
	events, err = persistence.Audit().List(context.Background(), store.AuditListOptions{
		Action: "authentication.administrator_recovery", Limit: 10,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	if err != nil || len(events) != 1 || !events[0].ActorID.IsZero() || events[0].Status != model.AuditStatusSuccess {
		t.Fatalf("reconciled audit events=%#v error=%v", events, err)
	}
	for _, secret := range []string{oldPassword, newPassword} {
		if strings.Contains(helper.Logs.String(), secret) || strings.Contains(string(events[0].Result), secret) {
			t.Fatal("administrator recovery secret appeared in logs or audit")
		}
	}
	cancel()
	select {
	case err := <-startDone:
		if err != nil {
			t.Fatalf("Server.Run() after cancellation = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not stop")
	}
}
