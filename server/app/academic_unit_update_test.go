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
func (s *academicUnitMutationStore) Get(context.Context, string) (*model.AcademicUnit, error) {
	*s.events = append(*s.events, "get")
	return s.current, nil
}
func (*academicUnitMutationStore) ListChildren(context.Context, string, string) ([]*model.AcademicUnit, error) {
	panic("unexpected ListChildren")
}
func (*academicUnitMutationStore) ListAncestors(context.Context, string) ([]*model.AcademicUnit, error) {
	panic("unexpected ListAncestors")
}
func (*academicUnitMutationStore) Search(context.Context, string, string, int) ([]*model.AcademicUnit, error) {
	panic("unexpected Search")
}
func (*academicUnitMutationStore) Update(context.Context, *model.AcademicUnit) (*model.AcademicUnit, error) {
	panic("unexpected Update")
}
func (*academicUnitMutationStore) Archive(context.Context, string, int64) (*model.AcademicUnit, error) {
	panic("unexpected Archive")
}
func (s *academicUnitMutationStore) UpdateWithAudit(
	_ context.Context,
	input *store.AcademicUnitUpdate,
) (*model.AcademicUnit, error) {
	*s.events = append(*s.events, "store-update")
	s.updateInput = input
	return s.updated, s.updateErr
}
func (s *academicUnitMutationStore) ArchiveWithAudit(
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
	unitID := model.NewId()
	oldParentID := model.NewId()
	newParentID := model.NewId()
	institutionID := model.NewId()
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
	auditor := &academicUnitCommandAuditor{events: &events, beginID: model.NewId()}
	effects := &academicUnitCommandEffectsFake{events: &events}
	service := newAcademicUnitCommandService(
		persistence,
		academicUnitAuthorizerStub{
			authorize: func(
				_ context.Context, _ Invocation, action model.Action, resource model.Resource,
			) error {
				events = append(events, "authorize-"+resource.ID)
				if action != model.ActionAcademicUnitManage {
					t.Fatalf("action = %q", action)
				}
				return nil
			},
		},
		auditor, effects, &academicUnitEffectFailureReporterFake{events: &events},
		func() time.Time { return time.UnixMilli(500) }, model.NewId,
	)
	got, err := service.Update(context.Background(), Invocation{}, UpdateAcademicUnitCommand{
		ID: unitID, ParentID: &newParentID,
		DisplayName: stringPointer("Applied Computing"),
	})
	if err != nil || got != &updated {
		t.Fatalf("Update() = %#v, %v", got, err)
	}
	if persistence.updateInput == nil ||
		persistence.updateInput.Unit.ParentID.String() != newParentID ||
		persistence.updateInput.Unit.DisplayName != "Applied Computing" ||
		!persistence.updateInput.Unit.UpdatedAt.Equal(time.UnixMilli(500).UTC()) ||
		persistence.updateInput.AuditEventID != auditor.beginID {
		t.Fatalf("update input = %#v", persistence.updateInput)
	}
	assertAcademicUnitCreateEvents(
		t, events,
		"authorize-"+unitID, "get", "authorize-"+newParentID,
		"audit-begin", "store-update", "effect-update",
	)
}

func TestAcademicUnitUpdateReparentToRootRequiresInstitutionAuthority(t *testing.T) {
	t.Parallel()

	events := []string{}
	unitID, parentID, institutionID := model.NewId(), model.NewId(), model.NewId()
	current := &model.AcademicUnit{
		ID: model.AcademicUnitID(unitID), CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(100),
		InstitutionID: model.InstitutionID(institutionID), ParentID: model.AcademicUnitID(parentID),
		Name: "computing", DisplayName: "Computing", Revision: 1,
	}
	persistence := &academicUnitMutationStore{events: &events, current: current}
	denied := NewError("authorization.denied")
	service := newAcademicUnitCommandService(
		persistence,
		academicUnitAuthorizerStub{authorize: func(_ context.Context, _ Invocation, action model.Action, resource model.Resource) error {
			events = append(events, "authorize-"+string(resource.Type)+"-"+resource.ID)
			if action != model.ActionAcademicUnitManage {
				t.Fatalf("action = %q", action)
			}
			if resource.Type == model.ResourceInstitution {
				return denied
			}
			return nil
		}},
		&academicUnitCommandAuditor{events: &events}, &academicUnitCommandEffectsFake{events: &events},
		&academicUnitEffectFailureReporterFake{events: &events}, time.Now, model.NewId,
	)
	emptyParent := ""
	if _, err := service.Update(context.Background(), Invocation{}, UpdateAcademicUnitCommand{ID: unitID, ParentID: &emptyParent}); !Is(err, "authorization.denied") {
		t.Fatalf("Update() error = %v, want authorization.denied", err)
	}
	assertAcademicUnitCreateEvents(t, events,
		"authorize-academic_unit-"+unitID, "get", "authorize-institution-"+institutionID,
	)
}

func TestAcademicUnitUpdateCycleFailureIsAuditedWithoutPublication(t *testing.T) {
	t.Parallel()

	events := []string{}
	unitID := model.NewId()
	current := &model.AcademicUnit{
		ID: model.AcademicUnitID(unitID), CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(100),
		InstitutionID: model.InstitutionID(model.NewId()), Name: "engineering", DisplayName: "Engineering",
		Revision: 1,
	}
	persistence := &academicUnitMutationStore{
		events: &events, current: current,
		updateErr: store.NewErrConflict(
			"academic_unit", "academic_units_acyclic", errors.New("cycle"),
		),
	}
	auditor := &academicUnitCommandAuditor{events: &events, beginID: model.NewId()}
	service := newAcademicUnitCommandService(
		persistence,
		academicUnitAuthorizerFunc(func(
			context.Context, Invocation, model.Action, model.Resource,
		) error {
			events = append(events, "authorize")
			return nil
		}),
		auditor, &academicUnitCommandEffectsFake{events: &events},
		&academicUnitEffectFailureReporterFake{events: &events}, time.Now, model.NewId,
	)
	_, err := service.Update(context.Background(), Invocation{}, UpdateAcademicUnitCommand{
		ID: unitID, ParentID: stringPointer(model.NewId()),
	})
	if !Is(err, "academic_unit.conflict") || auditor.failCode != "academic_unit.conflict" {
		t.Fatalf("Update() error = %v, failure audit = %q", err, auditor.failCode)
	}
	assertAcademicUnitCreateEvents(
		t, events, "authorize", "get", "authorize", "audit-begin", "store-update", "audit-fail",
	)
}

func TestAcademicUnitArchiveCommitsBeforePublishing(t *testing.T) {
	t.Parallel()

	events := []string{}
	unitID := model.NewId()
	current := &model.AcademicUnit{
		ID: model.AcademicUnitID(unitID), CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(100),
		InstitutionID: model.InstitutionID(model.NewId()), Name: "engineering", DisplayName: "Engineering",
		Revision: 1,
	}
	archived := *current
	archived.UpdatedAt = time.UnixMilli(500).UTC()
	archived.ArchivedAt = model.OptionalTimeFromMillis(500)
	persistence := &academicUnitMutationStore{
		events: &events, current: current, archived: &archived,
	}
	auditor := &academicUnitCommandAuditor{events: &events, beginID: model.NewId()}
	service := newAcademicUnitCommandService(
		persistence,
		academicUnitAuthorizerFunc(func(
			context.Context, Invocation, model.Action, model.Resource,
		) error {
			events = append(events, "authorize")
			return nil
		}),
		auditor, &academicUnitCommandEffectsFake{events: &events},
		&academicUnitEffectFailureReporterFake{events: &events},
		func() time.Time { return time.UnixMilli(500) }, model.NewId,
	)
	err := service.Archive(context.Background(), Invocation{}, ArchiveAcademicUnitCommand{ID: unitID})
	if err != nil {
		t.Fatal(err)
	}
	if persistence.archiveInput == nil || persistence.archiveInput.ID != unitID ||
		persistence.archiveInput.ArchiveAt != 500 ||
		persistence.archiveInput.AuditEventID != auditor.beginID {
		t.Fatalf("archive input = %#v", persistence.archiveInput)
	}
	assertAcademicUnitCreateEvents(
		t, events, "authorize", "get", "audit-begin", "store-archive", "effect-archive",
	)
}

func stringPointer(value string) *string { return &value }
