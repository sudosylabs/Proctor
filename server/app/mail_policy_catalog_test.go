// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

// TestEveryCatalogTemplateHasADeliveryRelevancePolicy walks the closed mail
// catalog the composer prepares from, so a template key added without a
// relevance case fails here instead of exhausting its delivery Job.
func TestEveryCatalogTemplateHasADeliveryRelevancePolicy(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	keys := model.AllMailTemplateKeys()
	require.NotEmpty(t, keys)
	for _, key := range keys {
		t.Run(string(key), func(t *testing.T) {
			t.Parallel()
			delivery := &model.MailDelivery{
				ID: model.NewMailDeliveryID(), OccurrenceID: model.NewMailOccurrenceID(), JobID: model.NewJobID(),
				TargetUserID: model.NewUserID(), TemplateKey: key, TemplateDigest: strings.Repeat("a", 64),
				MaskedRecipient: "m***@example.test", State: model.MailDeliveryQueued,
				CreatedAt: at, UpdatedAt: at, MessageDate: at, Deadline: at.Add(24 * time.Hour),
				MessageID: "<mail." + model.NewID() + "@example.test>", EncryptedPayload: json.RawMessage(`{"ciphertext":"secret"}`), Revision: 1,
			}
			got, err := evaluateMailDeliveryRelevance(context.Background(), delivery)
			require.NoError(t, err)
			require.Equal(t, mailDeliveryRelevant, got)
		})
	}
}
