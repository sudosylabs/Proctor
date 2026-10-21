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
	"time"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/exam/manageraccess"
	applicationidempotency "github.com/sudosylabs/proctor/server/app/idempotency"
	"github.com/sudosylabs/proctor/server/app/invocation"
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
type Publication struct{ deps PublicationDependencies }

// PublicationDependencies are the collaborators of Publication; NewPublication validates them once and the
// service retains them unchanged.
type PublicationDependencies struct {
	Persistence       store.ExamRevisionStore
	Access            store.ExamAuthoringStore
	Memberships       manageraccess.Memberships
	Authorizer        Authorizer
	Auditor           Auditor
	Effects           PublicationEffects
	EffectFailures    EffectFailures
	Now               func() time.Time
	NewID             func() model.ExamRevisionID
	InstitutionOrigin string
	Operational       OperationalRecorder
}

// NewPublication returns a Publication service, or an error when a required
// dependency is missing.
func NewPublication(deps PublicationDependencies) (*Publication, error) {
	if deps.Persistence == nil || deps.Access == nil || deps.Memberships == nil || deps.Authorizer == nil || deps.Auditor == nil || deps.Effects == nil || deps.EffectFailures == nil || deps.Now == nil || deps.NewID == nil {
		return nil, errors.New("Exam Revision publication dependencies are required")
	}
	return &Publication{deps: deps}, nil
}

func (p *Publication) recordOperational(event string, err error) {
	if p.deps.Operational != nil {
		p.deps.Operational.RecordOperational("exam", event, err)
	}
}

// PublishExamRevision idempotently publishes the current Exam Draft as a new
// immutable Exam Revision, fenced by ExpectedDraftRevision.
func (p *Publication) PublishExamRevision(ctx context.Context, invocation invocation.Invocation, command PublishRevisionCommand) (_ store.ExamRevisionSummary, resultErr error) {
	defer func() { p.recordOperational("publish_revision", resultErr) }()
	defer func() { resultErr = publicFailures.Conceal(resultErr) }()
	principal := invocation.Principal()
	if principal.Validate() != nil || !command.ExamID.IsValid() || command.ExpectedDraftRevision < 1 {
		return store.ExamRevisionSummary{}, apperror.Invalid("exam.invalid", "publication")
	}
	idempotency, err := applicationidempotency.Require(invocation, idempotencyOperationPublishRevision, command.IdempotencyKey, struct {
		ExamID                string `json:"exam_id"`
		ExpectedDraftRevision int64  `json:"expected_draft_revision"`
	}{command.ExamID.String(), command.ExpectedDraftRevision})
	if err != nil {
		return store.ExamRevisionSummary{}, err
	}
	at := model.TimeUTC(p.deps.Now())
	revisionID := p.deps.NewID()
	if !revisionID.IsValid() {
		return store.ExamRevisionSummary{}, apperror.Invalid("exam.invalid", "revision_id")
	}
	access, err := p.deps.Access.Access(ctx, command.ExamID, principal.UserID)
	if err != nil {
		return store.ExamRevisionSummary{}, examStoreErrors.Translate(err)
	}
	if access == nil || access.Exam == nil {
		return store.ExamRevisionSummary{}, apperror.New("exam.unavailable").Wrap(errors.New("Exam access projection is incomplete"))
	}
	action, err := manageraccess.SelectAction(ctx, p.deps.Memberships, principal.UserID, access, at, model.ActionExamPublish, model.ActionExamPublishOverride)
	if err != nil {
		return store.ExamRevisionSummary{}, apperror.New("exam.unavailable").Wrap(err)
	}
	resource := model.Resource{Type: model.ResourceExam, ID: command.ExamID.String()}
	if err = p.deps.Authorizer.Authorize(ctx, invocation, action, resource); err != nil {
		return store.ExamRevisionSummary{}, err
	}
	auditID, err := p.deps.Auditor.BeginAtScope(ctx, invocation, action, resource, model.RoleScopeAcademicUnit, access.Exam.AcademicUnitID.String(), "publish", map[string]any{
		"exam_id": command.ExamID.String(), "expected_draft_revision": command.ExpectedDraftRevision,
	}, nil)
	if err != nil {
		return store.ExamRevisionSummary{}, err
	}
	result, err := p.deps.Persistence.Publish(ctx, &store.ExamRevisionPublication{InstitutionOrigin: p.deps.InstitutionOrigin, RevisionID: revisionID, ExamID: command.ExamID,
		ActorUserID: principal.UserID, ManagerOverride: action == model.ActionExamPublishOverride,
		ExpectedDraftRevision: command.ExpectedDraftRevision, Kind: model.ExamRevisionPublicationStandard,
		PublishedAt: at, AuditEventID: auditID, AuditAt: model.MillisFromTime(at)}, idempotency)
	if err != nil {
		mapped := examStoreErrors.Translate(err)
		if auditErr := p.deps.Auditor.Fail(ctx, auditID, apperror.CodeOf(mapped, "exam.unavailable")); auditErr != nil {
			return store.ExamRevisionSummary{}, errors.Join(mapped, auditErr)
		}
		return store.ExamRevisionSummary{}, mapped
	}
	if result == nil || result.Revision == nil {
		return store.ExamRevisionSummary{}, apperror.New("exam.unavailable").Wrap(errors.New("Exam Revision store returned no result"))
	}
	if !result.Replayed {
		if effectErr := p.deps.Effects.RevisionPublished(ctx, *result.Revision); effectErr != nil {
			p.deps.EffectFailures.Report(ctx, "exam_revision_published", effectErr)
		}
	}
	return *result.Revision, nil
}

// GetRevisionQuery names one Exam Revision.
type GetRevisionQuery struct {
	ExamID     model.ExamID
	RevisionID model.ExamRevisionID
}

// GetExamRevision returns the summary of one Exam Revision to a caller
// authorized to view the Exam.
func (p *Publication) GetExamRevision(ctx context.Context, invocation invocation.Invocation, query GetRevisionQuery) (_ store.ExamRevisionSummary, resultErr error) {
	defer func() { resultErr = publicFailures.Conceal(resultErr) }()
	if err := p.authorizeView(ctx, invocation, query.ExamID); err != nil {
		return store.ExamRevisionSummary{}, err
	}
	revision, err := p.deps.Persistence.GetSummary(ctx, query.ExamID, query.RevisionID)
	if err != nil {
		return store.ExamRevisionSummary{}, examStoreErrors.Translate(err)
	}
	if revision == nil {
		return store.ExamRevisionSummary{}, apperror.New("exam.unavailable").Wrap(errors.New("Exam Revision store returned no summary"))
	}
	return *revision, nil
}

// ListExamRevisions returns one page of an Exam's Exam Revisions, newest
// first, to a caller authorized to view the Exam.
func (p *Publication) ListExamRevisions(ctx context.Context, invocation invocation.Invocation, query ListRevisionsQuery) (_ RevisionPage, resultErr error) {
	defer func() { resultErr = publicFailures.Conceal(resultErr) }()
	if err := p.authorizeView(ctx, invocation, query.ExamID); err != nil {
		return RevisionPage{}, err
	}
	if query.Limit < 1 || query.Limit > 200 {
		return RevisionPage{}, apperror.Invalid("exam.invalid", "limit")
	}
	items, err := p.deps.Persistence.List(ctx, store.ExamRevisionListOptions{ExamID: query.ExamID, BeforeNumber: query.BeforeNumber, BeforeRevisionID: query.BeforeRevisionID, Limit: query.Limit + 1})
	if err != nil {
		return RevisionPage{}, examStoreErrors.Translate(err)
	}
	page := RevisionPage{Items: items, HasMore: len(items) > query.Limit}
	if page.HasMore {
		page.Items = page.Items[:query.Limit]
	}
	return page, nil
}

func (p *Publication) authorizeView(ctx context.Context, invocation invocation.Invocation, examID model.ExamID) error {
	principal := invocation.Principal()
	if principal.Validate() != nil || !examID.IsValid() {
		return apperror.Invalid("exam.invalid", "exam_id")
	}
	access, err := p.deps.Access.Access(ctx, examID, principal.UserID)
	if err != nil {
		return examStoreErrors.Translate(err)
	}
	if access == nil || access.Exam == nil {
		return apperror.New("exam.unavailable").Wrap(errors.New("Exam access projection is incomplete"))
	}
	action, err := manageraccess.SelectAction(ctx, p.deps.Memberships, principal.UserID, access, model.TimeUTC(p.deps.Now()), model.ActionExamView, model.ActionExamViewOverride)
	if err != nil {
		return apperror.New("exam.unavailable").Wrap(err)
	}
	return p.deps.Authorizer.Authorize(ctx, invocation, action, model.Resource{Type: model.ResourceExam, ID: examID.String()})
}
