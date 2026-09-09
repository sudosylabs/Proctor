// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

func TestBrowserDeliveryHTTPDecodesExpandedUnicodeNavigation(t *testing.T) {
	prefix := "https://example.edu/"
	location, err := model.CanonicalizeBrowserLocation(prefix + strings.Repeat("漢", model.BrowserNavigationMaximumCharacters-len(prefix)))
	if err != nil {
		t.Fatal(err)
	}
	rule := "start"
	batch := model.BrowserActivityBatch{SourceSessionID: model.BrowserSourceSessionID("00000000-0000-4000-8000-000000019903"), ParticipationID: model.NewAttemptParticipationID(), Generation: 1, PolicyRevisionID: model.NewExamRevisionID(), PolicyDigest: model.SHA256Fingerprint([]byte("policy"))}
	batch.Events = []model.BrowserActivityEvent{{Sequence: 1, Kind: model.BrowserActivityTopNavigation, PolicyRevisionID: batch.PolicyRevisionID, ClientOccurredAt: time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC), Location: &location, MatchedRuleID: &rule}}
	raw, err := json.Marshal(batch)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) <= 32*1024 || len(raw) >= model.BrowserActivityAppendMaximumBytes {
		t.Fatalf("invalid expanded fixture size %d", len(raw))
	}
	request := httptest.NewRequest("POST", "/", bytes.NewReader(raw))
	request.Header.Set("Content-Type", "application/json")
	var decoded model.BrowserActivityBatch
	if err := (operationRequest{request: request}).decodeJSON(&decoded, "browserActivityBatch"); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Events) != 1 || decoded.Events[0].Location.Path != location.Path {
		t.Fatal("HTTP decoding changed serialized navigation")
	}
}
