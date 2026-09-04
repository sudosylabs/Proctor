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

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type auditListingStoreFake struct {
	events *[]string
	list   []*model.AuditEvent
	input  store.AuditListOptions
	err    error
}

func (s *auditListingStoreFake) List(_ context.Context, options store.AuditListOptions) ([]*model.AuditEvent, error) {
	*s.events = append(*s.events, "list-audits")
	s.input = options
	return s.list, s.err
}

type auditListingAuthorizerFake struct {
	events *[]string
	err    error
	scope  store.AuditVisibilityScope
}

func (a *auditListingAuthorizerFake) AuthorizeView(context.Context, Invocation) (store.AuditVisibilityScope, error) {
	*a.events = append(*a.events, "authorize-view")
	return a.scope, a.err
}

func TestAcademicAuditListingPassesScopeToPersistenceAndRedactsSecurityMetadata(t *testing.T) {
	t.Parallel()
	events := []string{}
	rootID := model.NewAcademicUnitID().String()
	event := &model.AuditEvent{
		ID: model.NewAuditEventID(), ActorID: model.NewUserID(), SessionID: model.NewSessionID(),
		CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(100),
		Action: string(model.ActionClassMembersManage), Resource: model.Resource{Type: model.ResourceClass, ID: model.NewClassID().String()},
		ScopeType: model.RoleScopeAcademicUnit, ScopeID: rootID, Status: model.AuditStatusSuccess,
		RequestID: "request", NodeID: "node", ClientType: "web", AuthMethod: "password", IPAddress: "127.0.0.1", UserAgent: "browser",
	}
	persistence := &auditListingStoreFake{events: &events, list: []*model.AuditEvent{event}}
	service := newAuditListingService(persistence, &auditListingAuthorizerFake{
		events: &events, scope: store.AuditVisibilityScope{
			AcademicUnitRootIDs: []string{rootID},
			AllowedActions:      []string{string(model.ActionClassMembersManage)},
		},
	})
	got, err := service.List(context.Background(), Invocation{}, ListAuditEventsQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(persistence.input.Visibility.AcademicUnitRootIDs, []string{rootID}) {
		t.Fatalf("persistence visibility = %#v", persistence.input.Visibility)
	}
	if len(got) != 1 || got[0].ActorID != event.ActorID || got[0].SessionID.IsValid() ||
		got[0].RequestID != "" || got[0].NodeID != "" || got[0].ClientType != "" ||
		got[0].AuthMethod != "" || got[0].IPAddress != "" || got[0].UserAgent != "" {
		t.Fatalf("academic audit projection = %#v", got)
	}
	if event.SessionID.IsZero() || event.IPAddress == "" {
		t.Fatal("academic audit projection mutated persistence result")
	}
}

func TestInstitutionAcademicAuditListingRetainsAcademicOnlyConstraint(t *testing.T) {
	t.Parallel()
	events := []string{}
	event := &model.AuditEvent{
		ID: model.NewAuditEventID(), SessionID: model.NewSessionID(),
		CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(100),
		Action:    string(model.ActionAcademicUnitManage),
		Resource:  model.Resource{Type: model.ResourceAcademicUnit, ID: model.NewAcademicUnitID().String()},
		ScopeType: model.RoleScopeAcademicUnit, ScopeID: model.NewAcademicUnitID().String(),
		Status: model.AuditStatusSuccess, IPAddress: "127.0.0.1",
	}
	persistence := &auditListingStoreFake{events: &events, list: []*model.AuditEvent{event}}
	service := newAuditListingService(persistence, &auditListingAuthorizerFake{
		events: &events,
		scope: store.AuditVisibilityScope{
			AcademicInstitutionWide: true,
			AllowedActions:          []string{string(model.ActionAcademicUnitManage)},
		},
	})
	got, err := service.List(context.Background(), Invocation{}, ListAuditEventsQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if !persistence.input.Visibility.AcademicInstitutionWide || persistence.input.Visibility.InstitutionWide ||
		!reflect.DeepEqual(persistence.input.Visibility.AllowedActions, []string{string(model.ActionAcademicUnitManage)}) {
		t.Fatalf("persistence visibility = %#v", persistence.input.Visibility)
	}
	if len(got) != 1 || got[0].SessionID.IsValid() || got[0].IPAddress != "" {
		t.Fatalf("academic institution projection = %#v", got)
	}
}

func TestAuditListingAuthorizesOnceThenReads(t *testing.T) {
	t.Parallel()
	events := []string{}
	event := &model.AuditEvent{ID: model.NewAuditEventID(), CreatedAt: model.TimeFromMillis(100)}
	persistence := &auditListingStoreFake{events: &events, list: []*model.AuditEvent{event}}
	service := newAuditListingService(persistence, &auditListingAuthorizerFake{
		events: &events, scope: store.AuditVisibilityScope{InstitutionWide: true},
	})
	got, err := service.List(context.Background(), Invocation{}, ListAuditEventsQuery{Limit: 10, ActorID: model.NewUserID().String()})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || persistence.input.Limit != 10 {
		t.Fatalf("result/input = %#v / %#v", got, persistence.input)
	}
	want := []string{"authorize-view", "list-audits"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestAuditListingFailsClosedWhenAuthorizationReturnsZeroVisibility(t *testing.T) {
	t.Parallel()
	events := []string{}
	persistence := &auditListingStoreFake{
		events: &events,
		list:   []*model.AuditEvent{{ID: model.NewAuditEventID(), CreatedAt: model.TimeFromMillis(100)}},
	}
	service := newAuditListingService(persistence, &auditListingAuthorizerFake{events: &events})
	_, err := service.List(context.Background(), Invocation{}, ListAuditEventsQuery{Limit: 10})
	if !Is(err, "authorization.denied") {
		t.Fatalf("error = %v, want authorization.denied", err)
	}
	if want := []string{"authorize-view"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestAuditListingRejectsInvalidQuery(t *testing.T) {
	t.Parallel()
	events := []string{}
	service := newAuditListingService(
		&auditListingStoreFake{events: &events},
		&auditListingAuthorizerFake{events: &events, scope: store.AuditVisibilityScope{InstitutionWide: true}},
	)
	_, err := service.List(context.Background(), Invocation{}, ListAuditEventsQuery{Limit: 500})
	if !Is(err, "audit.query.invalid") {
		t.Fatalf("error = %v", err)
	}
	want := []string{"authorize-view"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}
