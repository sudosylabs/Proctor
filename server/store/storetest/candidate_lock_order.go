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

// CandidateFenceKind names a lock that Invitation acceptance takes after it
// has locked the candidate User row.
type CandidateFenceKind string

const (
	// CandidateFenceClassEnrollment is the candidate's per-period Class
	// enrollment fence (student Class acceptance).
	CandidateFenceClassEnrollment CandidateFenceKind = "class_enrollment"
	// CandidateFenceAttemptAdmission is the candidate's Attempt admission fence
	// (Role Binding grants inside teacher and scoped-Role acceptance).
	CandidateFenceAttemptAdmission CandidateFenceKind = "attempt_admission"
	// CandidateFenceClassLifecycle is the installation-wide Class lifecycle
	// lock (student Class acceptance).
	CandidateFenceClassLifecycle CandidateFenceKind = "class_lifecycle"
)

// CandidateFence selects the lock a CandidateLockProbe takes after the User
// row. AcademicPeriodID is used only by the Class-enrollment fence.
type CandidateFence struct {
	Kind             CandidateFenceKind
	AcademicPeriodID model.AcademicPeriodID
}

// CandidateLockProbe runs operation while a concurrent transaction replays
// Invitation acceptance's lock sequence for the candidate: the User row for
// update, then fence once operation is waiting. It returns the first error
// from either side; a deadlock aborts one of them.
type CandidateLockProbe func(
	t *testing.T,
	ctx context.Context,
	candidateID model.UserID,
	fence CandidateFence,
	operation func() error,
) error

// TestExamManagerAddCandidateLockOrder verifies that adding an Exam Manager
// locks the target User before the Attempt admission fence, so it waits behind
// an Invitation acceptance for the same User instead of deadlocking it.
func TestExamManagerAddCandidateLockOrder(t *testing.T, ss store.Store, probe CandidateLockProbe) {
	t.Helper()
	require.NotNil(t, probe, "candidate lock probe is required")
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	unit := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "manager-lock-order-unit")
	creator, target := saveUser(t, ctx, ss), saveUser(t, ctx, ss)
	at := model.NowUTC()
	for _, user := range []*model.User{creator, target} {
		_, err := ss.AcademicUnitMember().Save(ctx, &model.AcademicUnitMember{AcademicUnitID: unit.ID, UserID: user.ID, StartsAt: at.Add(-time.Hour)})
		require.NoError(t, err)
	}
	created := createCatalogExam(t, ctx, ss, unit.ID, creator.ID, at, "manager-lock-order")
	add := newExamManagerMutation(t, ctx, ss, created.Value.Exam.ID, creator.ID, target.ID, 1, at.Add(time.Minute), false)
	add.Notices = examManagerMailNotices(t, at.Add(time.Minute), examManagerMailRecipient{target.ID, model.MailTemplateExamManagerAdded})
	var added *store.ExamManagerCommandResult
	err := probe(t, ctx, target.ID, CandidateFence{Kind: CandidateFenceAttemptAdmission}, func() error {
		var addErr error
		added, addErr = ss.ExamAuthoring().AddManager(ctx, add, examCommand(creator.ID, "exam.manager.add", "manager-lock-order", "manager-lock-order"))
		return addErr
	})
	require.NoError(t, err)
	require.NotNil(t, added)
	require.Equal(t, target.ID, added.Manager.UserID)
}

// TestRoleBindingCandidateLockOrder verifies that granting a Role Binding
// locks the User before the Attempt admission fence and the Class lifecycle
// lock, so it waits behind an Invitation acceptance for the same User.
func TestRoleBindingCandidateLockOrder(t *testing.T, ss store.Store, probe CandidateLockProbe) {
	t.Helper()
	require.NotNil(t, probe, "candidate lock probe is required")
	ctx := context.Background()
	fixture := saveClassFixture(t, ctx, ss)
	class := saveClass(t, ctx, ss, fixture.level.ID.String(), fixture.period.ID.String(), "binding-lock-order")
	for _, test := range []struct {
		name      string
		fence     CandidateFenceKind
		scopeType model.RoleScopeType
		scopeID   string
	}{
		{"attempt admission", CandidateFenceAttemptAdmission, model.RoleScopeInstitution, fixture.institution.ID.String()},
		{"class lifecycle", CandidateFenceClassLifecycle, model.RoleScopeClass, class.ID.String()},
	} {
		t.Run(test.name, func(t *testing.T) {
			user := saveUser(t, ctx, ss)
			role, err := SaveRole(ctx, ss, &model.Role{Name: "lock-order-" + string(test.fence), DisplayName: "Lock Order", Permissions: []string{string(model.ActionClassView)}})
			require.NoError(t, err)
			var saved *model.RoleBinding
			err = probe(t, ctx, user.ID, CandidateFence{Kind: test.fence}, func() error {
				var saveErr error
				saved, saveErr = SaveRoleBinding(ctx, ss, &model.RoleBinding{
					UserID: user.ID, RoleID: role.ID, ScopeType: test.scopeType, ScopeID: test.scopeID,
					StartsAt: model.TimeFromMillis(model.GetMillis()),
				})
				return saveErr
			})
			require.NoError(t, err)
			require.NotNil(t, saved)
			require.Equal(t, user.ID, saved.UserID)
		})
	}
}

// TestClassEnrollmentCandidateLockOrder verifies that an audited Class
// enrollment locks the student (as its notice recipient) before the Class
// lifecycle lock and the Class-enrollment fence, matching Invitation
// acceptance.
func TestClassEnrollmentCandidateLockOrder(t *testing.T, ss store.Store, probe CandidateLockProbe) {
	t.Helper()
	require.NotNil(t, probe, "candidate lock probe is required")
	ctx := context.Background()
	fixture := saveClassFixture(t, ctx, ss)
	start := model.GetMillis()
	period := saveAcademicPeriod(t, ctx, ss, fixture.institution.ID.String(), "enrollment-lock-order-"+model.NewID(), start-3_600_000)
	class := saveClass(t, ctx, ss, fixture.level.ID.String(), period.ID.String(), "enrollment-lock-order")
	for _, fence := range []CandidateFence{
		{Kind: CandidateFenceClassLifecycle},
		{Kind: CandidateFenceClassEnrollment, AcademicPeriodID: class.AcademicPeriodID},
	} {
		t.Run(string(fence.Kind), func(t *testing.T) {
			user := saveUser(t, ctx, ss)
			_, err := ss.Affiliation().Save(ctx, &model.Affiliation{
				UserID: user.ID, Kind: model.AffiliationStudent, StartsAt: model.TimeFromMillis(start - 1),
			})
			require.NoError(t, err)
			member := &model.ClassMember{ClassID: class.ID, UserID: user.ID, StartsAt: model.TimeFromMillis(start)}
			member.PrepareCreate(model.NewClassMemberID(), model.NowUTC())
			member.AcademicPeriodID = class.AcademicPeriodID
			attempt := saveClassMemberAuditAttempt(t, ctx, ss, class.ID.String())
			var enrolled *store.ClassEnrollmentResult
			err = probe(t, ctx, user.ID, fence, func() error {
				var enrollErr error
				enrolled, enrollErr = ss.ClassMember().Enroll(ctx, &store.ClassMemberEnrollment{
					Member: member, ExpectedRecipientRevision: user.Revision, AuditEventID: attempt.ID, AuditAt: model.GetMillis(),
					Notice: classMemberPreparedMail(t, member, model.MailTemplateAcademicClassEnrolled, member.CreatedAt),
				})
				return enrollErr
			})
			require.NoError(t, err)
			require.NotNil(t, enrolled)
			require.Equal(t, member.ID, enrolled.Membership.ID)
		})
	}
}
