// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
package attempt

import (
	"context"

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

func (service *Service) DeliveryBudget(ctx context.Context, call Call, query DeliveryBudgetQuery) (*model.DeliveryBudgetSnapshot, error) {
	release, admissionErr := service.enterControl(ctx, call, false)
	if admissionErr != nil {
		return nil, admissionErr
	}
	defer release()
	if !query.ParticipationID.IsValid() {
		return nil, invalid("participation_id")
	}
	access, err := nativeDeliverySelector(call, NativeDeliveryQuery{Access: query.Access, StreamID: query.ParticipationID.String()})
	if err != nil {
		return nil, err
	}
	value, err := service.deps.Persistence.DeliveryBudget(ctx, store.DeliveryBudgetAccess{Access: access.Access, ParticipationID: query.ParticipationID})
	if err != nil {
		return nil, mapNativeDeliveryError(err)
	}
	if value == nil || value.Validate() != nil || value.ParticipationID != query.ParticipationID {
		return nil, unavailable(model.ErrDeliveryInvalid)
	}
	return value, nil
}
func (service *Service) StopDeliveryDetails(ctx context.Context, call Call, command StopDeliveryDetailsCommand) (*model.StopDeliveryDetailsResult, error) {
	release, admissionErr := service.enterControl(ctx, call, false)
	if admissionErr != nil {
		return nil, admissionErr
	}
	defer release()
	access, err := candidateSelector(call, command.Access)
	if err != nil {
		return nil, err
	}
	if command.Request.Validate() != nil {
		return nil, invalid("stop_delivery_details")
	}
	idempotency, err := prepareIdempotency(call, store.DeliveryStopDetailsOperation, command.IdempotencyKey, struct {
		Attempt model.ExamAttemptID
		Request model.StopDeliveryDetails
	}{access.AttemptID, command.Request})
	if err != nil {
		return nil, err
	}
	target, err := service.deps.Persistence.ResolveLiveDeliveryTarget(ctx, access)
	if err != nil {
		return nil, mapStore(err)
	}
	if target == nil || !target.SittingID.IsValid() || !target.ClassID.IsValid() {
		return nil, unavailable(model.ErrDeliveryInvalid)
	}
	audit, err := service.deps.Auditor.Begin(ctx, call, model.ActionExamSittingParticipate, model.Resource{Type: model.ResourceExamSitting, ID: target.SittingID.String()}, model.RoleScopeClass, target.ClassID.String(), store.DeliveryStopDetailsOperation, map[string]any{"family": command.Request.Family})
	if err != nil {
		return nil, err
	}
	value, err := service.deps.Persistence.StopDeliveryDetails(ctx, &store.DeliveryDetailsStop{Access: access, Request: command.Request, AuditEventID: audit, AuditAt: model.MillisFromTime(service.deps.Now())}, idempotency)
	if err != nil {
		return nil, service.failAudit(ctx, audit, mapNativeDeliveryError(err))
	}
	if value == nil || value.Validate(command.Request.Family) != nil {
		return nil, unavailable(model.ErrDeliveryInvalid)
	}
	return value, nil
}
