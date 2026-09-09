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
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store/storetest"
	"testing"
)

func TestDeliveryCorruptReads(t *testing.T) {
	s := openTestStore(t)
	resetTestStore(t, s)
	ctx := context.Background()
	storetest.TestDeliveryCorruptReads(t, s, func(part model.AttemptParticipationID, source model.BrowserSourceSessionID, field string) func() {
		var read, write string
		var id string
		invalid := []byte(`{}`)
		switch field {
		case "native_progress":
			var allocated int64
			if err := s.GetMaster().Get(ctx, &allocated, `SELECT allocated_through_sequence FROM exam_attempt_security_owners WHERE participation_id=?`, part.String()); err != nil {
				t.Fatal(err)
			}
			if _, err := s.GetMaster().Exec(ctx, `UPDATE exam_attempt_security_owners SET allocated_through_sequence=0 WHERE participation_id=?`, part.String()); err != nil {
				t.Fatal(err)
			}
			return func() {
				if _, err := s.GetMaster().Exec(ctx, `UPDATE exam_attempt_security_owners SET allocated_through_sequence=? WHERE participation_id=?`, allocated, part.String()); err != nil {
					t.Fatal(err)
				}
			}
		case "native_pending_transition":
			read = `SELECT record_canonical FROM exam_native_delivery_records WHERE participation_id=? AND batch_sequence=4 AND record_index=0`
			write = `UPDATE exam_native_delivery_records SET record_canonical=? WHERE participation_id=? AND batch_sequence=4 AND record_index=0`
			id = part.String()
			var raw []byte
			if err := s.GetMaster().Get(ctx, &raw, read, id); err != nil {
				t.Fatal(err)
			}
			var record model.NativeRecord
			if err := json.Unmarshal(raw, &record); err != nil {
				t.Fatal(err)
			}
			record.Occurrence.Status = "opened"
			var err error
			invalid, err = json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
		case "native_source_sequence", "native_source_id", "native_coverage_state":
			read = `SELECT latest_report_canonical FROM exam_attempt_security_owners WHERE participation_id=?`
			write = `UPDATE exam_attempt_security_owners SET latest_report_canonical=? WHERE participation_id=?`
			id = part.String()
			var raw []byte
			if err := s.GetMaster().Get(ctx, &raw, read, id); err != nil {
				t.Fatal(err)
			}
			var snapshot map[string]json.RawMessage
			if err := json.Unmarshal(raw, &snapshot); err != nil {
				t.Fatal(err)
			}
			if field == "native_coverage_state" {
				var values []model.NativeCoverageClaim
				if err := json.Unmarshal(snapshot["coverage"], &values); err != nil || len(values) == 0 {
					t.Fatal("missing coverage fixture")
				}
				values[0].State = "unknown"
				snapshot["coverage"], _ = json.Marshal(values)
			} else {
				var values []model.NativeSourceCoverage
				if err := json.Unmarshal(snapshot["sources"], &values); err != nil || len(values) == 0 {
					t.Fatal("missing source fixture")
				}
				if field == "native_source_sequence" {
					values[0].Sequence = -1
				} else {
					values[0].SourceID = "unknown"
				}
				snapshot["sources"], _ = json.Marshal(values)
			}
			var err error
			invalid, err = json.Marshal(snapshot)
			if err != nil {
				t.Fatal(err)
			}
		case "native_binding":
			read = `SELECT binding_canonical FROM exam_attempt_security_owners WHERE participation_id=?`
			write = `UPDATE exam_attempt_security_owners SET binding_canonical=? WHERE participation_id=?`
			id = part.String()
		case "native_summary":
			read = `SELECT summary_canonical FROM exam_attempt_security_owners WHERE participation_id=?`
			write = `UPDATE exam_attempt_security_owners SET summary_canonical=? WHERE participation_id=?`
			id = part.String()
		case "browser_predecessor_malformed", "browser_predecessor_incomplete":
			read = `SELECT closure_canonical FROM browser_activity_sources WHERE id=?::uuid`
			write = `UPDATE browser_activity_sources SET closure_canonical=? WHERE id=?::uuid`
			id = string(source)
			invalid = []byte(`{"close_reason":"policy_correction"}`)
			if field == "browser_predecessor_malformed" {
				invalid = []byte(`{`)
			}
		case "browser_closure":
			read = `SELECT closure_canonical FROM browser_activity_sources WHERE id=?::uuid`
			write = `UPDATE browser_activity_sources SET closure_canonical=? WHERE id=?::uuid`
			id = string(source)
			invalid = []byte(`{"closed_at":"2026-09-09T12:00:00Z"}`)
		case "browser_pending_record":
			read = `SELECT record_canonical FROM browser_activity_events WHERE source_session_id=?::uuid AND sequence=3`
			write = `UPDATE browser_activity_events SET record_canonical=? WHERE source_session_id=?::uuid AND sequence=3`
			id = string(source)
		case "browser_receipt":
			read = `SELECT receipt_canonical FROM browser_activity_events WHERE source_session_id=?::uuid AND sequence=1`
			write = `UPDATE browser_activity_events SET receipt_canonical=? WHERE source_session_id=?::uuid AND sequence=1`
			id = string(source)
		default:
			t.Fatal("unknown corruption field")
		}
		var original []byte
		if err := s.GetMaster().Get(ctx, &original, read, id); err != nil {
			t.Fatal(err)
		}
		if _, err := s.GetMaster().Exec(ctx, write, invalid, id); err != nil {
			t.Fatal(err)
		}
		return func() {
			if _, err := s.GetMaster().Exec(ctx, write, original, id); err != nil {
				t.Fatal(err)
			}
		}
	})
}
