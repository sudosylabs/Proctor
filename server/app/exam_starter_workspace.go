// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
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
type CreateExamStarterWorkspaceDirectoryCommand = examworkspace.CreateDirectoryCommand
type CreateExamStarterWorkspaceFileCommand = examworkspace.CreateFileCommand
type MoveExamStarterWorkspaceEntryCommand = examworkspace.MoveEntryCommand
type ReplaceExamStarterWorkspaceFileCommand = examworkspace.ReplaceFileCommand
type RemoveExamStarterWorkspaceEntryCommand = examworkspace.RemoveEntryCommand

type examStarterWorkspaceUseCases interface {
	List(context.Context, Invocation, model.ExamID) ([]store.ExamStarterWorkspaceItem, error)
	OpenFile(context.Context, Invocation, model.ExamID, model.StarterWorkspaceEntryID) (*examworkspace.OpenedFile, error)
	CreateDirectory(context.Context, Invocation, examworkspace.CreateDirectoryCommand) (examworkspace.Result, error)
	CreateFile(context.Context, Invocation, examworkspace.CreateFileCommand) (examworkspace.Result, error)
	MoveEntry(context.Context, Invocation, examworkspace.MoveEntryCommand) (examworkspace.Result, error)
	ReplaceFile(context.Context, Invocation, examworkspace.ReplaceFileCommand) (examworkspace.Result, error)
	RemoveEntry(context.Context, Invocation, examworkspace.RemoveEntryCommand) (examworkspace.Result, error)
}

func (a *App) ListExamStarterWorkspace(ctx context.Context, invocation Invocation, query ListExamStarterWorkspaceQuery) ([]ExamStarterWorkspaceItem, error) {
	items, err := a.examStarterWorkspace.List(ctx, invocation, query.ExamID)
	if err != nil {
		return nil, examStarterWorkspaceError(err, true)
	}
	return items, nil
}

func (a *App) OpenExamStarterWorkspaceFile(ctx context.Context, invocation Invocation, query OpenExamStarterWorkspaceFileQuery) (OpenedExamStarterWorkspaceFile, error) {
	opened, err := a.examStarterWorkspace.OpenFile(ctx, invocation, query.ExamID, query.EntryID)
	if err != nil {
		return OpenedExamStarterWorkspaceFile{}, examStarterWorkspaceError(err, true)
	}
	return *opened, nil
}

func (a *App) CreateExamStarterWorkspaceDirectory(ctx context.Context, invocation Invocation, command CreateExamStarterWorkspaceDirectoryCommand) (ExamStarterWorkspaceResult, error) {
	result, err := a.examStarterWorkspace.CreateDirectory(ctx, invocation, command)
	if err != nil {
		return ExamStarterWorkspaceResult{}, examStarterWorkspaceError(err, true)
	}
	return result, nil
}

func (a *App) CreateExamStarterWorkspaceFile(ctx context.Context, invocation Invocation, command CreateExamStarterWorkspaceFileCommand) (ExamStarterWorkspaceResult, error) {
	result, err := a.examStarterWorkspace.CreateFile(ctx, invocation, command)
	if err != nil {
		return ExamStarterWorkspaceResult{}, examStarterWorkspaceError(err, true)
	}
	return result, nil
}

func (a *App) MoveExamStarterWorkspaceEntry(ctx context.Context, invocation Invocation, command MoveExamStarterWorkspaceEntryCommand) (ExamStarterWorkspaceResult, error) {
	result, err := a.examStarterWorkspace.MoveEntry(ctx, invocation, command)
	if err != nil {
		return ExamStarterWorkspaceResult{}, examStarterWorkspaceError(err, true)
	}
	return result, nil
}

func (a *App) ReplaceExamStarterWorkspaceFile(ctx context.Context, invocation Invocation, command ReplaceExamStarterWorkspaceFileCommand) (ExamStarterWorkspaceResult, error) {
	result, err := a.examStarterWorkspace.ReplaceFile(ctx, invocation, command)
	if err != nil {
		return ExamStarterWorkspaceResult{}, examStarterWorkspaceError(err, true)
	}
	return result, nil
}

func (a *App) RemoveExamStarterWorkspaceEntry(ctx context.Context, invocation Invocation, command RemoveExamStarterWorkspaceEntryCommand) (ExamStarterWorkspaceResult, error) {
	result, err := a.examStarterWorkspace.RemoveEntry(ctx, invocation, command)
	if err != nil {
		return ExamStarterWorkspaceResult{}, examStarterWorkspaceError(err, true)
	}
	return result, nil
}

func examStarterWorkspaceError(err error, conceal bool) error {
	return presentExamChildError(err, conceal, "exam.starter_workspace.unavailable", "exam.starter_workspace.not_found")
}

type examStarterWorkspaceRealtimeEffects struct{ realtime *realtimeService }

func (effects examStarterWorkspaceRealtimeEffects) Changed(ctx context.Context, examID model.ExamID, entryID model.StarterWorkspaceEntryID, draftRevision int64, operation examworkspace.ChangeOperation, changedAt time.Time) error {
	event, err := apprealtime.NewExamStarterWorkspaceChangedEvent(examID, entryID, draftRevision, string(operation), changedAt)
	if err != nil {
		return err
	}
	return effects.realtime.PublishRealtimeEvent(ctx, event)
}
func (effects examStarterWorkspaceRealtimeEffects) Report(ctx context.Context, operation string, err error) {
	effects.realtime.reportTransientFailure(ctx, operation, err)
}

var _ examworkspace.Authorizer = (*accessControlService)(nil)
var _ examworkspace.Auditor = mutationAuditAdapter{}
var _ examworkspace.Effects = examStarterWorkspaceRealtimeEffects{}
var _ examworkspace.EffectFailures = examStarterWorkspaceRealtimeEffects{}
