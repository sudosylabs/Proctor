// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"testing"
	"time"

	examsitting "github.com/sudosylabs/proctor/server/app/exam/sitting"
	apprealtime "github.com/sudosylabs/proctor/server/app/realtime"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestExamSittingScheduleUpdateIdempotencyPreservesPatchPresence(t *testing.T) {
	t.Parallel()
	invocation := NewInvocation(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{})
	examID, sittingID := model.NewExamID(), model.NewExamSittingID()
	revisionID, classID := model.NewExamRevisionID(), model.NewClassID()
	first, err := newExamSittingScheduleUpdateIdempotency(invocation, UpdateExamSittingScheduleCommand{
		ExamID: examID, SittingID: sittingID, ExpectedRevision: 2, ExamRevisionID: &revisionID, IdempotencyKey: "same-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := newExamSittingScheduleUpdateIdempotency(invocation, UpdateExamSittingScheduleCommand{
		ExamID: examID, SittingID: sittingID, ExpectedRevision: 2, ClassID: &classID, IdempotencyKey: "same-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Fingerprint == second.Fingerprint {
		t.Fatal("schedule update fingerprint erased patch field presence")
	}

	instant := time.Date(2026, 8, 15, 12, 0, 0, 0, time.FixedZone("fixture", 2*60*60))
	utc := instant.UTC()
	withZone, err := newExamSittingScheduleUpdateIdempotency(invocation, UpdateExamSittingScheduleCommand{
		ExamID: examID, SittingID: sittingID, ExpectedRevision: 2, ScheduledStartAt: &instant, IdempotencyKey: "same-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	withUTC, err := newExamSittingScheduleUpdateIdempotency(invocation, UpdateExamSittingScheduleCommand{
		ExamID: examID, SittingID: sittingID, ExpectedRevision: 2, ScheduledStartAt: &utc, IdempotencyKey: "same-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	if withZone.Fingerprint != withUTC.Fingerprint {
		t.Fatal("equivalent schedule instants produced different fingerprints")
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

func TestExamSittingManagerTransitionsUseOperationSpecificIdempotencyFingerprints(t *testing.T) {
	t.Parallel()
	invocation := NewInvocation(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{RequestID: "manager-transition"})
	examID, sittingID := model.NewExamID(), model.NewExamSittingID()
	base := PauseExamSittingCommand{ExamID: examID, SittingID: sittingID, ExpectedRevision: 3, PrivateReason: "first reason", IdempotencyKey: "same-key"}

	tests := []struct {
		name      string
		operation string
		invoke    func(*App, PauseExamSittingCommand) error
		captured  func(*examSittingUseCasesFake) *store.CommandIdempotency
	}{
		{name: "pause", operation: "exam.sitting.pause.v1", invoke: func(app *App, command PauseExamSittingCommand) error {
			_, err := app.PauseExamSitting(context.Background(), invocation, command)
			return err
		}, captured: func(fake *examSittingUseCasesFake) *store.CommandIdempotency { return fake.pause.Idempotency }},
		{name: "resume", operation: "exam.sitting.resume.v1", invoke: func(app *App, command PauseExamSittingCommand) error {
			_, err := app.ResumeExamSitting(context.Background(), invocation, command)
			return err
		}, captured: func(fake *examSittingUseCasesFake) *store.CommandIdempotency { return fake.resume.Idempotency }},
		{name: "close", operation: "exam.sitting.close.v1", invoke: func(app *App, command PauseExamSittingCommand) error {
			_, err := app.CloseExamSitting(context.Background(), invocation, command)
			return err
		}, captured: func(fake *examSittingUseCasesFake) *store.CommandIdempotency { return fake.close.Idempotency }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			first := &examSittingUseCasesFake{}
			if err := test.invoke(&App{examSittings: first}, base); err != nil {
				t.Fatal(err)
			}
			firstIdempotency := test.captured(first)
			if firstIdempotency == nil || firstIdempotency.Operation != test.operation {
				t.Fatalf("idempotency = %#v", firstIdempotency)
			}
			changed := base
			changed.PrivateReason = "second reason"
			second := &examSittingUseCasesFake{}
			if err := test.invoke(&App{examSittings: second}, changed); err != nil {
				t.Fatal(err)
			}
			if firstIdempotency.Fingerprint == test.captured(second).Fingerprint {
				t.Fatal("private lifecycle reason was absent from the command fingerprint")
			}
		})
	}

	deadline := time.Date(2026, time.August, 16, 14, 0, 0, 0, time.FixedZone("fixture", 2*60*60))
	first := &examSittingUseCasesFake{}
	_, err := (&App{examSittings: first}).ExtendExamSitting(context.Background(), invocation, ExtendExamSittingCommand{
		ExamID: examID, SittingID: sittingID, ExpectedRevision: 3, ScheduledEndAt: deadline,
		PrivateReason: "needed time", IdempotencyKey: "same-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	second := &examSittingUseCasesFake{}
	_, err = (&App{examSittings: second}).ExtendExamSitting(context.Background(), invocation, ExtendExamSittingCommand{
		ExamID: examID, SittingID: sittingID, ExpectedRevision: 3, ScheduledEndAt: deadline.UTC(),
		PrivateReason: "needed time", IdempotencyKey: "same-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.extend.Idempotency == nil || first.extend.Idempotency.Operation != "exam.sitting.extend.v1" ||
		first.extend.Idempotency.Fingerprint != second.extend.Idempotency.Fingerprint {
		t.Fatalf("extension idempotency = %#v / %#v", first.extend.Idempotency, second.extend.Idempotency)
	}
	third := &examSittingUseCasesFake{}
	_, err = (&App{examSittings: third}).ExtendExamSitting(context.Background(), invocation, ExtendExamSittingCommand{
		ExamID: examID, SittingID: sittingID, ExpectedRevision: 3, ScheduledEndAt: deadline.Add(time.Minute),
		PrivateReason: "needed time", IdempotencyKey: "same-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.extend.Idempotency.Fingerprint == third.extend.Idempotency.Fingerprint {
		t.Fatal("extended deadline was absent from the command fingerprint")
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

func TestExamSittingLifecycleJobReconcileClosesAZeroAttemptSitting(t *testing.T) {
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
		closed:  store.ExamSittingLifecycleResult{Value: &store.ExamSittingSnapshot{Sitting: sitting}, Changed: true, Transition: store.ExamSittingTransitionClosedNoAttempts},
	}
	result, err := (examSittingLifecycleJobUseCases{sittings: fake}).ReconcileExamSittingLifecycleFromJob(context.Background(), sitting.ID, jobID, attemptID)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.Value.Sitting.State != model.ExamSittingClosed || fake.closeCalls != 1 ||
		fake.advanceCall != (examsitting.SystemCall{JobID: jobID, AttemptID: attemptID}) || fake.closeCall != fake.advanceCall {
		t.Fatalf("result/calls = %#v / %d / %#v / %#v", result, fake.closeCalls, fake.advanceCall, fake.closeCall)
	}

	// Another node may close after AdvanceDue observes Closing but before this
	// execution reaches CloseIfNoAttempts. Reconciliation must report the
	// latter authoritative snapshot even though that close is a no-op.
	racing := &examSittingUseCasesFake{
		advance: store.ExamSittingLifecycleResult{Value: &store.ExamSittingSnapshot{Sitting: &closing}},
		closed:  store.ExamSittingLifecycleResult{Value: &store.ExamSittingSnapshot{Sitting: sitting}},
	}
	result, err = (examSittingLifecycleJobUseCases{sittings: racing}).ReconcileExamSittingLifecycleFromJob(context.Background(), sitting.ID, jobID, attemptID)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.Value.Sitting.State != model.ExamSittingClosed {
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
	at := time.Date(2026, time.August, 16, 9, 0, 0, 0, time.UTC)
	err := (examSittingRealtimeEffects{realtime: realtime}).LifecycleChanged(context.Background(), examID, sittingID,
		model.ExamSittingPaused, 4, store.ExamSittingTransitionManagerPaused, at.Add(2*time.Hour), at)
	if err != nil {
		t.Fatal(err)
	}
	sink.mu.Lock()
	events := append([]apprealtime.RealtimeEvent(nil), sink.events...)
	sink.mu.Unlock()
	if len(events) != 2 || events[0].Name != "exam_sitting_lifecycle_changed" ||
		events[0].Action != model.ActionExamSittingView ||
		events[1].Name != "exam_sitting_lifecycle_changed" ||
		events[1].Action != model.ActionExamSittingParticipate ||
		string(events[0].Data) != `{"exam_id":"`+examID.String()+`","exam_sitting_id":"`+sittingID.String()+`","state":"paused","revision":4,"reason_code":"manager_paused","scheduled_end_at":"2026-08-16T11:00:00Z","changed_at":"2026-08-16T09:00:00Z"}` ||
		string(events[1].Data) != string(events[0].Data) {
		t.Fatalf("events = %#v", events)
	}
}

type examSittingUseCasesFake struct {
	examSittingUseCases
	call        examsitting.Call
	sittingID   model.ExamSittingID
	pause       examsitting.PauseCommand
	resume      examsitting.ResumeCommand
	extend      examsitting.ExtendCommand
	close       examsitting.EarlyCloseCommand
	advance     store.ExamSittingLifecycleResult
	closed      store.ExamSittingLifecycleResult
	advanceCall examsitting.SystemCall
	closeCall   examsitting.SystemCall
	closeCalls  int
	err         error
}

func (fake *examSittingUseCasesFake) AuthorizeView(_ context.Context, call examsitting.Call, sittingID model.ExamSittingID) error {
	fake.call, fake.sittingID = call, sittingID
	return fake.err
}

func (fake *examSittingUseCasesFake) Pause(_ context.Context, _ examsitting.Call, command examsitting.PauseCommand) (store.ExamSittingSnapshot, error) {
	fake.pause = command
	return store.ExamSittingSnapshot{}, fake.err
}

func (fake *examSittingUseCasesFake) Resume(_ context.Context, _ examsitting.Call, command examsitting.ResumeCommand) (store.ExamSittingSnapshot, error) {
	fake.resume = command
	return store.ExamSittingSnapshot{}, fake.err
}

func (fake *examSittingUseCasesFake) Extend(_ context.Context, _ examsitting.Call, command examsitting.ExtendCommand) (store.ExamSittingSnapshot, error) {
	fake.extend = command
	return store.ExamSittingSnapshot{}, fake.err
}

func (fake *examSittingUseCasesFake) EarlyClose(_ context.Context, _ examsitting.Call, command examsitting.EarlyCloseCommand) (store.ExamSittingSnapshot, error) {
	fake.close = command
	return store.ExamSittingSnapshot{}, fake.err
}

func (fake *examSittingUseCasesFake) AdvanceDue(_ context.Context, call examsitting.SystemCall, _ model.ExamSittingID) (store.ExamSittingLifecycleResult, error) {
	fake.advanceCall = call
	return fake.advance, fake.err
}

func (fake *examSittingUseCasesFake) CloseIfNoAttempts(_ context.Context, call examsitting.SystemCall, _ model.ExamSittingID) (store.ExamSittingLifecycleResult, error) {
	fake.closeCalls++
	fake.closeCall = call
	return fake.closed, fake.err
}
