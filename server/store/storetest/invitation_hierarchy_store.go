// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package storetest

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type InvitationHierarchySQLProbe struct {
	MoveProgrammeBeforeIssue func(*testing.T, context.Context, *model.Programme, model.AcademicUnitID, func() error) error
	MoveLevelBeforeAccept    func(*testing.T, context.Context, *model.ProgrammeLevel, model.ProgrammeID, func() error) error
}

func TestInvitationHierarchyFences(t *testing.T, ss store.Store, probe InvitationHierarchySQLProbe) {
	t.Run("IssueSerializesWithProgrammeScopeMove", func(t *testing.T) {
		testInvitationIssueSerializesWithProgrammeScopeMove(t, ss, probe.MoveProgrammeBeforeIssue)
	})
	t.Run("AcceptSerializesWithProgrammeLevelScopeMove", func(t *testing.T) {
		testInvitationAcceptSerializesWithLevelScopeMove(t, ss, probe.MoveLevelBeforeAccept)
	})
}

func testInvitationIssueSerializesWithProgrammeScopeMove(
	t *testing.T,
	ss store.Store,
	move func(*testing.T, context.Context, *model.Programme, model.AcademicUnitID, func() error) error,
) {
	t.Helper()
	ctx := context.Background()
	fixture, class, inviter, role, binding, issuedAt := invitationAcceptanceStoreFixture(t, ctx, ss, "programme-move-race")
	replaceInvitationClassAuthorityWithUnit(t, ctx, ss, inviter, role, binding, fixture.programme.AcademicUnitID, issuedAt)
	targetUnit := saveAcademicUnit(t, ctx, ss, fixture.institution.ID.String(), "", "invitation-programme-move-target")
	issue := studentClassInvitationIssueFixture(t, ss, inviter, class, fixture.period, issuedAt)
	err := move(t, ctx, fixture.programme, targetUnit.ID, func() error {
		_, issueErr := ss.Invitation().IssueStudentClass(ctx, issue)
		return issueErr
	})
	require.True(t, store.IsConflict(err), "IssueStudentClass() concurrent Programme move error")
	_, err = ss.Invitation().Get(ctx, issue.Invitation.ID)
	require.True(t, store.IsNotFound(err), "Invitation survived concurrent Programme move")
}

func testInvitationAcceptSerializesWithLevelScopeMove(
	t *testing.T,
	ss store.Store,
	move func(*testing.T, context.Context, *model.ProgrammeLevel, model.ProgrammeID, func() error) error,
) {
	t.Helper()
	ctx := context.Background()
	fixture, class, inviter, role, binding, issuedAt := invitationAcceptanceStoreFixture(t, ctx, ss, "level-move-race")
	replaceInvitationClassAuthorityWithUnit(t, ctx, ss, inviter, role, binding, fixture.programme.AcademicUnitID, issuedAt)
	issue := studentClassInvitationIssueFixture(t, ss, inviter, class, fixture.period, issuedAt)
	invitation, err := ss.Invitation().IssueStudentClass(ctx, issue)
	require.NoError(t, err)
	targetUnit := saveAcademicUnit(t, ctx, ss, fixture.institution.ID.String(), "", "invitation-level-move-target")
	targetProgramme := saveProgramme(t, ctx, ss, targetUnit.ID.String(), "invitation-level-move-programme")
	acceptance := studentClassInvitationAcceptanceFixture(t, invitation, model.NowUTC())
	err = move(t, ctx, fixture.level, targetProgramme.ID, func() error {
		_, acceptErr := ss.Invitation().AcceptStudentClass(ctx, acceptance)
		return acceptErr
	})
	require.True(t, store.IsConflict(err), "AcceptStudentClass() concurrent Programme Level move error")
	current, err := ss.Invitation().Get(ctx, invitation.ID)
	require.NoError(t, err)
	require.Equal(t, model.InvitationPending, current.State)
}

func replaceInvitationClassAuthorityWithUnit(
	t *testing.T,
	ctx context.Context,
	ss store.Store,
	inviter *model.User,
	role *model.Role,
	binding *model.RoleBinding,
	unitID model.AcademicUnitID,
	issuedAt time.Time,
) {
	t.Helper()
	_, err := EndRoleBinding(ctx, ss, binding.ID, model.NowUTC())
	require.NoError(t, err)
	_, err = SaveRoleBinding(ctx, ss, &model.RoleBinding{
		UserID: inviter.ID, RoleID: role.ID, ScopeType: model.RoleScopeAcademicUnit,
		ScopeID: unitID.String(), StartsAt: issuedAt.Add(-time.Second),
	})
	require.NoError(t, err)
}
