// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"

	examengine "github.com/sudosylabs/proctor/server/app/exam"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type CreateExamExportCommand = examengine.CreateExportCommand
type ExamExportQuery = examengine.ExportQuery
type ExamExportDownload = examengine.ExportDownload

type examExportUseCases interface {
	Create(context.Context, examengine.Call, examengine.CreateExportCommand) (*model.ExamExport, error)
	Get(context.Context, examengine.Call, examengine.ExportQuery) (*model.ExamExport, error)
	Open(context.Context, examengine.Call, examengine.ExportQuery) (*examengine.ExportDownload, error)
	BuildFromJob(context.Context, store.ExamExportBuild) error
}

func (a *App) CreateExamExport(ctx context.Context, invocation Invocation, command CreateExamExportCommand) (*model.ExamExport, error) {
	result, err := a.examExports.Create(ctx, examengine.NewCall(invocation.Principal(), invocation.RequestMetadata()), command)
	return result, examError(err, true)
}
func (a *App) GetExamExport(ctx context.Context, invocation Invocation, query ExamExportQuery) (*model.ExamExport, error) {
	result, err := a.examExports.Get(ctx, examengine.NewCall(invocation.Principal(), invocation.RequestMetadata()), query)
	return result, examError(err, true)
}
func (a *App) OpenExamExport(ctx context.Context, invocation Invocation, query ExamExportQuery) (*ExamExportDownload, error) {
	result, err := a.examExports.Open(ctx, examengine.NewCall(invocation.Principal(), invocation.RequestMetadata()), query)
	return result, examError(err, true)
}
