// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"strings"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// GetAcademicUnitQuery identifies one academic unit to retrieve.
type GetAcademicUnitQuery struct {
	ID string
}

// ListAcademicUnitsQuery lists direct children of a parent unit. An empty
// ParentID lists root units of the installation institution.
type ListAcademicUnitsQuery struct {
	ParentID string
}

// SearchAcademicUnitsQuery searches academic units by name within the
// installation institution.
type SearchAcademicUnitsQuery struct {
	Query string
	Limit int
}

type academicUnitAuthorizer interface {
	Authorize(context.Context, Invocation, model.Action, model.Resource) error
	Installation(context.Context) (model.Resource, error)
	AuthorizeInstallation(
		context.Context, Invocation, model.Action,
	) (model.Resource, error)
}

func (a academicUnitReadAuthorization) Installation(
	ctx context.Context,
) (model.Resource, error) {
	institution, err := a.institutions.GetSingleton(ctx)
	if err != nil {
		return model.Resource{}, academicUnitReadError("institution", err)
	}
	return model.Resource{Type: model.ResourceInstitution, Id: institution.Id}, nil
}

type academicUnitReadAuthorization struct {
	authorization *AuthorizationService
	institutions  store.InstitutionStore
}

func (a academicUnitReadAuthorization) Authorize(
	ctx context.Context,
	invocation Invocation,
	action model.Action,
	resource model.Resource,
) error {
	return fromLegacyAppError(a.authorization.authorizeCurrentState(
		ctx,
		invocation.Principal(),
		action,
		resource,
		invocation.RequestMetadata(),
	))
}

func (a academicUnitReadAuthorization) AuthorizeInstallation(
	ctx context.Context,
	invocation Invocation,
	action model.Action,
) (model.Resource, error) {
	resource, err := a.Installation(ctx)
	if err != nil {
		return model.Resource{}, err
	}
	if err := a.Authorize(ctx, invocation, action, resource); err != nil {
		return model.Resource{}, err
	}
	return resource, nil
}

type academicUnitQueryService struct {
	academicUnits academicUnitReader
	authorization academicUnitAuthorizer
}

type academicUnitReader interface {
	Get(context.Context, string) (*model.AcademicUnit, error)
	ListChildren(context.Context, string, string) ([]*model.AcademicUnit, error)
	Search(context.Context, string, string, int) ([]*model.AcademicUnit, error)
}

func newAcademicUnitQueryService(
	academicUnits academicUnitReader,
	authorization academicUnitAuthorizer,
) *academicUnitQueryService {
	return &academicUnitQueryService{
		academicUnits: academicUnits, authorization: authorization,
	}
}

// GetAcademicUnit returns one academic unit after authorizing
// academic_unit.view on that unit.
func (a *App) GetAcademicUnit(
	ctx context.Context,
	invocation Invocation,
	query GetAcademicUnitQuery,
) (*model.AcademicUnit, error) {
	return a.academicUnits.Get(ctx, invocation, query)
}

func (s *academicUnitQueryService) Get(
	ctx context.Context,
	invocation Invocation,
	query GetAcademicUnitQuery,
) (*model.AcademicUnit, error) {
	id := strings.TrimSpace(query.ID)
	if !model.IsValidId(id) {
		return nil, NewError("request.invalid").WithField("field", "academic_unit_id")
	}
	resource := model.Resource{Type: model.ResourceAcademicUnit, Id: id}
	if err := s.authorization.Authorize(
		ctx, invocation, model.ActionAcademicUnitView, resource,
	); err != nil {
		return nil, err
	}
	unit, err := s.academicUnits.Get(ctx, id)
	if err != nil {
		return nil, academicUnitReadError("academic_unit", err)
	}
	return unit, nil
}

// ListAcademicUnits lists direct children under ParentID, or root units when
// ParentID is empty.
func (a *App) ListAcademicUnits(
	ctx context.Context,
	invocation Invocation,
	query ListAcademicUnitsQuery,
) ([]*model.AcademicUnit, error) {
	return a.academicUnits.List(ctx, invocation, query)
}

func (s *academicUnitQueryService) List(
	ctx context.Context,
	invocation Invocation,
	query ListAcademicUnitsQuery,
) ([]*model.AcademicUnit, error) {
	parentID := strings.TrimSpace(query.ParentID)
	if parentID != "" && !model.IsValidId(parentID) {
		return nil, NewError("request.invalid").WithField("field", "parent_id")
	}
	if parentID != "" {
		resource := model.Resource{Type: model.ResourceAcademicUnit, Id: parentID}
		if err := s.authorization.Authorize(
			ctx, invocation, model.ActionAcademicUnitView, resource,
		); err != nil {
			return nil, err
		}
	}

	var institution model.Resource
	if parentID == "" {
		var err error
		institution, err = s.authorization.AuthorizeInstallation(
			ctx, invocation, model.ActionInstitutionManage,
		)
		if err != nil {
			return nil, err
		}
	} else {
		var err error
		institution, err = s.authorization.Installation(ctx)
		if err != nil {
			return nil, err
		}
	}
	units, err := s.academicUnits.ListChildren(ctx, institution.Id, parentID)
	if err != nil {
		return nil, academicUnitReadError("academic_unit", err)
	}
	return nonNilAcademicUnits(units), nil
}

// SearchAcademicUnits searches units by name. It requires institution.manage.
func (a *App) SearchAcademicUnits(
	ctx context.Context,
	invocation Invocation,
	query SearchAcademicUnitsQuery,
) ([]*model.AcademicUnit, error) {
	return a.academicUnits.Search(ctx, invocation, query)
}

func (s *academicUnitQueryService) Search(
	ctx context.Context,
	invocation Invocation,
	query SearchAcademicUnitsQuery,
) ([]*model.AcademicUnit, error) {
	institution, err := s.authorization.AuthorizeInstallation(
		ctx, invocation, model.ActionInstitutionManage,
	)
	if err != nil {
		return nil, err
	}
	units, err := s.academicUnits.Search(
		ctx,
		institution.Id,
		strings.TrimSpace(query.Query),
		normalizeAdministrationLimit(query.Limit),
	)
	if err != nil {
		return nil, academicUnitReadError("academic_unit", err)
	}
	return nonNilAcademicUnits(units), nil
}

func nonNilAcademicUnits(units []*model.AcademicUnit) []*model.AcademicUnit {
	if units == nil {
		return []*model.AcademicUnit{}
	}
	return units
}

func academicUnitReadError(resource string, err error) error {
	switch {
	case store.IsNotFound(err):
		return NewError("resource.not_found").
			WithField("resource", resource).
			Wrap(err)
	case store.IsConflict(err):
		return NewError(resource+".conflict").
			WithField("resource", resource).
			Wrap(err)
	default:
		var invalid *store.ErrInvalidInput
		var reference *store.ErrReference
		if errors.As(err, &invalid) || errors.As(err, &reference) {
			return NewError(resource+".invalid").
				WithField("resource", resource).
				Wrap(err)
		}
		return NewError("administration.unavailable").
			WithField("resource", resource).
			Wrap(err)
	}
}
