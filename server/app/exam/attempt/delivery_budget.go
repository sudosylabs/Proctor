// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
package attempt

import (
	"context"

	"github.com/sudosylabs/proctor/server/app/apperror"
	applicationidempotency "github.com/sudosylabs/proctor/server/app/idempotency"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type DeliveryBudgetQuery struct {
	Access          CandidateAccess
	ParticipationID model.AttemptParticipationID
}
type StopDeliveryDetailsCommand struct {
	Access         CandidateAccess
	Request        model.StopDeliveryDetails
	IdempotencyKey string
}

func (service *Service) DeliveryBudget(ctx context.Context, invocation invocation.Invocation, query DeliveryBudgetQuery) (_ *model.DeliveryBudgetSnapshot, resultErr error) {
	defer func() { resultErr = publicFailures.Conceal(resultErr) }()
	release, admissionErr := service.enterControl(ctx, invocation, false)
	if admissionErr != nil {
		return nil, admissionErr
	}
	defer release()
	if !query.ParticipationID.IsValid() {
		return nil, apperror.Invalid("exam.attempt.invalid", "participation_id")
	}
	access, err := nativeDeliverySelector(invocation, NativeDeliveryQuery{Access: query.Access, StreamID: query.ParticipationID.String()})
	if err != nil {
		return nil, err
	}
	value, err := service.deps.Persistence.DeliveryBudget(ctx, store.DeliveryBudgetAccess{Access: access.Access, ParticipationID: query.ParticipationID})
	if err != nil {
		return nil, deliveryStoreError(err)
	}
	if value == nil || value.Validate() != nil || value.ParticipationID != query.ParticipationID {
		return nil, apperror.New("exam.attempt.unavailable").Wrap(model.ErrDeliveryInvalid)
	}
	return value, nil
}
func (service *Service) StopDeliveryDetails(ctx context.Context, invocation invocation.Invocation, command StopDeliveryDetailsCommand) (_ *model.StopDeliveryDetailsResult, resultErr error) {
	defer func() { resultErr = publicFailures.Conceal(resultErr) }()
	release, admissionErr := service.enterControl(ctx, invocation, false)
	if admissionErr != nil {
		return nil, admissionErr
	}
	defer release()
	access, err := candidateSelector(invocation, command.Access)
	if err != nil {
		return nil, err
	}
	if command.Request.Validate() != nil {
		return nil, apperror.Invalid("exam.attempt.invalid", "stop_delivery_details")
	}
	idempotency, err := applicationidempotency.Require(invocation, store.DeliveryStopDetailsOperation, command.IdempotencyKey, struct {
		Attempt model.ExamAttemptID
		Request model.StopDeliveryDetails
	}{access.AttemptID, command.Request})
	if err != nil {
		return nil, err
	}
	target, err := service.deps.Persistence.ResolveLiveDeliveryTarget(ctx, access)
	if err != nil {
		return nil, storeError(storeErrors, err)
	}
	if target == nil || !target.SittingID.IsValid() || !target.ClassID.IsValid() {
		return nil, apperror.New("exam.attempt.unavailable").Wrap(model.ErrDeliveryInvalid)
	}
	audit, err := service.deps.Auditor.BeginAtScope(ctx, invocation, model.ActionExamSittingParticipate, model.Resource{Type: model.ResourceExamSitting, ID: target.SittingID.String()}, model.RoleScopeClass, target.ClassID.String(), store.DeliveryStopDetailsOperation, map[string]any{"family": command.Request.Family}, nil)
	if err != nil {
		return nil, err
	}
	value, err := service.deps.Persistence.StopDeliveryDetails(ctx, &store.DeliveryDetailsStop{Access: access, Request: command.Request, AuditEventID: audit, AuditAt: model.MillisFromTime(service.deps.Now())}, idempotency)
	if err != nil {
		return nil, service.failAudit(ctx, audit, deliveryStoreError(err))
	}
	if value == nil || value.Validate(command.Request.Family) != nil {
		return nil, apperror.New("exam.attempt.unavailable").Wrap(model.ErrDeliveryInvalid)
	}
	return value, nil
}
