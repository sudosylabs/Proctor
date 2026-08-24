// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"strings"
	"time"

	examengine "github.com/sudosylabs/proctor/server/app/exam"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type ListExamsQuery struct {
	AcademicUnitID  model.AcademicUnitID
	Query           string
	ArchiveFilter   ExamArchiveFilter
	BeforeUpdatedAt time.Time
	BeforeExamID    model.ExamID
	Limit           int
}

type ExamArchiveFilter string

const (
	ExamArchiveActive   ExamArchiveFilter = "active"
	ExamArchiveArchived ExamArchiveFilter = "archived"
	ExamArchiveAll      ExamArchiveFilter = "all"
)

type ExamSummary struct {
	ID             model.ExamID
	AcademicUnitID model.AcademicUnitID
	CreatorUserID  model.UserID
	OwnerUserID    model.UserID
	Title          string
	UpdatedAt      time.Time
	ArchivedAt     model.OptionalTime
	Revision       int64
	ManagerCount   int
}

type ExamCatalogPage struct{ Items []ExamSummary }

type ArchiveExamCommand struct {
	ExamID               model.ExamID
	ExpectedExamRevision int64
	IdempotencyKey       string
}

func (a *App) ListExams(ctx context.Context, invocation Invocation, query ListExamsQuery) (ExamCatalogPage, error) {
	if query.Limit == 0 {
		query.Limit = 50
	}
	if query.ArchiveFilter == "" {
		query.ArchiveFilter = ExamArchiveActive
	}
	page, err := a.exams.List(ctx, examengine.NewCall(invocation.Principal(), invocation.RequestMetadata()), examengine.ListQuery{
		AcademicUnitID: query.AcademicUnitID, Query: strings.TrimSpace(query.Query), ArchiveFilter: store.ExamArchiveFilter(query.ArchiveFilter),
		BeforeUpdatedAt: query.BeforeUpdatedAt, BeforeExamID: query.BeforeExamID, Limit: query.Limit,
	})
	if err != nil {
		return ExamCatalogPage{}, examError(err, false)
	}
	result := ExamCatalogPage{Items: make([]ExamSummary, 0, len(page.Items))}
	for _, item := range page.Items {
		result.Items = append(result.Items, ExamSummary{ID: item.ID, AcademicUnitID: item.AcademicUnitID,
			CreatorUserID: item.CreatorUserID, OwnerUserID: item.OwnerUserID, Title: item.Title,
			UpdatedAt: item.UpdatedAt, ArchivedAt: item.ArchivedAt, Revision: item.Revision, ManagerCount: item.ManagerCount})
	}
	return result, nil
}

func (a *App) ArchiveExam(ctx context.Context, invocation Invocation, command ArchiveExamCommand) (model.Exam, error) {
	exam, err := a.exams.Archive(ctx, examengine.NewCall(invocation.Principal(), invocation.RequestMetadata()), examengine.ArchiveCommand{
		ExamID: command.ExamID, ExpectedExamRevision: command.ExpectedExamRevision, IdempotencyKey: command.IdempotencyKey,
	})
	if err != nil {
		return model.Exam{}, examError(err, true)
	}
	return exam, nil
}
