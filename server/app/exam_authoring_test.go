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
	if child.create.IdempotencyKey != "retry-key" {
		t.Fatalf("idempotency key = %q", child.create.IdempotencyKey)
	}
}

func TestCreateExamRequiresIdempotencyKey(t *testing.T) {
	t.Parallel()
	application := &App{exams: &examUseCasesFake{err: &examengine.Fault{Code: "idempotency.key_required"}}}
	_, err := application.CreateExam(context.Background(), NewInvocation(testExamPrincipal(model.NewUserID()), model.RequestMetadata{}), CreateExamCommand{AcademicUnitID: model.NewAcademicUnitID(), Title: "Algorithms"})
	if !Is(err, "idempotency.key_required") {
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
	if view != child.view || child.edit.ExamID != examID || child.edit.ExpectedDraftRevision != 4 || child.edit.Title == nil || *child.edit.Title != title || child.edit.InstructionsMarkdown != nil {
		t.Fatalf("view/edit = %#v / %#v", view, child.edit)
	}
	if child.edit.IdempotencyKey != "edit-once" {
		t.Fatalf("idempotency key = %q", child.edit.IdempotencyKey)
	}
}

func TestEditExamDraftTextRequiresIdempotencyKey(t *testing.T) {
	t.Parallel()
	application := &App{exams: &examUseCasesFake{err: &examengine.Fault{Code: "idempotency.key_required"}}}
	title := "Algorithms"
	_, err := application.EditExamDraftText(context.Background(), NewInvocation(testExamPrincipal(model.NewUserID()), model.RequestMetadata{}), EditExamDraftTextCommand{
		ExamID: model.NewExamID(), ExpectedDraftRevision: 1, Title: &title,
	})
	if !Is(err, "idempotency.key_required") {
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
	if view != child.view || got.ExamID != examID || got.ExpectedDraftRevision != 4 || got.FocusLoss != (model.FocusLossPolicy{
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
	application := &App{exams: &examUseCasesFake{err: &examengine.Fault{Code: "idempotency.key_required"}}}
	_, err := application.ConfigureExamDraftFocusLoss(context.Background(), NewInvocation(testExamPrincipal(model.NewUserID()), model.RequestMetadata{}), ConfigureExamDraftFocusLossCommand{
		ExamID: model.NewExamID(), ExpectedDraftRevision: 1, Enabled: true, MinimumDuration: time.Second,
		IncidentCount: 1, Window: time.Minute, Outcome: model.IntegrityOutcomeFlag,
	})
	if !Is(err, "idempotency.key_required") {
		t.Fatalf("error = %v, want idempotency.key_required", err)
	}
}

func TestConfigureExamDraftExecutionProfileBuildsTypedIdempotentChildCommand(t *testing.T) {
	t.Parallel()
	userID := model.NewUserID()
	child := &examUseCasesFake{}
	application := &App{exams: child}
	examID := model.NewExamID()
	view, err := application.ConfigureExamDraftExecutionProfile(context.Background(), NewInvocation(testExamPrincipal(userID), model.RequestMetadata{RequestID: "execution-profile"}), ConfigureExamDraftExecutionProfileCommand{
		ExamID: examID, ExpectedDraftRevision: 7, Enabled: true, Image: "golang-1.24", Network: model.ExecutionNetworkAllowlist,
		IdempotencyKey: "configure-execution-once",
	})
	if err != nil {
		t.Fatal(err)
	}
	got := child.executionProfile
	if view != child.view || got.ExamID != examID || got.ExpectedDraftRevision != 7 || got.Profile != (model.ExecutionProfile{
		Enabled: true, Image: "golang-1.24", Network: model.ExecutionNetworkAllowlist,
	}) {
		t.Fatalf("view/command = %#v / %#v", view, got)
	}
	if got.IdempotencyKey != "configure-execution-once" {
		t.Fatalf("idempotency key = %q", got.IdempotencyKey)
	}
}

func TestConfigureExamDraftExecutionProfileRequiresIdempotencyKey(t *testing.T) {
	t.Parallel()
	application := &App{exams: &examUseCasesFake{err: &examengine.Fault{Code: "idempotency.key_required"}}}
	_, err := application.ConfigureExamDraftExecutionProfile(context.Background(), NewInvocation(testExamPrincipal(model.NewUserID()), model.RequestMetadata{}), ConfigureExamDraftExecutionProfileCommand{
		ExamID: model.NewExamID(), ExpectedDraftRevision: 1, Enabled: true, Image: "golang-1.24", Network: model.ExecutionNetworkNone,
	})
	if !Is(err, "idempotency.key_required") {
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
	call             examengine.Call
	create           examengine.CreateCommand
	edit             examengine.EditDraftTextCommand
	focusLoss        examengine.ConfigureDraftFocusLossCommand
	executionProfile examengine.ConfigureDraftExecutionProfileCommand
	list             examengine.ListQuery
	catalog          examengine.CatalogPage
	archive          examengine.ArchiveCommand
	managerList      examengine.ListManagersQuery
	managerCommand   examengine.AddManagerCommand
	managerPage      examengine.ManagerPage
	managerChange    examengine.ManagerChange
	archived         model.Exam
	authorizeExamID  model.ExamID
	view             ExamView
	err              error
}

func (f *examUseCasesFake) ListManagers(_ context.Context, call examengine.Call, query examengine.ListManagersQuery) (examengine.ManagerPage, error) {
	f.call, f.managerList = call, query
	return f.managerPage, f.err
}
func (f *examUseCasesFake) AddManager(_ context.Context, call examengine.Call, command examengine.AddManagerCommand) (examengine.ManagerChange, error) {
	f.call, f.managerCommand = call, command
	return f.managerChange, f.err
}
func (f *examUseCasesFake) RemoveManager(_ context.Context, call examengine.Call, command examengine.RemoveManagerCommand) (examengine.ManagerChange, error) {
	f.call, f.managerCommand = call, command
	return f.managerChange, f.err
}
func (f *examUseCasesFake) TransferOwner(_ context.Context, call examengine.Call, command examengine.TransferOwnerCommand) (examengine.ManagerChange, error) {
	f.call, f.managerCommand = call, command
	return f.managerChange, f.err
}

func (f *examUseCasesFake) List(_ context.Context, call examengine.Call, query examengine.ListQuery) (examengine.CatalogPage, error) {
	f.call, f.list = call, query
	return f.catalog, f.err
}

func (f *examUseCasesFake) Archive(_ context.Context, call examengine.Call, command examengine.ArchiveCommand) (model.Exam, error) {
	f.call, f.archive = call, command
	return f.archived, f.err
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

func (f *examUseCasesFake) ConfigureDraftFocusLoss(_ context.Context, call examengine.Call, command examengine.ConfigureDraftFocusLossCommand) (examengine.View, error) {
	f.call, f.focusLoss = call, command
	return f.view, f.err
}

func (f *examUseCasesFake) ConfigureDraftExecutionProfile(_ context.Context, call examengine.Call, command examengine.ConfigureDraftExecutionProfileCommand) (examengine.View, error) {
	f.call, f.executionProfile = call, command
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
