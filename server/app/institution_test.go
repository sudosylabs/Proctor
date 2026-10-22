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

type institutionStoreFake struct {
	events      *[]string
	current     *model.Institution
	updated     *model.Institution
	updateInput *store.InstitutionUpdate
	updateErr   error
}

func (s *institutionStoreFake) GetSingleton(context.Context) (*model.Institution, error) {
	*s.events = append(*s.events, "get")
	return s.current, nil
}

func (s *institutionStoreFake) Update(
	_ context.Context,
	input *store.InstitutionUpdate,
) (*model.Institution, error) {
	*s.events = append(*s.events, "store-update")
	s.updateInput = input
	return s.updated, s.updateErr
}

type institutionAuthorizerFake struct {
	events   *[]string
	action   model.Action
	resource model.Resource
	err      error
}

func (a *institutionAuthorizerFake) Authorize(
	_ context.Context,
	_ invocation.Invocation,
	action model.Action,
	resource model.Resource,
) error {
	*a.events = append(*a.events, "authorize")
	a.action, a.resource = action, resource
	return a.err
}

type institutionAuditorFake struct {
	events    *[]string
	beginID   string
	beginIDs  []string
	failCode  string
	action    model.Action
	resource  model.Resource
	resources []model.Resource
	scopeType model.RoleScopeType
	scopeID   string
}

func (a *institutionAuditorFake) Begin(_ context.Context, _ invocation.Invocation, action model.Action, resource model.Resource, _ string, _, _ map[string]any) (model.AuditEventID, error) {
	*a.events = append(*a.events, "audit-begin")
	a.action, a.resource = action, resource
	a.resources = append(a.resources, resource)
	if len(a.beginIDs) > 0 {
		id := a.beginIDs[0]
		a.beginIDs = a.beginIDs[1:]
		return model.AuditEventID(id), nil
	}
	return model.AuditEventID(a.beginID), nil
}

func (a *institutionAuditorFake) BeginAtScope(ctx context.Context, invocation invocation.Invocation, action model.Action, resource model.Resource, scopeType model.RoleScopeType, scopeID, operation string, value, prior map[string]any) (model.AuditEventID, error) {
	a.scopeType, a.scopeID = scopeType, scopeID
	return a.Begin(ctx, invocation, action, resource, operation, value, prior)
}

func (a *institutionAuditorFake) Fail(_ context.Context, _ model.AuditEventID, code string) error {
	*a.events = append(*a.events, "audit-fail")
	a.failCode = code
	return nil
}

func TestInstitutionGetAuthorizesSingletonResource(t *testing.T) {
	t.Parallel()

	events := []string{}
	institution := &model.Institution{ID: model.InstitutionID(model.NewID())}
	authorizer := &institutionAuthorizerFake{events: &events}
	service := newInstitutionService(institutionServiceDependencies{Store: &institutionStoreFake{events: &events, current: institution}, Authorization: authorizer, Audit: &institutionAuditorFake{events: &events}, Now: time.Now})
	got, err := service.GetInstitution(context.Background(), invocation.Invocation{}, GetInstitutionQuery{})
	require.NoError(t, err, "Get()")
	require.Same(t, institution, got, "Get()")
	require.Equal(t, model.ActionInstitutionManage, authorizer.action, "authorization")
	require.Equal(t, model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()}, authorizer.resource, "authorization")
	require.Equal(t, []string{"get", "authorize"}, events)
}

func TestInstitutionUpdateCommitsSuccessAuditAtomically(t *testing.T) {
	t.Parallel()

	events := []string{}
	current := &model.Institution{
		ID: model.InstitutionID(model.NewID()), CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(100),
		Name: "northbridge", DisplayName: "Northbridge University", Revision: 1, ExamCapacity: model.DefaultExamCapacityPolicy(),
	}
	updated := *current
	updated.DisplayName = "Northbridge"
	capacity := model.DefaultExamCapacityPolicy()
	capacity.ResourceMaximumCount = 25
	updated.ExamCapacity = capacity
	persistence := &institutionStoreFake{
		events: &events, current: current, updated: &updated,
	}
	auditor := &institutionAuditorFake{events: &events, beginID: model.NewID()}
	service := newInstitutionService(institutionServiceDependencies{Store: persistence, Authorization: &institutionAuthorizerFake{events: &events}, Audit: auditor, Now: func() time.Time { return time.UnixMilli(500) }})
	displayName := "Northbridge"
	got, err := service.UpdateInstitution(context.Background(), invocation.Invocation{}, UpdateInstitutionCommand{
		DisplayName: &displayName, ExamCapacity: &capacity,
	})
	require.NoError(t, err, "Update()")
	require.Same(t, &updated, got, "Update()")
	require.NotNil(t, persistence.updateInput)
	require.Equal(t, displayName, persistence.updateInput.Institution.DisplayName)
	require.Equal(t, capacity, persistence.updateInput.Institution.ExamCapacity)
	require.True(t, persistence.updateInput.Institution.UpdatedAt.Equal(time.UnixMilli(500).UTC()))
	require.Equal(t, model.AuditEventID(auditor.beginID), persistence.updateInput.AuditEventID)
	// Contract (guardrail 8): authorization precedes the audit, and the audit
	// begins before the durable write.
	require.Equal(t, []string{"get", "authorize", "audit-begin", "store-update"}, events)
}

func TestInstitutionUpdateFailureCompletesFailedAttempt(t *testing.T) {
	t.Parallel()

	events := []string{}
	current := &model.Institution{
		ID: model.InstitutionID(model.NewID()), CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(100),
		Name: "northbridge", DisplayName: "Northbridge", Revision: 1, ExamCapacity: model.DefaultExamCapacityPolicy(),
	}
	persistence := &institutionStoreFake{
		events: &events, current: current,
		updateErr: store.NewErrConflict("institution", "institutions_name_key", errors.New("duplicate")),
	}
	auditor := &institutionAuditorFake{events: &events, beginID: model.NewID()}
	service := newInstitutionService(institutionServiceDependencies{Store: persistence, Authorization: &institutionAuthorizerFake{events: &events}, Audit: auditor, Now: time.Now})
	name := "duplicate"
	_, err := service.UpdateInstitution(context.Background(), invocation.Invocation{}, UpdateInstitutionCommand{Name: &name})
	require.True(t, apperror.Is(err, "institution.conflict"), "Update() error = %v, audit code", err)
	require.Equal(t, "institution.conflict", auditor.failCode, "Update() error = %v, audit code", err)
	// Contract (guardrail 8): authorization precedes the audit, the audit begins
	// before the durable write, and the audit outcome follows the write.
	require.Equal(t, []string{"get", "authorize", "audit-begin", "store-update", "audit-fail"}, events)
}

func TestInstitutionUpdateRejectsInvalidExamCapacityBeforeAudit(t *testing.T) {
	t.Parallel()

	events := []string{}
	current := &model.Institution{
		ID: model.NewInstitutionID(), CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(100),
		Name: "northbridge", DisplayName: "Northbridge", Revision: 1, ExamCapacity: model.DefaultExamCapacityPolicy(),
	}
	invalid := model.DefaultExamCapacityPolicy()
	invalid.WorkspaceMaximumTotalBytes = invalid.WorkspaceMaximumFileBytes - 1
	service := newInstitutionService(institutionServiceDependencies{Store: &institutionStoreFake{events: &events, current: current}, Authorization: &institutionAuthorizerFake{events: &events}, Audit: &institutionAuditorFake{events: &events}, Now: time.Now})

	_, err := service.UpdateInstitution(context.Background(), invocation.Invocation{}, UpdateInstitutionCommand{ExamCapacity: &invalid})
	require.True(t, apperror.Is(err, "institution.invalid"), "Update() error = %v", err)
	require.Equal(t, []string{"get", "authorize"}, events)
}
