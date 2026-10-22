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
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// GetAcademicUnitQuery identifies one academic unit to retrieve.
type GetAcademicUnitQuery struct {
	ID model.AcademicUnitID
}

// ListAcademicUnitsQuery lists direct children of a parent unit. An empty
// ParentID lists root units of the installation institution.
type ListAcademicUnitsQuery struct {
	ParentID model.AcademicUnitID
	NamePageQuery
}

// SearchAcademicUnitsQuery searches academic units by name within the
// installation institution.
type SearchAcademicUnitsQuery struct {
	Query string
	NamePageQuery
}

type academicUnitAuthorizer interface {
	Authorize(context.Context, invocation.Invocation, model.Action, model.Resource) error
	InstitutionResource(context.Context) (model.Resource, error)
	AuthorizeInstitution(
		context.Context, invocation.Invocation, model.Action,
	) (model.Resource, error)
}

type scopedAcademicResourceAuthorizer interface {
	Authorize(context.Context, invocation.Invocation, model.Action, model.Resource) error
	AuthorizeWithScope(context.Context, invocation.Invocation, model.Action, model.Resource) (model.RoleScopeType, string, error)
}

// academicUnitQueryServiceDependencies are the collaborators of academicUnitQueryService;
// newAcademicUnitQueryService validates them once and academicUnitQueryService keeps them in deps.
type academicUnitQueryServiceDependencies struct {
	AcademicUnits academicUnitReader
	Authorization academicUnitAuthorizer
}

type academicUnitQueryService struct {
	deps academicUnitQueryServiceDependencies
}

type academicUnitReader interface {
	Get(context.Context, model.AcademicUnitID) (*model.AcademicUnit, error)
	ListChildren(context.Context, model.InstitutionID, model.AcademicUnitID, store.AcademicNameListOptions) ([]*model.AcademicUnit, error)
	Search(context.Context, model.InstitutionID, store.AcademicNameListOptions) ([]*model.AcademicUnit, error)
}

func newAcademicUnitQueryService(deps academicUnitQueryServiceDependencies) *academicUnitQueryService {
	return &academicUnitQueryService{deps: deps}
}

// GetAcademicUnit returns one academic unit after authorizing
// academic_unit.view on that unit.
func (s *academicUnitQueryService) GetAcademicUnit(
	ctx context.Context,
	invocation invocation.Invocation,
	query GetAcademicUnitQuery,
) (*model.AcademicUnit, error) {
	id := query.ID
	if !id.IsValid() {
		return nil, apperror.New("request.invalid").WithField("field", "academic_unit_id")
	}
	resource := model.Resource{Type: model.ResourceAcademicUnit, ID: id.String()}
	if err := s.deps.Authorization.Authorize(
		ctx, invocation, model.ActionAcademicUnitView, resource,
	); err != nil {
		return nil, err
	}
	unit, err := s.deps.AcademicUnits.Get(ctx, id)
	if err != nil {
		return nil, academicUnitStoreErrors.Translate(err)
	}
	return unit, nil
}

// ListAcademicUnits lists direct children under ParentID, or root units when
// ParentID is empty.
func (s *academicUnitQueryService) ListAcademicUnits(
	ctx context.Context,
	invocation invocation.Invocation,
	query ListAcademicUnitsQuery,
) (Page[*model.AcademicUnit], error) {
	parentID := query.ParentID
	if parentID != "" && !parentID.IsValid() {
		return Page[*model.AcademicUnit]{}, apperror.New("request.invalid").WithField("field", "parent_id")
	}
	limit, probe, err := resolvePageLimit(query.Limit, "limit")
	if err != nil {
		return Page[*model.AcademicUnit]{}, err
	}
	if parentID != "" {
		resource := model.Resource{Type: model.ResourceAcademicUnit, ID: parentID.String()}
		if err = s.deps.Authorization.Authorize(
			ctx, invocation, model.ActionAcademicUnitView, resource,
		); err != nil {
			return Page[*model.AcademicUnit]{}, err
		}
	}

	var institution model.Resource
	if parentID == "" {
		institution, err = s.deps.Authorization.AuthorizeInstitution(
			ctx, invocation, model.ActionAcademicUnitView,
		)
	} else {
		institution, err = s.deps.Authorization.InstitutionResource(ctx)
	}
	if err != nil {
		return Page[*model.AcademicUnit]{}, err
	}
	units, err := s.deps.AcademicUnits.ListChildren(ctx, model.InstitutionID(institution.ID), parentID, store.AcademicNameListOptions{
		AfterName: query.AfterName, AfterID: query.AfterID, Limit: probe,
	})
	if err != nil {
		return Page[*model.AcademicUnit]{}, academicUnitStoreErrors.Translate(err)
	}
	return newPage(units, limit), nil
}

// SearchAcademicUnits searches units by name through institution-scoped
// academic_unit.view.
func (s *academicUnitQueryService) SearchAcademicUnits(
	ctx context.Context,
	invocation invocation.Invocation,
	query SearchAcademicUnitsQuery,
) (Page[*model.AcademicUnit], error) {
	limit, probe, err := resolvePageLimit(query.Limit, "limit")
	if err != nil {
		return Page[*model.AcademicUnit]{}, err
	}
	institution, err := s.deps.Authorization.AuthorizeInstitution(
		ctx, invocation, model.ActionAcademicUnitView,
	)
	if err != nil {
		return Page[*model.AcademicUnit]{}, err
	}
	units, err := s.deps.AcademicUnits.Search(ctx, model.InstitutionID(institution.ID), store.AcademicNameListOptions{
		Term: strings.TrimSpace(query.Query), AfterName: query.AfterName, AfterID: query.AfterID, Limit: probe,
	})
	if err != nil {
		return Page[*model.AcademicUnit]{}, academicUnitStoreErrors.Translate(err)
	}
	return newPage(units, limit), nil
}

var academicUnitStoreErrors = apperror.AdministrationStoreTable("academic_unit", "academic_unit.conflict", "academic_unit.invalid", nil)
