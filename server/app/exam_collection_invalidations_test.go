// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"testing"
	"time"

	apprealtime "github.com/sudosylabs/proctor/server/app/realtime"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestExamArchiveInvalidatesEveryAffectedCollection(t *testing.T) {
	t.Parallel()
	realtime := newTestRealtimeService(t, noopAuthenticationCache{})
	sink := &recordingRealtimeSink{}
	if err := realtime.SetSink(sink); err != nil {
		t.Fatal(err)
	}
	if err := realtime.SetClusterFanout(&recordingRealtimeCluster{}); err != nil {
		t.Fatal(err)
	}
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
	if err := effects.Archived(context.Background(), examID, 4, time.Unix(100, 0)); err != nil {
		t.Fatal(err)
	}
	sink.mu.Lock()
	events := append([]string(nil), sinkEventNames(sink.events)...)
	userID := sink.events[2].UserID
	sink.mu.Unlock()
	want := []string{"exam_archived", "manager.sitting_board.changed", "candidate.exam_activity.changed"}
	if len(events) != len(want) {
		t.Fatalf("archive invalidations = %#v", events)
	}
	for index := range want {
		if events[index] != want[index] {
			t.Fatalf("archive invalidations = %#v, want %#v", events, want)
		}
	}
	if userID != candidateID.String() {
		t.Fatalf("candidate target = %q, want %q", userID, candidateID)
	}
}

func sinkEventNames(events []apprealtime.RealtimeEvent) []string {
	names := make([]string, len(events))
	for index := range events {
		names[index] = events[index].Name
	}
	return names
}
