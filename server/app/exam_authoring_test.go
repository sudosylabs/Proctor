// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"testing"
	"time"

	examengine "github.com/sudosylabs/proctor/server/app/exam"
	"github.com/sudosylabs/proctor/server/model"
)

func TestCreateExamBuildsChildCallAndIdempotency(t *testing.T) {
	t.Parallel()
	userID := model.NewUserID()
	principal := testExamPrincipal(userID)
	child := &examUseCasesFake{}
	application := &App{exams: child}
	unitID := model.NewAcademicUnitID()
	view, err := application.CreateExam(context.Background(), NewInvocation(principal, model.RequestMetadata{RequestID: "request-1"}), CreateExamCommand{
		AcademicUnitID: unitID, Title: "Algorithms", IdempotencyKey: "retry-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	if view != child.view || child.call.Principal().UserID != userID || child.call.RequestMetadata().RequestID != "request-1" {
		t.Fatalf("view/call = %#v / %#v", view, child.call)
	}
	if child.create.Idempotency == nil || child.create.Idempotency.Operation != "exam.create.v1" || child.create.Idempotency.UserID != userID {
		t.Fatalf("idempotency = %#v", child.create.Idempotency)
	}
}

func TestCreateExamRequiresIdempotencyKey(t *testing.T) {
	t.Parallel()
	application := &App{exams: &examUseCasesFake{}}
	_, err := application.CreateExam(context.Background(), NewInvocation(testExamPrincipal(model.NewUserID()), model.RequestMetadata{}), CreateExamCommand{AcademicUnitID: model.NewAcademicUnitID(), Title: "Algorithms"})
	if !Is(err, "idempotency.key_required") {
		t.Fatalf("error = %v, want idempotency.key_required", err)
	}
}

func TestGetExamConcealsMissingAndDeniedTargets(t *testing.T) {
	t.Parallel()
	for _, failure := range []error{&examengine.Fault{Code: "exam.not_found"}, NewError("authorization.denied")} {
		child := &examUseCasesFake{err: failure}
		application := &App{exams: child}
		_, err := application.GetExam(context.Background(), NewInvocation(testExamPrincipal(model.NewUserID()), model.RequestMetadata{}), GetExamQuery{ExamID: model.NewExamID()})
		if !Is(err, "resource.not_found") {
			t.Fatalf("GetExam error = %v, want concealed not found", err)
		}
	}
}

type examUseCasesFake struct {
	call   examengine.Call
	create examengine.CreateCommand
	view   ExamView
	err    error
}

func (f *examUseCasesFake) Create(_ context.Context, call examengine.Call, command examengine.CreateCommand) (examengine.View, error) {
	f.call, f.create = call, command
	return f.view, f.err
}

func (f *examUseCasesFake) Get(_ context.Context, call examengine.Call, _ model.ExamID) (examengine.View, error) {
	f.call = call
	return f.view, f.err
}

func testExamPrincipal(userID model.UserID) model.Principal {
	return model.Principal{
		UserID: userID, SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password",
		AuthenticationStrength: model.AuthenticationSingleFactor, ClientType: model.SessionClientWeb,
		AuthenticatedAt: time.Now().UTC(),
	}
}
