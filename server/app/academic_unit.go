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

type scopedAcademicResourceAuthorizer interface {
	Authorize(context.Context, Invocation, model.Action, model.Resource) error
	AuthorizeWithScope(context.Context, Invocation, model.Action, model.Resource) (model.RoleScopeType, string, error)
}

func (a academicUnitAuthorization) Installation(
	ctx context.Context,
) (model.Resource, error) {
	institution, err := a.institutions.GetSingleton(ctx)
	if err != nil {
		return model.Resource{}, academicUnitReadError("institution", err)
	}
	return model.Resource{Type: model.ResourceInstitution, ID: institution.ResourceID()}, nil
}

type academicUnitAuthorization struct {
	authorization *accessControlService
	institutions  store.InstitutionStore
}

func (a academicUnitAuthorization) Authorize(
	ctx context.Context,
	invocation Invocation,
	action model.Action,
	resource model.Resource,
) error {
	return a.authorization.authorizeCurrentState(
		ctx,
		invocation.Principal(),
		action,
		resource,
		invocation.RequestMetadata(),
	)
}

func (a academicUnitAuthorization) AuthorizeWithScope(
	ctx context.Context,
	invocation Invocation,
	action model.Action,
	resource model.Resource,
) (model.RoleScopeType, string, error) {
	return a.authorization.authorizeCurrentStateWithScope(
		ctx,
		invocation.Principal(),
		action,
		resource,
		invocation.RequestMetadata(),
	)
}

func (a academicUnitAuthorization) AuthorizePreflight(
	ctx context.Context,
	invocation Invocation,
	action model.Action,
	resourceType model.ResourceType,
) error {
	return a.authorization.authorizeResourcePreflight(ctx, invocation, action, resourceType)
}

func (a academicUnitAuthorization) AuthorizeUserRead(ctx context.Context, invocation Invocation, userID string) error {
	_, err := a.authorization.authorizeUserRead(ctx, invocation, userID)
	return err
}

func (a academicUnitAuthorization) AuthorizeInstallation(
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

func (a academicUnitAuthorization) AuthorizeAcademicPeriodOwner(
	ctx context.Context,
	invocation Invocation,
	action model.Action,
	period *model.AcademicPeriod,
) error {
	return a.authorization.authorizeAcademicPeriodOwner(ctx, invocation, action, period)
}

func (a academicUnitAuthorization) AuthorizeAcademicPeriodList(
	ctx context.Context,
	invocation Invocation,
) (store.AcademicPeriodVisibilityScope, error) {
	return a.authorization.authorizeAcademicPeriodList(ctx, invocation)
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
	resource := model.Resource{Type: model.ResourceAcademicUnit, ID: id}
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
		resource := model.Resource{Type: model.ResourceAcademicUnit, ID: parentID}
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
			ctx, invocation, model.ActionAcademicUnitView,
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
	units, err := s.academicUnits.ListChildren(ctx, institution.ID, parentID)
	if err != nil {
		return nil, academicUnitReadError("academic_unit", err)
	}
	return nonNilAcademicUnits(units), nil
}

// SearchAcademicUnits searches units by name through institution-scoped
// academic_unit.view.
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
		ctx, invocation, model.ActionAcademicUnitView,
	)
	if err != nil {
		return nil, err
	}
	units, err := s.academicUnits.Search(
		ctx,
		institution.ID,
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
