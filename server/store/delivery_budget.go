// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
package store

import (
	"errors"

	"github.com/sudosylabs/proctor/server/model"
)

// ErrDeliveryAdmissionFull means no detail worker or database connection was admitted.
// Callers must not turn this load-shedding refusal into additional recovery reads.
var ErrDeliveryAdmissionFull = errors.New("delivery admission capacity unavailable")

type DeliveryBudgetAccess struct {
	Access          CandidateAttemptAccess
	ParticipationID model.AttemptParticipationID
}
type DeliveryDetailsStop struct {
	Access       CandidateAttemptAccess
	Request      model.StopDeliveryDetails
	AuditEventID string
	AuditAt      int64
}

const DeliveryStopDetailsOperation = "exam.delivery.stop_details.v1"
