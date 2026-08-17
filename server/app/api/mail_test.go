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
	sends int
	ids   []model.MailDeliveryID
	view  application.MailDeliveryView
}

func (m *recordingMailAPI) SendTestMail(context.Context, application.Invocation) (application.MailDeliveryView, error) {
	m.sends++
	return m.view, nil
}
func (m *recordingMailAPI) GetMailDelivery(_ context.Context, _ application.Invocation, id model.MailDeliveryID) (application.MailDeliveryView, error) {
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
