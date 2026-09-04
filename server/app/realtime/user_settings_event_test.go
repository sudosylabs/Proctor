// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package realtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

func TestUserSettingsChangedEventIsAContentFreeUserTargetedHint(t *testing.T) {
	t.Parallel()
	userID := model.NewUserID()
	revision := model.NewUserSettingsRevision()
	changedAt := time.Date(2026, 8, 15, 14, 30, 0, 123000000, time.UTC)

	event, err := NewUserSettingsChangedEvent(userID, revision, 1, changedAt)
	if err != nil {
		t.Fatal(err)
	}
	if event.Name != "user_settings_changed" || event.UserID != userID.String() ||
		event.Action != "" || event.Resource != (model.Resource{}) {
		t.Fatalf("event target = %#v", event)
	}
	if got := string(event.Data); got != `{"revision":"`+revision.String()+`","format_version":1,"changed_at":1786804200123}` {
		t.Fatalf("event data = %s", got)
	}
	var fields map[string]any
	if err := json.Unmarshal(event.Data, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 3 {
		t.Fatalf("event fields = %#v", fields)
	}
	for _, forbidden := range []string{"source", "key", "value", "comment", "diagnostic", "idempotency", "session", "client"} {
		if strings.Contains(strings.ToLower(string(event.Data)), forbidden) {
			t.Fatalf("event data contains forbidden %q: %s", forbidden, event.Data)
		}
	}
}

func TestUserSettingsChangedEventRejectsInvalidMetadata(t *testing.T) {
	t.Parallel()
	now := time.Now()
	userID := model.NewUserID()
	revision := model.NewUserSettingsRevision()
	tests := []struct {
		name     string
		userID   model.UserID
		revision model.UserSettingsRevision
		format   int
		at       time.Time
	}{
		{name: "user", revision: revision, format: 1, at: now},
		{name: "revision", userID: userID, format: 1, at: now},
		{name: "format", userID: userID, revision: revision, at: now},
		{name: "time", userID: userID, revision: revision, format: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := NewUserSettingsChangedEvent(test.userID, test.revision, test.format, test.at); err == nil {
				t.Fatal("accepted invalid settings event metadata")
			}
		})
	}
}

func TestUserSettingsChangedEventUsesExistingLocalFirstClusterPathWithoutLooping(t *testing.T) {
	t.Parallel()
	order := &orderedCalls{}
	sink := &recordingSink{publish: func(event RealtimeEvent) {
		if event.Name != "user_settings_changed" {
			t.Fatalf("local event = %#v", event)
		}
		order.add("local")
	}}
	fanout := &recordingFanout{broadcast: func(_ string, _ []byte) error {
		order.add("peer")
		return nil
	}}
	service := newOrdinaryTestService(t)
	if err := service.SetSink(sink); err != nil {
		t.Fatal(err)
	}
	if err := service.SetClusterFanout(fanout); err != nil {
		t.Fatal(err)
	}
	userID := model.NewUserID()
	event, err := NewUserSettingsChangedEvent(userID, model.NewUserSettingsRevision(), 1, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Publish(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(order.snapshot(), ","); got != "local,peer" {
		t.Fatalf("delivery order = %s", got)
	}
	if len(fanout.broadcasts) != 1 {
		t.Fatalf("peer broadcasts = %d", len(fanout.broadcasts))
	}
	peerPayload := strings.ToLower(string(fanout.broadcasts[0].data))
	if !strings.Contains(peerPayload, `"user_id":"`+userID.String()+`"`) {
		t.Fatalf("peer payload lost exact User target: %s", peerPayload)
	}
	for _, forbidden := range []string{`"source":`, `"idempotency_key":`, `"session_id":`, `"client_type":`, `"diagnostics":`} {
		if strings.Contains(peerPayload, forbidden) {
			t.Fatalf("peer payload contains forbidden %q: %s", forbidden, peerPayload)
		}
	}
	handler := fanout.handler(clusterEventPublication)
	if err := handler(context.Background(), fanout.broadcasts[0].data); err != nil {
		t.Fatal(err)
	}
	if len(sink.events) != 2 || len(fanout.broadcasts) != 1 {
		t.Fatalf("peer delivery local=%d broadcasts=%d", len(sink.events), len(fanout.broadcasts))
	}
}
