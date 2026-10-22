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

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
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

func (a *auditListingAuthorizerFake) AuthorizeList(context.Context, invocation.Invocation) (store.AuditVisibilityScope, error) {
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
			AcademicUnitRootIDs: []model.AcademicUnitID{model.AcademicUnitID(rootID)},
			AllowedActions:      []string{string(model.ActionClassMembersManage)},
		},
	})
	got, err := service.ListAuditEvents(context.Background(), invocation.Invocation{}, ListAuditEventsQuery{Limit: 10})
	require.NoError(t, err)
	require.Equal(t, []model.AcademicUnitID{model.AcademicUnitID(rootID)}, persistence.input.Visibility.AcademicUnitRootIDs)
	require.Len(t, got, 1)
	require.Equal(t, event.ActorID, got[0].ActorID)
	require.False(t, got[0].SessionID.IsValid())
	require.Empty(t, got[0].RequestID)
	require.Empty(t, got[0].NodeID)
	require.Empty(t, got[0].ClientType)
	require.Empty(t, got[0].AuthMethod)
	require.Empty(t, got[0].IPAddress)
	require.Empty(t, got[0].UserAgent)
	require.False(t, event.SessionID.IsZero(), "academic audit projection mutated persistence result")
	require.NotEmpty(t, event.IPAddress, "academic audit projection mutated persistence result")
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
	got, err := service.ListAuditEvents(context.Background(), invocation.Invocation{}, ListAuditEventsQuery{Limit: 10})
	require.NoError(t, err)
	require.True(t, persistence.input.Visibility.AcademicInstitutionWide)
	require.False(t, persistence.input.Visibility.InstitutionWide)
	require.Equal(t, []string{string(model.ActionAcademicUnitManage)}, persistence.input.Visibility.AllowedActions)
	require.Len(t, got, 1)
	require.False(t, got[0].SessionID.IsValid())
	require.Empty(t, got[0].IPAddress)
}

func TestAuditListingAuthorizesOnceThenReads(t *testing.T) {
	t.Parallel()
	events := []string{}
	event := &model.AuditEvent{ID: model.NewAuditEventID(), CreatedAt: model.TimeFromMillis(100)}
	persistence := &auditListingStoreFake{events: &events, list: []*model.AuditEvent{event}}
	service := newAuditListingService(persistence, &auditListingAuthorizerFake{
		events: &events, scope: store.AuditVisibilityScope{InstitutionWide: true},
	})
	got, err := service.ListAuditEvents(context.Background(), invocation.Invocation{}, ListAuditEventsQuery{Limit: 10, ActorID: model.NewUserID().String()})
	require.NoError(t, err)
	require.Len(t, got, 1, "result/input = %#v / %#v", got, persistence.input)
	require.Equal(t, 10, persistence.input.Limit, "result/input = %#v / %#v", got, persistence.input)
	// Contract: authorization precedes the read.
	require.Equal(t, []string{"authorize-view", "list-audits"}, events)
}

func TestAuditListingFailsClosedWhenAuthorizationReturnsZeroVisibility(t *testing.T) {
	t.Parallel()
	events := []string{}
	persistence := &auditListingStoreFake{
		events: &events,
		list:   []*model.AuditEvent{{ID: model.NewAuditEventID(), CreatedAt: model.TimeFromMillis(100)}},
	}
	service := newAuditListingService(persistence, &auditListingAuthorizerFake{events: &events})
	_, err := service.ListAuditEvents(context.Background(), invocation.Invocation{}, ListAuditEventsQuery{Limit: 10})
	require.True(t, apperror.Is(err, "authorization.denied"), "error = %v, want authorization.denied", err)
	require.Equal(t, []string{"authorize-view"}, events, "events")
}

func TestAuditListingRejectsInvalidQuery(t *testing.T) {
	t.Parallel()
	events := []string{}
	service := newAuditListingService(
		&auditListingStoreFake{events: &events},
		&auditListingAuthorizerFake{events: &events, scope: store.AuditVisibilityScope{InstitutionWide: true}},
	)
	_, err := service.ListAuditEvents(context.Background(), invocation.Invocation{}, ListAuditEventsQuery{Limit: 500})
	require.True(t, apperror.Is(err, "audit.query.invalid"), "error = %v", err)
	want := []string{"authorize-view"}
	require.Equal(t, want, events, "events")
}
