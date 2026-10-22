// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package exam

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestListManagersUsesManageAuthorizationAndBoundedCursor(t *testing.T) {
	fixture := newAuthoringFixture(t)
	fixture.persistence.actorIsManager = true
	fixture.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: fixture.unitID}}
	grantedAt := time.Date(2026, 8, 13, 9, 0, 0, 0, time.UTC)
	target := model.NewUserID()
	fixture.persistence.managerSummaries = []store.ExamManagerSummary{{Manager: model.ExamManager{
		ExamID: fixture.examID, UserID: target, GrantedByUserID: fixture.userID, GrantedAt: grantedAt,
	}, IsCreator: false, IsOwner: false}}

	page, err := fixture.service.ListExamManagers(context.Background(), fixture.invocation, ListManagersQuery{
		ExamID: fixture.examID, BeforeGrantedAt: grantedAt.Add(time.Second), BeforeUserID: model.NewUserID(), Limit: 20,
	})
	require.NoError(t, err)
	require.Equal(t, model.ActionExamManage, fixture.authorizer.action, "authorization/page = %s/%#v", fixture.authorizer.action, page)
	require.Len(t, page.Items, 1, "authorization/page = %s/%#v", fixture.authorizer.action, page)
	require.Equal(t, target, page.Items[0].Manager.UserID, "authorization/page = %s/%#v", fixture.authorizer.action, page)
	require.Equal(t, 20, fixture.persistence.managerListOptions.Limit)
	require.Equal(t, fixture.examID, fixture.persistence.managerListOptions.ExamID)
}

func TestAddManagerChecksEligibilityBeforeAuditedAtomicMutation(t *testing.T) {
	fixture := newAuthoringFixture(t)
	fixture.persistence.actorIsManager = true
	fixture.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: fixture.unitID}}
	target := model.NewUserID()
	fixture.users.user = activeTestUser(target)
	fixture.users.users = map[string]*model.User{
		fixture.userID.String(): activeTestUser(fixture.userID),
		target.String():         fixture.users.user,
	}
	fixture.memberships.itemsByUser = map[string][]*model.AcademicUnitMember{
		fixture.userID.String(): {{AcademicUnitID: fixture.unitID}},
		target.String():         {{AcademicUnitID: fixture.unitID}},
	}
	command := AddManagerCommand{ExamID: fixture.examID, UserID: target, ExpectedExamRevision: 1,
		IdempotencyKey: "test-key"}

	result, err := fixture.service.AddExamManager(context.Background(), fixture.invocation, command)
	require.NoError(t, err)
	require.NotNil(t, result.Manager, "result/mutation = %#v/%#v", result, fixture.persistence.managerMutation)
	require.Equal(t, target, result.Manager.UserID, "result/mutation = %#v/%#v", result, fixture.persistence.managerMutation)
	require.Equal(t, target, fixture.persistence.managerMutation.TargetUserID, "result/mutation = %#v/%#v", result, fixture.persistence.managerMutation)
	require.Equal(t, fixture.examID.String(), fixture.auditor.value["exam_id"])
	require.Equal(t, target.String(), fixture.auditor.value["user_id"])
	require.Len(t, fixture.mail.requests, 1)
	request := fixture.mail.requests[0]
	require.Equal(t, target, request.Recipient.ID)
	require.Equal(t, "Test", request.ExamTitle)
	require.Equal(t, model.MailTemplateExamManagerAdded, request.TemplateKey)
	require.Equal(t, ManagerMailRelationshipManager, request.Relationship)
	require.False(t, request.ActionAt.IsZero())
	require.Len(t, fixture.persistence.managerMutation.Notices, 1)
	require.Equal(t, target, fixture.persistence.managerMutation.Notices[0].Delivery.TargetUserID)
	assertStoreIdempotency(t, fixture.persistence.idempotency, fixture.userID, idempotencyOperationAddManager, "test-key",
		fmt.Sprintf(`{"exam_id":%q,"user_id":%q,"expected_exam_revision":1}`, fixture.examID, target))
}

func TestAddManagerUsesNativeMembershipDecisionAndMutationTime(t *testing.T) {
	fixture := newAuthoringFixture(t)
	decisionAt := time.Date(2026, 9, 5, 12, 0, 0, 123700789, time.FixedZone("offset", 3600))
	boundary := model.TimeUTC(decisionAt)
	fixture.service.deps.Now = func() time.Time { return decisionAt }
	fixture.persistence.actorIsManager = true
	target := model.NewUserID()
	fixture.users.user = activeTestUser(target)
	fixture.users.users = map[string]*model.User{
		fixture.userID.String(): activeTestUser(fixture.userID),
		target.String():         fixture.users.user,
	}
	memberships := map[string]*model.AcademicUnitMember{
		fixture.userID.String(): {
			AcademicUnitID: fixture.unitID, UserID: fixture.userID,
			StartsAt: boundary.Add(-time.Hour), EndsAt: model.OptionalTimeFrom(boundary),
		},
		target.String(): {AcademicUnitID: fixture.unitID, UserID: target, StartsAt: boundary},
	}
	checked := make(map[string]time.Time)
	fixture.memberships.lookup = func(userID string, at time.Time) ([]*model.AcademicUnitMember, error) {
		checked[userID] = at
		if membership := memberships[userID]; membership != nil && membership.IsActiveAt(at) {
			return []*model.AcademicUnitMember{membership}, nil
		}
		return nil, nil
	}

	_, err := fixture.service.AddExamManager(context.Background(), fixture.invocation, AddManagerCommand{
		ExamID: fixture.examID, UserID: target, ExpectedExamRevision: 1, IdempotencyKey: "precise-manager-time",
	})
	require.NoError(t, err)
	require.Equal(t, model.ActionExamManageOverride, fixture.authorizer.action, "expired actor/new target authorization = %s/%#v", fixture.authorizer.action, fixture.auditor.value)
	require.Equal(t, true, fixture.auditor.value["target_eligible"], "expired actor/new target authorization = %s/%#v", fixture.authorizer.action, fixture.auditor.value)
	require.Len(t, checked, 2, "membership decision times")
	require.Equal(t, boundary, checked[fixture.userID.String()], "membership decision times")
	require.Equal(t, boundary, checked[target.String()], "membership decision times")
	mutation := fixture.persistence.managerMutation
	require.NotNil(t, mutation, "mutation times")
	require.True(t, mutation.ManagerOverride, "mutation times")
	require.Equal(t, boundary, mutation.ChangedAt, "mutation times")
	require.Equal(t, boundary, mutation.AuditAt, "mutation times")
	require.Len(t, fixture.mail.requests, 1)
	require.Equal(t, boundary, fixture.mail.requests[0].ActionAt)
}

func TestAddManagerRejectsInactiveOrUnrelatedTargetBeforeAudit(t *testing.T) {
	for _, test := range []struct {
		name       string
		activeUser bool
		membership bool
	}{
		{name: "inactive user", membership: true},
		{name: "missing exact membership", activeUser: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newAuthoringFixture(t)
			fixture.persistence.actorIsManager = true
			fixture.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: fixture.unitID}}
			target := model.NewUserID()
			fixture.persistence.managerErr = store.NewErrConflict("exam_manager", "exam_manager_ineligible", nil)
			fixture.users.user = activeTestUser(target)
			if !test.activeUser {
				fixture.users.user.DisabledAt = model.OptionalTimeFrom(time.Now().UTC())
			}
			if test.membership {
				fixture.memberships.itemsByUser = map[string][]*model.AcademicUnitMember{target.String(): {{AcademicUnitID: fixture.unitID}}}
			} else {
				fixture.memberships.itemsByUser = map[string][]*model.AcademicUnitMember{target.String(): {}}
			}
			_, err := fixture.service.AddExamManager(context.Background(), fixture.invocation, AddManagerCommand{
				ExamID: fixture.examID, UserID: target, ExpectedExamRevision: 1, IdempotencyKey: "test-key",
			})
			require.Equal(t, "exam.manager.ineligible", faultCode(err))
			require.NotNil(t, fixture.persistence.managerMutation, "ineligible target did not reach the replay-aware guarded Store path: mutation=%#v audit=%#v failure", fixture.persistence.managerMutation, fixture.auditor.value)
			require.Equal(t, false, fixture.auditor.value["target_eligible"], "ineligible target did not reach the replay-aware guarded Store path: mutation=%#v audit=%#v failure", fixture.persistence.managerMutation, fixture.auditor.value)
			require.Equal(t, "exam.manager.ineligible", fixture.auditor.failedCode, "ineligible target did not reach the replay-aware guarded Store path: mutation=%#v audit=%#v failure", fixture.persistence.managerMutation, fixture.auditor.value)
		})
	}
}

func TestManagerMailPreparationFailureStartsNoMutationAndFailsAudit(t *testing.T) {
	fixture := newAuthoringFixture(t)
	fixture.persistence.actorIsManager = true
	fixture.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: fixture.unitID}}
	target := model.NewUserID()
	fixture.users.user = activeTestUser(target)
	fixture.memberships.itemsByUser = map[string][]*model.AcademicUnitMember{target.String(): {{AcademicUnitID: fixture.unitID}}}
	fixture.mail.err = errors.New("render unavailable")

	_, err := fixture.service.AddExamManager(context.Background(), fixture.invocation, AddManagerCommand{
		ExamID: fixture.examID, UserID: target, ExpectedExamRevision: 1,
		IdempotencyKey: "test-key",
	})
	require.Equal(t, "exam.unavailable", faultCode(err))
	require.Nil(t, fixture.persistence.managerMutation, "mutation/audit")
	require.Equal(t, "exam.unavailable", fixture.auditor.failedCode, "mutation/audit")
}

func TestRemoveAndTransferPreserveStoreInvariantFailures(t *testing.T) {
	fixture := newAuthoringFixture(t)
	fixture.persistence.actorIsManager = true
	fixture.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: fixture.unitID}}
	fixture.persistence.managerErr = store.NewErrConflict("exam_manager", "exam_owner_manager", nil)
	_, err := fixture.service.RemoveExamManager(context.Background(), fixture.invocation, RemoveManagerCommand{
		ExamID: fixture.examID, UserID: fixture.userID, ExpectedExamRevision: 1, IdempotencyKey: "test-key",
	})
	require.Equal(t, "exam.manager.owner_protected", faultCode(err), "remove owner error")
	assertStoreIdempotency(t, fixture.persistence.idempotency, fixture.userID, idempotencyOperationRemoveManager, "test-key",
		fmt.Sprintf(`{"exam_id":%q,"user_id":%q,"expected_exam_revision":1}`, fixture.examID, fixture.userID))
	_, exists := fixture.auditor.value["target_eligible"]
	require.False(t, exists, "removal audit claims an eligibility decision: %#v", fixture.auditor.value)

	fixture = newAuthoringFixture(t)
	fixture.persistence.actorIsManager = true
	fixture.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: fixture.unitID}}
	target := model.NewUserID()
	fixture.users.user = activeTestUser(target)
	fixture.users.users = map[string]*model.User{
		fixture.userID.String(): activeTestUser(fixture.userID),
		target.String():         fixture.users.user,
	}
	fixture.memberships.itemsByUser = map[string][]*model.AcademicUnitMember{target.String(): {{AcademicUnitID: fixture.unitID}}}
	transferred, err := fixture.service.TransferExamOwnership(context.Background(), fixture.invocation, TransferOwnerCommand{
		ExamID: fixture.examID, UserID: target, ExpectedExamRevision: 1, IdempotencyKey: "test-key",
	})
	require.NoError(t, err, "transfer = %#v", transferred)
	require.NotNil(t, transferred.Exam, "transfer = %#v", transferred)
	require.Equal(t, target, transferred.Exam.OwnerUserID, "transfer = %#v", transferred)
	require.Len(t, fixture.mail.requests, 2)
	require.Equal(t, fixture.userID, fixture.mail.requests[0].Recipient.ID)
	require.Equal(t, model.MailTemplateExamOwnershipTransferredFromYou, fixture.mail.requests[0].TemplateKey)
	require.Equal(t, ManagerMailRelationshipManager, fixture.mail.requests[0].Relationship)
	require.Equal(t, target, fixture.mail.requests[1].Recipient.ID)
	require.Equal(t, model.MailTemplateExamOwnershipTransferredToYou, fixture.mail.requests[1].TemplateKey)
	require.Equal(t, ManagerMailRelationshipOwner, fixture.mail.requests[1].Relationship)
	assertStoreIdempotency(t, fixture.persistence.idempotency, fixture.userID, idempotencyOperationTransferOwner, "test-key",
		fmt.Sprintf(`{"exam_id":%q,"user_id":%q,"expected_exam_revision":1}`, fixture.examID, target))
}

func activeTestUser(id model.UserID) *model.User {
	at := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	user := &model.User{Username: "manager", Email: "manager@example.edu"}
	user.PrepareCreate(id, at)
	return user
}

func faultCode(err error) string {
	if fault, ok := apperror.As(err); ok {
		return fault.Code()
	}
	return ""
}
