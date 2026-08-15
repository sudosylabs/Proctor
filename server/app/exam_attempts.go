// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	examattempt "github.com/sudosylabs/proctor/server/app/exam/attempt"
	examsitting "github.com/sudosylabs/proctor/server/app/exam/sitting"
	apprealtime "github.com/sudosylabs/proctor/server/app/realtime"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type ExamAttemptConnection = examattempt.ConnectionResult
type ExamAttemptConnectionClosed = examattempt.ConnectionClosedResult
type CandidateExamAttemptAccess = examattempt.CandidateAccess
type CandidateExamPresentation = examattempt.Presentation
type CandidateExamWorkspaceItem = store.CandidateAttemptWorkspaceItem
type ExamAttemptManagerView = store.ExamAttemptManagerSnapshot
type OpenedExamAttemptContent = examattempt.OpenedContent

type ConnectExamAttemptCommand struct {
	SittingID            model.ExamSittingID
	ContinuityCredential string
	IdempotencyKey       string
}

type CloseExamAttemptConnectionCommand = examattempt.CloseConnectionCommand

type ListCandidateExamWorkspaceQuery struct {
	Access       CandidateExamAttemptAccess
	AfterPath    string
	AfterEntryID model.AttemptWorkspaceEntryID
	Limit        int
}

type CandidateExamWorkspacePage struct {
	Items   []CandidateExamWorkspaceItem
	HasMore bool
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
	CloseConnection(context.Context, examattempt.Call, examattempt.CloseConnectionCommand) (examattempt.ConnectionClosedResult, error)
	GetPresentation(context.Context, examattempt.Call, examattempt.CandidateAccess) (examattempt.Presentation, error)
	ListWorkspace(context.Context, examattempt.Call, examattempt.WorkspaceQuery) (examattempt.WorkspacePage, error)
	OpenResource(context.Context, examattempt.Call, examattempt.CandidateAccess, model.ExamResourceID) (*examattempt.OpenedContent, error)
	OpenWorkspaceFile(context.Context, examattempt.Call, examattempt.CandidateAccess, model.AttemptWorkspaceEntryID) (*examattempt.OpenedContent, error)
	GetManaged(context.Context, examattempt.Call, examattempt.GetManagedAttemptQuery) (*store.ExamAttemptManagerSnapshot, error)
	ListManaged(context.Context, examattempt.Call, examattempt.ListManagedAttemptsQuery) (examattempt.ManagedAttemptPage, error)
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
		Access: query.Access, AfterPath: query.AfterPath, AfterEntryID: query.AfterEntryID, Limit: query.Limit,
	})
	if err != nil {
		return CandidateExamWorkspacePage{}, examAttemptError(err, true)
	}
	return CandidateExamWorkspacePage{Items: result.Items, HasMore: result.HasMore}, nil
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

type examAttemptManagerAuthorizationAdapter struct{ sittings examSittingUseCases }

func (adapter examAttemptManagerAuthorizationAdapter) AuthorizeSittingView(ctx context.Context, call examattempt.Call, sittingID model.ExamSittingID) error {
	return adapter.sittings.AuthorizeView(ctx, examsitting.NewCall(call.Principal(), call.RequestMetadata()), sittingID)
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

func (effects examAttemptRealtimeEffects) Report(ctx context.Context, operation string, err error) {
	effects.realtime.reportTransientFailure(ctx, operation, err)
}

var _ examattempt.ManagerAuthorizer = examAttemptManagerAuthorizationAdapter{}
var _ examattempt.Auditor = examAttemptAuditAdapter{}
var _ examattempt.Effects = examAttemptRealtimeEffects{}
var _ examattempt.EffectFailures = examAttemptRealtimeEffects{}
