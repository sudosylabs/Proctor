// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	examattempt "github.com/sudosylabs/proctor/server/app/exam/attempt"
	examsitting "github.com/sudosylabs/proctor/server/app/exam/sitting"
	apprealtime "github.com/sudosylabs/proctor/server/app/realtime"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type ExamAttemptConnection = examattempt.ConnectionResult
type ExamAttemptConnectionClosed = examattempt.ConnectionClosedResult
type ExamAttemptParticipationRenewal = examattempt.ParticipationRenewal
type ExamAttemptFocusLossEvaluation = examattempt.FocusLossEvaluation
type ExamAttemptReallowResult = examattempt.ReallowResult
type CandidateExamAttemptAccess = examattempt.CandidateAccess
type CandidateExamPresentation = examattempt.Presentation
type CandidateExamWorkspaceItem = store.CandidateAttemptWorkspaceItem
type ExamAttemptManagerView = store.ExamAttemptManagerSnapshot
type OpenedExamAttemptContent = examattempt.OpenedContent
type ExamAttemptWorkspaceMutationAccess = examattempt.WorkspaceMutationAccess
type ExamAttemptWorkspaceMutationResult = examattempt.WorkspaceMutationResult
type CandidateExamWorkspaceJournalPage = examattempt.WorkspaceJournalPage

type ConnectExamAttemptCommand struct {
	SittingID            model.ExamSittingID
	ContinuityCredential string
	IdempotencyKey       string
}

type CloseExamAttemptConnectionCommand = examattempt.CloseConnectionCommand

type RenewExamAttemptParticipationCommand = examattempt.RenewParticipationCommand

type EvaluateExamAttemptFocusLossCommand = examattempt.FocusLossCommand

type ReallowExamAttemptCommand struct {
	ExamID                  model.ExamID
	SittingID               model.ExamSittingID
	AttemptID               model.ExamAttemptID
	SuspensionID            model.AttemptSuspensionID
	ExpectedAttemptRevision int64
	PrivateReason           string
	IdempotencyKey          string
}

type ListCandidateExamWorkspaceQuery struct {
	Access         CandidateExamAttemptAccess
	ExpectedCursor int64
	AfterEntryID   model.AttemptWorkspaceEntryID
	Limit          int
}

type CandidateExamWorkspacePage struct {
	WorkspaceID     model.ExamAttemptWorkspaceID
	Cursor          int64
	Items           []CandidateExamWorkspaceItem
	HasMore         bool
	RefreshRequired bool
}

type ListCandidateExamWorkspaceJournalQuery = examattempt.WorkspaceJournalQuery

type CreateCandidateExamWorkspaceDirectoryCommand struct {
	Access         ExamAttemptWorkspaceMutationAccess
	Path           string
	IdempotencyKey string
}

type CreateCandidateExamWorkspaceFileCommand struct {
	Access         ExamAttemptWorkspaceMutationAccess
	Path           string
	MediaType      string
	ExpectedSHA256 string
	Body           io.Reader
	Size           int64
	IdempotencyKey string
}

type ReplaceCandidateExamWorkspaceFileCommand struct {
	Access                 ExamAttemptWorkspaceMutationAccess
	EntryID                model.AttemptWorkspaceEntryID
	ExpectedPath           string
	ExpectedContentVersion model.WorkspaceContentVersion
	MediaType              string
	ExpectedSHA256         string
	Body                   io.Reader
	Size                   int64
	IdempotencyKey         string
}

type MoveCandidateExamWorkspaceEntryCommand struct {
	Access          ExamAttemptWorkspaceMutationAccess
	EntryID         model.AttemptWorkspaceEntryID
	ExpectedPath    string
	DestinationPath string
	IdempotencyKey  string
}

type DeleteCandidateExamWorkspaceEntryCommand struct {
	Access                 ExamAttemptWorkspaceMutationAccess
	EntryID                model.AttemptWorkspaceEntryID
	ExpectedPath           string
	ExpectedContentVersion model.WorkspaceContentVersion
	IdempotencyKey         string
}

type OpenCandidateExamResourceQuery struct {
	Access     CandidateExamAttemptAccess
	ResourceID model.ExamResourceID
}

type OpenCandidateExamWorkspaceFileQuery struct {
	Access  CandidateExamAttemptAccess
	EntryID model.AttemptWorkspaceEntryID
}

type GetExamAttemptQuery struct {
	ExamID    model.ExamID
	SittingID model.ExamSittingID
	AttemptID model.ExamAttemptID
}

type ListExamAttemptsQuery struct {
	ExamID          model.ExamID
	SittingID       model.ExamSittingID
	States          []model.ExamAttemptState
	BeforeCreatedAt time.Time
	BeforeAttemptID model.ExamAttemptID
	Limit           int
}

type ExamAttemptManagerPage struct {
	Items   []ExamAttemptManagerView
	HasMore bool
}

type examAttemptUseCases interface {
	Connect(context.Context, examattempt.Call, examattempt.ConnectCommand) (examattempt.ConnectionResult, error)
	RenewParticipation(context.Context, examattempt.Call, examattempt.RenewParticipationCommand) (examattempt.ParticipationRenewal, error)
	EvaluateFocusLoss(context.Context, examattempt.Call, examattempt.FocusLossCommand) (examattempt.FocusLossEvaluation, error)
	Reallow(context.Context, examattempt.Call, examattempt.ReallowCommand) (examattempt.ReallowResult, error)
	ScanExpiredParticipations(context.Context, int) (examattempt.ExpiryScanResult, error)
	CloseConnection(context.Context, examattempt.Call, examattempt.CloseConnectionCommand) (examattempt.ConnectionClosedResult, error)
	GetPresentation(context.Context, examattempt.Call, examattempt.CandidateAccess) (examattempt.Presentation, error)
	ListWorkspace(context.Context, examattempt.Call, examattempt.WorkspaceQuery) (examattempt.WorkspacePage, error)
	ListWorkspaceJournal(context.Context, examattempt.Call, examattempt.WorkspaceJournalQuery) (examattempt.WorkspaceJournalPage, error)
	CreateWorkspaceDirectory(context.Context, examattempt.Call, examattempt.CreateWorkspaceDirectoryCommand) (examattempt.WorkspaceMutationResult, error)
	CreateWorkspaceFile(context.Context, examattempt.Call, examattempt.CreateWorkspaceFileCommand) (examattempt.WorkspaceMutationResult, error)
	ReplaceWorkspaceFile(context.Context, examattempt.Call, examattempt.ReplaceWorkspaceFileCommand) (examattempt.WorkspaceMutationResult, error)
	MoveWorkspaceEntry(context.Context, examattempt.Call, examattempt.MoveWorkspaceEntryCommand) (examattempt.WorkspaceMutationResult, error)
	DeleteWorkspaceEntry(context.Context, examattempt.Call, examattempt.DeleteWorkspaceEntryCommand) (examattempt.WorkspaceMutationResult, error)
	OpenResource(context.Context, examattempt.Call, examattempt.CandidateAccess, model.ExamResourceID) (*examattempt.OpenedContent, error)
	OpenWorkspaceFile(context.Context, examattempt.Call, examattempt.CandidateAccess, model.AttemptWorkspaceEntryID) (*examattempt.OpenedContent, error)
	GetManaged(context.Context, examattempt.Call, examattempt.GetManagedAttemptQuery) (*store.ExamAttemptManagerSnapshot, error)
	ListManaged(context.Context, examattempt.Call, examattempt.ListManagedAttemptsQuery) (examattempt.ManagedAttemptPage, error)
	Submit(context.Context, examattempt.Call, examattempt.SubmitCommand) (examattempt.SubmissionResult, error)
	GetSubmission(context.Context, examattempt.Call, examattempt.GetSubmissionQuery) (*examattempt.ManagedSubmission, error)
	ListSubmissionManifest(context.Context, examattempt.Call, examattempt.ListSubmissionManifestQuery) (examattempt.SubmissionManifestPage, error)
	OpenSubmissionFile(context.Context, examattempt.Call, examattempt.OpenSubmissionFileQuery) (*examattempt.OpenedContent, error)
	ListAutomaticSealTargets(context.Context, model.ExamSittingID, model.ExamAttemptID, int) ([]store.ExamSubmissionAutomaticSealTarget, error)
	SealForSittingClose(context.Context, examattempt.SystemCall, store.ExamSubmissionAutomaticSealTarget) (examattempt.AutomaticSubmissionResult, error)
}

func (a *App) ListCandidateExamWorkspaceJournal(ctx context.Context, invocation Invocation,
	query ListCandidateExamWorkspaceJournalQuery,
) (CandidateExamWorkspaceJournalPage, error) {
	result, err := a.examAttempts.ListWorkspaceJournal(ctx, examattempt.NewCall(invocation.Principal(), invocation.RequestMetadata()), query)
	if err != nil {
		return CandidateExamWorkspaceJournalPage{}, examAttemptError(err, true)
	}
	return result, nil
}

func (a *App) CreateCandidateExamWorkspaceDirectory(ctx context.Context, invocation Invocation,
	command CreateCandidateExamWorkspaceDirectoryCommand,
) (ExamAttemptWorkspaceMutationResult, error) {
	idempotency, err := candidateWorkspaceIdempotency(invocation, command.IdempotencyKey, command.Access,
		model.AttemptWorkspaceMutationCreateDirectory, struct {
			Path string `json:"path"`
		}{command.Path})
	if err != nil {
		return ExamAttemptWorkspaceMutationResult{}, err
	}
	result, err := a.examAttempts.CreateWorkspaceDirectory(ctx, examattempt.NewCall(invocation.Principal(), invocation.RequestMetadata()),
		examattempt.CreateWorkspaceDirectoryCommand{Access: command.Access, Path: command.Path, Idempotency: idempotency})
	if err != nil {
		return ExamAttemptWorkspaceMutationResult{}, examAttemptError(err, true)
	}
	return result, nil
}

func (a *App) CreateCandidateExamWorkspaceFile(ctx context.Context, invocation Invocation,
	command CreateCandidateExamWorkspaceFileCommand,
) (ExamAttemptWorkspaceMutationResult, error) {
	idempotency, err := candidateWorkspaceIdempotency(invocation, command.IdempotencyKey, command.Access,
		model.AttemptWorkspaceMutationCreateFile, struct {
			Path, MediaType, SHA256 string
			Size                    int64
		}{command.Path, command.MediaType, command.ExpectedSHA256, command.Size})
	if err != nil {
		return ExamAttemptWorkspaceMutationResult{}, err
	}
	result, err := a.examAttempts.CreateWorkspaceFile(ctx, examattempt.NewCall(invocation.Principal(), invocation.RequestMetadata()),
		examattempt.CreateWorkspaceFileCommand{Access: command.Access, Path: command.Path, MediaType: command.MediaType,
			ExpectedSHA256: command.ExpectedSHA256, Body: command.Body, Size: command.Size, Idempotency: idempotency})
	if err != nil {
		return ExamAttemptWorkspaceMutationResult{}, examAttemptError(err, true)
	}
	return result, nil
}

func (a *App) ReplaceCandidateExamWorkspaceFile(ctx context.Context, invocation Invocation,
	command ReplaceCandidateExamWorkspaceFileCommand,
) (ExamAttemptWorkspaceMutationResult, error) {
	idempotency, err := candidateWorkspaceIdempotency(invocation, command.IdempotencyKey, command.Access,
		model.AttemptWorkspaceMutationReplaceFile, struct {
			EntryID, Path, Version, MediaType, SHA256 string
			Size                                      int64
		}{command.EntryID.String(), command.ExpectedPath, command.ExpectedContentVersion.String(), command.MediaType, command.ExpectedSHA256, command.Size})
	if err != nil {
		return ExamAttemptWorkspaceMutationResult{}, err
	}
	result, err := a.examAttempts.ReplaceWorkspaceFile(ctx, examattempt.NewCall(invocation.Principal(), invocation.RequestMetadata()),
		examattempt.ReplaceWorkspaceFileCommand{Access: command.Access, EntryID: command.EntryID, ExpectedPath: command.ExpectedPath,
			ExpectedContentVersion: command.ExpectedContentVersion, MediaType: command.MediaType, ExpectedSHA256: command.ExpectedSHA256,
			Body: command.Body, Size: command.Size, Idempotency: idempotency})
	if err != nil {
		return ExamAttemptWorkspaceMutationResult{}, examAttemptError(err, true)
	}
	return result, nil
}

func (a *App) MoveCandidateExamWorkspaceEntry(ctx context.Context, invocation Invocation,
	command MoveCandidateExamWorkspaceEntryCommand,
) (ExamAttemptWorkspaceMutationResult, error) {
	idempotency, err := candidateWorkspaceIdempotency(invocation, command.IdempotencyKey, command.Access,
		model.AttemptWorkspaceMutationMoveEntry, struct{ EntryID, ExpectedPath, DestinationPath string }{
			command.EntryID.String(), command.ExpectedPath, command.DestinationPath})
	if err != nil {
		return ExamAttemptWorkspaceMutationResult{}, err
	}
	result, err := a.examAttempts.MoveWorkspaceEntry(ctx, examattempt.NewCall(invocation.Principal(), invocation.RequestMetadata()),
		examattempt.MoveWorkspaceEntryCommand{Access: command.Access, EntryID: command.EntryID, ExpectedPath: command.ExpectedPath,
			DestinationPath: command.DestinationPath, Idempotency: idempotency})
	if err != nil {
		return ExamAttemptWorkspaceMutationResult{}, examAttemptError(err, true)
	}
	return result, nil
}

func (a *App) DeleteCandidateExamWorkspaceEntry(ctx context.Context, invocation Invocation,
	command DeleteCandidateExamWorkspaceEntryCommand,
) (ExamAttemptWorkspaceMutationResult, error) {
	idempotency, err := candidateWorkspaceIdempotency(invocation, command.IdempotencyKey, command.Access,
		model.AttemptWorkspaceMutationDeleteEntry, struct{ EntryID, ExpectedPath, ExpectedContentVersion string }{
			command.EntryID.String(), command.ExpectedPath, command.ExpectedContentVersion.String()})
	if err != nil {
		return ExamAttemptWorkspaceMutationResult{}, err
	}
	result, err := a.examAttempts.DeleteWorkspaceEntry(ctx, examattempt.NewCall(invocation.Principal(), invocation.RequestMetadata()),
		examattempt.DeleteWorkspaceEntryCommand{Access: command.Access, EntryID: command.EntryID, ExpectedPath: command.ExpectedPath,
			ExpectedContentVersion: command.ExpectedContentVersion, Idempotency: idempotency})
	if err != nil {
		return ExamAttemptWorkspaceMutationResult{}, examAttemptError(err, true)
	}
	return result, nil
}

func candidateWorkspaceIdempotency(invocation Invocation, key string, access ExamAttemptWorkspaceMutationAccess,
	operation model.AttemptWorkspaceMutationKind, semantic any,
) (*store.CommandIdempotency, error) {
	if key == "" {
		return nil, NewError("idempotency.key_required")
	}
	return newCommandIdempotency(invocation, store.ExamAttemptWorkspaceMutationOperation, key, struct {
		AttemptID string `json:"exam_attempt_id"`
		Operation string `json:"operation"`
		Command   any    `json:"command"`
	}{access.AttemptID.String(), string(operation), semantic})
}

func (a *App) ReallowExamAttempt(ctx context.Context, invocation Invocation, command ReallowExamAttemptCommand) (ExamAttemptReallowResult, error) {
	if command.IdempotencyKey == "" {
		return ExamAttemptReallowResult{}, NewError("idempotency.key_required")
	}
	idempotency, err := newCommandIdempotency(invocation, store.ExamAttemptReallowOperation, command.IdempotencyKey, struct {
		ExamID                  string `json:"exam_id"`
		SittingID               string `json:"exam_sitting_id"`
		AttemptID               string `json:"exam_attempt_id"`
		SuspensionID            string `json:"suspension_id"`
		ExpectedAttemptRevision int64  `json:"expected_attempt_revision"`
		PrivateReason           string `json:"private_reason"`
	}{command.ExamID.String(), command.SittingID.String(), command.AttemptID.String(), command.SuspensionID.String(),
		command.ExpectedAttemptRevision, command.PrivateReason})
	if err != nil {
		return ExamAttemptReallowResult{}, err
	}
	result, err := a.examAttempts.Reallow(ctx, examattempt.NewCall(invocation.Principal(), invocation.RequestMetadata()), examattempt.ReallowCommand{
		ExamID: command.ExamID, SittingID: command.SittingID, AttemptID: command.AttemptID, SuspensionID: command.SuspensionID,
		ExpectedAttemptRevision: command.ExpectedAttemptRevision, PrivateReason: command.PrivateReason, Idempotency: idempotency,
	})
	if err != nil {
		return ExamAttemptReallowResult{}, examAttemptError(err, true)
	}
	return result, nil
}

func (a *App) RenewExamAttemptParticipation(ctx context.Context, invocation Invocation, command RenewExamAttemptParticipationCommand) (ExamAttemptParticipationRenewal, error) {
	result, err := a.examAttempts.RenewParticipation(ctx, examattempt.NewCall(invocation.Principal(), invocation.RequestMetadata()), command)
	if err != nil {
		return ExamAttemptParticipationRenewal{}, examAttemptError(err, true)
	}
	return result, nil
}

func (a *App) EvaluateExamAttemptFocusLoss(ctx context.Context, invocation Invocation,
	command EvaluateExamAttemptFocusLossCommand,
) (ExamAttemptFocusLossEvaluation, error) {
	result, err := a.examAttempts.EvaluateFocusLoss(ctx,
		examattempt.NewCall(invocation.Principal(), invocation.RequestMetadata()), command)
	if err != nil {
		return ExamAttemptFocusLossEvaluation{}, examAttemptError(err, true)
	}
	return result, nil
}

func (a *App) ConnectExamAttempt(ctx context.Context, invocation Invocation, command ConnectExamAttemptCommand) (ExamAttemptConnection, error) {
	if command.IdempotencyKey == "" {
		return ExamAttemptConnection{}, NewError("idempotency.key_required")
	}
	idempotency, err := newCommandIdempotency(invocation, store.ExamAttemptConnectOperation, command.IdempotencyKey, struct {
		SittingID                string `json:"exam_sitting_id"`
		SessionID                string `json:"session_id"`
		ContinuityCredentialHash string `json:"continuity_credential_hash"`
	}{command.SittingID.String(), invocation.Principal().SessionID.String(), model.HashToken(command.ContinuityCredential)})
	if err != nil {
		return ExamAttemptConnection{}, err
	}
	result, err := a.examAttempts.Connect(ctx, examattempt.NewCall(invocation.Principal(), invocation.RequestMetadata()), examattempt.ConnectCommand{
		SittingID: command.SittingID, ContinuityCredential: command.ContinuityCredential, Idempotency: idempotency,
	})
	if err != nil {
		return ExamAttemptConnection{}, examAttemptError(err, true)
	}
	return result, nil
}

func (a *App) CloseExamAttemptConnection(ctx context.Context, invocation Invocation, command CloseExamAttemptConnectionCommand) (ExamAttemptConnectionClosed, error) {
	result, err := a.examAttempts.CloseConnection(ctx, examattempt.NewCall(invocation.Principal(), invocation.RequestMetadata()), command)
	if err != nil {
		return ExamAttemptConnectionClosed{}, examAttemptError(err, true)
	}
	return result, nil
}

func (a *App) GetCandidateExamPresentation(ctx context.Context, invocation Invocation, access CandidateExamAttemptAccess) (CandidateExamPresentation, error) {
	result, err := a.examAttempts.GetPresentation(ctx, examattempt.NewCall(invocation.Principal(), invocation.RequestMetadata()), access)
	if err != nil {
		return CandidateExamPresentation{}, examAttemptError(err, true)
	}
	return result, nil
}

func (a *App) ListCandidateExamWorkspace(ctx context.Context, invocation Invocation, query ListCandidateExamWorkspaceQuery) (CandidateExamWorkspacePage, error) {
	result, err := a.examAttempts.ListWorkspace(ctx, examattempt.NewCall(invocation.Principal(), invocation.RequestMetadata()), examattempt.WorkspaceQuery{
		Access: query.Access, ExpectedCursor: query.ExpectedCursor, AfterEntryID: query.AfterEntryID, Limit: query.Limit,
	})
	if err != nil {
		return CandidateExamWorkspacePage{}, examAttemptError(err, true)
	}
	return CandidateExamWorkspacePage{WorkspaceID: result.WorkspaceID, Cursor: result.Cursor,
		Items: result.Items, HasMore: result.HasMore, RefreshRequired: result.RefreshRequired}, nil
}

func (a *App) OpenCandidateExamResource(ctx context.Context, invocation Invocation, query OpenCandidateExamResourceQuery) (OpenedExamAttemptContent, error) {
	result, err := a.examAttempts.OpenResource(ctx, examattempt.NewCall(invocation.Principal(), invocation.RequestMetadata()), query.Access, query.ResourceID)
	if err != nil {
		return OpenedExamAttemptContent{}, examAttemptError(err, true)
	}
	if result == nil {
		return OpenedExamAttemptContent{}, NewError("exam.attempt.unavailable")
	}
	return *result, nil
}

func (a *App) OpenCandidateExamWorkspaceFile(ctx context.Context, invocation Invocation, query OpenCandidateExamWorkspaceFileQuery) (OpenedExamAttemptContent, error) {
	result, err := a.examAttempts.OpenWorkspaceFile(ctx, examattempt.NewCall(invocation.Principal(), invocation.RequestMetadata()), query.Access, query.EntryID)
	if err != nil {
		return OpenedExamAttemptContent{}, examAttemptError(err, true)
	}
	if result == nil {
		return OpenedExamAttemptContent{}, NewError("exam.attempt.unavailable")
	}
	return *result, nil
}

func (a *App) GetExamAttempt(ctx context.Context, invocation Invocation, query GetExamAttemptQuery) (ExamAttemptManagerView, error) {
	result, err := a.examAttempts.GetManaged(ctx, examattempt.NewCall(invocation.Principal(), invocation.RequestMetadata()), examattempt.GetManagedAttemptQuery(query))
	if err != nil {
		return ExamAttemptManagerView{}, examAttemptError(err, true)
	}
	if result == nil {
		return ExamAttemptManagerView{}, NewError("exam.attempt.unavailable")
	}
	return *result, nil
}

func (a *App) ListExamAttempts(ctx context.Context, invocation Invocation, query ListExamAttemptsQuery) (ExamAttemptManagerPage, error) {
	result, err := a.examAttempts.ListManaged(ctx, examattempt.NewCall(invocation.Principal(), invocation.RequestMetadata()), examattempt.ListManagedAttemptsQuery{
		ExamID: query.ExamID, SittingID: query.SittingID, States: append([]model.ExamAttemptState(nil), query.States...),
		BeforeCreatedAt: query.BeforeCreatedAt, BeforeAttemptID: query.BeforeAttemptID, Limit: query.Limit,
	})
	if err != nil {
		return ExamAttemptManagerPage{}, examAttemptError(err, true)
	}
	return ExamAttemptManagerPage{Items: result.Items, HasMore: result.HasMore}, nil
}

func examAttemptError(err error, conceal bool) error {
	if err == nil {
		return nil
	}
	if existing, ok := As(err); ok {
		if conceal && existing.Code() == "authorization.denied" {
			return NewError("resource.not_found").Wrap(err)
		}
		return err
	}
	var fault *examattempt.Fault
	if !errors.As(err, &fault) {
		return NewError("exam.attempt.unavailable").Wrap(err)
	}
	if conceal && (fault.Code == "exam.attempt.not_found" || fault.Code == "exam.attempt.continuity_invalid") {
		return NewError("resource.not_found").Wrap(err)
	}
	mapped := NewError(fault.Code)
	for key, value := range fault.SafeFields {
		mapped.WithField(key, fmt.Sprint(value))
	}
	return mapped.Wrap(err)
}

type examAttemptManagerAuthorizationAdapter struct {
	sittings    examSittingUseCases
	submissions store.ExamSubmissionStore
}

func (adapter examAttemptManagerAuthorizationAdapter) AuthorizeSittingView(ctx context.Context, call examattempt.Call, sittingID model.ExamSittingID) error {
	return adapter.sittings.AuthorizeView(ctx, examsitting.NewCall(call.Principal(), call.RequestMetadata()), sittingID)
}

func (adapter examAttemptManagerAuthorizationAdapter) AuthorizeSittingManage(ctx context.Context, call examattempt.Call, sittingID model.ExamSittingID) (bool, error) {
	return adapter.sittings.AuthorizeManage(ctx, examsitting.NewCall(call.Principal(), call.RequestMetadata()), sittingID)
}

func (adapter examAttemptManagerAuthorizationAdapter) AuthorizeSubmissionView(ctx context.Context, call examattempt.Call,
	submissionID model.SubmissionID,
) error {
	if adapter.submissions == nil || !submissionID.IsValid() {
		return &examattempt.Fault{Code: "exam.attempt.unavailable", Cause: errors.New("Submission authorization dependencies are invalid")}
	}
	authorization, err := adapter.submissions.Resolve(ctx, submissionID)
	if err != nil {
		if store.IsNotFound(err) {
			return &examattempt.Fault{Code: "exam.attempt.not_found", Cause: err}
		}
		return &examattempt.Fault{Code: "exam.attempt.unavailable", Cause: err}
	}
	if authorization == nil || authorization.SubmissionID != submissionID || !authorization.ExamID.IsValid() ||
		!authorization.SittingID.IsValid() || !authorization.AttemptID.IsValid() || !authorization.AcademicUnitID.IsValid() {
		return &examattempt.Fault{Code: "exam.attempt.unavailable", Cause: errors.New("Submission authorization projection is incomplete")}
	}
	return adapter.sittings.AuthorizeSubmissionView(ctx, examsitting.NewCall(call.Principal(), call.RequestMetadata()),
		authorization.ExamID, submissionID)
}

type examAttemptAuditAdapter struct{ audit mutationAuditAdapter }

func (adapter examAttemptAuditAdapter) Begin(ctx context.Context, call examattempt.Call, action model.Action, resource model.Resource,
	scopeType model.RoleScopeType, scopeID, operation string, value map[string]any,
) (string, error) {
	return adapter.audit.BeginAtScope(ctx, NewInvocation(call.Principal(), call.RequestMetadata()), action, resource,
		scopeType, scopeID, operation, value, nil)
}

func (adapter examAttemptAuditAdapter) Fail(ctx context.Context, id, code string) error {
	return adapter.audit.Fail(ctx, id, code)
}

type examAttemptRealtimeEffects struct{ realtime *realtimeService }

func (effects examAttemptRealtimeEffects) ConnectionOpened(ctx context.Context, result examattempt.ConnectionResult) error {
	event, err := apprealtime.NewExamAttemptConnectionOpenedEvent(result.Attempt.SittingID,
		result.Attempt.ID, result.Attempt.CandidateUserID, result.Connection.ID, result.Connection.OpenedAt)
	if err != nil {
		return err
	}
	return effects.realtime.Publish(ctx, event)
}

func (effects examAttemptRealtimeEffects) ConnectionClosed(ctx context.Context, result examattempt.ConnectionClosedResult) error {
	event, err := apprealtime.NewExamAttemptConnectionClosedEvent(result.SittingID, result.AttemptID,
		result.CandidateUserID, result.Connection.ID, result.Connection.CloseReason, result.Connection.ClosedAt.Time)
	if err != nil {
		return err
	}
	return effects.realtime.Publish(ctx, event)
}

func (effects examAttemptRealtimeEffects) ParticipationExpired(ctx context.Context, result examattempt.ParticipationExpiry) error {
	managerEvent, err := apprealtime.NewExamAttemptSuspendedEvent(result.SittingID, result.Attempt.ID, result.CandidateUserID,
		result.Connection.ID, result.Flag.ID, result.Suspension.ID, result.Attempt.Revision, result.DatabaseTime)
	if err != nil {
		return err
	}
	candidateEvent, err := apprealtime.NewCandidateExamAttemptSuspendedEvent(result.SittingID, result.Attempt.ID,
		result.CandidateUserID, result.Suspension.CandidateReason, result.DatabaseTime)
	if err != nil {
		return err
	}
	if !result.ConnectionClosed {
		return errors.Join(effects.realtime.Publish(ctx, managerEvent), effects.realtime.Publish(ctx, candidateEvent))
	}
	connectionEvent, err := apprealtime.NewExamAttemptConnectionClosedEvent(result.SittingID, result.Attempt.ID,
		result.CandidateUserID, result.Connection.ID, result.Connection.CloseReason, result.Connection.ClosedAt.Time)
	if err != nil {
		return err
	}
	return errors.Join(effects.realtime.Publish(ctx, connectionEvent), effects.realtime.Publish(ctx, managerEvent),
		effects.realtime.Publish(ctx, candidateEvent))
}

func (effects examAttemptRealtimeEffects) AttemptReallowed(ctx context.Context, result examattempt.ReallowResult) error {
	managerEvent, err := apprealtime.NewExamAttemptReallowedEvent(result.SittingID, result.Attempt.ID, result.CandidateUserID,
		result.Suspension.ID, result.Attempt.Revision, result.Attempt.UpdatedAt)
	if err != nil {
		return err
	}
	candidateEvent, err := apprealtime.NewCandidateExamAttemptReallowedEvent(result.SittingID, result.Attempt.ID,
		result.CandidateUserID, result.Attempt.UpdatedAt)
	if err != nil {
		return err
	}
	return errors.Join(effects.realtime.Publish(ctx, managerEvent), effects.realtime.Publish(ctx, candidateEvent))
}

func (effects examAttemptRealtimeEffects) WorkspaceChanged(ctx context.Context, result examattempt.WorkspaceMutationResult) error {
	event, err := apprealtime.NewCandidateExamAttemptWorkspaceChangedEvent(result.SittingID, result.AttemptID,
		result.CandidateUserID, result.Change.EntryID, result.Change.Operation, result.Change.Cursor, result.Change.ChangedAt)
	if err != nil {
		return err
	}
	return effects.realtime.Publish(ctx, event)
}

func (effects examAttemptRealtimeEffects) FocusLossEvaluated(ctx context.Context, result examattempt.FocusLossEvaluation) error {
	events := make([]apprealtime.RealtimeEvent, 0, 5)
	if result.ConnectionClosed {
		event, err := apprealtime.NewExamAttemptConnectionClosedEvent(result.SittingID, result.AttemptID,
			result.CandidateUserID, result.Connection.ID, result.Connection.CloseReason, result.Connection.ClosedAt.Time)
		if err != nil {
			return err
		}
		events = append(events, event)
	}
	if result.ManagerNotificationRequired {
		event, err := apprealtime.NewExamAttemptIntegrityFlaggedEvent(result.SittingID, result.AttemptID,
			result.CandidateUserID, result.Flag.ID, result.PolicyOutcome, result.RetainedEvidenceCount,
			result.EvidenceOverflowCount, result.ReceivedAt)
		if err != nil {
			return err
		}
		events = append(events, event)
	}
	if result.SuspensionCreated {
		managerEvent, err := apprealtime.NewExamAttemptSuspendedEvent(result.SittingID, result.AttemptID,
			result.CandidateUserID, result.Connection.ID, result.Flag.ID, result.Suspension.ID,
			result.Attempt.Revision, result.ReceivedAt)
		if err != nil {
			return err
		}
		candidateEvent, err := apprealtime.NewCandidateExamAttemptSuspendedEvent(result.SittingID, result.AttemptID,
			result.CandidateUserID, result.Suspension.CandidateReason, result.ReceivedAt)
		if err != nil {
			return err
		}
		events = append(events, managerEvent, candidateEvent)
	}
	if result.CandidateWarningCreated {
		event, err := apprealtime.NewCandidateExamAttemptFocusLossWarningEvent(result.SittingID, result.AttemptID,
			result.CandidateUserID, result.ReceivedAt)
		if err != nil {
			return err
		}
		events = append(events, event)
	}
	var joined error
	for _, event := range events {
		joined = errors.Join(joined, effects.realtime.Publish(ctx, event))
	}
	return joined
}

func (effects examAttemptRealtimeEffects) AttemptSubmitted(ctx context.Context, result examattempt.SubmissionResult) error {
	publishErr, constructionErr := effects.publishExamAttemptSubmittedFacts(ctx, result)
	if constructionErr != nil {
		return constructionErr
	}
	return errors.Join(publishErr, effects.realtime.UnbindExamAttemptConnection(ctx, result.ConnectionID))
}

func (effects examAttemptRealtimeEffects) publishExamAttemptSubmittedFacts(ctx context.Context,
	result examattempt.SubmissionResult,
) (publishErr, constructionErr error) {
	managerEvent, err := apprealtime.NewExamAttemptSubmittedEvent(result.SittingID, result.Receipt.AttemptID,
		result.CandidateUserID, result.Receipt.SubmissionID, result.Receipt.WorkspaceCursor,
		result.Receipt.ManifestDigest, result.Receipt.SubmittedAt)
	if err != nil {
		return nil, err
	}
	candidateEvent, err := apprealtime.NewCandidateExamAttemptSubmittedEvent(result.SittingID, result.Receipt.AttemptID,
		result.CandidateUserID, result.Receipt.SubmissionID, result.Receipt.WorkspaceCursor,
		result.Receipt.ManifestDigest, result.Receipt.SubmittedAt)
	if err != nil {
		return nil, err
	}
	return errors.Join(effects.realtime.Publish(ctx, managerEvent), effects.realtime.Publish(ctx, candidateEvent)), nil
}

func (effects examAttemptRealtimeEffects) AttemptSealedForSittingClose(ctx context.Context, result examattempt.AutomaticSubmissionResult) error {
	publishErr, constructionErr := effects.publishExamAttemptSubmittedFacts(ctx, result.SubmissionResult)
	if constructionErr != nil {
		return constructionErr
	}
	if result.ConnectionClosed {
		connectionEvent, eventErr := apprealtime.NewExamAttemptConnectionClosedEvent(result.SittingID,
			result.Receipt.AttemptID, result.CandidateUserID, result.ConnectionID,
			model.AttemptConnectionCloseSittingClosed, result.Receipt.SubmittedAt)
		if eventErr != nil {
			return eventErr
		}
		publishErr = errors.Join(publishErr, effects.realtime.Publish(ctx, connectionEvent))
	}
	return errors.Join(publishErr, effects.realtime.UnbindExamAttemptConnection(ctx, result.ConnectionID))
}

func (effects examAttemptRealtimeEffects) Report(ctx context.Context, operation string, err error) {
	effects.realtime.reportTransientFailure(ctx, operation, err)
}

var _ examattempt.ManagerAuthorizer = examAttemptManagerAuthorizationAdapter{}
var _ examattempt.Auditor = examAttemptAuditAdapter{}
var _ examattempt.Effects = examAttemptRealtimeEffects{}
var _ examattempt.EffectFailures = examAttemptRealtimeEffects{}
