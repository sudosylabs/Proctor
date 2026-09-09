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
	"slices"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type browserEventMetadata struct {
	Interpretation int `json:"interpretation"`
}
type browserAppendRecord struct {
	Event                  model.BrowserActivityEvent
	Raw, Receipt, Metadata []byte
	Fingerprint            string
	Bytes                  int64
}
type browserAppendResult struct {
	Acknowledgement *model.BrowserActivityAcknowledgement
	Refusal         string
}

func (s *sqlExamAttemptStore) debitBrowserAppend(ctx context.Context, access store.BrowserDeliveryAccess) error {
	allowed, err := runSQLTransaction(ctx, s.GetMaster().Begin, "browser append allowance", func(ctx context.Context, tx *sqlxTxWrapper) (bool, error) {
		if err := s.lockBrowserDelivery(ctx, tx, access); err != nil {
			return false, err
		}
		var rate struct {
			Tokens int64        `db:"browser_rate_tokens"`
			At     sql.NullTime `db:"browser_rate_at"`
			Now    time.Time    `db:"server_time"`
		}
		if err := tx.Get(ctx, &rate, `SELECT browser_rate_tokens,browser_rate_at,clock_timestamp() AS server_time FROM exam_attempt_delivery_budgets WHERE exam_attempt_id=? FOR UPDATE`, access.Access.AttemptID.String()); err != nil {
			return false, err
		}
		if rate.At.Valid {
			rate.Tokens = min(int64(8000), rate.Tokens+min(int64(4000), max(int64(0), rate.Now.Sub(rate.At.Time).Milliseconds()))*2)
		}
		allowed := rate.Tokens >= 1000
		if allowed {
			rate.Tokens -= 1000
		}
		_, err := tx.Exec(ctx, `UPDATE exam_attempt_delivery_budgets SET browser_rate_tokens=?,browser_rate_at=? WHERE exam_attempt_id=?`, rate.Tokens, rate.Now, access.Access.AttemptID.String())
		return allowed, err
	})
	if err != nil {
		return err
	}
	if !allowed {
		return store.NewErrConflict("browser_delivery", "append_rate_limited", nil)
	}
	return nil
}

func (s *sqlExamAttemptStore) AppendBrowserActivity(ctx context.Context, input *store.BrowserActivityAppend) (*model.BrowserActivityAcknowledgement, error) {
	return s.appendBrowserActivity(ctx, input, nil)
}
func (s *sqlExamAttemptStore) AppendHistoricalBrowserDelivery(ctx context.Context, input *store.BrowserActivityAppend, command *store.CommandIdempotency) (*model.BrowserActivityAcknowledgement, error) {
	if input == nil || command == nil || command.Operation != store.BrowserDeliveryAppendOperation || command.UserID != input.Access.CandidateUserID || input.Access.ConnectionID != "" || input.Access.ContinuityCredentialHash != "" {
		return nil, store.NewErrInvalidInput("browser_delivery", "historical_append", nil)
	}
	return s.appendBrowserActivity(ctx, input, command)
}
func (s *sqlExamAttemptStore) appendBrowserActivity(ctx context.Context, input *store.BrowserActivityAppend, command *store.CommandIdempotency) (*model.BrowserActivityAcknowledgement, error) {
	if input == nil || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("browser_activity", "append", nil)
	}
	batch := model.BrowserActivityBatch{SourceSessionID: input.SourceSessionID, ParticipationID: input.ParticipationID, Generation: input.Generation, PolicyRevisionID: input.PolicyRevisionID, PolicyDigest: input.PolicyDigest, Events: input.Events}
	if batch.Validate() != nil {
		return nil, store.NewErrInvalidInput("browser_activity", "append", nil)
	}
	release, admissionErr := s.enterDeliveryAppend(ctx, "browser")
	if admissionErr != nil {
		return nil, admissionErr
	}
	defer release()
	access := store.BrowserDeliveryAccess{Access: input.Access, SourceSessionID: input.SourceSessionID, ParticipationID: input.ParticipationID}
	if err := s.debitBrowserAppend(ctx, access); err != nil {
		return nil, err
	}
	perform := func(ctx context.Context, tx *sqlxTxWrapper, audit bool) (browserAppendResult, error) {
		if err := s.lockBrowserDelivery(ctx, tx, access); err != nil {
			return browserAppendResult{}, err
		}
		status, err := browserSourceStatus(ctx, tx, input.SourceSessionID)
		if err != nil {
			return browserAppendResult{}, err
		}
		if status.PolicyRevisionID != input.PolicyRevisionID || status.PolicyDigest != input.PolicyDigest || status.Generation != input.Generation {
			return browserAppendResult{}, store.NewErrConflict("browser_activity", "browser_source_fence", nil)
		}
		if (status.Closure.ClosedAt != nil) != (command != nil) {
			return browserAppendResult{}, store.NewErrConflict("browser_activity", "browser_source_fence", nil)
		}
		if status.Closure.UploadExpiresAt != nil && !status.ServerTime.Before(*status.Closure.UploadExpiresAt) {
			return browserAppendResult{}, model.ErrDeliveryExpired
		}
		_, gaps, err := browserDeliveryRanges(ctx, tx, input.SourceSessionID)
		if err != nil {
			return browserAppendResult{}, err
		}
		var source struct {
			Exam    string `db:"exam_id"`
			Sitting string `db:"exam_sitting_id"`
			Policy  []byte `db:"browser_policy_canonical"`
		}
		if err := tx.Get(ctx, &source, `SELECT s.exam_id,s.exam_sitting_id,r.browser_policy_canonical FROM browser_activity_sources s JOIN exam_revisions r ON r.id=s.policy_revision_id AND r.exam_id=s.exam_id AND r.browser_policy_digest=s.policy_digest WHERE s.id=?::uuid`, string(input.SourceSessionID)); err != nil {
			return browserAppendResult{}, err
		}
		policy, err := model.DecodeBrowserPolicy(source.Policy)
		if err != nil {
			return browserAppendResult{}, err
		}
		receipts := make([]model.BrowserEventReceipt, 0, len(input.Events))
		fresh := make([]browserAppendRecord, 0, len(input.Events))
		var bytes, metadataBytes int64
		allocated := status.AllocatedThrough
		windowBase := max(status.HighestContiguous, status.SettledThrough)
		for _, event := range input.Events {
			fingerprint, err := event.Fingerprint()
			if err != nil {
				return browserAppendResult{}, err
			}
			var retained struct {
				Fingerprint string `db:"event_fingerprint"`
				Receipt     []byte `db:"receipt_canonical"`
			}
			err = tx.Get(ctx, &retained, `SELECT event_fingerprint,receipt_canonical FROM browser_activity_events WHERE source_session_id=?::uuid AND sequence=?`, string(input.SourceSessionID), event.Sequence)
			if err == nil {
				if retained.Fingerprint != fingerprint {
					return browserAppendResult{}, store.NewErrConflict("browser_activity", "browser_activity_sequence", nil)
				}
				var receipt model.BrowserEventReceipt
				if json.Unmarshal(retained.Receipt, &receipt) != nil || receipt.Validate() != nil {
					return browserAppendResult{}, invalidPersistedState("browser_delivery", "receipt", model.ErrDeliveryInvalid)
				}
				receipts = append(receipts, receipt)
				continue
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return browserAppendResult{}, err
			}
			if event.Sequence <= status.TerminalMissingThrough || status.DetailMode == "summary_only" {
				return browserAppendResult{}, store.NewErrConflict("browser_delivery", "detail_budget_exhausted", nil)
			}
			if slices.ContainsFunc(gaps, func(r model.SequenceRange) bool { return r.First <= event.Sequence && event.Sequence <= r.Last }) {
				return browserAppendResult{}, store.NewErrConflict("browser_delivery", "declaration_conflict", nil)
			}
			if status.Closure.ClosedAt != nil {
				boundary := *status.Closure.KnownAtClose
				if status.Closure.FinalSequence != nil {
					boundary = *status.Closure.FinalSequence
				}
				if event.Sequence > boundary {
					return browserAppendResult{}, store.NewErrConflict("browser_delivery", "replay_window_exceeded", nil)
				}
			}
			if event.Sequence > windowBase+model.BrowserReceiveWindow {
				return browserAppendResult{}, store.NewErrConflict("browser_delivery", "replay_window_exceeded", nil)
			}
			prior, err := browserVerifiedPrior(ctx, tx, input.SourceSessionID, event.RedirectFromSequence)
			if err != nil {
				return browserAppendResult{}, err
			}
			if _, err := model.ValidateBrowserPolicyEvent(event, policy, prior); err != nil {
				return browserAppendResult{}, store.NewErrConflict("browser_activity", "browser_activity_policy_semantics", nil)
			}
			raw, err := event.Canonical()
			if err != nil {
				return browserAppendResult{}, err
			}
			receipt := model.BrowserEventReceipt{Sequence: event.Sequence, EventDigest: fingerprint, ReceivedAt: status.ServerTime}
			receiptRaw, err := canonicalPreflightValue(receipt)
			if err != nil {
				return browserAppendResult{}, err
			}
			metadata, err := canonicalPreflightValue(browserEventMetadata{})
			if err != nil {
				return browserAppendResult{}, err
			}
			count := int64(len(raw) + len(receiptRaw) + len(metadata))
			if len(raw) > model.BrowserActivityAppendMaximumBytes {
				return browserAppendResult{}, model.ErrDeliveryInvalid
			}
			if len(receiptRaw) > 512 || len(metadata) > 256 {
				return browserAppendResult{}, invalidPersistedState("browser_delivery", "receipt", model.ErrDeliveryInvalid)
			}
			fresh = append(fresh, browserAppendRecord{Event: event, Raw: raw, Receipt: receiptRaw, Metadata: metadata, Fingerprint: fingerprint, Bytes: count})
			receipts = append(receipts, receipt)
			bytes += count
			metadataBytes += int64(len(receiptRaw) + len(metadata))
			allocated = max(allocated, event.Sequence)
		}
		if metadataBytes > 64*1024 || bytes > 320*1024 {
			return browserAppendResult{}, store.NewErrInvalidInput("browser_activity", "counted_batch_size", nil)
		}
		if len(fresh) > 0 {
			part, attempt, pending, err := browserQuotas(ctx, tx, input.ParticipationID, input.Access.AttemptID)
			if err != nil {
				return browserAppendResult{}, err
			}
			delta := allocated - status.AllocatedThrough
			nextPart, err := part.Charge(int64(len(fresh)), bytes, delta)
			if err != nil {
				return browserAppendResult{}, err
			}
			nextAttempt, err := attempt.Charge(int64(len(fresh)), bytes, delta)
			if err != nil {
				return browserAppendResult{}, err
			}
			if nextPart.SummaryOnly || nextAttempt.SummaryOnly {
				reason := nextPart.StopReason
				attemptScope := nextAttempt.SummaryOnly
				if attemptScope {
					reason = nextAttempt.StopReason
				}
				if reason == nil {
					return browserAppendResult{}, invalidPersistedState("browser_delivery", "receipt", model.ErrDeliveryInvalid)
				}
				if err := latchBrowserDelivery(ctx, tx, input.Access.AttemptID, input.ParticipationID, *reason, attemptScope); err != nil {
					return browserAppendResult{}, err
				}
				code := "detail_budget_exhausted"
				if *reason == model.DeliveryStopPositions {
					code = "sequence_limit"
				}
				if err := completeBrowserAppendAuditIf(ctx, tx, input, code, audit); err != nil {
					return browserAppendResult{}, err
				}
				return browserAppendResult{Refusal: code}, nil
			}
			repair := input.Events[0].Sequence == windowBase+1
			for i := 1; i < len(input.Events); i++ {
				repair = repair && input.Events[i].Sequence == input.Events[i-1].Sequence+1
			}
			var recoverable bool
			if err := tx.Get(ctx, &recoverable, `SELECT EXISTS(SELECT 1 FROM browser_activity_sources WHERE exam_attempt_id=? AND allocated_through_sequence>GREATEST(highest_contiguous,settled_through_sequence,terminal_missing_through_sequence))`, input.Access.AttemptID.String()); err != nil {
				return browserAppendResult{}, err
			}
			// Include the earlier gap this proposed out-of-order append creates;
			// persisted watermarks still describe the state before admission.
			recoverable = recoverable || !repair
			if !model.CanRetainPendingDelivery(pending, part.RetainedBytes, part.ByteLimit, bytes, repair, recoverable) || !model.CanRetainPendingDelivery(pending, attempt.RetainedBytes, attempt.ByteLimit, bytes, repair, recoverable) {
				return browserAppendResult{}, store.NewErrConflict("browser_delivery", "pending_capacity", nil)
			}
			if err := markBrowserInventoryChanged(ctx, tx, input.Access.AttemptID); err != nil {
				return browserAppendResult{}, err
			}
			for _, record := range fresh {
				if err := insertBrowserEvent(ctx, tx, input, source.Exam, source.Sitting, record, status.ServerTime); err != nil {
					return browserAppendResult{}, err
				}
			}
			if _, err := tx.Exec(ctx, `UPDATE browser_activity_sources SET allocated_through_sequence=?,highest_seen=GREATEST(highest_seen,?) WHERE id=?::uuid`, allocated, fresh[len(fresh)-1].Event.Sequence, string(input.SourceSessionID)); err != nil {
				return browserAppendResult{}, err
			}
			if _, err := tx.Exec(ctx, `UPDATE exam_attempt_security_owners SET browser_retained_records=?,browser_retained_bytes=?,browser_allocated_positions=? WHERE participation_id=?`, nextPart.RetainedRecords, nextPart.RetainedBytes, nextPart.AllocatedPositions, input.ParticipationID.String()); err != nil {
				return browserAppendResult{}, err
			}
			if _, err := tx.Exec(ctx, `UPDATE exam_attempt_delivery_budgets SET browser_retained_records=?,browser_retained_bytes=?,browser_allocated_positions=?,browser_pending_bytes=browser_pending_bytes+? WHERE exam_attempt_id=?`, nextAttempt.RetainedRecords, nextAttempt.RetainedBytes, nextAttempt.AllocatedPositions, bytes, input.Access.AttemptID.String()); err != nil {
				return browserAppendResult{}, err
			}
		}
		current, err := settleBrowserSource(ctx, tx, input.SourceSessionID)
		if err != nil {
			return browserAppendResult{}, err
		}
		if err := advanceBrowserInterpretation(ctx, tx, input.SourceSessionID, input.Access.AttemptID, policy, current.SettledThrough); err != nil {
			return browserAppendResult{}, err
		}
		if _, err := tx.Exec(ctx, `UPDATE browser_activity_sources SET highest_contiguous=?,settled_through_sequence=? WHERE id=?::uuid`, current.HighestContiguous, current.SettledThrough, string(input.SourceSessionID)); err != nil {
			return browserAppendResult{}, err
		}
		acknowledgement := &model.BrowserActivityAcknowledgement{SourceSessionID: input.SourceSessionID, Receipts: receipts, HighestContiguous: current.HighestContiguous, SettledThrough: current.SettledThrough, HighestSeen: current.HighestSeen, AllocatedThrough: current.AllocatedThrough, TerminalMissingThrough: current.TerminalMissingThrough, MissingRanges: []model.BrowserActivityMissingRange{}, MissingRangesTruncated: current.MissingRangesTruncated, ServerTime: current.ServerTime, ExamID: model.ExamID(source.Exam), SittingID: model.ExamSittingID(source.Sitting)}
		for _, r := range current.MissingRanges {
			acknowledgement.MissingRanges = append(acknowledgement.MissingRanges, model.BrowserActivityMissingRange{First: r.First, Last: r.Last})
		}
		if err := completeBrowserAppendAuditIf(ctx, tx, input, "retained", audit); err != nil {
			return browserAppendResult{}, err
		}
		return browserAppendResult{Acknowledgement: acknowledgement}, nil
	}
	execute := func(ctx context.Context, tx *sqlxTxWrapper) (browserAppendResult, error) {
		return perform(ctx, tx, true)
	}
	var result browserAppendResult
	var err error
	if command == nil {
		result, err = runSQLTransaction(ctx, s.GetMaster().Begin, "append Browser Activity", execute)
	} else {
		outcome, mutationErr := runIdempotentMutation(ctx, s.SQLStore, "historical browser append", idempotentMutation[browserAppendResult]{command: command, auditEventID: input.AuditEventID, execute: execute,
			encode: func(value browserAppendResult) ([]byte, error) { return canonicalPreflightValue(value) },
			decode: func(version int, raw []byte) (browserAppendResult, error) {
				var value browserAppendResult
				if version != 1 {
					return value, model.ErrDeliveryInvalid
				}
				err := decodeCommandOutcome(raw, &value)
				return value, err
			},
			hydrateReplay: func(ctx context.Context, tx *sqlxTxWrapper, value browserAppendResult) (browserAppendResult, error) {
				owner, err := s.lockBrowserDeclaration(ctx, tx, access)
				if err != nil {
					return browserAppendResult{}, err
				}
				if owner.Closure.ClosedAt == nil {
					return browserAppendResult{}, model.ErrDeliveryConflict
				}
				if value.Refusal != "" {
					return value, nil
				}
				// The retained per-event ledger, rather than the cached HTTP outcome, remains
				// the acknowledgement authority on every replay.
				for _, event := range input.Events {
					var digest string
					if err := tx.Get(ctx, &digest, `SELECT event_fingerprint FROM browser_activity_events WHERE source_session_id=?::uuid AND sequence=?`, string(input.SourceSessionID), event.Sequence); err != nil {
						if errors.Is(err, sql.ErrNoRows) {
							return browserAppendResult{}, model.ErrDeliveryExpired
						}
						return browserAppendResult{}, err
					}
					want, err := event.Fingerprint()
					if err != nil || digest != want {
						return browserAppendResult{}, model.ErrDeliveryConflict
					}
				}
				return perform(ctx, tx, false)
			},
			completeReplay: func(ctx context.Context, tx *sqlxTxWrapper, value browserAppendResult, original string) error {
				return completeBrowserDeliveryAudit(ctx, tx, input.AuditEventID, input.AuditAt, input.SourceSessionID, true)
			},
		})
		err = mutationErr
		if outcome != nil {
			result = outcome.Value
		}
	}

	if err != nil {
		return nil, err
	}
	if result.Refusal != "" {
		return nil, &store.BrowserDeliveryRefusal{Reason: result.Refusal}
	}
	return result.Acknowledgement, nil
}

func browserQuotas(ctx context.Context, tx *sqlxTxWrapper, partID model.AttemptParticipationID, attemptID model.ExamAttemptID) (model.DeliveryQuotaUsage, model.DeliveryQuotaUsage, int64, error) {
	part, attempt := model.NewDeliveryQuotaUsage(false, false), model.NewDeliveryQuotaUsage(false, true)
	var row struct {
		PR      int64          `db:"pr"`
		PB      int64          `db:"pb"`
		PP      int64          `db:"pp"`
		PS      bool           `db:"ps"`
		PReason sql.NullString `db:"p_reason"`
		AR      int64          `db:"ar"`
		AB      int64          `db:"ab"`
		AP      int64          `db:"ap"`
		AS      bool           `db:"asummary"`
		AReason sql.NullString `db:"a_reason"`
		Pending int64          `db:"pending"`
	}
	err := tx.Get(ctx, &row, `SELECT o.browser_retained_records AS pr,o.browser_retained_bytes AS pb,o.browser_allocated_positions AS pp,o.browser_summary_only AS ps,o.browser_stop_reason AS p_reason,b.browser_retained_records AS ar,b.browser_retained_bytes AS ab,b.browser_allocated_positions AS ap,b.browser_summary_only AS asummary,b.browser_stop_reason AS a_reason,b.browser_pending_bytes AS pending FROM exam_attempt_security_owners o JOIN exam_attempt_delivery_budgets b ON b.exam_attempt_id=o.exam_attempt_id WHERE o.participation_id=? AND o.exam_attempt_id=? FOR UPDATE OF o,b`, partID.String(), attemptID.String())
	part.RetainedRecords, part.RetainedBytes, part.AllocatedPositions, part.SummaryOnly = row.PR, row.PB, row.PP, row.PS
	attempt.RetainedRecords, attempt.RetainedBytes, attempt.AllocatedPositions, attempt.SummaryOnly = row.AR, row.AB, row.AP, row.AS
	if row.PReason.Valid {
		v := model.DeliveryStopReason(row.PReason.String)
		part.StopReason = &v
	}
	if row.AReason.Valid {
		v := model.DeliveryStopReason(row.AReason.String)
		attempt.StopReason = &v
	}
	if err == nil && (part.Validate() != nil || attempt.Validate() != nil) {
		err = model.ErrDeliveryInvalid
	}
	return part, attempt, row.Pending, err
}
func latchBrowserDelivery(ctx context.Context, tx *sqlxTxWrapper, attempt model.ExamAttemptID, part model.AttemptParticipationID, reason model.DeliveryStopReason, attemptScope bool) error {
	if attemptScope {
		if _, err := tx.Exec(ctx, `UPDATE exam_attempt_delivery_budgets SET browser_summary_only=true,browser_stop_reason=COALESCE(browser_stop_reason,?) WHERE exam_attempt_id=?`, string(reason), attempt.String()); err != nil {
			return err
		}
		return settleExhaustedBrowserSources(ctx, tx, attempt, "")
	}
	if _, err := tx.Exec(ctx, `UPDATE exam_attempt_security_owners SET browser_summary_only=true,browser_stop_reason=COALESCE(browser_stop_reason,?) WHERE participation_id=?`, string(reason), part.String()); err != nil {
		return err
	}
	return settleExhaustedBrowserSources(ctx, tx, attempt, part)
}
func insertBrowserEvent(ctx context.Context, tx *sqlxTxWrapper, input *store.BrowserActivityAppend, exam, sitting string, record browserAppendRecord, now time.Time) error {
	event := record.Event
	var scheme, host, port, path, matched, reason any
	if event.Location != nil {
		scheme = event.Location.Scheme
		host = event.Location.Host
		port = nullableString(event.Location.Port)
		path = event.Location.Path
	}
	if event.MatchedRuleID != nil {
		matched = *event.MatchedRuleID
	}
	if event.BlockReason != nil {
		reason = string(*event.BlockReason)
	}
	_, err := tx.Exec(ctx, `INSERT INTO browser_activity_events(source_session_id,sequence,exam_id,exam_sitting_id,exam_attempt_id,participation_id,generation,policy_revision_id,kind,client_occurred_at,location_scheme,location_host,location_port,location_path,matched_rule_id,block_reason,event_fingerprint,received_at,record_canonical,receipt_canonical,metadata_canonical,redirect_from_sequence,counted_bytes) VALUES(?::uuid,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, string(input.SourceSessionID), event.Sequence, exam, sitting, input.Access.AttemptID.String(), input.ParticipationID.String(), input.Generation, input.PolicyRevisionID.String(), string(event.Kind), event.ClientOccurredAt, scheme, host, port, path, matched, reason, record.Fingerprint, now, record.Raw, record.Receipt, record.Metadata, nullableInt64Pointer(event.RedirectFromSequence), record.Bytes)
	return err
}
func browserVerifiedPrior(ctx context.Context, tx *sqlxTxWrapper, source model.BrowserSourceSessionID, sequence *int64) (*model.BrowserActivityEvent, error) {
	if sequence == nil {
		return nil, nil
	}
	var raw []byte
	err := tx.Get(ctx, &raw, `SELECT record_canonical FROM browser_activity_events WHERE source_session_id=?::uuid AND sequence=? AND interpretation_state=1`, string(source), *sequence)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var event model.BrowserActivityEvent
	if json.Unmarshal(raw, &event) != nil {
		return nil, invalidPersistedState("browser_delivery", "record", model.ErrDeliveryInvalid)
	}
	return &event, nil
}
func advanceBrowserInterpretation(ctx context.Context, tx *sqlxTxWrapper, source model.BrowserSourceSessionID, attempt model.ExamAttemptID, policy model.BrowserPolicy, through int64) error {
	var rows []struct {
		Sequence   int64     `db:"sequence"`
		ReceivedAt time.Time `db:"received_at"`
		Raw        []byte    `db:"record_canonical"`
		Bytes      int64     `db:"counted_bytes"`
	}
	if err := tx.Select(ctx, &rows, `SELECT sequence,record_canonical,counted_bytes,received_at FROM browser_activity_events WHERE source_session_id=?::uuid AND interpretation_state=0 AND sequence<=? ORDER BY sequence LIMIT 50001`, string(source), through); err != nil {
		return err
	}
	if len(rows) > 50000 {
		return invalidPersistedState("browser_delivery", "interpretation", model.ErrDeliveryInvalid)
	}
	var released int64
	for _, row := range rows {
		var event model.BrowserActivityEvent
		if json.Unmarshal(row.Raw, &event) != nil {
			return invalidPersistedState("browser_delivery", "interpretation", model.ErrDeliveryInvalid)
		}
		prior, err := browserVerifiedPrior(ctx, tx, source, event.RedirectFromSequence)
		if err != nil {
			return err
		}
		verified, validationErr := model.ValidateBrowserPolicyEvent(event, policy, prior)
		state := 2
		if validationErr == nil && verified {
			state = 1
			if err := retainBrowserIntegrity(ctx, tx, source, attempt, event, policy, row.ReceivedAt); err != nil {
				return err
			}
		}
		// A missing or contradictory earlier report is uncertainty, never grounds
		// to reject a later repair or manufacture verified browser evidence.
		metadata, err := canonicalPreflightValue(browserEventMetadata{Interpretation: state})
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE browser_activity_events SET interpretation_state=?,metadata_canonical=? WHERE source_session_id=?::uuid AND sequence=? AND interpretation_state=0`, state, metadata, string(source), row.Sequence); err != nil {
			return err
		}
		released += row.Bytes
	}
	if released > 0 {
		result, err := tx.Exec(ctx, `UPDATE exam_attempt_delivery_budgets SET browser_pending_bytes=browser_pending_bytes-? WHERE exam_attempt_id=? AND browser_pending_bytes>=?`, released, attempt.String(), released)
		if err != nil {
			return err
		}
		if count, err := result.RowsAffected(); err != nil || count != 1 {
			return invalidPersistedState("browser_delivery", "interpretation", model.ErrDeliveryInvalid)
		}
	}
	return nil
}
func completeBrowserAppendAudit(ctx context.Context, tx *sqlxTxWrapper, input *store.BrowserActivityAppend, result string) error {
	data, err := model.EncodeAuditData(map[string]any{"source_session_id": string(input.SourceSessionID), "submitted_event_count": len(input.Events), "result": result})
	if err != nil {
		return err
	}
	_, err = completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", data, input.AuditAt)
	return err
}

func completeBrowserAppendAuditIf(ctx context.Context, tx *sqlxTxWrapper, input *store.BrowserActivityAppend, result string, complete bool) error {
	if !complete {
		return nil
	}
	return completeBrowserAppendAudit(ctx, tx, input, result)
}
