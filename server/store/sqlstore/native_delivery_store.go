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

type nativeDeliveryOwner struct {
	ParticipationID     string         `db:"participation_id"`
	Binding             []byte         `db:"binding_canonical"`
	Allocated           int64          `db:"allocated_through_sequence"`
	TerminalThrough     int64          `db:"terminal_missing_through_sequence"`
	SummaryOnly         bool           `db:"summary_only"`
	StopReason          sql.NullString `db:"stop_reason"`
	AttemptStopReason   sql.NullString `db:"attempt_stop_reason"`
	AttemptSummaryOnly  bool           `db:"attempt_summary_only"`
	ClosureRaw          []byte         `db:"closure_canonical"`
	DeclarationRevision int64          `db:"declaration_revision"`
	SummaryRaw          []byte         `db:"summary_canonical"`
	SummaryReceivedAt   sql.NullTime   `db:"summary_received_at"`
	SummaryFinal        bool           `db:"summary_final"`
	Now                 time.Time
	Closure             model.DeliveryClosure
}

// Lifecycle owners call this under their existing Attempt/Participation locks.
// The fixed closure slot has already been reserved at admission.
func closeNativeDelivery(ctx context.Context, tx *sqlxTxWrapper, participationID model.AttemptParticipationID, reason model.DeliveryCloseReason, at time.Time) error {
	var row struct {
		Raw       []byte `db:"closure_canonical"`
		Allocated int64  `db:"allocated_through_sequence"`
	}
	if err := tx.Get(ctx, &row, `SELECT closure_canonical,allocated_through_sequence FROM exam_attempt_security_owners WHERE participation_id=? FOR UPDATE`, participationID.String()); err != nil {
		return err
	}
	var closure model.DeliveryClosure
	if len(row.Raw) > 0 && json.Unmarshal(row.Raw, &closure) != nil {
		return invalidPersistedState("native_delivery", "closure", model.ErrDeliveryInvalid)
	}
	var lease time.Time
	if err := tx.Get(ctx, &lease, `SELECT lease_expires_at FROM exam_attempt_participations WHERE id=?`, participationID.String()); err != nil {
		return err
	}
	if lease.Before(at) {
		at = lease
		reason = model.DeliveryClosedParticipation
	}
	closed, err := closure.Close(true, reason, row.Allocated, at.UTC().Truncate(time.Millisecond), nil)
	if err != nil {
		return err
	}
	raw, err := canonicalPreflightValue(closed)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE exam_attempt_security_owners SET closure_canonical=?,upload_expires_at=?,security_interaction_allowed=false WHERE participation_id=?`, raw, closed.UploadExpiresAt, participationID.String())
	if err != nil {
		return err
	}
	return closeParticipationBrowserSources(ctx, tx, participationID, reason, at)
}

func (s *sqlExamAttemptStore) lockNativeDelivery(ctx context.Context, tx *sqlxTxWrapper, access store.NativeDeliveryAccess) (nativeDeliveryOwner, error) {
	return s.nativeDeliveryOwner(ctx, tx, access, true)
}

// Read projections must not settle evidence or complete lifecycle transitions.
func (s *sqlExamAttemptStore) nativeDeliveryOwner(ctx context.Context, tx *sqlxTxWrapper, access store.NativeDeliveryAccess, persist bool) (nativeDeliveryOwner, error) {
	var owner nativeDeliveryOwner
	a := access.Access
	if !a.AttemptID.IsValid() || !a.CandidateUserID.IsValid() || !a.SessionID.IsValid() || !a.DesktopRegistrationID.IsValid() || !model.IsValidDPoPKeyThumbprint(a.DPoPKeyThumbprint) || !model.IsValidAgreementID(access.StreamID) || access.ParticipationID != "" && !access.ParticipationID.IsValid() || access.Generation < 0 {
		return owner, store.NewErrInvalidInput("native_delivery", "access", nil)
	}
	var attemptID string
	if err := tx.Get(ctx, &attemptID, `SELECT sit.id FROM exam_sittings sit JOIN exam_attempts a ON a.exam_sitting_id=sit.id WHERE a.id=? AND a.candidate_user_id=? FOR UPDATE OF sit`, a.AttemptID.String(), a.CandidateUserID.String()); err != nil {
		return owner, translateError("native_delivery", access.StreamID, err)
	}
	if err := tx.Get(ctx, &attemptID, `SELECT id FROM exam_attempts WHERE id=? FOR UPDATE`, a.AttemptID.String()); err != nil {
		return owner, err
	}
	var sessionID string
	if err := tx.Get(ctx, &sessionID, `SELECT se.id FROM sessions se JOIN users u ON u.id=se.user_id JOIN desktop_registrations dr ON dr.id=se.desktop_registration_id AND dr.user_id=u.id WHERE se.id=? AND se.user_id=? AND se.desktop_registration_id=? AND se.dpop_key_thumbprint=? AND dr.key_thumbprint=? AND dr.revoked_at IS NULL AND se.archived_at IS NULL AND se.revoked_at IS NULL AND se.expires_at>clock_timestamp() AND se.idle_expires_at>clock_timestamp() AND u.archived_at IS NULL AND u.disabled_at IS NULL FOR SHARE OF se,u,dr`, a.SessionID.String(), a.CandidateUserID.String(), a.DesktopRegistrationID.String(), a.DPoPKeyThumbprint, a.DPoPKeyThumbprint); err != nil {
		return owner, translateError("native_delivery", access.StreamID, err)
	}
	var retired bool
	if err := tx.Get(ctx, &retired, `SELECT security_retired_at IS NOT NULL FROM exam_attempt_delivery_budgets WHERE exam_attempt_id=?`, a.AttemptID.String()); err != nil {
		return owner, err
	}
	if retired {
		return owner, model.ErrDeliveryExpired
	}
	var ignored int64
	if err := tx.Get(ctx, &ignored, `SELECT control_metadata_bytes FROM exam_attempt_delivery_budgets WHERE exam_attempt_id=? FOR UPDATE`, a.AttemptID.String()); err != nil {
		return owner, err
	}
	err := tx.Get(ctx, &owner, `SELECT o.participation_id,o.binding_canonical,o.allocated_through_sequence,o.terminal_missing_through_sequence,o.summary_only,o.stop_reason,b.native_stop_reason AS attempt_stop_reason,b.native_summary_only AS attempt_summary_only,o.closure_canonical,o.declaration_revision,o.summary_canonical,o.summary_received_at,o.summary_final FROM exam_attempt_security_owners o JOIN exam_attempt_delivery_budgets b ON b.exam_attempt_id=o.exam_attempt_id JOIN exam_attempt_participations p ON p.id=o.participation_id WHERE o.exam_attempt_id=? AND o.delivery_stream_id=? AND o.registration_id=? AND o.key_thumbprint=? FOR UPDATE OF o,p`, a.AttemptID.String(), access.StreamID, a.DesktopRegistrationID.String(), a.DPoPKeyThumbprint)
	if err != nil {
		return owner, translateError("native_delivery", access.StreamID, err)
	}
	var binding admittedSecurityBinding
	if json.Unmarshal(owner.Binding, &binding) != nil || binding.Security.Validate() != nil {
		return owner, model.ErrDeliveryInvalid
	}
	if access.ParticipationID != "" && access.ParticipationID != binding.Security.ParticipationID || access.Generation != 0 && access.Generation != binding.Security.Generation {
		return owner, store.NewErrNotFound("native_delivery", access.StreamID)
	}
	access.ParticipationID = binding.Security.ParticipationID
	access.Generation = binding.Security.Generation
	if err := tx.Get(ctx, &owner.Now, `SELECT clock_timestamp()`); err != nil {
		return owner, err
	}
	owner.Now = owner.Now.UTC().Truncate(time.Millisecond)
	if len(owner.ClosureRaw) > 0 {
		if json.Unmarshal(owner.ClosureRaw, &owner.Closure) != nil || owner.Closure.Validate(true) != nil {
			return owner, invalidPersistedState("native_delivery", "closure", model.ErrDeliveryInvalid)
		}
	}
	if owner.Closure.ClosedAt == nil {
		// Expiry is authoritative even when its asynchronous lifecycle worker has
		// not run. An ended owner's first boundary must never move to read time.
		var p struct {
			State     string         `db:"state"`
			EndReason sql.NullString `db:"end_reason"`
			EndedAt   sql.NullTime   `db:"ended_at"`
			Lease     time.Time      `db:"lease_expires_at"`
		}
		if err := tx.Get(ctx, &p, `SELECT state,end_reason,ended_at,lease_expires_at FROM exam_attempt_participations WHERE id=?`, access.ParticipationID.String()); err != nil {
			return owner, err
		}
		if p.EndedAt.Valid || !owner.Now.Before(p.Lease) {
			at := p.Lease
			reason := model.DeliveryClosedParticipation
			if p.EndedAt.Valid && p.EndedAt.Time.Before(at) {
				at = p.EndedAt.Time
				reason = nativeParticipationCloseReason(model.AttemptParticipationEndReason(p.EndReason.String))
			}
			if persist {
				if err := closeNativeDelivery(ctx, tx, access.ParticipationID, reason, at); err != nil {
					return owner, err
				}
			}
			owner.Closure, err = owner.Closure.Close(true, reason, owner.Allocated, at.UTC().Truncate(time.Millisecond), nil)
			if err != nil {
				return owner, err
			}
		} else if _, err := s.lockCandidateGuard(ctx, tx, a); err != nil {
			return owner, err
		}
	}
	if owner.Closure.ClosedAt != nil && !owner.Now.Before(*owner.Closure.UploadExpiresAt) {
		owner.Closure, err = owner.Closure.Expire(true, owner.Now)
		if err != nil {
			return owner, err
		}
		owner.TerminalThrough = owner.Allocated
		if !persist {
			return owner, nil
		}
		raw, err := canonicalPreflightValue(owner.Closure)
		if err != nil {
			return owner, err
		}
		if _, err = tx.Exec(ctx, `UPDATE exam_attempt_security_owners SET closure_canonical=?,terminal_missing_through_sequence=? WHERE participation_id=?`, raw, owner.TerminalThrough, owner.ParticipationID); err != nil {
			return owner, err
		}
	}
	return owner, nil
}

func nativeParticipationCloseReason(reason model.AttemptParticipationEndReason) model.DeliveryCloseReason {
	switch reason {
	case model.AttemptParticipationEndSubmitted:
		return model.DeliveryClosedSubmission
	case model.AttemptParticipationEndManagerEnded:
		return model.DeliveryClosedManagerEnd
	case model.AttemptParticipationEndSittingClosed:
		return model.DeliveryClosedSitting
	case model.AttemptParticipationEndPolicySuspended, model.AttemptParticipationEndKicked:
		return model.DeliveryClosedSuspension
	default:
		return model.DeliveryClosedParticipation
	}
}

type nativeDeclarationRecord struct {
	Kind          string                          `json:"kind"`
	RequestDigest string                          `json:"request_digest"`
	Gaps          *model.DeclareDeliveryGaps      `json:"gaps"`
	Final         *model.FinalDeliveryDeclaration `json:"final"`
	Receipt       model.DeliveryGapReceipt        `json:"receipt"`
}

func nativeDeliveryRanges(ctx context.Context, tx *sqlxTxWrapper, owner nativeDeliveryOwner) ([]model.SequenceRange, []model.SequenceRange, error) {
	var positions []int64
	if err := tx.Select(ctx, &positions, `SELECT batch_sequence FROM exam_native_delivery_batches WHERE participation_id=? ORDER BY batch_sequence`, owner.ParticipationID); err != nil {
		return nil, nil, err
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
	if err := tx.Select(ctx, &declarations, `SELECT canonical FROM exam_native_delivery_declarations WHERE participation_id=? AND kind='gaps'`, owner.ParticipationID); err != nil {
		return nil, nil, err
	}
	gaps := []model.SequenceRange{}
	for _, raw := range declarations {
		var declaration nativeDeclarationRecord
		if json.Unmarshal(raw, &declaration) != nil || declaration.Gaps == nil || declaration.Gaps.Validate() != nil {
			return nil, nil, invalidPersistedState("native_delivery", "gaps", model.ErrDeliveryInvalid)
		}
		gaps = append(gaps, declaration.Gaps.Ranges...)
	}
	return received, mergeNativeDeliveryRanges(gaps), nil
}

func mergeNativeDeliveryRanges(gaps []model.SequenceRange) []model.SequenceRange {
	slices.SortFunc(gaps, func(a, b model.SequenceRange) int {
		if a.First < b.First {
			return -1
		}
		if a.First > b.First {
			return 1
		}
		return 0
	})
	merged := []model.SequenceRange{}
	for _, gap := range gaps {
		if len(merged) > 0 && merged[len(merged)-1].Last+1 == gap.First {
			merged[len(merged)-1].Last = gap.Last
		} else {
			merged = append(merged, gap)
		}
	}
	return merged
}

func nativeDeliveryStatus(ctx context.Context, tx *sqlxTxWrapper, owner nativeDeliveryOwner, streamID string) (*model.NativeSecurityStreamStatus, error) {
	if err := advanceNativeInterpretation(ctx, tx, owner); err != nil {
		return nil, err
	}
	return projectNativeDeliveryStatus(ctx, tx, owner, streamID)
}

func projectNativeDeliveryStatus(ctx context.Context, tx *sqlxTxWrapper, owner nativeDeliveryOwner, streamID string) (*model.NativeSecurityStreamStatus, error) {
	received, gaps, err := nativeDeliveryRanges(ctx, tx, owner)
	if err != nil {
		return nil, err
	}
	progress, err := model.ResolveDeliveryProgress(owner.Allocated, received, gaps, owner.TerminalThrough)
	if err != nil {
		return nil, err
	}
	status := &model.NativeSecurityStreamStatus{NativeDeliveryProgress: model.NativeDeliveryProgress{HighestContiguousBatchSequence: progress.HighestContiguous, SettledThroughBatchSequence: progress.SettledThrough, HighestSeenBatchSequence: progress.HighestSeen, MissingBatchRanges: progress.Missing, MissingRangesTruncated: progress.MissingTruncated, ServerTime: owner.Now}, StreamID: streamID, AllocatedThroughSequence: owner.Allocated, TerminalMissingThroughSequence: owner.TerminalThrough, Closure: owner.Closure, DeclarationRevision: owner.DeclarationRevision, DetailMode: "collecting"}
	if owner.SummaryOnly || owner.AttemptSummaryOnly {
		status.DetailMode = "summary_only"
		scope := "participation"
		if owner.AttemptSummaryOnly {
			scope = "attempt"
		}
		status.BudgetScope = &scope
		reason := model.DeliveryStopReason(owner.StopReason.String)
		if owner.AttemptSummaryOnly {
			reason = model.DeliveryStopReason(owner.AttemptStopReason.String)
		}
		if reason == "" {
			return nil, invalidPersistedState("native_delivery", "stop_reason", model.ErrDeliveryInvalid)
		}
		status.SummaryOnlyReason = &reason
	}
	if len(owner.SummaryRaw) > 0 {
		var summary model.UnretainedDeliverySummary
		if json.Unmarshal(owner.SummaryRaw, &summary) != nil {
			return nil, model.ErrDeliveryInvalid
		}
		status.Summary = &summary
	}
	if err := status.Validate(); err != nil {
		return nil, err
	}
	return status, nil
}

func (s *sqlExamAttemptStore) NativeDeliveryStatus(ctx context.Context, access store.NativeDeliveryAccess) (*model.NativeSecurityStreamStatus, error) {
	return runSQLTransaction(ctx, s.GetMaster().Begin, "native delivery status", func(ctx context.Context, tx *sqlxTxWrapper) (*model.NativeSecurityStreamStatus, error) {
		owner, err := s.nativeDeliveryOwner(ctx, tx, access, false)
		if err != nil {
			return nil, err
		}
		return projectNativeDeliveryStatus(ctx, tx, owner, access.StreamID)
	})
}
func (s *sqlExamAttemptStore) NativeDeliveryReceipt(ctx context.Context, access store.NativeDeliveryAccess, sequence int64) (*model.NativeBatchReceipt, error) {
	if sequence < 1 || sequence > model.NativeParticipationPositionLimit {
		return nil, store.NewErrInvalidInput("native_delivery", "sequence", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "native delivery receipt", func(ctx context.Context, tx *sqlxTxWrapper) (*model.NativeBatchReceipt, error) {
		owner, err := s.nativeDeliveryOwner(ctx, tx, access, false)
		if err != nil {
			return nil, err
		}
		if owner.Closure.UploadExpiresAt != nil && !owner.Now.Before(*owner.Closure.UploadExpiresAt) {
			return nil, model.ErrDeliveryExpired
		}
		var raw []byte
		if err := tx.Get(ctx, &raw, `SELECT receipt_canonical FROM exam_native_delivery_batches WHERE participation_id=? AND batch_sequence=?`, owner.ParticipationID, sequence); err != nil {
			return nil, translateError("native_receipt", access.StreamID, err)
		}
		var receipt model.NativeBatchReceipt
		if json.Unmarshal(raw, &receipt) != nil || receipt.Validate() != nil {
			return nil, model.ErrDeliveryInvalid
		}
		return &receipt, nil
	})
}

func nativeDeliveryConflict() error {
	return store.NewErrConflict("native_delivery", "declaration_conflict", nil)
}

func nativeDeclarationReplay(ctx context.Context, tx *sqlxTxWrapper, owner nativeDeliveryOwner, id, digest string) (*nativeDeclarationRecord, error) {
	var raw []byte
	err := tx.Get(ctx, &raw, `SELECT canonical FROM exam_native_delivery_declarations WHERE participation_id=? AND declaration_id=?`, owner.ParticipationID, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var record nativeDeclarationRecord
	if json.Unmarshal(raw, &record) != nil {
		return nil, model.ErrDeliveryInvalid
	}
	if record.RequestDigest != digest {
		return nil, nativeDeliveryConflict()
	}
	return &record, nil
}

type nativeDeclarationOutcome struct {
	Refusal  string                            `json:"refusal"`
	Receipt  model.DeliveryGapReceipt          `json:"receipt"`
	Status   *model.NativeSecurityStreamStatus `json:"status"`
	Capacity *model.DeliveryMetadataCapacity   `json:"capacity"`
}

func (s *sqlExamAttemptStore) DeclareNativeDeliveryGaps(ctx context.Context, input *store.NativeDeliveryGapDeclaration, command *store.CommandIdempotency) (*model.DeliveryGapReceipt, error) {
	if input == nil || input.Declaration.Validate() != nil {
		return nil, store.NewErrInvalidInput("native_delivery", "gaps", nil)
	}
	raw, err := input.Declaration.Canonical()
	if err != nil {
		return nil, err
	}
	result, err := s.declareNativeDelivery(ctx, input.Access, input.AuditEventID, input.AuditAt, command, nativeDeclarationRecord{Kind: "gaps", RequestDigest: model.SHA256Fingerprint(raw), Gaps: &input.Declaration})
	if err != nil {
		return nil, err
	}
	if result.Refusal != "" {
		return nil, &store.NativeDeliveryRefusal{Reason: result.Refusal}
	}
	if result.Capacity != nil {
		return nil, result.Capacity
	}
	return &result.Receipt, nil
}
func (s *sqlExamAttemptStore) SealNativeDelivery(ctx context.Context, input *store.NativeDeliveryFinalDeclaration, command *store.CommandIdempotency) (*model.NativeSecurityStreamStatus, error) {
	if input == nil || input.Declaration.Validate() != nil {
		return nil, store.NewErrInvalidInput("native_delivery", "final", nil)
	}
	raw, err := input.Declaration.Canonical()
	if err != nil {
		return nil, err
	}
	result, err := s.declareNativeDelivery(ctx, input.Access, input.AuditEventID, input.AuditAt, command, nativeDeclarationRecord{Kind: "final", RequestDigest: model.SHA256Fingerprint(raw), Final: &input.Declaration})
	if err != nil {
		return nil, err
	}
	if result.Refusal != "" {
		return nil, &store.NativeDeliveryRefusal{Reason: result.Refusal}
	}
	if result.Capacity != nil {
		return nil, result.Capacity
	}
	return result.Status, nil
}

func (s *sqlExamAttemptStore) declareNativeDelivery(ctx context.Context, access store.NativeDeliveryAccess, auditID string, auditAt int64, command *store.CommandIdempotency, record nativeDeclarationRecord) (*nativeDeclarationOutcome, error) {
	if command == nil || command.UserID != access.Access.CandidateUserID || !model.IsValidId(auditID) || auditAt <= 0 {
		return nil, store.NewErrInvalidInput("native_delivery", "declaration", nil)
	}
	declarationID := ""
	if record.Gaps != nil {
		declarationID = record.Gaps.DeclarationID
	} else {
		declarationID = record.Final.DeclarationID
	}
	result, err := runIdempotentMutation(ctx, s.SQLStore, "declare native delivery", idempotentMutation[*nativeDeclarationOutcome]{command: command, auditEventID: auditID,
		execute: func(ctx context.Context, tx *sqlxTxWrapper) (*nativeDeclarationOutcome, error) {
			owner, err := s.lockNativeDelivery(ctx, tx, access)
			if err != nil {
				return nil, err
			}
			if owner.Closure.UploadExpiresAt != nil && !owner.Now.Before(*owner.Closure.UploadExpiresAt) {
				return nil, model.ErrDeliveryExpired
			}
			prior, err := nativeDeclarationReplay(ctx, tx, owner, declarationID, record.RequestDigest)
			if err != nil {
				return nil, err
			}
			if prior != nil {
				if prior.Kind != record.Kind {
					return nil, nativeDeliveryConflict()
				}
				status, err := nativeDeliveryStatus(ctx, tx, owner, access.StreamID)
				if err != nil {
					return nil, err
				}
				if err := completeNativeDeliveryAudit(ctx, tx, auditID, auditAt, access.StreamID, true); err != nil {
					return nil, err
				}
				return &nativeDeclarationOutcome{Receipt: prior.Receipt, Status: status}, nil
			}
			received, gaps, err := nativeDeliveryRanges(ctx, tx, owner)
			if err != nil {
				return nil, err
			}
			progress, err := model.ResolveDeliveryProgress(owner.Allocated, received, gaps, owner.TerminalThrough)
			if err != nil {
				return nil, err
			}
			newAllocated := owner.Allocated
			if record.Gaps != nil {
				if err := model.ValidateDeliveryGapPlacement(true, progress, owner.Allocated, owner.DeclarationRevision, owner.Closure, owner.SummaryOnly || owner.AttemptSummaryOnly, *record.Gaps, received, gaps, owner.Now); err != nil {
					return nil, err
				}
				newAllocated = max(newAllocated, record.Gaps.AllocatedThroughSequence)
			} else {
				closure, delta, err := owner.Closure.DeclareFinal(true, owner.DeclarationRevision, owner.Allocated, owner.SummaryOnly || owner.AttemptSummaryOnly, *record.Final, owner.Now)
				if err != nil {
					return nil, err
				}
				owner.Closure = closure
				newAllocated += delta
			}
			var budget struct {
				Metadata  int64 `db:"control_metadata_bytes"`
				Positions int64 `db:"native_allocated_positions"`
				Intervals int64 `db:"explicit_missing_intervals"`
			}
			if err := tx.Get(ctx, &budget, `SELECT control_metadata_bytes,native_allocated_positions,explicit_missing_intervals FROM exam_attempt_delivery_budgets WHERE exam_attempt_id=?`, access.Access.AttemptID.String()); err != nil {
				return nil, err
			}
			delta := newAllocated - owner.Allocated

			refusal := ""
			if delta > model.NativeParticipationPositionLimit-owner.Allocated || delta > model.NativeAttemptPositionLimit-budget.Positions {
				if err := latchNativeDelivery(ctx, tx, access.Access.AttemptID, owner.ParticipationID, model.DeliveryStopPositions, delta > model.NativeAttemptPositionLimit-budget.Positions); err != nil {
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
				if err := completeNativeDeliveryAudit(ctx, tx, auditID, auditAt, access.StreamID, false); err != nil {
					return nil, err
				}
				return &nativeDeclarationOutcome{Refusal: refusal}, nil
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
					if err := completeNativeDeliveryAudit(ctx, tx, auditID, auditAt, access.StreamID, false); err != nil {
						return nil, err
					}
					return &nativeDeclarationOutcome{Capacity: capacity}, nil
				}
				budget.Metadata = next
			}
			if _, err := tx.Exec(ctx, `INSERT INTO exam_native_delivery_declarations(participation_id,declaration_id,kind,canonical) VALUES(?,?,?,?)`, owner.ParticipationID, declarationID, record.Kind, encoded); err != nil {
				return nil, err
			}
			status, err := nativeDeliveryStatus(ctx, tx, owner, access.StreamID)
			if err != nil {
				return nil, err
			}
			closure, err := canonicalPreflightValue(owner.Closure)
			if err != nil {
				return nil, err
			}
			if _, err := tx.Exec(ctx, `UPDATE exam_attempt_security_owners SET allocated_through_sequence=?,declaration_revision=?,closure_canonical=? WHERE participation_id=?`, owner.Allocated, owner.DeclarationRevision, closure, owner.ParticipationID); err != nil {
				return nil, err
			}
			if _, err := tx.Exec(ctx, `UPDATE exam_attempt_delivery_budgets SET control_metadata_bytes=?,native_allocated_positions=native_allocated_positions+?,explicit_missing_intervals=? WHERE exam_attempt_id=?`, budget.Metadata, delta, budget.Intervals, access.Access.AttemptID.String()); err != nil {
				return nil, err
			}
			if err := completeNativeDeliveryAudit(ctx, tx, auditID, auditAt, access.StreamID, false); err != nil {
				return nil, err
			}
			return &nativeDeclarationOutcome{Receipt: record.Receipt, Status: status}, nil
		},
		encode: func(value *nativeDeclarationOutcome) ([]byte, error) { return canonicalPreflightValue(value) },
		decode: func(version int, raw []byte) (*nativeDeclarationOutcome, error) {
			var result nativeDeclarationOutcome
			if version != 1 {
				return nil, model.ErrDeliveryInvalid
			}
			if err := decodeCommandOutcome(raw, &result); err != nil {
				return nil, err
			}
			return &result, nil
		},
		hydrateReplay: func(ctx context.Context, tx *sqlxTxWrapper, value *nativeDeclarationOutcome) (*nativeDeclarationOutcome, error) {
			owner, err := s.lockNativeDelivery(ctx, tx, access)
			if err != nil {
				return nil, err
			}
			if owner.Closure.UploadExpiresAt != nil && !owner.Now.Before(*owner.Closure.UploadExpiresAt) {
				return nil, model.ErrDeliveryExpired
			}
			if value.Capacity == nil {
				value.Status, err = nativeDeliveryStatus(ctx, tx, owner, access.StreamID)
			}
			return value, err
		},
		completeReplay: func(ctx context.Context, tx *sqlxTxWrapper, value *nativeDeclarationOutcome, original string) error {
			return completeNativeDeliveryAudit(ctx, tx, auditID, auditAt, access.StreamID, true)
		},
	})
	if err != nil {
		return nil, err
	}
	return result.Value, nil
}

func latchDeliveryMetadata(ctx context.Context, tx *sqlxTxWrapper, attemptID model.ExamAttemptID) error {
	if _, err := tx.Exec(ctx, `UPDATE exam_attempt_delivery_budgets SET native_summary_only=true,native_stop_reason=COALESCE(native_stop_reason,'metadata'),browser_summary_only=true,browser_stop_reason=COALESCE(browser_stop_reason,'metadata') WHERE exam_attempt_id=?`, attemptID.String()); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE exam_attempt_security_owners SET terminal_missing_through_sequence=allocated_through_sequence WHERE exam_attempt_id=?`, attemptID.String()); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE browser_activity_sources SET terminal_missing_through_sequence=allocated_through_sequence WHERE exam_attempt_id=?`, attemptID.String())
	return err
}
func completeNativeDeliveryAudit(ctx context.Context, tx *sqlxTxWrapper, id string, at int64, streamID string, replayed bool) error {
	data, err := model.EncodeAuditData(map[string]any{"stream_id": streamID, "idempotency_replayed": replayed})
	if err != nil {
		return err
	}
	_, err = completeAuditEvent(ctx, tx, id, model.AuditStatusSuccess, "", data, at)
	return err
}

func (s *sqlExamAttemptStore) UpdateNativeDeliverySummary(ctx context.Context, input *store.NativeDeliverySummaryUpdate, command *store.CommandIdempotency) (*model.NativeSecurityStreamStatus, error) {
	if input == nil || input.Summary.Validate() != nil || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 || command == nil || command.Operation != store.NativeDeliverySummaryOperation || command.UserID != input.Access.Access.CandidateUserID {
		return nil, store.NewErrInvalidInput("native_delivery", "summary", nil)
	}
	hydrate := func(ctx context.Context, tx *sqlxTxWrapper, _ *model.NativeSecurityStreamStatus) (*model.NativeSecurityStreamStatus, error) {
		owner, err := s.lockNativeDelivery(ctx, tx, input.Access)
		if err != nil {
			return nil, err
		}
		if owner.Closure.UploadExpiresAt != nil && !owner.Now.Before(*owner.Closure.UploadExpiresAt) {
			return nil, model.ErrDeliveryExpired
		}
		if len(owner.SummaryRaw) == 0 {
			return nil, model.ErrDeliveryExpired
		}
		return nativeDeliveryStatus(ctx, tx, owner, input.Access.StreamID)
	}
	result, err := runIdempotentMutation(ctx, s.SQLStore, "native delivery summary", idempotentMutation[*model.NativeSecurityStreamStatus]{command: command, auditEventID: input.AuditEventID,
		execute: func(ctx context.Context, tx *sqlxTxWrapper) (*model.NativeSecurityStreamStatus, error) {
			owner, err := s.lockNativeDelivery(ctx, tx, input.Access)
			if err != nil {
				return nil, err
			}
			if owner.Closure.UploadExpiresAt != nil && !owner.Now.Before(*owner.Closure.UploadExpiresAt) {
				return nil, model.ErrDeliveryExpired
			}
			replay := false
			if len(owner.SummaryRaw) > 0 {
				var prior model.UnretainedDeliverySummary
				if json.Unmarshal(owner.SummaryRaw, &prior) != nil {
					return nil, model.ErrDeliveryInvalid
				}
				replay, err = prior.Compare(input.Summary)
				if err != nil {
					return nil, err
				}
			}
			if !replay {
				final := owner.Closure.FinalSequence != nil && !owner.SummaryFinal
				if owner.SummaryReceivedAt.Valid && owner.Now.Sub(owner.SummaryReceivedAt.Time) < 5*time.Second && !final {
					return nil, store.NewErrConflict("native_delivery", "summary_rate_limited", nil)
				}
				raw, err := input.Summary.Canonical()
				if err != nil {
					return nil, err
				}
				if _, err := tx.Exec(ctx, `UPDATE exam_attempt_security_owners SET summary_canonical=?,summary_received_at=?,summary_final=summary_final OR ? WHERE participation_id=?`, raw, owner.Now, final, owner.ParticipationID); err != nil {
					return nil, err
				}
				owner.SummaryRaw = raw
			}
			if err := completeNativeDeliveryAudit(ctx, tx, input.AuditEventID, input.AuditAt, input.Access.StreamID, replay); err != nil {
				return nil, err
			}
			return nativeDeliveryStatus(ctx, tx, owner, input.Access.StreamID)
		}, encode: func(v *model.NativeSecurityStreamStatus) ([]byte, error) { return canonicalPreflightValue(v) },
		decode: func(version int, raw []byte) (*model.NativeSecurityStreamStatus, error) {
			var v model.NativeSecurityStreamStatus
			if version != 1 {
				return nil, model.ErrDeliveryInvalid
			}
			if err := decodeCommandOutcome(raw, &v); err != nil {
				return nil, err
			}
			return &v, nil
		},
		hydrateReplay: hydrate,
		completeReplay: func(ctx context.Context, tx *sqlxTxWrapper, v *model.NativeSecurityStreamStatus, original string) error {
			return completeNativeDeliveryAudit(ctx, tx, input.AuditEventID, input.AuditAt, input.Access.StreamID, true)
		},
	})
	if err != nil {
		return nil, err
	}
	return result.Value, nil
}

func (s *sqlExamAttemptStore) ResolveNativeDeliveryTarget(ctx context.Context, access store.NativeDeliveryAccess) (*store.NativeDeliveryTarget, error) {
	return runSQLTransaction(ctx, s.GetMaster().Begin, "resolve native delivery target", func(ctx context.Context, tx *sqlxTxWrapper) (*store.NativeDeliveryTarget, error) {
		owner, err := s.nativeDeliveryOwner(ctx, tx, access, false)
		if err != nil {
			return nil, err
		}
		var binding admittedSecurityBinding
		if json.Unmarshal(owner.Binding, &binding) != nil {
			return nil, model.ErrNativeDeliveryInvalid
		}
		var row struct {
			SittingID string `db:"sitting_id"`
			ClassID   string `db:"class_id"`
		}
		if err := tx.Get(ctx, &row, `SELECT s.id AS sitting_id,s.class_id FROM exam_attempts a JOIN exam_sittings s ON s.id=a.exam_sitting_id WHERE a.id=?`, access.Access.AttemptID.String()); err != nil {
			return nil, err
		}
		sittingID, err := model.ParseExamSittingID(row.SittingID)
		if err != nil {
			return nil, err
		}
		classID, err := model.ParseClassID(row.ClassID)
		if err != nil {
			return nil, err
		}
		return &store.NativeDeliveryTarget{SittingID: sittingID, ClassID: classID, Security: binding.Security, MatrixDigest: binding.CapabilityMatrixDigest}, nil
	})
}
