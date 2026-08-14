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

func TestEditExamDraftTextBuildsPresenceAwareIdempotentChildCommand(t *testing.T) {
	t.Parallel()
	userID := model.NewUserID()
	child := &examUseCasesFake{}
	application := &App{exams: child}
	examID := model.NewExamID()
	title := "Distributed Systems"
	view, err := application.EditExamDraftText(context.Background(), NewInvocation(testExamPrincipal(userID), model.RequestMetadata{RequestID: "request-edit"}), EditExamDraftTextCommand{
		ExamID: examID, ExpectedDraftRevision: 4, Title: &title, IdempotencyKey: "edit-once",
	})
	if err != nil {
		t.Fatal(err)
	}
	if view != child.view || child.edit.ExamID != examID || child.edit.ExpectedDraftRevision != 4 || child.edit.Title == nil || *child.edit.Title != title || child.edit.InstructionsMarkdown != nil {
		t.Fatalf("view/edit = %#v / %#v", view, child.edit)
	}
	if child.edit.Idempotency == nil || child.edit.Idempotency.Operation != "exam.draft.text.edit.v1" || child.edit.Idempotency.UserID != userID {
		t.Fatalf("idempotency = %#v", child.edit.Idempotency)
	}
}

func TestEditExamDraftTextRequiresIdempotencyKey(t *testing.T) {
	t.Parallel()
	application := &App{exams: &examUseCasesFake{}}
	title := "Algorithms"
	_, err := application.EditExamDraftText(context.Background(), NewInvocation(testExamPrincipal(model.NewUserID()), model.RequestMetadata{}), EditExamDraftTextCommand{
		ExamID: model.NewExamID(), ExpectedDraftRevision: 1, Title: &title,
	})
	if !Is(err, "idempotency.key_required") {
		t.Fatalf("error = %v, want idempotency.key_required", err)
	}
}

func TestEditExamDraftTextFingerprintsNormalizedTitle(t *testing.T) {
	t.Parallel()
	userID := model.NewUserID()
	child := &examUseCasesFake{}
	application := &App{exams: child}
	invocation := NewInvocation(testExamPrincipal(userID), model.RequestMetadata{})
	examID := model.NewExamID()
	plain, padded := "Algorithms", "  Algorithms  "
	var first [32]byte
	for index, title := range []*string{&plain, &padded} {
		if _, err := application.EditExamDraftText(context.Background(), invocation, EditExamDraftTextCommand{
			ExamID: examID, ExpectedDraftRevision: 1, Title: title, IdempotencyKey: "same-key",
		}); err != nil {
			t.Fatal(err)
		}
		if child.edit.Title == nil || *child.edit.Title != plain {
			t.Fatalf("child title = %#v", child.edit.Title)
		}
		if index == 0 {
			first = child.edit.Idempotency.Fingerprint
		}
	}
	if child.edit.Idempotency.Fingerprint != first {
		t.Fatal("semantically identical titles produced different fingerprints")
	}
}

func TestAuthorizeWebSocketExamSubscriptionUsesExamRelationshipGate(t *testing.T) {
	t.Parallel()
	child := &examUseCasesFake{}
	application := &App{exams: child}
	principal := testExamPrincipal(model.NewUserID())
	examID := model.NewExamID()
	err := application.AuthorizeWebSocketSubscription(context.Background(), principal, model.RequestMetadata{RequestID: "subscribe"}, model.ActionExamView, model.Resource{Type: model.ResourceExam, ID: examID.String()})
	if err != nil {
		t.Fatal(err)
	}
	if child.authorizeExamID != examID || child.call.Principal().UserID != principal.UserID {
		t.Fatalf("authorization = %s %#v", child.authorizeExamID, child.call)
	}
}

func TestAuthorizeWebSocketExamSubscriptionConcealsMissingAndDeniedExams(t *testing.T) {
	t.Parallel()
	principal := testExamPrincipal(model.NewUserID())
	resource := model.Resource{Type: model.ResourceExam, ID: model.NewExamID().String()}
	for _, failure := range []error{&examengine.Fault{Code: "exam.not_found"}, NewError("authorization.denied")} {
		application := &App{exams: &examUseCasesFake{err: failure}}
		err := application.AuthorizeWebSocketSubscription(context.Background(), principal, model.RequestMetadata{}, model.ActionExamView, resource)
		if !Is(err, "resource.not_found") {
			t.Fatalf("error = %v, want concealed resource.not_found", err)
		}
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
	call            examengine.Call
	create          examengine.CreateCommand
	edit            examengine.EditDraftTextCommand
	authorizeExamID model.ExamID
	view            ExamView
	err             error
}

func (f *examUseCasesFake) AuthorizeView(_ context.Context, call examengine.Call, examID model.ExamID) error {
	f.call, f.authorizeExamID = call, examID
	return f.err
}

func (f *examUseCasesFake) Create(_ context.Context, call examengine.Call, command examengine.CreateCommand) (examengine.View, error) {
	f.call, f.create = call, command
	return f.view, f.err
}

func (f *examUseCasesFake) Get(_ context.Context, call examengine.Call, _ model.ExamID) (examengine.View, error) {
	f.call = call
	return f.view, f.err
}

func (f *examUseCasesFake) EditDraftText(_ context.Context, call examengine.Call, command examengine.EditDraftTextCommand) (examengine.View, error) {
	f.call, f.edit = call, command
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
