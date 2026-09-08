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

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// A recovery projection is deliberately obtained after the failed transaction.
// Every read repeats current ownership checks. Read failures preserve the original
// refusal without disclosing a cached or client-selected owner's state.
func (service *Service) nativeDeliveryFailure(ctx context.Context, access store.NativeDeliveryAccess, err error) error {
	mapped := mapNativeDeliveryError(err)
	var fault *Fault
	if errors.Is(err, store.ErrDeliveryAdmissionFull) || !errors.As(mapped, &fault) || !SupportsDeliveryRecovery(fault.Code) {
		return mapped
	}
	target, readErr := service.deps.Persistence.ResolveNativeDeliveryTarget(ctx, access)
	if readErr != nil || target == nil || target.Security.Validate() != nil {
		return mapped
	}
	part := target.Security.ParticipationID
	budget, readErr := service.deps.Persistence.DeliveryBudget(ctx, store.DeliveryBudgetAccess{Access: access.Access, ParticipationID: part})
	if readErr != nil || budget == nil || budget.ParticipationID != part || budget.Generation != target.Security.Generation {
		return mapped
	}
	status, readErr := service.deps.Persistence.NativeDeliveryStatus(ctx, access)
	if readErr != nil || status == nil || status.StreamID != access.StreamID {
		return mapped
	}
	return withDeliveryRecovery(fault, model.DeliveryRecovery{Family: "native", Budget: *budget, Native: status})
}

func (service *Service) browserDeliveryFailure(ctx context.Context, access store.BrowserDeliveryAccess, err error) error {
	mapped := mapBrowserDeliveryError(err)
	var fault *Fault
	if errors.Is(err, store.ErrDeliveryAdmissionFull) || !errors.As(mapped, &fault) || !SupportsDeliveryRecovery(fault.Code) {
		return mapped
	}
	status, readErr := service.deps.Persistence.BrowserSourceStatus(ctx, access)
	if readErr != nil || status == nil || status.SourceSessionID != access.SourceSessionID {
		return mapped
	}
	budget, readErr := service.deps.Persistence.DeliveryBudget(ctx, store.DeliveryBudgetAccess{Access: access.Access, ParticipationID: status.ParticipationID})
	if readErr != nil || budget == nil {
		return mapped
	}
	return withDeliveryRecovery(fault, model.DeliveryRecovery{Family: "browser", Budget: *budget, Browser: status})
}

func withDeliveryRecovery(fault *Fault, recovery model.DeliveryRecovery) error {
	if recovery.Validate() != nil {
		return fault
	}
	result := *fault
	result.Recovery = &recovery
	return &result
}

// SupportsDeliveryRecovery identifies the application failures with bounded repair context.
func SupportsDeliveryRecovery(code string) bool {
	switch code {
	case "exam.delivery.pending_capacity", "exam.delivery.replay_window_exceeded",
		"exam.delivery.detail_budget_exhausted", "exam.delivery.sequence_limit",
		"exam.delivery.declaration_conflict", "exam.delivery.upload_expired",
		"exam.delivery.append_rate_limited", "exam.delivery.summary_rate_limited":
		return true
	default:
		return false
	}
}
