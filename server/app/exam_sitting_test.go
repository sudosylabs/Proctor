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

	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/app/realtime"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestExamSittingLifecycleJobReconcileLeavesClosingToDedicatedSealingJob(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 16, 9, 0, 0, 0, time.UTC)
	sitting, err := model.NewExamSitting(model.NewExamSittingID(), model.NewExamID(), model.NewExamRevisionID(), model.NewClassID(), at.Add(time.Hour), at.Add(2*time.Hour), at)
	require.NoError(t, err)
	err = sitting.Open(at.Add(time.Hour))
	require.NoError(t, err)
	err = sitting.EnterClosing(model.ExamSittingReasonScheduledEndReached, at.Add(2*time.Hour))
	require.NoError(t, err)
	closing := *sitting
	err = sitting.Close(at.Add(2*time.Hour + time.Second))
	require.NoError(t, err)
	jobID, attemptID := model.NewJobID(), model.NewJobAttemptID()
	fake := &examSittingLifecycleFake{
		advance: store.ExamSittingLifecycleResult{Value: &store.ExamSittingSnapshot{Sitting: &closing}, Changed: true, Transition: store.ExamSittingTransitionScheduledEndReached},
	}
	result, err := (examSittingLifecycleJobUseCases{sittings: fake}).ReconcileExamSittingLifecycleFromJob(context.Background(), sitting.ID, jobID, attemptID)
	require.NoError(t, err)
	require.NotNil(t, result, "result/call")
	require.Equal(t, model.ExamSittingClosing, result.Value.Sitting.State, "result/call")
	require.Equal(t, invocation.System{JobID: jobID, AttemptID: attemptID}, fake.advanceCall, "result/call")

	// Another node may close before the lifecycle read. The dedicated sealing
	// handler still owns any closure work, so this adapter returns AdvanceDue's
	// authoritative value and never invokes the legacy zero-Attempt closer.
	racing := &examSittingLifecycleFake{
		advance: store.ExamSittingLifecycleResult{Value: &store.ExamSittingSnapshot{Sitting: &closing}},
	}
	result, err = (examSittingLifecycleJobUseCases{sittings: racing}).ReconcileExamSittingLifecycleFromJob(context.Background(), sitting.ID, jobID, attemptID)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, model.ExamSittingClosing, result.Value.Sitting.State)
}

func TestExamSittingLifecycleEffectPublishesOnlySafeBoundedMetadata(t *testing.T) {
	t.Parallel()
	service := newTestRealtimeService(t, noopAuthenticationCache{})
	sink := &recordingRealtimeSink{}
	require.NoError(t, service.AttachRealtimeSink(sink))
	require.NoError(t, service.AttachRealtimeClusterFanout(&recordingRealtimeCluster{}))
	examID, sittingID := model.NewExamID(), model.NewExamSittingID()
	candidateID := model.NewUserID()
	collections := examCollectionInvalidationEffects{
		sittings: &examCollectionInvalidationStoreFake{candidateIDs: []model.UserID{candidateID}},
		realtime: service,
	}
	at := time.Date(2026, time.August, 16, 9, 0, 0, 0, time.UTC)
	err := (examSittingRealtimeEffects{realtime: service, collections: collections}).LifecycleChanged(context.Background(), examID, sittingID,
		model.ExamSittingPaused, 4, store.ExamSittingTransitionManagerPaused, at.Add(2*time.Hour), at)
	require.NoError(t, err)
	sink.mu.Lock()
	events := append([]realtime.RealtimeEvent(nil), sink.events...)
	sink.mu.Unlock()
	require.Len(t, events, 4)
	require.Equal(t, "exam_sitting_lifecycle_changed", events[0].Name)
	require.Equal(t, model.ActionExamSittingView, events[0].Action)
	require.Equal(t, "exam_sitting_lifecycle_changed", events[1].Name)
	require.Equal(t, model.ActionExamSittingParticipate, events[1].Action)
	require.Equal(t, `{"exam_id":"`+examID.String()+`","exam_sitting_id":"`+sittingID.String()+`","state":"paused","revision":4,"reason_code":"manager_paused","scheduled_end_at":"2026-08-16T11:00:00Z","changed_at":"2026-08-16T09:00:00Z"}`, string(events[0].Data))
	require.Equal(t, string(events[0].Data), string(events[1].Data))
	require.Equal(t, "manager.sitting_board.changed", events[2].Name)
	require.Equal(t, model.ActionExamSittingView, events[2].Action)
	require.Equal(t, `{"schema_version":1,"exam_id":"`+examID.String()+`","exam_sitting_id":"`+sittingID.String()+`"}`, string(events[2].Data))
	require.Equal(t, "candidate.exam_activity.changed", events[3].Name)
	require.Equal(t, candidateID, events[3].UserID)
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
	advanceCall invocation.System
	err         error
}

func (f *examSittingLifecycleFake) AdvanceDue(_ context.Context, call invocation.System, _ model.ExamSittingID) (store.ExamSittingLifecycleResult, error) {
	f.advanceCall = call
	return f.advance, f.err
}

func (f *examSittingLifecycleFake) ListLifecycleDue(context.Context, invocation.System, store.ExamSittingLifecycleDueOptions) ([]store.ExamSittingLifecycleDue, error) {
	return nil, f.err
}
