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

type PublishRevisionCommand struct {
	ExamID                model.ExamID
	ExpectedDraftRevision int64
	IdempotencyKey        string
}

type ListRevisionsQuery struct {
	ExamID           model.ExamID
	BeforeNumber     int64
	BeforeRevisionID model.ExamRevisionID
	Limit            int
}

type RevisionPage struct {
	Items   []store.ExamRevisionSummary
	HasMore bool
}

type PublicationEffects interface {
	RevisionPublished(context.Context, store.ExamRevisionSummary) error
}

// Publication owns the authorization, audit, persistence and after-commit
// effect recipe for freezing Drafts into immutable Exam Revisions.
type Publication struct {
	store       store.ExamRevisionStore
	access      store.ExamAuthoringStore
	memberships memberships
	authorizer  Authorizer
	auditor     Auditor
	effects     PublicationEffects
	failures    EffectFailures
	now         func() time.Time
	newID       func() model.ExamRevisionID
}

func NewPublication(persistence store.ExamRevisionStore, access store.ExamAuthoringStore, memberships memberships, authorizer Authorizer, auditor Auditor, effects PublicationEffects, failures EffectFailures, now func() time.Time, newID func() model.ExamRevisionID) (*Publication, error) {
	if persistence == nil || access == nil || memberships == nil || authorizer == nil || auditor == nil || effects == nil || failures == nil || now == nil || newID == nil {
		return nil, errors.New("Exam Revision publication dependencies are required")
	}
	return &Publication{store: persistence, access: access, memberships: memberships, authorizer: authorizer, auditor: auditor, effects: effects, failures: failures, now: now, newID: newID}, nil
}

func (p *Publication) Publish(ctx context.Context, call Call, command PublishRevisionCommand) (store.ExamRevisionSummary, error) {
	principal := call.Principal()
	if principal.Validate() != nil || !command.ExamID.IsValid() || command.ExpectedDraftRevision < 1 {
		return store.ExamRevisionSummary{}, invalid("publication")
	}
	idempotency, err := prepareIdempotency(call, idempotencyOperationPublishRevision, command.IdempotencyKey, struct {
		ExamID                string `json:"exam_id"`
		ExpectedDraftRevision int64  `json:"expected_draft_revision"`
	}{command.ExamID.String(), command.ExpectedDraftRevision})
	if err != nil {
		return store.ExamRevisionSummary{}, err
	}
	at := model.TimeUTC(p.now())
	revisionID := p.newID()
	if !revisionID.IsValid() {
		return store.ExamRevisionSummary{}, invalid("revision_id")
	}
	access, err := p.access.Access(ctx, command.ExamID, principal.UserID)
	if err != nil {
		return store.ExamRevisionSummary{}, mapStoreError(err)
	}
	if access == nil || access.Exam == nil {
		return store.ExamRevisionSummary{}, unavailable(errors.New("Exam access projection is incomplete"))
	}
	action, err := actionForAccess(ctx, p.memberships, principal.UserID, access, at, model.ActionExamPublish, model.ActionExamPublishOverride)
	if err != nil {
		return store.ExamRevisionSummary{}, err
	}
	resource := model.Resource{Type: model.ResourceExam, ID: command.ExamID.String()}
	if err = p.authorizer.Authorize(ctx, call, action, resource); err != nil {
		return store.ExamRevisionSummary{}, err
	}
	auditID, err := p.auditor.Begin(ctx, call, action, resource, model.RoleScopeAcademicUnit, access.Exam.AcademicUnitID.String(), "publish", map[string]any{
		"exam_id": command.ExamID.String(), "expected_draft_revision": command.ExpectedDraftRevision,
	}, nil)
	if err != nil {
		return store.ExamRevisionSummary{}, err
	}
	result, err := p.store.Publish(ctx, &store.ExamRevisionPublication{RevisionID: revisionID, ExamID: command.ExamID,
		ActorUserID: principal.UserID, ManagerOverride: action == model.ActionExamPublishOverride,
		ExpectedDraftRevision: command.ExpectedDraftRevision, Kind: model.ExamRevisionPublicationStandard,
		PublishedAt: at, AuditEventID: auditID, AuditAt: model.MillisFromTime(at)}, idempotency)
	if err != nil {
		mapped := mapStoreError(err)
		var fault *Fault
		if !errors.As(mapped, &fault) {
			fault = &Fault{Code: "exam.unavailable", Cause: mapped}
		}
		if auditErr := p.auditor.Fail(ctx, auditID, fault.Code); auditErr != nil {
			return store.ExamRevisionSummary{}, auditErr
		}
		return store.ExamRevisionSummary{}, mapped
	}
	if result == nil || result.Revision == nil {
		return store.ExamRevisionSummary{}, unavailable(errors.New("Exam Revision store returned no result"))
	}
	if !result.Replayed {
		if effectErr := p.effects.RevisionPublished(ctx, *result.Revision); effectErr != nil {
			p.failures.Report(ctx, "exam_revision_published", effectErr)
		}
	}
	return *result.Revision, nil
}

func (p *Publication) Get(ctx context.Context, call Call, examID model.ExamID, revisionID model.ExamRevisionID) (store.ExamRevisionSummary, error) {
	if err := p.authorizeView(ctx, call, examID); err != nil {
		return store.ExamRevisionSummary{}, err
	}
	revision, err := p.store.GetSummary(ctx, examID, revisionID)
	if err != nil {
		return store.ExamRevisionSummary{}, mapStoreError(err)
	}
	if revision == nil {
		return store.ExamRevisionSummary{}, unavailable(errors.New("Exam Revision store returned no summary"))
	}
	return *revision, nil
}

func (p *Publication) List(ctx context.Context, call Call, query ListRevisionsQuery) (RevisionPage, error) {
	if err := p.authorizeView(ctx, call, query.ExamID); err != nil {
		return RevisionPage{}, err
	}
	if query.Limit < 1 || query.Limit > 200 {
		return RevisionPage{}, invalid("limit")
	}
	items, err := p.store.List(ctx, store.ExamRevisionListOptions{ExamID: query.ExamID, BeforeNumber: query.BeforeNumber, BeforeRevisionID: query.BeforeRevisionID, Limit: query.Limit + 1})
	if err != nil {
		return RevisionPage{}, mapStoreError(err)
	}
	page := RevisionPage{Items: items, HasMore: len(items) > query.Limit}
	if page.HasMore {
		page.Items = page.Items[:query.Limit]
	}
	return page, nil
}

func (p *Publication) authorizeView(ctx context.Context, call Call, examID model.ExamID) error {
	principal := call.Principal()
	if principal.Validate() != nil || !examID.IsValid() {
		return invalid("exam_id")
	}
	access, err := p.access.Access(ctx, examID, principal.UserID)
	if err != nil {
		return mapStoreError(err)
	}
	if access == nil || access.Exam == nil {
		return unavailable(errors.New("Exam access projection is incomplete"))
	}
	action, err := actionForAccess(ctx, p.memberships, principal.UserID, access, model.TimeUTC(p.now()), model.ActionExamView, model.ActionExamViewOverride)
	if err != nil {
		return err
	}
	return p.authorizer.Authorize(ctx, call, action, model.Resource{Type: model.ResourceExam, ID: examID.String()})
}
