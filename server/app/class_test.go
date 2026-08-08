// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type classStoreFake struct {
	events      *[]string
	current     *model.Class
	unitID      string
	created     *model.Class
	createInput *store.ClassCreation
	updateInput *store.ClassUpdate
	createErr   error
	getUnitErr  error
}

func (s *classStoreFake) Get(context.Context, string) (*model.Class, error) {
	*s.events = append(*s.events, "get-class")
	return s.current, nil
}
func (*classStoreFake) ListByProgrammeLevel(context.Context, string) ([]*model.Class, error) {
	return nil, nil
}
func (*classStoreFake) SearchByAcademicUnit(context.Context, string, string, int) ([]*model.Class, error) {
	return nil, nil
}
func (s *classStoreFake) GetAcademicUnitId(context.Context, string) (string, error) {
	*s.events = append(*s.events, "get-unit")
	return s.unitID, s.getUnitErr
}
func (s *classStoreFake) Create(_ context.Context, input *store.ClassCreation) (*model.Class, error) {
	*s.events = append(*s.events, "store-create")
	s.createInput = input
	return s.created, s.createErr
}
func (s *classStoreFake) UpdateWithAudit(_ context.Context, input *store.ClassUpdate) (*model.Class, error) {
	*s.events = append(*s.events, "store-update")
	s.updateInput = input
	return input.Class, nil
}
func (*classStoreFake) ArchiveWithAudit(context.Context, *store.ClassArchive) (*model.Class, error) {
	return nil, nil
}

func TestClassGetAuthorizesExactScopeBeforeReading(t *testing.T) {
	t.Parallel()
	events := []string{}
	service := newClassService(&classStoreFake{events: &events}, &classProgrammeLevelFake{events: &events}, &programmeOwnerFake{events: &events}, &programmeAuthorizerFake{events: &events, err: NewError("authorization.denied")}, &institutionAuditorFake{events: &events}, time.Now, model.NewId)
	_, err := service.Get(context.Background(), Invocation{}, GetClassQuery{ID: model.NewId()})
	if !Is(err, "authorization.denied") {
		t.Fatalf("Get() error = %v", err)
	}
	if !reflect.DeepEqual(events, []string{"authorize"}) {
		t.Fatalf("events = %v", events)
	}
}

func TestClassUpdateMoveAuthorizesCurrentAndDestinationOwners(t *testing.T) {
	t.Parallel()
	events := []string{}
	oldUnitID, newUnitID, programmeID, newLevelID := model.NewId(), model.NewId(), model.NewId(), model.NewId()
	current := &model.Class{
		ID:               model.ClassID(model.NewId()),
		CreatedAt:        model.TimeFromMillis(100),
		UpdatedAt:        model.TimeFromMillis(100),
		Revision:         7,
		ProgrammeLevelID: model.ProgrammeLevelID(model.NewId()),
		AcademicPeriodID: model.AcademicPeriodID(model.NewId()),
		Name:             "class-a",
		DisplayName:      "Class A",
	}
	persistence := &classStoreFake{events: &events, current: current, unitID: oldUnitID}
	service := newClassService(persistence, &classProgrammeLevelFake{events: &events, level: &model.ProgrammeLevel{ID: model.ProgrammeLevelID(newLevelID), ProgrammeID: model.ProgrammeID(programmeID)}}, &programmeOwnerFake{events: &events, programme: &model.Programme{ID: model.ProgrammeID(programmeID), AcademicUnitID: model.AcademicUnitID(newUnitID)}}, &programmeAuthorizerFake{events: &events}, &institutionAuditorFake{events: &events, beginID: model.NewId()}, func() time.Time { return time.UnixMilli(500) }, model.NewId)
	updated, err := service.Update(context.Background(), Invocation{}, UpdateClassCommand{ID: current.ID.String(), ProgrammeLevelID: &newLevelID})
	if err != nil {
		t.Fatal(err)
	}
	if updated.ProgrammeLevelID.String() != newLevelID || persistence.updateInput.Class.AcademicPeriodID != current.AcademicPeriodID {
		t.Fatalf("updated = %#v", updated)
	}
	if persistence.updateInput.ExpectedAcademicUnitID != oldUnitID || persistence.updateInput.ExpectedRevision != current.Revision {
		t.Fatalf("update precondition = %#v", persistence.updateInput)
	}
	want := []string{"get-unit", "authorize", "get-class", "get-level", "get-programme", "authorize", "audit-begin", "store-update"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestClassMutationMissingClassUsesClassResource(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		run  func(*classService) error
	}{
		{name: "update", run: func(service *classService) error {
			_, err := service.Update(context.Background(), Invocation{}, UpdateClassCommand{ID: model.NewId()})
			return err
		}},
		{name: "archive", run: func(service *classService) error {
			return service.Archive(context.Background(), Invocation{}, ArchiveClassCommand{ID: model.NewId()})
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			events := []string{}
			service := newClassService(&classStoreFake{events: &events, getUnitErr: store.NewErrNotFound("class", model.NewId())}, &classProgrammeLevelFake{events: &events}, &programmeOwnerFake{events: &events}, &programmeAuthorizerFake{events: &events}, &institutionAuditorFake{events: &events}, time.Now, model.NewId)
			err := test.run(service)
			appErr, ok := As(err)
			if !ok || appErr.Code() != "resource.not_found" || appErr.Fields()["resource"] != "class" {
				t.Fatalf("error = %#v", err)
			}
		})
	}
}

func TestClassCreateConflictCompletesFailedAttempt(t *testing.T) {
	t.Parallel()
	events := []string{}
	levelID, programmeID := model.NewId(), model.NewId()
	auditor := &institutionAuditorFake{events: &events, beginID: model.NewId()}
	service := newClassService(&classStoreFake{events: &events, createErr: store.NewErrConflict("class", "classes_programme_level_id_academic_period_id_name_key", nil)}, &classProgrammeLevelFake{events: &events, level: &model.ProgrammeLevel{ID: model.ProgrammeLevelID(levelID), ProgrammeID: model.ProgrammeID(programmeID)}}, &programmeOwnerFake{events: &events, programme: &model.Programme{ID: model.ProgrammeID(programmeID), AcademicUnitID: model.AcademicUnitID(model.NewId())}}, &programmeAuthorizerFake{events: &events}, auditor, time.Now, model.NewId)
	_, err := service.Create(context.Background(), Invocation{}, CreateClassCommand{ProgrammeLevelID: levelID, AcademicPeriodID: model.NewId(), Name: "class-a", DisplayName: "Class A"})
	if !Is(err, "class.conflict") || auditor.failCode != "class.conflict" {
		t.Fatalf("Create() error = %v, audit = %q", err, auditor.failCode)
	}
}

type classProgrammeLevelFake struct {
	events *[]string
	level  *model.ProgrammeLevel
}

func (s *classProgrammeLevelFake) Get(context.Context, string) (*model.ProgrammeLevel, error) {
	*s.events = append(*s.events, "get-level")
	return s.level, nil
}

func TestClassCreatePreservesBothParentsAndAtomicAudit(t *testing.T) {
	t.Parallel()
	events := []string{}
	unitID, programmeID, levelID, periodID, classID, auditID := model.NewId(), model.NewId(), model.NewId(), model.NewId(), model.NewId(), model.NewId()
	created := &model.Class{ID: model.ClassID(classID), ProgrammeLevelID: model.ProgrammeLevelID(levelID), AcademicPeriodID: model.AcademicPeriodID(periodID)}
	persistence := &classStoreFake{events: &events, created: created}
	service := newClassService(persistence, &classProgrammeLevelFake{events: &events, level: &model.ProgrammeLevel{ID: model.ProgrammeLevelID(levelID), ProgrammeID: model.ProgrammeID(programmeID)}}, &programmeOwnerFake{events: &events, programme: &model.Programme{ID: model.ProgrammeID(programmeID), AcademicUnitID: model.AcademicUnitID(unitID)}}, &programmeAuthorizerFake{events: &events}, &institutionAuditorFake{events: &events, beginID: auditID}, func() time.Time { return time.UnixMilli(500) }, func() string { return classID })
	got, err := service.Create(context.Background(), Invocation{}, CreateClassCommand{ProgrammeLevelID: levelID, AcademicPeriodID: periodID, Name: "class-a", DisplayName: "Class A"})
	if err != nil || got != created {
		t.Fatalf("Create() = %#v, %v", got, err)
	}
	if persistence.createInput == nil ||
		persistence.createInput.Class.ProgrammeLevelID.String() != levelID ||
		persistence.createInput.Class.AcademicPeriodID.String() != periodID ||
		persistence.createInput.AuditEventID != auditID {
		t.Fatalf("create input = %#v", persistence.createInput)
	}
	if !reflect.DeepEqual(events, []string{"get-level", "get-programme", "authorize", "audit-begin", "store-create"}) {
		t.Fatalf("events = %v", events)
	}
}
