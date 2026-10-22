// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// ListBrowserActivity returns one page of the Attempt's Browser Activity events
// ordered by receipt time, source, and sequence, continuing after the optional
// keyset cursor. Limit must be 1..201, and the cursor fields must be set
// together.
func (s *SQLExamAttemptStore) ListBrowserActivity(ctx context.Context, options store.BrowserActivityListOptions) ([]store.BrowserActivityRecord, error) {
	if !options.ExamID.IsValid() || !options.SittingID.IsValid() || !options.AttemptID.IsValid() || options.Limit < 1 || options.Limit > 201 ||
		(options.AfterReceivedAt.IsZero() != !options.AfterSourceID.IsValid()) ||
		(!options.AfterReceivedAt.IsZero() && options.AfterSequence < 1) {
		return nil, store.NewErrInvalidInput("browser_activity", "list", nil)
	}
	query := `SELECT e.source_session_id::text,e.sequence,e.exam_attempt_id,e.participation_id,e.generation,e.policy_revision_id,e.kind,
		e.client_occurred_at,e.location_scheme,e.location_host,e.location_port,e.location_path,e.matched_rule_id,e.block_reason,e.redirect_from_sequence,e.received_at
		FROM browser_activity_events e WHERE e.exam_id=? AND e.exam_sitting_id=? AND e.exam_attempt_id=?`
	args := []any{options.ExamID.String(), options.SittingID.String(), options.AttemptID.String()}
	if !options.AfterReceivedAt.IsZero() {
		query += ` AND (e.received_at,e.source_session_id,e.sequence)>(?,?::uuid,?)`
		args = append(args, model.TimeUTC(options.AfterReceivedAt), string(options.AfterSourceID), options.AfterSequence)
	}
	query += ` ORDER BY e.received_at,e.source_session_id,e.sequence LIMIT ?`
	args = append(args, options.Limit)
	var rows []browserActivityRecordRow
	if err := s.GetMaster().Select(ctx, &rows, query, args...); err != nil {
		return nil, fmt.Errorf("list Browser Activity: %w", err)
	}
	result := make([]store.BrowserActivityRecord, len(rows))
	for index, row := range rows {
		record, err := row.record()
		if err != nil {
			return nil, err
		}
		result[index] = record
	}
	return result, nil
}

type browserActivityRecordRow struct {
	RedirectFromSequence sql.NullInt64  `db:"redirect_from_sequence"`
	SourceSessionID      string         `db:"source_session_id"`
	Sequence             int64          `db:"sequence"`
	AttemptID            string         `db:"exam_attempt_id"`
	ParticipationID      string         `db:"participation_id"`
	Generation           int64          `db:"generation"`
	PolicyRevision       string         `db:"policy_revision_id"`
	Kind                 string         `db:"kind"`
	ClientOccurred       time.Time      `db:"client_occurred_at"`
	LocationScheme       sql.NullString `db:"location_scheme"`
	LocationHost         sql.NullString `db:"location_host"`
	LocationPort         sql.NullString `db:"location_port"`
	LocationPath         sql.NullString `db:"location_path"`
	MatchedRuleID        sql.NullString `db:"matched_rule_id"`
	BlockReason          sql.NullString `db:"block_reason"`
	ReceivedAt           time.Time      `db:"received_at"`
}

func (row browserActivityRecordRow) record() (store.BrowserActivityRecord, error) {
	attemptID, err := model.ParseExamAttemptID(row.AttemptID)
	if err != nil {
		return store.BrowserActivityRecord{}, invalidPersistedState("browser_activity", "exam_attempt_id", err)
	}
	participationID, err := model.ParseAttemptParticipationID(row.ParticipationID)
	if err != nil {
		return store.BrowserActivityRecord{}, invalidPersistedState("browser_activity", "participation_id", err)
	}
	revisionID, err := model.ParseExamRevisionID(row.PolicyRevision)
	if err != nil {
		return store.BrowserActivityRecord{}, invalidPersistedState("browser_activity", "policy_revision_id", err)
	}
	event := model.BrowserActivityEvent{Sequence: row.Sequence, Kind: model.BrowserActivityKind(row.Kind), PolicyRevisionID: revisionID,
		ClientOccurredAt: model.TimeUTC(row.ClientOccurred), ReceivedAt: model.TimeUTC(row.ReceivedAt)}
	if row.LocationScheme.Valid {
		event.Location = &model.BrowserLocation{Scheme: row.LocationScheme.String, Host: row.LocationHost.String, Port: row.LocationPort.String, Path: row.LocationPath.String}
	}
	if row.MatchedRuleID.Valid {
		value := row.MatchedRuleID.String
		event.MatchedRuleID = &value
	}
	if row.BlockReason.Valid {
		value := model.BrowserActivityBlockReason(row.BlockReason.String)
		event.BlockReason = &value
	}
	if row.RedirectFromSequence.Valid {
		sequence := row.RedirectFromSequence.Int64
		event.RedirectFromSequence = &sequence
	}
	client := event
	client.ReceivedAt = time.Time{}
	if err = client.ValidateClientRecord(); err != nil {
		return store.BrowserActivityRecord{}, invalidPersistedState("browser_activity", "event", err)
	}
	return store.BrowserActivityRecord{AttemptID: attemptID, ParticipationID: participationID, Generation: row.Generation,
		SourceSessionID: model.BrowserSourceSessionID(row.SourceSessionID), Event: event}, nil
}
