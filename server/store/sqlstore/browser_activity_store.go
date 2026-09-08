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
	"errors"
	"fmt"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

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
	rule, err := policy.MatchLocation(*event.Location)
	if err != nil || rule == nil || event.MatchedRuleID == nil || rule.RuleID != *event.MatchedRuleID {
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
