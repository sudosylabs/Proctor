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
	"reflect"
	"testing"
	"time"

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

func (s *institutionStoreFake) UpdateWithAudit(
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
	_ Invocation,
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

func (a *institutionAuditorFake) Begin(
	_ context.Context,
	_ Invocation,
	action model.Action,
	resource model.Resource,
	_ string,
	_ map[string]any,
	_ map[string]any,
) (string, error) {
	*a.events = append(*a.events, "audit-begin")
	a.action, a.resource = action, resource
	a.resources = append(a.resources, resource)
	if len(a.beginIDs) > 0 {
		id := a.beginIDs[0]
		a.beginIDs = a.beginIDs[1:]
		return id, nil
	}
	return a.beginID, nil
}

func (a *institutionAuditorFake) BeginAtScope(
	ctx context.Context,
	invocation Invocation,
	action model.Action,
	resource model.Resource,
	scopeType model.RoleScopeType,
	scopeID string,
	operation string,
	value map[string]any,
	prior map[string]any,
) (string, error) {
	a.scopeType, a.scopeID = scopeType, scopeID
	return a.Begin(ctx, invocation, action, resource, operation, value, prior)
}

func (a *institutionAuditorFake) Fail(
	_ context.Context,
	_ string,
	code string,
) error {
	*a.events = append(*a.events, "audit-fail")
	a.failCode = code
	return nil
}

func TestInstitutionGetAuthorizesSingletonResource(t *testing.T) {
	t.Parallel()

	events := []string{}
	institution := &model.Institution{ID: model.InstitutionID(model.NewId())}
	authorizer := &institutionAuthorizerFake{events: &events}
	service := newInstitutionService(
		&institutionStoreFake{events: &events, current: institution}, authorizer,
		&institutionAuditorFake{events: &events}, time.Now,
	)
	got, err := service.Get(context.Background(), Invocation{})
	if err != nil || got != institution {
		t.Fatalf("Get() = %#v, %v", got, err)
	}
	if authorizer.action != model.ActionInstitutionManage ||
		authorizer.resource != (model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()}) {
		t.Fatalf("authorization = %q %#v", authorizer.action, authorizer.resource)
	}
	if !reflect.DeepEqual(events, []string{"get", "authorize"}) {
		t.Fatalf("events = %v", events)
	}
}

func TestInstitutionUpdateCommitsSuccessAuditAtomically(t *testing.T) {
	t.Parallel()

	events := []string{}
	current := &model.Institution{
		ID: model.InstitutionID(model.NewId()), CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(100),
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
	auditor := &institutionAuditorFake{events: &events, beginID: model.NewId()}
	service := newInstitutionService(
		persistence, &institutionAuthorizerFake{events: &events}, auditor,
		func() time.Time { return time.UnixMilli(500) },
	)
	displayName := "Northbridge"
	got, err := service.Update(context.Background(), Invocation{}, UpdateInstitutionCommand{
		DisplayName: &displayName, ExamCapacity: &capacity,
	})
	if err != nil || got != &updated {
		t.Fatalf("Update() = %#v, %v", got, err)
	}
	if persistence.updateInput == nil ||
		persistence.updateInput.Institution.DisplayName != displayName ||
		persistence.updateInput.Institution.ExamCapacity != capacity ||
		!persistence.updateInput.Institution.UpdatedAt.Equal(time.UnixMilli(500).UTC()) ||
		persistence.updateInput.AuditEventID != auditor.beginID {
		t.Fatalf("update input = %#v", persistence.updateInput)
	}
	if !reflect.DeepEqual(events, []string{"get", "authorize", "audit-begin", "store-update"}) {
		t.Fatalf("events = %v", events)
	}
}

func TestInstitutionUpdateFailureCompletesFailedAttempt(t *testing.T) {
	t.Parallel()

	events := []string{}
	current := &model.Institution{
		ID: model.InstitutionID(model.NewId()), CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(100),
		Name: "northbridge", DisplayName: "Northbridge", Revision: 1, ExamCapacity: model.DefaultExamCapacityPolicy(),
	}
	persistence := &institutionStoreFake{
		events: &events, current: current,
		updateErr: store.NewErrConflict("institution", "institutions_name_key", errors.New("duplicate")),
	}
	auditor := &institutionAuditorFake{events: &events, beginID: model.NewId()}
	service := newInstitutionService(
		persistence, &institutionAuthorizerFake{events: &events}, auditor, time.Now,
	)
	name := "duplicate"
	_, err := service.Update(context.Background(), Invocation{}, UpdateInstitutionCommand{Name: &name})
	if !Is(err, "institution.conflict") || auditor.failCode != "institution.conflict" {
		t.Fatalf("Update() error = %v, audit code = %q", err, auditor.failCode)
	}
	if !reflect.DeepEqual(events, []string{"get", "authorize", "audit-begin", "store-update", "audit-fail"}) {
		t.Fatalf("events = %v", events)
	}
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
	service := newInstitutionService(
		&institutionStoreFake{events: &events, current: current},
		&institutionAuthorizerFake{events: &events}, &institutionAuditorFake{events: &events}, time.Now,
	)

	_, err := service.Update(context.Background(), Invocation{}, UpdateInstitutionCommand{ExamCapacity: &invalid})
	if !Is(err, "institution.invalid") {
		t.Fatalf("Update() error = %v", err)
	}
	if !reflect.DeepEqual(events, []string{"get", "authorize"}) {
		t.Fatalf("events = %v", events)
	}
}
