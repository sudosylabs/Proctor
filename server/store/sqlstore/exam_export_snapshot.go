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
	"slices"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// This is a versioned persistence projection, not a dump of tables. Each
// collection names its columns deliberately so a future credential/internal
// column can never silently enter the portable records format.
type examExportRecordsV1 struct {
	SchemaVersion int                       `json:"schema_version"`
	ExportID      model.ExamExportID        `json:"export_id"`
	CapturedAt    string                    `json:"captured_at"`
	ExamID        model.ExamID              `json:"exam_id"`
	SittingID     model.ExamSittingID       `json:"exam_sitting_id"`
	Categories    []model.RetentionCategory `json:"categories"`
	Submissions   []map[string]any          `json:"submissions"`
}

type examExportSnapshotBudget struct{ rows, bytes int }

func (b *examExportSnapshotBudget) collect(ctx context.Context, tx *sqlxTxWrapper, query string, args ...any) ([]json.RawMessage, error) {
	remaining := model.ExamExportMaximumRecords - b.rows
	// Every selected column is bounded by its owning record contract. A LIMIT
	// applies before JSON aggregation, so a large Sitting cannot build an
	// unbounded PostgreSQL aggregate or application slice.
	query = `SELECT row_to_json(r) FROM (` + query + ` LIMIT ?) r`
	args = append(args, remaining+1)
	var values []json.RawMessage
	if err := tx.Select(ctx, &values, query, args...); err != nil {
		return nil, err
	}
	if len(values) > remaining {
		return nil, store.NewErrConflict("exam_export", "limit", nil)
	}
	b.rows += len(values)
	if values == nil {
		values = []json.RawMessage{}
	}
	for _, v := range values {
		b.bytes += len(v)
		if b.bytes > model.ExamExportMaximumSnapshotBytes {
			return nil, store.NewErrConflict("exam_export", "limit", nil)
		}
	}
	return values, nil
}

func captureExamExportSnapshot(ctx context.Context, tx *sqlxTxWrapper, e *model.ExamExport, selected []store.ExamSubmissionAuthorization) ([]byte, error) {
	document := examExportRecordsV1{SchemaVersion: 1, ExportID: e.ID, CapturedAt: e.CreatedAt.Format("2006-01-02T15:04:05.999999999Z07:00"), ExamID: e.Scope.ExamID, SittingID: e.Scope.SittingID, Categories: slices.Clone(e.Categories), Submissions: make([]map[string]any, 0, len(selected))}
	budget := examExportSnapshotBudget{}
	entryCount := 0
	for _, a := range selected {
		var retired struct {
			Work      bool `db:"work"`
			Integrity bool `db:"integrity"`
		}
		if err := tx.Get(ctx, &retired, `SELECT work_retired_at IS NOT NULL AS work,integrity_retired_at IS NOT NULL AS integrity FROM exam_submissions WHERE id=?`, a.SubmissionID.String()); err != nil {
			return nil, err
		}
		if slices.Contains(e.Categories, model.RetentionCategoryWork) && retired.Work || slices.Contains(e.Categories, model.RetentionCategoryIntegrity) && retired.Integrity {
			return nil, store.NewErrConflict("exam_export", "source_retired", nil)
		}
		header, err := budget.collect(ctx, tx, `SELECT id AS submission_id,exam_attempt_id,exam_revision_id,provenance,submitted_at FROM exam_submissions WHERE id=?`, a.SubmissionID.String())
		if err != nil {
			return nil, err
		}
		if len(header) != 1 {
			return nil, store.NewErrNotFound("exam_export", "submission")
		}
		record := map[string]any{"submission": header[0], "candidate_user_id": a.CandidateUserID}
		if slices.Contains(e.Categories, model.RetentionCategoryWork) {
			work, err := budget.collect(ctx, tx, `SELECT manifest_schema_version,workspace_cursor,manifest_digest,manifest_entry_count,manifest_total_file_bytes FROM exam_submissions WHERE id=?`, a.SubmissionID.String())
			if err != nil {
				return nil, err
			}
			entries, err := loadExamExportEntries(ctx, tx, a.SubmissionID, model.ExamExportMaximumEntries-entryCount)
			if err != nil {
				return nil, err
			}
			entryCount += len(entries)
			portable := make([]map[string]any, 0, len(entries))
			for _, entry := range entries {
				item := map[string]any{"entry_id": entry.EntryID, "kind": entry.Kind, "path": entry.Path}
				if entry.Kind == model.StarterWorkspaceEntryFile {
					e.FileCount++
					e.SourceBytes += entry.SizeBytes
					if e.SourceBytes > model.ExamExportMaximumSourceBytes {
						return nil, store.NewErrConflict("exam_export", "limit", nil)
					}
					item["content_version"] = entry.ContentVersion
					item["media_type"] = entry.MediaType
					item["size_bytes"] = entry.SizeBytes
					item["sha256"] = entry.SHA256
					item["archive_path"] = "files/" + a.SubmissionID.String() + "/" + entry.EntryID.String()
				}
				portable = append(portable, item)
			}
			record["work"] = map[string]any{"manifest": work[0], "entries": portable}
		}
		if slices.Contains(e.Categories, model.RetentionCategoryIntegrity) {
			integrity := map[string]any{}
			queries := []struct {
				name, query string
				id          string
			}{
				{"submission", `SELECT final_focus_loss_sequence,browser_activity_state,browser_activity_source_session_id,browser_activity_final_sequence,browser_activity_gap_reason,integrity_state,unresolved_integrity_count FROM exam_submissions WHERE id=?`, a.SubmissionID.String()},
				{"flags", `SELECT id,generation,policy_kind,state,created_at FROM integrity_flags WHERE exam_attempt_id=? ORDER BY id`, a.AttemptID.String()},
				{"evidence", `SELECT id,integrity_flag_id,participation_id,generation,policy_kind,focus_loss_signal_id,sequence,duration_milliseconds,source,missing_before,observed_at,recorded_at FROM integrity_evidence WHERE exam_attempt_id=? ORDER BY id`, a.AttemptID.String()},
				{"discrepancies", `SELECT id,generation,kind,schema_version,focus_loss_signal_id,sequence,duration_milliseconds,source,missing_before,correction_revision_id,browser_activity_source_session_id,final_sequence,gap_reason,unresolved_count,received_at FROM integrity_discrepancies WHERE submission_id=? ORDER BY id`, a.SubmissionID.String()},
				{"reviews", `SELECT id,state,release_state,revision,created_by_user_id,manager_notes,student_remarks_markdown,flag_count,evidence_count,discrepancy_count,evidence_inventory_digest,created_at,updated_at,finalized_at,finalized_by_user_id,released_at,released_by_user_id FROM submission_reviews WHERE submission_id=? ORDER BY id`, a.SubmissionID.String()},
				{"decisions", `SELECT id,submission_review_id,integrity_flag_id,outcome,revision,actor_user_id,private_rationale,decided_at FROM integrity_review_decisions WHERE exam_attempt_id=? ORDER BY id`, a.AttemptID.String()},
				{"review_inventory_flags", `SELECT submission_review_id,integrity_flag_id,decision_id,decision_revision FROM submission_review_inventory_flags WHERE exam_attempt_id=? ORDER BY submission_review_id,integrity_flag_id`, a.AttemptID.String()},
				{"review_inventory_evidence", `SELECT submission_review_id,integrity_flag_id,integrity_evidence_id FROM submission_review_inventory_evidence WHERE exam_attempt_id=? ORDER BY submission_review_id,integrity_evidence_id`, a.AttemptID.String()},
				{"review_inventory_discrepancies", `SELECT submission_review_id,integrity_discrepancy_id FROM submission_review_inventory_discrepancies WHERE submission_id=? ORDER BY submission_review_id,integrity_discrepancy_id`, a.SubmissionID.String()},
				{"review_waivers", `SELECT revision,review_revision,discrepancy_count,actor_user_id,recorded_at,reason_code,private_reason FROM submission_review_waivers WHERE submission_id=?`, a.SubmissionID.String()},
				{"browser_activity", `SELECT source_session_id,sequence,participation_id,generation,policy_revision_id,kind,client_occurred_at,location_scheme,location_host,location_port,location_path,matched_rule_id,block_reason,received_at FROM browser_activity_events WHERE exam_attempt_id=? ORDER BY source_session_id,sequence`, a.AttemptID.String()},
				{"suspensions", `SELECT id,integrity_flag_id,generation,state,source,candidate_reason,started_at,ended_at,reallowed_by_user_id,private_reason FROM exam_attempt_suspensions WHERE exam_attempt_id=? ORDER BY id`, a.AttemptID.String()},
				{"manager_end_actions", `SELECT actor_user_id,private_reason,ended_at FROM exam_attempt_manager_end_actions WHERE submission_id=?`, a.SubmissionID.String()},
				{"focus_loss_accounting", `SELECT generation,accepted_sequence,unresolved_missing_count,retained_evidence_count,overflow_count,overflow_first_received_at,overflow_last_received_at,overflow_maximum_duration_milliseconds FROM exam_attempt_focus_loss_evaluations WHERE exam_attempt_id=? ORDER BY generation`, a.AttemptID.String()},
			}
			for _, q := range queries {
				values, err := budget.collect(ctx, tx, q.query, q.id)
				if err != nil {
					return nil, err
				}
				integrity[q.name] = values
			}
			record["integrity"] = integrity
		}
		document.Submissions = append(document.Submissions, record)
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, err
	}
	if len(encoded) > model.ExamExportMaximumSnapshotBytes {
		return nil, store.NewErrConflict("exam_export", "limit", nil)
	}
	// PostgreSQL's canonical JSON representation includes additional spaces.
	// Check that exact durable representation before the named insert.
	var persistedBytes int
	if err = tx.Get(ctx, &persistedBytes, `SELECT octet_length(?::jsonb::text)`, string(encoded)); err != nil {
		return nil, err
	}
	if persistedBytes > model.ExamExportMaximumSnapshotBytes {
		return nil, store.NewErrConflict("exam_export", "limit", nil)
	}
	return encoded, nil
}

func loadExamExportEntries(ctx context.Context, tx *sqlxTxWrapper, id model.SubmissionID, limit int) ([]model.ExamSubmissionManifestEntry, error) {
	var rows []examSubmissionManifestPersistenceRow
	if err := tx.Select(ctx, &rows, `SELECT entry_id,kind,path,workspace_object_id,content_version,media_type,size_bytes,sha256,storage_origin,starter_object_id,attempt_object_id FROM exam_submission_manifest_entries WHERE submission_id=? ORDER BY entry_id LIMIT ?`, id.String(), limit+1); err != nil {
		return nil, err
	}
	if len(rows) > limit {
		return nil, store.NewErrConflict("exam_export", "limit", nil)
	}
	result := make([]model.ExamSubmissionManifestEntry, 0, len(rows))
	for _, row := range rows {
		entry, err := row.model()
		if err != nil {
			return nil, err
		}
		result = append(result, entry)
	}
	return result, nil
}
