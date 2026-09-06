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
			if action != test.wantAction || !errors.Is(err, test.wantErr) {
				t.Fatalf("action/error = %q/%v, want %q/%v", action, err, test.wantAction, test.wantErr)
			}
			wantCalls := 0
			if test.manager {
				wantCalls = 1
			}
			if calls != wantCalls {
				t.Fatalf("membership calls = %d, want %d", calls, wantCalls)
			}
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
	if err != nil || first != model.ActionExamPublish {
		t.Fatalf("initial selection = %q/%v", first, err)
	}
	members = nil
	second, err := manageraccess.SelectAction(context.Background(), memberships, userID, access, at,
		model.ActionExamPublish, model.ActionExamPublishOverride)
	if err != nil || second != model.ActionExamPublishOverride {
		t.Fatalf("selection after membership revocation = %q/%v", second, err)
	}
}

func TestHasCurrentMembershipForwardsUserTimeAndContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	userID, unitID := model.NewUserID(), model.NewAcademicUnitID()
	at := time.Date(2026, 9, 5, 12, 0, 0, 123456789, time.FixedZone("test", 3600))
	memberships := membershipLookup(func(gotContext context.Context, gotUser string, gotAt time.Time) ([]*model.AcademicUnitMember, error) {
		if gotContext != ctx || gotUser != userID.String() || gotAt != model.TimeUTC(at) {
			t.Fatalf("lookup inputs = %v/%s/%v, want original context/%s/%v", gotContext, gotUser, gotAt, userID, model.TimeUTC(at))
		}
		return []*model.AcademicUnitMember{nil, {AcademicUnitID: model.NewAcademicUnitID()}, {AcademicUnitID: unitID}}, nil
	})
	member, err := manageraccess.HasCurrentMembership(ctx, memberships, userID, unitID, at)
	if err != nil || !member {
		t.Fatalf("membership = %t/%v, want true", member, err)
	}
}

type membershipLookup func(context.Context, string, time.Time) ([]*model.AcademicUnitMember, error)

func (lookup membershipLookup) ListActiveByUser(ctx context.Context, userID string, at time.Time) ([]*model.AcademicUnitMember, error) {
	return lookup(ctx, userID, at)
}
