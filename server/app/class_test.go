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
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/testlib/ordertest"
)

type classStoreFake struct {
	archiveInput *store.ClassArchive
	events       *[]string
	current      *model.Class
	unitID       string
	created      *model.Class
	createInput  *store.ClassCreation
	updateInput  *store.ClassUpdate
	createErr    error
	getUnitErr   error
}

func (s *classStoreFake) Get(context.Context, model.ClassID) (*model.Class, error) {
	*s.events = append(*s.events, "get-class")
	return s.current, nil
}
func (*classStoreFake) ListByProgrammeLevel(context.Context, model.ProgrammeLevelID) ([]*model.Class, error) {
	return nil, nil
}
func (*classStoreFake) SearchByAcademicUnit(context.Context, model.AcademicUnitID, string, int) ([]*model.Class, error) {
	return nil, nil
}
func (s *classStoreFake) GetAcademicUnitID(context.Context, model.ClassID) (model.AcademicUnitID, error) {
	*s.events = append(*s.events, "get-unit")
	return model.AcademicUnitID(s.unitID), s.getUnitErr
}
func (s *classStoreFake) Create(_ context.Context, input *store.ClassCreation) (*model.Class, error) {
	*s.events = append(*s.events, "store-create")
	s.createInput = input
	return s.created, s.createErr
}
func (s *classStoreFake) Update(_ context.Context, input *store.ClassUpdate) (*model.Class, error) {
	*s.events = append(*s.events, "store-update")
	s.updateInput = input
	return input.Class, nil
}
func (s *classStoreFake) Archive(_ context.Context, input *store.ClassArchive) (*model.Class, error) {
	*s.events = append(*s.events, "store-archive")
	s.archiveInput = input
	return s.current, nil
}

func TestClassGetAuthorizesExactScopeBeforeReading(t *testing.T) {
	t.Parallel()
	events := []string{}
	service := newClassService(&classStoreFake{events: &events}, &programmeAuthorizerFake{events: &events, err: apperror.New("authorization.denied")}, &institutionAuditorFake{events: &events}, time.Now, model.NewID)
	_, err := service.GetClass(context.Background(), Invocation{}, GetClassQuery{ID: model.NewClassID()})
	require.True(t, apperror.Is(err, "authorization.denied"), "Get() error = %v", err)
	require.Equal(t, []string{"authorize"}, events)
}

func TestClassUpdateMoveAuthorizesCurrentAndDestinationOwners(t *testing.T) {
	t.Parallel()
	events := []string{}
	oldUnitID, newLevelID := model.NewID(), model.NewID()
	current := &model.Class{
		ID:               model.ClassID(model.NewID()),
		CreatedAt:        model.TimeFromMillis(100),
		UpdatedAt:        model.TimeFromMillis(100),
		Revision:         7,
		ProgrammeLevelID: model.ProgrammeLevelID(model.NewID()),
		AcademicPeriodID: model.AcademicPeriodID(model.NewID()),
		Name:             "class-a",
		DisplayName:      "Class A",
	}
	persistence := &classStoreFake{events: &events, current: current, unitID: oldUnitID}
	service := newClassService(persistence, &programmeAuthorizerFake{events: &events}, &institutionAuditorFake{events: &events, beginID: model.NewID()}, func() time.Time { return time.UnixMilli(500) }, model.NewID)
	updated, err := service.UpdateClass(context.Background(), Invocation{}, UpdateClassCommand{ID: current.ID, ProgrammeLevelID: &newLevelID})
	require.NoError(t, err)
	require.Equal(t, newLevelID, updated.ProgrammeLevelID.String())
	require.Equal(t, current.AcademicPeriodID, persistence.updateInput.Class.AcademicPeriodID)
	require.Equal(t, model.AcademicUnitID(oldUnitID), persistence.updateInput.ExpectedAcademicUnitID)
	require.Equal(t, current.Revision, persistence.updateInput.ExpectedRevision)
	// Contract (guardrail 8): authorization precedes the audit, and the audit
	// begins before the durable write. Lookups may move freely.
	ordertest.RequireInOrder(t, events, "authorize", "authorize", "audit-begin", "store-update")
}

func TestClassMutationMissingClassUsesClassResource(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		run  func(*classService) error
	}{
		{name: "update", run: func(service *classService) error {
			_, err := service.UpdateClass(context.Background(), Invocation{}, UpdateClassCommand{ID: model.NewClassID()})
			return err
		}},
		{name: "archive", run: func(service *classService) error {
			return service.ArchiveClass(context.Background(), Invocation{}, ArchiveClassCommand{ID: model.NewClassID()})
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			events := []string{}
			service := newClassService(&classStoreFake{events: &events, getUnitErr: store.NewErrNotFound("class", model.NewID())}, &programmeAuthorizerFake{events: &events}, &institutionAuditorFake{events: &events}, time.Now, model.NewID)
			err := test.run(service)
			appErr, ok := apperror.As(err)
			require.True(t, ok)
			require.Equal(t, "resource.not_found", appErr.Code())
			require.Equal(t, "class", appErr.Fields()["resource"])
		})
	}
}

func TestClassCreateConflictCompletesFailedAttempt(t *testing.T) {
	t.Parallel()
	events := []string{}
	levelID := model.NewID()
	auditor := &institutionAuditorFake{events: &events, beginID: model.NewID()}
	service := newClassService(&classStoreFake{events: &events, createErr: store.NewErrConflict("class", "classes_programme_level_id_academic_period_id_name_key", nil)}, &programmeAuthorizerFake{events: &events}, auditor, time.Now, model.NewID)
	_, err := service.CreateClass(context.Background(), Invocation{}, CreateClassCommand{ProgrammeLevelID: model.ProgrammeLevelID(levelID), AcademicPeriodID: model.NewID(), Name: "class-a", DisplayName: "Class A"})
	require.True(t, apperror.Is(err, "class.conflict"), "Create() error = %v, audit", err)
	require.Equal(t, "class.conflict", auditor.failCode, "Create() error = %v, audit", err)
}

func TestClassCreatePreservesBothParentsAndAtomicAudit(t *testing.T) {
	t.Parallel()
	events := []string{}
	levelID, periodID, classID, auditID := model.NewID(), model.NewID(), model.NewID(), model.NewID()
	created := &model.Class{ID: model.ClassID(classID), ProgrammeLevelID: model.ProgrammeLevelID(levelID), AcademicPeriodID: model.AcademicPeriodID(periodID)}
	persistence := &classStoreFake{events: &events, created: created}
	service := newClassService(persistence, &programmeAuthorizerFake{events: &events}, &institutionAuditorFake{events: &events, beginID: auditID}, func() time.Time { return time.UnixMilli(500) }, func() string { return classID })
	got, err := service.CreateClass(context.Background(), Invocation{}, CreateClassCommand{ProgrammeLevelID: model.ProgrammeLevelID(levelID), AcademicPeriodID: periodID, Name: "class-a", DisplayName: "Class A"})
	require.NoError(t, err, "Create()")
	require.Same(t, created, got, "Create()")
	require.NotNil(t, persistence.createInput)
	require.Equal(t, levelID, persistence.createInput.Class.ProgrammeLevelID.String())
	require.Equal(t, periodID, persistence.createInput.Class.AcademicPeriodID.String())
	require.Equal(t, model.AuditEventID(auditID), persistence.createInput.AuditEventID)
	// Contract (guardrail 8): authorization precedes the audit, and the audit
	// begins before the durable write. Lookups may move freely.
	ordertest.RequireInOrder(t, events, "authorize", "audit-begin", "store-create")
}
