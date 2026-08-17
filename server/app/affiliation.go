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
	AuthorizeUserRead(context.Context, Invocation, string) error
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
	if !model.IsValidId(userID) {
		return nil, NewError("request.invalid").WithField("field", "user_id")
	}
	if err := s.authorization.AuthorizeUserRead(ctx, invocation, userID); err != nil {
		return nil, err
	}
	affiliations, err := s.store.ListByUser(ctx, userID)
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
	userID, err := model.ParseUserID(resource.ID)
	if err != nil {
		return nil, NewError("request.invalid").WithField("field", "user_id").Wrap(err)
	}
	affiliationID, err := model.ParseAffiliationID(s.newID())
	if err != nil {
		return nil, NewError("request.invalid").WithField("field", "affiliation_id").Wrap(err)
	}
	candidate := &model.Affiliation{
		UserID:   userID,
		Kind:     command.Kind,
		StartsAt: model.TimeFromMillis(command.StartAt),
		EndsAt:   model.OptionalTimeFromMillis(command.EndAt),
	}
	at := s.now()
	candidate.PrepareCreate(affiliationID, at)
	if err := candidate.Validate(); err != nil {
		return nil, domainInvalid("affiliation.invalid", err)
	}
	return runAuditedMutation(
		ctx,
		s.audit,
		mutationAttempt{
			Invocation: invocation,
			Action:     model.ActionUserManage,
			Resource:   resource,
			Operation:  "create",
			Value:      candidate.Auditable(),
		},
		func() time.Time { return at },
		func(ctx context.Context, reference mutationAttemptReference) (*model.Affiliation, error) {
			return s.store.Create(ctx, &store.AffiliationCreation{
				Affiliation: candidate, AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis,
			})
		},
		affiliationError,
	)
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
	resource, err := s.authorizeUser(ctx, invocation, current.UserID.String())
	if err != nil {
		return nil, err
	}
	if current.Kind == model.AffiliationStudent {
		enrollments, err := s.enrollments.ListByUser(ctx, current.UserID.String())
		if err != nil {
			return nil, affiliationError(err)
		}
		for _, enrollment := range enrollments {
			// Open-ended enrollment (EndsAt absent) blocks ending a student affiliation.
			if enrollment != nil && !enrollment.EndsAt.Valid {
				return nil, NewError("affiliation.student_has_active_enrollment")
			}
		}
	}
	return runAuditedMutation(
		ctx,
		s.audit,
		mutationAttempt{
			Invocation: invocation,
			Action:     model.ActionUserManage,
			Resource:   resource,
			Operation:  "end",
			Prior:      current.Auditable(),
		},
		s.now,
		func(ctx context.Context, reference mutationAttemptReference) (*model.Affiliation, error) {
			return s.store.EndWithAudit(ctx, &store.AffiliationEnd{
				ID: id, ExpectedRevision: current.Revision, EndAt: reference.MutationAtMillis,
				AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis,
			})
		},
		affiliationError,
	)
}

func (s *affiliationService) authorizeUser(ctx context.Context, invocation Invocation, userID string) (model.Resource, error) {
	if !model.IsValidId(userID) {
		return model.Resource{}, NewError("request.invalid").WithField("field", "user_id")
	}
	resource := model.Resource{Type: model.ResourceUser, ID: userID}
	if err := s.authorization.Authorize(ctx, invocation, model.ActionUserManage, resource); err != nil {
		return model.Resource{}, err
	}
	return resource, nil
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
