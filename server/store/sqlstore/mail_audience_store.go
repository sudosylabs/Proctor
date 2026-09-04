// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"context"
	"fmt"
)

// advanceUserMailEligibilityRevision serializes every User eligibility change
// against disabled Sitting watermarks. Callers stamp the returned revision on
// the same User update in this transaction.
func advanceUserMailEligibilityRevision(ctx context.Context, tx *sqlxTxWrapper) (int64, error) {
	var revision int64
	if err := tx.Get(ctx, &revision, `UPDATE mail_audience_states
		SET user_eligibility_revision=user_eligibility_revision+1 WHERE singleton=1
		RETURNING user_eligibility_revision`); err != nil {
		return 0, fmt.Errorf("advance User mail eligibility revision: %w", err)
	}
	return revision, nil
}

func currentUserMailEligibilityRevision(ctx context.Context, tx *sqlxTxWrapper) (int64, error) {
	var revision int64
	if err := tx.Get(ctx, &revision, `SELECT user_eligibility_revision FROM mail_audience_states
		WHERE singleton=1 FOR SHARE`); err != nil {
		return 0, fmt.Errorf("lock User mail eligibility chronology: %w", err)
	}
	return revision, nil
}
