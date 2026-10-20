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
	"time"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/exam/manageraccess"
	applicationidempotency "github.com/sudosylabs/proctor/server/app/idempotency"
	"github.com/sudosylabs/proctor/server/app/invocation"
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
	IdempotencyKey       string
}

type RemoveManagerCommand = AddManagerCommand
type TransferOwnerCommand = AddManagerCommand
type ManagerChange = store.ExamManagerCommandResult

func (a *Authoring) ListManagers(ctx context.Context, invocation invocation.Invocation, query ListManagersQuery) (ManagerPage, error) {
	if !query.ExamID.IsValid() || query.Limit < 1 || query.Limit > 200 ||
		(query.BeforeGrantedAt.IsZero() != query.BeforeUserID.IsZero()) {
		return ManagerPage{}, apperror.Invalid("exam.invalid", "manager_list")
	}
	if _, _, err := a.authorizeManagement(ctx, invocation, query.ExamID, model.TimeUTC(a.deps.Now())); err != nil {
		return ManagerPage{}, err
	}
	items, err := a.deps.Persistence.ListManagers(ctx, store.ExamManagerListOptions{
		ExamID: query.ExamID, BeforeGrantedAt: query.BeforeGrantedAt, BeforeUserID: query.BeforeUserID, Limit: query.Limit,
	})
	if err != nil {
		return ManagerPage{}, examStoreErrors.Translate(err)
	}
	page := ManagerPage{Items: make([]ManagerSummary, 0, len(items))}
	for _, item := range items {
		page.Items = append(page.Items, ManagerSummary{
			Manager: item.Manager, IsCreator: item.IsCreator, IsOwner: item.IsOwner,
		})
	}
	return page, nil
}

func (a *Authoring) AddManager(ctx context.Context, invocation invocation.Invocation, command AddManagerCommand) (ManagerChange, error) {
	return a.changeManager(ctx, invocation, command, managerAddition, a.deps.Persistence.AddManager)
}

func (a *Authoring) RemoveManager(ctx context.Context, invocation invocation.Invocation, command RemoveManagerCommand) (ManagerChange, error) {
	return a.changeManager(ctx, invocation, command, managerRemoval, a.deps.Persistence.RemoveManager)
}

func (a *Authoring) TransferOwner(ctx context.Context, invocation invocation.Invocation, command TransferOwnerCommand) (ManagerChange, error) {
	return a.changeManager(ctx, invocation, command, ownershipTransfer, a.deps.Persistence.TransferOwner)
}

type managerMutation func(context.Context, *store.ExamManagerMutation, *store.CommandIdempotency) (*store.ExamManagerCommandResult, error)

type managerEffect func(context.Context, Effects, model.ExamID, model.UserID, int64, time.Time) error

type managerTransition struct {
	operation            string
	idempotencyOperation string
	eligibilityRequired  bool
	templateKey          model.MailTemplateKey
	relationship         ManagerMailRelationship
	ownershipTransfer    bool
	publish              managerEffect
}

var (
	managerAddition = managerTransition{operation: "add_manager", idempotencyOperation: idempotencyOperationAddManager, eligibilityRequired: true,
		templateKey: model.MailTemplateExamManagerAdded, relationship: ManagerMailRelationshipManager,
		publish: func(ctx context.Context, effects Effects, examID model.ExamID, userID model.UserID, revision int64, changedAt time.Time) error {
			return effects.ManagerChanged(ctx, examID, userID, true, revision, changedAt)
		}}
	managerRemoval = managerTransition{operation: "remove_manager", idempotencyOperation: idempotencyOperationRemoveManager,
		templateKey: model.MailTemplateExamManagerRemoved, relationship: ManagerMailRelationshipNoLongerManager,
		publish: func(ctx context.Context, effects Effects, examID model.ExamID, userID model.UserID, revision int64, changedAt time.Time) error {
			return effects.ManagerChanged(ctx, examID, userID, false, revision, changedAt)
		}}
	ownershipTransfer = managerTransition{operation: "transfer_owner", idempotencyOperation: idempotencyOperationTransferOwner, eligibilityRequired: true, ownershipTransfer: true,
		publish: func(ctx context.Context, effects Effects, examID model.ExamID, userID model.UserID, revision int64, changedAt time.Time) error {
			return effects.OwnerTransferred(ctx, examID, userID, revision, changedAt)
		}}
)

func (a *Authoring) changeManager(ctx context.Context, invocation invocation.Invocation, command AddManagerCommand, transition managerTransition, mutate managerMutation) (ManagerChange, error) {
	principal := invocation.Principal()
	if principal.Validate() != nil || !command.ExamID.IsValid() || !command.UserID.IsValid() || command.ExpectedExamRevision < 1 {
		return ManagerChange{}, apperror.Invalid("exam.invalid", "manager_command")
	}
	idempotency, err := applicationidempotency.Require(invocation, transition.idempotencyOperation, command.IdempotencyKey, struct {
		ExamID               string `json:"exam_id"`
		UserID               string `json:"user_id"`
		ExpectedExamRevision int64  `json:"expected_exam_revision"`
	}{command.ExamID.String(), command.UserID.String(), command.ExpectedExamRevision})
	if err != nil {
		return ManagerChange{}, err
	}
	decisionAt := model.TimeUTC(a.deps.Now())
	access, action, err := a.authorizeManagement(ctx, invocation, command.ExamID, decisionAt)
	if err != nil {
		return ManagerChange{}, err
	}
	eligible := false
	if transition.eligibilityRequired {
		eligible, err = a.isEligibleManager(ctx, command.UserID, access.Exam.AcademicUnitID, decisionAt)
		if err != nil {
			return ManagerChange{}, err
		}
	}
	snapshot, err := a.deps.Persistence.Get(ctx, command.ExamID, principal.UserID)
	if err != nil {
		return ManagerChange{}, examStoreErrors.Translate(err)
	}
	if snapshot == nil || snapshot.Draft == nil {
		return ManagerChange{}, apperror.New("exam.unavailable").Wrap(errors.New("exam store returned no mail identity"))
	}
	resource := model.Resource{Type: model.ResourceExam, ID: command.ExamID.String()}
	auditData := map[string]any{
		"exam_id": command.ExamID.String(), "user_id": command.UserID.String(),
		"expected_exam_revision": command.ExpectedExamRevision, "exam_revision": command.ExpectedExamRevision + 1,
	}
	if transition.eligibilityRequired {
		auditData["target_eligible"] = eligible
	}
	auditID, err := a.deps.Auditor.Begin(ctx, invocation, action, resource, model.RoleScopeAcademicUnit, access.Exam.AcademicUnitID.String(), transition.operation, auditData, nil)
	if err != nil {
		return ManagerChange{}, err
	}
	// Mail and mutation records retain their existing millisecond contract;
	// current membership decisions above use the full domain precision.
	at := model.TimeFromMillis(model.MillisFromTime(decisionAt))
	notices, err := a.prepareManagerNotices(ctx, access.Exam.OwnerUserID, command.UserID, snapshot.Draft.Title, at, transition)
	if err != nil {
		mapped := apperror.New("exam.unavailable").Wrap(err)
		if auditErr := a.deps.Auditor.Fail(ctx, auditID, apperror.CodeOf(mapped, "exam.unavailable")); auditErr != nil {
			return ManagerChange{}, errors.Join(mapped, auditErr)
		}
		return ManagerChange{}, mapped
	}
	result, err := mutate(ctx, &store.ExamManagerMutation{
		ExamID: command.ExamID, ActorUserID: principal.UserID, TargetUserID: command.UserID,
		ManagerOverride: action == model.ActionExamManageOverride, ExpectedRevision: command.ExpectedExamRevision,
		ChangedAt: model.MillisFromTime(at), AuditEventID: auditID, AuditAt: model.MillisFromTime(at), Notices: notices,
	}, idempotency)
	if err != nil {
		mapped := examStoreErrors.Translate(err)
		if auditErr := a.deps.Auditor.Fail(ctx, auditID, apperror.CodeOf(mapped, "exam.unavailable")); auditErr != nil {
			return ManagerChange{}, errors.Join(mapped, auditErr)
		}
		return ManagerChange{}, mapped
	}
	if result == nil || result.Exam == nil || result.Manager == nil {
		return ManagerChange{}, apperror.New("exam.unavailable").Wrap(errors.New("exam store returned no manager result"))
	}
	if !result.Replayed {
		effectErr := transition.publish(ctx, a.deps.Effects, result.Exam.ID, command.UserID, result.Exam.Revision, at)
		if effectErr != nil {
			a.deps.EffectFailures.Report(ctx, transition.operation, effectErr)
		}
	}
	return *result, nil
}

func (a *Authoring) prepareManagerNotices(ctx context.Context, ownerID, targetID model.UserID, title string, at time.Time, transition managerTransition) ([]store.ExamManagerMail, error) {
	type recipientNotice struct {
		userID       model.UserID
		key          model.MailTemplateKey
		relationship ManagerMailRelationship
	}
	requests := []recipientNotice{{userID: targetID, key: transition.templateKey, relationship: transition.relationship}}
	if transition.ownershipTransfer {
		requests = []recipientNotice{
			{userID: ownerID, key: model.MailTemplateExamOwnershipTransferredFromYou, relationship: ManagerMailRelationshipManager},
			{userID: targetID, key: model.MailTemplateExamOwnershipTransferredToYou, relationship: ManagerMailRelationshipOwner},
		}
	}
	notices := make([]store.ExamManagerMail, 0, len(requests))
	for _, request := range requests {
		recipient, err := a.deps.Users.Get(ctx, request.userID)
		if err != nil || recipient == nil {
			return nil, errors.Join(errors.New("resolve Exam Manager mail recipient"), err)
		}
		prepared, err := a.deps.Mail.PrepareManagerMail(ManagerMailPreparation{
			Recipient: recipient, OccurrenceID: model.NewMailOccurrenceID(), TemplateKey: request.key,
			ExamTitle: title, Relationship: request.relationship, ActionAt: at,
		})
		if err != nil || prepared == nil {
			return nil, errors.Join(errors.New("prepare Exam Manager mail"), err)
		}
		notices = append(notices, *prepared)
	}
	return notices, nil
}

func (a *Authoring) authorizeManagement(ctx context.Context, invocation invocation.Invocation, examID model.ExamID, at time.Time) (*store.ExamAccessSnapshot, model.Action, error) {
	principal := invocation.Principal()
	access, err := a.deps.Persistence.Access(ctx, examID, principal.UserID)
	if err != nil {
		return nil, "", examStoreErrors.Translate(err)
	}
	if access == nil || access.Exam == nil {
		return nil, "", apperror.New("exam.unavailable").Wrap(errors.New("exam store returned no access projection"))
	}
	action, err := a.actionForAccess(ctx, principal.UserID, access, at, model.ActionExamManage, model.ActionExamManageOverride)
	if err != nil {
		return nil, "", err
	}
	if err := a.deps.Authorizer.Authorize(ctx, invocation, action, model.Resource{Type: model.ResourceExam, ID: examID.String()}); err != nil {
		return nil, "", err
	}
	return access, action, nil
}

func (a *Authoring) isEligibleManager(ctx context.Context, userID model.UserID, unitID model.AcademicUnitID, at time.Time) (bool, error) {
	user, err := a.deps.Users.Get(ctx, userID)
	if err != nil {
		if store.IsNotFound(err) {
			return false, nil
		}
		return false, apperror.New("exam.unavailable").Wrap(err)
	}
	if user == nil || !user.IsActive() {
		return false, nil
	}
	eligible, err := manageraccess.HasCurrentMembership(ctx, a.deps.Memberships, userID, unitID, at)
	if err != nil {
		return false, apperror.New("exam.unavailable").Wrap(err)
	}
	return eligible, nil
}
