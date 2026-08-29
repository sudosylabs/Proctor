// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"strings"
	"testing"
	"time"

	examcorrection "github.com/sudosylabs/proctor/server/app/exam/correction"
	apprealtime "github.com/sudosylabs/proctor/server/app/realtime"
	"github.com/sudosylabs/proctor/server/model"
)

func TestCorrectionApplyForwardsInstructionsManifestOrderAndRawKey(t *testing.T) {
	t.Parallel()
	fake := &examCorrectionUseCasesFake{}
	application := &App{examCorrections: fake}
	invocation := NewInvocation(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{})
	examID, sittingID, revisionID := model.NewExamID(), model.NewExamSittingID(), model.NewExamRevisionID()
	first, second := model.NewExamResourceID(), model.NewExamResourceID()
	base := ApplyExamSittingCorrectionCommand{ExamID: examID, SittingID: sittingID, ExpectedSittingRevision: 2, ExpectedCurrentRevisionID: revisionID, Instructions: ExamSittingCorrectionInstructions{Present: true}, Resources: []ExamSittingCorrectionResourceManifestItem{{ResourceID: first, DisplayName: "First"}, {ResourceID: second, DisplayName: "Second"}}, PrivateReason: "Fix ambiguity", IdempotencyKey: "same"}
	if _, err := application.ApplyExamSittingCorrection(context.Background(), invocation, base); err != nil {
		t.Fatal(err)
	}
	if !fake.apply.Instructions.Present || len(fake.apply.Resources) != 2 || fake.apply.Resources[0].ResourceID != first ||
		fake.apply.Resources[1].ResourceID != second || fake.apply.IdempotencyKey != "same" {
		t.Fatalf("child command = %#v", fake.apply)
	}
}

func TestCorrectionStageForwardsBodyDigestAndRawKey(t *testing.T) {
	t.Parallel()
	fake := &examCorrectionUseCasesFake{}
	application := &App{examCorrections: fake}
	invocation := NewInvocation(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{})
	command := StageExamSittingCorrectionResourceContentCommand{ExamID: model.NewExamID(), SittingID: model.NewExamSittingID(), BaseRevisionID: model.NewExamRevisionID(), Target: ExamSittingCorrectionResourceAddition, MediaType: model.ExamResourceMediaText, Body: strings.NewReader("one"), Size: 3, ExpectedSHA256: strings.Repeat("a", 64), IdempotencyKey: "same"}
	if _, err := application.StageExamSittingCorrectionResourceContent(context.Background(), invocation, command); err != nil {
		t.Fatal(err)
	}
	if fake.stage.Body == nil || fake.stage.ExpectedSHA256 != strings.Repeat("a", 64) || fake.stage.IdempotencyKey != "same" {
		t.Fatalf("child command = %#v", fake.stage)
	}
}

func TestCorrectionFacadeConcealsAuthorizationAndNotFound(t *testing.T) {
	t.Parallel()
	for _, cause := range []error{NewError("authorization.denied"), &examcorrection.Fault{Code: "exam.sitting.correction.not_found"}} {
		mapped := examCorrectionError(cause, true)
		appErr, ok := As(mapped)
		if !ok || appErr.Code() != "resource.not_found" {
			t.Fatalf("cause=%v mapped=%v", cause, mapped)
		}
	}
}

func TestCorrectionEffectPublishesManagerAndCandidateRefetchFacts(t *testing.T) {
	t.Parallel()
	realtime := newTestRealtimeService(t, noopAuthenticationCache{})
	sink := &recordingRealtimeSink{}
	if err := realtime.SetSink(sink); err != nil {
		t.Fatal(err)
	}
	if err := realtime.SetClusterFanout(&recordingRealtimeCluster{}); err != nil {
		t.Fatal(err)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	sink.mu.Lock()
	events := append([]apprealtime.RealtimeEvent(nil), sink.events...)
	sink.mu.Unlock()
	if len(events) != 4 || events[0].Action != model.ActionExamSittingView ||
		events[1].Action != model.ActionExamSittingParticipate || events[0].Name != "exam_sitting_content_corrected" ||
		events[1].Name != events[0].Name || string(events[1].Data) != string(events[0].Data) ||
		events[2].Name != "manager.sitting_board.changed" ||
		events[3].Name != "candidate.exam_activity.changed" || events[3].UserID != candidateID.String() {
		t.Fatalf("events = %#v", events)
	}
}

type examCorrectionUseCasesFake struct {
	stage examcorrection.StageResourceContentCommand
	apply examcorrection.ApplyCommand
}

func (f *examCorrectionUseCasesFake) StageResourceContent(_ context.Context, _ examcorrection.Call, c examcorrection.StageResourceContentCommand) (examcorrection.ResourceStage, error) {
	f.stage = c
	return examcorrection.ResourceStage{}, nil
}
func (f *examCorrectionUseCasesFake) Apply(_ context.Context, _ examcorrection.Call, c examcorrection.ApplyCommand) (examcorrection.Result, error) {
	f.apply = c
	return examcorrection.Result{}, nil
}
