// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

func TestTeacherInvitationDelegationRequirementRejectsInertAcademicUnitAction(t *testing.T) {
	_, err := teacherInvitationDelegationRequirement(model.ActionRoleManage)
	require.Error(t, err, "role.manage produced an Academic Unit delegation requirement")
	requirement, err := teacherInvitationDelegationRequirement(model.ActionProgrammeManage)
	require.NoError(t, err)
	require.True(t, requirement.Institution)
	require.True(t, requirement.AcademicUnit)
	examRequirement, err := teacherInvitationDelegationRequirement(model.ActionExamManage)
	require.NoError(t, err)
	require.True(t, examRequirement.Institution)
	require.True(t, examRequirement.AcademicUnit)
}

func TestInvitationEffectiveIntervalCovered(t *testing.T) {
	start := model.TimeUTC(time.Date(2026, time.August, 18, 10, 0, 0, 0, time.UTC))
	finite := model.OptionalTimeFrom(start.Add(4 * time.Hour))
	require.False(t, invitationEffectiveIntervalCovered(start, finite, start, model.OptionalTime{}), "finite relationship covered an unbounded package")
	require.True(t, invitationEffectiveIntervalCovered(start, finite, start.Add(time.Hour), model.OptionalTimeFrom(start.Add(3*time.Hour))), "covering bounded relationship was rejected")
	require.False(t, invitationEffectiveIntervalCovered(start.Add(time.Hour), model.OptionalTime{}, start, model.OptionalTimeFrom(start.Add(3*time.Hour))), "relationship beginning after package start was accepted")
}
