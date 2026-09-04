// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type recordingMailAPI struct {
	sends     int
	ids       []model.MailDeliveryID
	view      application.MailDeliveryView
	lists     int
	listQuery application.ListMailDeliveriesQuery
	metrics   int
	rekeys    []string
	rekeyIDs  []model.JobID
}

func (m *recordingMailAPI) GetMailKeyState(context.Context, application.Invocation) (application.MailKeyStateView, error) {
	return application.MailKeyStateView{PrimaryKeyID: "22222222222222222222222222222222",
		RequiredPrimaryKeyID: "22222222222222222222222222222222", Active: []application.MailPayloadKeyUsageView{{
			KeyID: "11111111111111111111111111111111", ActiveReferences: 7,
		}}}, nil
}

func (m *recordingMailAPI) StartMailRekey(_ context.Context, _ application.Invocation, retiringKeyID string) (application.MailRekeyView, error) {
	m.rekeys = append(m.rekeys, retiringKeyID)
	return application.MailRekeyView{JobID: model.NewJobID(), PrimaryKeyID: "22222222222222222222222222222222",
		RetiringKeyID: retiringKeyID, CreatedAt: time.Unix(100, 0).UTC()}, nil
}

func (m *recordingMailAPI) GetMailRekeyStatus(_ context.Context, _ application.Invocation, jobID model.JobID) (application.MailRekeyStatusView, error) {
	m.rekeyIDs = append(m.rekeyIDs, jobID)
	return application.MailRekeyStatusView{JobID: jobID, Status: model.JobStatusSucceeded,
		PrimaryKeyID: "22222222222222222222222222222222", RetiringKeyID: "11111111111111111111111111111111",
		CreatedAt: time.Unix(100, 0).UTC(), UpdatedAt: time.Unix(103, 0).UTC(), CompletedAt: model.OptionalTimeFrom(time.Unix(103, 0).UTC()),
		AttemptCount: 2, MaximumAttempts: 11, Processed: 9, Reencrypted: 8,
		Progress: &application.MailRekeyProgressView{Current: 4, Total: 9, Stage: "reencrypting"},
		Proof:    &application.MailRekeyProofView{RetirementSafe: true}}, nil
}

func (m *recordingMailAPI) GetMailMetrics(context.Context, application.Invocation) (application.MailMetricsSnapshot, error) {
	m.metrics++
	return application.MailMetricsSnapshot{HealthCode: application.MailHealthHealthy}, nil
}

func (m *recordingMailAPI) SendTestMail(context.Context, application.Invocation) (application.MailDeliveryView, error) {
	m.sends++
	return m.view, nil
}
func (m *recordingMailAPI) GetMailDelivery(_ context.Context, _ application.Invocation, id model.MailDeliveryID) (application.MailDeliveryView, error) {
	m.ids = append(m.ids, id)
	return m.view, nil
}
func (m *recordingMailAPI) ListMailDeliveries(_ context.Context, _ application.Invocation, query application.ListMailDeliveriesQuery) (application.MailDeliveryPage, error) {
	m.lists++
	m.listQuery = query
	return application.MailDeliveryPage{Items: []application.MailDeliveryView{m.view}}, nil
}
func (m *recordingMailAPI) CancelMailDelivery(_ context.Context, _ application.Invocation, id model.MailDeliveryID) (application.MailDeliveryView, error) {
	m.ids = append(m.ids, id)
	return m.view, nil
}
func (m *recordingMailAPI) RetryMailDelivery(_ context.Context, _ application.Invocation, id model.MailDeliveryID) (application.MailDeliveryView, error) {
	m.ids = append(m.ids, id)
	return m.view, nil
}

func TestMailResourceRejectsCallerSuppliedContentAndProjectsNoSecrets(t *testing.T) {
	at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	id := model.NewMailDeliveryID()
	fake := &recordingMailAPI{view: application.MailDeliveryView{ID: id, OccurrenceID: model.NewMailOccurrenceID(), TargetUserID: model.NewUserID(), TemplateKey: model.MailTemplateSystemTest, TemplateDigest: strings.Repeat("a", 64), MaskedRecipient: "o***@example.test", State: model.MailDeliveryQueued, CreatedAt: at, UpdatedAt: at, MessageDate: at, Deadline: at.Add(time.Hour), MessageID: "<mail." + id.String() + "@example.test>"}}
	module := mailResourceModule{mail: fake}
	request, _ := http.NewRequest(http.MethodPost, "/api/v1/mail/test", bytes.NewBufferString(`{"recipient":"attacker@example.test","body":"chosen"}`))
	if _, err := module.sendTest(operationRequest{request: request}); !application.Is(err, "request.invalid") || fake.sends != 0 {
		t.Fatalf("caller content accepted: %v sends=%d", err, fake.sends)
	}
	request, _ = http.NewRequest(http.MethodPost, "/api/v1/mail/test", nil)
	result, err := module.sendTest(operationRequest{request: request})
	if err != nil || result.status != http.StatusAccepted || fake.sends != 1 {
		t.Fatalf("empty test = %#v, %v", result, err)
	}
	response := mailResponse(fake.view)
	encoded := response.ID + response.MaskedRecipient + response.MessageID
	for _, forbidden := range []string{"operator@example.test", "ciphertext", "subject", "html", "body"} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("response leaked %q", forbidden)
		}
	}
}

func TestMailDeliveryListCursorFiltersAndMutationBodiesAreBounded(t *testing.T) {
	at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	id := model.NewMailDeliveryID()
	fake := &recordingMailAPI{view: application.MailDeliveryView{ID: id, OccurrenceID: model.NewMailOccurrenceID(), TargetUserID: model.NewUserID(), TemplateKey: model.MailTemplateSystemTest, TemplateDigest: strings.Repeat("a", 64), MaskedRecipient: "o***@example.test", State: model.MailDeliveryFailed, CreatedAt: at, UpdatedAt: at, MessageDate: at, Deadline: at.Add(time.Hour), MessageID: "<mail." + id.String() + "@example.test>"}}
	module := mailResourceModule{mail: fake}
	request, _ := http.NewRequest(http.MethodGet, "/api/v1/mail/deliveries?state=failed&template_key=system.mail_test&created_after=1&created_before=9999999999999&limit=1", nil)
	result, err := module.listDeliveries(operationRequest{request: request})
	if err != nil || result.status != http.StatusOK || fake.lists != 1 {
		t.Fatalf("list result=%#v err=%v lists=%d", result, err, fake.lists)
	}
	cursor, err := encodeMailDeliveryCursor(mailDeliveryCursor{CreatedAt: at.Format(time.RFC3339Nano), ID: id.String()})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeMailDeliveryCursor(cursor)
	if err != nil || decoded.ID != id.String() || decoded.CreatedAt != at.Format(time.RFC3339Nano) {
		t.Fatalf("cursor=%#v err=%v", decoded, err)
	}
	request, _ = http.NewRequest(http.MethodGet, "/api/v1/mail/deliveries?state=not-a-state", nil)
	if _, err = module.listDeliveries(operationRequest{request: request}); !application.Is(err, "mail.query.invalid") || fake.lists != 1 {
		t.Fatalf("invalid filter err=%v lists=%d", err, fake.lists)
	}
	request, _ = http.NewRequest(http.MethodPost, "/api/v1/mail/deliveries/"+id.String()+"/retry", bytes.NewBufferString(`{"body":"forbidden"}`))
	if _, err = module.retryDelivery(operationRequest{request: request, params: Params{MailDeliveryID: id.String()}}); !application.Is(err, "request.invalid") || len(fake.ids) != 0 {
		t.Fatalf("retry body err=%v ids=%v", err, fake.ids)
	}
}

func TestMailDeliveryEndpointForwardsExactCursorAndMapsMalformedCursor(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 24, 10, 11, 12, 345, time.UTC)
	id := model.NewMailDeliveryID()
	fake := &recordingMailAPI{view: application.MailDeliveryView{ID: model.NewMailDeliveryID(), CreatedAt: at.Add(-time.Minute)}}
	logger, _ := newTestLogger(t)
	httpAPI := newFocusedResourceAPI(t, logger, &academicUnitHTTPApplication{principal: operatorTestPrincipal()}, mailResource(fake))
	cursor, err := encodeMailDeliveryCursor(mailDeliveryCursor{CreatedAt: at.Format(time.RFC3339Nano), ID: id.String()})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/mail/deliveries?cursor="+url.QueryEscape(cursor), nil)
	request.Header.Set("Authorization", "Bearer credential")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !fake.listQuery.BeforeCreatedAt.Equal(at) || fake.listQuery.BeforeID != id {
		t.Fatalf("cursor forwarding = %d query=%#v body=%s", response.Code, fake.listQuery, response.Body.String())
	}

	malformed := httptest.NewRequest(http.MethodGet, "/api/v1/mail/deliveries?cursor=not-a-cursor", nil)
	malformed.Header.Set("Authorization", "Bearer credential")
	malformedResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(malformedResponse, malformed)
	assertHTTPProblem(t, malformedResponse, http.StatusBadRequest, "mail.query.invalid")
}

func TestMailMetricsProjectsOnlyBoundedOperationalDimensions(t *testing.T) {
	t.Parallel()
	fake := &recordingMailAPI{}
	result, err := (mailResourceModule{mail: fake}).getMetrics(operationRequest{request: &http.Request{}})
	if err != nil || result.status != http.StatusOK || fake.metrics != 1 {
		t.Fatalf("getMetrics() = %#v, %v calls=%d", result, err, fake.metrics)
	}
}

func TestMailRekeyAcceptsOnlyAKeyIdentityAndProjectsSafeOperation(t *testing.T) {
	t.Parallel()
	fake := &recordingMailAPI{}
	module := mailResourceModule{mail: fake}
	request, _ := http.NewRequest(http.MethodPost, "/api/v1/mail/rekey",
		bytes.NewBufferString(`{"retiring_key_id":"11111111111111111111111111111111"}`))
	result, err := module.startRekey(operationRequest{request: request})
	if err != nil || result.status != http.StatusAccepted || len(fake.rekeys) != 1 ||
		fake.rekeys[0] != "11111111111111111111111111111111" {
		t.Fatalf("startRekey() = %#v, %v calls=%#v", result, err, fake.rekeys)
	}
	response, ok := result.body.(mailRekeyResponse)
	if !ok || response.JobID == "" || response.PrimaryKeyID != "22222222222222222222222222222222" ||
		response.RetiringKeyID != fake.rekeys[0] || response.CreatedAt != 100000 {
		t.Fatalf("safe rekey response = %#v", result.body)
	}
	invalid, _ := http.NewRequest(http.MethodPost, "/api/v1/mail/rekey",
		bytes.NewBufferString(`{"retiring_key_id":"11111111111111111111111111111111","encryption_key":"secret"}`))
	if _, err = module.startRekey(operationRequest{request: invalid}); !application.Is(err, "request.invalid") || len(fake.rekeys) != 1 {
		t.Fatalf("secret-bearing rekey body error = %v calls=%#v", err, fake.rekeys)
	}
	stateResult, err := module.getKeyState(operationRequest{request: &http.Request{}})
	state, ok := stateResult.body.(mailKeyStateResponse)
	if err != nil || !ok || state.PrimaryKeyID != "22222222222222222222222222222222" ||
		state.RequiredPrimaryKeyID != state.PrimaryKeyID || len(state.Active) != 1 ||
		state.Active[0].KeyID != "11111111111111111111111111111111" || state.Active[0].ActiveReferences != 7 {
		t.Fatalf("key state response = %#v, %v", stateResult.body, err)
	}
}

func TestMailRekeyStatusProjectsSafeProgressAndFinalProof(t *testing.T) {
	t.Parallel()
	jobID := model.NewJobID()
	fake := &recordingMailAPI{}
	result, err := (mailResourceModule{mail: fake}).getRekeyStatus(operationRequest{
		request: &http.Request{}, params: Params{JobID: jobID.String()},
	})
	if err != nil || result.status != http.StatusOK || len(fake.rekeyIDs) != 1 || fake.rekeyIDs[0] != jobID {
		t.Fatalf("getRekeyStatus() = %#v, %v ids=%#v", result, err, fake.rekeyIDs)
	}
	response, ok := result.body.(mailRekeyStatusResponse)
	if !ok || response.JobID != jobID.String() || response.Status != model.JobStatusSucceeded ||
		response.Progress == nil || response.Progress.Current != 4 || response.Progress.Total != 9 ||
		response.Processed != 9 || response.Reencrypted != 8 || response.Proof == nil || !response.Proof.RetirementSafe ||
		response.Proof.NonPrimaryReferences != 0 || response.Proof.RetiringReferences != 0 {
		t.Fatalf("safe rekey status response = %#v", result.body)
	}
	encoded, _ := json.Marshal(response)
	for _, forbidden := range []string{"ciphertext", "recipient", "encryption_key", "command", "checkpoint", "result"} {
		if bytes.Contains(encoded, []byte(forbidden)) {
			t.Fatalf("rekey status leaked %q: %s", forbidden, encoded)
		}
	}
}
