// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package exam

import (
	"context"
	"testing"
	"time"

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

	page, err := fixture.service.ListManagers(context.Background(), fixture.call, ListManagersQuery{
		ExamID: fixture.examID, BeforeGrantedAt: grantedAt.Add(time.Second), BeforeUserID: model.NewUserID(), Limit: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if fixture.authorizer.action != model.ActionExamManage || len(page.Items) != 1 || page.Items[0].Manager.UserID != target {
		t.Fatalf("authorization/page = %s/%#v", fixture.authorizer.action, page)
	}
	if fixture.persistence.managerListOptions.Limit != 20 || fixture.persistence.managerListOptions.ExamID != fixture.examID {
		t.Fatalf("list options = %#v", fixture.persistence.managerListOptions)
	}
}

func TestAddManagerChecksEligibilityBeforeAuditedAtomicMutation(t *testing.T) {
	fixture := newAuthoringFixture(t)
	fixture.persistence.actorIsManager = true
	fixture.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: fixture.unitID}}
	target := model.NewUserID()
	fixture.users.user = activeTestUser(target)
	fixture.memberships.itemsByUser = map[string][]*model.AcademicUnitMember{
		fixture.userID.String(): {{AcademicUnitID: fixture.unitID}},
		target.String():         {{AcademicUnitID: fixture.unitID}},
	}
	command := AddManagerCommand{ExamID: fixture.examID, UserID: target, ExpectedExamRevision: 1,
		Idempotency: &store.CommandIdempotency{UserID: fixture.userID}}

	result, err := fixture.service.AddManager(context.Background(), fixture.call, command)
	if err != nil {
		t.Fatal(err)
	}
	if result.Manager == nil || result.Manager.UserID != target || fixture.persistence.managerMutation.TargetUserID != target {
		t.Fatalf("result/mutation = %#v/%#v", result, fixture.persistence.managerMutation)
	}
	if fixture.auditor.value["exam_id"] != fixture.examID.String() || fixture.auditor.value["user_id"] != target.String() {
		t.Fatalf("audit value = %#v", fixture.auditor.value)
	}
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
			_, err := fixture.service.AddManager(context.Background(), fixture.call, AddManagerCommand{
				ExamID: fixture.examID, UserID: target, ExpectedExamRevision: 1, Idempotency: &store.CommandIdempotency{UserID: fixture.userID},
			})
			if faultCode(err) != "exam.manager.ineligible" {
				t.Fatalf("error = %v", err)
			}
			if fixture.persistence.managerMutation == nil || fixture.auditor.value["target_eligible"] != false || fixture.auditor.failedCode != "exam.manager.ineligible" {
				t.Fatalf("ineligible target did not reach the replay-aware guarded Store path: mutation=%#v audit=%#v failure=%q", fixture.persistence.managerMutation, fixture.auditor.value, fixture.auditor.failedCode)
			}
		})
	}
}

func TestRemoveAndTransferPreserveStoreInvariantFailures(t *testing.T) {
	fixture := newAuthoringFixture(t)
	fixture.persistence.actorIsManager = true
	fixture.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: fixture.unitID}}
	fixture.persistence.managerErr = store.NewErrConflict("exam_manager", "exam_owner_manager", nil)
	_, err := fixture.service.RemoveManager(context.Background(), fixture.call, RemoveManagerCommand{
		ExamID: fixture.examID, UserID: fixture.userID, ExpectedExamRevision: 1, Idempotency: &store.CommandIdempotency{UserID: fixture.userID},
	})
	if faultCode(err) != "exam.manager.owner_protected" {
		t.Fatalf("remove owner error = %v", err)
	}
	if _, exists := fixture.auditor.value["target_eligible"]; exists {
		t.Fatalf("removal audit claims an eligibility decision: %#v", fixture.auditor.value)
	}

	fixture = newAuthoringFixture(t)
	fixture.persistence.actorIsManager = true
	fixture.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: fixture.unitID}}
	target := model.NewUserID()
	fixture.users.user = activeTestUser(target)
	fixture.memberships.itemsByUser = map[string][]*model.AcademicUnitMember{target.String(): {{AcademicUnitID: fixture.unitID}}}
	transferred, err := fixture.service.TransferOwner(context.Background(), fixture.call, TransferOwnerCommand{
		ExamID: fixture.examID, UserID: target, ExpectedExamRevision: 1, Idempotency: &store.CommandIdempotency{UserID: fixture.userID},
	})
	if err != nil || transferred.Exam == nil || transferred.Exam.OwnerUserID != target {
		t.Fatalf("transfer = %#v, %v", transferred, err)
	}
}

func activeTestUser(id model.UserID) *model.User {
	at := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	user := &model.User{Username: "manager", Email: "manager@example.edu"}
	user.PrepareCreate(id, at)
	return user
}

func faultCode(err error) string {
	if fault, ok := err.(*Fault); ok {
		return fault.Code
	}
	return ""
}
