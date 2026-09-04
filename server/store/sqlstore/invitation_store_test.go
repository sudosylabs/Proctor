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

	"github.com/sudosylabs/proctor/server/model"
)

func TestTeacherInvitationDelegationRequirementRejectsInertAcademicUnitAction(t *testing.T) {
	if _, err := teacherInvitationDelegationRequirement(model.ActionRoleManage); err == nil {
		t.Fatal("role.manage produced an Academic Unit delegation requirement")
	}
	requirement, err := teacherInvitationDelegationRequirement(model.ActionProgrammeManage)
	if err != nil {
		t.Fatal(err)
	}
	if !requirement.Institution || !requirement.AcademicUnit {
		t.Fatalf("programme.manage delegation requirement = %#v", requirement)
	}
	examRequirement, err := teacherInvitationDelegationRequirement(model.ActionExamManage)
	if err != nil {
		t.Fatal(err)
	}
	if !examRequirement.Institution || !examRequirement.AcademicUnit {
		t.Fatalf("exam.manage delegation requirement = %#v", examRequirement)
	}
}

func TestInvitationEffectiveIntervalCovered(t *testing.T) {
	start := model.TimeUTC(time.Date(2026, time.August, 18, 10, 0, 0, 0, time.UTC))
	finite := model.OptionalTimeFrom(start.Add(4 * time.Hour))
	if invitationEffectiveIntervalCovered(start, finite, start, model.OptionalTime{}) {
		t.Fatal("finite relationship covered an unbounded package")
	}
	if !invitationEffectiveIntervalCovered(start, finite, start.Add(time.Hour), model.OptionalTimeFrom(start.Add(3*time.Hour))) {
		t.Fatal("covering bounded relationship was rejected")
	}
	if invitationEffectiveIntervalCovered(start.Add(time.Hour), model.OptionalTime{}, start, model.OptionalTimeFrom(start.Add(3*time.Hour))) {
		t.Fatal("relationship beginning after package start was accepted")
	}
}
