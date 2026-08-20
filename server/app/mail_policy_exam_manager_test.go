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

func TestExamManagerNoticesRemainRelevantAsHistoricalFacts(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	for _, key := range []model.MailTemplateKey{
		model.MailTemplateExamManagerAdded,
		model.MailTemplateExamManagerRemoved,
		model.MailTemplateExamOwnershipTransferredToYou,
		model.MailTemplateExamOwnershipTransferredFromYou,
	} {
		delivery := &model.MailDelivery{
			ID: model.NewMailDeliveryID(), OccurrenceID: model.NewMailOccurrenceID(), JobID: model.NewJobID(),
			TargetUserID: model.NewUserID(), TemplateKey: key, TemplateDigest: strings.Repeat("a", 64),
			MaskedRecipient: "m***@example.test", State: model.MailDeliveryQueued,
			CreatedAt: at, UpdatedAt: at, MessageDate: at, Deadline: at.Add(72 * time.Hour),
			MessageID: "<mail." + model.NewId() + "@example.test>", EncryptedPayload: json.RawMessage(`{"ciphertext":"secret"}`), Revision: 1,
		}
		got, err := evaluateMailDeliveryRelevance(context.Background(), delivery)
		if err != nil {
			t.Fatalf("evaluateMailDeliveryRelevance(%q): %v", key, err)
		}
		if got != mailDeliveryRelevant {
			t.Fatalf("evaluateMailDeliveryRelevance(%q) = %v", key, got)
		}
	}
}
