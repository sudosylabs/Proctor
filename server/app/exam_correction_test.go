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

	examcorrection "github.com/sudosylabs/proctor/server/app/exam/correction"
	apprealtime "github.com/sudosylabs/proctor/server/app/realtime"
	"github.com/sudosylabs/proctor/server/model"
)

func TestCorrectionEffectPublishesManagerAndCandidateRefetchFacts(t *testing.T) {
	t.Parallel()
	realtime := newTestRealtimeService(t, noopAuthenticationCache{})
	sink := &recordingRealtimeSink{}
	require.NoError(t, realtime.AttachRealtimeSink(sink))
	require.NoError(t, realtime.AttachRealtimeClusterFanout(&recordingRealtimeCluster{}))
	examID, sittingID := model.NewExamID(), model.NewExamSittingID()
	candidateID := model.NewUserID()
	collections := examCollectionInvalidationEffects{
		sittings: &examCollectionInvalidationStoreFake{candidateIDs: []model.UserID{candidateID}},
		realtime: realtime,
	}
	previousRevisionID, revisionID := model.NewExamRevisionID(), model.NewExamRevisionID()
	at := time.Date(2026, time.August, 17, 12, 0, 0, 0, time.UTC)
	err := (examCorrectionRealtimeEffects{realtime: realtime, collections: collections}).Corrected(context.Background(), examcorrection.Result{
		ExamID: examID, SittingID: sittingID, PreviousRevisionID: previousRevisionID,
		RevisionID: revisionID, SittingRevision: 7, EffectiveAt: at,
	})
	require.NoError(t, err)
	sink.mu.Lock()
	events := append([]apprealtime.RealtimeEvent(nil), sink.events...)
	sink.mu.Unlock()
	require.Len(t, events, 4)
	require.Equal(t, model.ActionExamSittingView, events[0].Action)
	require.Equal(t, model.ActionExamSittingParticipate, events[1].Action)
	require.Equal(t, "exam_sitting_content_corrected", events[0].Name)
	require.Equal(t, events[0].Name, events[1].Name)
	require.Equal(t, string(events[0].Data), string(events[1].Data))
	require.Equal(t, "manager.sitting_board.changed", events[2].Name)
	require.Equal(t, "candidate.exam_activity.changed", events[3].Name)
	require.Equal(t, candidateID.String(), events[3].UserID)

}
