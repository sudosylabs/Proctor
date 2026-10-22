// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

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

// GetAcademicPeriodQuery names one Academic Period.
type GetAcademicPeriodQuery struct{ ID model.AcademicPeriodID }
type ListAcademicPeriodsQuery struct {
	Query string
	// AfterStartAt, AfterName, and AfterID are the last Academic Period of the
	// previous page in (start, name, identity) order, all set or all empty.
	AfterStartAt time.Time
	AfterName    string
	AfterID      model.AcademicPeriodID
	Limit        int
}
type CreateAcademicPeriodCommand struct {
	OwnerType                      string
	OwnerID                        string
	Name, DisplayName, Description string
	StartAt, EndAt                 int64
	IdempotencyKey                 string
}
type UpdateAcademicPeriodCommand struct {
	ExpectedRevision               *int64
	ID                             model.AcademicPeriodID
	Name, DisplayName, Description *string
	StartAt, EndAt                 *int64
}
type ArchiveAcademicPeriodCommand struct {
	ExpectedRevision *int64
	ID               model.AcademicPeriodID
}

type academicPeriodStore interface {
	Get(context.Context, model.AcademicPeriodID) (*model.AcademicPeriod, error)
	ListVisible(context.Context, store.AcademicPeriodVisibilityScope, store.AcademicPeriodListOptions) ([]*model.AcademicPeriod, error)
	Create(context.Context, *store.AcademicPeriodCreation) (*model.AcademicPeriod, error)
	Update(context.Context, *store.AcademicPeriodUpdate) (*model.AcademicPeriod, error)
	Archive(context.Context, *store.AcademicPeriodArchive) (*model.AcademicPeriod, error)
}

type academicPeriodAuthorizer interface {
	Authorize(context.Context, invocation.Invocation, model.Action, model.Resource) error
	AuthorizeAcademicPeriodOwner(context.Context, invocation.Invocation, model.Action, *model.AcademicPeriod) error
	AuthorizeAcademicPeriodList(context.Context, invocation.Invocation) (store.AcademicPeriodVisibilityScope, error)
}

// academicPeriodServiceDependencies are the collaborators of academicPeriodService;
// newAcademicPeriodService validates them once and academicPeriodService keeps them in deps.
type academicPeriodServiceDependencies struct {
	Store         academicPeriodStore
	Authorization academicPeriodAuthorizer
	Audit         mutationAuditor
	Now           func() time.Time
	NewID         func() string
}

type academicPeriodService struct {
	deps academicPeriodServiceDependencies
}

func newAcademicPeriodService(deps academicPeriodServiceDependencies) *academicPeriodService {
	return &academicPeriodService{deps: deps}
}

func (s *academicPeriodService) GetAcademicPeriod(ctx context.Context, invocation invocation.Invocation, query GetAcademicPeriodQuery) (*model.AcademicPeriod, error) {
	id := query.ID
	if !id.IsValid() {
		return nil, apperror.New("request.invalid").WithField("field", "academic_period_id")
	}
	if err := s.deps.Authorization.Authorize(ctx, invocation, model.ActionAcademicPeriodView, model.Resource{Type: model.ResourceAcademicPeriod, ID: id.String()}); err != nil {
		return nil, err
	}
	period, err := s.get(ctx, id)
	if err != nil {
		return nil, err
	}
	return period, nil
}

func (s *academicPeriodService) ListAcademicPeriods(ctx context.Context, invocation invocation.Invocation, query ListAcademicPeriodsQuery) (Page[*model.AcademicPeriod], error) {
	limit, probe, err := resolvePageLimit(query.Limit, "limit")
	if err != nil {
		return Page[*model.AcademicPeriod]{}, err
	}
	visibility, err := s.deps.Authorization.AuthorizeAcademicPeriodList(ctx, invocation)
	if err != nil {
		return Page[*model.AcademicPeriod]{}, err
	}
	periods, err := s.deps.Store.ListVisible(ctx, visibility, store.AcademicPeriodListOptions{
		Term: strings.TrimSpace(query.Query), AfterStartAt: query.AfterStartAt, AfterName: query.AfterName,
		AfterID: query.AfterID, Limit: probe,
	})
	if err != nil {
		return Page[*model.AcademicPeriod]{}, academicPeriodStoreErrors.Translate(err)
	}
	return newPage(periods, limit), nil
}

func (s *academicPeriodService) CreateAcademicPeriod(ctx context.Context, invocation invocation.Invocation, command CreateAcademicPeriodCommand) (*model.AcademicPeriod, error) {
	periodID, err := model.ParseAcademicPeriodID(s.deps.NewID())
	if err != nil {
		return nil, apperror.New("request.invalid").WithField("field", "academic_period_id").Wrap(err)
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
	candidate.PrepareCreate(periodID, s.deps.Now())
	if err := candidate.Validate(); err != nil {
		return nil, domainInvalid("academic_period.invalid", err)
	}
	if err = s.deps.Authorization.AuthorizeAcademicPeriodOwner(ctx, invocation, model.ActionAcademicPeriodManage, candidate); err != nil {
		return nil, err
	}
	idempotency, err := idempotency.Prepare(invocation, "academic_period.create", command.IdempotencyKey, struct {
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
		s.deps.Audit,
		mutationAttempt{
			Invocation: invocation,
			Action:     model.ActionAcademicPeriodManage,
			Resource:   model.Resource{Type: model.ResourceAcademicPeriod, ID: candidate.ID.String()},
			ScopeType:  academicPeriodOwnerScopeType(candidate.Owner),
			ScopeID:    candidate.Owner.ID(),
			Operation:  "create",
			Value:      candidate.Auditable(),
		},
		s.deps.Now,
		func(ctx context.Context, reference mutationAttemptReference) (*store.AcademicPeriodCommandResult, error) {
			input := &store.AcademicPeriodCreation{
				Period: candidate, AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis,
			}
			if idempotency == nil {
				period, createErr := s.deps.Store.Create(ctx, input)
				return &store.AcademicPeriodCommandResult{Value: period}, createErr
			}
			idempotentStore, ok := s.deps.Store.(interface {
				CreateIdempotently(context.Context, *store.AcademicPeriodCreation, *store.CommandIdempotency) (*store.AcademicPeriodCommandResult, error)
			})
			if !ok {
				return nil, errors.New("academic period store does not support idempotent creation")
			}
			return idempotentStore.CreateIdempotently(ctx, input, idempotency)
		},
		academicPeriodStoreErrors.Translate,
	)
	if err != nil {
		return nil, err
	}
	return result.Value, nil
}

func (s *academicPeriodService) UpdateAcademicPeriod(ctx context.Context, invocation invocation.Invocation, command UpdateAcademicPeriodCommand) (*model.AcademicPeriod, error) {
	id := command.ID
	if !id.IsValid() {
		return nil, apperror.New("request.invalid").WithField("field", "academic_period_id")
	}
	resource := model.Resource{Type: model.ResourceAcademicPeriod, ID: id.String()}
	if err := s.deps.Authorization.Authorize(ctx, invocation, model.ActionAcademicPeriodManage, resource); err != nil {
		return nil, err
	}
	current, err := s.get(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := checkAcademicRevision(command.ExpectedRevision, current.Revision, "academic_period.conflict"); err != nil {
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
	candidate.PrepareUpdate(s.deps.Now())
	if err := candidate.Validate(); err != nil {
		return nil, domainInvalid("academic_period.invalid", err)
	}
	return runAuditedMutation(
		ctx,
		s.deps.Audit,
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
		s.deps.Now,
		func(ctx context.Context, reference mutationAttemptReference) (*model.AcademicPeriod, error) {
			return s.deps.Store.Update(ctx, &store.AcademicPeriodUpdate{
				Period: &candidate, AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis,
			})
		},
		academicPeriodStoreErrors.Translate,
	)
}

func (s *academicPeriodService) ArchiveAcademicPeriod(ctx context.Context, invocation invocation.Invocation, command ArchiveAcademicPeriodCommand) error {
	id := command.ID
	if !id.IsValid() {
		return apperror.New("request.invalid").WithField("field", "academic_period_id")
	}
	resource := model.Resource{Type: model.ResourceAcademicPeriod, ID: id.String()}
	if err := s.deps.Authorization.Authorize(ctx, invocation, model.ActionAcademicPeriodManage, resource); err != nil {
		return err
	}
	current, err := s.get(ctx, id)
	if err != nil {
		return err
	}
	if err := checkAcademicRevision(command.ExpectedRevision, current.Revision, "academic_period.conflict"); err != nil {
		return err
	}
	_, err = runAuditedMutation(
		ctx,
		s.deps.Audit,
		mutationAttempt{
			Invocation: invocation,
			Action:     model.ActionAcademicPeriodManage,
			Resource:   resource,
			ScopeType:  academicPeriodOwnerScopeType(current.Owner),
			ScopeID:    current.Owner.ID(),
			Operation:  "archive",
			Prior:      current.Auditable(),
		},
		s.deps.Now,
		func(ctx context.Context, reference mutationAttemptReference) (*model.AcademicPeriod, error) {
			return s.deps.Store.Archive(ctx, &store.AcademicPeriodArchive{
				ExpectedRevision: current.Revision,
				ID:               current.ID, ArchiveAt: reference.MutationAtMillis,
				AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis,
			})
		},
		academicPeriodStoreErrors.Translate,
	)
	return err
}

func parseAcademicPeriodOwner(ownerType, ownerID string) (model.AcademicPeriodOwner, error) {
	ownerID = strings.TrimSpace(ownerID)
	switch model.ResourceType(strings.TrimSpace(ownerType)) {
	case model.ResourceInstitution:
		id, err := model.ParseInstitutionID(ownerID)
		if err != nil {
			return model.AcademicPeriodOwner{}, apperror.New("request.invalid").WithField("field", "owner_id").Wrap(err)
		}
		return model.NewInstitutionAcademicPeriodOwner(id), nil
	case model.ResourceAcademicUnit:
		id, err := model.ParseAcademicUnitID(ownerID)
		if err != nil {
			return model.AcademicPeriodOwner{}, apperror.New("request.invalid").WithField("field", "owner_id").Wrap(err)
		}
		return model.NewAcademicUnitAcademicPeriodOwner(id), nil
	default:
		return model.AcademicPeriodOwner{}, apperror.New("request.invalid").WithField("field", "owner_type")
	}
}

func academicPeriodOwnerScopeType(owner model.AcademicPeriodOwner) model.RoleScopeType {
	if owner.Type() == model.ResourceAcademicUnit {
		return model.RoleScopeAcademicUnit
	}
	return model.RoleScopeInstitution
}

func (s *academicPeriodService) get(ctx context.Context, id model.AcademicPeriodID) (*model.AcademicPeriod, error) {
	if !id.IsValid() {
		return nil, apperror.New("request.invalid").WithField("field", "academic_period_id")
	}
	period, err := s.deps.Store.Get(ctx, id)
	if err != nil {
		return nil, academicPeriodStoreErrors.Translate(err)
	}
	return period, nil
}

var academicPeriodStoreErrors = apperror.AdministrationStoreTable("academic_period", "academic_period.conflict", "academic_period.invalid", nil)
