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

func TestMembershipPageFilters(t *testing.T) {
	t.Parallel()
	now := model.TimeFromMillis(1000)
	at, limit, err := resolveMembershipPage(MembershipPageQuery{}, now)
	require.NoError(t, err, "first page policy")
	require.Equal(t, int64(1000), at, "first page policy")
	require.Equal(t, 50, limit, "first page policy")
	history, instant := true, int64(2000)
	for _, query := range []MembershipPageQuery{{Limit: 201}, {Limit: -1}, {History: &history, ActiveAt: &instant}} {
		_, _, pageErr := resolveMembershipPage(query, now)
		require.True(t, apperror.Is(pageErr, "request.invalid"), "invalid query = %#v, %v", query, err)
	}
	at, _, err = resolveMembershipPage(MembershipPageQuery{History: &history}, now)
	require.NoError(t, err, "history policy")
	require.Equal(t, int64(0), at, "history policy")
	at, _, err = resolveMembershipPage(MembershipPageQuery{ActiveAt: &instant}, now)
	require.NoError(t, err, "effective policy")
	require.Equal(t, instant, at, "effective policy")
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
	service := &academicUnitMemberService{deps: academicUnitMemberServiceDependencies{Store: persistence, Authorization: authorization, Now: func() time.Time { return now }}}
	first, err := service.ListAcademicUnitMembersPage(ctx, invocation.Invocation{}, ListAcademicUnitMembersPageQuery{AcademicUnitID: scopeID, MembershipPageQuery: MembershipPageQuery{Limit: 1}})
	require.NoError(t, err, "first page = %#v", first)
	require.True(t, first.HasMore, "first page = %#v", first)
	require.Len(t, events, 2)
	require.Equal(t, "authorize", events[0])
	require.Equal(t, "list-page", events[1])
	now = now.Add(time.Hour)
	persistence.pageResult = &store.AcademicUnitMemberPage{}
	next, err := service.ListAcademicUnitMembersPage(ctx, invocation.Invocation{}, ListAcademicUnitMembersPageQuery{AcademicUnitID: scopeID, AfterUserID: member.UserID, AfterID: member.ID, MembershipPageQuery: MembershipPageQuery{ActiveAt: &first.ActiveAt}})
	require.NoError(t, err, "last page = %#v", next)
	require.False(t, next.HasMore, "last page = %#v", next)
	require.NotNil(t, next.Members, "last page = %#v", next)
	require.Equal(t, int64(1000), persistence.pageOptions.ActiveAt)
	require.Equal(t, member.ID, persistence.pageOptions.AfterID)
	require.Equal(t, member.UserID, persistence.pageOptions.AfterUserID)
	authorization.err = apperror.New("authorization.denied")
	events = nil
	_, err = service.ListAcademicUnitMembersPage(ctx, invocation.Invocation{}, ListAcademicUnitMembersPageQuery{AcademicUnitID: scopeID, AfterUserID: member.UserID, AfterID: member.ID, MembershipPageQuery: MembershipPageQuery{ActiveAt: &first.ActiveAt}})
	require.True(t, apperror.Is(err, "authorization.denied"), "revoked membership read: %v, events", err)
	require.Len(t, events, 1, "revoked membership read: %v, events", err)
	require.Equal(t, "authorize", events[0], "revoked membership read: %v, events", err)
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
	service := &classMemberService{deps: classMemberServiceDependencies{Store: persistence, Authorization: authorization, Now: func() time.Time { return now }}}
	first, err := service.ListClassMembersPage(ctx, invocation.Invocation{}, ListClassMembersPageQuery{ClassID: scopeID, MembershipPageQuery: MembershipPageQuery{Limit: 1}})
	require.NoError(t, err, "first page = %#v", first)
	require.True(t, first.HasMore, "first page = %#v", first)
	require.Len(t, events, 2)
	require.Equal(t, "authorize", events[0])
	require.Equal(t, "list-page", events[1])
	now = now.Add(time.Hour)
	persistence.pageResult = &store.ClassMemberPage{}
	next, err := service.ListClassMembersPage(ctx, invocation.Invocation{}, ListClassMembersPageQuery{ClassID: scopeID, AfterUserID: member.UserID, AfterID: member.ID, MembershipPageQuery: MembershipPageQuery{ActiveAt: &first.ActiveAt}})
	require.NoError(t, err, "last page = %#v", next)
	require.False(t, next.HasMore, "last page = %#v", next)
	require.NotNil(t, next.Members, "last page = %#v", next)
	require.Equal(t, int64(1000), persistence.pageOptions.ActiveAt)
	require.Equal(t, member.ID, persistence.pageOptions.AfterID)
	require.Equal(t, member.UserID, persistence.pageOptions.AfterUserID)
	authorization.err = apperror.New("authorization.denied")
	events = nil
	_, err = service.ListClassMembersPage(ctx, invocation.Invocation{}, ListClassMembersPageQuery{ClassID: scopeID, AfterUserID: member.UserID, AfterID: member.ID, MembershipPageQuery: MembershipPageQuery{ActiveAt: &first.ActiveAt}})
	require.True(t, apperror.Is(err, "authorization.denied"), "revoked membership read: %v, events", err)
	require.Len(t, events, 1, "revoked membership read: %v, events", err)
	require.Equal(t, "authorize", events[0], "revoked membership read: %v, events", err)
}
