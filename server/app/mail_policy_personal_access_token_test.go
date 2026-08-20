// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

func TestPersonalAccessTokenSecurityNoticesRemainRelevantUntilTheirDeadline(t *testing.T) {
	t.Parallel()

	for _, key := range []model.MailTemplateKey{
		model.MailTemplateIdentityPersonalAccessTokenCreated,
		model.MailTemplateIdentityPersonalAccessTokenEnabled,
		model.MailTemplateIdentityPersonalAccessTokenDisabled,
		model.MailTemplateIdentityPersonalAccessTokenRevoked,
	} {
		key := key
		t.Run(string(key), func(t *testing.T) {
			t.Parallel()

			at := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
			delivery := &model.MailDelivery{
				ID: model.NewMailDeliveryID(), OccurrenceID: model.NewMailOccurrenceID(), JobID: model.NewJobID(),
				TargetUserID: model.NewUserID(), TemplateKey: key, TemplateDigest: strings.Repeat("a", 64),
				MaskedRecipient: "o***@example.test", State: model.MailDeliveryQueued,
				CreatedAt: at, UpdatedAt: at, MessageDate: at, Deadline: at.Add(24 * time.Hour),
				MessageID:        "<mail." + model.NewId() + "@example.test>",
				EncryptedPayload: json.RawMessage(`{"ciphertext":"secret"}`), Revision: 1,
			}
			got, err := evaluateMailDeliveryRelevance(context.Background(), delivery)
			if err != nil {
				t.Fatalf("evaluateMailDeliveryRelevance(%s): %v", key, err)
			}
			if got != mailDeliveryRelevant {
				t.Fatalf("evaluateMailDeliveryRelevance(%s) = %v, want relevant", key, got)
			}
		})
	}
}
