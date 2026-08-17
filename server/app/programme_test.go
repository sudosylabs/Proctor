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

type programmeStoreFake struct {
	events       *[]string
	current      *model.Programme
	created      *model.Programme
	createInput  *store.ProgrammeCreation
	updateInput  *store.ProgrammeUpdate
	archiveInput *store.ProgrammeArchive
	createErr    error
}

func (s *programmeStoreFake) Get(context.Context, string) (*model.Programme, error) {
	*s.events = append(*s.events, "get")
	return s.current, nil
}
func (*programmeStoreFake) ListByAcademicUnit(context.Context, string) ([]*model.Programme, error) {
	return nil, nil
}
func (*programmeStoreFake) SearchByAcademicUnit(context.Context, string, string, int) ([]*model.Programme, error) {
	return nil, nil
}
func (s *programmeStoreFake) Create(_ context.Context, input *store.ProgrammeCreation) (*model.Programme, error) {
	*s.events = append(*s.events, "store-create")
	s.createInput = input
	return s.created, s.createErr
}
func (s *programmeStoreFake) UpdateWithAudit(_ context.Context, input *store.ProgrammeUpdate) (*model.Programme, error) {
	*s.events = append(*s.events, "store-update")
	s.updateInput = input
	return input.Programme, nil
}
func (s *programmeStoreFake) ArchiveWithAudit(_ context.Context, input *store.ProgrammeArchive) (*model.Programme, error) {
	*s.events = append(*s.events, "store-archive")
	s.archiveInput = input
	return s.current, nil
}

type programmeAuthorizerFake struct {
	events       *[]string
	action       model.Action
	resource     model.Resource
	scopeType    model.RoleScopeType
	scopeID      string
	err          error
	preflightErr error
}

func (a *programmeAuthorizerFake) Authorize(_ context.Context, _ Invocation, action model.Action, resource model.Resource) error {
	*a.events = append(*a.events, "authorize")
	a.action, a.resource = action, resource
	return a.err
}

func (a *programmeAuthorizerFake) AuthorizePreflight(_ context.Context, _ Invocation, action model.Action, resourceType model.ResourceType) error {
	*a.events = append(*a.events, "authorize-preflight")
	a.action = action
	a.resource = model.Resource{Type: resourceType}
	return a.preflightErr
}

func (a *programmeAuthorizerFake) AuthorizeWithScope(
	ctx context.Context,
	invocation Invocation,
	action model.Action,
	resource model.Resource,
) (model.RoleScopeType, string, error) {
	if err := a.Authorize(ctx, invocation, action, resource); err != nil {
		return "", "", err
	}
	if resource.Type == model.ResourceClass {
		return model.RoleScopeClass, resource.ID, nil
	}
	if a.scopeType.IsValid() && model.IsValidId(a.scopeID) {
		return a.scopeType, a.scopeID, nil
	}
	return model.RoleScopeAcademicUnit, resource.ID, nil
}

func TestProgrammeCreateAuthorizationDenialStopsBeforeAuditAndStore(t *testing.T) {
	t.Parallel()
	events := []string{}
	denied := NewError("authorization.denied")
	service := newProgrammeService(
		&programmeStoreFake{events: &events},
		&programmeAuthorizerFake{events: &events, err: denied},
		&institutionAuditorFake{events: &events, beginID: model.NewId()},
		time.Now, model.NewId,
	)
	_, err := service.Create(context.Background(), Invocation{}, CreateProgrammeCommand{
		AcademicUnitID: model.NewId(), Name: "computer-science", DisplayName: "Computer Science",
	})
	if !Is(err, "authorization.denied") {
		t.Fatalf("Create() error = %v", err)
	}
	if !reflect.DeepEqual(events, []string{"authorize"}) {
		t.Fatalf("events = %v", events)
	}
}

func TestProgrammeCreateConflictCompletesFailedAttempt(t *testing.T) {
	t.Parallel()
	events := []string{}
	auditor := &institutionAuditorFake{events: &events, beginID: model.NewId()}
	service := newProgrammeService(
		&programmeStoreFake{events: &events, createErr: store.NewErrConflict("programme", "programmes_academic_unit_id_name_key", nil)},
		&programmeAuthorizerFake{events: &events}, auditor,
		time.Now, model.NewId,
	)
	_, err := service.Create(context.Background(), Invocation{}, CreateProgrammeCommand{
		AcademicUnitID: model.NewId(), Name: "computer-science", DisplayName: "Computer Science",
	})
	if !Is(err, "programme.conflict") || auditor.failCode != "programme.conflict" {
		t.Fatalf("Create() error = %v, audit code = %q", err, auditor.failCode)
	}
	if !reflect.DeepEqual(events, []string{"authorize", "audit-begin", "store-create", "audit-fail"}) {
		t.Fatalf("events = %v", events)
	}
}
func (*programmeAuthorizerFake) Installation(context.Context) (model.Resource, error) {
	return model.Resource{}, nil
}
func (*programmeAuthorizerFake) AuthorizeInstallation(context.Context, Invocation, model.Action) (model.Resource, error) {
	return model.Resource{}, nil
}

func TestProgrammeCreatePreservesAcademicUnitOwnershipAndAtomicAudit(t *testing.T) {
	t.Parallel()
	events := []string{}
	unitID, auditID, programmeID := model.NewId(), model.NewId(), model.NewId()
	created := &model.Programme{ID: model.ProgrammeID(programmeID), AcademicUnitID: model.AcademicUnitID(unitID)}
	persistence := &programmeStoreFake{events: &events, created: created}
	authorizer := &programmeAuthorizerFake{events: &events}
	auditor := &institutionAuditorFake{events: &events, beginID: auditID}
	service := newProgrammeService(persistence, authorizer, auditor, func() time.Time { return time.UnixMilli(500) }, func() string { return programmeID })
	got, err := service.Create(context.Background(), Invocation{}, CreateProgrammeCommand{
		AcademicUnitID: unitID, Name: "computer-science", DisplayName: "Computer Science",
	})
	if err != nil || got != created {
		t.Fatalf("Create() = %#v, %v", got, err)
	}
	if authorizer.action != model.ActionProgrammeManage || authorizer.resource != (model.Resource{Type: model.ResourceAcademicUnit, ID: unitID}) {
		t.Fatalf("authorization = %q %#v", authorizer.action, authorizer.resource)
	}
	if persistence.createInput == nil ||
		persistence.createInput.Programme.AcademicUnitID.String() != unitID ||
		persistence.createInput.Programme.ID.String() != programmeID ||
		persistence.createInput.AuditEventID != auditID {
		t.Fatalf("create input = %#v", persistence.createInput)
	}
	if !reflect.DeepEqual(events, []string{"authorize", "audit-begin", "store-create"}) {
		t.Fatalf("events = %v", events)
	}
}

func TestProgrammeUpdateCannotMoveOwnership(t *testing.T) {
	t.Parallel()
	events := []string{}
	current := &model.Programme{
		ID:        model.ProgrammeID(model.NewId()),
		CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(100),
		AcademicUnitID: model.AcademicUnitID(model.NewId()),
		Name:           "computer-science", DisplayName: "Computer Science",
	}
	persistence := &programmeStoreFake{events: &events, current: current}
	service := newProgrammeService(persistence, &programmeAuthorizerFake{events: &events}, &institutionAuditorFake{events: &events, beginID: model.NewId()}, func() time.Time { return time.UnixMilli(500) }, model.NewId)
	name := "computing"
	updated, err := service.Update(context.Background(), Invocation{}, UpdateProgrammeCommand{ID: current.ID.String(), Name: &name})
	if err != nil {
		t.Fatal(err)
	}
	if updated.AcademicUnitID != current.AcademicUnitID || persistence.updateInput.Programme.AcademicUnitID != current.AcademicUnitID {
		t.Fatalf("ownership changed: %#v", updated)
	}
	if !reflect.DeepEqual(events, []string{"authorize", "get", "audit-begin", "store-update"}) {
		t.Fatalf("events = %v", events)
	}
}

func TestProgrammeArchiveUsesAtomicStoreSeam(t *testing.T) {
	t.Parallel()
	events := []string{}
	current := &model.Programme{ID: model.ProgrammeID(model.NewId()), AcademicUnitID: model.AcademicUnitID(model.NewId())}
	persistence := &programmeStoreFake{events: &events, current: current}
	auditID := model.NewId()
	service := newProgrammeService(persistence, &programmeAuthorizerFake{events: &events}, &institutionAuditorFake{events: &events, beginID: auditID}, func() time.Time { return time.UnixMilli(500) }, model.NewId)
	if err := service.Archive(context.Background(), Invocation{}, ArchiveProgrammeCommand{ID: current.ID.String()}); err != nil {
		t.Fatal(err)
	}
	if persistence.archiveInput == nil || persistence.archiveInput.ID != current.ID.String() || persistence.archiveInput.AuditEventID != auditID {
		t.Fatalf("archive input = %#v", persistence.archiveInput)
	}
}
