//go:build integration

// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"context"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store/storetest"
	"testing"
)

func TestBrowserRetainedRepairReservation(t *testing.T) {
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	storetest.TestBrowserRetainedRepairReservation(t, persistence, func(ctx context.Context, attempt model.ExamAttemptID, participation model.AttemptParticipationID, partBytes, attemptBytes int64) error {
		if _, err := persistence.GetMaster().Exec(ctx, `UPDATE exam_attempt_security_owners SET browser_retained_bytes=? WHERE participation_id=?`, partBytes, participation.String()); err != nil {
			return err
		}
		_, err := persistence.GetMaster().Exec(ctx, `UPDATE exam_attempt_delivery_budgets SET browser_retained_bytes=? WHERE exam_attempt_id=?`, attemptBytes, attempt.String())
		return err
	})
}
