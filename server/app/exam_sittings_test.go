// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	examsitting "github.com/sudosylabs/proctor/server/app/exam/sitting"
	apprealtime "github.com/sudosylabs/proctor/server/app/realtime"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestExamSittingScheduleUpdateForwardsPatchPresenceAndRawKey(t *testing.T) {
	t.Parallel()
	invocation := NewInvocation(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{})
	examID, sittingID := model.NewExamID(), model.NewExamSittingID()
	revisionID := model.NewExamRevisionID()
	instant := time.Date(2026, 8, 15, 12, 0, 0, 123456789, time.FixedZone("fixture", 2*60*60))
	var zeroTime time.Time
	var zeroClass model.ClassID
	for _, command := range []UpdateExamSittingScheduleCommand{
		{ExamID: examID, SittingID: sittingID, ExpectedRevision: 2, ExamRevisionID: &revisionID,
			ScheduledStartAt: &instant, IdempotencyKey: " raw-key "},
		{ExamID: examID, SittingID: sittingID, ExpectedRevision: 2, ClassID: &zeroClass,
			ScheduledEndAt: &zeroTime, IdempotencyKey: " raw-key "},
	} {
		fake := &examSittingUseCasesFake{}
		_, err := (&App{examSittings: fake}).UpdateExamSittingSchedule(context.Background(), invocation, command)
		if err != nil {
			t.Fatal(err)
		}
		if fake.update != command {
			t.Fatalf("patch presence, pointer identity, or raw input changed: %#v", fake.update)
		}
	}
}

func TestAuthorizeWebSocketSittingSubscriptionUsesRelationshipGate(t *testing.T) {
	t.Parallel()
	child := &examSittingUseCasesFake{}
	application := &App{examSittings: child}
	principal := testExamPrincipal(model.NewUserID())
	sittingID := model.NewExamSittingID()
	err := application.AuthorizeWebSocketSubscription(context.Background(), principal,
		model.RequestMetadata{RequestID: "sitting-subscribe"}, model.ActionExamSittingView,
		model.Resource{Type: model.ResourceExamSitting, ID: sittingID.String()})
	if err != nil {
		t.Fatal(err)
	}
	if child.sittingID != sittingID || child.call.Principal().UserID != principal.UserID {
		t.Fatalf("authorization = %s %#v", child.sittingID, child.call)
	}
}

func TestAuthorizeWebSocketSittingSubscriptionConcealsMissingAndDeniedTargets(t *testing.T) {
	t.Parallel()
	principal := testExamPrincipal(model.NewUserID())
	resource := model.Resource{Type: model.ResourceExamSitting, ID: model.NewExamSittingID().String()}
	for _, failure := range []error{&examsitting.Fault{Code: "exam.sitting.not_found"}, NewError("authorization.denied")} {
		application := &App{examSittings: &examSittingUseCasesFake{err: failure}}
		err := application.AuthorizeWebSocketSubscription(context.Background(), principal, model.RequestMetadata{},
			model.ActionExamSittingView, resource)
		if !Is(err, "resource.not_found") {
			t.Fatalf("error = %v, want concealed resource.not_found", err)
		}
	}
}

func TestExamSittingFacadeForwardsCommandsAndPreservesInvocationAndResults(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	invocation := NewInvocation(model.Principal{UserID: model.NewUserID(), CredentialScopes: []string{"exam:manage"}},
		model.RequestMetadata{RequestID: "sitting-command", IPAddress: "192.0.2.1", UserAgent: "test"})
	examID, sittingID := model.NewExamID(), model.NewExamSittingID()
	revisionID, classID := model.NewExamRevisionID(), model.NewClassID()
	start := time.Date(2026, time.August, 16, 12, 0, 0, 123456789, time.FixedZone("fixture", 2*60*60))
	end := start.Add(time.Hour)
	schedule := ScheduleExamSittingCommand{ExamID: examID, ExamRevisionID: revisionID, ClassID: classID,
		ScheduledStartAt: start, ScheduledEndAt: end, IdempotencyKey: " raw-schedule-key "}
	update := UpdateExamSittingScheduleCommand{ExamID: examID, SittingID: sittingID, ExpectedRevision: 3,
		ExamRevisionID: &revisionID, ClassID: &classID, ScheduledStartAt: &start, ScheduledEndAt: &end, IdempotencyKey: " raw-update-key "}
	transition := PauseExamSittingCommand{ExamID: examID, SittingID: sittingID, ExpectedRevision: 3,
		PrivateReason: " raw reason ", IdempotencyKey: " raw-transition-key "}
	cancelCommand := CancelExamSittingCommand(transition)
	extend := ExtendExamSittingCommand{ExamID: examID, SittingID: sittingID, ExpectedRevision: 3, ScheduledEndAt: end,
		PrivateReason: " raw extension reason ", IdempotencyKey: " raw-extend-key "}
	result := store.ExamSittingSnapshot{Sitting: &model.ExamSitting{ID: sittingID, Revision: 4}}
	for _, test := range []struct {
		name    string
		command any
		invoke  func(*App) (ExamSittingView, error)
	}{
		{"schedule", schedule, func(app *App) (ExamSittingView, error) {
			return app.ScheduleExamSitting(ctx, invocation, schedule)
		}},
		{"update", update, func(app *App) (ExamSittingView, error) {
			return app.UpdateExamSittingSchedule(ctx, invocation, update)
		}},
		{"cancel", cancelCommand, func(app *App) (ExamSittingView, error) {
			return app.CancelExamSitting(ctx, invocation, cancelCommand)
		}},
		{"pause", transition, func(app *App) (ExamSittingView, error) {
			return app.PauseExamSitting(ctx, invocation, transition)
		}},
		{"resume", transition, func(app *App) (ExamSittingView, error) {
			return app.ResumeExamSitting(ctx, invocation, transition)
		}},
		{"close", transition, func(app *App) (ExamSittingView, error) {
			return app.CloseExamSitting(ctx, invocation, transition)
		}},
		{"extend", extend, func(app *App) (ExamSittingView, error) {
			return app.ExtendExamSitting(ctx, invocation, extend)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := &examSittingUseCasesFake{result: result}
			got, err := test.invoke(&App{examSittings: fake})
			if err != nil || got != result || fake.command != test.command {
				t.Fatalf("command = %#v, result = %#v, %v", fake.command, got, err)
			}
			if fake.ctx != ctx || !reflect.DeepEqual(fake.call.Principal(), invocation.Principal()) || fake.call.RequestMetadata() != invocation.RequestMetadata() {
				t.Fatalf("child call lost context, principal, or request metadata: %#v", fake.call)
			}
			principal := fake.call.Principal()
			principal.CredentialScopes[0] = "changed"
			if fake.call.Principal().CredentialScopes[0] != "exam:manage" || invocation.Principal().CredentialScopes[0] != "exam:manage" {
				t.Fatal("child call exposed mutable principal scopes")
			}
		})
	}
}

func TestExamSittingManagerTransitionsConcealTargetsAndMapChildFaults(t *testing.T) {
	t.Parallel()
	invocation := NewInvocation(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{})
	command := PauseExamSittingCommand{ExamID: model.NewExamID(), SittingID: model.NewExamSittingID(), ExpectedRevision: 2, PrivateReason: "reason", IdempotencyKey: "key"}
	tests := []struct {
		name   string
		invoke func(*App) error
		cause  error
		code   string
		field  string
	}{
		{name: "pause conceals missing", cause: &examsitting.Fault{Code: "exam.sitting.not_found"}, code: "resource.not_found", invoke: func(app *App) error {
			_, err := app.PauseExamSitting(context.Background(), invocation, command)
			return err
		}},
		{name: "resume conceals denied", cause: NewError("authorization.denied"), code: "resource.not_found", invoke: func(app *App) error {
			_, err := app.ResumeExamSitting(context.Background(), invocation, command)
			return err
		}},
		{name: "extend maps safe field", cause: &examsitting.Fault{Code: "exam.sitting.invalid", SafeFields: map[string]any{"field": "scheduled_end_at"}}, code: "exam.sitting.invalid", field: "scheduled_end_at", invoke: func(app *App) error {
			_, err := app.ExtendExamSitting(context.Background(), invocation, ExtendExamSittingCommand{
				ExamID: command.ExamID, SittingID: command.SittingID, ExpectedRevision: command.ExpectedRevision,
				ScheduledEndAt: time.Now().Add(time.Hour), PrivateReason: command.PrivateReason, IdempotencyKey: command.IdempotencyKey,
			})
			return err
		}},
		{name: "close maps dependency failure", cause: errors.New("database unavailable"), code: "exam.sitting.unavailable", invoke: func(app *App) error {
			_, err := app.CloseExamSitting(context.Background(), invocation, command)
			return err
		}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := test.invoke(&App{examSittings: &examSittingUseCasesFake{err: test.cause}})
			if !Is(err, test.code) {
				t.Fatalf("error = %v, want %s", err, test.code)
			}
			if test.field != "" {
				mapped, ok := As(err)
				if !ok || mapped.Fields()["field"] != test.field {
					t.Fatalf("safe fields = %#v", mapped)
				}
			}
		})
	}
}

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
	fake := &examSittingUseCasesFake{
		advance: store.ExamSittingLifecycleResult{Value: &store.ExamSittingSnapshot{Sitting: &closing}, Changed: true, Transition: store.ExamSittingTransitionScheduledEndReached},
	}
	result, err := (examSittingLifecycleJobUseCases{sittings: fake}).ReconcileExamSittingLifecycleFromJob(context.Background(), sitting.ID, jobID, attemptID)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.Value.Sitting.State != model.ExamSittingClosing ||
		fake.advanceCall != (examsitting.SystemCall{JobID: jobID, AttemptID: attemptID}) {
		t.Fatalf("result/call = %#v / %#v", result, fake.advanceCall)
	}

	// Another node may close before the lifecycle read. The dedicated sealing
	// handler still owns any closure work, so this adapter returns AdvanceDue's
	// authoritative value and never invokes the legacy zero-Attempt closer.
	racing := &examSittingUseCasesFake{
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

type examSittingUseCasesFake struct {
	examSittingUseCases
	ctx         context.Context
	call        examsitting.Call
	command     any
	result      store.ExamSittingSnapshot
	sittingID   model.ExamSittingID
	update      examsitting.UpdateScheduleCommand
	advance     store.ExamSittingLifecycleResult
	advanceCall examsitting.SystemCall
	err         error
}

func (fake *examSittingUseCasesFake) Schedule(ctx context.Context, call examsitting.Call, command examsitting.ScheduleCommand) (store.ExamSittingSnapshot, error) {
	fake.ctx, fake.call, fake.command = ctx, call, command
	return fake.result, fake.err
}

func (fake *examSittingUseCasesFake) UpdateSchedule(ctx context.Context, call examsitting.Call, command examsitting.UpdateScheduleCommand) (store.ExamSittingSnapshot, error) {
	fake.ctx, fake.call, fake.command, fake.update = ctx, call, command, command
	return fake.result, fake.err
}

func (fake *examSittingUseCasesFake) Cancel(ctx context.Context, call examsitting.Call, command examsitting.CancelCommand) (store.ExamSittingSnapshot, error) {
	fake.ctx, fake.call, fake.command = ctx, call, command
	return fake.result, fake.err
}

func (fake *examSittingUseCasesFake) AuthorizeView(_ context.Context, call examsitting.Call, sittingID model.ExamSittingID) error {
	fake.call, fake.sittingID = call, sittingID
	return fake.err
}

func (fake *examSittingUseCasesFake) Pause(ctx context.Context, call examsitting.Call, command examsitting.PauseCommand) (store.ExamSittingSnapshot, error) {
	fake.ctx, fake.call, fake.command = ctx, call, command
	return fake.result, fake.err
}

func (fake *examSittingUseCasesFake) Resume(ctx context.Context, call examsitting.Call, command examsitting.ResumeCommand) (store.ExamSittingSnapshot, error) {
	fake.ctx, fake.call, fake.command = ctx, call, command
	return fake.result, fake.err
}

func (fake *examSittingUseCasesFake) Extend(ctx context.Context, call examsitting.Call, command examsitting.ExtendCommand) (store.ExamSittingSnapshot, error) {
	fake.ctx, fake.call, fake.command = ctx, call, command
	return fake.result, fake.err
}

func (fake *examSittingUseCasesFake) EarlyClose(ctx context.Context, call examsitting.Call, command examsitting.EarlyCloseCommand) (store.ExamSittingSnapshot, error) {
	fake.ctx, fake.call, fake.command = ctx, call, command
	return fake.result, fake.err
}

func (fake *examSittingUseCasesFake) AdvanceDue(_ context.Context, call examsitting.SystemCall, _ model.ExamSittingID) (store.ExamSittingLifecycleResult, error) {
	fake.advanceCall = call
	return fake.advance, fake.err
}
