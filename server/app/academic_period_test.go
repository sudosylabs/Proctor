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

type academicPeriodStoreFake struct {
	events      *[]string
	current     *model.AcademicPeriod
	created     *model.AcademicPeriod
	createInput *store.AcademicPeriodCreation
	updateInput *store.AcademicPeriodUpdate
	createErr   error
}

func (s *academicPeriodStoreFake) Get(context.Context, string) (*model.AcademicPeriod, error) {
	*s.events = append(*s.events, "get-period")
	return s.current, nil
}
func (*academicPeriodStoreFake) ListByInstitution(context.Context, string) ([]*model.AcademicPeriod, error) {
	return nil, nil
}
func (*academicPeriodStoreFake) SearchByInstitution(context.Context, string, string, int) ([]*model.AcademicPeriod, error) {
	return nil, nil
}
func (s *academicPeriodStoreFake) Create(_ context.Context, input *store.AcademicPeriodCreation) (*model.AcademicPeriod, error) {
	*s.events = append(*s.events, "store-create")
	s.createInput = input
	return s.created, s.createErr
}
func (s *academicPeriodStoreFake) UpdateWithAudit(_ context.Context, input *store.AcademicPeriodUpdate) (*model.AcademicPeriod, error) {
	*s.events = append(*s.events, "store-update")
	s.updateInput = input
	return input.Period, nil
}
func (*academicPeriodStoreFake) ArchiveWithAudit(context.Context, *store.AcademicPeriodArchive) (*model.AcademicPeriod, error) {
	return nil, nil
}

type academicPeriodAuthorizerFake struct {
	events   *[]string
	resource model.Resource
	err      error
}

func (a *academicPeriodAuthorizerFake) AuthorizeInstallation(context.Context, Invocation, model.Action) (model.Resource, error) {
	*a.events = append(*a.events, "authorize-installation")
	return a.resource, a.err
}

func TestAcademicPeriodCreateRejectsInvalidHalfOpenIntervalBeforeAudit(t *testing.T) {
	t.Parallel()
	events := []string{}
	service := newAcademicPeriodService(
		&academicPeriodStoreFake{events: &events},
		&academicPeriodAuthorizerFake{events: &events, resource: model.Resource{Type: model.ResourceInstitution, Id: model.NewId()}},
		&institutionAuditorFake{events: &events, beginID: model.NewId()}, time.Now, model.NewId,
	)
	_, err := service.Create(context.Background(), Invocation{}, CreateAcademicPeriodCommand{Name: "invalid", DisplayName: "Invalid", StartAt: 200, EndAt: 200})
	if !Is(err, "academic_period.invalid") {
		t.Fatalf("Create() error = %v", err)
	}
	if !reflect.DeepEqual(events, []string{"authorize-installation"}) {
		t.Fatalf("events = %v", events)
	}
}

func TestAcademicPeriodUpdatePreservesInstitutionOwnership(t *testing.T) {
	t.Parallel()
	events := []string{}
	period := &model.AcademicPeriod{Id: model.NewId(), CreateAt: 100, UpdateAt: 100, InstitutionId: model.NewId(), Name: "2026", DisplayName: "2026", StartAt: 100, EndAt: 200}
	persistence := &academicPeriodStoreFake{events: &events, current: period}
	service := newAcademicPeriodService(persistence, &academicPeriodAuthorizerFake{events: &events}, &institutionAuditorFake{events: &events, beginID: model.NewId()}, func() time.Time { return time.UnixMilli(500) }, model.NewId)
	name := "2026-revised"
	updated, err := service.Update(context.Background(), Invocation{}, UpdateAcademicPeriodCommand{ID: period.Id, Name: &name})
	if err != nil {
		t.Fatal(err)
	}
	if updated.InstitutionId != period.InstitutionId || persistence.updateInput.Period.InstitutionId != period.InstitutionId {
		t.Fatalf("ownership changed: %#v", updated)
	}
}

func TestAcademicPeriodCreateAuthorizationDenialStopsBeforeAuditAndStore(t *testing.T) {
	t.Parallel()
	events := []string{}
	service := newAcademicPeriodService(&academicPeriodStoreFake{events: &events}, &academicPeriodAuthorizerFake{events: &events, resource: model.Resource{Type: model.ResourceInstitution, Id: model.NewId()}, err: NewError("authorization.denied")}, &institutionAuditorFake{events: &events}, time.Now, model.NewId)
	_, err := service.Create(context.Background(), Invocation{}, CreateAcademicPeriodCommand{Name: "2026", DisplayName: "2026", StartAt: 100, EndAt: 200})
	if !Is(err, "authorization.denied") {
		t.Fatalf("Create() error = %v", err)
	}
	if !reflect.DeepEqual(events, []string{"authorize-installation"}) {
		t.Fatalf("events = %v", events)
	}
}

func TestAcademicPeriodLookupOperationsAuthorizeBeforeReading(t *testing.T) {
	t.Parallel()
	operations := map[string]func(*academicPeriodService) error{
		"get": func(service *academicPeriodService) error {
			_, err := service.Get(context.Background(), Invocation{}, GetAcademicPeriodQuery{ID: model.NewId()})
			return err
		},
		"update": func(service *academicPeriodService) error {
			_, err := service.Update(context.Background(), Invocation{}, UpdateAcademicPeriodCommand{ID: model.NewId()})
			return err
		},
		"archive": func(service *academicPeriodService) error {
			return service.Archive(context.Background(), Invocation{}, ArchiveAcademicPeriodCommand{ID: model.NewId()})
		},
	}
	for name, operation := range operations {
		name, operation := name, operation
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			events := []string{}
			service := newAcademicPeriodService(&academicPeriodStoreFake{events: &events}, &academicPeriodAuthorizerFake{events: &events, err: NewError("authorization.denied")}, &institutionAuditorFake{events: &events}, time.Now, model.NewId)
			err := operation(service)
			if !Is(err, "authorization.denied") {
				t.Fatalf("operation error = %v", err)
			}
			if !reflect.DeepEqual(events, []string{"authorize-installation"}) {
				t.Fatalf("events = %v", events)
			}
		})
	}
}

func TestAcademicPeriodCreateConflictCompletesFailedAttempt(t *testing.T) {
	t.Parallel()
	events := []string{}
	auditor := &institutionAuditorFake{events: &events, beginID: model.NewId()}
	service := newAcademicPeriodService(&academicPeriodStoreFake{events: &events, createErr: store.NewErrConflict("academic_period", "academic_periods_active_name_key", nil)}, &academicPeriodAuthorizerFake{events: &events, resource: model.Resource{Type: model.ResourceInstitution, Id: model.NewId()}}, auditor, time.Now, model.NewId)
	_, err := service.Create(context.Background(), Invocation{}, CreateAcademicPeriodCommand{Name: "2026", DisplayName: "2026", StartAt: 100, EndAt: 200})
	if !Is(err, "academic_period.conflict") || auditor.failCode != "academic_period.conflict" {
		t.Fatalf("Create() error = %v, audit code = %q", err, auditor.failCode)
	}
}

func TestAcademicPeriodCreateOwnsTemporalValidationAndAtomicAudit(t *testing.T) {
	t.Parallel()
	events := []string{}
	institutionID, periodID, auditID := model.NewId(), model.NewId(), model.NewId()
	created := &model.AcademicPeriod{Id: periodID, InstitutionId: institutionID}
	persistence := &academicPeriodStoreFake{events: &events, created: created}
	service := newAcademicPeriodService(
		persistence,
		&academicPeriodAuthorizerFake{events: &events, resource: model.Resource{Type: model.ResourceInstitution, Id: institutionID}},
		&institutionAuditorFake{events: &events, beginID: auditID},
		func() time.Time { return time.UnixMilli(500) }, func() string { return periodID },
	)

	got, err := service.Create(context.Background(), Invocation{}, CreateAcademicPeriodCommand{
		Name: "2026-2027", DisplayName: "2026-2027", StartAt: 100, EndAt: 200,
	})
	if err != nil || got != created {
		t.Fatalf("Create() = %#v, %v", got, err)
	}
	if persistence.createInput == nil || persistence.createInput.Period.InstitutionId != institutionID || persistence.createInput.Period.Id != periodID || persistence.createInput.AuditEventID != auditID {
		t.Fatalf("create input = %#v", persistence.createInput)
	}
	if !reflect.DeepEqual(events, []string{"authorize-installation", "audit-begin", "store-create"}) {
		t.Fatalf("events = %v", events)
	}
}
