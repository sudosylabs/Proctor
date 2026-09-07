// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import "time"

// UserMFARecovery is the durable authority fence created by assisted MFA reset.
// Generation zero is the initial unrestricted state. A completed reenrollment
// clears only the restriction; its generation and reset instant remain forever
// to reject previously collected primary proof and unfinished provider flows.
type UserMFARecovery struct {
	UserID               UserID
	Generation           int64
	ResetAt              OptionalTime
	ReenrollmentRequired bool
	UpdatedAt            time.Time
}

func (r UserMFARecovery) Validate() error {
	if !r.UserID.IsValid() || r.Generation < 0 ||
		(r.Generation == 0 && (r.ResetAt.Valid || r.ReenrollmentRequired || !r.UpdatedAt.IsZero())) ||
		(r.Generation > 0 && (!r.ResetAt.Valid || r.ResetAt.Time.IsZero() || r.UpdatedAt.Before(r.ResetAt.Time))) {
		return invalidModelError("UserMFARecovery.Validate", "user_mfa_recovery", "state", "is inconsistent", "")
	}
	return nil
}

func (r UserMFARecovery) Auditable() map[string]any {
	return map[string]any{"user_id": r.UserID.String(), "generation": r.Generation, "reset_at": r.ResetAt.Millis(), "reenrollment_required": r.ReenrollmentRequired}
}
