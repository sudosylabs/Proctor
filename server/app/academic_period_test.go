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
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type academicPeriodStoreFake struct {
	archiveInput *store.AcademicPeriodArchive
	events       *[]string
	current      *model.AcademicPeriod
	created      *model.AcademicPeriod
	createInput  *store.AcademicPeriodCreation
	updateInput  *store.AcademicPeriodUpdate
	idempotency  *store.CommandIdempotency
	createErr    error
}

func (s *academicPeriodStoreFake) CreateIdempotently(_ context.Context, input *store.AcademicPeriodCreation, command *store.CommandIdempotency) (*store.AcademicPeriodCommandResult, error) {
	s.createInput, s.idempotency = input, command
	return &store.AcademicPeriodCommandResult{Value: input.Period}, nil
}

func (s *academicPeriodStoreFake) Get(context.Context, model.AcademicPeriodID) (*model.AcademicPeriod, error) {
	*s.events = append(*s.events, "get-period")
	return s.current, nil
}
func (*academicPeriodStoreFake) ListVisible(context.Context, store.AcademicPeriodVisibilityScope, store.AcademicPeriodListOptions) ([]*model.AcademicPeriod, error) {
	return nil, nil
}
func (s *academicPeriodStoreFake) Create(_ context.Context, input *store.AcademicPeriodCreation) (*model.AcademicPeriod, error) {
	*s.events = append(*s.events, "store-create")
	s.createInput = input
	return s.created, s.createErr
}
func (s *academicPeriodStoreFake) Update(_ context.Context, input *store.AcademicPeriodUpdate) (*model.AcademicPeriod, error) {
	*s.events = append(*s.events, "store-update")
	s.updateInput = input
	return input.Period, nil
}
func (s *academicPeriodStoreFake) Archive(_ context.Context, input *store.AcademicPeriodArchive) (*model.AcademicPeriod, error) {
	*s.events = append(*s.events, "store-archive")
	s.archiveInput = input
	return s.current, nil
}

type academicPeriodAuthorizerFake struct {
	events   *[]string
	resource model.Resource
	period   *model.AcademicPeriod
	action   model.Action
	err      error
}

func (a *academicPeriodAuthorizerFake) Authorize(_ context.Context, _ invocation.Invocation, action model.Action, resource model.Resource) error {
	*a.events = append(*a.events, "authorize-period")
	a.resource = resource
	a.action = action
	return a.err
}
func (a *academicPeriodAuthorizerFake) AuthorizeAcademicPeriodOwner(_ context.Context, _ invocation.Invocation, action model.Action, period *model.AcademicPeriod) error {
	*a.events = append(*a.events, "authorize-period-owner")
	a.resource = period.Owner.Resource()
	a.action, a.period = action, period
	return a.err
}
func (a *academicPeriodAuthorizerFake) AuthorizeAcademicPeriodList(context.Context, invocation.Invocation) (store.AcademicPeriodVisibilityScope, error) {
	*a.events = append(*a.events, "authorize-period-list")
	return store.AcademicPeriodVisibilityScope{InstitutionID: model.InstitutionID(a.resource.ID), InstitutionWide: true}, a.err
}

func TestAcademicPeriodCreateRejectsInvalidHalfOpenIntervalBeforeAudit(t *testing.T) {
	t.Parallel()
	events := []string{}
	service := newAcademicPeriodService(academicPeriodServiceDependencies{Store: &academicPeriodStoreFake{events: &events}, Authorization: &academicPeriodAuthorizerFake{events: &events}, Audit: &institutionAuditorFake{events: &events, beginID: model.NewID()}, Now: time.Now, NewID: model.NewID})
	_, err := service.CreateAcademicPeriod(context.Background(), invocation.Invocation{}, CreateAcademicPeriodCommand{OwnerType: string(model.ResourceInstitution), OwnerID: model.NewID(), Name: "invalid", DisplayName: "Invalid", StartAt: 200, EndAt: 200})
	require.True(t, apperror.Is(err, "academic_period.invalid"), "Create() error = %v", err)
	require.Empty(t, events)
}

func TestAcademicPeriodUpdatePreservesInstitutionOwnership(t *testing.T) {
	t.Parallel()
	events := []string{}
	period := &model.AcademicPeriod{
		ID:          model.AcademicPeriodID(model.NewID()),
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
	service := newAcademicPeriodService(academicPeriodServiceDependencies{Store: persistence, Authorization: &academicPeriodAuthorizerFake{events: &events}, Audit: &institutionAuditorFake{events: &events, beginID: model.NewID()}, Now: func() time.Time { return time.UnixMilli(500) }, NewID: model.NewID})
	name := "2026-revised"
	updated, err := service.UpdateAcademicPeriod(context.Background(), invocation.Invocation{}, UpdateAcademicPeriodCommand{ID: period.ID, Name: &name})
	require.NoError(t, err)
	require.Equal(t, period.Owner, updated.Owner)
	require.Equal(t, period.Owner, persistence.updateInput.Period.Owner)
}

func TestAcademicPeriodCreateAuthorizationDenialStopsBeforeAuditAndStore(t *testing.T) {
	t.Parallel()
	events := []string{}
	service := newAcademicPeriodService(academicPeriodServiceDependencies{Store: &academicPeriodStoreFake{events: &events}, Authorization: &academicPeriodAuthorizerFake{events: &events, err: apperror.New("authorization.denied")}, Audit: &institutionAuditorFake{events: &events}, Now: time.Now, NewID: model.NewID})
	_, err := service.CreateAcademicPeriod(context.Background(), invocation.Invocation{}, CreateAcademicPeriodCommand{OwnerType: string(model.ResourceInstitution), OwnerID: model.NewID(), Name: "2026", DisplayName: "2026", StartAt: 100, EndAt: 200})
	require.True(t, apperror.Is(err, "authorization.denied"), "Create() error = %v", err)
	require.Equal(t, []string{"authorize-period-owner"}, events)
}

func TestAcademicPeriodLookupOperationsAuthorizeBeforeReading(t *testing.T) {
	t.Parallel()
	operations := map[string]func(*academicPeriodService) error{
		"get": func(service *academicPeriodService) error {
			_, err := service.GetAcademicPeriod(context.Background(), invocation.Invocation{}, GetAcademicPeriodQuery{ID: model.NewAcademicPeriodID()})
			return err
		},
		"update": func(service *academicPeriodService) error {
			_, err := service.UpdateAcademicPeriod(context.Background(), invocation.Invocation{}, UpdateAcademicPeriodCommand{ID: model.NewAcademicPeriodID()})
			return err
		},
		"archive": func(service *academicPeriodService) error {
			return service.ArchiveAcademicPeriod(context.Background(), invocation.Invocation{}, ArchiveAcademicPeriodCommand{ID: model.NewAcademicPeriodID()})
		},
	}
	for name, operation := range operations {
		name, operation := name, operation
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			events := []string{}
			service := newAcademicPeriodService(academicPeriodServiceDependencies{Store: &academicPeriodStoreFake{events: &events}, Authorization: &academicPeriodAuthorizerFake{events: &events, err: apperror.New("authorization.denied")}, Audit: &institutionAuditorFake{events: &events}, Now: time.Now, NewID: model.NewID})
			err := operation(service)
			require.True(t, apperror.Is(err, "authorization.denied"), "operation error = %v", err)
			require.Equal(t, []string{"authorize-period"}, events)
		})
	}
}

func TestAcademicPeriodCreateConflictCompletesFailedAttempt(t *testing.T) {
	t.Parallel()
	events := []string{}
	auditor := &institutionAuditorFake{events: &events, beginID: model.NewID()}
	service := newAcademicPeriodService(academicPeriodServiceDependencies{Store: &academicPeriodStoreFake{events: &events, createErr: store.NewErrConflict("academic_period", "academic_periods_institution_id_name_key", nil)}, Authorization: &academicPeriodAuthorizerFake{events: &events}, Audit: auditor, Now: time.Now, NewID: model.NewID})
	_, err := service.CreateAcademicPeriod(context.Background(), invocation.Invocation{}, CreateAcademicPeriodCommand{OwnerType: string(model.ResourceInstitution), OwnerID: model.NewID(), Name: "2026", DisplayName: "2026", StartAt: 100, EndAt: 200})
	require.True(t, apperror.Is(err, "academic_period.conflict"), "Create() error = %v, audit code", err)
	require.Equal(t, "academic_period.conflict", auditor.failCode, "Create() error = %v, audit code", err)
}

func TestAcademicPeriodCreateOwnsTemporalValidationAndAtomicAudit(t *testing.T) {
	t.Parallel()
	events := []string{}
	institutionID, periodID, auditID := model.NewID(), model.NewID(), model.NewID()
	created := &model.AcademicPeriod{ID: model.AcademicPeriodID(periodID), Owner: model.NewInstitutionAcademicPeriodOwner(model.InstitutionID(institutionID))}
	persistence := &academicPeriodStoreFake{events: &events, created: created}
	auditor := &institutionAuditorFake{events: &events, beginID: auditID}
	authorizer := &academicPeriodAuthorizerFake{events: &events, resource: model.Resource{Type: model.ResourceInstitution, ID: institutionID}}
	service := newAcademicPeriodService(academicPeriodServiceDependencies{Store: persistence, Authorization: authorizer, Audit: auditor, Now: func() time.Time { return time.UnixMilli(500) }, NewID: func() string { return periodID }})

	got, err := service.CreateAcademicPeriod(context.Background(), invocation.Invocation{}, CreateAcademicPeriodCommand{
		OwnerType: string(model.ResourceInstitution), OwnerID: institutionID,
		Name: "2026-2027", DisplayName: "2026-2027", StartAt: 100, EndAt: 200,
	})
	require.NoError(t, err, "Create()")
	require.Same(t, created, got, "Create()")
	require.NotNil(t, persistence.createInput)
	require.Equal(t, institutionID, persistence.createInput.Period.Owner.InstitutionID.String())
	require.Equal(t, periodID, persistence.createInput.Period.ID.String())
	require.Equal(t, model.AuditEventID(auditID), persistence.createInput.AuditEventID)
	require.Equal(t, model.ActionAcademicPeriodManage, auditor.action, "mutation audit")
	require.Equal(t, model.Resource{Type: model.ResourceAcademicPeriod, ID: periodID}, auditor.resource, "mutation audit")
	require.Equal(t, model.RoleScopeInstitution, auditor.scopeType, "mutation audit scope")
	require.Equal(t, institutionID, auditor.scopeID, "mutation audit scope")
	require.Equal(t, model.ActionAcademicPeriodManage, authorizer.action, "authorization")
	require.NotNil(t, authorizer.period, "authorization")
	require.Equal(t, periodID, authorizer.period.ID.String(), "authorization")
	// Contract (guardrail 8): authorization precedes the audit, and the audit
	// begins before the durable write.
	require.Equal(t, []string{"authorize-period-owner", "audit-begin", "store-create"}, events)
}

func TestAcademicPeriodCreateIdempotencyBindsCanonicalOwner(t *testing.T) {
	t.Parallel()
	events := []string{}
	makeService := func(persistence *academicPeriodStoreFake) *academicPeriodService {
		return newAcademicPeriodService(academicPeriodServiceDependencies{Store: persistence, Authorization: &academicPeriodAuthorizerFake{events: &events}, Audit: &institutionAuditorFake{events: &events, beginID: model.NewID()}, Now: func() time.Time { return time.UnixMilli(500) }, NewID: model.NewID})
	}
	invocation := invocation.New(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{})
	institutionID := model.NewInstitutionID().String()
	institution := &academicPeriodStoreFake{events: &events}
	_, err := makeService(institution).CreateAcademicPeriod(context.Background(), invocation, CreateAcademicPeriodCommand{
		OwnerType: " institution ", OwnerID: " " + institutionID + " ",
		Name: "period", DisplayName: "Period", StartAt: 100, EndAt: 200, IdempotencyKey: "same-key",
	})
	require.NoError(t, err)
	canonical := &academicPeriodStoreFake{events: &events}
	_, err = makeService(canonical).CreateAcademicPeriod(context.Background(), invocation, CreateAcademicPeriodCommand{
		OwnerType: string(model.ResourceInstitution), OwnerID: institutionID,
		Name: "period", DisplayName: "Period", StartAt: 100, EndAt: 200, IdempotencyKey: "same-key",
	})
	require.NoError(t, err)
	unit := &academicPeriodStoreFake{events: &events}
	_, err = makeService(unit).CreateAcademicPeriod(context.Background(), invocation, CreateAcademicPeriodCommand{
		OwnerType: string(model.ResourceAcademicUnit), OwnerID: model.NewAcademicUnitID().String(),
		Name: "period", DisplayName: "Period", StartAt: 100, EndAt: 200, IdempotencyKey: "same-key",
	})
	require.NoError(t, err)
	require.NotNil(t, institution.idempotency, "owner fingerprints")
	require.NotNil(t, canonical.idempotency, "owner fingerprints")
	require.NotNil(t, unit.idempotency, "owner fingerprints")
	require.Equal(t, canonical.idempotency.Fingerprint, institution.idempotency.Fingerprint, "owner fingerprints")
	require.NotEqual(t, unit.idempotency.Fingerprint, institution.idempotency.Fingerprint, "owner fingerprints")
}
