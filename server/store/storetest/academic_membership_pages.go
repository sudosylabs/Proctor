// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package storetest

import (
	"context"
	"slices"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func testAcademicUnitMemberPaging(t *testing.T, ss store.Store) {
	ctx := context.Background()
	parents := saveClassFixture(t, ctx, ss)
	scope := saveAcademicUnit(t, ctx, ss, parents.institution.ID.String(), "", "paging-unit")
	at := model.GetMillis() + 1000
	for i := range 5 {
		user := saveUser(t, ctx, ss)
		startsAt := at
		if i == 4 {
			startsAt = at + 100
		}
		candidate := &model.AcademicUnitMember{AcademicUnitID: scope.ID, UserID: user.ID, StartsAt: model.TimeFromMillis(startsAt)}
		member, err := ss.AcademicUnitMember().Save(ctx, candidate)
		requireNoError(t, err)
		if i == 0 {
			_, err := ss.AcademicUnitMember().End(ctx, member.ID.String(), member.Revision, at+10)
			requireNoError(t, err)
			next := &model.AcademicUnitMember{AcademicUnitID: scope.ID, UserID: user.ID, StartsAt: model.TimeFromMillis(at + 10)}
			_, err = ss.AcademicUnitMember().Save(ctx, next)
			requireNoError(t, err)
		}
	}
	outsideUser := saveUser(t, ctx, ss)
	_, err := ss.AcademicUnitMember().Save(ctx, &model.AcademicUnitMember{AcademicUnitID: parents.programme.AcademicUnitID, UserID: outsideUser.ID, StartsAt: model.TimeFromMillis(at)})
	requireNoError(t, err)

	for _, activeAt := range []int64{0, at, at + 10, at + 200} {
		expected, err := ss.AcademicUnitMember().ListByAcademicUnit(ctx, scope.ID.String(), activeAt)
		requireNoError(t, err)
		expectedIDs := make([]string, len(expected))
		for i, member := range expected {
			expectedIDs[i] = member.ID.String()
		}
		for _, limit := range []int{1, 2, 3, 200} {
			options := store.AcademicUnitMemberPageOptions{AcademicUnitID: scope.ID, ActiveAt: activeAt, Limit: limit}
			actual := []string{}
			for pageNumber := 0; ; pageNumber++ {
				if pageNumber > len(expected)+1 {
					t.Fatal("membership page cursor did not advance")
				}
				page, err := ss.AcademicUnitMember().ListPageByAcademicUnit(ctx, options)
				requireNoError(t, err)
				if len(page.Members) > limit {
					t.Fatalf("unbounded page: %d", len(page.Members))
				}
				for _, member := range page.Members {
					if member.AcademicUnitID != scope.ID {
						t.Fatal("page escaped its authorized scope")
					}
					actual = append(actual, member.ID.String())
				}
				if len(page.Members) > 0 {
					last := page.Members[len(page.Members)-1]
					options.AfterUserID, options.AfterID = last.UserID, last.ID
				}
				if !page.HasMore {
					empty, err := ss.AcademicUnitMember().ListPageByAcademicUnit(ctx, options)
					requireNoError(t, err)
					if len(empty.Members) != 0 || empty.HasMore {
						t.Fatal("final cursor did not produce an empty page")
					}
					break
				}
				if len(page.Members) == 0 {
					t.Fatal("empty page advertised more results")
				}
			}
			if !slices.Equal(actual, expectedIDs) {
				t.Fatalf("effective=%d limit=%d pages=%v, want=%v", activeAt, limit, actual, expectedIDs)
			}
		}
	}
	for _, options := range []store.AcademicUnitMemberPageOptions{
		{AcademicUnitID: scope.ID, Limit: 0}, {AcademicUnitID: scope.ID, Limit: 201},
		{AcademicUnitID: scope.ID, Limit: 1, ActiveAt: -1},
		{AcademicUnitID: scope.ID, Limit: 1, AfterID: model.NewAcademicUnitMemberID()},
		{AcademicUnitID: scope.ID, Limit: 1, AfterUserID: model.NewUserID()},
		{Limit: 1},
	} {
		if _, err := ss.AcademicUnitMember().ListPageByAcademicUnit(ctx, options); err == nil {
			t.Fatalf("invalid page accepted: %#v", options)
		}
	}
}

func testClassMemberPaging(t *testing.T, ss store.Store) {
	ctx := context.Background()
	parents := saveClassFixture(t, ctx, ss)
	scope := saveClass(t, ctx, ss, parents.level.ID.String(), parents.period.ID.String(), "paging-class")
	at := model.GetMillis() + 1000
	for i := range 5 {
		user := saveUser(t, ctx, ss)
		_, err := ss.Affiliation().Save(ctx, &model.Affiliation{UserID: user.ID, Kind: model.AffiliationStudent, StartsAt: model.TimeFromMillis(at - 1)})
		requireNoError(t, err)
		startsAt := at
		if i == 4 {
			startsAt = at + 100
		}
		candidate := &model.ClassMember{ClassID: scope.ID, UserID: user.ID, StartsAt: model.TimeFromMillis(startsAt)}
		enrolled, err := ss.ClassMember().Enroll(ctx, candidate)
		requireNoError(t, err)
		member := enrolled.Membership
		if i == 0 {
			_, err := ss.ClassMember().End(ctx, member.ID.String(), member.Revision, at+10)
			requireNoError(t, err)
			next := &model.ClassMember{ClassID: scope.ID, UserID: user.ID, StartsAt: model.TimeFromMillis(at + 10)}
			_, err = ss.ClassMember().Enroll(ctx, next)
			requireNoError(t, err)
		}
	}
	outsideClass := saveClass(t, ctx, ss, parents.level.ID.String(), parents.period.ID.String(), "paging-other-class")
	outsideUser := saveUser(t, ctx, ss)
	_, err := ss.Affiliation().Save(ctx, &model.Affiliation{UserID: outsideUser.ID, Kind: model.AffiliationStudent, StartsAt: model.TimeFromMillis(at - 1)})
	requireNoError(t, err)
	_, err = ss.ClassMember().Enroll(ctx, &model.ClassMember{ClassID: outsideClass.ID, UserID: outsideUser.ID, StartsAt: model.TimeFromMillis(at)})
	requireNoError(t, err)

	for _, activeAt := range []int64{0, at, at + 10, at + 200} {
		expected, err := ss.ClassMember().ListByClass(ctx, scope.ID.String(), activeAt)
		requireNoError(t, err)
		expectedIDs := make([]string, len(expected))
		for i, member := range expected {
			expectedIDs[i] = member.ID.String()
		}
		for _, limit := range []int{1, 2, 3, 200} {
			options := store.ClassMemberPageOptions{ClassID: scope.ID, ActiveAt: activeAt, Limit: limit}
			actual := []string{}
			for pageNumber := 0; ; pageNumber++ {
				if pageNumber > len(expected)+1 {
					t.Fatal("membership page cursor did not advance")
				}
				page, err := ss.ClassMember().ListPageByClass(ctx, options)
				requireNoError(t, err)
				if len(page.Members) > limit {
					t.Fatalf("unbounded page: %d", len(page.Members))
				}
				for _, member := range page.Members {
					if member.ClassID != scope.ID {
						t.Fatal("page escaped its authorized scope")
					}
					actual = append(actual, member.ID.String())
				}
				if len(page.Members) > 0 {
					last := page.Members[len(page.Members)-1]
					options.AfterUserID, options.AfterID = last.UserID, last.ID
				}
				if !page.HasMore {
					empty, err := ss.ClassMember().ListPageByClass(ctx, options)
					requireNoError(t, err)
					if len(empty.Members) != 0 || empty.HasMore {
						t.Fatal("final cursor did not produce an empty page")
					}
					break
				}
				if len(page.Members) == 0 {
					t.Fatal("empty page advertised more results")
				}
			}
			if !slices.Equal(actual, expectedIDs) {
				t.Fatalf("effective=%d limit=%d pages=%v, want=%v", activeAt, limit, actual, expectedIDs)
			}
		}
	}
	for _, options := range []store.ClassMemberPageOptions{
		{ClassID: scope.ID, Limit: 0}, {ClassID: scope.ID, Limit: 201},
		{ClassID: scope.ID, Limit: 1, ActiveAt: -1},
		{ClassID: scope.ID, Limit: 1, AfterID: model.NewClassMemberID()},
		{ClassID: scope.ID, Limit: 1, AfterUserID: model.NewUserID()},
		{Limit: 1},
	} {
		if _, err := ss.ClassMember().ListPageByClass(ctx, options); err == nil {
			t.Fatalf("invalid page accepted: %#v", options)
		}
	}
}
