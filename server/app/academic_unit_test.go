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

	"github.com/sudosylabs/proctor/server/model"
)

type academicUnitAuthorizerFunc func(
	context.Context,
	Invocation,
	model.Action,
	model.Resource,
) error

type academicUnitAuthorizerStub struct {
	authorize             academicUnitAuthorizerFunc
	installation          func(context.Context) (model.Resource, error)
	authorizeInstallation func(context.Context, Invocation, model.Action) (model.Resource, error)
}

func (s academicUnitAuthorizerStub) Authorize(
	ctx context.Context,
	invocation Invocation,
	action model.Action,
	resource model.Resource,
) error {
	return s.authorize(ctx, invocation, action, resource)
}

func (s academicUnitAuthorizerStub) Installation(
	ctx context.Context,
) (model.Resource, error) {
	if s.installation != nil {
		return s.installation(ctx)
	}
	panic("unexpected Installation")
}

func (s academicUnitAuthorizerStub) AuthorizeInstallation(
	ctx context.Context,
	invocation Invocation,
	action model.Action,
) (model.Resource, error) {
	return s.authorizeInstallation(ctx, invocation, action)
}

func (f academicUnitAuthorizerFunc) Authorize(
	ctx context.Context,
	invocation Invocation,
	action model.Action,
	resource model.Resource,
) error {
	return f(ctx, invocation, action, resource)
}

func (f academicUnitAuthorizerFunc) AuthorizeInstallation(
	context.Context, Invocation, model.Action,
) (model.Resource, error) {
	panic("unexpected AuthorizeInstallation")
}

func (f academicUnitAuthorizerFunc) Installation(
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
func (s *academicUnitReadStore) Get(
	context.Context, string,
) (*model.AcademicUnit, error) {
	s.getCalls++
	return s.unit, s.err
}
func (s *academicUnitReadStore) ListChildren(
	_ context.Context, _ string, parentID string,
) ([]*model.AcademicUnit, error) {
	s.listCalls++
	s.lastParentID = parentID
	return s.children, s.err
}
func (s *academicUnitReadStore) ListAncestors(
	context.Context, string,
) ([]*model.AcademicUnit, error) {
	panic("unexpected ListAncestors")
}
func (s *academicUnitReadStore) Search(
	_ context.Context, _ string, query string, limit int,
) ([]*model.AcademicUnit, error) {
	s.searchCalls++
	s.lastQuery, s.lastLimit = query, limit
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
	service := newAcademicUnitQueryService(
		units,
		academicUnitAuthorizerFunc(func(
			context.Context, Invocation, model.Action, model.Resource,
		) error {
			return NewError("authorization.denied")
		}),
	)
	_, err := service.Get(
		context.Background(),
		NewInvocation(model.Principal{}, model.RequestMetadata{}),
		GetAcademicUnitQuery{ID: model.NewId()},
	)
	if !Is(err, "authorization.denied") {
		t.Fatalf("Get() error = %v, want authorization.denied", err)
	}
	if units.getCalls != 0 {
		t.Fatalf("persistence reads after denial = %d, want 0", units.getCalls)
	}
}

func TestAcademicUnitGetUsesUnitViewScope(t *testing.T) {
	t.Parallel()

	unit := &model.AcademicUnit{ID: model.AcademicUnitID(model.NewId()), InstitutionID: model.InstitutionID(model.NewId())}
	units := &academicUnitReadStore{unit: unit}
	var gotAction model.Action
	var gotResource model.Resource
	service := newAcademicUnitQueryService(
		units,
		academicUnitAuthorizerFunc(func(
			_ context.Context, _ Invocation, action model.Action, resource model.Resource,
		) error {
			gotAction, gotResource = action, resource
			return nil
		}),
	)
	got, err := service.Get(
		context.Background(), Invocation{}, GetAcademicUnitQuery{ID: unit.ID.String()},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got != unit {
		t.Fatalf("Get() = %#v, want %#v", got, unit)
	}
	if gotAction != model.ActionAcademicUnitView ||
		gotResource != (model.Resource{Type: model.ResourceAcademicUnit, ID: unit.ID.String()}) {
		t.Fatalf("authorization = %s %#v", gotAction, gotResource)
	}
}

func TestAcademicUnitRootListUsesInstitutionScopedAcademicUnitViewAndReturnsEmptyArray(t *testing.T) {
	t.Parallel()

	institution := &model.Institution{ID: model.InstitutionID(model.NewId())}
	units := &academicUnitReadStore{}
	var gotAction model.Action
	var gotResource model.Resource
	service := newAcademicUnitQueryService(units, academicUnitAuthorizerStub{
		authorize: func(context.Context, Invocation, model.Action, model.Resource) error {
			panic("unexpected Authorize")
		},
		authorizeInstallation: func(
			_ context.Context, _ Invocation, action model.Action,
		) (model.Resource, error) {
			gotAction = action
			gotResource = model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()}
			return gotResource, nil
		},
	})
	got, err := service.List(context.Background(), Invocation{}, ListAcademicUnitsQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("List() = %#v, want non-nil empty result", got)
	}
	if gotAction != model.ActionAcademicUnitView ||
		gotResource != (model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()}) {
		t.Fatalf("authorization = %s %#v", gotAction, gotResource)
	}
}

func TestAcademicUnitSearchNormalizesInput(t *testing.T) {
	t.Parallel()

	institution := &model.Institution{ID: model.InstitutionID(model.NewId())}
	units := &academicUnitReadStore{}
	service := newAcademicUnitQueryService(units, academicUnitAuthorizerStub{
		authorize: func(context.Context, Invocation, model.Action, model.Resource) error {
			panic("unexpected Authorize")
		},
		authorizeInstallation: func(
			context.Context, Invocation, model.Action,
		) (model.Resource, error) {
			return model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()}, nil
		},
	})
	got, err := service.Search(
		context.Background(), Invocation{},
		SearchAcademicUnitsQuery{Query: "  computing  ", Limit: 0},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || units.lastQuery != "computing" ||
		units.lastLimit != defaultAdministrationListLimit {
		t.Fatalf(
			"Search() = %#v, query = %q, limit = %d",
			got, units.lastQuery, units.lastLimit,
		)
	}
}

func TestAcademicUnitRootListDenialDoesNotReadQueryStore(t *testing.T) {
	t.Parallel()

	units := &academicUnitReadStore{}
	service := newAcademicUnitQueryService(units, academicUnitAuthorizerStub{
		authorize: func(context.Context, Invocation, model.Action, model.Resource) error {
			panic("unexpected Authorize")
		},
		authorizeInstallation: func(
			context.Context, Invocation, model.Action,
		) (model.Resource, error) {
			return model.Resource{}, NewError("authorization.denied")
		},
	})
	_, err := service.List(context.Background(), Invocation{}, ListAcademicUnitsQuery{})
	if !Is(err, "authorization.denied") {
		t.Fatalf("List() error = %v, want authorization.denied", err)
	}
	if units.listCalls != 0 {
		t.Fatalf("query store changed after denial: %#v", units)
	}
}

func TestAcademicUnitSearchDenialDoesNotReadQueryStore(t *testing.T) {
	t.Parallel()

	units := &academicUnitReadStore{}
	service := newAcademicUnitQueryService(units, academicUnitAuthorizerStub{
		authorize: func(context.Context, Invocation, model.Action, model.Resource) error {
			panic("unexpected Authorize")
		},
		authorizeInstallation: func(
			context.Context, Invocation, model.Action,
		) (model.Resource, error) {
			return model.Resource{}, NewError("authorization.denied")
		},
	})
	_, err := service.Search(context.Background(), Invocation{}, SearchAcademicUnitsQuery{})
	if !Is(err, "authorization.denied") {
		t.Fatalf("Search() error = %v, want authorization.denied", err)
	}
	if units.searchCalls != 0 {
		t.Fatalf("query store called after denial: %#v", units)
	}
}
