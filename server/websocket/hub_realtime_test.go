// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package websocket

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/sudosylabs/proctor/server/app/realtime"
	"github.com/sudosylabs/proctor/server/model"
)

func TestEventFromRealtimePreservesWireFieldsAndClonesData(t *testing.T) {
	t.Parallel()

	data := json.RawMessage(`{"status":"ready"}`)
	source := realtime.RealtimeEvent{
		ID:     model.NewId(),
		Name:   "academic_unit.updated",
		UserID: model.NewId(),
		Action: model.ActionAcademicUnitView,
		Resource: model.Resource{
			Type: model.ResourceAcademicUnit,
			ID:   model.NewId(),
		},
		Data: data,
	}

	event := eventFromRealtime(source)
	if event.Id != source.ID || event.Event != source.Name ||
		event.UserID != source.UserID || event.Action != source.Action {
		t.Fatalf("wire event fields = %#v, source = %#v", event, source)
	}
	if event.Resource != resourceFromModel(source.Resource) {
		t.Fatalf("wire resource = %#v, want %#v", event.Resource, source.Resource)
	}
	if !bytes.Equal(event.Data, source.Data) {
		t.Fatalf("wire data = %s, want %s", event.Data, source.Data)
	}

	event.Data[0] = '['
	if bytes.Equal(event.Data, source.Data) {
		t.Fatal("wire event data aliases the realtime event data")
	}
}

func TestCloseCodeForRealtimeReason(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		reason     realtime.ConnectionCloseReason
		wantCode   int
		wantReason string
	}{
		{
			name:       "session revoked",
			reason:     realtime.ConnectionCloseSessionRevoked,
			wantCode:   CloseSessionRevoked,
			wantReason: "session revoked",
		},
		{
			name:       "authorization changed",
			reason:     realtime.ConnectionCloseAuthorizationChanged,
			wantCode:   CloseAuthorizationChanged,
			wantReason: "authorization changed",
		},
		{
			name:       "unspecified",
			wantCode:   CloseServer,
			wantReason: "connection closed",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			code, reason := closeCodeForReason(test.reason)
			if code != test.wantCode || reason != test.wantReason {
				t.Fatalf("close = (%d, %q), want (%d, %q)", code, reason, test.wantCode, test.wantReason)
			}
		})
	}
}
