// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"strings"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/model"
)

// GetAcademicUnitQuery identifies one academic unit to retrieve.
type GetAcademicUnitQuery struct {
	ID model.AcademicUnitID
}

// ListAcademicUnitsQuery lists direct children of a parent unit. An empty
// ParentID lists root units of the installation institution.
type ListAcademicUnitsQuery struct {
	ParentID model.AcademicUnitID
}

// SearchAcademicUnitsQuery searches academic units by name within the
// installation institution.
type SearchAcademicUnitsQuery struct {
	Query string
	Limit int
}

type academicUnitAuthorizer interface {
	Authorize(context.Context, Invocation, model.Action, model.Resource) error
	InstitutionResource(context.Context) (model.Resource, error)
	AuthorizeInstitution(
		context.Context, Invocation, model.Action,
	) (model.Resource, error)
}

type scopedAcademicResourceAuthorizer interface {
	Authorize(context.Context, Invocation, model.Action, model.Resource) error
	AuthorizeWithScope(context.Context, Invocation, model.Action, model.Resource) (model.RoleScopeType, string, error)
}

type academicUnitQueryService struct {
	academicUnits academicUnitReader
	authorization academicUnitAuthorizer
}

type academicUnitReader interface {
	Get(context.Context, model.AcademicUnitID) (*model.AcademicUnit, error)
	ListChildren(context.Context, model.InstitutionID, model.AcademicUnitID) ([]*model.AcademicUnit, error)
	Search(context.Context, model.InstitutionID, string, int) ([]*model.AcademicUnit, error)
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
	id := query.ID
	if !id.IsValid() {
		return nil, apperror.New("request.invalid").WithField("field", "academic_unit_id")
	}
	resource := model.Resource{Type: model.ResourceAcademicUnit, ID: id.String()}
	if err := s.authorization.Authorize(
		ctx, invocation, model.ActionAcademicUnitView, resource,
	); err != nil {
		return nil, err
	}
	unit, err := s.academicUnits.Get(ctx, id)
	if err != nil {
		return nil, academicUnitStoreErrors.Translate(err)
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
	parentID := query.ParentID
	if parentID != "" && !parentID.IsValid() {
		return nil, apperror.New("request.invalid").WithField("field", "parent_id")
	}
	if parentID != "" {
		resource := model.Resource{Type: model.ResourceAcademicUnit, ID: parentID.String()}
		if err := s.authorization.Authorize(
			ctx, invocation, model.ActionAcademicUnitView, resource,
		); err != nil {
			return nil, err
		}
	}

	var institution model.Resource
	if parentID == "" {
		var err error
		institution, err = s.authorization.AuthorizeInstitution(
			ctx, invocation, model.ActionAcademicUnitView,
		)
		if err != nil {
			return nil, err
		}
	} else {
		var err error
		institution, err = s.authorization.InstitutionResource(ctx)
		if err != nil {
			return nil, err
		}
	}
	units, err := s.academicUnits.ListChildren(ctx, model.InstitutionID(institution.ID), parentID)
	if err != nil {
		return nil, academicUnitStoreErrors.Translate(err)
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
	institution, err := s.authorization.AuthorizeInstitution(
		ctx, invocation, model.ActionAcademicUnitView,
	)
	if err != nil {
		return nil, err
	}
	units, err := s.academicUnits.Search(
		ctx,
		model.InstitutionID(institution.ID),
		strings.TrimSpace(query.Query),
		normalizeAdministrationLimit(query.Limit),
	)
	if err != nil {
		return nil, academicUnitStoreErrors.Translate(err)
	}
	return nonNilAcademicUnits(units), nil
}

func nonNilAcademicUnits(units []*model.AcademicUnit) []*model.AcademicUnit {
	if units == nil {
		return []*model.AcademicUnit{}
	}
	return units
}

var academicUnitStoreErrors = apperror.AdministrationStoreTable("academic_unit", "academic_unit.conflict", "academic_unit.invalid", nil)
