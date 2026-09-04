// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

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
	idempotency *store.CommandIdempotency
	createErr   error
}

func (s *academicPeriodStoreFake) CreateIdempotently(_ context.Context, input *store.AcademicPeriodCreation, command *store.CommandIdempotency) (*store.AcademicPeriodCommandResult, error) {
	s.createInput, s.idempotency = input, command
	return &store.AcademicPeriodCommandResult{Value: input.Period}, nil
}

func (s *academicPeriodStoreFake) Get(context.Context, string) (*model.AcademicPeriod, error) {
	*s.events = append(*s.events, "get-period")
	return s.current, nil
}
func (*academicPeriodStoreFake) ListVisible(context.Context, store.AcademicPeriodVisibilityScope, string, int) ([]*model.AcademicPeriod, error) {
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
	period   *model.AcademicPeriod
	action   model.Action
	err      error
}

func (a *academicPeriodAuthorizerFake) Authorize(_ context.Context, _ Invocation, action model.Action, resource model.Resource) error {
	*a.events = append(*a.events, "authorize-period")
	a.resource = resource
	a.action = action
	return a.err
}
func (a *academicPeriodAuthorizerFake) AuthorizeAcademicPeriodOwner(_ context.Context, _ Invocation, action model.Action, period *model.AcademicPeriod) error {
	*a.events = append(*a.events, "authorize-period-owner")
	a.resource = period.Owner.Resource()
	a.action, a.period = action, period
	return a.err
}
func (a *academicPeriodAuthorizerFake) AuthorizeAcademicPeriodList(context.Context, Invocation) (store.AcademicPeriodVisibilityScope, error) {
	*a.events = append(*a.events, "authorize-period-list")
	return store.AcademicPeriodVisibilityScope{InstitutionID: a.resource.ID, InstitutionWide: true}, a.err
}

func TestAcademicPeriodCreateRejectsInvalidHalfOpenIntervalBeforeAudit(t *testing.T) {
	t.Parallel()
	events := []string{}
	service := newAcademicPeriodService(
		&academicPeriodStoreFake{events: &events},
		&academicPeriodAuthorizerFake{events: &events},
		&institutionAuditorFake{events: &events, beginID: model.NewId()}, time.Now, model.NewId,
	)
	_, err := service.Create(context.Background(), Invocation{}, CreateAcademicPeriodCommand{OwnerType: string(model.ResourceInstitution), OwnerID: model.NewId(), Name: "invalid", DisplayName: "Invalid", StartAt: 200, EndAt: 200})
	if !Is(err, "academic_period.invalid") {
		t.Fatalf("Create() error = %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %v", events)
	}
}

func TestAcademicPeriodUpdatePreservesInstitutionOwnership(t *testing.T) {
	t.Parallel()
	events := []string{}
	period := &model.AcademicPeriod{
		ID:          model.AcademicPeriodID(model.NewId()),
		CreatedAt:   model.TimeFromMillis(100),
		UpdatedAt:   model.TimeFromMillis(100),
		Revision:    1,
		Owner:       model.NewInstitutionAcademicPeriodOwner(model.NewInstitutionID()),
		Name:        "2026",
		DisplayName: "2026",
		StartsAt:    model.TimeFromMillis(100),
		EndsAt:      model.TimeFromMillis(200),
	}
	persistence := &academicPeriodStoreFake{events: &events, current: period}
	service := newAcademicPeriodService(persistence, &academicPeriodAuthorizerFake{events: &events}, &institutionAuditorFake{events: &events, beginID: model.NewId()}, func() time.Time { return time.UnixMilli(500) }, model.NewId)
	name := "2026-revised"
	updated, err := service.Update(context.Background(), Invocation{}, UpdateAcademicPeriodCommand{ID: period.ID.String(), Name: &name})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Owner != period.Owner || persistence.updateInput.Period.Owner != period.Owner {
		t.Fatalf("ownership changed: %#v", updated)
	}
}

func TestAcademicPeriodCreateAuthorizationDenialStopsBeforeAuditAndStore(t *testing.T) {
	t.Parallel()
	events := []string{}
	service := newAcademicPeriodService(&academicPeriodStoreFake{events: &events}, &academicPeriodAuthorizerFake{events: &events, err: NewError("authorization.denied")}, &institutionAuditorFake{events: &events}, time.Now, model.NewId)
	_, err := service.Create(context.Background(), Invocation{}, CreateAcademicPeriodCommand{OwnerType: string(model.ResourceInstitution), OwnerID: model.NewId(), Name: "2026", DisplayName: "2026", StartAt: 100, EndAt: 200})
	if !Is(err, "authorization.denied") {
		t.Fatalf("Create() error = %v", err)
	}
	if !reflect.DeepEqual(events, []string{"authorize-period-owner"}) {
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
			if !reflect.DeepEqual(events, []string{"authorize-period"}) {
				t.Fatalf("events = %v", events)
			}
		})
	}
}

func TestAcademicPeriodCreateConflictCompletesFailedAttempt(t *testing.T) {
	t.Parallel()
	events := []string{}
	auditor := &institutionAuditorFake{events: &events, beginID: model.NewId()}
	service := newAcademicPeriodService(&academicPeriodStoreFake{events: &events, createErr: store.NewErrConflict("academic_period", "academic_periods_institution_id_name_key", nil)}, &academicPeriodAuthorizerFake{events: &events}, auditor, time.Now, model.NewId)
	_, err := service.Create(context.Background(), Invocation{}, CreateAcademicPeriodCommand{OwnerType: string(model.ResourceInstitution), OwnerID: model.NewId(), Name: "2026", DisplayName: "2026", StartAt: 100, EndAt: 200})
	if !Is(err, "academic_period.conflict") || auditor.failCode != "academic_period.conflict" {
		t.Fatalf("Create() error = %v, audit code = %q", err, auditor.failCode)
	}
}

func TestAcademicPeriodCreateOwnsTemporalValidationAndAtomicAudit(t *testing.T) {
	t.Parallel()
	events := []string{}
	institutionID, periodID, auditID := model.NewId(), model.NewId(), model.NewId()
	created := &model.AcademicPeriod{ID: model.AcademicPeriodID(periodID), Owner: model.NewInstitutionAcademicPeriodOwner(model.InstitutionID(institutionID))}
	persistence := &academicPeriodStoreFake{events: &events, created: created}
	auditor := &institutionAuditorFake{events: &events, beginID: auditID}
	authorizer := &academicPeriodAuthorizerFake{events: &events, resource: model.Resource{Type: model.ResourceInstitution, ID: institutionID}}
	service := newAcademicPeriodService(
		persistence,
		authorizer,
		auditor,
		func() time.Time { return time.UnixMilli(500) }, func() string { return periodID },
	)

	got, err := service.Create(context.Background(), Invocation{}, CreateAcademicPeriodCommand{
		OwnerType: string(model.ResourceInstitution), OwnerID: institutionID,
		Name: "2026-2027", DisplayName: "2026-2027", StartAt: 100, EndAt: 200,
	})
	if err != nil || got != created {
		t.Fatalf("Create() = %#v, %v", got, err)
	}
	if persistence.createInput == nil ||
		persistence.createInput.Period.Owner.InstitutionID.String() != institutionID ||
		persistence.createInput.Period.ID.String() != periodID ||
		persistence.createInput.AuditEventID != auditID {
		t.Fatalf("create input = %#v", persistence.createInput)
	}
	if auditor.action != model.ActionAcademicPeriodManage || auditor.resource != (model.Resource{Type: model.ResourceAcademicPeriod, ID: periodID}) {
		t.Fatalf("mutation audit = %q %#v", auditor.action, auditor.resource)
	}
	if auditor.scopeType != model.RoleScopeInstitution || auditor.scopeID != institutionID {
		t.Fatalf("mutation audit scope = %s/%s", auditor.scopeType, auditor.scopeID)
	}
	if authorizer.action != model.ActionAcademicPeriodManage || authorizer.period == nil || authorizer.period.ID.String() != periodID {
		t.Fatalf("authorization = %q %#v", authorizer.action, authorizer.period)
	}
	if !reflect.DeepEqual(events, []string{"authorize-period-owner", "audit-begin", "store-create"}) {
		t.Fatalf("events = %v", events)
	}
}

func TestAcademicPeriodCreateIdempotencyBindsCanonicalOwner(t *testing.T) {
	t.Parallel()
	events := []string{}
	makeService := func(persistence *academicPeriodStoreFake) *academicPeriodService {
		return newAcademicPeriodService(
			persistence, &academicPeriodAuthorizerFake{events: &events},
			&institutionAuditorFake{events: &events, beginID: model.NewId()},
			func() time.Time { return time.UnixMilli(500) }, model.NewId,
		)
	}
	invocation := NewInvocation(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{})
	institutionID := model.NewInstitutionID().String()
	institution := &academicPeriodStoreFake{events: &events}
	if _, err := makeService(institution).Create(context.Background(), invocation, CreateAcademicPeriodCommand{
		OwnerType: " institution ", OwnerID: " " + institutionID + " ",
		Name: "period", DisplayName: "Period", StartAt: 100, EndAt: 200, IdempotencyKey: "same-key",
	}); err != nil {
		t.Fatal(err)
	}
	canonical := &academicPeriodStoreFake{events: &events}
	if _, err := makeService(canonical).Create(context.Background(), invocation, CreateAcademicPeriodCommand{
		OwnerType: string(model.ResourceInstitution), OwnerID: institutionID,
		Name: "period", DisplayName: "Period", StartAt: 100, EndAt: 200, IdempotencyKey: "same-key",
	}); err != nil {
		t.Fatal(err)
	}
	unit := &academicPeriodStoreFake{events: &events}
	if _, err := makeService(unit).Create(context.Background(), invocation, CreateAcademicPeriodCommand{
		OwnerType: string(model.ResourceAcademicUnit), OwnerID: model.NewAcademicUnitID().String(),
		Name: "period", DisplayName: "Period", StartAt: 100, EndAt: 200, IdempotencyKey: "same-key",
	}); err != nil {
		t.Fatal(err)
	}
	if institution.idempotency == nil || canonical.idempotency == nil || unit.idempotency == nil ||
		institution.idempotency.Fingerprint != canonical.idempotency.Fingerprint ||
		institution.idempotency.Fingerprint == unit.idempotency.Fingerprint {
		t.Fatalf("owner fingerprints = %#v %#v %#v", institution.idempotency, canonical.idempotency, unit.idempotency)
	}
}
