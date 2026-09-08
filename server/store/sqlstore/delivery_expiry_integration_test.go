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
	"encoding/json"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store/storetest"
)

func TestDeliveryExpiryStore(t *testing.T) {
	s := openTestStore(t)
	resetTestStore(t, s)
	storetest.TestDeliveryExpiryStore(t, s, func(t *testing.T, ctx context.Context, attempt model.ExamAttemptID) {
		for _, table := range []string{"exam_attempt_security_owners", "browser_activity_sources"} {
			var rows []struct {
				Part string `db:"part"`
				Raw  []byte `db:"closure_canonical"`
			}
			selector := "participation_id"
			if table == "browser_activity_sources" {
				selector = "id::text"
			}
			if err := s.GetMaster().Select(ctx, &rows, `SELECT `+selector+` AS part,closure_canonical FROM `+table+` WHERE exam_attempt_id=?`, attempt.String()); err != nil {
				t.Fatal(err)
			}
			for _, row := range rows {
				var closure model.DeliveryClosure
				if err := json.Unmarshal(row.Raw, &closure); err != nil {
					t.Fatal(err)
				}
				*closure.ClosedAt = closure.ClosedAt.Add(-25 * time.Hour)
				*closure.UploadExpiresAt = closure.UploadExpiresAt.Add(-25 * time.Hour)
				raw, err := canonicalPreflightValue(closure)
				if err != nil {
					t.Fatal(err)
				}
				query := `UPDATE exam_attempt_security_owners SET closure_canonical=?,upload_expires_at=? WHERE participation_id=?`
				if table == "browser_activity_sources" {
					query = `UPDATE browser_activity_sources SET closure_canonical=?,upload_expires_at=?,started_at=started_at-interval '25 hours',ended_at=ended_at-interval '25 hours' WHERE id=?::uuid`
				}
				if _, err := s.GetMaster().Exec(ctx, query, raw, closure.UploadExpiresAt, row.Part); err != nil {
					t.Fatal(err)
				}
			}
		}
	})
}

func TestDeliveryBudgetStore(t *testing.T) {
	s := openTestStore(t)
	resetTestStore(t, s)
	storetest.TestDeliveryBudgetStore(t, s)
}

func TestBrowserIntegrityStore(t *testing.T) {
	s := openTestStore(t)
	resetTestStore(t, s)
	storetest.TestBrowserIntegrityStore(t, s)
}

func TestBrowserReviewWaiverStore(t *testing.T) {
	s := openTestStore(t)
	resetTestStore(t, s)
	storetest.TestBrowserReviewWaiverStore(t, s)
}

func TestBrowserSecurityWatermarks(t *testing.T) {
	s := openTestStore(t)
	resetTestStore(t, s)
	storetest.TestBrowserSecurityWatermarks(t, s)
}

func TestBrowserIntegrityGroupCapacity(t *testing.T) {
	s := openTestStore(t)
	resetTestStore(t, s)
	storetest.TestBrowserIntegrityGroupCapacity(t, s)
}

func TestBrowserIntegrityCopyBoundaries(t *testing.T) {
	s := openTestStore(t)
	resetTestStore(t, s)
	storetest.TestBrowserIntegrityCopyBoundaries(t, s, func(t *testing.T, ctx context.Context, attempt model.ExamAttemptID, records, bytes int64) {
		if _, err := s.GetMaster().Exec(ctx, `UPDATE exam_attempt_delivery_budgets SET browser_evidence_records=?,browser_evidence_bytes=? WHERE exam_attempt_id=?`, records, bytes, attempt.String()); err != nil {
			t.Fatal(err)
		}
	})
}
