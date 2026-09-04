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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/testlib"
)

func TestPersonalAccessTokenSecurityNoticeIntegration(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	if dataSource == "" {
		t.Fatal("PROCTOR_TEST_DATABASE_URL is not set")
	}
	persistence := openAuthenticationStore(t, dataSource)
	helper := testlib.Setup(t, testlib.WithStore(persistence))
	bootstrap := performJSONRequest(helper.Handler(), http.MethodPost, "/api/v1/bootstrap", map[string]any{
		"bootstrap_secret": testlib.BootstrapSecret,
		"institution":      map[string]any{"name": "northbridge", "display_name": "Northbridge University"},
		"administrator":    map[string]any{"username": "pat-admin", "email": "pat-admin@example.edu"},
		"password":         "bootstrap correct horse battery staple",
	}, "")
	if bootstrap.Code != http.StatusCreated {
		t.Fatalf("bootstrap = %d: %s", bootstrap.Code, bootstrap.Body.String())
	}
	startIntegrationServer(t, helper)
	login := loginIntegrationUser(t, helper.Handler(), "pat-admin", "bootstrap correct horse battery staple", model.SessionClientCLI, "pat-notices")

	description := "Reporting automation <safe>"
	create := performJSONRequest(helper.Handler(), http.MethodPost, "/api/v1/users/me/tokens", map[string]any{
		"description": description,
		"scopes":      []string{string(model.ActionClassView)},
		"expires_at":  time.Now().Add(24 * time.Hour).UnixMilli(),
	}, login.Tokens.AccessToken)
	if create.Code != http.StatusCreated {
		t.Fatalf("create PAT = %d: %s logs=%s", create.Code, create.Body.String(), helper.Logs.String())
	}
	var created struct {
		Token struct {
			ID string `json:"id"`
		} `json:"token"`
		Credential string `json:"credential"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Token.ID == "" || created.Credential == "" {
		t.Fatalf("created PAT = %#v", created)
	}
	persisted, err := persistence.PersonalAccessToken().Get(context.Background(), created.Token.ID)
	if err != nil {
		t.Fatal(err)
	}

	var disableGroup sync.WaitGroup
	disableResponses := make(chan *httptest.ResponseRecorder, 2)
	for range 2 {
		disableGroup.Add(1)
		go func() {
			defer disableGroup.Done()
			disableResponses <- performJSONRequest(helper.Handler(), http.MethodPost, "/api/v1/users/me/tokens/"+created.Token.ID+"/disable", nil, login.Tokens.AccessToken)
		}()
	}
	disableGroup.Wait()
	close(disableResponses)
	for response := range disableResponses {
		if response.Code != http.StatusOK {
			t.Fatalf("concurrent disable = %d: %s", response.Code, response.Body.String())
		}
	}

	for _, operation := range []struct {
		method string
		path   string
		status int
	}{
		{http.MethodPost, "/api/v1/users/me/tokens/" + created.Token.ID + "/enable", http.StatusOK},
		{http.MethodDelete, "/api/v1/users/me/tokens/" + created.Token.ID, http.StatusNoContent},
	} {
		response := performJSONRequest(helper.Handler(), operation.method, operation.path, nil, login.Tokens.AccessToken)
		if response.Code != operation.status {
			t.Fatalf("%s %s = %d: %s", operation.method, operation.path, response.Code, response.Body.String())
		}
	}

	keys := []model.MailTemplateKey{
		model.MailTemplateIdentityPersonalAccessTokenCreated,
		model.MailTemplateIdentityPersonalAccessTokenDisabled,
		model.MailTemplateIdentityPersonalAccessTokenEnabled,
		model.MailTemplateIdentityPersonalAccessTokenRevoked,
	}
	deadline := time.Now().Add(10 * time.Second)
	var deliveries []*model.MailDelivery
	for {
		deliveries, err = persistence.Mail().ListDeliveries(context.Background(), store.MailDeliveryListOptions{TemplateKeys: keys, Limit: 20})
		if err != nil {
			t.Fatal(err)
		}
		complete := len(deliveries) == len(keys)
		for _, delivery := range deliveries {
			complete = complete && delivery.State == model.MailDeliveryAccepted && len(delivery.EncryptedPayload) == 0
		}
		if complete {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("PAT notices did not reach accepted: %#v", deliveries)
		}
		time.Sleep(20 * time.Millisecond)
	}

	audits, err := persistence.Audit().List(context.Background(), store.AuditListOptions{Limit: 200, Visibility: store.AuditVisibilityScope{InstitutionWide: true}})
	if err != nil {
		t.Fatal(err)
	}
	encodedAudits, _ := json.Marshal(audits)
	patAuditCounts := map[string]int{}
	for _, event := range audits {
		if strings.HasPrefix(event.Action, "personal_access_token.") {
			patAuditCounts[event.Action]++
		}
	}
	for _, action := range []string{"personal_access_token.create", "personal_access_token.disable", "personal_access_token.enable", "personal_access_token.revoke"} {
		if patAuditCounts[action] != 1 {
			t.Fatalf("PAT terminal audits = %#v, want one %s", patAuditCounts, action)
		}
	}
	var preparations int
	if err := persistence.GetMaster().Get(context.Background(), &preparations, `SELECT COUNT(*) FROM personal_access_token_mutation_preparations`); err != nil || preparations != 0 {
		t.Fatalf("remaining PAT preparations=%d err=%v", preparations, err)
	}
	encodedDeliveries, _ := json.Marshal(deliveries)
	var encodedJobs []byte
	for _, delivery := range deliveries {
		job, jobErr := persistence.Job().Get(context.Background(), delivery.JobID)
		if jobErr != nil {
			t.Fatal(jobErr)
		}
		encoded, encodeErr := json.Marshal(job)
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		encodedJobs = append(encodedJobs, encoded...)
	}
	for _, secret := range []string{created.Credential, persisted.TokenHash} {
		if bytes.Contains(encodedAudits, []byte(secret)) || bytes.Contains(encodedDeliveries, []byte(secret)) ||
			bytes.Contains(encodedJobs, []byte(secret)) || strings.Contains(helper.Logs.String(), secret) {
			t.Fatal("PAT credential or hash leaked into durable metadata or logs")
		}
	}
	captured := helper.Mailer.Deliveries()
	if len(captured) != len(keys) {
		t.Fatalf("captured PAT notices = %d, want %d", len(captured), len(keys))
	}
	for _, message := range captured {
		if bytes.Contains(message.Data, []byte(created.Credential)) || bytes.Contains(message.Data, []byte(persisted.TokenHash)) ||
			bytes.Contains(message.Data, []byte(string(model.ActionClassView))) {
			t.Fatal("PAT credential, hash, or full scope appeared in a security notice")
		}
	}
}
