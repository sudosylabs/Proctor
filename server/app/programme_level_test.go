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

type programmeLevelStoreFake struct {
	events       *[]string
	current      *model.ProgrammeLevel
	created      *model.ProgrammeLevel
	createInput  *store.ProgrammeLevelCreation
	updateInput  *store.ProgrammeLevelUpdate
	archiveInput *store.ProgrammeLevelArchive
	createErr    error
}

func (s *programmeLevelStoreFake) Get(context.Context, string) (*model.ProgrammeLevel, error) {
	*s.events = append(*s.events, "get-level")
	return s.current, nil
}
func (*programmeLevelStoreFake) ListByProgramme(context.Context, string) ([]*model.ProgrammeLevel, error) {
	return nil, nil
}
func (*programmeLevelStoreFake) SearchByProgramme(context.Context, string, string, int) ([]*model.ProgrammeLevel, error) {
	return nil, nil
}
func (s *programmeLevelStoreFake) Create(_ context.Context, input *store.ProgrammeLevelCreation) (*model.ProgrammeLevel, error) {
	*s.events = append(*s.events, "store-create")
	s.createInput = input
	return s.created, s.createErr
}
func (s *programmeLevelStoreFake) UpdateWithAudit(_ context.Context, input *store.ProgrammeLevelUpdate) (*model.ProgrammeLevel, error) {
	*s.events = append(*s.events, "store-update")
	s.updateInput = input
	return input.Level, nil
}
func (s *programmeLevelStoreFake) ArchiveWithAudit(_ context.Context, input *store.ProgrammeLevelArchive) (*model.ProgrammeLevel, error) {
	*s.events = append(*s.events, "store-archive")
	s.archiveInput = input
	return s.current, nil
}

type programmeOwnerFake struct {
	events    *[]string
	programme *model.Programme
	err       error
}

func (s *programmeOwnerFake) Get(context.Context, string) (*model.Programme, error) {
	*s.events = append(*s.events, "get-programme")
	return s.programme, s.err
}

func TestProgrammeLevelCreatePreservesMissingProgrammeErrorContract(t *testing.T) {
	t.Parallel()
	events := []string{}
	programmeID := model.NewId()
	service := newProgrammeLevelService(
		&programmeLevelStoreFake{events: &events},
		&programmeOwnerFake{events: &events, err: store.NewErrNotFound("programme", programmeID)},
		&programmeAuthorizerFake{events: &events}, &institutionAuditorFake{events: &events}, time.Now, model.NewId,
	)

	_, err := service.Create(context.Background(), Invocation{}, CreateProgrammeLevelCommand{ProgrammeID: programmeID})
	failure, ok := As(err)
	if !ok || failure.Code() != "resource.not_found" || failure.Fields()["resource"] != "programme" {
		t.Fatalf("Create() error = %#v", err)
	}
	if !reflect.DeepEqual(events, []string{"get-programme"}) {
		t.Fatalf("events = %v", events)
	}
}

func TestProgrammeLevelCreatePreservesProgrammeOwnershipAndAtomicAudit(t *testing.T) {
	t.Parallel()
	events := []string{}
	unitID, programmeID, levelID, auditID := model.NewId(), model.NewId(), model.NewId(), model.NewId()
	created := &model.ProgrammeLevel{ID: model.ProgrammeLevelID(levelID), ProgrammeID: model.ProgrammeID(programmeID)}
	persistence := &programmeLevelStoreFake{events: &events, created: created}
	service := newProgrammeLevelService(
		persistence, &programmeOwnerFake{events: &events, programme: &model.Programme{ID: model.ProgrammeID(programmeID), AcademicUnitID: model.AcademicUnitID(unitID)}},
		&programmeAuthorizerFake{events: &events}, &institutionAuditorFake{events: &events, beginID: auditID},
		func() time.Time { return time.UnixMilli(500) }, func() string { return levelID },
	)
	got, err := service.Create(context.Background(), Invocation{}, CreateProgrammeLevelCommand{
		ProgrammeID: programmeID, Name: "year-1", DisplayName: "Year 1",
	})
	if err != nil || got != created {
		t.Fatalf("Create() = %#v, %v", got, err)
	}
	if persistence.createInput == nil ||
		persistence.createInput.Level.ProgrammeID.String() != programmeID ||
		persistence.createInput.Level.ID.String() != levelID ||
		persistence.createInput.AuditEventID != auditID {
		t.Fatalf("create input = %#v", persistence.createInput)
	}
	if !reflect.DeepEqual(events, []string{"get-programme", "authorize", "audit-begin", "store-create"}) {
		t.Fatalf("events = %v", events)
	}
}

func TestProgrammeLevelUpdateCannotMoveProgramme(t *testing.T) {
	t.Parallel()
	events := []string{}
	programme := &model.Programme{ID: model.ProgrammeID(model.NewId()), AcademicUnitID: model.AcademicUnitID(model.NewId())}
	current := &model.ProgrammeLevel{
		ID: model.ProgrammeLevelID(model.NewId()),
		CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(100),
		ProgrammeID: programme.ID, Name: "year-1", DisplayName: "Year 1",
	}
	persistence := &programmeLevelStoreFake{events: &events, current: current}
	service := newProgrammeLevelService(
		persistence, &programmeOwnerFake{events: &events, programme: programme},
		&programmeAuthorizerFake{events: &events}, &institutionAuditorFake{events: &events, beginID: model.NewId()},
		func() time.Time { return time.UnixMilli(500) }, model.NewId,
	)
	name := "foundation"
	updated, err := service.Update(context.Background(), Invocation{}, UpdateProgrammeLevelCommand{ID: current.ID.String(), Name: &name})
	if err != nil {
		t.Fatal(err)
	}
	if updated.ProgrammeID != current.ProgrammeID || persistence.updateInput.Level.ProgrammeID != current.ProgrammeID {
		t.Fatalf("ownership changed: %#v", updated)
	}
	if !reflect.DeepEqual(events, []string{"get-level", "get-programme", "authorize", "audit-begin", "store-update"}) {
		t.Fatalf("events = %v", events)
	}
}

func TestProgrammeLevelCreateAuthorizationDenialStopsBeforeAuditAndStore(t *testing.T) {
	t.Parallel()
	events := []string{}
	programme := &model.Programme{ID: model.ProgrammeID(model.NewId()), AcademicUnitID: model.AcademicUnitID(model.NewId())}
	service := newProgrammeLevelService(
		&programmeLevelStoreFake{events: &events}, &programmeOwnerFake{events: &events, programme: programme},
		&programmeAuthorizerFake{events: &events, err: NewError("authorization.denied")},
		&institutionAuditorFake{events: &events, beginID: model.NewId()}, time.Now, model.NewId,
	)
	_, err := service.Create(context.Background(), Invocation{}, CreateProgrammeLevelCommand{ProgrammeID: programme.ID.String(), Name: "year-1", DisplayName: "Year 1"})
	if !Is(err, "authorization.denied") {
		t.Fatalf("Create() error = %v", err)
	}
	if !reflect.DeepEqual(events, []string{"get-programme", "authorize"}) {
		t.Fatalf("events = %v", events)
	}
}

func TestProgrammeLevelCreateConflictCompletesFailedAttempt(t *testing.T) {
	t.Parallel()
	events := []string{}
	programme := &model.Programme{ID: model.ProgrammeID(model.NewId()), AcademicUnitID: model.AcademicUnitID(model.NewId())}
	auditor := &institutionAuditorFake{events: &events, beginID: model.NewId()}
	service := newProgrammeLevelService(
		&programmeLevelStoreFake{events: &events, createErr: store.NewErrConflict("programme_level", "programme_levels_active_name_key", nil)},
		&programmeOwnerFake{events: &events, programme: programme}, &programmeAuthorizerFake{events: &events},
		auditor, time.Now, model.NewId,
	)
	_, err := service.Create(context.Background(), Invocation{}, CreateProgrammeLevelCommand{ProgrammeID: programme.ID.String(), Name: "year-1", DisplayName: "Year 1"})
	if !Is(err, "programme_level.conflict") || auditor.failCode != "programme_level.conflict" {
		t.Fatalf("Create() error = %v, audit code = %q", err, auditor.failCode)
	}
	if !reflect.DeepEqual(events, []string{"get-programme", "authorize", "audit-begin", "store-create", "audit-fail"}) {
		t.Fatalf("events = %v", events)
	}
}
