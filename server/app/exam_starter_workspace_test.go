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
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/exam/workspace"
	"github.com/sudosylabs/proctor/server/model"
)

func TestExamStarterWorkspaceRealtimeEffectContainsOnlySafeChangeMetadata(t *testing.T) {
	t.Parallel()
	sink := &recordingRealtimeSink{}
	cluster := &recordingRealtimeCluster{}
	realtime := newTestRealtimeService(t, noopAuthenticationCache{})
	require.NoError(t, realtime.AttachRealtimeSink(sink))
	require.NoError(t, realtime.AttachRealtimeClusterFanout(cluster))
	examID, entryID := model.NewExamID(), model.NewStarterWorkspaceEntryID()
	changedAt := time.Date(2026, 8, 15, 8, 30, 0, 123456789, time.UTC)
	require.NoError(t, (examStarterWorkspaceRealtimeEffects{realtime: realtime}).Changed(context.Background(), examID, entryID, 4, workspace.ChangeFileReplaced, changedAt))
	require.Len(t, sink.events, 1)
	require.Equal(t, "exam_starter_workspace_changed", sink.events[0].Name)
	require.Equal(t, model.Resource{Type: model.ResourceExam, ID: examID.String()}, sink.events[0].Resource)
	var data map[string]any
	require.NoError(t, json.Unmarshal(sink.events[0].Data, &data))
	require.Len(t, data, 5)
	require.Equal(t, examID.String(), data["exam_id"])
	require.Equal(t, entryID.String(), data["entry_id"])
	require.Equal(t, "file_replaced", data["operation"])
	require.Equal(t, float64(4), data["draft_revision"])
	require.Equal(t, "2026-08-15T08:30:00.123456Z", data["changed_at"])
	for _, forbidden := range []string{"path", "content", "sha256", "object_id"} {
		_, exists := data[forbidden]
		require.False(t, exists, "event exposed %s: %#v", forbidden, data)
	}
}
