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

type browserDeclarationOwner struct {
	SourceSessionID                                 model.BrowserSourceSessionID
	ParticipationID                                 string
	Allocated, TerminalThrough, DeclarationRevision int64
	SummaryOnly, AttemptSummaryOnly                 bool
	Closure                                         model.DeliveryClosure
	Now                                             time.Time
}

func (s *sqlExamAttemptStore) lockBrowserDeclaration(ctx context.Context, tx *sqlxTxWrapper, access store.BrowserDeliveryAccess) (browserDeclarationOwner, error) {
	var owner browserDeclarationOwner
	if err := s.lockBrowserDelivery(ctx, tx, access); err != nil {
		return owner, err
	}
	status, err := browserSourceStatus(ctx, tx, access.SourceSessionID)
	if err != nil {
		return owner, err
	}
	if status.Closure.UploadExpiresAt != nil && !status.ServerTime.Before(*status.Closure.UploadExpiresAt) {
		return owner, model.ErrDeliveryExpired
	}
	return browserDeclarationOwner{SourceSessionID: access.SourceSessionID, ParticipationID: status.ParticipationID.String(), Allocated: status.AllocatedThrough, TerminalThrough: status.TerminalMissingThrough, DeclarationRevision: status.DeclarationRevision, SummaryOnly: status.DetailMode == "summary_only", Closure: status.Closure, Now: status.ServerTime}, nil
}
func completeBrowserDeliveryAudit(ctx context.Context, tx *sqlxTxWrapper, id string, at int64, source model.BrowserSourceSessionID, replayed bool) error {
	data, err := model.EncodeAuditData(map[string]any{"source_session_id": string(source), "idempotency_replayed": replayed})
	if err != nil {
		return err
	}
	_, err = completeAuditEvent(ctx, tx, id, model.AuditStatusSuccess, "", data, at)
	return err
}
func browserDeliveryConflict() error {
	return store.NewErrConflict("browser_delivery", "declaration_conflict", nil)
}

func browserDeclarationReplay(ctx context.Context, tx *sqlxTxWrapper, owner browserDeclarationOwner, id, digest string) (*deliveryDeclarationRecord, error) {
	var raw []byte
	err := tx.Get(ctx, &raw, `SELECT canonical FROM browser_delivery_declarations WHERE source_session_id=?::uuid AND declaration_id=?`, string(owner.SourceSessionID), id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	record, err := decodeDeliveryDeclarationRecord(raw)
	if err != nil {
		return nil, err
	}
	if record.Receipt.DeclarationID != id || record.Receipt.SettledThroughSequence > owner.Allocated {
		return nil, invalidPersistedState("delivery_declaration", "receipt", model.ErrDeliveryInvalid)
	}
	if record.RequestDigest != digest {
		return nil, browserDeliveryConflict()
	}
	return &record, nil
}

type browserDeclarationOutcome struct {
	Refusal  string                          `json:"refusal"`
	Receipt  model.DeliveryGapReceipt        `json:"receipt"`
	Status   *model.BrowserSourceStatus      `json:"status"`
	Capacity *model.DeliveryMetadataCapacity `json:"capacity"`
}

func (s *sqlExamAttemptStore) DeclareBrowserDeliveryGaps(ctx context.Context, input *store.BrowserDeliveryGapDeclaration, command *store.CommandIdempotency) (*model.BrowserDeliveryGapResult, error) {
	if input == nil || input.Declaration.Validate() != nil {
		return nil, store.NewErrInvalidInput("browser_delivery", "gaps", nil)
	}
	raw, err := input.Declaration.Canonical()
	if err != nil {
		return nil, err
	}
	result, err := s.declareBrowserDelivery(ctx, input.Access, input.AuditEventID, input.AuditAt, command, deliveryDeclarationRecord{Kind: "gaps", RequestDigest: model.SHA256Fingerprint(raw), Gaps: &input.Declaration})
	if err != nil {
		return nil, err
	}
	if result.Refusal != "" {
		return nil, &store.BrowserDeliveryRefusal{Reason: result.Refusal}
	}
	if result.Capacity != nil {
		return nil, result.Capacity
	}
	return &model.BrowserDeliveryGapResult{Receipt: result.Receipt, Status: *result.Status}, nil
}
func (s *sqlExamAttemptStore) SealBrowserDelivery(ctx context.Context, input *store.BrowserDeliveryFinalDeclaration, command *store.CommandIdempotency) (*model.BrowserSourceStatus, error) {
	if input == nil || input.Declaration.Validate() != nil {
		return nil, store.NewErrInvalidInput("browser_delivery", "final", nil)
	}
	raw, err := input.Declaration.Canonical()
	if err != nil {
		return nil, err
	}
	result, err := s.declareBrowserDelivery(ctx, input.Access, input.AuditEventID, input.AuditAt, command, deliveryDeclarationRecord{Kind: "final", RequestDigest: model.SHA256Fingerprint(raw), Final: &input.Declaration})
	if err != nil {
		return nil, err
	}
	if result.Refusal != "" {
		return nil, &store.BrowserDeliveryRefusal{Reason: result.Refusal}
	}
	if result.Capacity != nil {
		return nil, result.Capacity
	}
	return result.Status, nil
}

func (s *sqlExamAttemptStore) declareBrowserDelivery(ctx context.Context, access store.BrowserDeliveryAccess, auditID string, auditAt int64, command *store.CommandIdempotency, record deliveryDeclarationRecord) (*browserDeclarationOutcome, error) {
	expectedOperation := store.BrowserDeliveryFinalOperation
	if record.Gaps != nil {
		expectedOperation = store.BrowserDeliveryGapsOperation
	}
	if command == nil || command.Operation != expectedOperation || command.UserID != access.Access.CandidateUserID || !model.IsValidId(auditID) || auditAt <= 0 {
		return nil, store.NewErrInvalidInput("browser_delivery", "declaration", nil)
	}
	declarationID := ""
	if record.Gaps != nil {
		declarationID = record.Gaps.DeclarationID
	} else {
		declarationID = record.Final.DeclarationID
	}
	result, err := runIdempotentMutation(ctx, s.SQLStore, "declare browser delivery", idempotentMutation[*browserDeclarationOutcome]{command: command, auditEventID: auditID,
		execute: func(ctx context.Context, tx *sqlxTxWrapper) (*browserDeclarationOutcome, error) {
			owner, err := s.lockBrowserDeclaration(ctx, tx, access)
			if err != nil {
				return nil, err
			}
			if owner.Closure.UploadExpiresAt != nil && !owner.Now.Before(*owner.Closure.UploadExpiresAt) {
				return nil, model.ErrDeliveryExpired
			}
			prior, err := browserDeclarationReplay(ctx, tx, owner, declarationID, record.RequestDigest)
			if err != nil {
				return nil, err
			}
			if prior != nil {
				if prior.Kind != record.Kind {
					return nil, browserDeliveryConflict()
				}
				status, err := browserSourceStatus(ctx, tx, access.SourceSessionID)
				if err != nil {
					return nil, err
				}
				if err := completeBrowserDeliveryAudit(ctx, tx, auditID, auditAt, access.SourceSessionID, true); err != nil {
					return nil, err
				}
				return &browserDeclarationOutcome{Receipt: prior.Receipt, Status: status}, nil
			}
			received, gaps, err := browserDeliveryRanges(ctx, tx, access.SourceSessionID)
			if err != nil {
				return nil, err
			}
			progress, err := model.ResolveDeliveryProgress(owner.Allocated, received, gaps, owner.TerminalThrough)
			if err != nil {
				return nil, err
			}
			newAllocated := owner.Allocated
			if record.Gaps != nil {
				if err := model.ValidateDeliveryGapPlacement(false, progress, owner.Allocated, owner.DeclarationRevision, owner.Closure, owner.SummaryOnly || owner.AttemptSummaryOnly, *record.Gaps, received, gaps, owner.Now); err != nil {
					return nil, err
				}
				newAllocated = max(newAllocated, record.Gaps.AllocatedThroughSequence)
			} else {
				closure, delta, err := owner.Closure.DeclareFinal(false, owner.DeclarationRevision, owner.Allocated, owner.SummaryOnly || owner.AttemptSummaryOnly, *record.Final, owner.Now)
				if err != nil {
					return nil, err
				}
				owner.Closure = closure
				newAllocated += delta
			}
			var budget struct {
				Metadata      int64 `db:"control_metadata_bytes"`
				Positions     int64 `db:"browser_allocated_positions"`
				PartPositions int64 `db:"part_positions"`
				Intervals     int64 `db:"explicit_missing_intervals"`
			}
			if err := tx.Get(ctx, &budget, `SELECT b.control_metadata_bytes,b.browser_allocated_positions,b.explicit_missing_intervals,o.browser_allocated_positions AS part_positions FROM exam_attempt_delivery_budgets b JOIN exam_attempt_security_owners o ON o.exam_attempt_id=b.exam_attempt_id WHERE b.exam_attempt_id=? AND o.participation_id=?`, access.Access.AttemptID.String(), owner.ParticipationID); err != nil {
				return nil, err
			}
			delta := newAllocated - owner.Allocated

			refusal := ""
			if delta > model.BrowserParticipationPositionLimit-budget.PartPositions || delta > model.BrowserAttemptPositionLimit-budget.Positions {
				if err := latchBrowserDelivery(ctx, tx, access.Access.AttemptID, model.AttemptParticipationID(owner.ParticipationID), model.DeliveryStopPositions, delta > model.BrowserAttemptPositionLimit-budget.Positions); err != nil {
					return nil, err
				}
				refusal = "sequence_limit"
			} else if record.Gaps != nil && int64(len(record.Gaps.Ranges)) > model.DeliveryMissingIntervalLimit-budget.Intervals {
				if err := latchDeliveryMetadata(ctx, tx, access.Access.AttemptID); err != nil {
					return nil, err
				}
				refusal = "detail_budget_exhausted"
			}
			if refusal != "" {
				if err := completeBrowserDeliveryAudit(ctx, tx, auditID, auditAt, access.SourceSessionID, false); err != nil {
					return nil, err
				}
				return &browserDeclarationOutcome{Refusal: refusal}, nil
			}
			if err := markBrowserInventoryChanged(ctx, tx, access.Access.AttemptID); err != nil {
				return nil, err
			}
			owner.Allocated = newAllocated
			owner.DeclarationRevision++
			if record.Gaps != nil {
				// A declaration may join adjacent earlier ranges. Resolve the new
				// settled watermark after its durable insertion below.
				budget.Intervals += int64(len(record.Gaps.Ranges))
			}
			record.Receipt = model.DeliveryGapReceipt{DeclarationID: declarationID, RequestDigest: record.RequestDigest, DeclarationRevision: owner.DeclarationRevision}
			prospectiveGaps := gaps
			if record.Gaps != nil {
				prospectiveGaps = mergeNativeDeliveryRanges(append(slices.Clone(gaps), record.Gaps.Ranges...))
			}
			prospective, err := model.ResolveDeliveryProgress(owner.Allocated, received, prospectiveGaps, owner.TerminalThrough)
			if err != nil {
				return nil, err
			}
			record.Receipt.SettledThroughSequence = prospective.SettledThrough

			encoded, err := canonicalPreflightValue(record)
			if err != nil {
				return nil, err
			}
			if record.Gaps != nil {
				next, err := model.ReserveDeliveryMetadata(budget.Metadata, int64(len(encoded)))
				if err != nil {
					var capacity *model.DeliveryMetadataCapacity
					if !errors.As(err, &capacity) {
						return nil, err
					}
					if err := latchDeliveryMetadata(ctx, tx, access.Access.AttemptID); err != nil {
						return nil, err
					}
					if err := completeBrowserDeliveryAudit(ctx, tx, auditID, auditAt, access.SourceSessionID, false); err != nil {
						return nil, err
					}
					return &browserDeclarationOutcome{Capacity: capacity}, nil
				}
				budget.Metadata = next
			}
			if _, err := tx.Exec(ctx, `INSERT INTO browser_delivery_declarations(source_session_id,declaration_id,kind,canonical) VALUES(?::uuid,?,?,?)`, string(access.SourceSessionID), declarationID, record.Kind, encoded); err != nil {
				return nil, err
			}
			closure, err := canonicalPreflightValue(owner.Closure)
			if err != nil {
				return nil, err
			}
			if _, err := tx.Exec(ctx, `UPDATE browser_activity_sources SET allocated_through_sequence=?,declaration_revision=?,closure_canonical=? WHERE id=?::uuid`, owner.Allocated, owner.DeclarationRevision, closure, string(access.SourceSessionID)); err != nil {
				return nil, err
			}
			if _, err := tx.Exec(ctx, `UPDATE exam_attempt_delivery_budgets SET control_metadata_bytes=?,browser_allocated_positions=browser_allocated_positions+?,explicit_missing_intervals=? WHERE exam_attempt_id=?`, budget.Metadata, delta, budget.Intervals, access.Access.AttemptID.String()); err != nil {
				return nil, err
			}
			if err := completeBrowserDeliveryAudit(ctx, tx, auditID, auditAt, access.SourceSessionID, false); err != nil {
				return nil, err
			}
			if _, err := tx.Exec(ctx, `UPDATE exam_attempt_security_owners SET browser_allocated_positions=browser_allocated_positions+? WHERE participation_id=?`, delta, owner.ParticipationID); err != nil {
				return nil, err
			}
			status, err := settleBrowserSource(ctx, tx, access.SourceSessionID)
			if err != nil {
				return nil, err
			}
			return &browserDeclarationOutcome{Receipt: record.Receipt, Status: status}, nil
		},
		encode: func(value *browserDeclarationOutcome) ([]byte, error) { return canonicalPreflightValue(value) },
		decode: func(version int, raw []byte) (*browserDeclarationOutcome, error) {
			var result browserDeclarationOutcome
			if version != 1 {
				return nil, invalidPersistedState("browser_delivery", "value", model.ErrDeliveryInvalid)
			}
			if err := decodeCommandOutcome(raw, &result); err != nil {
				return nil, err
			}
			if err := validateDeliveryDeclarationOutcome(result.Refusal, result.Receipt, result.Capacity); err != nil {
				return nil, err
			}
			return &result, nil
		},
		hydrateReplay: func(ctx context.Context, tx *sqlxTxWrapper, value *browserDeclarationOutcome) (*browserDeclarationOutcome, error) {
			owner, err := s.lockBrowserDeclaration(ctx, tx, access)
			if err != nil {
				return nil, err
			}
			if owner.Closure.UploadExpiresAt != nil && !owner.Now.Before(*owner.Closure.UploadExpiresAt) {
				return nil, model.ErrDeliveryExpired
			}
			if value.Refusal == "" && value.Capacity == nil && (value.Receipt.DeclarationID != declarationID || value.Receipt.RequestDigest != record.RequestDigest || value.Receipt.SettledThroughSequence > owner.Allocated || value.Receipt.DeclarationRevision > owner.DeclarationRevision) {
				return nil, invalidPersistedState("delivery_declaration", "receipt", model.ErrDeliveryInvalid)
			}
			if value.Capacity == nil {
				value.Status, err = browserSourceStatus(ctx, tx, access.SourceSessionID)
			}
			return value, err
		},
		completeReplay: func(ctx context.Context, tx *sqlxTxWrapper, value *browserDeclarationOutcome, original string) error {
			return completeBrowserDeliveryAudit(ctx, tx, auditID, auditAt, access.SourceSessionID, true)
		},
	})
	if err != nil {
		return nil, err
	}
	return result.Value, nil
}

func browserDeliveryRanges(ctx context.Context, tx *sqlxTxWrapper, source model.BrowserSourceSessionID) ([]model.SequenceRange, []model.SequenceRange, error) {
	var positions []int64
	if err := tx.Select(ctx, &positions, `SELECT sequence FROM browser_activity_events WHERE source_session_id=?::uuid ORDER BY sequence LIMIT 50001`, string(source)); err != nil {
		return nil, nil, err
	}
	if len(positions) > 50000 {
		return nil, nil, invalidPersistedState("browser_delivery", "ranges", model.ErrDeliveryInvalid)
	}
	received := []model.SequenceRange{}
	for _, seq := range positions {
		if len(received) > 0 && received[len(received)-1].Last+1 == seq {
			received[len(received)-1].Last = seq
		} else {
			received = append(received, model.SequenceRange{First: seq, Last: seq})
		}
	}
	var declarations [][]byte
	if err := tx.Select(ctx, &declarations, `SELECT canonical FROM browser_delivery_declarations WHERE source_session_id=?::uuid AND kind='gaps' LIMIT 4097`, string(source)); err != nil {
		return nil, nil, err
	}
	if len(declarations) > 4096 {
		return nil, nil, invalidPersistedState("browser_delivery", "ranges", model.ErrDeliveryInvalid)
	}
	gaps := []model.SequenceRange{}
	for _, raw := range declarations {
		d, err := decodeDeliveryDeclarationRecord(raw)
		if err != nil || d.Gaps == nil {
			return nil, nil, invalidPersistedState("browser_delivery", "ranges", model.ErrDeliveryInvalid)
		}
		gaps = append(gaps, d.Gaps.Ranges...)
	}
	if len(gaps) > 4096 {
		return nil, nil, invalidPersistedState("browser_delivery", "ranges", model.ErrDeliveryInvalid)
	}
	return received, mergeNativeDeliveryRanges(gaps), nil
}
func (s *sqlExamAttemptStore) ResolveBrowserDeliveryTarget(ctx context.Context, access store.BrowserDeliveryAccess) (*store.DeliveryOwnerTarget, error) {
	return runSQLTransaction(ctx, s.GetMaster().Begin, "resolve browser delivery owner", func(ctx context.Context, tx *sqlxTxWrapper) (*store.DeliveryOwnerTarget, error) {
		if err := s.lockBrowserDelivery(ctx, tx, access); err != nil {
			return nil, err
		}
		var row struct {
			Sitting string `db:"sitting"`
			Class   string `db:"class"`
		}
		if err := tx.Get(ctx, &row, `SELECT s.id AS sitting,s.class_id AS class FROM exam_attempts a JOIN exam_sittings s ON s.id=a.exam_sitting_id WHERE a.id=?`, access.Access.AttemptID.String()); err != nil {
			return nil, err
		}
		return &store.DeliveryOwnerTarget{SittingID: model.ExamSittingID(row.Sitting), ClassID: model.ClassID(row.Class)}, nil
	})
}
func (s *sqlExamAttemptStore) BrowserDeliveryReceipts(ctx context.Context, access store.BrowserDeliveryAccess, first int64, limit int) (*model.BrowserReceiptPage, error) {
	if first < 1 || first > model.BrowserParticipationPositionLimit || limit < 1 || limit > 64 {
		return nil, store.NewErrInvalidInput("browser_delivery", "receipt_page", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "browser receipt page", func(ctx context.Context, tx *sqlxTxWrapper) (*model.BrowserReceiptPage, error) {
		if _, err := s.lockBrowserDeclaration(ctx, tx, access); err != nil {
			return nil, err
		}
		var rows [][]byte
		if err := tx.Select(ctx, &rows, `SELECT receipt_canonical FROM browser_activity_events WHERE source_session_id=?::uuid AND sequence>=? ORDER BY sequence LIMIT ?`, string(access.SourceSessionID), first, limit+1); err != nil {
			return nil, err
		}
		value := &model.BrowserReceiptPage{Receipts: []model.BrowserEventReceipt{}}
		for i, raw := range rows {
			var receipt model.BrowserEventReceipt
			if json.Unmarshal(raw, &receipt) != nil || receipt.Validate() != nil {
				return nil, invalidPersistedState("browser_delivery", "value", model.ErrDeliveryInvalid)
			}
			if i == limit {
				next := receipt.Sequence
				value.NextSequence = &next
			} else {
				value.Receipts = append(value.Receipts, receipt)
			}
		}
		raw, err := canonicalPreflightValue(value)
		if err != nil {
			return nil, err
		}
		if len(raw) > 16384 {
			return nil, invalidPersistedState("browser_delivery", "value", model.ErrDeliveryInvalid)
		}
		return value, nil
	})
}
func (s *sqlExamAttemptStore) UpdateBrowserDeliverySummary(ctx context.Context, input *store.BrowserDeliverySummaryUpdate, command *store.CommandIdempotency) (*model.BrowserDeliverySummaryResult, error) {
	if input == nil || input.Summary.Validate() != nil || command == nil || command.Operation != store.BrowserDeliverySummaryOperation || command.UserID != input.Access.Access.CandidateUserID || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("browser_delivery", "summary", nil)
	}
	hydrate := func(ctx context.Context, tx *sqlxTxWrapper, value *model.BrowserDeliverySummaryResult) (*model.BrowserDeliverySummaryResult, error) {
		if _, err := s.lockBrowserDeclaration(ctx, tx, input.Access); err != nil {
			return nil, err
		}
		status, err := browserSourceStatus(ctx, tx, input.Access.SourceSessionID)
		if err != nil {
			return nil, err
		}
		if status.Summary == nil {
			return nil, invalidPersistedState("browser_delivery", "value", model.ErrDeliveryInvalid)
		}
		return &model.BrowserDeliverySummaryResult{Summary: *status.Summary, Status: *status}, nil
	}
	result, err := runIdempotentMutation(ctx, s.SQLStore, "browser delivery summary", idempotentMutation[*model.BrowserDeliverySummaryResult]{command: command, auditEventID: input.AuditEventID,
		execute: func(ctx context.Context, tx *sqlxTxWrapper) (*model.BrowserDeliverySummaryResult, error) {
			owner, err := s.lockBrowserDeclaration(ctx, tx, input.Access)
			if err != nil {
				return nil, err
			}
			var row struct {
				Raw      []byte       `db:"summary_canonical"`
				Received sql.NullTime `db:"summary_received_at"`
				Final    bool         `db:"summary_final"`
			}
			if err := tx.Get(ctx, &row, `SELECT summary_canonical,summary_received_at,summary_final FROM browser_activity_sources WHERE id=?::uuid`, string(input.Access.SourceSessionID)); err != nil {
				return nil, err
			}
			replay := false
			if len(row.Raw) > 0 {
				var prior model.UnretainedDeliverySummary
				if json.Unmarshal(row.Raw, &prior) != nil {
					return nil, invalidPersistedState("browser_delivery", "value", model.ErrDeliveryInvalid)
				}
				replay, err = prior.Compare(input.Summary)
				if err != nil {
					return nil, err
				}
			}
			if !replay {
				if err := markBrowserInventoryChanged(ctx, tx, input.Access.Access.AttemptID); err != nil {
					return nil, err
				}
				final := owner.Closure.FinalSequence != nil && !row.Final
				if row.Received.Valid && owner.Now.Sub(row.Received.Time) < 5*time.Second && !final {
					return nil, store.NewErrConflict("browser_delivery", "summary_rate_limited", nil)
				}
				raw, err := input.Summary.Canonical()
				if err != nil {
					return nil, err
				}
				if _, err := tx.Exec(ctx, `UPDATE browser_activity_sources SET summary_canonical=?,summary_received_at=?,summary_final=summary_final OR ? WHERE id=?::uuid`, raw, owner.Now, final, string(input.Access.SourceSessionID)); err != nil {
					return nil, err
				}
			}
			if !replay {
				if _, err := settleBrowserSource(ctx, tx, input.Access.SourceSessionID); err != nil {
					return nil, err
				}
			}
			if err := completeBrowserDeliveryAudit(ctx, tx, input.AuditEventID, input.AuditAt, input.Access.SourceSessionID, replay); err != nil {
				return nil, err
			}
			return hydrate(ctx, tx, nil)
		}, encode: func(value *model.BrowserDeliverySummaryResult) ([]byte, error) { return canonicalPreflightValue(value) },
		decode: func(version int, raw []byte) (*model.BrowserDeliverySummaryResult, error) {
			var v model.BrowserDeliverySummaryResult
			if version != 1 {
				return nil, invalidPersistedState("browser_delivery", "value", model.ErrDeliveryInvalid)
			}
			if err := decodeCommandOutcome(raw, &v); err != nil {
				return nil, err
			}
			return &v, nil
		}, hydrateReplay: hydrate,
		completeReplay: func(ctx context.Context, tx *sqlxTxWrapper, value *model.BrowserDeliverySummaryResult, original string) error {
			return completeBrowserDeliveryAudit(ctx, tx, input.AuditEventID, input.AuditAt, input.Access.SourceSessionID, true)
		},
	})
	if err != nil {
		return nil, err
	}
	return result.Value, nil
}
