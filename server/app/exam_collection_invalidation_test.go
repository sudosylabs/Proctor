// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/realtime"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestExamArchiveInvalidatesEveryAffectedCollection(t *testing.T) {
	t.Parallel()
	realtime := newTestRealtimeService(t, noopAuthenticationCache{})
	sink := &recordingRealtimeSink{}
	require.NoError(t, realtime.AttachRealtimeSink(sink))
	require.NoError(t, realtime.AttachRealtimeClusterFanout(&recordingRealtimeCluster{}))
	examID, sittingID, candidateID := model.NewExamID(), model.NewExamSittingID(), model.NewUserID()
	collections := examCollectionInvalidationEffects{
		sittings: &examCollectionInvalidationStoreFake{
			candidateIDs: []model.UserID{candidateID},
			targets: []store.ExamSittingInvalidationTarget{{
				ExamID: examID, SittingID: sittingID,
			}},
		},
		realtime: realtime,
	}
	effects := examRealtimeEffects{realtime: realtime, collections: collections}
	require.NoError(t, effects.Archived(context.Background(), examID, 4, time.Unix(100, 0)))
	sink.mu.Lock()
	events := append([]string(nil), sinkEventNames(sink.events)...)
	userID := sink.events[2].UserID
	sink.mu.Unlock()
	want := []string{"exam_archived", "manager.sitting_board.changed", "candidate.exam_activity.changed"}
	require.Len(t, events, len(want))
	for index := range want {
		require.Equal(t, want[index], events[index], "archive invalidations = %#v, want %#v", events, want)
	}
	require.Equal(t, candidateID, userID, "candidate target = %q, want %q", userID, candidateID)
}

func sinkEventNames(events []realtime.RealtimeEvent) []string {
	names := make([]string, len(events))
	for index := range events {
		names[index] = events[index].Name
	}
	return names
}
