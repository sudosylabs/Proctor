// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package manageraccess_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/exam/manageraccess"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestSelectActionRequiresCurrentManagerAndExactUnitMembership(t *testing.T) {
	t.Parallel()
	userID, unitID := model.NewUserID(), model.NewAcademicUnitID()
	at := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	failure := errors.New("membership unavailable")
	for _, test := range []struct {
		name       string
		manager    bool
		members    []*model.AcademicUnitMember
		lookupErr  error
		wantAction model.Action
		wantErr    error
	}{
		{name: "current manager and exact unit", manager: true, members: []*model.AcademicUnitMember{{AcademicUnitID: unitID}}, wantAction: model.ActionSubmissionView},
		{name: "membership does not create manager relationship", members: []*model.AcademicUnitMember{{AcademicUnitID: unitID}}, wantAction: model.ActionSubmissionViewOverride},
		{name: "non-manager skips unavailable membership lookup", lookupErr: failure, wantAction: model.ActionSubmissionViewOverride},
		{name: "manager without current membership", manager: true, wantAction: model.ActionSubmissionViewOverride},
		{name: "membership in another unit is insufficient", manager: true, members: []*model.AcademicUnitMember{{AcademicUnitID: model.NewAcademicUnitID()}}, wantAction: model.ActionSubmissionViewOverride},
		{name: "nil membership entries are ignored", manager: true, members: []*model.AcademicUnitMember{nil, {AcademicUnitID: unitID}}, wantAction: model.ActionSubmissionView},
		{name: "failed lookup never selects either permission path", manager: true, members: []*model.AcademicUnitMember{{AcademicUnitID: unitID}}, lookupErr: failure, wantErr: failure},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			calls := 0
			memberships := membershipLookup(func(context.Context, string, time.Time) ([]*model.AcademicUnitMember, error) {
				calls++
				return test.members, test.lookupErr
			})
			access := &store.ExamAccessSnapshot{Exam: &model.Exam{AcademicUnitID: unitID}, ActorIsManager: test.manager}
			action, err := manageraccess.SelectAction(context.Background(), memberships, userID, access, at,
				model.ActionSubmissionView, model.ActionSubmissionViewOverride)
			require.Equal(t, test.wantAction, action, "action/error")
			require.ErrorIs(t, err, test.wantErr, "action/error")
			wantCalls := 0
			if test.manager {
				wantCalls = 1
			}
			require.Equal(t, wantCalls, calls, "membership calls")
		})
	}
}

func TestSelectActionRechecksMembershipForEachCall(t *testing.T) {
	t.Parallel()
	userID, unitID := model.NewUserID(), model.NewAcademicUnitID()
	members := []*model.AcademicUnitMember{{AcademicUnitID: unitID}}
	memberships := membershipLookup(func(context.Context, string, time.Time) ([]*model.AcademicUnitMember, error) {
		return members, nil
	})
	access := &store.ExamAccessSnapshot{Exam: &model.Exam{AcademicUnitID: unitID}, ActorIsManager: true}
	at := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	first, err := manageraccess.SelectAction(context.Background(), memberships, userID, access, at,
		model.ActionExamPublish, model.ActionExamPublishOverride)
	require.NoError(t, err, "initial selection")
	require.Equal(t, model.ActionExamPublish, first, "initial selection")
	members = nil
	second, err := manageraccess.SelectAction(context.Background(), memberships, userID, access, at,
		model.ActionExamPublish, model.ActionExamPublishOverride)
	require.NoError(t, err, "selection after membership revocation")
	require.Equal(t, model.ActionExamPublishOverride, second, "selection after membership revocation")
}

func TestHasCurrentMembershipForwardsUserTimeAndContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	userID, unitID := model.NewUserID(), model.NewAcademicUnitID()
	at := time.Date(2026, 9, 5, 12, 0, 0, 123456789, time.FixedZone("test", 3600))
	memberships := membershipLookup(func(gotContext context.Context, gotUser string, gotAt time.Time) ([]*model.AcademicUnitMember, error) {
		require.Equal(t, ctx, gotContext, "lookup inputs = %v/%s/%v, want original context/%s", gotContext, gotUser, gotAt, userID)
		require.Equal(t, userID.String(), gotUser, "lookup inputs = %v/%s/%v, want original context/%s", gotContext, gotUser, gotAt, userID)
		require.Equal(t, model.TimeUTC(at), gotAt, "lookup inputs = %v/%s/%v, want original context/%s", gotContext, gotUser, gotAt, userID)
		return []*model.AcademicUnitMember{nil, {AcademicUnitID: model.NewAcademicUnitID()}, {AcademicUnitID: unitID}}, nil
	})
	member, err := manageraccess.HasCurrentMembership(ctx, memberships, userID, unitID, at)
	require.NoError(t, err, "membership")
	require.True(t, member, "membership")
}

type membershipLookup func(context.Context, string, time.Time) ([]*model.AcademicUnitMember, error)

func (l membershipLookup) ListActiveByUser(ctx context.Context, userID model.UserID, at time.Time) ([]*model.AcademicUnitMember, error) {
	return l(ctx, userID.String(), at)
}
