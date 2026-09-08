// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
package store

import "github.com/sudosylabs/proctor/server/model"

const DeliveryExpireOperation = "exam.delivery.expire.v1"

// DeliveryExpiryDue is a bounded maintenance selector, not candidate authority.
// The named mutation rechecks the owner and its database-time deadline.
type DeliveryExpiryDue struct {
	Family          string
	SourceID        string
	AttemptID       model.ExamAttemptID
	ParticipationID model.AttemptParticipationID
	SittingID       model.ExamSittingID
	ClassID         model.ClassID
}

func (d DeliveryExpiryDue) Valid() bool {
	if !d.AttemptID.IsValid() || !d.ParticipationID.IsValid() || !d.SittingID.IsValid() || !d.ClassID.IsValid() {
		return false
	}
	return d.Family == "native" && model.IsValidAgreementID(d.SourceID) || d.Family == "browser" && model.BrowserSourceSessionID(d.SourceID).IsValid()
}

type DeliveryExpiry struct {
	Due          DeliveryExpiryDue
	AuditEventID string
	AuditAt      int64
}
