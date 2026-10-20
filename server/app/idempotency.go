// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"github.com/sudosylabs/proctor/server/model"
)

func replayAuditData(value map[string]any, originalAuditID string) map[string]any {
	result := make(map[string]any, len(value)+2)
	for key, item := range value {
		result[key] = item
	}
	result["idempotency_replayed"] = true
	if model.AuditEventID(originalAuditID).IsValid() {
		result["original_audit_event_id"] = originalAuditID
	}
	return result
}
