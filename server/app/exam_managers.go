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
)

type ListExamManagersQuery = examengine.ListManagersQuery

type ExamManagerPage = examengine.ManagerPage
type ExamManagerSummary = examengine.ManagerSummary
type ExamManagerChange = examengine.ManagerChange

type AddExamManagerCommand = examengine.AddManagerCommand

type RemoveExamManagerCommand = examengine.RemoveManagerCommand
type TransferExamOwnershipCommand = examengine.TransferOwnerCommand

func (a *App) ListExamManagers(ctx context.Context, invocation Invocation, query ListExamManagersQuery) (ExamManagerPage, error) {
	page, err := a.exams.ListManagers(ctx, invocation, query)
	if err != nil {
		return ExamManagerPage{}, examError(err, true)
	}
	return page, nil
}

func (a *App) AddExamManager(ctx context.Context, invocation Invocation, command AddExamManagerCommand) (ExamManagerChange, error) {
	return a.runExamManagerCommand(ctx, invocation, command, a.exams.AddManager)
}

func (a *App) RemoveExamManager(ctx context.Context, invocation Invocation, command RemoveExamManagerCommand) (ExamManagerChange, error) {
	return a.runExamManagerCommand(ctx, invocation, command, a.exams.RemoveManager)
}

func (a *App) TransferExamOwnership(ctx context.Context, invocation Invocation, command TransferExamOwnershipCommand) (ExamManagerChange, error) {
	return a.runExamManagerCommand(ctx, invocation, command, a.exams.TransferOwner)
}

type examManagerUseCase func(context.Context, Invocation, examengine.AddManagerCommand) (examengine.ManagerChange, error)

func (a *App) runExamManagerCommand(ctx context.Context, invocation Invocation, command AddExamManagerCommand, run examManagerUseCase) (ExamManagerChange, error) {
	result, err := run(ctx, invocation, command)
	if err != nil {
		return ExamManagerChange{}, examError(err, true)
	}
	return result, nil
}
