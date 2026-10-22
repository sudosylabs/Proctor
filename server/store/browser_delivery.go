// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package store

import "github.com/sudosylabs/proctor/server/model"

type BrowserDeliveryGapDeclaration struct {
	Access       BrowserDeliveryAccess
	Declaration  model.DeclareDeliveryGaps
	AuditEventID model.AuditEventID
	AuditAt      int64
}
type BrowserDeliveryFinalDeclaration struct {
	Access       BrowserDeliveryAccess
	Declaration  model.FinalDeliveryDeclaration
	AuditEventID model.AuditEventID
	AuditAt      int64
}
type BrowserDeliverySummaryUpdate struct {
	Access       BrowserDeliveryAccess
	Summary      model.UnretainedDeliverySummary
	AuditEventID model.AuditEventID
	AuditAt      int64
}

const (
	BrowserDeliveryGapsOperation    = "exam.browser_delivery.gaps"
	BrowserDeliveryFinalOperation   = "exam.browser_delivery.final"
	BrowserDeliverySummaryOperation = "exam.browser_delivery.summary"
	BrowserDeliveryAppendOperation  = "exam.browser_delivery.append"
)
