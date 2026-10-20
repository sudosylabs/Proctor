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

	appinvocation "github.com/sudosylabs/proctor/server/app/invocation"
	apprealtime "github.com/sudosylabs/proctor/server/app/realtime"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestExamSittingLifecycleJobReconcileLeavesClosingToDedicatedSealingJob(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 16, 9, 0, 0, 0, time.UTC)
	sitting, err := model.NewExamSitting(model.NewExamSittingID(), model.NewExamID(), model.NewExamRevisionID(), model.NewClassID(), at.Add(time.Hour), at.Add(2*time.Hour), at)
	if err != nil {
		t.Fatal(err)
	}
	if err = sitting.Open(at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err = sitting.EnterClosing(model.ExamSittingReasonScheduledEndReached, at.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	closing := *sitting
	if err = sitting.Close(at.Add(2*time.Hour + time.Second)); err != nil {
		t.Fatal(err)
	}
	jobID, attemptID := model.NewJobID(), model.NewJobAttemptID()
	fake := &examSittingLifecycleFake{
		advance: store.ExamSittingLifecycleResult{Value: &store.ExamSittingSnapshot{Sitting: &closing}, Changed: true, Transition: store.ExamSittingTransitionScheduledEndReached},
	}
	result, err := (examSittingLifecycleJobUseCases{sittings: fake}).ReconcileExamSittingLifecycleFromJob(context.Background(), sitting.ID, jobID, attemptID)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.Value.Sitting.State != model.ExamSittingClosing ||
		fake.advanceCall != (appinvocation.System{JobID: jobID, AttemptID: attemptID}) {
		t.Fatalf("result/call = %#v / %#v", result, fake.advanceCall)
	}

	// Another node may close before the lifecycle read. The dedicated sealing
	// handler still owns any closure work, so this adapter returns AdvanceDue's
	// authoritative value and never invokes the legacy zero-Attempt closer.
	racing := &examSittingLifecycleFake{
		advance: store.ExamSittingLifecycleResult{Value: &store.ExamSittingSnapshot{Sitting: &closing}},
	}
	result, err = (examSittingLifecycleJobUseCases{sittings: racing}).ReconcileExamSittingLifecycleFromJob(context.Background(), sitting.ID, jobID, attemptID)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.Value.Sitting.State != model.ExamSittingClosing {
		t.Fatalf("racing close result = %#v", result)
	}
}

func TestExamSittingLifecycleEffectPublishesOnlySafeBoundedMetadata(t *testing.T) {
	t.Parallel()
	realtime := newTestRealtimeService(t, noopAuthenticationCache{})
	sink := &recordingRealtimeSink{}
	if err := realtime.AttachRealtimeSink(sink); err != nil {
		t.Fatal(err)
	}
	if err := realtime.AttachRealtimeClusterFanout(&recordingRealtimeCluster{}); err != nil {
		t.Fatal(err)
	}
	examID, sittingID := model.NewExamID(), model.NewExamSittingID()
	candidateID := model.NewUserID()
	collections := examCollectionInvalidationEffects{
		sittings: &examCollectionInvalidationStoreFake{candidateIDs: []model.UserID{candidateID}},
		realtime: realtime,
	}
	at := time.Date(2026, time.August, 16, 9, 0, 0, 0, time.UTC)
	err := (examSittingRealtimeEffects{realtime: realtime, collections: collections}).LifecycleChanged(context.Background(), examID, sittingID,
		model.ExamSittingPaused, 4, store.ExamSittingTransitionManagerPaused, at.Add(2*time.Hour), at)
	if err != nil {
		t.Fatal(err)
	}
	sink.mu.Lock()
	events := append([]apprealtime.RealtimeEvent(nil), sink.events...)
	sink.mu.Unlock()
	if len(events) != 4 || events[0].Name != "exam_sitting_lifecycle_changed" ||
		events[0].Action != model.ActionExamSittingView ||
		events[1].Name != "exam_sitting_lifecycle_changed" ||
		events[1].Action != model.ActionExamSittingParticipate ||
		string(events[0].Data) != `{"exam_id":"`+examID.String()+`","exam_sitting_id":"`+sittingID.String()+`","state":"paused","revision":4,"reason_code":"manager_paused","scheduled_end_at":"2026-08-16T11:00:00Z","changed_at":"2026-08-16T09:00:00Z"}` ||
		string(events[1].Data) != string(events[0].Data) || events[2].Name != "manager.sitting_board.changed" ||
		events[2].Action != model.ActionExamSittingView || string(events[2].Data) !=
		`{"schema_version":1,"exam_id":"`+examID.String()+`","exam_sitting_id":"`+sittingID.String()+`"}` ||
		events[3].Name != "candidate.exam_activity.changed" || events[3].UserID != candidateID.String() {
		t.Fatalf("events = %#v", events)
	}
}

type examCollectionInvalidationStoreFake struct {
	candidateIDs []model.UserID
	targets      []store.ExamSittingInvalidationTarget
}

func (f *examCollectionInvalidationStoreFake) ListInvalidationTargetsByExam(
	context.Context,
	model.ExamID,
	model.ExamSittingID,
	int,
) ([]store.ExamSittingInvalidationTarget, error) {
	return append([]store.ExamSittingInvalidationTarget(nil), f.targets...), nil
}

func (f *examCollectionInvalidationStoreFake) ListCandidateInvalidationTargetsBySitting(
	context.Context,
	model.ExamSittingID,
	model.UserID,
	int,
) ([]model.UserID, error) {
	return append([]model.UserID(nil), f.candidateIDs...), nil
}

type examSittingLifecycleFake struct {
	advance     store.ExamSittingLifecycleResult
	advanceCall appinvocation.System
	err         error
}

func (fake *examSittingLifecycleFake) AdvanceDue(_ context.Context, call appinvocation.System, _ model.ExamSittingID) (store.ExamSittingLifecycleResult, error) {
	fake.advanceCall = call
	return fake.advance, fake.err
}

func (fake *examSittingLifecycleFake) ListLifecycleDue(context.Context, store.ExamSittingLifecycleDueOptions) ([]store.ExamSittingLifecycleDue, error) {
	return nil, fake.err
}
