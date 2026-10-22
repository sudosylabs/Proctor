// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type academicUnitAuthorizerFunc func(
	context.Context,
	invocation.Invocation,
	model.Action,
	model.Resource,
) error

type academicUnitAuthorizerStub struct {
	authorize             academicUnitAuthorizerFunc
	installation          func(context.Context) (model.Resource, error)
	authorizeInstallation func(context.Context, invocation.Invocation, model.Action) (model.Resource, error)
}

func (s academicUnitAuthorizerStub) Authorize(
	ctx context.Context,
	invocation invocation.Invocation,
	action model.Action,
	resource model.Resource,
) error {
	return s.authorize(ctx, invocation, action, resource)
}

func (s academicUnitAuthorizerStub) InstitutionResource(
	ctx context.Context,
) (model.Resource, error) {
	if s.installation != nil {
		return s.installation(ctx)
	}
	panic("unexpected Installation")
}

func (s academicUnitAuthorizerStub) AuthorizeInstitution(
	ctx context.Context,
	invocation invocation.Invocation,
	action model.Action,
) (model.Resource, error) {
	return s.authorizeInstallation(ctx, invocation, action)
}

func (f academicUnitAuthorizerFunc) Authorize(
	ctx context.Context,
	invocation invocation.Invocation,
	action model.Action,
	resource model.Resource,
) error {
	return f(ctx, invocation, action, resource)
}

func (f academicUnitAuthorizerFunc) AuthorizeInstitution(
	context.Context, invocation.Invocation, model.Action,
) (model.Resource, error) {
	panic("unexpected AuthorizeInstallation")
}

func (f academicUnitAuthorizerFunc) InstitutionResource(
	context.Context,
) (model.Resource, error) {
	panic("unexpected Installation")
}

type academicUnitReadStore struct {
	getCalls      int
	listCalls     int
	searchCalls   int
	unit          *model.AcademicUnit
	children      []*model.AcademicUnit
	searchResults []*model.AcademicUnit
	lastParentID  string
	lastQuery     string
	lastLimit     int
	err           error
}

func (s *academicUnitReadStore) Save(
	context.Context, *model.AcademicUnit,
) (*model.AcademicUnit, error) {
	panic("unexpected Save")
}
func (s *academicUnitReadStore) Get(context.Context, model.AcademicUnitID) (*model.AcademicUnit, error) {
	s.getCalls++
	return s.unit, s.err
}
func (s *academicUnitReadStore) ListChildren(_ context.Context, _ model.InstitutionID, parentID model.AcademicUnitID, _ store.AcademicNameListOptions) ([]*model.AcademicUnit, error) {
	s.listCalls++
	s.lastParentID = parentID.String()
	return s.children, s.err
}
func (s *academicUnitReadStore) ListAncestors(
	context.Context, string,
) ([]*model.AcademicUnit, error) {
	panic("unexpected ListAncestors")
}
func (s *academicUnitReadStore) Search(_ context.Context, _ model.InstitutionID, options store.AcademicNameListOptions) ([]*model.AcademicUnit, error) {
	s.searchCalls++
	s.lastQuery, s.lastLimit = options.Term, options.Limit
	return s.searchResults, s.err
}
func (s *academicUnitReadStore) Update(
	context.Context, *model.AcademicUnit,
) (*model.AcademicUnit, error) {
	panic("unexpected Update")
}
func (s *academicUnitReadStore) Archive(
	context.Context, string, int64,
) (*model.AcademicUnit, error) {
	panic("unexpected Archive")
}

func TestAcademicUnitGetDenialDoesNotReadPersistence(t *testing.T) {
	t.Parallel()

	units := &academicUnitReadStore{}
	service := newAcademicUnitQueryService(academicUnitQueryServiceDependencies{AcademicUnits: units, Authorization: academicUnitAuthorizerFunc(func(
		context.Context, invocation.Invocation, model.Action, model.Resource,
	) error {
		return apperror.New("authorization.denied")
	})})
	_, err := service.GetAcademicUnit(
		context.Background(),
		invocation.New(model.Principal{}, model.RequestMetadata{}),
		GetAcademicUnitQuery{ID: model.NewAcademicUnitID()},
	)
	require.True(t, apperror.Is(err, "authorization.denied"), "Get() error = %v, want authorization.denied", err)
	require.Equal(t, 0, units.getCalls, "persistence reads after denial")
}

func TestAcademicUnitGetUsesUnitViewScope(t *testing.T) {
	t.Parallel()

	unit := &model.AcademicUnit{ID: model.AcademicUnitID(model.NewID()), InstitutionID: model.InstitutionID(model.NewID())}
	units := &academicUnitReadStore{unit: unit}
	var gotAction model.Action
	var gotResource model.Resource
	service := newAcademicUnitQueryService(academicUnitQueryServiceDependencies{AcademicUnits: units, Authorization: academicUnitAuthorizerFunc(func(
		_ context.Context, _ invocation.Invocation, action model.Action, resource model.Resource,
	) error {
		gotAction, gotResource = action, resource
		return nil
	})})
	got, err := service.GetAcademicUnit(
		context.Background(), invocation.Invocation{}, GetAcademicUnitQuery{ID: unit.ID},
	)
	require.NoError(t, err)
	require.Same(t, unit, got, "Get()")
	require.Equal(t, model.ActionAcademicUnitView, gotAction, "authorization")
	require.Equal(t, model.Resource{Type: model.ResourceAcademicUnit, ID: unit.ID.String()}, gotResource, "authorization")
}

func TestAcademicUnitRootListUsesInstitutionScopedAcademicUnitViewAndReturnsEmptyArray(t *testing.T) {
	t.Parallel()

	institution := &model.Institution{ID: model.InstitutionID(model.NewID())}
	units := &academicUnitReadStore{}
	var gotAction model.Action
	var gotResource model.Resource
	service := newAcademicUnitQueryService(academicUnitQueryServiceDependencies{AcademicUnits: units, Authorization: academicUnitAuthorizerStub{
		authorize: func(context.Context, invocation.Invocation, model.Action, model.Resource) error {
			panic("unexpected Authorize")
		},
		authorizeInstallation: func(
			_ context.Context, _ invocation.Invocation, action model.Action,
		) (model.Resource, error) {
			gotAction = action
			gotResource = model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()}
			return gotResource, nil
		},
	}})
	got, err := service.ListAcademicUnits(context.Background(), invocation.Invocation{}, ListAcademicUnitsQuery{})
	require.NoError(t, err)
	require.NotNil(t, got.Items, "List()")
	require.Empty(t, got.Items, "List()")
	require.Equal(t, model.ActionAcademicUnitView, gotAction, "authorization")
	require.Equal(t, model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()}, gotResource, "authorization")
}

func TestAcademicUnitSearchNormalizesInput(t *testing.T) {
	t.Parallel()

	institution := &model.Institution{ID: model.InstitutionID(model.NewID())}
	units := &academicUnitReadStore{}
	service := newAcademicUnitQueryService(academicUnitQueryServiceDependencies{AcademicUnits: units, Authorization: academicUnitAuthorizerStub{
		authorize: func(context.Context, invocation.Invocation, model.Action, model.Resource) error {
			panic("unexpected Authorize")
		},
		authorizeInstallation: func(
			context.Context, invocation.Invocation, model.Action,
		) (model.Resource, error) {
			return model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()}, nil
		},
	}})
	got, err := service.SearchAcademicUnits(
		context.Background(), invocation.Invocation{},
		SearchAcademicUnitsQuery{Query: "  computing  "},
	)
	require.NoError(t, err)
	require.NotNil(t, got, "Search()")
	require.Equal(t, "computing", units.lastQuery, "Search()")
	require.Equal(t, DefaultPageLimit+1, units.lastLimit, "Search() probes one row past the default page")
}

func TestAcademicUnitRootListDenialDoesNotReadQueryStore(t *testing.T) {
	t.Parallel()

	units := &academicUnitReadStore{}
	service := newAcademicUnitQueryService(academicUnitQueryServiceDependencies{AcademicUnits: units, Authorization: academicUnitAuthorizerStub{
		authorize: func(context.Context, invocation.Invocation, model.Action, model.Resource) error {
			panic("unexpected Authorize")
		},
		authorizeInstallation: func(
			context.Context, invocation.Invocation, model.Action,
		) (model.Resource, error) {
			return model.Resource{}, apperror.New("authorization.denied")
		},
	}})
	_, err := service.ListAcademicUnits(context.Background(), invocation.Invocation{}, ListAcademicUnitsQuery{})
	require.True(t, apperror.Is(err, "authorization.denied"), "List() error = %v, want authorization.denied", err)
	require.Equal(t, 0, units.listCalls)
}

func TestAcademicUnitSearchDenialDoesNotReadQueryStore(t *testing.T) {
	t.Parallel()

	units := &academicUnitReadStore{}
	service := newAcademicUnitQueryService(academicUnitQueryServiceDependencies{AcademicUnits: units, Authorization: academicUnitAuthorizerStub{
		authorize: func(context.Context, invocation.Invocation, model.Action, model.Resource) error {
			panic("unexpected Authorize")
		},
		authorizeInstallation: func(
			context.Context, invocation.Invocation, model.Action,
		) (model.Resource, error) {
			return model.Resource{}, apperror.New("authorization.denied")
		},
	}})
	_, err := service.SearchAcademicUnits(context.Background(), invocation.Invocation{}, SearchAcademicUnitsQuery{})
	require.True(t, apperror.Is(err, "authorization.denied"), "Search() error = %v, want authorization.denied", err)
	require.Equal(t, 0, units.searchCalls)
}
