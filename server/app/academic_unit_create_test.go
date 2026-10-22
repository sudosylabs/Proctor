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

func (s *academicUnitCreateStore) Update(
	context.Context, *store.AcademicUnitUpdate,
) (*model.AcademicUnit, error) {
	panic("unexpected Update")
}

func (s *academicUnitCreateStore) Archive(
	context.Context, *store.AcademicUnitArchive,
) (*model.AcademicUnit, error) {
	panic("unexpected Archive")
}

func (*academicUnitCreateStore) Get(context.Context, model.AcademicUnitID) (*model.AcademicUnit, error) {
	panic("unexpected Get")
}
func (*academicUnitCreateStore) ListChildren(context.Context, model.InstitutionID, model.AcademicUnitID, store.AcademicNameListOptions) ([]*model.AcademicUnit, error) {
	panic("unexpected ListChildren")
}
func (*academicUnitCreateStore) ListAncestors(context.Context, string) ([]*model.AcademicUnit, error) {
	panic("unexpected ListAncestors")
}
func (*academicUnitCreateStore) Search(context.Context, model.InstitutionID, store.AcademicNameListOptions) ([]*model.AcademicUnit, error) {
	panic("unexpected Search")
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

func (a *academicUnitCommandAuditor) Begin(_ context.Context, _ invocation.Invocation, action model.Action, resource model.Resource, operation string, value, prior map[string]any) (model.AuditEventID, error) {
	*a.events = append(*a.events, "audit-begin")
	a.beginData = academicUnitMutationAudit{
		action: action, resource: resource, operation: operation,
		value: value, prior: prior,
	}
	return model.AuditEventID(a.beginID), a.beginErr
}

func (a *academicUnitCommandAuditor) Fail(_ context.Context, auditID model.AuditEventID, errorCode string) error {
	*a.events = append(*a.events, "audit-fail")
	a.failCode = errorCode
	if auditID != model.AuditEventID(a.beginID) {
		return errors.New("wrong audit ID")
	}
	return a.failErr
}

type academicUnitCommandEffectsFake struct {
	events *[]string
	err    error
	unitID model.AcademicUnitID
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
	unitID model.AcademicUnitID,
) error {
	*e.events = append(*e.events, "effect")
	e.unitID = unitID
	return e.err
}

func (e *academicUnitCommandEffectsFake) Updated(
	_ context.Context,
	unitID model.AcademicUnitID,
) error {
	*e.events = append(*e.events, "effect-update")
	e.unitID = unitID
	return e.err
}

func (e *academicUnitCommandEffectsFake) Archived(
	_ context.Context,
	unitID model.AcademicUnitID,
) error {
	*e.events = append(*e.events, "effect-archive")
	e.unitID = unitID
	return e.err
}

func TestAcademicUnitCreateRootCommitsAuditBeforePublishing(t *testing.T) {
	t.Parallel()

	institutionID := model.NewID()
	saved := &model.AcademicUnit{
		ID: model.AcademicUnitID(model.NewID()), InstitutionID: model.InstitutionID(institutionID),
		Name: "engineering", DisplayName: "Engineering",
	}
	events := []string{}
	creator := &academicUnitCreateStore{events: &events, result: saved}
	auditor := &academicUnitCommandAuditor{events: &events, beginID: model.NewID()}
	effects := &academicUnitCommandEffectsFake{events: &events}
	createdID := model.NewID()
	service := newAcademicUnitCommandService(academicUnitCommandServiceDependencies{Store: creator, Authorization: academicUnitAuthorizerStub{
		authorize: func(context.Context, invocation.Invocation, model.Action, model.Resource) error {
			panic("unexpected Authorize")
		},
		authorizeInstallation: func(
			context.Context, invocation.Invocation, model.Action,
		) (model.Resource, error) {
			events = append(events, "authorize")
			return model.Resource{Type: model.ResourceInstitution, ID: institutionID}, nil
		},
	}, Audit: auditor, Effects: effects, EffectFailures: &academicUnitEffectFailureReporterFake{events: &events}, Now: func() time.Time { return time.UnixMilli(500) }, NewID: func() string { return createdID }})

	got, err := service.CreateAcademicUnit(context.Background(), invocation.Invocation{}, CreateAcademicUnitCommand{
		Name: "engineering", DisplayName: "Engineering", Description: "Faculty",
	})
	require.NoError(t, err)
	require.Same(t, saved, got, "Create()")
	require.NotNil(t, creator.input)
	require.Equal(t, institutionID, creator.input.Unit.InstitutionID.String())
	require.Empty(t, creator.input.Unit.ParentID.String())
	require.Equal(t, createdID, creator.input.Unit.ID.String())
	require.Equal(t, "engineering", creator.input.Unit.Name)
	require.Equal(t, model.AuditEventID(auditor.beginID), creator.input.AuditEventID)
	require.True(t, time.UnixMilli(500).UTC().Equal(creator.input.AuditAt))
	require.Equal(t, saved.ID, effects.unitID, "published unit = %q, want %q", effects.unitID, saved.ID)
	assertAcademicUnitCreateEvents(t, events, "authorize", "audit-begin", "store", "effect")
}

func TestAcademicUnitCreateReplayDoesNotPublishAgain(t *testing.T) {
	institutionID := model.NewID()
	saved := &model.AcademicUnit{ID: model.NewAcademicUnitID(), InstitutionID: model.InstitutionID(institutionID), Name: "engineering", DisplayName: "Engineering"}
	events := []string{}
	creator := &academicUnitCreateStore{events: &events, idempotentResult: &store.AcademicUnitCommandResult{Value: saved, Replayed: true}}
	service := newAcademicUnitCommandService(academicUnitCommandServiceDependencies{Store: creator, Authorization: academicUnitAuthorizerStub{authorizeInstallation: func(context.Context, invocation.Invocation, model.Action) (model.Resource, error) {
		return model.Resource{Type: model.ResourceInstitution, ID: institutionID}, nil
	}}, Audit: &academicUnitCommandAuditor{events: &events, beginID: model.NewID()}, Effects: &academicUnitCommandEffectsFake{events: &events}, EffectFailures: &academicUnitEffectFailureReporterFake{events: &events}, Now: func() time.Time { return time.UnixMilli(500) }, NewID: model.NewID})
	invocation := invocation.New(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{})
	got, err := service.CreateAcademicUnit(context.Background(), invocation, CreateAcademicUnitCommand{Name: "engineering", DisplayName: "Engineering", IdempotencyKey: "retry-key"})
	require.NoError(t, err)
	require.Same(t, saved, got, "Create()")
	assertAcademicUnitCreateEvents(t, events, "audit-begin", "store-idempotent")
}

func TestAcademicUnitCreateDenialStopsBeforeDurableWork(t *testing.T) {
	t.Parallel()

	events := []string{}
	creator := &academicUnitCreateStore{}
	service := newAcademicUnitCommandService(academicUnitCommandServiceDependencies{Store: creator, Authorization: academicUnitAuthorizerStub{
		authorize: func(context.Context, invocation.Invocation, model.Action, model.Resource) error {
			panic("unexpected Authorize")
		},
		authorizeInstallation: func(
			context.Context, invocation.Invocation, model.Action,
		) (model.Resource, error) {
			return model.Resource{}, apperror.New("authorization.denied")
		},
	}, Audit: &academicUnitCommandAuditor{events: &events}, Effects: &academicUnitCommandEffectsFake{events: &events}, EffectFailures: &academicUnitEffectFailureReporterFake{events: &events}, Now: time.Now, NewID: model.NewID})
	_, err := service.CreateAcademicUnit(context.Background(), invocation.Invocation{}, CreateAcademicUnitCommand{Name: "engineering"})
	require.True(t, apperror.Is(err, "authorization.denied"), "Create() error = %v, want authorization.denied", err)
	require.Nil(t, creator.input, "work after denial: input")
	require.Empty(t, events, "work after denial: input")
}

func TestAcademicUnitCreateFailureAuditsAndDoesNotPublish(t *testing.T) {
	t.Parallel()

	events := []string{}
	institutionID := model.NewID()
	parentID := model.NewID()
	creator := &academicUnitCreateStore{
		events: &events,
		err:    store.NewErrConflict("academic_unit", "name", errors.New("duplicate")),
	}
	auditor := &academicUnitCommandAuditor{events: &events, beginID: model.NewID()}
	service := newAcademicUnitCommandService(academicUnitCommandServiceDependencies{Store: creator, Authorization: academicUnitAuthorizerStub{
		authorize: func(
			_ context.Context, _ invocation.Invocation, action model.Action, resource model.Resource,
		) error {
			events = append(events, "authorize")
			require.Equal(t, model.ActionAcademicUnitManage, action, "authorization = %s %#v", action, resource)
			require.Equal(t, model.ResourceAcademicUnit, resource.Type, "authorization = %s %#v", action, resource)
			return nil
		},
		installation: func(context.Context) (model.Resource, error) {
			return model.Resource{Type: model.ResourceInstitution, ID: institutionID}, nil
		},
	}, Audit: auditor, Effects: &academicUnitCommandEffectsFake{events: &events}, EffectFailures: &academicUnitEffectFailureReporterFake{events: &events}, Now: time.Now, NewID: model.NewID})
	_, err := service.CreateAcademicUnit(context.Background(), invocation.Invocation{}, CreateAcademicUnitCommand{
		ParentID: model.AcademicUnitID(parentID), Name: "computing", DisplayName: "Computing",
	})
	require.True(t, apperror.Is(err, "academic_unit.conflict"), "Create() error = %v, want academic_unit.conflict", err)
	require.Equal(t, "academic_unit.conflict", auditor.failCode)
	assertAcademicUnitCreateEvents(t, events, "authorize", "audit-begin", "store", "audit-fail")
}

func TestAcademicUnitCreateIgnoresPostCommitEffectFailure(t *testing.T) {
	t.Parallel()

	events := []string{}
	saved := &model.AcademicUnit{ID: model.AcademicUnitID(model.NewID()), InstitutionID: model.InstitutionID(model.NewID()), Name: "engineering"}
	creator := &academicUnitCreateStore{events: &events, result: saved}
	reporter := &academicUnitEffectFailureReporterFake{events: &events}
	effectErr := errors.New("cluster unavailable")
	service := newAcademicUnitCommandService(academicUnitCommandServiceDependencies{Store: creator, Authorization: academicUnitAuthorizerStub{
		authorizeInstallation: func(
			context.Context, invocation.Invocation, model.Action,
		) (model.Resource, error) {
			return model.Resource{Type: model.ResourceInstitution, ID: saved.InstitutionID.String()}, nil
		},
	}, Audit: &academicUnitCommandAuditor{events: &events, beginID: model.NewID()}, Effects: &academicUnitCommandEffectsFake{events: &events, err: effectErr}, EffectFailures: reporter, Now: time.Now, NewID: model.NewID})
	got, err := service.CreateAcademicUnit(context.Background(), invocation.Invocation{}, CreateAcademicUnitCommand{
		Name: "engineering", DisplayName: "Engineering",
	})
	require.NoError(t, err, "Create()")
	require.Same(t, saved, got, "Create()")
	require.ErrorIs(t, reporter.err, effectErr, "reported effect error")
	require.Equal(t, "academic_unit_created", reporter.operation)
	assertAcademicUnitCreateEvents(t, events, "audit-begin", "store", "effect", "effect-report")
}

func assertAcademicUnitCreateEvents(t *testing.T, got []string, want ...string) {
	t.Helper()
	require.Len(t, got, len(want), "events")
	for index := range want {
		require.Equal(t, want[index], got[index], "events = %v, want %v", got, want)
	}
}
