// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package exam

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/idempotency"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type ListQuery struct {
	AcademicUnitID  model.AcademicUnitID
	Query           string
	ArchiveFilter   store.ExamArchiveFilter
	SittingStates   []model.ExamSittingState
	EndsAfter       time.Time
	StartsBefore    time.Time
	BeforeUpdatedAt time.Time
	BeforeExamID    model.ExamID
	Limit           int
}

type CatalogPage struct {
	Items []store.ExamSummary
}

type ArchiveCommand struct {
	ExamID               model.ExamID
	ExpectedExamRevision int64
	IdempotencyKey       string
}

// ListExams returns one page of the Exams the caller may see, filtered by
// Academic Unit, text, archive state, and Sitting state or window. Limit
// defaults to 50 and the archive filter to active Exams.
func (a *Authoring) ListExams(ctx context.Context, invocation invocation.Invocation, query ListQuery) (_ CatalogPage, resultErr error) {
	defer func() { resultErr = publicFailures.Present(resultErr) }()
	if query.Limit == 0 {
		query.Limit = 50
	}
	if query.ArchiveFilter == "" {
		query.ArchiveFilter = store.ExamArchiveActive
	}
	query.Query = strings.TrimSpace(query.Query)
	principal := invocation.Principal()
	if principal.Validate() != nil || query.Limit < 1 || query.Limit > 200 ||
		(query.BeforeUpdatedAt.IsZero() != query.BeforeExamID.IsZero()) ||
		(query.EndsAfter.IsZero() != query.StartsBefore.IsZero()) || (!query.EndsAfter.IsZero() && !query.EndsAfter.Before(query.StartsBefore)) ||
		(!query.AcademicUnitID.IsZero() && !query.AcademicUnitID.IsValid()) {
		return CatalogPage{}, apperror.Invalid("exam.invalid", "list_query")
	}
	switch query.ArchiveFilter {
	case store.ExamArchiveActive, store.ExamArchiveArchived, store.ExamArchiveAll:
	default:
		return CatalogPage{}, apperror.Invalid("exam.invalid", "archive_filter")
	}
	visibility, err := a.deps.Authorizer.AuthorizeList(ctx, invocation, query.AcademicUnitID)
	if err != nil {
		return CatalogPage{}, err
	}
	visibility.ActorUserID = principal.UserID
	states := make([]model.ExamSittingState, 0, len(query.SittingStates))
	seenStates := make(map[model.ExamSittingState]struct{}, len(query.SittingStates))
	for _, state := range query.SittingStates {
		if !state.IsValid() {
			return CatalogPage{}, apperror.Invalid("exam.invalid", "sitting_state")
		}
		if _, exists := seenStates[state]; exists {
			return CatalogPage{}, apperror.Invalid("exam.invalid", "sitting_state")
		}
		seenStates[state] = struct{}{}
		states = append(states, state)
	}
	items, err := a.deps.Persistence.List(ctx, store.ExamListOptions{AcademicUnitID: query.AcademicUnitID,
		Query: strings.TrimSpace(query.Query), ArchiveFilter: query.ArchiveFilter, BeforeUpdatedAt: query.BeforeUpdatedAt,
		SittingStates: states, EndsAfter: model.TimeUTC(query.EndsAfter), StartsBefore: model.TimeUTC(query.StartsBefore),
		BeforeExamID: query.BeforeExamID, Limit: query.Limit, Visibility: visibility})
	if err != nil {
		return CatalogPage{}, examStoreErrors.Translate(err)
	}
	if items == nil {
		items = []store.ExamSummary{}
	}
	return CatalogPage{Items: items}, nil
}

// ArchiveExam idempotently archives an Exam, fenced by ExpectedExamRevision.
func (a *Authoring) ArchiveExam(ctx context.Context, invocation invocation.Invocation, command ArchiveCommand) (_ model.Exam, resultErr error) {
	defer func() { resultErr = publicFailures.Conceal(resultErr) }()
	principal := invocation.Principal()
	if principal.Validate() != nil || !command.ExamID.IsValid() || command.ExpectedExamRevision < 1 {
		return model.Exam{}, apperror.Invalid("exam.invalid", "exam_revision")
	}
	idempotency, err := idempotency.Require(invocation, idempotencyOperationArchive, command.IdempotencyKey, struct {
		ExamID               string `json:"exam_id"`
		ExpectedExamRevision int64  `json:"expected_exam_revision"`
	}{ExamID: command.ExamID.String(), ExpectedExamRevision: command.ExpectedExamRevision})
	if err != nil {
		return model.Exam{}, err
	}
	at := model.TimeUTC(a.deps.Now())
	access, err := a.deps.Persistence.Access(ctx, command.ExamID, principal.UserID)
	if err != nil {
		return model.Exam{}, examStoreErrors.Translate(err)
	}
	if access == nil || access.Exam == nil {
		return model.Exam{}, apperror.New("exam.unavailable").Wrap(errors.New("exam store returned no access projection"))
	}
	action, err := a.actionForAccess(ctx, principal.UserID, access, at, model.ActionExamManage, model.ActionExamManageOverride)
	if err != nil {
		return model.Exam{}, err
	}
	resource := model.Resource{Type: model.ResourceExam, ID: command.ExamID.String()}
	if err = a.deps.Authorizer.Authorize(ctx, invocation, action, resource); err != nil {
		return model.Exam{}, err
	}
	auditID, err := a.deps.Auditor.BeginAtScope(ctx, invocation, action, resource, model.RoleScopeAcademicUnit, access.Exam.AcademicUnitID.String(), "archive", map[string]any{
		"exam_id": command.ExamID.String(), "expected_exam_revision": command.ExpectedExamRevision,
		"exam_revision": command.ExpectedExamRevision + 1,
	}, nil)
	if err != nil {
		return model.Exam{}, err
	}
	result, err := a.deps.Persistence.Archive(ctx, &store.ExamArchive{ExamID: command.ExamID, ActorUserID: principal.UserID,
		ManagerOverride: action == model.ActionExamManageOverride, ExpectedRevision: command.ExpectedExamRevision,
		ArchivedAt: model.MillisFromTime(at), AuditEventID: auditID, AuditAt: model.MillisFromTime(at)}, idempotency)
	if err != nil {
		mapped := examStoreErrors.Translate(err)
		if auditErr := a.deps.Auditor.Fail(ctx, auditID, apperror.CodeOf(mapped, "exam.unavailable")); auditErr != nil {
			return model.Exam{}, errors.Join(mapped, auditErr)
		}
		return model.Exam{}, mapped
	}
	if result == nil || result.Value == nil {
		return model.Exam{}, apperror.New("exam.unavailable").Wrap(errors.New("exam store returned no archive result"))
	}
	if !result.Replayed {
		if effectErr := a.deps.Effects.Archived(ctx, result.Value.ID, result.Value.Revision, result.Value.ArchivedAt.Time); effectErr != nil {
			a.deps.EffectFailures.Report(ctx, "exam_archived", effectErr)
		}
	}
	return *result.Value, nil
}
