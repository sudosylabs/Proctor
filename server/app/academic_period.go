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

type GetAcademicPeriodQuery struct{ ID string }
type ListAcademicPeriodsQuery struct {
	Query string
	Limit int
}
type CreateAcademicPeriodCommand struct {
	OwnerType                      string
	OwnerID                        string
	Name, DisplayName, Description string
	StartAt, EndAt                 int64
	IdempotencyKey                 string
}
type UpdateAcademicPeriodCommand struct {
	ID                             string
	Name, DisplayName, Description *string
	StartAt, EndAt                 *int64
}
type ArchiveAcademicPeriodCommand struct{ ID string }

type academicPeriodStore interface {
	Get(context.Context, string) (*model.AcademicPeriod, error)
	ListVisible(context.Context, store.AcademicPeriodVisibilityScope, string, int) ([]*model.AcademicPeriod, error)
	Create(context.Context, *store.AcademicPeriodCreation) (*model.AcademicPeriod, error)
	UpdateWithAudit(context.Context, *store.AcademicPeriodUpdate) (*model.AcademicPeriod, error)
	ArchiveWithAudit(context.Context, *store.AcademicPeriodArchive) (*model.AcademicPeriod, error)
}

type academicPeriodAuthorizer interface {
	Authorize(context.Context, Invocation, model.Action, model.Resource) error
	AuthorizeAcademicPeriodOwner(context.Context, Invocation, model.Action, *model.AcademicPeriod) error
	AuthorizeAcademicPeriodList(context.Context, Invocation) (store.AcademicPeriodVisibilityScope, error)
}

type academicPeriodService struct {
	store         academicPeriodStore
	authorization academicPeriodAuthorizer
	audit         mutationAuditor
	now           func() time.Time
	newID         func() string
}

func newAcademicPeriodService(persistence academicPeriodStore, authorization academicPeriodAuthorizer, audit mutationAuditor, now func() time.Time, newID func() string) *academicPeriodService {
	return &academicPeriodService{store: persistence, authorization: authorization, audit: audit, now: now, newID: newID}
}

func (a *App) GetAcademicPeriod(ctx context.Context, invocation Invocation, query GetAcademicPeriodQuery) (*model.AcademicPeriod, error) {
	return a.academicPeriods.Get(ctx, invocation, query)
}

func (s *academicPeriodService) Get(ctx context.Context, invocation Invocation, query GetAcademicPeriodQuery) (*model.AcademicPeriod, error) {
	id := strings.TrimSpace(query.ID)
	if !model.IsValidId(id) {
		return nil, NewError("request.invalid").WithField("field", "academic_period_id")
	}
	if err := s.authorization.Authorize(ctx, invocation, model.ActionAcademicPeriodView, model.Resource{Type: model.ResourceAcademicPeriod, ID: id}); err != nil {
		return nil, err
	}
	period, err := s.get(ctx, id)
	if err != nil {
		return nil, err
	}
	return period, nil
}

func (a *App) ListAcademicPeriods(ctx context.Context, invocation Invocation, query ListAcademicPeriodsQuery) ([]*model.AcademicPeriod, error) {
	return a.academicPeriods.List(ctx, invocation, query)
}

func (s *academicPeriodService) List(ctx context.Context, invocation Invocation, query ListAcademicPeriodsQuery) ([]*model.AcademicPeriod, error) {
	visibility, err := s.authorization.AuthorizeAcademicPeriodList(ctx, invocation)
	if err != nil {
		return nil, err
	}
	periods, err := s.store.ListVisible(
		ctx, visibility, strings.TrimSpace(query.Query), normalizeAdministrationLimit(query.Limit),
	)
	if err != nil {
		return nil, academicPeriodError(err)
	}
	if periods == nil {
		periods = []*model.AcademicPeriod{}
	}
	return periods, nil
}

func (a *App) CreateAcademicPeriod(ctx context.Context, invocation Invocation, command CreateAcademicPeriodCommand) (*model.AcademicPeriod, error) {
	return a.academicPeriods.Create(ctx, invocation, command)
}

func (s *academicPeriodService) Create(ctx context.Context, invocation Invocation, command CreateAcademicPeriodCommand) (*model.AcademicPeriod, error) {
	periodID, err := model.ParseAcademicPeriodID(s.newID())
	if err != nil {
		return nil, NewError("request.invalid").WithField("field", "academic_period_id").Wrap(err)
	}
	owner, err := parseAcademicPeriodOwner(command.OwnerType, command.OwnerID)
	if err != nil {
		return nil, err
	}
	candidate := &model.AcademicPeriod{
		Owner:       owner,
		Name:        command.Name,
		DisplayName: command.DisplayName,
		Description: command.Description,
		StartsAt:    model.TimeFromMillis(command.StartAt),
		EndsAt:      model.TimeFromMillis(command.EndAt),
	}
	candidate.PrepareCreate(periodID, s.now())
	if err := candidate.Validate(); err != nil {
		return nil, domainInvalid("academic_period.invalid", err)
	}
	if err := s.authorization.AuthorizeAcademicPeriodOwner(ctx, invocation, model.ActionAcademicPeriodManage, candidate); err != nil {
		return nil, err
	}
	idempotency, err := newCommandIdempotency(invocation, "academic_period.create.v1", command.IdempotencyKey, struct {
		OwnerType, OwnerID             string
		Name, DisplayName, Description string
		StartAt, EndAt                 int64
	}{
		string(candidate.Owner.Type()), candidate.Owner.ID(),
		candidate.Name, candidate.DisplayName, candidate.Description,
		model.MillisFromTime(candidate.StartsAt), model.MillisFromTime(candidate.EndsAt),
	})
	if err != nil {
		return nil, err
	}
	result, err := runAuditedMutation(
		ctx,
		s.audit,
		mutationAttempt{
			Invocation: invocation,
			Action:     model.ActionAcademicPeriodManage,
			Resource:   model.Resource{Type: model.ResourceAcademicPeriod, ID: candidate.ID.String()},
			ScopeType:  academicPeriodOwnerScopeType(candidate.Owner),
			ScopeID:    candidate.Owner.ID(),
			Operation:  "create",
			Value:      candidate.Auditable(),
		},
		s.now,
		func(ctx context.Context, reference mutationAttemptReference) (*store.AcademicPeriodCommandResult, error) {
			input := &store.AcademicPeriodCreation{
				Period: candidate, AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis,
			}
			if idempotency == nil {
				period, createErr := s.store.Create(ctx, input)
				return &store.AcademicPeriodCommandResult{Value: period}, createErr
			}
			idempotentStore, ok := s.store.(interface {
				CreateIdempotently(context.Context, *store.AcademicPeriodCreation, *store.CommandIdempotency) (*store.AcademicPeriodCommandResult, error)
			})
			if !ok {
				return nil, errors.New("academic period store does not support idempotent creation")
			}
			return idempotentStore.CreateIdempotently(ctx, input, idempotency)
		},
		func(err error) error {
			if mapped := idempotencyError(err); mapped != nil {
				return mapped
			}
			return academicPeriodError(err)
		},
	)
	if err != nil {
		return nil, err
	}
	return result.Value, nil
}

func (a *App) UpdateAcademicPeriod(ctx context.Context, invocation Invocation, command UpdateAcademicPeriodCommand) (*model.AcademicPeriod, error) {
	return a.academicPeriods.Update(ctx, invocation, command)
}

func (s *academicPeriodService) Update(ctx context.Context, invocation Invocation, command UpdateAcademicPeriodCommand) (*model.AcademicPeriod, error) {
	id := strings.TrimSpace(command.ID)
	if !model.IsValidId(id) {
		return nil, NewError("request.invalid").WithField("field", "academic_period_id")
	}
	resource := model.Resource{Type: model.ResourceAcademicPeriod, ID: id}
	if err := s.authorization.Authorize(ctx, invocation, model.ActionAcademicPeriodManage, resource); err != nil {
		return nil, err
	}
	current, err := s.get(ctx, id)
	if err != nil {
		return nil, err
	}
	candidate := *current
	if command.Name != nil {
		candidate.Name = *command.Name
	}
	if command.DisplayName != nil {
		candidate.DisplayName = *command.DisplayName
	}
	if command.Description != nil {
		candidate.Description = *command.Description
	}
	if command.StartAt != nil {
		candidate.StartsAt = model.TimeFromMillis(*command.StartAt)
	}
	if command.EndAt != nil {
		candidate.EndsAt = model.TimeFromMillis(*command.EndAt)
	}
	candidate.PrepareUpdate(s.now())
	if err := candidate.Validate(); err != nil {
		return nil, domainInvalid("academic_period.invalid", err)
	}
	return runAuditedMutation(
		ctx,
		s.audit,
		mutationAttempt{
			Invocation: invocation,
			Action:     model.ActionAcademicPeriodManage,
			Resource:   resource,
			ScopeType:  academicPeriodOwnerScopeType(candidate.Owner),
			ScopeID:    candidate.Owner.ID(),
			Operation:  "patch",
			Value:      candidate.Auditable(),
			Prior:      current.Auditable(),
		},
		s.now,
		func(ctx context.Context, reference mutationAttemptReference) (*model.AcademicPeriod, error) {
			return s.store.UpdateWithAudit(ctx, &store.AcademicPeriodUpdate{
				Period: &candidate, AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis,
			})
		},
		academicPeriodError,
	)
}

func (a *App) ArchiveAcademicPeriod(ctx context.Context, invocation Invocation, command ArchiveAcademicPeriodCommand) error {
	return a.academicPeriods.Archive(ctx, invocation, command)
}

func (s *academicPeriodService) Archive(ctx context.Context, invocation Invocation, command ArchiveAcademicPeriodCommand) error {
	id := strings.TrimSpace(command.ID)
	if !model.IsValidId(id) {
		return NewError("request.invalid").WithField("field", "academic_period_id")
	}
	resource := model.Resource{Type: model.ResourceAcademicPeriod, ID: id}
	if err := s.authorization.Authorize(ctx, invocation, model.ActionAcademicPeriodManage, resource); err != nil {
		return err
	}
	current, err := s.get(ctx, id)
	if err != nil {
		return err
	}
	_, err = runAuditedMutation(
		ctx,
		s.audit,
		mutationAttempt{
			Invocation: invocation,
			Action:     model.ActionAcademicPeriodManage,
			Resource:   resource,
			ScopeType:  academicPeriodOwnerScopeType(current.Owner),
			ScopeID:    current.Owner.ID(),
			Operation:  "archive",
			Prior:      current.Auditable(),
		},
		s.now,
		func(ctx context.Context, reference mutationAttemptReference) (*model.AcademicPeriod, error) {
			return s.store.ArchiveWithAudit(ctx, &store.AcademicPeriodArchive{
				ID: current.ID.String(), ArchiveAt: reference.MutationAtMillis,
				AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis,
			})
		},
		academicPeriodError,
	)
	return err
}

func parseAcademicPeriodOwner(ownerType, ownerID string) (model.AcademicPeriodOwner, error) {
	ownerID = strings.TrimSpace(ownerID)
	switch model.ResourceType(strings.TrimSpace(ownerType)) {
	case model.ResourceInstitution:
		id, err := model.ParseInstitutionID(ownerID)
		if err != nil {
			return model.AcademicPeriodOwner{}, NewError("request.invalid").WithField("field", "owner_id").Wrap(err)
		}
		return model.NewInstitutionAcademicPeriodOwner(id), nil
	case model.ResourceAcademicUnit:
		id, err := model.ParseAcademicUnitID(ownerID)
		if err != nil {
			return model.AcademicPeriodOwner{}, NewError("request.invalid").WithField("field", "owner_id").Wrap(err)
		}
		return model.NewAcademicUnitAcademicPeriodOwner(id), nil
	default:
		return model.AcademicPeriodOwner{}, NewError("request.invalid").WithField("field", "owner_type")
	}
}

func academicPeriodOwnerScopeType(owner model.AcademicPeriodOwner) model.RoleScopeType {
	if owner.Type() == model.ResourceAcademicUnit {
		return model.RoleScopeAcademicUnit
	}
	return model.RoleScopeInstitution
}

func (s *academicPeriodService) get(ctx context.Context, id string) (*model.AcademicPeriod, error) {
	id = strings.TrimSpace(id)
	if !model.IsValidId(id) {
		return nil, NewError("request.invalid").WithField("field", "academic_period_id")
	}
	period, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, academicPeriodError(err)
	}
	return period, nil
}

func academicPeriodError(err error) error {
	switch {
	case store.IsNotFound(err):
		return NewError("resource.not_found").WithField("resource", "academic_period").Wrap(err)
	case store.IsConflict(err):
		return NewError("academic_period.conflict").WithField("resource", "academic_period").Wrap(err)
	default:
		var invalid *store.ErrInvalidInput
		var reference *store.ErrReference
		if errors.As(err, &invalid) || errors.As(err, &reference) {
			return NewError("academic_period.invalid").WithField("resource", "academic_period").Wrap(err)
		}
		return NewError("administration.unavailable").WithField("resource", "academic_period").Wrap(err)
	}
}
