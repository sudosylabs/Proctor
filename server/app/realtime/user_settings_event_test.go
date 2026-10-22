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

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

func TestUserSettingsChangedEventIsAContentFreeUserTargetedHint(t *testing.T) {
	t.Parallel()
	userID := model.NewUserID()
	revision := model.NewUserSettingsRevision()
	changedAt := time.Date(2026, 8, 15, 14, 30, 0, 123000000, time.UTC)

	event, err := NewUserSettingsChangedEvent(userID, revision, 1, changedAt)
	require.NoError(t, err)
	require.Equal(t, "user_settings_changed", event.Name)
	require.Equal(t, userID.String(), event.UserID)
	require.Empty(t, event.Action)
	require.Equal(t, model.Resource{}, event.Resource)
	require.Equal(t, `{"revision":"`+revision.String()+`","format_version":1,"changed_at":"2026-08-15T14:30:00.123Z"}`, string(event.Data))
	var fields map[string]any
	require.NoError(t, json.Unmarshal(event.Data, &fields))
	require.Len(t, fields, 3)
	for _, forbidden := range []string{"source", "key", "value", "comment", "diagnostic", "idempotency", "session", "client"} {
		require.NotContains(t, strings.ToLower(string(event.Data)), forbidden, "event data contains forbidden %q: %s", forbidden, event.Data)
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
			_, err := NewUserSettingsChangedEvent(test.userID, test.revision, test.format, test.at)
			require.Error(t, err, "accepted invalid settings event metadata")
		})
	}
}

func TestUserSettingsChangedEventUsesExistingLocalFirstClusterPathWithoutLooping(t *testing.T) {
	t.Parallel()
	order := &orderedCalls{}
	sink := &recordingSink{publish: func(event RealtimeEvent) {
		require.Equal(t, "user_settings_changed", event.Name)
		order.add("local")
	}}
	fanout := &recordingFanout{broadcast: func(_ string, _ []byte) error {
		order.add("peer")
		return nil
	}}
	service := newOrdinaryTestService(t)
	require.NoError(t, service.SetSink(sink))
	require.NoError(t, service.SetClusterFanout(fanout))
	userID := model.NewUserID()
	event, err := NewUserSettingsChangedEvent(userID, model.NewUserSettingsRevision(), 1, time.Now())
	require.NoError(t, err)
	require.NoError(t, service.Publish(context.Background(), event))
	// Contract: ordinary publication delivers locally before the best-effort
	// peer fan-out.
	require.Equal(t, []string{"local", "peer"}, order.snapshot())
	require.Len(t, fanout.broadcasts, 1, "peer broadcasts")
	peerPayload := strings.ToLower(string(fanout.broadcasts[0].data))
	require.Contains(t, peerPayload, `"user_id":"`+userID.String()+`"`)
	for _, forbidden := range []string{`"source":`, `"idempotency_key":`, `"session_id":`, `"client_type":`, `"diagnostics":`} {
		require.NotContains(t, peerPayload, forbidden, "peer payload contains forbidden")
	}
	handler := fanout.handler(clusterEventPublication)
	require.NoError(t, handler(context.Background(), fanout.broadcasts[0].data))
	require.Len(t, sink.events, 2, "peer delivery local")
	require.Len(t, fanout.broadcasts, 1, "peer delivery local")
}
