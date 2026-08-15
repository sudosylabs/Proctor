// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"testing"
	"time"

	examsitting "github.com/sudosylabs/proctor/server/app/exam/sitting"
	"github.com/sudosylabs/proctor/server/model"
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

type examSittingUseCasesFake struct {
	examSittingUseCases
	call      examsitting.Call
	sittingID model.ExamSittingID
	err       error
}

func (fake *examSittingUseCasesFake) AuthorizeView(_ context.Context, call examsitting.Call, sittingID model.ExamSittingID) error {
	fake.call, fake.sittingID = call, sittingID
	return fake.err
}
