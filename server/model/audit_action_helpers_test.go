// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

// IsAuditAction reports whether action is a declared audit action: a
// permission Action or an event-only audit action.
func IsAuditAction(action string) bool {
	if _, ok := actionDefinitions[Action(action)]; ok {
		return true
	}
	_, ok := eventAuditActions[Action(action)]
	return ok
}
