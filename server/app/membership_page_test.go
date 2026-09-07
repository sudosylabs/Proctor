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

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestMembershipPageFilters(t *testing.T) {
	t.Parallel()
	now := model.TimeFromMillis(1000)
	at, limit, err := resolveMembershipPage(MembershipPageQuery{}, now)
	if err != nil || at != 1000 || limit != 50 {
		t.Fatalf("first page policy = %d, %d, %v", at, limit, err)
	}
	history, instant := true, int64(2000)
	for _, query := range []MembershipPageQuery{{Limit: 201}, {Limit: -1}, {History: &history, ActiveAt: &instant}} {
		if _, _, err := resolveMembershipPage(query, now); !Is(err, "request.invalid") {
			t.Fatalf("invalid query = %#v, %v", query, err)
		}
	}
	at, _, err = resolveMembershipPage(MembershipPageQuery{History: &history}, now)
	if err != nil || at != 0 {
		t.Fatalf("history policy = %d, %v", at, err)
	}
	at, _, err = resolveMembershipPage(MembershipPageQuery{ActiveAt: &instant}, now)
	if err != nil || at != instant {
		t.Fatalf("effective policy = %d, %v", at, err)
	}
}

func TestAcademicUnitMemberPagingAuthorizesEveryPage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	events := []string{}
	scopeID := model.NewAcademicUnitID()
	member := &model.AcademicUnitMember{ID: model.NewAcademicUnitMemberID(), UserID: model.NewUserID(), AcademicUnitID: scopeID}
	persistence := &academicUnitMemberStoreFake{events: &events, pageResult: &store.AcademicUnitMemberPage{Members: []*model.AcademicUnitMember{member}, HasMore: true}}
	authorization := &programmeAuthorizerFake{events: &events}
	now := model.TimeFromMillis(1000)
	service := &academicUnitMemberService{store: persistence, authorization: authorization, now: func() time.Time { return now }}
	first, err := service.ListPage(ctx, Invocation{}, ListAcademicUnitMembersPageQuery{AcademicUnitID: scopeID.String(), MembershipPageQuery: MembershipPageQuery{Limit: 1}})
	if err != nil || !first.HasMore {
		t.Fatalf("first page = %#v, %v", first, err)
	}
	if len(events) != 2 || events[0] != "authorize" || events[1] != "list-page" {
		t.Fatalf("page read before authorization: %v", events)
	}
	now = now.Add(time.Hour)
	persistence.pageResult = &store.AcademicUnitMemberPage{}
	next, err := service.ListPage(ctx, Invocation{}, ListAcademicUnitMembersPageQuery{AcademicUnitID: scopeID.String(), AfterUserID: member.UserID, AfterID: member.ID, MembershipPageQuery: MembershipPageQuery{ActiveAt: &first.ActiveAt}})
	if err != nil || next.HasMore || next.Members == nil {
		t.Fatalf("last page = %#v, %v", next, err)
	}
	if persistence.pageOptions.ActiveAt != 1000 || persistence.pageOptions.AfterID != member.ID || persistence.pageOptions.AfterUserID != member.UserID {
		t.Fatalf("cursor was not honored: %#v", persistence.pageOptions)
	}
	authorization.err = NewError("authorization.denied")
	events = nil
	_, err = service.ListPage(ctx, Invocation{}, ListAcademicUnitMembersPageQuery{AcademicUnitID: scopeID.String(), AfterUserID: member.UserID, AfterID: member.ID, MembershipPageQuery: MembershipPageQuery{ActiveAt: &first.ActiveAt}})
	if !Is(err, "authorization.denied") || len(events) != 1 || events[0] != "authorize" {
		t.Fatalf("revoked membership read: %v, events=%v", err, events)
	}
}

func TestClassMemberPagingAuthorizesEveryPage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	events := []string{}
	scopeID := model.NewClassID()
	member := &model.ClassMember{ID: model.NewClassMemberID(), UserID: model.NewUserID(), ClassID: scopeID}
	persistence := &classMemberStoreFake{events: &events, pageResult: &store.ClassMemberPage{Members: []*model.ClassMember{member}, HasMore: true}}
	authorization := &programmeAuthorizerFake{events: &events}
	now := model.TimeFromMillis(1000)
	service := &classMemberService{store: persistence, authorization: authorization, now: func() time.Time { return now }}
	first, err := service.ListPage(ctx, Invocation{}, ListClassMembersPageQuery{ClassID: scopeID.String(), MembershipPageQuery: MembershipPageQuery{Limit: 1}})
	if err != nil || !first.HasMore {
		t.Fatalf("first page = %#v, %v", first, err)
	}
	if len(events) != 2 || events[0] != "authorize" || events[1] != "list-page" {
		t.Fatalf("page read before authorization: %v", events)
	}
	now = now.Add(time.Hour)
	persistence.pageResult = &store.ClassMemberPage{}
	next, err := service.ListPage(ctx, Invocation{}, ListClassMembersPageQuery{ClassID: scopeID.String(), AfterUserID: member.UserID, AfterID: member.ID, MembershipPageQuery: MembershipPageQuery{ActiveAt: &first.ActiveAt}})
	if err != nil || next.HasMore || next.Members == nil {
		t.Fatalf("last page = %#v, %v", next, err)
	}
	if persistence.pageOptions.ActiveAt != 1000 || persistence.pageOptions.AfterID != member.ID || persistence.pageOptions.AfterUserID != member.UserID {
		t.Fatalf("cursor was not honored: %#v", persistence.pageOptions)
	}
	authorization.err = NewError("authorization.denied")
	events = nil
	_, err = service.ListPage(ctx, Invocation{}, ListClassMembersPageQuery{ClassID: scopeID.String(), AfterUserID: member.UserID, AfterID: member.ID, MembershipPageQuery: MembershipPageQuery{ActiveAt: &first.ActiveAt}})
	if !Is(err, "authorization.denied") || len(events) != 1 || events[0] != "authorize" {
		t.Fatalf("revoked membership read: %v, events=%v", err, events)
	}
}
