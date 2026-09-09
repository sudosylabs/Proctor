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
	"testing"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store/storetest"
)

func TestBrowserExhaustionSettlement(t *testing.T) {
	for _, mode := range []string{"participation_records", "attempt_records", "participation_bytes", "attempt_bytes", "metadata", "intervals"} {
		t.Run(mode, func(t *testing.T) {
			s := openTestStore(t)
			resetTestStore(t, s)
			ctx := context.Background()
			storetest.TestBrowserExhaustionSettlement(t, s, mode, func(attempt model.ExamAttemptID, part model.AttemptParticipationID) {
				var query string
				var value int64
				id := attempt.String()
				switch mode {
				case "participation_records":
					query = `UPDATE exam_attempt_security_owners SET browser_retained_records=? WHERE participation_id=?`
					value = model.DeliveryParticipationRecordLimit
					id = part.String()
				case "attempt_records":
					query = `UPDATE exam_attempt_delivery_budgets SET browser_retained_records=? WHERE exam_attempt_id=?`
					value = model.DeliveryAttemptRecordLimit
				case "participation_bytes":
					query = `UPDATE exam_attempt_security_owners SET browser_retained_bytes=? WHERE participation_id=?`
					value = model.DeliveryParticipationByteLimit
					id = part.String()
				case "attempt_bytes":
					query = `UPDATE exam_attempt_delivery_budgets SET browser_retained_bytes=? WHERE exam_attempt_id=?`
					value = model.DeliveryAttemptByteLimit
				case "metadata":
					query = `UPDATE exam_attempt_delivery_budgets SET control_metadata_bytes=? WHERE exam_attempt_id=?`
					value = model.DeliveryMetadataLimitBytes
				case "intervals":
					query = `UPDATE exam_attempt_delivery_budgets SET explicit_missing_intervals=? WHERE exam_attempt_id=?`
					value = model.DeliveryMissingIntervalLimit
				}
				if _, err := s.GetMaster().Exec(ctx, query, value, id); err != nil {
					t.Fatal(err)
				}
			}, func(attempt model.ExamAttemptID) int64 {
				var bytes int64
				if err := s.GetMaster().Get(ctx, &bytes, `SELECT browser_pending_bytes FROM exam_attempt_delivery_budgets WHERE exam_attempt_id=?`, attempt.String()); err != nil {
					t.Fatal(err)
				}
				return bytes
			})
		})
	}
}
