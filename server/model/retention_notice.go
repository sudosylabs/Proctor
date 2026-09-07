// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import "time"

// RetentionNotice is the durable recipient projection of a scheduled category.
// Delivery status is separate from the retention state and never acknowledges
// that the recipient read a notice. It contains no candidate or answer content.
type RetentionNotice struct {
	RetirementID      RetentionRetirementID
	RecipientUserID   UserID
	Scope             RetentionHoldScope
	Category          RetentionCategory
	State             RetentionRetirementState
	CreatedAt         time.Time
	RetireAfter       time.Time
	CancelledAt       OptionalTime
	DeliveryState     string
	DeliveryErrorCode string
}
