// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type academicUnitCreateStore struct {
	events           *[]string
	input            *store.AcademicUnitCreation
	result           *model.AcademicUnit
	err              error
	idempotentResult *store.AcademicUnitCommandResult
}

func (s *academicUnitCreateStore) CreateIdempotently(_ context.Context, input *store.AcademicUnitCreation, _ *store.CommandIdempotency) (*store.AcademicUnitCommandResult, error) {
	if s.events != nil {
		*s.events = append(*s.events, "store-idempotent")
	}
	s.input = input
	return s.idempotentResult, s.err
}

func (s *academicUnitCreateStore) Create(
	_ context.Context,
	input *store.AcademicUnitCreation,
) (*model.AcademicUnit, error) {
	if s.events != nil {
		*s.events = append(*s.events, "store")
	}
	s.input = input
	return s.result, s.err
}

func (s *academicUnitCreateStore) UpdateWithAudit(
	context.Context, *store.AcademicUnitUpdate,
) (*model.AcademicUnit, error) {
	panic("unexpected UpdateWithAudit")
}

func (s *academicUnitCreateStore) ArchiveWithAudit(
	context.Context, *store.AcademicUnitArchive,
) (*model.AcademicUnit, error) {
	panic("unexpected ArchiveWithAudit")
}

func (*academicUnitCreateStore) Save(context.Context, *model.AcademicUnit) (*model.AcademicUnit, error) {
	panic("unexpected Save")
}
func (*academicUnitCreateStore) Get(context.Context, string) (*model.AcademicUnit, error) {
	panic("unexpected Get")
}
func (*academicUnitCreateStore) ListChildren(context.Context, string, string) ([]*model.AcademicUnit, error) {
	panic("unexpected ListChildren")
}
func (*academicUnitCreateStore) ListAncestors(context.Context, string) ([]*model.AcademicUnit, error) {
	panic("unexpected ListAncestors")
}
func (*academicUnitCreateStore) Search(context.Context, string, string, int) ([]*model.AcademicUnit, error) {
	panic("unexpected Search")
}
func (*academicUnitCreateStore) Update(context.Context, *model.AcademicUnit) (*model.AcademicUnit, error) {
	panic("unexpected Update")
}
func (*academicUnitCreateStore) Archive(context.Context, string, int64) (*model.AcademicUnit, error) {
	panic("unexpected Archive")
}

type academicUnitCommandAuditor struct {
	events    *[]string
	beginID   string
	beginErr  error
	failErr   error
	failCode  string
	beginData academicUnitMutationAudit
}

type academicUnitMutationAudit struct {
	action    model.Action
	resource  model.Resource
	operation string
	value     map[string]any
	prior     map[string]any
}

func (a *academicUnitCommandAuditor) Begin(
	_ context.Context,
	_ Invocation,
	action model.Action,
	resource model.Resource,
	operation string,
	value map[string]any,
	prior map[string]any,
) (string, error) {
	*a.events = append(*a.events, "audit-begin")
	a.beginData = academicUnitMutationAudit{
		action: action, resource: resource, operation: operation,
		value: value, prior: prior,
	}
	return a.beginID, a.beginErr
}

func (a *academicUnitCommandAuditor) Fail(
	_ context.Context,
	auditID string,
	errorCode string,
) error {
	*a.events = append(*a.events, "audit-fail")
	a.failCode = errorCode
	if auditID != a.beginID {
		return errors.New("wrong audit ID")
	}
	return a.failErr
}

type academicUnitCommandEffectsFake struct {
	events *[]string
	err    error
	unitID string
}

type academicUnitEffectFailureReporterFake struct {
	events    *[]string
	operation string
	err       error
}

func (r *academicUnitEffectFailureReporterFake) Report(
	_ context.Context,
	operation string,
	err error,
) {
	*r.events = append(*r.events, "effect-report")
	r.operation = operation
	r.err = err
}

func (e *academicUnitCommandEffectsFake) Created(
	_ context.Context,
	unitID string,
) error {
	*e.events = append(*e.events, "effect")
	e.unitID = unitID
	return e.err
}

func (e *academicUnitCommandEffectsFake) Updated(
	_ context.Context,
	unitID string,
) error {
	*e.events = append(*e.events, "effect-update")
	e.unitID = unitID
	return e.err
}

func (e *academicUnitCommandEffectsFake) Archived(
	_ context.Context,
	unitID string,
) error {
	*e.events = append(*e.events, "effect-archive")
	e.unitID = unitID
	return e.err
}

func TestAcademicUnitCreateRootCommitsAuditBeforePublishing(t *testing.T) {
	t.Parallel()

	institutionID := model.NewId()
	saved := &model.AcademicUnit{
		ID: model.AcademicUnitID(model.NewId()), InstitutionID: model.InstitutionID(institutionID),
		Name: "engineering", DisplayName: "Engineering",
	}
	events := []string{}
	creator := &academicUnitCreateStore{events: &events, result: saved}
	auditor := &academicUnitCommandAuditor{events: &events, beginID: model.NewId()}
	effects := &academicUnitCommandEffectsFake{events: &events}
	createdID := model.NewId()
	service := newAcademicUnitCommandService(
		creator,
		academicUnitAuthorizerStub{
			authorize: func(context.Context, Invocation, model.Action, model.Resource) error {
				panic("unexpected Authorize")
			},
			authorizeInstallation: func(
				context.Context, Invocation, model.Action,
			) (model.Resource, error) {
				events = append(events, "authorize")
				return model.Resource{Type: model.ResourceInstitution, ID: institutionID}, nil
			},
		},
		auditor,
		effects,
		&academicUnitEffectFailureReporterFake{events: &events},
		func() time.Time { return time.UnixMilli(500) },
		func() string { return createdID },
	)

	got, err := service.Create(context.Background(), Invocation{}, CreateAcademicUnitCommand{
		Name: "engineering", DisplayName: "Engineering", Description: "Faculty",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != saved {
		t.Fatalf("Create() = %#v, want %#v", got, saved)
	}
	if creator.input == nil || creator.input.Unit.InstitutionID.String() != institutionID ||
		creator.input.Unit.ParentID.String() != "" || creator.input.Unit.ID.String() != createdID ||
		creator.input.Unit.Name != "engineering" ||
		creator.input.AuditEventID != auditor.beginID || creator.input.AuditAt != 500 {
		t.Fatalf("creation input = %#v", creator.input)
	}
	if effects.unitID != saved.ID.String() {
		t.Fatalf("published unit = %q, want %q", effects.unitID, saved.ID)
	}
	assertAcademicUnitCreateEvents(t, events, "authorize", "audit-begin", "store", "effect")
}

func TestAcademicUnitCreateReplayDoesNotPublishAgain(t *testing.T) {
	institutionID := model.NewId()
	saved := &model.AcademicUnit{ID: model.NewAcademicUnitID(), InstitutionID: model.InstitutionID(institutionID), Name: "engineering", DisplayName: "Engineering"}
	events := []string{}
	creator := &academicUnitCreateStore{events: &events, idempotentResult: &store.AcademicUnitCommandResult{Value: saved, Replayed: true}}
	service := newAcademicUnitCommandService(
		creator,
		academicUnitAuthorizerStub{authorizeInstallation: func(context.Context, Invocation, model.Action) (model.Resource, error) {
			return model.Resource{Type: model.ResourceInstitution, ID: institutionID}, nil
		}},
		&academicUnitCommandAuditor{events: &events, beginID: model.NewId()},
		&academicUnitCommandEffectsFake{events: &events},
		&academicUnitEffectFailureReporterFake{events: &events},
		func() time.Time { return time.UnixMilli(500) }, model.NewId,
	)
	invocation := NewInvocation(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{})
	got, err := service.Create(context.Background(), invocation, CreateAcademicUnitCommand{Name: "engineering", DisplayName: "Engineering", IdempotencyKey: "retry-key"})
	if err != nil {
		t.Fatal(err)
	}
	if got != saved {
		t.Fatalf("Create() = %#v, want replay %#v", got, saved)
	}
	assertAcademicUnitCreateEvents(t, events, "audit-begin", "store-idempotent")
}

func TestAcademicUnitCreateDenialStopsBeforeDurableWork(t *testing.T) {
	t.Parallel()

	events := []string{}
	creator := &academicUnitCreateStore{}
	service := newAcademicUnitCommandService(
		creator,
		academicUnitAuthorizerStub{
			authorize: func(context.Context, Invocation, model.Action, model.Resource) error {
				panic("unexpected Authorize")
			},
			authorizeInstallation: func(
				context.Context, Invocation, model.Action,
			) (model.Resource, error) {
				return model.Resource{}, NewError("authorization.denied")
			},
		},
		&academicUnitCommandAuditor{events: &events},
		&academicUnitCommandEffectsFake{events: &events},
		&academicUnitEffectFailureReporterFake{events: &events},
		time.Now,
		model.NewId,
	)
	_, err := service.Create(context.Background(), Invocation{}, CreateAcademicUnitCommand{Name: "engineering"})
	if !Is(err, "authorization.denied") {
		t.Fatalf("Create() error = %v, want authorization.denied", err)
	}
	if creator.input != nil || len(events) != 0 {
		t.Fatalf("work after denial: input=%#v events=%v", creator.input, events)
	}
}

func TestAcademicUnitCreateFailureAuditsAndDoesNotPublish(t *testing.T) {
	t.Parallel()

	events := []string{}
	institutionID := model.NewId()
	parentID := model.NewId()
	creator := &academicUnitCreateStore{
		events: &events,
		err:    store.NewErrConflict("academic_unit", "name", errors.New("duplicate")),
	}
	auditor := &academicUnitCommandAuditor{events: &events, beginID: model.NewId()}
	service := newAcademicUnitCommandService(
		creator,
		academicUnitAuthorizerStub{
			authorize: func(
				_ context.Context, _ Invocation, action model.Action, resource model.Resource,
			) error {
				events = append(events, "authorize")
				if action != model.ActionAcademicUnitManage || resource.Type != model.ResourceAcademicUnit {
					t.Fatalf("authorization = %s %#v", action, resource)
				}
				return nil
			},
			installation: func(context.Context) (model.Resource, error) {
				return model.Resource{Type: model.ResourceInstitution, ID: institutionID}, nil
			},
		},
		auditor,
		&academicUnitCommandEffectsFake{events: &events},
		&academicUnitEffectFailureReporterFake{events: &events},
		time.Now,
		model.NewId,
	)
	_, err := service.Create(context.Background(), Invocation{}, CreateAcademicUnitCommand{
		ParentID: parentID, Name: "computing", DisplayName: "Computing",
	})
	if !Is(err, "academic_unit.conflict") {
		t.Fatalf("Create() error = %v, want academic_unit.conflict", err)
	}
	if auditor.failCode != "academic_unit.conflict" {
		t.Fatalf("failure audit code = %q", auditor.failCode)
	}
	assertAcademicUnitCreateEvents(t, events, "authorize", "audit-begin", "store", "audit-fail")
}

func TestAcademicUnitCreateIgnoresPostCommitEffectFailure(t *testing.T) {
	t.Parallel()

	events := []string{}
	saved := &model.AcademicUnit{ID: model.AcademicUnitID(model.NewId()), InstitutionID: model.InstitutionID(model.NewId()), Name: "engineering"}
	creator := &academicUnitCreateStore{events: &events, result: saved}
	reporter := &academicUnitEffectFailureReporterFake{events: &events}
	effectErr := errors.New("cluster unavailable")
	service := newAcademicUnitCommandService(
		creator,
		academicUnitAuthorizerStub{
			authorizeInstallation: func(
				context.Context, Invocation, model.Action,
			) (model.Resource, error) {
				return model.Resource{Type: model.ResourceInstitution, ID: saved.InstitutionID.String()}, nil
			},
		},
		&academicUnitCommandAuditor{events: &events, beginID: model.NewId()},
		&academicUnitCommandEffectsFake{events: &events, err: effectErr},
		reporter,
		time.Now,
		model.NewId,
	)
	got, err := service.Create(context.Background(), Invocation{}, CreateAcademicUnitCommand{
		Name: "engineering", DisplayName: "Engineering",
	})
	if err != nil || got != saved {
		t.Fatalf("Create() = %#v, %v; want committed result", got, err)
	}
	if !errors.Is(reporter.err, effectErr) {
		t.Fatalf("reported effect error = %v, want %v", reporter.err, effectErr)
	}
	if reporter.operation != "academic_unit_created" {
		t.Fatalf("reported operation = %q", reporter.operation)
	}
	assertAcademicUnitCreateEvents(t, events, "audit-begin", "store", "effect", "effect-report")
}

func assertAcademicUnitCreateEvents(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("events = %v, want %v", got, want)
		}
	}
}
