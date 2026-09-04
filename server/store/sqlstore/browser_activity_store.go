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
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func (s *sqlExamAttemptStore) StartBrowserActivity(ctx context.Context, input *store.BrowserActivitySourceStart) (*model.BrowserActivityAcknowledgement, error) {
	if input == nil || !input.ParticipationID.IsValid() || input.Generation < 1 || !input.SourceSessionID.IsValid() ||
		(input.PredecessorSessionID.IsValid() != input.ResetReason.IsValid()) {
		return nil, store.NewErrInvalidInput("browser_activity", "source_start", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "start Browser Activity source", func(ctx context.Context, tx *sqlxTxWrapper) (*model.BrowserActivityAcknowledgement, error) {
		guard, err := s.lockCandidateGuard(ctx, tx, input.Access)
		if err != nil {
			return nil, err
		}
		if guard.SittingState != string(model.ExamSittingOpen) {
			return nil, store.NewErrConflict("exam_sitting", "exam_sitting_state", nil)
		}
		pendingAcknowledgement, err := hasPendingCandidateCorrectionAcknowledgement(ctx, tx, guard.AttemptID,
			guard.SittingID, guard.AdmissionRevisionID, guard.RevisionID)
		if err != nil {
			return nil, err
		}
		if pendingAcknowledgement {
			return nil, store.NewErrConflict("browser_activity", "exam_correction_acknowledgement_required", nil)
		}
		var participationCount int
		if err = tx.Get(ctx, &participationCount, `SELECT COUNT(*) FROM exam_attempt_participations p
			JOIN exam_attempt_connections c ON c.participation_id=p.id AND c.exam_attempt_id=p.exam_attempt_id
			WHERE p.id=? AND p.exam_attempt_id=? AND p.generation=? AND p.state='active' AND c.id=? AND c.state='open'
			AND p.session_id=? AND c.session_id=?`, input.ParticipationID.String(), guard.AttemptID, input.Generation,
			input.Access.ConnectionID.String(), input.Access.SessionID.String(), input.Access.SessionID.String()); err != nil {
			return nil, fmt.Errorf("validate Browser Activity Participation: %w", err)
		}
		if participationCount != 1 {
			return nil, store.NewErrConflict("browser_activity", "attempt_participation_generation", nil)
		}
		gapBefore, err := browserActivityGapAttention(ctx, tx, guard.AttemptID)
		if err != nil {
			return nil, err
		}
		var enabled bool
		if err = tx.Get(ctx, &enabled, `SELECT (browser_policy_document->>'enabled')::boolean FROM exam_revisions
			WHERE id=? AND exam_id=(SELECT exam_id FROM exam_attempts WHERE id=?) AND sealed=true`, guard.RevisionID, guard.AttemptID); err != nil {
			return nil, translateError("exam_revision", guard.RevisionID, err)
		}
		if !enabled {
			return nil, store.NewErrConflict("browser_activity", "browser_policy_disabled", nil)
		}
		var existing struct {
			ID                string    `db:"id"`
			HighestContiguous int64     `db:"highest_contiguous"`
			HighestSeen       int64     `db:"highest_seen"`
			StartedAt         time.Time `db:"started_at"`
		}
		err = tx.Get(ctx, &existing, `SELECT id::text,highest_contiguous,highest_seen,started_at FROM browser_activity_sources
			WHERE participation_id=? AND state='current' FOR UPDATE`, input.ParticipationID.String())
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("lock current Browser Activity source: %w", err)
		}
		currentExists := err == nil
		var databaseNow time.Time
		if err = tx.Get(ctx, &databaseNow, `SELECT statement_timestamp()`); err != nil {
			return nil, err
		}
		databaseNow = model.TimeUTC(databaseNow)
		if currentExists && existing.ID == string(input.SourceSessionID) {
			acknowledgement, acknowledgementErr := browserActivityAcknowledgement(ctx, tx, input.SourceSessionID,
				existing.HighestContiguous, existing.HighestSeen, databaseNow)
			return browserActivityResult(acknowledgement, guard.ExamID, guard.SittingID, false, acknowledgementErr)
		}
		if currentExists {
			if string(input.PredecessorSessionID) != existing.ID || !input.ResetReason.IsValid() {
				return nil, store.NewErrConflict("browser_activity", "browser_source_current", nil)
			}
			result, updateErr := tx.Exec(ctx, `UPDATE browser_activity_sources SET state='gapped',ended_at=? WHERE id=?::uuid AND state='current'`, databaseNow, existing.ID)
			if updateErr != nil {
				return nil, updateErr
			}
			if affected, affectedErr := result.RowsAffected(); affectedErr != nil || affected != 1 {
				return nil, store.NewErrConflict("browser_activity", "browser_source_current", affectedErr)
			}
		} else if input.PredecessorSessionID.IsValid() || input.ResetReason.IsValid() {
			return nil, store.NewErrConflict("browser_activity", "browser_source_predecessor", nil)
		}
		var sourceCount int
		if err = tx.Get(ctx, &sourceCount, `SELECT COUNT(*) FROM browser_activity_sources WHERE participation_id=?`, input.ParticipationID.String()); err != nil {
			return nil, err
		}
		if sourceCount >= model.BrowserSourceMaximumPerParticipation {
			return nil, store.NewErrConflict("browser_activity", "browser_source_limit", nil)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO browser_activity_sources
			(id,exam_id,exam_sitting_id,exam_attempt_id,participation_id,generation,session_id,connection_id,predecessor_id,reset_reason,state,started_at)
			VALUES (?::uuid,?,?,?,?,?,?,?,?::uuid,?,'current',?)`, string(input.SourceSessionID), guard.ExamID, guard.SittingID, guard.AttemptID,
			input.ParticipationID.String(), input.Generation, input.Access.SessionID.String(), input.Access.ConnectionID.String(),
			nullableString(string(input.PredecessorSessionID)), nullableString(string(input.ResetReason)), databaseNow); err != nil {
			return nil, fmt.Errorf("insert Browser Activity source: %w", translateError("browser_activity_source", string(input.SourceSessionID), err))
		}
		gapAfter, err := browserActivityGapAttention(ctx, tx, guard.AttemptID)
		if err != nil {
			return nil, err
		}
		return browserActivityResult(&model.BrowserActivityAcknowledgement{SourceSessionID: input.SourceSessionID,
			MissingRanges: []model.BrowserActivityMissingRange{}, ServerTime: databaseNow}, guard.ExamID, guard.SittingID,
			gapBefore != gapAfter, nil)
	})
}

func (s *sqlExamAttemptStore) AppendBrowserActivity(ctx context.Context, input *store.BrowserActivityAppend) (*model.BrowserActivityAcknowledgement, error) {
	if input == nil || !input.ParticipationID.IsValid() || input.Generation < 1 || !input.SourceSessionID.IsValid() ||
		len(input.Events) < 1 || len(input.Events) > model.BrowserActivityAppendMaximumEvents {
		return nil, store.NewErrInvalidInput("browser_activity", "append", nil)
	}
	if encoded, err := json.Marshal(input.Events); err != nil || len(encoded) > model.BrowserActivityAppendMaximumBytes {
		return nil, store.NewErrInvalidInput("browser_activity", "batch_size", nil)
	}
	fingerprints := make([]string, len(input.Events))
	for index, event := range input.Events {
		if event.ValidateClientRecord() != nil || index > 0 && event.Sequence <= input.Events[index-1].Sequence {
			return nil, store.NewErrInvalidInput("browser_activity", "events", nil)
		}
		fingerprint, err := event.Fingerprint()
		if err != nil {
			return nil, store.NewErrInvalidInput("browser_activity", "event", nil).Wrap(err)
		}
		fingerprints[index] = fingerprint
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "append Browser Activity", func(ctx context.Context, tx *sqlxTxWrapper) (*model.BrowserActivityAcknowledgement, error) {
		guard, err := s.lockCandidateGuard(ctx, tx, input.Access)
		if err != nil {
			return nil, err
		}
		if guard.SittingState != string(model.ExamSittingOpen) && guard.SittingState != string(model.ExamSittingPaused) {
			return nil, store.NewErrConflict("exam_sitting", "exam_sitting_state", nil)
		}
		var source struct {
			ExamID            string `db:"exam_id"`
			SittingID         string `db:"exam_sitting_id"`
			AttemptID         string `db:"exam_attempt_id"`
			ParticipationID   string `db:"participation_id"`
			Generation        int64  `db:"generation"`
			SessionID         string `db:"session_id"`
			ConnectionID      string `db:"connection_id"`
			State             string `db:"state"`
			HighestContiguous int64  `db:"highest_contiguous"`
			HighestSeen       int64  `db:"highest_seen"`
		}
		if err = tx.Get(ctx, &source, `SELECT exam_id,exam_sitting_id,exam_attempt_id,participation_id,generation,session_id,connection_id,state,highest_contiguous,highest_seen
			FROM browser_activity_sources WHERE id=?::uuid FOR UPDATE`, string(input.SourceSessionID)); err != nil {
			return nil, translateError("browser_activity_source", string(input.SourceSessionID), err)
		}
		if source.AttemptID != guard.AttemptID || source.SittingID != guard.SittingID || source.ParticipationID != input.ParticipationID.String() ||
			source.Generation != input.Generation || source.SessionID != input.Access.SessionID.String() || source.ConnectionID != input.Access.ConnectionID.String() || source.State != "current" {
			return nil, store.NewErrConflict("browser_activity", "browser_source_fence", nil)
		}
		gapBefore, err := browserActivityGapAttention(ctx, tx, guard.AttemptID)
		if err != nil {
			return nil, err
		}
		if input.Events[len(input.Events)-1].Sequence > source.HighestContiguous+model.BrowserActivityMaximumReorderWindow {
			return nil, store.NewErrConflict("browser_activity", "browser_activity_reorder_window", nil)
		}
		var databaseNow time.Time
		if err = tx.Get(ctx, &databaseNow, `SELECT statement_timestamp()`); err != nil {
			return nil, err
		}
		databaseNow = model.TimeUTC(databaseNow)
		policies := make(map[model.ExamRevisionID]model.BrowserPolicy)
		for index, event := range input.Events {
			policy, exists := policies[event.PolicyRevisionID]
			if !exists {
				policy, err = applicableBrowserActivityPolicy(ctx, tx, source.AttemptID, source.SittingID,
					event.PolicyRevisionID)
				if err != nil {
					return nil, err
				}
				policies[event.PolicyRevisionID] = policy
			}
			if err = validateBrowserActivityPolicyClaim(event, policy); err != nil {
				return nil, store.NewErrConflict("browser_activity", "browser_activity_policy_semantics", err)
			}
			var scheme, host, port, eventPath any
			if event.Location != nil {
				scheme, host, eventPath = event.Location.Scheme, event.Location.Host, event.Location.Path
				port = nullableString(event.Location.Port)
			}
			var matchedRule, blockReason any
			if event.MatchedRuleID != nil {
				matchedRule = *event.MatchedRuleID
			}
			if event.BlockReason != nil {
				blockReason = string(*event.BlockReason)
			}
			result, insertErr := tx.Exec(ctx, `INSERT INTO browser_activity_events
				(source_session_id,sequence,exam_id,exam_sitting_id,exam_attempt_id,participation_id,generation,policy_revision_id,kind,
				client_occurred_at,location_scheme,location_host,location_port,location_path,matched_rule_id,block_reason,event_fingerprint,received_at)
				VALUES (?::uuid,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT (source_session_id,sequence) DO NOTHING`,
				string(input.SourceSessionID), event.Sequence, source.ExamID, source.SittingID, source.AttemptID, source.ParticipationID, source.Generation,
				event.PolicyRevisionID.String(), string(event.Kind), model.TimeUTC(event.ClientOccurredAt), scheme, host, port, eventPath, matchedRule, blockReason,
				fingerprints[index], databaseNow)
			if insertErr != nil {
				return nil, fmt.Errorf("insert Browser Activity event: %w", insertErr)
			}
			affected, affectedErr := result.RowsAffected()
			if affectedErr != nil {
				return nil, affectedErr
			}
			if affected == 0 {
				var retained string
				if err = tx.Get(ctx, &retained, `SELECT event_fingerprint FROM browser_activity_events WHERE source_session_id=?::uuid AND sequence=?`, string(input.SourceSessionID), event.Sequence); err != nil {
					return nil, err
				}
				if retained != fingerprints[index] {
					return nil, store.NewErrConflict("browser_activity", "browser_activity_sequence", nil)
				}
			}
			source.HighestSeen = max(source.HighestSeen, event.Sequence)
		}
		var sequences []int64
		if err = tx.Select(ctx, &sequences, `SELECT sequence FROM browser_activity_events WHERE source_session_id=?::uuid AND sequence>? AND sequence<=? ORDER BY sequence`,
			string(input.SourceSessionID), source.HighestContiguous, source.HighestSeen); err != nil {
			return nil, err
		}
		highestContiguous := source.HighestContiguous
		for _, sequence := range sequences {
			if sequence != highestContiguous+1 {
				break
			}
			highestContiguous = sequence
		}
		if _, err = tx.Exec(ctx, `UPDATE browser_activity_sources SET highest_contiguous=?,highest_seen=? WHERE id=?::uuid AND state='current'`,
			highestContiguous, source.HighestSeen, string(input.SourceSessionID)); err != nil {
			return nil, err
		}
		gapAfter, err := browserActivityGapAttention(ctx, tx, guard.AttemptID)
		if err != nil {
			return nil, err
		}
		acknowledgement, err := browserActivityAcknowledgement(ctx, tx, input.SourceSessionID, highestContiguous,
			source.HighestSeen, databaseNow)
		return browserActivityResult(acknowledgement, source.ExamID, source.SittingID, gapBefore != gapAfter, err)
	})
}

func applicableBrowserActivityPolicy(ctx context.Context, executor sqlxExecutor, attemptID, sittingID string,
	revisionID model.ExamRevisionID,
) (model.BrowserPolicy, error) {
	var document []byte
	err := executor.Get(ctx, &document, `SELECT revision.browser_policy_document FROM exam_revisions revision
		JOIN exam_attempts attempt ON attempt.id=? AND attempt.exam_id=revision.exam_id AND attempt.exam_sitting_id=?
		JOIN exam_sittings sitting ON sitting.id=attempt.exam_sitting_id AND sitting.exam_id=attempt.exam_id
		JOIN exam_revisions admission ON admission.id=attempt.admission_revision_id AND admission.exam_id=attempt.exam_id
		JOIN exam_revisions current_revision ON current_revision.id=sitting.exam_revision_id AND current_revision.exam_id=attempt.exam_id
		WHERE revision.id=? AND revision.sealed=true AND revision.number>=admission.number
		AND revision.number<=current_revision.number AND (revision.id=attempt.admission_revision_id OR EXISTS (
			SELECT 1 FROM exam_sitting_live_corrections correction WHERE correction.exam_sitting_id=sitting.id
			AND correction.correction_revision_id=revision.id))`, attemptID, sittingID, revisionID.String())
	if errors.Is(err, sql.ErrNoRows) {
		return model.BrowserPolicy{}, store.NewErrConflict("browser_activity", "browser_policy_revision", nil)
	}
	if err != nil {
		return model.BrowserPolicy{}, fmt.Errorf("resolve applicable Browser Policy: %w", err)
	}
	policy, err := model.ParseBrowserPolicyDocument(document)
	if err != nil || !policy.Enabled {
		return model.BrowserPolicy{}, store.NewErrConflict("browser_activity", "browser_policy_revision", err)
	}
	return policy, nil
}

func validateBrowserActivityPolicyClaim(event model.BrowserActivityEvent, policy model.BrowserPolicy) error {
	if event.Kind == model.BrowserActivityOpened || event.Kind == model.BrowserActivityClosed {
		return nil
	}
	if event.Location == nil {
		return errors.New("Browser Activity location is missing")
	}
	if event.Kind == model.BrowserActivityBlockedNavigation {
		if event.MatchedRuleID == nil {
			return nil
		}
		for _, rule := range policy.Rules {
			if rule.RuleID == *event.MatchedRuleID {
				return nil
			}
		}
		return errors.New("blocked Browser Activity rule does not exist")
	}
	host := event.Location.Host
	if event.Location.Port != "" {
		host = net.JoinHostPort(host, event.Location.Port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	rule, location, err := policy.Match("https://" + host + event.Location.Path)
	if err != nil || rule == nil || event.MatchedRuleID == nil || rule.RuleID != *event.MatchedRuleID ||
		location != *event.Location {
		return errors.New("successful Browser Activity claim does not match its policy")
	}
	return nil
}

func browserActivityGapAttention(ctx context.Context, executor sqlxExecutor, attemptID string) (bool, error) {
	var attention bool
	err := executor.Get(ctx, &attention, `SELECT EXISTS (SELECT 1 FROM browser_activity_sources
		WHERE exam_attempt_id=? AND (state='gapped' OR (state='current' AND highest_contiguous<highest_seen)))`, attemptID)
	if err != nil {
		return false, fmt.Errorf("inspect Browser Activity gap attention: %w", err)
	}
	return attention, nil
}

func browserActivityResult(acknowledgement *model.BrowserActivityAcknowledgement, examID, sittingID string,
	gapAttentionChanged bool, err error,
) (*model.BrowserActivityAcknowledgement, error) {
	if err != nil {
		return nil, err
	}
	parsedExamID, err := model.ParseExamID(examID)
	if err != nil {
		return nil, invalidPersistedState("browser_activity_source", "exam_id", err)
	}
	parsedSittingID, err := model.ParseExamSittingID(sittingID)
	if err != nil {
		return nil, invalidPersistedState("browser_activity_source", "exam_sitting_id", err)
	}
	acknowledgement.ExamID = parsedExamID
	acknowledgement.SittingID = parsedSittingID
	acknowledgement.GapAttentionChanged = gapAttentionChanged
	return acknowledgement, nil
}

func browserActivityAcknowledgement(ctx context.Context, tx *sqlxTxWrapper, sourceID model.BrowserSourceSessionID,
	highestContiguous, highestSeen int64, serverTime time.Time,
) (*model.BrowserActivityAcknowledgement, error) {
	var present []int64
	if highestSeen > highestContiguous {
		if err := tx.Select(ctx, &present, `SELECT sequence FROM browser_activity_events WHERE source_session_id=?::uuid AND sequence>? AND sequence<=? ORDER BY sequence`,
			string(sourceID), highestContiguous, highestSeen); err != nil {
			return nil, err
		}
	}
	presentSet := make(map[int64]struct{}, len(present))
	for _, sequence := range present {
		presentSet[sequence] = struct{}{}
	}
	ranges := make([]model.BrowserActivityMissingRange, 0, model.BrowserActivityMaximumMissingRanges)
	truncated := false
	for sequence := highestContiguous + 1; sequence <= highestSeen; {
		if _, exists := presentSet[sequence]; exists {
			sequence++
			continue
		}
		first := sequence
		for sequence <= highestSeen {
			if _, exists := presentSet[sequence]; exists {
				break
			}
			sequence++
		}
		if len(ranges) == model.BrowserActivityMaximumMissingRanges {
			truncated = true
			break
		}
		ranges = append(ranges, model.BrowserActivityMissingRange{First: first, Last: sequence - 1})
	}
	return &model.BrowserActivityAcknowledgement{SourceSessionID: sourceID, HighestContiguous: highestContiguous, HighestSeen: highestSeen,
		MissingRanges: ranges, MissingRangesTruncated: truncated, ServerTime: model.TimeUTC(serverTime)}, nil
}

func (s *sqlExamAttemptStore) ListBrowserActivity(ctx context.Context, options store.BrowserActivityListOptions) ([]store.BrowserActivityRecord, error) {
	if !options.ExamID.IsValid() || !options.SittingID.IsValid() || !options.AttemptID.IsValid() || options.Limit < 1 || options.Limit > 201 ||
		(options.AfterReceivedAt.IsZero() != !options.AfterSourceID.IsValid()) ||
		(!options.AfterReceivedAt.IsZero() && options.AfterSequence < 1) {
		return nil, store.NewErrInvalidInput("browser_activity", "list", nil)
	}
	query := `SELECT e.source_session_id::text,e.sequence,e.exam_attempt_id,e.participation_id,e.generation,e.policy_revision_id,e.kind,
		e.client_occurred_at,e.location_scheme,e.location_host,e.location_port,e.location_path,e.matched_rule_id,e.block_reason,e.received_at
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
	SourceSessionID string         `db:"source_session_id"`
	Sequence        int64          `db:"sequence"`
	AttemptID       string         `db:"exam_attempt_id"`
	ParticipationID string         `db:"participation_id"`
	Generation      int64          `db:"generation"`
	PolicyRevision  string         `db:"policy_revision_id"`
	Kind            string         `db:"kind"`
	ClientOccurred  time.Time      `db:"client_occurred_at"`
	LocationScheme  sql.NullString `db:"location_scheme"`
	LocationHost    sql.NullString `db:"location_host"`
	LocationPort    sql.NullString `db:"location_port"`
	LocationPath    sql.NullString `db:"location_path"`
	MatchedRuleID   sql.NullString `db:"matched_rule_id"`
	BlockReason     sql.NullString `db:"block_reason"`
	ReceivedAt      time.Time      `db:"received_at"`
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
	client := event
	client.ReceivedAt = time.Time{}
	if err = client.ValidateClientRecord(); err != nil {
		return store.BrowserActivityRecord{}, invalidPersistedState("browser_activity", "event", err)
	}
	return store.BrowserActivityRecord{AttemptID: attemptID, ParticipationID: participationID, Generation: row.Generation,
		SourceSessionID: model.BrowserSourceSessionID(row.SourceSessionID), Event: event}, nil
}
