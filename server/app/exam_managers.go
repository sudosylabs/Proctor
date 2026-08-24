// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"time"

	examengine "github.com/sudosylabs/proctor/server/app/exam"
	"github.com/sudosylabs/proctor/server/model"
)

type ListExamManagersQuery struct {
	ExamID          model.ExamID
	BeforeGrantedAt time.Time
	BeforeUserID    model.UserID
	Limit           int
}

type ExamManagerPage = examengine.ManagerPage
type ExamManagerSummary = examengine.ManagerSummary
type ExamManagerChange = examengine.ManagerChange

type AddExamManagerCommand struct {
	ExamID               model.ExamID
	UserID               model.UserID
	ExpectedExamRevision int64
	IdempotencyKey       string
}

type RemoveExamManagerCommand = AddExamManagerCommand
type TransferExamOwnershipCommand = AddExamManagerCommand

func (a *App) ListExamManagers(ctx context.Context, invocation Invocation, query ListExamManagersQuery) (ExamManagerPage, error) {
	page, err := a.exams.ListManagers(ctx, examengine.NewCall(invocation.Principal(), invocation.RequestMetadata()), examengine.ListManagersQuery{
		ExamID: query.ExamID, BeforeGrantedAt: query.BeforeGrantedAt, BeforeUserID: query.BeforeUserID, Limit: query.Limit,
	})
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

type examManagerUseCase func(context.Context, examengine.Call, examengine.AddManagerCommand) (examengine.ManagerChange, error)

func (a *App) runExamManagerCommand(ctx context.Context, invocation Invocation, command AddExamManagerCommand, run examManagerUseCase) (ExamManagerChange, error) {
	result, err := run(ctx, examengine.NewCall(invocation.Principal(), invocation.RequestMetadata()), examengine.AddManagerCommand{
		ExamID: command.ExamID, UserID: command.UserID, ExpectedExamRevision: command.ExpectedExamRevision, IdempotencyKey: command.IdempotencyKey,
	})
	if err != nil {
		return ExamManagerChange{}, examError(err, true)
	}
	return result, nil
}
