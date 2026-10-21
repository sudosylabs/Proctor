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

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/model"
)

type recordingMailAPI struct {
	sends     int
	ids       []model.MailDeliveryID
	view      app.MailDeliveryView
	lists     int
	listQuery app.ListMailDeliveriesQuery
	metrics   int
	rekeys    []string
	rekeyIDs  []model.JobID
}

func (m *recordingMailAPI) GetMailKeyState(context.Context, app.Invocation) (app.MailKeyStateView, error) {
	return app.MailKeyStateView{PrimaryKeyID: "22222222222222222222222222222222",
		RequiredPrimaryKeyID: "22222222222222222222222222222222", Active: []app.MailPayloadKeyUsageView{{
			KeyID: "11111111111111111111111111111111", ActiveReferences: 7,
		}}}, nil
}

func (m *recordingMailAPI) StartMailRekey(_ context.Context, _ app.Invocation, retiringKeyID string) (app.MailRekeyView, error) {
	m.rekeys = append(m.rekeys, retiringKeyID)
	return app.MailRekeyView{JobID: model.NewJobID(), PrimaryKeyID: "22222222222222222222222222222222",
		RetiringKeyID: retiringKeyID, CreatedAt: time.Unix(100, 0).UTC()}, nil
}

func (m *recordingMailAPI) GetMailRekeyStatus(_ context.Context, _ app.Invocation, jobID model.JobID) (app.MailRekeyStatusView, error) {
	m.rekeyIDs = append(m.rekeyIDs, jobID)
	return app.MailRekeyStatusView{JobID: jobID, Status: model.JobStatusSucceeded,
		PrimaryKeyID: "22222222222222222222222222222222", RetiringKeyID: "11111111111111111111111111111111",
		CreatedAt: time.Unix(100, 0).UTC(), UpdatedAt: time.Unix(103, 0).UTC(), CompletedAt: model.OptionalTimeFrom(time.Unix(103, 0).UTC()),
		AttemptCount: 2, MaximumAttempts: 11, Processed: 9, Reencrypted: 8,
		Progress: &app.MailRekeyProgressView{Current: 4, Total: 9, Stage: "reencrypting"},
		Proof:    &app.MailRekeyProofView{RetirementSafe: true}}, nil
}

func (m *recordingMailAPI) GetMailMetrics(context.Context, app.Invocation) (app.MailMetricsSnapshot, error) {
	m.metrics++
	return app.MailMetricsSnapshot{HealthCode: app.MailHealthHealthy}, nil
}

func (m *recordingMailAPI) SendTestMail(context.Context, app.Invocation) (app.MailDeliveryView, error) {
	m.sends++
	return m.view, nil
}
func (m *recordingMailAPI) GetMailDelivery(_ context.Context, _ app.Invocation, id model.MailDeliveryID) (app.MailDeliveryView, error) {
	m.ids = append(m.ids, id)
	return m.view, nil
}
func (m *recordingMailAPI) ListMailDeliveries(_ context.Context, _ app.Invocation, query app.ListMailDeliveriesQuery) (app.MailDeliveryPage, error) {
	m.lists++
	m.listQuery = query
	return app.MailDeliveryPage{Items: []app.MailDeliveryView{m.view}}, nil
}
func (m *recordingMailAPI) CancelMailDelivery(_ context.Context, _ app.Invocation, id model.MailDeliveryID) (app.MailDeliveryView, error) {
	m.ids = append(m.ids, id)
	return m.view, nil
}
func (m *recordingMailAPI) RetryMailDelivery(_ context.Context, _ app.Invocation, id model.MailDeliveryID) (app.MailDeliveryView, error) {
	m.ids = append(m.ids, id)
	return m.view, nil
}

func TestMailResourceRejectsCallerSuppliedContentAndProjectsNoSecrets(t *testing.T) {
	at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	id := model.NewMailDeliveryID()
	fake := &recordingMailAPI{view: app.MailDeliveryView{ID: id, OccurrenceID: model.NewMailOccurrenceID(), TargetUserID: model.NewUserID(), TemplateKey: model.MailTemplateSystemTest, TemplateDigest: strings.Repeat("a", 64), MaskedRecipient: "o***@example.test", State: model.MailDeliveryQueued, CreatedAt: at, UpdatedAt: at, MessageDate: at, Deadline: at.Add(time.Hour), MessageID: "<mail." + id.String() + "@example.test>"}}
	module := mailResourceModule{mail: fake}
	request, _ := http.NewRequest(http.MethodPost, "/api/v1/mail/test", bytes.NewBufferString(`{"recipient":"attacker@example.test","body":"chosen"}`))
	_, err := module.sendTest(operationRequest{request: request})
	require.True(t, apperror.Is(err, "request.invalid"), "caller content accepted: %v sends", err)
	require.Equal(t, 0, fake.sends, "caller content accepted: %v sends", err)
	request, _ = http.NewRequest(http.MethodPost, "/api/v1/mail/test", nil)
	result, err := module.sendTest(operationRequest{request: request})
	require.NoError(t, err, "empty test = %#v", result)
	require.Equal(t, http.StatusAccepted, result.status, "empty test = %#v", result)
	require.Equal(t, 1, fake.sends, "empty test = %#v", result)
	response := mailResponse(fake.view)
	encoded := response.ID + response.MaskedRecipient + response.MessageID
	for _, forbidden := range []string{"operator@example.test", "ciphertext", "subject", "html", "body"} {
		require.NotContains(t, encoded, forbidden)
	}
}

func TestMailDeliveryListCursorFiltersAndMutationBodiesAreBounded(t *testing.T) {
	at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	id := model.NewMailDeliveryID()
	fake := &recordingMailAPI{view: app.MailDeliveryView{ID: id, OccurrenceID: model.NewMailOccurrenceID(), TargetUserID: model.NewUserID(), TemplateKey: model.MailTemplateSystemTest, TemplateDigest: strings.Repeat("a", 64), MaskedRecipient: "o***@example.test", State: model.MailDeliveryFailed, CreatedAt: at, UpdatedAt: at, MessageDate: at, Deadline: at.Add(time.Hour), MessageID: "<mail." + id.String() + "@example.test>"}}
	module := mailResourceModule{mail: fake}
	request, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/mail/deliveries?state=failed&template_key=system.mail_test&created_after=1970-01-01T00:00:00.001Z&created_before=2286-11-20T17:46:39.999Z&limit=1", nil)
	result, err := module.listDeliveries(operationRequest{request: request})
	require.NoError(t, err, "list result=%#v err=%v lists", result, err)
	require.Equal(t, http.StatusOK, result.status, "list result=%#v err=%v lists", result, err)
	require.Equal(t, 1, fake.lists, "list result=%#v err=%v lists", result, err)
	cursor, err := encodeMailDeliveryCursor(mailDeliveryCursor{CreatedAt: at.Format(time.RFC3339Nano), ID: id.String()})
	require.NoError(t, err)
	decoded, err := decodeMailDeliveryCursor(cursor)
	require.NoError(t, err, "cursor=%#v err", decoded)
	require.Equal(t, id.String(), decoded.ID, "cursor=%#v err", decoded)
	require.Equal(t, at.Format(time.RFC3339Nano), decoded.CreatedAt, "cursor=%#v err", decoded)
	request, _ = http.NewRequest(http.MethodGet, "/api/v1/mail/deliveries?state=not-a-state", nil)
	_, err = module.listDeliveries(operationRequest{request: request})
	require.True(t, apperror.Is(err, "request.invalid"), "invalid filter err=%v lists", err)
	require.Equal(t, 1, fake.lists, "invalid filter err=%v lists", err)
	request, _ = http.NewRequest(http.MethodPost, "/api/v1/mail/deliveries/"+id.String()+"/retry", bytes.NewBufferString(`{"body":"forbidden"}`))
	_, err = module.retryDelivery(operationRequest{request: request, params: Params{MailDeliveryID: id.String()}})
	require.True(t, apperror.Is(err, "request.invalid"), "retry body err=%v ids", err)
	require.Empty(t, fake.ids, "retry body err=%v ids", err)
}

func TestMailDeliveryEndpointForwardsExactCursorAndMapsMalformedCursor(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 24, 10, 11, 12, 345000, time.UTC)
	id := model.NewMailDeliveryID()
	fake := &recordingMailAPI{view: app.MailDeliveryView{ID: model.NewMailDeliveryID(), CreatedAt: at.Add(-time.Minute)}}
	logger, _ := newTestLogger(t)
	httpAPI := newFocusedResourceAPI(t, logger, &academicUnitHTTPApplication{principal: operatorTestPrincipal()}, mailResource(fake))
	cursor, err := encodeMailDeliveryCursor(mailDeliveryCursor{CreatedAt: at.Format(time.RFC3339Nano), ID: id.String()})
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/mail/deliveries?cursor="+url.QueryEscape(cursor), nil)
	request.Header.Set("Authorization", "Bearer credential")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, "cursor forwarding = %d query=%#v body=%s", response.Code, fake.listQuery, response.Body.String())
	require.True(t, fake.listQuery.BeforeCreatedAt.Equal(at), "cursor forwarding = %d query=%#v body=%s", response.Code, fake.listQuery, response.Body.String())
	require.Equal(t, id, fake.listQuery.BeforeID, "cursor forwarding = %d query=%#v body=%s", response.Code, fake.listQuery, response.Body.String())

	malformed := httptest.NewRequest(http.MethodGet, "/api/v1/mail/deliveries?cursor=not-a-cursor", nil)
	malformed.Header.Set("Authorization", "Bearer credential")
	malformedResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(malformedResponse, malformed)
	assertHTTPProblem(t, malformedResponse, http.StatusBadRequest, "request.invalid")
}

func TestMailMetricsProjectsOnlyBoundedOperationalDimensions(t *testing.T) {
	t.Parallel()
	fake := &recordingMailAPI{}
	result, err := (mailResourceModule{mail: fake}).getMetrics(operationRequest{request: &http.Request{}})
	require.NoError(t, err, "getMetrics() = %#v, %v calls", result, err)
	require.Equal(t, http.StatusOK, result.status, "getMetrics() = %#v, %v calls", result, err)
	require.Equal(t, 1, fake.metrics, "getMetrics() = %#v, %v calls", result, err)
}

func TestMailRekeyAcceptsOnlyAKeyIdentityAndProjectsSafeOperation(t *testing.T) {
	t.Parallel()
	fake := &recordingMailAPI{}
	module := mailResourceModule{mail: fake}
	request, _ := http.NewRequest(http.MethodPost, "/api/v1/mail/rekey",
		bytes.NewBufferString(`{"retiring_key_id":"11111111111111111111111111111111"}`))
	request.Header.Set("Content-Type", "application/json")
	result, err := module.startRekey(operationRequest{request: request})
	require.NoError(t, err, "startRekey() = %#v, %v calls", result, err)
	require.Equal(t, http.StatusAccepted, result.status, "startRekey() = %#v, %v calls", result, err)
	require.Len(t, fake.rekeys, 1, "startRekey() = %#v, %v calls", result, err)
	require.Equal(t, "11111111111111111111111111111111", fake.rekeys[0], "startRekey() = %#v, %v calls", result, err)
	response, ok := result.body.(mailRekeyResponse)
	require.True(t, ok)
	require.NotEmpty(t, response.JobID)
	require.Equal(t, "22222222222222222222222222222222", response.PrimaryKeyID)
	require.Equal(t, fake.rekeys[0], response.RetiringKeyID)
	require.Equal(t, "1970-01-01T00:01:40Z", response.CreatedAt)
	invalid, _ := http.NewRequest(http.MethodPost, "/api/v1/mail/rekey",
		bytes.NewBufferString(`{"retiring_key_id":"11111111111111111111111111111111","encryption_key":"secret"}`))
	invalid.Header.Set("Content-Type", "application/json")
	_, err = module.startRekey(operationRequest{request: invalid})
	require.True(t, apperror.Is(err, "request.invalid"), "secret-bearing rekey body error = %v calls", err)
	require.Len(t, fake.rekeys, 1, "secret-bearing rekey body error = %v calls", err)
	stateResult, err := module.getKeyState(operationRequest{request: &http.Request{}})
	state, ok := stateResult.body.(mailKeyStateResponse)
	require.NoError(t, err, "key state response = %#v", stateResult.body)
	require.True(t, ok, "key state response = %#v", stateResult.body)
	require.Equal(t, "22222222222222222222222222222222", state.PrimaryKeyID, "key state response = %#v", stateResult.body)
	require.Equal(t, state.PrimaryKeyID, state.RequiredPrimaryKeyID, "key state response = %#v", stateResult.body)
	require.Len(t, state.Active, 1, "key state response = %#v", stateResult.body)
	require.Equal(t, "11111111111111111111111111111111", state.Active[0].KeyID, "key state response = %#v", stateResult.body)
	require.Equal(t, int64(7), state.Active[0].ActiveReferences, "key state response = %#v", stateResult.body)
}

func TestMailRekeyStatusProjectsSafeProgressAndFinalProof(t *testing.T) {
	t.Parallel()
	jobID := model.NewJobID()
	fake := &recordingMailAPI{}
	result, err := (mailResourceModule{mail: fake}).getRekeyStatus(operationRequest{
		request: &http.Request{}, params: Params{JobID: jobID.String()},
	})
	require.NoError(t, err, "getRekeyStatus() = %#v, %v ids", result, err)
	require.Equal(t, http.StatusOK, result.status, "getRekeyStatus() = %#v, %v ids", result, err)
	require.Len(t, fake.rekeyIDs, 1, "getRekeyStatus() = %#v, %v ids", result, err)
	require.Equal(t, jobID, fake.rekeyIDs[0], "getRekeyStatus() = %#v, %v ids", result, err)
	response, ok := result.body.(mailRekeyStatusResponse)
	require.True(t, ok)
	require.Equal(t, jobID.String(), response.JobID)
	require.Equal(t, model.JobStatusSucceeded, response.Status)
	require.NotNil(t, response.Progress)
	require.Equal(t, int64(4), response.Progress.Current)
	require.Equal(t, int64(9), response.Progress.Total)
	require.Equal(t, int64(9), response.Processed)
	require.Equal(t, int64(8), response.Reencrypted)
	require.NotNil(t, response.Proof)
	require.True(t, response.Proof.RetirementSafe)
	require.Equal(t, int64(0), response.Proof.NonPrimaryReferences)
	require.Equal(t, int64(0), response.Proof.RetiringReferences)
	encoded, _ := json.Marshal(response)
	for _, forbidden := range []string{"ciphertext", "recipient", "encryption_key", "command", "checkpoint", "result"} {
		require.False(t, bytes.Contains(encoded, []byte(forbidden)), "rekey status leaked %q: %s", forbidden, encoded)
	}
}
