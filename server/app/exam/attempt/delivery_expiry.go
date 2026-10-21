// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
package attempt

import (
	"context"
	"errors"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func (s *Service) ScanExpiredDeliveries(ctx context.Context, limit int) (ExpiryScanResult, error) {
	if limit < 1 || limit > 200 {
		return ExpiryScanResult{}, apperror.Invalid("exam.attempt.invalid", "delivery_expiry_limit")
	}
	due, err := s.deps.Persistence.ListExpiredDeliveries(ctx, limit)
	if err != nil {
		return ExpiryScanResult{}, storeError(storeErrors, err)
	}
	result := ExpiryScanResult{Due: len(due)}
	if len(due) > limit {
		return result, apperror.New("exam.attempt.unavailable").Wrap(model.ErrDeliveryInvalid)
	}
	for _, d := range due {
		if !d.Valid() {
			return result, apperror.New("exam.attempt.unavailable").Wrap(model.ErrDeliveryInvalid)
		}
		audit, err := s.deps.SystemAuditor.Begin(ctx, model.ActionExamSittingManage, model.Resource{Type: model.ResourceExamSitting, ID: d.SittingID.String()}, model.RoleScopeClass, d.ClassID.String(), store.DeliveryExpireOperation, map[string]any{"family": d.Family, "source_id": d.SourceID})
		if err != nil {
			return result, err
		}
		changed, err := s.deps.Persistence.ExpireDelivery(ctx, &store.DeliveryExpiry{Due: d, AuditEventID: audit, AuditAt: model.MillisFromTime(s.deps.Now())})
		if err != nil {
			if auditErr := s.deps.SystemAuditor.Fail(ctx, audit, "exam.attempt.unavailable"); auditErr != nil {
				return result, errors.Join(storeError(storeErrors, err), auditErr)
			}
			return result, storeError(storeErrors, err)
		}
		result.Completed++
		if !changed {
			result.Replayed++
		}
	}
	return result, nil
}
