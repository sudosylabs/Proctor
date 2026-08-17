// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type recordingMailAPI struct {
	sends   int
	ids     []model.MailDeliveryID
	view    application.MailDeliveryView
	lists   int
	metrics int
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
func (m *recordingMailAPI) ListMailDeliveries(context.Context, application.Invocation, application.ListMailDeliveriesQuery) (application.MailDeliveryPage, error) {
	m.lists++
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
	cursor := encodeMailDeliveryCursor(mailDeliveryCursor{CreatedAt: at.Format(time.RFC3339Nano), ID: id.String()})
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

func TestMailMetricsProjectsOnlyBoundedOperationalDimensions(t *testing.T) {
	t.Parallel()
	fake := &recordingMailAPI{}
	result, err := (mailResourceModule{mail: fake}).getMetrics(operationRequest{request: &http.Request{}})
	if err != nil || result.status != http.StatusOK || fake.metrics != 1 {
		t.Fatalf("getMetrics() = %#v, %v calls=%d", result, err, fake.metrics)
	}
}
