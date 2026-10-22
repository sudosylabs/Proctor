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
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type academicUnitMutationStore struct {
	events       *[]string
	current      *model.AcademicUnit
	updated      *model.AcademicUnit
	archived     *model.AcademicUnit
	updateInput  *store.AcademicUnitUpdate
	archiveInput *store.AcademicUnitArchive
	updateErr    error
	archiveErr   error
}

func (*academicUnitMutationStore) Create(context.Context, *store.AcademicUnitCreation) (*model.AcademicUnit, error) {
	panic("unexpected Create")
}
func (*academicUnitMutationStore) Save(context.Context, *model.AcademicUnit) (*model.AcademicUnit, error) {
	panic("unexpected Save")
}
func (s *academicUnitMutationStore) Get(context.Context, model.AcademicUnitID) (*model.AcademicUnit, error) {
	*s.events = append(*s.events, "get")
	return s.current, nil
}
func (*academicUnitMutationStore) ListChildren(context.Context, model.InstitutionID, model.AcademicUnitID, store.AcademicNameListOptions) ([]*model.AcademicUnit, error) {
	panic("unexpected ListChildren")
}
func (*academicUnitMutationStore) ListAncestors(context.Context, string) ([]*model.AcademicUnit, error) {
	panic("unexpected ListAncestors")
}
func (*academicUnitMutationStore) Search(context.Context, model.InstitutionID, store.AcademicNameListOptions) ([]*model.AcademicUnit, error) {
	panic("unexpected Search")
}
func (s *academicUnitMutationStore) Update(
	_ context.Context,
	input *store.AcademicUnitUpdate,
) (*model.AcademicUnit, error) {
	*s.events = append(*s.events, "store-update")
	s.updateInput = input
	return s.updated, s.updateErr
}
func (s *academicUnitMutationStore) Archive(
	_ context.Context,
	input *store.AcademicUnitArchive,
) (*model.AcademicUnit, error) {
	*s.events = append(*s.events, "store-archive")
	s.archiveInput = input
	return s.archived, s.archiveErr
}

func TestAcademicUnitUpdateReparentAuthorizesBothScopesBeforeCommit(t *testing.T) {
	t.Parallel()

	events := []string{}
	unitID := model.NewID()
	oldParentID := model.NewID()
	newParentID := model.NewID()
	institutionID := model.NewID()
	current := &model.AcademicUnit{
		ID: model.AcademicUnitID(unitID), CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(100),
		InstitutionID: model.InstitutionID(institutionID), ParentID: model.AcademicUnitID(oldParentID),
		Name: "computing", DisplayName: "Computing", Revision: 1,
	}
	updated := *current
	updated.ParentID = model.AcademicUnitID(newParentID)
	updated.DisplayName = "Applied Computing"
	persistence := &academicUnitMutationStore{
		events: &events, current: current, updated: &updated,
	}
	auditor := &academicUnitCommandAuditor{events: &events, beginID: model.NewID()}
	effects := &academicUnitCommandEffectsFake{events: &events}
	service := newAcademicUnitCommandService(academicUnitCommandServiceDependencies{Store: persistence, Authorization: academicUnitAuthorizerStub{
		authorize: func(
			_ context.Context, _ invocation.Invocation, action model.Action, resource model.Resource,
		) error {
			events = append(events, "authorize-"+resource.ID)
			require.Equal(t, model.ActionAcademicUnitManage, action)
			return nil
		},
	}, Audit: auditor, Effects: effects, EffectFailures: &academicUnitEffectFailureReporterFake{events: &events}, Now: func() time.Time { return time.UnixMilli(500) }, NewID: model.NewID})
	got, err := service.UpdateAcademicUnit(context.Background(), invocation.Invocation{}, UpdateAcademicUnitCommand{
		ID: model.AcademicUnitID(unitID), ParentID: &newParentID,
		DisplayName: stringPointer("Applied Computing"),
	})
	require.NoError(t, err, "Update()")
	require.Same(t, &updated, got, "Update()")
	require.NotNil(t, persistence.updateInput)
	require.Equal(t, newParentID, persistence.updateInput.Unit.ParentID.String())
	require.Equal(t, "Applied Computing", persistence.updateInput.Unit.DisplayName)
	require.True(t, persistence.updateInput.Unit.UpdatedAt.Equal(time.UnixMilli(500).UTC()))
	require.Equal(t, model.AuditEventID(auditor.beginID), persistence.updateInput.AuditEventID)
	assertAcademicUnitCreateEvents(
		t, events,
		"authorize-"+unitID, "get", "authorize-"+newParentID,
		"audit-begin", "store-update", "effect-update",
	)
}

func TestAcademicUnitUpdateReparentToRootRequiresInstitutionAuthority(t *testing.T) {
	t.Parallel()

	events := []string{}
	unitID, parentID, institutionID := model.NewID(), model.NewID(), model.NewID()
	current := &model.AcademicUnit{
		ID: model.AcademicUnitID(unitID), CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(100),
		InstitutionID: model.InstitutionID(institutionID), ParentID: model.AcademicUnitID(parentID),
		Name: "computing", DisplayName: "Computing", Revision: 1,
	}
	persistence := &academicUnitMutationStore{events: &events, current: current}
	denied := apperror.New("authorization.denied")
	service := newAcademicUnitCommandService(academicUnitCommandServiceDependencies{Store: persistence, Authorization: academicUnitAuthorizerStub{authorize: func(_ context.Context, _ invocation.Invocation, action model.Action, resource model.Resource) error {
		events = append(events, "authorize-"+string(resource.Type)+"-"+resource.ID)
		require.Equal(t, model.ActionAcademicUnitManage, action)
		if resource.Type == model.ResourceInstitution {
			return denied
		}
		return nil
	}}, Audit: &academicUnitCommandAuditor{events: &events}, Effects: &academicUnitCommandEffectsFake{events: &events}, EffectFailures: &academicUnitEffectFailureReporterFake{events: &events}, Now: time.Now, NewID: model.NewID})
	emptyParent := ""
	_, err := service.UpdateAcademicUnit(context.Background(), invocation.Invocation{}, UpdateAcademicUnitCommand{ID: model.AcademicUnitID(unitID), ParentID: &emptyParent})
	require.True(t, apperror.Is(err, "authorization.denied"), "Update() error = %v, want authorization.denied", err)
	assertAcademicUnitCreateEvents(t, events,
		"authorize-academic_unit-"+unitID, "get", "authorize-institution-"+institutionID,
	)
}

func TestAcademicUnitUpdateCycleFailureIsAuditedWithoutPublication(t *testing.T) {
	t.Parallel()

	events := []string{}
	unitID := model.NewID()
	current := &model.AcademicUnit{
		ID: model.AcademicUnitID(unitID), CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(100),
		InstitutionID: model.InstitutionID(model.NewID()), Name: "engineering", DisplayName: "Engineering",
		Revision: 1,
	}
	persistence := &academicUnitMutationStore{
		events: &events, current: current,
		updateErr: store.NewErrConflict(
			"academic_unit", "academic_units_acyclic", errors.New("cycle"),
		),
	}
	auditor := &academicUnitCommandAuditor{events: &events, beginID: model.NewID()}
	service := newAcademicUnitCommandService(academicUnitCommandServiceDependencies{Store: persistence, Authorization: academicUnitAuthorizerFunc(func(
		context.Context, invocation.Invocation, model.Action, model.Resource,
	) error {
		events = append(events, "authorize")
		return nil
	}), Audit: auditor, Effects: &academicUnitCommandEffectsFake{events: &events}, EffectFailures: &academicUnitEffectFailureReporterFake{events: &events}, Now: time.Now, NewID: model.NewID})
	_, err := service.UpdateAcademicUnit(context.Background(), invocation.Invocation{}, UpdateAcademicUnitCommand{
		ID: model.AcademicUnitID(unitID), ParentID: stringPointer(model.NewID()),
	})
	require.True(t, apperror.Is(err, "academic_unit.conflict"), "Update() error = %v, failure audit", err)
	require.Equal(t, "academic_unit.conflict", auditor.failCode, "Update() error = %v, failure audit", err)
	assertAcademicUnitCreateEvents(
		t, events, "authorize", "get", "authorize", "audit-begin", "store-update", "audit-fail",
	)
}

func TestAcademicUnitArchiveCommitsBeforePublishing(t *testing.T) {
	t.Parallel()

	events := []string{}
	unitID := model.NewID()
	current := &model.AcademicUnit{
		ID: model.AcademicUnitID(unitID), CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(100),
		InstitutionID: model.InstitutionID(model.NewID()), Name: "engineering", DisplayName: "Engineering",
		Revision: 1,
	}
	archived := *current
	archived.UpdatedAt = time.UnixMilli(500).UTC()
	archived.ArchivedAt = model.OptionalTimeFromMillis(500)
	persistence := &academicUnitMutationStore{
		events: &events, current: current, archived: &archived,
	}
	auditor := &academicUnitCommandAuditor{events: &events, beginID: model.NewID()}
	service := newAcademicUnitCommandService(academicUnitCommandServiceDependencies{Store: persistence, Authorization: academicUnitAuthorizerFunc(func(
		context.Context, invocation.Invocation, model.Action, model.Resource,
	) error {
		events = append(events, "authorize")
		return nil
	}), Audit: auditor, Effects: &academicUnitCommandEffectsFake{events: &events}, EffectFailures: &academicUnitEffectFailureReporterFake{events: &events}, Now: func() time.Time { return time.UnixMilli(500) }, NewID: model.NewID})
	err := service.ArchiveAcademicUnit(context.Background(), invocation.Invocation{}, ArchiveAcademicUnitCommand{ID: model.AcademicUnitID(unitID)})
	require.NoError(t, err)
	require.NotNil(t, persistence.archiveInput)
	require.Equal(t, model.AcademicUnitID(unitID), persistence.archiveInput.ID)
	require.True(t, time.UnixMilli(500).UTC().Equal(persistence.archiveInput.ArchiveAt))
	require.Equal(t, model.AuditEventID(auditor.beginID), persistence.archiveInput.AuditEventID)
	assertAcademicUnitCreateEvents(
		t, events, "authorize", "get", "audit-begin", "store-archive", "effect-archive",
	)
}

func stringPointer(value string) *string { return &value }
