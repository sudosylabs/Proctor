// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type ListAffiliationsQuery struct {
	UserID string
}

type CreateAffiliationCommand struct {
	UserID  string
	Kind    model.AffiliationKind
	StartAt int64
	EndAt   int64
}

type EndAffiliationCommand struct {
	ID string
}

type affiliationStore interface {
	Get(context.Context, string) (*model.Affiliation, error)
	ListByUser(context.Context, string) ([]*model.Affiliation, error)
	Create(context.Context, *store.AffiliationCreation) (*model.Affiliation, error)
	EndWithAudit(context.Context, *store.AffiliationEnd) (*model.Affiliation, error)
}

type affiliationEnrollmentReader interface {
	ListByUser(context.Context, string) ([]*model.ClassMember, error)
}

type affiliationAuthorizer interface {
	Authorize(context.Context, Invocation, model.Action, model.Resource) error
}

type affiliationService struct {
	store         affiliationStore
	enrollments   affiliationEnrollmentReader
	authorization affiliationAuthorizer
	audit         mutationAuditor
	now           func() time.Time
	newID         func() string
}

func newAffiliationService(persistence affiliationStore, enrollments affiliationEnrollmentReader, authorization affiliationAuthorizer, audit mutationAuditor, now func() time.Time, newID func() string) *affiliationService {
	return &affiliationService{store: persistence, enrollments: enrollments, authorization: authorization, audit: audit, now: now, newID: newID}
}

func (a *App) ListAffiliations(ctx context.Context, invocation Invocation, query ListAffiliationsQuery) ([]*model.Affiliation, error) {
	return a.affiliations.List(ctx, invocation, query)
}

func (s *affiliationService) List(ctx context.Context, invocation Invocation, query ListAffiliationsQuery) ([]*model.Affiliation, error) {
	userID := strings.TrimSpace(query.UserID)
	resource, err := s.authorizeUser(ctx, invocation, userID)
	if err != nil {
		return nil, err
	}
	affiliations, err := s.store.ListByUser(ctx, resource.Id)
	if err != nil {
		return nil, affiliationError(err)
	}
	if affiliations == nil {
		affiliations = []*model.Affiliation{}
	}
	return affiliations, nil
}

func (a *App) CreateAffiliation(ctx context.Context, invocation Invocation, command CreateAffiliationCommand) (*model.Affiliation, error) {
	return a.affiliations.Create(ctx, invocation, command)
}

func (s *affiliationService) Create(ctx context.Context, invocation Invocation, command CreateAffiliationCommand) (*model.Affiliation, error) {
	resource, err := s.authorizeUser(ctx, invocation, strings.TrimSpace(command.UserID))
	if err != nil {
		return nil, err
	}
	candidate := &model.Affiliation{UserId: resource.Id, Kind: command.Kind, StartAt: command.StartAt, EndAt: command.EndAt}
	at := s.now().UnixMilli()
	candidate.PrepareCreate(s.newID(), at)
	if appErr := candidate.IsValid(); appErr != nil {
		return nil, NewError("affiliation.invalid").WithFields(appErr.SafeFields()).Wrap(appErr)
	}
	auditID, err := s.audit.Begin(ctx, invocation, model.ActionUserManage, resource, "create", candidate.Auditable(), nil)
	if err != nil {
		return nil, err
	}
	saved, err := s.store.Create(ctx, &store.AffiliationCreation{Affiliation: candidate, AuditEventID: auditID, AuditAt: at})
	if err != nil {
		return nil, s.failMutation(ctx, auditID, err)
	}
	return saved, nil
}

func (a *App) EndAffiliation(ctx context.Context, invocation Invocation, command EndAffiliationCommand) (*model.Affiliation, error) {
	return a.affiliations.End(ctx, invocation, command)
}

func (s *affiliationService) End(ctx context.Context, invocation Invocation, command EndAffiliationCommand) (*model.Affiliation, error) {
	id := strings.TrimSpace(command.ID)
	if !model.IsValidId(id) {
		return nil, NewError("request.invalid").WithField("field", "affiliation_id")
	}
	current, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, affiliationError(err)
	}
	resource, err := s.authorizeUser(ctx, invocation, current.UserId)
	if err != nil {
		return nil, err
	}
	if current.Kind == model.AffiliationStudent {
		enrollments, err := s.enrollments.ListByUser(ctx, current.UserId)
		if err != nil {
			return nil, affiliationError(err)
		}
		for _, enrollment := range enrollments {
			if enrollment.EndAt == 0 {
				return nil, NewError("affiliation.student_has_active_enrollment")
			}
		}
	}
	auditID, err := s.audit.Begin(ctx, invocation, model.ActionUserManage, resource, "end", nil, current.Auditable())
	if err != nil {
		return nil, err
	}
	at := s.now().UnixMilli()
	ended, err := s.store.EndWithAudit(ctx, &store.AffiliationEnd{ID: id, ExpectedRevision: current.Revision, EndAt: at, AuditEventID: auditID, AuditAt: at})
	if err != nil {
		return nil, s.failMutation(ctx, auditID, err)
	}
	return ended, nil
}

func (s *affiliationService) authorizeUser(ctx context.Context, invocation Invocation, userID string) (model.Resource, error) {
	if !model.IsValidId(userID) {
		return model.Resource{}, NewError("request.invalid").WithField("field", "user_id")
	}
	resource := model.Resource{Type: model.ResourceUser, Id: userID}
	if err := s.authorization.Authorize(ctx, invocation, model.ActionUserManage, resource); err != nil {
		return model.Resource{}, err
	}
	return resource, nil
}

func (s *affiliationService) failMutation(ctx context.Context, auditID string, err error) error {
	mapped := affiliationError(err)
	failure, _ := As(mapped)
	if auditErr := s.audit.Fail(ctx, auditID, failure.Code()); auditErr != nil {
		return auditErr
	}
	return mapped
}

func affiliationError(err error) error {
	switch {
	case store.IsNotFound(err):
		return NewError("resource.not_found").WithField("resource", "affiliation").Wrap(err)
	case store.IsConflict(err):
		var conflict *store.ErrConflict
		if errors.As(err, &conflict) && conflict.Constraint == "affiliation_student_has_active_enrollment" {
			return NewError("affiliation.student_has_active_enrollment").Wrap(err)
		}
		if errors.As(err, &conflict) && conflict.Constraint == "affiliation_end_time" {
			return NewError("resource.not_found").WithField("resource", "affiliation").Wrap(err)
		}
		return NewError("affiliation.conflict").WithField("resource", "affiliation").Wrap(err)
	default:
		var invalid *store.ErrInvalidInput
		var reference *store.ErrReference
		if errors.As(err, &invalid) || errors.As(err, &reference) {
			return NewError("affiliation.invalid").WithField("resource", "affiliation").Wrap(err)
		}
		return NewError("administration.unavailable").WithField("resource", "affiliation").Wrap(err)
	}
}
