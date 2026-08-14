// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package exam

import (
	"context"
	"errors"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type ListManagersQuery struct {
	ExamID          model.ExamID
	BeforeGrantedAt time.Time
	BeforeUserID    model.UserID
	Limit           int
}

// ManagerSummary is the bounded application projection for one management
// relationship. User profile data is deliberately outside this seam.
type ManagerSummary struct {
	Manager   model.ExamManager
	IsCreator bool
	IsOwner   bool
}

type ManagerPage struct{ Items []ManagerSummary }

type AddManagerCommand struct {
	ExamID               model.ExamID
	UserID               model.UserID
	ExpectedExamRevision int64
	Idempotency          *store.CommandIdempotency
}

type RemoveManagerCommand = AddManagerCommand
type TransferOwnerCommand = AddManagerCommand
type ManagerChange = store.ExamManagerCommandResult

func (a *Authoring) ListManagers(ctx context.Context, call Call, query ListManagersQuery) (ManagerPage, error) {
	if !query.ExamID.IsValid() || query.Limit < 1 || query.Limit > 200 ||
		(query.BeforeGrantedAt.IsZero() != query.BeforeUserID.IsZero()) {
		return ManagerPage{}, invalid("manager_list")
	}
	if _, _, err := a.authorizeManagement(ctx, call, query.ExamID, model.TimeUTC(a.now())); err != nil {
		return ManagerPage{}, err
	}
	items, err := a.persistence.ListManagers(ctx, store.ExamManagerListOptions{
		ExamID: query.ExamID, BeforeGrantedAt: query.BeforeGrantedAt, BeforeUserID: query.BeforeUserID, Limit: query.Limit,
	})
	if err != nil {
		return ManagerPage{}, mapStoreError(err)
	}
	page := ManagerPage{Items: make([]ManagerSummary, 0, len(items))}
	for _, item := range items {
		page.Items = append(page.Items, ManagerSummary{
			Manager: item.Manager, IsCreator: item.IsCreator, IsOwner: item.IsOwner,
		})
	}
	return page, nil
}

func (a *Authoring) AddManager(ctx context.Context, call Call, command AddManagerCommand) (ManagerChange, error) {
	return a.changeManager(ctx, call, command, managerAddition, a.persistence.AddManager)
}

func (a *Authoring) RemoveManager(ctx context.Context, call Call, command RemoveManagerCommand) (ManagerChange, error) {
	return a.changeManager(ctx, call, command, managerRemoval, a.persistence.RemoveManager)
}

func (a *Authoring) TransferOwner(ctx context.Context, call Call, command TransferOwnerCommand) (ManagerChange, error) {
	return a.changeManager(ctx, call, command, ownershipTransfer, a.persistence.TransferOwner)
}

type managerMutation func(context.Context, *store.ExamManagerMutation, *store.CommandIdempotency) (*store.ExamManagerCommandResult, error)

type managerEffect func(context.Context, Effects, model.ExamID, model.UserID, int64, time.Time) error

type managerTransition struct {
	operation           string
	eligibilityRequired bool
	publish             managerEffect
}

var (
	managerAddition = managerTransition{operation: "add_manager", eligibilityRequired: true,
		publish: func(ctx context.Context, effects Effects, examID model.ExamID, userID model.UserID, revision int64, changedAt time.Time) error {
			return effects.ManagerChanged(ctx, examID, userID, true, revision, changedAt)
		}}
	managerRemoval = managerTransition{operation: "remove_manager",
		publish: func(ctx context.Context, effects Effects, examID model.ExamID, userID model.UserID, revision int64, changedAt time.Time) error {
			return effects.ManagerChanged(ctx, examID, userID, false, revision, changedAt)
		}}
	ownershipTransfer = managerTransition{operation: "transfer_owner", eligibilityRequired: true,
		publish: func(ctx context.Context, effects Effects, examID model.ExamID, userID model.UserID, revision int64, changedAt time.Time) error {
			return effects.OwnerTransferred(ctx, examID, userID, revision, changedAt)
		}}
)

func (a *Authoring) changeManager(ctx context.Context, call Call, command AddManagerCommand, transition managerTransition, mutate managerMutation) (ManagerChange, error) {
	principal := call.Principal()
	if principal.Validate() != nil || !command.ExamID.IsValid() || !command.UserID.IsValid() || command.ExpectedExamRevision < 1 {
		return ManagerChange{}, invalid("manager_command")
	}
	if command.Idempotency == nil {
		return ManagerChange{}, &Fault{Code: "idempotency.key_required"}
	}
	at := model.TimeUTC(a.now())
	access, action, err := a.authorizeManagement(ctx, call, command.ExamID, at)
	if err != nil {
		return ManagerChange{}, err
	}
	eligible := false
	if transition.eligibilityRequired {
		eligible, err = a.isEligibleManager(ctx, command.UserID, access.Exam.AcademicUnitID, at)
		if err != nil {
			return ManagerChange{}, err
		}
	}
	resource := model.Resource{Type: model.ResourceExam, ID: command.ExamID.String()}
	auditData := map[string]any{
		"exam_id": command.ExamID.String(), "user_id": command.UserID.String(),
		"expected_exam_revision": command.ExpectedExamRevision, "exam_revision": command.ExpectedExamRevision + 1,
	}
	if transition.eligibilityRequired {
		auditData["target_eligible"] = eligible
	}
	auditID, err := a.auditor.Begin(ctx, call, action, resource, model.RoleScopeAcademicUnit, access.Exam.AcademicUnitID.String(), transition.operation, auditData, nil)
	if err != nil {
		return ManagerChange{}, err
	}
	result, err := mutate(ctx, &store.ExamManagerMutation{
		ExamID: command.ExamID, ActorUserID: principal.UserID, TargetUserID: command.UserID,
		ManagerOverride: action == model.ActionExamManageOverride, ExpectedRevision: command.ExpectedExamRevision,
		ChangedAt: model.MillisFromTime(at), AuditEventID: auditID, AuditAt: model.MillisFromTime(at),
	}, command.Idempotency)
	if err != nil {
		mapped := mapStoreError(err)
		if auditErr := a.auditor.Fail(ctx, auditID, faultCodeForAudit(mapped)); auditErr != nil {
			return ManagerChange{}, auditErr
		}
		return ManagerChange{}, mapped
	}
	if result == nil || result.Exam == nil || result.Manager == nil {
		return ManagerChange{}, unavailable(errors.New("exam store returned no manager result"))
	}
	if !result.Replayed {
		effectErr := transition.publish(ctx, a.effects, result.Exam.ID, command.UserID, result.Exam.Revision, at)
		if effectErr != nil {
			a.failures.Report(ctx, transition.operation, effectErr)
		}
	}
	return *result, nil
}

func (a *Authoring) authorizeManagement(ctx context.Context, call Call, examID model.ExamID, at time.Time) (*store.ExamAccessSnapshot, model.Action, error) {
	principal := call.Principal()
	access, err := a.persistence.Access(ctx, examID, principal.UserID)
	if err != nil {
		return nil, "", mapStoreError(err)
	}
	if access == nil || access.Exam == nil {
		return nil, "", unavailable(errors.New("exam store returned no access projection"))
	}
	action, err := a.actionForAccess(ctx, principal.UserID, access, at, model.ActionExamManage, model.ActionExamManageOverride)
	if err != nil {
		return nil, "", err
	}
	if err := a.authorizer.Authorize(ctx, call, action, model.Resource{Type: model.ResourceExam, ID: examID.String()}); err != nil {
		return nil, "", err
	}
	return access, action, nil
}

func (a *Authoring) isEligibleManager(ctx context.Context, userID model.UserID, unitID model.AcademicUnitID, at time.Time) (bool, error) {
	user, err := a.users.Get(ctx, userID.String())
	if err != nil {
		if store.IsNotFound(err) {
			return false, nil
		}
		return false, unavailable(err)
	}
	if user == nil || !user.IsActive() {
		return false, nil
	}
	eligible, err := a.hasCurrentMembership(ctx, userID, unitID, at)
	if err != nil {
		return false, unavailable(err)
	}
	return eligible, nil
}

func faultCodeForAudit(err error) string {
	var fault *Fault
	if errors.As(err, &fault) {
		return fault.Code
	}
	return "exam.unavailable"
}
