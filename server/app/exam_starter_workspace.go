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

// ListExamStarterWorkspaceQuery names the Exam whose Exam Draft Starter
// Workspace is listed.
type ListExamStarterWorkspaceQuery = examworkspace.ListQuery

// OpenExamStarterWorkspaceFileQuery names one Exam Draft Starter Workspace
// file of an Exam.
type OpenExamStarterWorkspaceFileQuery = examworkspace.OpenFileQuery
type CreateExamStarterWorkspaceDirectoryCommand = examworkspace.CreateDirectoryCommand
type CreateExamStarterWorkspaceFileCommand = examworkspace.CreateFileCommand
type MoveExamStarterWorkspaceEntryCommand = examworkspace.MoveEntryCommand
type ReplaceExamStarterWorkspaceFileCommand = examworkspace.ReplaceFileCommand
type RemoveExamStarterWorkspaceEntryCommand = examworkspace.RemoveEntryCommand

type examStarterWorkspaceRealtimeEffects struct{ realtime *realtimeService }

func (e examStarterWorkspaceRealtimeEffects) Changed(ctx context.Context, examID model.ExamID, entryID model.StarterWorkspaceEntryID, draftRevision int64, operation examworkspace.ChangeOperation, changedAt time.Time) error {
	event, err := apprealtime.NewExamStarterWorkspaceChangedEvent(examID, entryID, draftRevision, string(operation), changedAt)
	if err != nil {
		return err
	}
	return e.realtime.PublishRealtimeEvent(ctx, event)
}
func (e examStarterWorkspaceRealtimeEffects) Report(ctx context.Context, operation string, err error) {
	e.realtime.reportTransientFailure(ctx, operation, err)
}

var _ examworkspace.Authorizer = (*accessControlService)(nil)
var _ examworkspace.Auditor = mutationAuditAdapter{}
var _ examworkspace.Effects = examStarterWorkspaceRealtimeEffects{}
var _ examworkspace.EffectFailures = examStarterWorkspaceRealtimeEffects{}
