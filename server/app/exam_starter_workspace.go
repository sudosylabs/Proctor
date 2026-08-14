// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	examworkspace "github.com/sudosylabs/proctor/server/app/exam/workspace"
	apprealtime "github.com/sudosylabs/proctor/server/app/realtime"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type ExamStarterWorkspaceItem = store.ExamStarterWorkspaceItem
type ExamStarterWorkspaceResult = examworkspace.Result
type OpenedExamStarterWorkspaceFile = examworkspace.OpenedFile

type ListExamStarterWorkspaceQuery struct{ ExamID model.ExamID }
type OpenExamStarterWorkspaceFileQuery struct {
	ExamID  model.ExamID
	EntryID model.StarterWorkspaceEntryID
}
type CreateExamStarterWorkspaceDirectoryCommand struct {
	ExamID                model.ExamID
	ExpectedDraftRevision int64
	Path                  string
	IdempotencyKey        string
}
type CreateExamStarterWorkspaceFileCommand struct {
	ExamID                model.ExamID
	ExpectedDraftRevision int64
	Path                  string
	MediaType             string
	ExpectedSHA256        string
	Body                  io.Reader
	Size                  int64
	IdempotencyKey        string
}
type MoveExamStarterWorkspaceEntryCommand struct {
	ExamID                model.ExamID
	EntryID               model.StarterWorkspaceEntryID
	ExpectedDraftRevision int64
	Path                  string
	IdempotencyKey        string
}
type ReplaceExamStarterWorkspaceFileCommand struct {
	ExamID                 model.ExamID
	EntryID                model.StarterWorkspaceEntryID
	ExpectedDraftRevision  int64
	ExpectedContentVersion model.WorkspaceContentVersion
	MediaType              string
	ExpectedSHA256         string
	Body                   io.Reader
	Size                   int64
	IdempotencyKey         string
}
type RemoveExamStarterWorkspaceEntryCommand struct {
	ExamID                model.ExamID
	EntryID               model.StarterWorkspaceEntryID
	ExpectedDraftRevision int64
	IdempotencyKey        string
}

type examStarterWorkspaceUseCases interface {
	List(context.Context, examworkspace.Call, model.ExamID) ([]store.ExamStarterWorkspaceItem, error)
	OpenFile(context.Context, examworkspace.Call, model.ExamID, model.StarterWorkspaceEntryID) (*examworkspace.OpenedFile, error)
	CreateDirectory(context.Context, examworkspace.Call, examworkspace.CreateDirectoryCommand) (examworkspace.Result, error)
	CreateFile(context.Context, examworkspace.Call, examworkspace.CreateFileCommand) (examworkspace.Result, error)
	MoveEntry(context.Context, examworkspace.Call, examworkspace.MoveEntryCommand) (examworkspace.Result, error)
	ReplaceFile(context.Context, examworkspace.Call, examworkspace.ReplaceFileCommand) (examworkspace.Result, error)
	RemoveEntry(context.Context, examworkspace.Call, examworkspace.RemoveEntryCommand) (examworkspace.Result, error)
}

func (a *App) ListExamStarterWorkspace(ctx context.Context, invocation Invocation, query ListExamStarterWorkspaceQuery) ([]ExamStarterWorkspaceItem, error) {
	items, err := a.examStarterWorkspace.List(ctx, examworkspace.NewCall(invocation.Principal(), invocation.RequestMetadata()), query.ExamID)
	if err != nil {
		return nil, examStarterWorkspaceError(err, true)
	}
	return items, nil
}

func (a *App) OpenExamStarterWorkspaceFile(ctx context.Context, invocation Invocation, query OpenExamStarterWorkspaceFileQuery) (OpenedExamStarterWorkspaceFile, error) {
	opened, err := a.examStarterWorkspace.OpenFile(ctx, examworkspace.NewCall(invocation.Principal(), invocation.RequestMetadata()), query.ExamID, query.EntryID)
	if err != nil {
		return OpenedExamStarterWorkspaceFile{}, examStarterWorkspaceError(err, true)
	}
	return *opened, nil
}

func (a *App) CreateExamStarterWorkspaceDirectory(ctx context.Context, invocation Invocation, command CreateExamStarterWorkspaceDirectoryCommand) (ExamStarterWorkspaceResult, error) {
	idempotency, err := newExamStarterWorkspaceIdempotency(invocation, "exam.starter_workspace.directory.create.v1", command.IdempotencyKey,
		command.ExamID, command.ExpectedDraftRevision, "", "", command.Path, "", 0, "")
	if err != nil {
		return ExamStarterWorkspaceResult{}, err
	}
	result, err := a.examStarterWorkspace.CreateDirectory(ctx, examworkspace.NewCall(invocation.Principal(), invocation.RequestMetadata()), examworkspace.CreateDirectoryCommand{
		ExamID: command.ExamID, ExpectedDraftRevision: command.ExpectedDraftRevision, Path: command.Path, Idempotency: idempotency})
	if err != nil {
		return ExamStarterWorkspaceResult{}, examStarterWorkspaceError(err, true)
	}
	return result, nil
}

func (a *App) CreateExamStarterWorkspaceFile(ctx context.Context, invocation Invocation, command CreateExamStarterWorkspaceFileCommand) (ExamStarterWorkspaceResult, error) {
	idempotency, err := newExamStarterWorkspaceIdempotency(invocation, "exam.starter_workspace.file.create.v1", command.IdempotencyKey,
		command.ExamID, command.ExpectedDraftRevision, "", "", command.Path, command.MediaType, command.Size, command.ExpectedSHA256)
	if err != nil {
		return ExamStarterWorkspaceResult{}, err
	}
	result, err := a.examStarterWorkspace.CreateFile(ctx, examworkspace.NewCall(invocation.Principal(), invocation.RequestMetadata()), examworkspace.CreateFileCommand{
		ExamID: command.ExamID, ExpectedDraftRevision: command.ExpectedDraftRevision, Path: command.Path, MediaType: command.MediaType,
		ExpectedSHA256: command.ExpectedSHA256, Body: command.Body, Size: command.Size, Idempotency: idempotency})
	if err != nil {
		return ExamStarterWorkspaceResult{}, examStarterWorkspaceError(err, true)
	}
	return result, nil
}

func (a *App) MoveExamStarterWorkspaceEntry(ctx context.Context, invocation Invocation, command MoveExamStarterWorkspaceEntryCommand) (ExamStarterWorkspaceResult, error) {
	idempotency, err := newExamStarterWorkspaceIdempotency(invocation, "exam.starter_workspace.entry.move.v1", command.IdempotencyKey,
		command.ExamID, command.ExpectedDraftRevision, "", command.EntryID.String(), command.Path, "", 0, "")
	if err != nil {
		return ExamStarterWorkspaceResult{}, err
	}
	result, err := a.examStarterWorkspace.MoveEntry(ctx, examworkspace.NewCall(invocation.Principal(), invocation.RequestMetadata()), examworkspace.MoveEntryCommand{
		ExamID: command.ExamID, EntryID: command.EntryID, ExpectedDraftRevision: command.ExpectedDraftRevision, Path: command.Path, Idempotency: idempotency})
	if err != nil {
		return ExamStarterWorkspaceResult{}, examStarterWorkspaceError(err, true)
	}
	return result, nil
}

func (a *App) ReplaceExamStarterWorkspaceFile(ctx context.Context, invocation Invocation, command ReplaceExamStarterWorkspaceFileCommand) (ExamStarterWorkspaceResult, error) {
	idempotency, err := newExamStarterWorkspaceIdempotency(invocation, "exam.starter_workspace.file.replace.v1", command.IdempotencyKey,
		command.ExamID, command.ExpectedDraftRevision, command.ExpectedContentVersion, command.EntryID.String(), "", command.MediaType, command.Size, command.ExpectedSHA256)
	if err != nil {
		return ExamStarterWorkspaceResult{}, err
	}
	result, err := a.examStarterWorkspace.ReplaceFile(ctx, examworkspace.NewCall(invocation.Principal(), invocation.RequestMetadata()), examworkspace.ReplaceFileCommand{
		ExamID: command.ExamID, EntryID: command.EntryID, ExpectedDraftRevision: command.ExpectedDraftRevision, ExpectedContentVersion: command.ExpectedContentVersion, MediaType: command.MediaType,
		ExpectedSHA256: command.ExpectedSHA256, Body: command.Body, Size: command.Size, Idempotency: idempotency})
	if err != nil {
		return ExamStarterWorkspaceResult{}, examStarterWorkspaceError(err, true)
	}
	return result, nil
}

func (a *App) RemoveExamStarterWorkspaceEntry(ctx context.Context, invocation Invocation, command RemoveExamStarterWorkspaceEntryCommand) (ExamStarterWorkspaceResult, error) {
	idempotency, err := newExamStarterWorkspaceIdempotency(invocation, "exam.starter_workspace.entry.remove.v1", command.IdempotencyKey,
		command.ExamID, command.ExpectedDraftRevision, "", command.EntryID.String(), "", "", 0, "")
	if err != nil {
		return ExamStarterWorkspaceResult{}, err
	}
	result, err := a.examStarterWorkspace.RemoveEntry(ctx, examworkspace.NewCall(invocation.Principal(), invocation.RequestMetadata()), examworkspace.RemoveEntryCommand{
		ExamID: command.ExamID, EntryID: command.EntryID, ExpectedDraftRevision: command.ExpectedDraftRevision, Idempotency: idempotency})
	if err != nil {
		return ExamStarterWorkspaceResult{}, examStarterWorkspaceError(err, true)
	}
	return result, nil
}

func newExamStarterWorkspaceIdempotency(invocation Invocation, operation, key string, examID model.ExamID, revision int64,
	expectedContentVersion model.WorkspaceContentVersion, entryID, path, mediaType string, size int64, sha256 string) (*store.CommandIdempotency, error) {
	if key == "" {
		return nil, NewError("idempotency.key_required")
	}
	return newCommandIdempotency(invocation, operation, key, struct {
		ExamID                 string `json:"exam_id"`
		ExpectedDraftRevision  int64  `json:"expected_draft_revision"`
		ExpectedContentVersion string `json:"expected_content_version,omitempty"`
		EntryID                string `json:"entry_id,omitempty"`
		Path                   string `json:"path,omitempty"`
		MediaType              string `json:"media_type,omitempty"`
		Size                   int64  `json:"size,omitempty"`
		SHA256                 string `json:"sha256,omitempty"`
	}{examID.String(), revision, expectedContentVersion.String(), entryID, path, mediaType, size, sha256})
}

func examStarterWorkspaceError(err error, conceal bool) error {
	if err == nil {
		return nil
	}
	if existing, ok := As(err); ok {
		if conceal && existing.Code() == "authorization.denied" {
			return NewError("resource.not_found").Wrap(err)
		}
		return err
	}
	var fault *examworkspace.Fault
	if !errors.As(err, &fault) {
		return NewError("exam.starter_workspace.unavailable").Wrap(err)
	}
	if conceal && fault.Code == "exam.starter_workspace.not_found" {
		return NewError("resource.not_found").Wrap(err)
	}
	mapped := NewError(fault.Code)
	for key, value := range fault.SafeFields {
		mapped.WithField(key, fmt.Sprint(value))
	}
	return mapped.Wrap(err)
}

type examStarterWorkspaceAuthorizationAdapter struct{ authorization *accessControlService }

func (a examStarterWorkspaceAuthorizationAdapter) Authorize(ctx context.Context, call examworkspace.Call, action model.Action, resource model.Resource) error {
	return a.authorization.authorizeCurrentState(ctx, call.Principal(), action, resource, call.RequestMetadata())
}

type examStarterWorkspaceAuditAdapter struct{ audit mutationAuditAdapter }

func (a examStarterWorkspaceAuditAdapter) Begin(ctx context.Context, call examworkspace.Call, action model.Action, resource model.Resource, scopeType model.RoleScopeType, scopeID, operation string, value, prior map[string]any) (string, error) {
	return a.audit.BeginAtScope(ctx, NewInvocation(call.Principal(), call.RequestMetadata()), action, resource, scopeType, scopeID, operation, value, prior)
}
func (a examStarterWorkspaceAuditAdapter) Fail(ctx context.Context, id, code string) error {
	return a.audit.Fail(ctx, id, code)
}

type examStarterWorkspaceRealtimeEffects struct{ realtime *realtimeService }

func (effects examStarterWorkspaceRealtimeEffects) Changed(ctx context.Context, examID model.ExamID, entryID model.StarterWorkspaceEntryID, draftRevision int64, operation examworkspace.ChangeOperation, changedAt time.Time) error {
	event, err := apprealtime.NewExamStarterWorkspaceChangedEvent(examID, entryID, draftRevision, string(operation), changedAt)
	if err != nil {
		return err
	}
	return effects.realtime.Publish(ctx, event)
}
func (effects examStarterWorkspaceRealtimeEffects) Report(ctx context.Context, operation string, err error) {
	effects.realtime.reportTransientFailure(ctx, operation, err)
}

var _ examworkspace.Authorizer = examStarterWorkspaceAuthorizationAdapter{}
var _ examworkspace.Auditor = examStarterWorkspaceAuditAdapter{}
var _ examworkspace.Effects = examStarterWorkspaceRealtimeEffects{}
var _ examworkspace.EffectFailures = examStarterWorkspaceRealtimeEffects{}
