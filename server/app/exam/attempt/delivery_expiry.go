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

func (service *Service) ScanExpiredDeliveries(ctx context.Context, limit int) (ExpiryScanResult, error) {
	if limit < 1 || limit > 200 {
		return ExpiryScanResult{}, invalid("delivery_expiry_limit")
	}
	due, err := service.deps.Persistence.ListExpiredDeliveries(ctx, limit)
	if err != nil {
		return ExpiryScanResult{}, mapStore(err)
	}
	result := ExpiryScanResult{Due: len(due)}
	if len(due) > limit {
		return result, unavailable(model.ErrDeliveryInvalid)
	}
	for _, d := range due {
		if !d.Valid() {
			return result, unavailable(model.ErrDeliveryInvalid)
		}
		audit, err := service.deps.SystemAuditor.Begin(ctx, model.ActionExamSittingManage, model.Resource{Type: model.ResourceExamSitting, ID: d.SittingID.String()}, model.RoleScopeClass, d.ClassID.String(), store.DeliveryExpireOperation, map[string]any{"family": d.Family, "source_id": d.SourceID})
		if err != nil {
			return result, err
		}
		changed, err := service.deps.Persistence.ExpireDelivery(ctx, &store.DeliveryExpiry{Due: d, AuditEventID: audit, AuditAt: model.MillisFromTime(service.deps.Now())})
		if err != nil {
			if auditErr := service.deps.SystemAuditor.Fail(ctx, audit, "exam.attempt.unavailable"); auditErr != nil {
				return result, auditErr
			}
			return result, mapStore(err)
		}
		result.Completed++
		if !changed {
			result.Replayed++
		}
	}
	return result, nil
}
