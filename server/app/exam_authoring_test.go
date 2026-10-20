// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/app/apperror"
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
	if !reflect.DeepEqual(view, child.view) || child.invocation.Principal().UserID != userID || child.invocation.RequestMetadata().RequestID != "request-1" {
		t.Fatalf("view/call = %#v / %#v", view, child.invocation)
	}
	if child.create.IdempotencyKey != "retry-key" {
		t.Fatalf("idempotency key = %q", child.create.IdempotencyKey)
	}
}

func TestCreateExamRequiresIdempotencyKey(t *testing.T) {
	t.Parallel()
	application := &App{exams: &examUseCasesFake{err: apperror.New("idempotency.key_required")}}
	_, err := application.CreateExam(context.Background(), NewInvocation(testExamPrincipal(model.NewUserID()), model.RequestMetadata{}), CreateExamCommand{AcademicUnitID: model.NewAcademicUnitID(), Title: "Algorithms"})
	if !apperror.Is(err, "idempotency.key_required") {
		t.Fatalf("error = %v, want idempotency.key_required", err)
	}
}

func TestListExamsNormalizesTitleSearchForChildUseCase(t *testing.T) {
	t.Parallel()
	child := &examUseCasesFake{}
	application := &App{exams: child}
	_, err := application.ListExams(context.Background(), NewInvocation(testExamPrincipal(model.NewUserID()), model.RequestMetadata{}), ListExamsQuery{
		Query: "  Distributed Systems  ", ArchiveFilter: ExamArchiveActive, Limit: 25,
	})
	if err != nil {
		t.Fatal(err)
	}
	if child.list.Query != "Distributed Systems" || child.list.Limit != 25 {
		t.Fatalf("child list query = %#v", child.list)
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
	if !reflect.DeepEqual(view, child.view) || child.edit.ExamID != examID || child.edit.ExpectedDraftRevision != 4 || child.edit.Title == nil || *child.edit.Title != title || child.edit.InstructionsMarkdown != nil {
		t.Fatalf("view/edit = %#v / %#v", view, child.edit)
	}
	if child.edit.IdempotencyKey != "edit-once" {
		t.Fatalf("idempotency key = %q", child.edit.IdempotencyKey)
	}
}

func TestEditExamDraftTextRequiresIdempotencyKey(t *testing.T) {
	t.Parallel()
	application := &App{exams: &examUseCasesFake{err: apperror.New("idempotency.key_required")}}
	title := "Algorithms"
	_, err := application.EditExamDraftText(context.Background(), NewInvocation(testExamPrincipal(model.NewUserID()), model.RequestMetadata{}), EditExamDraftTextCommand{
		ExamID: model.NewExamID(), ExpectedDraftRevision: 1, Title: &title,
	})
	if !apperror.Is(err, "idempotency.key_required") {
		t.Fatalf("error = %v, want idempotency.key_required", err)
	}
}

func TestEditExamDraftTextForwardsRawTitleAndKey(t *testing.T) {
	t.Parallel()
	userID := model.NewUserID()
	child := &examUseCasesFake{}
	application := &App{exams: child}
	invocation := NewInvocation(testExamPrincipal(userID), model.RequestMetadata{})
	examID := model.NewExamID()
	padded := "  Algorithms  "
	if _, err := application.EditExamDraftText(context.Background(), invocation, EditExamDraftTextCommand{
		ExamID: examID, ExpectedDraftRevision: 1, Title: &padded, IdempotencyKey: "same-key",
	}); err != nil {
		t.Fatal(err)
	}
	if child.edit.Title == nil || *child.edit.Title != padded || child.edit.IdempotencyKey != "same-key" {
		t.Fatalf("child command = %#v", child.edit)
	}
}

func TestConfigureExamDraftFocusLossBuildsTypedIdempotentChildCommand(t *testing.T) {
	t.Parallel()
	userID := model.NewUserID()
	child := &examUseCasesFake{}
	application := &App{exams: child}
	examID := model.NewExamID()
	view, err := application.ConfigureExamDraftFocusLoss(context.Background(), NewInvocation(testExamPrincipal(userID), model.RequestMetadata{RequestID: "focus-policy"}), ConfigureExamDraftFocusLossCommand{
		ExamID: examID, ExpectedDraftRevision: 4, Enabled: false,
		MinimumDuration: 500*time.Millisecond + time.Nanosecond, IncidentCount: 100,
		Window: 4*time.Hour + time.Nanosecond, Outcome: model.IntegrityOutcomeFlagAndSuspend,
		IdempotencyKey: "configure-focus-once",
	})
	if err != nil {
		t.Fatal(err)
	}
	got := child.focusLoss
	if !reflect.DeepEqual(view, child.view) || got.ExamID != examID || got.ExpectedDraftRevision != 4 || got.FocusLoss != (model.FocusLossPolicy{
		Enabled: false, MinimumDuration: 500*time.Millisecond + time.Nanosecond, IncidentCount: 100, Window: 4*time.Hour + time.Nanosecond, Outcome: model.IntegrityOutcomeFlagAndSuspend,
	}) {
		t.Fatalf("view/command = %#v / %#v", view, got)
	}
	if got.IdempotencyKey != "configure-focus-once" {
		t.Fatalf("idempotency key = %q", got.IdempotencyKey)
	}
}

func TestConfigureExamDraftFocusLossRequiresIdempotencyKey(t *testing.T) {
	t.Parallel()
	application := &App{exams: &examUseCasesFake{err: apperror.New("idempotency.key_required")}}
	_, err := application.ConfigureExamDraftFocusLoss(context.Background(), NewInvocation(testExamPrincipal(model.NewUserID()), model.RequestMetadata{}), ConfigureExamDraftFocusLossCommand{
		ExamID: model.NewExamID(), ExpectedDraftRevision: 1, Enabled: true, MinimumDuration: time.Second,
		IncidentCount: 1, Window: time.Minute, Outcome: model.IntegrityOutcomeFlag,
	})
	if !apperror.Is(err, "idempotency.key_required") {
		t.Fatalf("error = %v, want idempotency.key_required", err)
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
	if child.authorizeExamID != examID || child.invocation.Principal().UserID != principal.UserID {
		t.Fatalf("authorization = %s %#v", child.authorizeExamID, child.invocation)
	}
}

func TestAuthorizeWebSocketExamSubscriptionConcealsMissingAndDeniedExams(t *testing.T) {
	t.Parallel()
	principal := testExamPrincipal(model.NewUserID())
	resource := model.Resource{Type: model.ResourceExam, ID: model.NewExamID().String()}
	for _, failure := range []error{apperror.New("exam.not_found"), apperror.New("authorization.denied")} {
		application := &App{exams: &examUseCasesFake{err: failure}}
		err := application.AuthorizeWebSocketSubscription(context.Background(), principal, model.RequestMetadata{}, model.ActionExamView, resource)
		if !apperror.Is(err, "resource.not_found") {
			t.Fatalf("error = %v, want concealed resource.not_found", err)
		}
	}
}

func TestGetExamConcealsMissingAndDeniedTargets(t *testing.T) {
	t.Parallel()
	for _, failure := range []error{apperror.New("exam.not_found"), apperror.New("authorization.denied")} {
		child := &examUseCasesFake{err: failure}
		application := &App{exams: child}
		_, err := application.GetExam(context.Background(), NewInvocation(testExamPrincipal(model.NewUserID()), model.RequestMetadata{}), GetExamQuery{ExamID: model.NewExamID()})
		if !apperror.Is(err, "resource.not_found") {
			t.Fatalf("GetExam error = %v, want concealed not found", err)
		}
	}
}

type examUseCasesFake struct {
	nativePolicy    examengine.ConfigureDraftNativePolicyCommand
	invocation      Invocation
	create          examengine.CreateCommand
	edit            examengine.EditDraftTextCommand
	focusLoss       examengine.ConfigureDraftFocusLossCommand
	browserPolicy   examengine.ConfigureDraftBrowserPolicyCommand
	list            examengine.ListQuery
	catalog         examengine.CatalogPage
	archive         examengine.ArchiveCommand
	managerList     examengine.ListManagersQuery
	managerCommand  examengine.AddManagerCommand
	managerPage     examengine.ManagerPage
	managerChange   examengine.ManagerChange
	archived        model.Exam
	authorizeExamID model.ExamID
	view            ExamView
	err             error
}

func (f *examUseCasesFake) ListManagers(_ context.Context, invocation Invocation, query examengine.ListManagersQuery) (examengine.ManagerPage, error) {
	f.invocation, f.managerList = invocation, query
	return f.managerPage, f.err
}
func (f *examUseCasesFake) AddManager(_ context.Context, invocation Invocation, command examengine.AddManagerCommand) (examengine.ManagerChange, error) {
	f.invocation, f.managerCommand = invocation, command
	return f.managerChange, f.err
}
func (f *examUseCasesFake) RemoveManager(_ context.Context, invocation Invocation, command examengine.RemoveManagerCommand) (examengine.ManagerChange, error) {
	f.invocation, f.managerCommand = invocation, command
	return f.managerChange, f.err
}
func (f *examUseCasesFake) TransferOwner(_ context.Context, invocation Invocation, command examengine.TransferOwnerCommand) (examengine.ManagerChange, error) {
	f.invocation, f.managerCommand = invocation, command
	return f.managerChange, f.err
}

func (f *examUseCasesFake) List(_ context.Context, invocation Invocation, query examengine.ListQuery) (examengine.CatalogPage, error) {
	f.invocation, f.list = invocation, query
	return f.catalog, f.err
}

func (f *examUseCasesFake) Archive(_ context.Context, invocation Invocation, command examengine.ArchiveCommand) (model.Exam, error) {
	f.invocation, f.archive = invocation, command
	return f.archived, f.err
}

func (f *examUseCasesFake) AuthorizeView(_ context.Context, invocation Invocation, examID model.ExamID) error {
	f.invocation, f.authorizeExamID = invocation, examID
	return f.err
}

func (f *examUseCasesFake) Create(_ context.Context, invocation Invocation, command examengine.CreateCommand) (examengine.View, error) {
	f.invocation, f.create = invocation, command
	return f.view, f.err
}

func (f *examUseCasesFake) Get(_ context.Context, invocation Invocation, _ model.ExamID) (examengine.View, error) {
	f.invocation = invocation
	return f.view, f.err
}

func (f *examUseCasesFake) EditDraftText(_ context.Context, invocation Invocation, command examengine.EditDraftTextCommand) (examengine.View, error) {
	f.invocation, f.edit = invocation, command
	return f.view, f.err
}

func (f *examUseCasesFake) ConfigureDraftFocusLoss(_ context.Context, invocation Invocation, command examengine.ConfigureDraftFocusLossCommand) (examengine.View, error) {
	f.invocation, f.focusLoss = invocation, command
	return f.view, f.err
}

func (f *examUseCasesFake) ConfigureDraftBrowserPolicy(_ context.Context, invocation Invocation, command examengine.ConfigureDraftBrowserPolicyCommand) (examengine.View, error) {
	f.invocation, f.browserPolicy = invocation, command
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

func (f *examUseCasesFake) ConfigureDraftNativePolicy(_ context.Context, invocation Invocation, command examengine.ConfigureDraftNativePolicyCommand) (examengine.View, error) {
	f.invocation, f.nativePolicy = invocation, command
	return f.view, f.err
}
