// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package exam

import (
	"context"
	"errors"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type ListQuery struct {
	AcademicUnitID  model.AcademicUnitID
	ArchiveFilter   store.ExamArchiveFilter
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
	Idempotency          *store.CommandIdempotency
}

func (a *Authoring) List(ctx context.Context, call Call, query ListQuery) (CatalogPage, error) {
	principal := call.Principal()
	if principal.Validate() != nil || query.Limit < 1 || query.Limit > 200 ||
		(query.BeforeUpdatedAt.IsZero() != query.BeforeExamID.IsZero()) ||
		(!query.AcademicUnitID.IsZero() && !query.AcademicUnitID.IsValid()) {
		return CatalogPage{}, invalid("list_query")
	}
	switch query.ArchiveFilter {
	case store.ExamArchiveActive, store.ExamArchiveArchived, store.ExamArchiveAll:
	default:
		return CatalogPage{}, invalid("archive_filter")
	}
	visibility, err := a.authorizer.AuthorizeList(ctx, call, query.AcademicUnitID)
	if err != nil {
		return CatalogPage{}, err
	}
	visibility.ActorUserID = principal.UserID
	items, err := a.persistence.List(ctx, store.ExamListOptions{AcademicUnitID: query.AcademicUnitID,
		ArchiveFilter: query.ArchiveFilter, BeforeUpdatedAt: query.BeforeUpdatedAt,
		BeforeExamID: query.BeforeExamID, Limit: query.Limit, Visibility: visibility})
	if err != nil {
		return CatalogPage{}, mapStoreError(err)
	}
	if items == nil {
		items = []store.ExamSummary{}
	}
	return CatalogPage{Items: items}, nil
}

func (a *Authoring) Archive(ctx context.Context, call Call, command ArchiveCommand) (model.Exam, error) {
	principal := call.Principal()
	if principal.Validate() != nil || !command.ExamID.IsValid() || command.ExpectedExamRevision < 1 {
		return model.Exam{}, invalid("exam_revision")
	}
	if command.Idempotency == nil {
		return model.Exam{}, &Fault{Code: "idempotency.key_required"}
	}
	at := model.TimeUTC(a.now())
	access, err := a.persistence.Access(ctx, command.ExamID, principal.UserID)
	if err != nil {
		return model.Exam{}, mapStoreError(err)
	}
	if access == nil || access.Exam == nil {
		return model.Exam{}, unavailable(errors.New("exam store returned no access projection"))
	}
	action, err := a.actionForAccess(ctx, principal.UserID, access, at, model.ActionExamManage, model.ActionExamManageOverride)
	if err != nil {
		return model.Exam{}, err
	}
	resource := model.Resource{Type: model.ResourceExam, ID: command.ExamID.String()}
	if err := a.authorizer.Authorize(ctx, call, action, resource); err != nil {
		return model.Exam{}, err
	}
	auditID, err := a.auditor.Begin(ctx, call, action, resource, model.RoleScopeAcademicUnit, access.Exam.AcademicUnitID.String(), "archive", map[string]any{
		"exam_id": command.ExamID.String(), "expected_exam_revision": command.ExpectedExamRevision,
		"exam_revision": command.ExpectedExamRevision + 1,
	}, nil)
	if err != nil {
		return model.Exam{}, err
	}
	result, err := a.persistence.Archive(ctx, &store.ExamArchive{ExamID: command.ExamID, ActorUserID: principal.UserID,
		ManagerOverride: action == model.ActionExamManageOverride, ExpectedRevision: command.ExpectedExamRevision,
		ArchivedAt: model.MillisFromTime(at), AuditEventID: auditID, AuditAt: model.MillisFromTime(at)}, command.Idempotency)
	if err != nil {
		mapped := mapStoreError(err)
		var fault *Fault
		if !errors.As(mapped, &fault) {
			fault = &Fault{Code: "exam.unavailable", Cause: mapped}
		}
		if auditErr := a.auditor.Fail(ctx, auditID, fault.Code); auditErr != nil {
			return model.Exam{}, auditErr
		}
		return model.Exam{}, mapped
	}
	if result == nil || result.Value == nil {
		return model.Exam{}, unavailable(errors.New("exam store returned no archive result"))
	}
	if !result.Replayed {
		if effectErr := a.effects.Archived(ctx, result.Value.ID, result.Value.Revision, result.Value.ArchivedAt.Time); effectErr != nil {
			a.failures.Report(ctx, "exam_archived", effectErr)
		}
	}
	return *result.Value, nil
}
