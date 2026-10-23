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

// The rate debit commits separately from the append transaction: replay and a
// refused request consume the same shared User/Attempt allowance. The Attempt
// has exactly one candidate; Session and transport changes cannot refill it.
func (s *SQLExamAttemptStore) debitNativeAppend(ctx context.Context, access store.NativeDeliveryAccess) error {
	allowed, err := runSQLTransaction(ctx, s.GetMaster().Begin, "native append allowance", func(ctx context.Context, tx *sqlxTxWrapper) (bool, error) {
		owner, err := s.lockNativeDelivery(ctx, tx, access)
		if err != nil {
			return false, err
		}
		var rate struct {
			Tokens int64        `db:"native_rate_tokens"`
			At     sql.NullTime `db:"native_rate_at"`
		}
		if err := tx.Get(ctx, &rate, `SELECT native_rate_tokens,native_rate_at FROM exam_attempt_delivery_budgets WHERE exam_attempt_id=?`, access.Access.AttemptID.String()); err != nil {
			return false, err
		}
		if rate.At.Valid {
			rate.Tokens = min(int64(8000), rate.Tokens+min(int64(4000), max(int64(0), owner.Now.Sub(rate.At.Time).Milliseconds()))*2)
		}
		allowed := rate.Tokens >= 1000
		if allowed {
			rate.Tokens -= 1000
		}
		_, err = tx.Exec(ctx, `UPDATE exam_attempt_delivery_budgets SET native_rate_tokens=?,native_rate_at=? WHERE exam_attempt_id=?`, rate.Tokens, owner.Now, access.Access.AttemptID.String())
		return allowed, err
	})
	if err != nil {
		return err
	}
	if !allowed {
		return store.NewErrConflict("native_delivery", "append_rate_limited", nil)
	}
	return nil
}

// nativeAppendOutcome is the committed result of a native append; its
// progress is recomputed on replay and never persisted.
type nativeAppendOutcome struct {
	Capacity *model.DeliveryMetadataCapacity
	Receipt  *model.NativeBatchReceipt
	Refusal  string
	Progress model.NativeDeliveryProgress
}

// nativeAppendOutcomeDocument is the persisted outcome of a native append.
type nativeAppendOutcomeDocument struct {
	Capacity *model.DeliveryMetadataCapacity `json:"capacity"`
	Receipt  *model.NativeBatchReceipt       `json:"receipt"`
	Refusal  string                          `json:"refusal"`
}

func (nativeAppendOutcomeDocument) outcomeDocument() {}

// AppendNativeDelivery appends one native security batch to the Participation's
// delivery stream under a NativeDeliveryAppendOperation command owned by the
// Candidate, debiting the delivery budget first. A replay returns the original
// receipt with current progress, and an expired upload deadline yields
// model.ErrDeliveryExpired.
func (s *SQLExamAttemptStore) AppendNativeDelivery(ctx context.Context, input *store.NativeDeliveryAppend, command *store.CommandIdempotency) (*model.NativeSecurityAcknowledgement, error) {
	if input == nil || input.Batch.Validate() != nil || command == nil || command.UserID != input.Access.Access.CandidateUserID || command.Operation != store.NativeDeliveryAppendOperation || !input.AuditEventID.IsValid() || input.AuditAt.IsZero() || input.Access.StreamID != input.Batch.StreamID {
		return nil, store.NewErrInvalidInput("native_delivery", "batch", nil)
	}
	release, admissionErr := s.enterDeliveryAppend(ctx, "native")
	if admissionErr != nil {
		return nil, admissionErr
	}
	defer release()
	inputCopy := *input
	inputCopy.Access.ParticipationID, inputCopy.Access.Generation = input.Batch.ParticipationID, input.Batch.Generation
	input = &inputCopy
	if err := s.debitNativeAppend(ctx, input.Access); err != nil {
		return nil, err
	}
	raw, err := input.Batch.Canonical()
	if err != nil {
		return nil, err
	}
	digest := model.SHA256Fingerprint(raw)
	result, err := runIdempotentMutation(ctx, s.SQLStore, "append native delivery", idempotentMutation[*nativeAppendOutcome]{command: command, auditEventID: input.AuditEventID,
		execute: func(ctx context.Context, tx *sqlxTxWrapper) (*nativeAppendOutcome, error) {
			return s.executeNativeAppend(ctx, tx, input, raw, digest)
		},
		encode: encodeNativeAppendOutcome,
		decode: decodeNativeAppendOutcome,
		hydrateReplay: func(ctx context.Context, tx *sqlxTxWrapper, value *nativeAppendOutcome) (*nativeAppendOutcome, error) {
			return s.hydrateNativeAppend(ctx, tx, input, digest, value)
		},
		completeReplay: func(ctx context.Context, tx *sqlxTxWrapper, _ *nativeAppendOutcome, _ string) error {
			return completeNativeDeliveryAudit(ctx, tx, input.AuditEventID, input.AuditAt, input.Access.StreamID, true)
		},
	})
	if err != nil {
		return nil, err
	}
	if result.Value.Capacity != nil {
		return nil, result.Value.Capacity
	}
	if result.Value.Refusal != "" {
		return nil, &store.NativeDeliveryRefusal{Reason: result.Value.Refusal}
	}
	return &model.NativeSecurityAcknowledgement{Receipt: *result.Value.Receipt, NativeDeliveryProgress: result.Value.Progress}, nil
}

// encodeNativeAppendOutcome encodes a native append outcome for persistence.
func encodeNativeAppendOutcome(value *nativeAppendOutcome) ([]byte, error) {
	return encodeOutcome(nativeAppendOutcomeDocument{Capacity: value.Capacity, Receipt: value.Receipt, Refusal: value.Refusal})
}

// decodeNativeAppendOutcome decodes a persisted native append outcome.
func decodeNativeAppendOutcome(raw []byte) (*nativeAppendOutcome, error) {
	var document nativeAppendOutcomeDocument
	if decodeOutcome(raw, &document) != nil {
		return nil, model.ErrNativeDeliveryInvalid
	}
	return &nativeAppendOutcome{Capacity: document.Capacity, Receipt: document.Receipt, Refusal: document.Refusal}, nil
}

// hydrateNativeAppend refreshes an append outcome with its retained receipt
// and the stream's current delivery progress.
func (s *SQLExamAttemptStore) hydrateNativeAppend(ctx context.Context, tx *sqlxTxWrapper, input *store.NativeDeliveryAppend, digest string, value *nativeAppendOutcome) (*nativeAppendOutcome, error) {
	owner, err := s.lockNativeDelivery(ctx, tx, input.Access)
	if err != nil {
		return nil, err
	}
	if owner.Closure.UploadExpiresAt != nil && !owner.Now.Before(*owner.Closure.UploadExpiresAt) {
		return nil, model.ErrDeliveryExpired
	}
	if value.Receipt != nil {
		receipt, replayErr := nativeBatchReplay(ctx, tx, owner.ParticipationID, input.Batch.BatchSequence, digest)
		if replayErr != nil {
			return nil, replayErr
		}
		if receipt == nil {
			return nil, model.ErrDeliveryExpired
		}
		value.Receipt = receipt
	}
	status, err := nativeDeliveryStatus(ctx, tx, owner, input.Access.StreamID)
	if err != nil {
		return nil, err
	}
	value.Progress = status.NativeDeliveryProgress
	return value, nil
}

// finishNativeAppend completes the append audit and hydrates the outcome.
func (s *SQLExamAttemptStore) finishNativeAppend(ctx context.Context, tx *sqlxTxWrapper, input *store.NativeDeliveryAppend, digest string, value *nativeAppendOutcome, replay bool) (*nativeAppendOutcome, error) {
	if err := completeNativeDeliveryAudit(ctx, tx, input.AuditEventID, input.AuditAt, input.Batch.StreamID, replay); err != nil {
		return nil, err
	}
	return s.hydrateNativeAppend(ctx, tx, input, digest, value)
}

// executeNativeAppend performs a first native append inside the idempotent
// mutation's transaction.
func (s *SQLExamAttemptStore) executeNativeAppend(ctx context.Context, tx *sqlxTxWrapper, input *store.NativeDeliveryAppend, raw []byte, digest string) (*nativeAppendOutcome, error) {
	owner, err := s.lockNativeDelivery(ctx, tx, input.Access)
	if err != nil {
		return nil, err
	}
	if owner.Closure.UploadExpiresAt != nil && !owner.Now.Before(*owner.Closure.UploadExpiresAt) {
		return nil, model.ErrDeliveryExpired
	}
	binding, resolved, err := resolveNativeDeliveryPolicy(ctx, tx, owner, input.DesktopBuild)
	if err != nil {
		return nil, err
	}
	b, admitted := input.Batch, binding.Security
	if b.SecuritySessionID != admitted.SecuritySessionID || b.PolicyDigest != admitted.Policy.Digest || b.ApplicationReleaseID != admitted.Policy.ApplicationReleaseID || b.MatrixID != admitted.Policy.MatrixID {
		return nil, nativeAppendConflict("batch_conflict")
	}
	prior, err := nativeBatchReplay(ctx, tx, owner.ParticipationID, b.BatchSequence, digest)
	if err != nil {
		return nil, err
	}
	if prior != nil {
		return s.finishNativeAppend(ctx, tx, input, digest, &nativeAppendOutcome{Receipt: prior}, true)
	}
	if slices.ContainsFunc(b.Records, func(record model.NativeRecord) bool { return record.Occurrence != nil }) {
		var retired bool
		if err = tx.Get(ctx, &retired, `SELECT EXISTS(SELECT 1 FROM exam_submissions WHERE exam_attempt_id=? AND integrity_retired_at IS NOT NULL)`, input.Access.Access.AttemptID.String()); err != nil {
			return nil, err
		}
		if retired {
			return nil, model.ErrDeliveryExpired
		}
	}
	if owner.SummaryOnly || owner.AttemptSummaryOnly {
		return s.finishNativeAppend(ctx, tx, input, digest, &nativeAppendOutcome{Refusal: "detail_budget_exhausted"}, false)
	}
	base, err := checkNativeAppendPosition(ctx, tx, owner, b)
	if err != nil {
		return nil, err
	}
	capacity, err := admitNativeBatchSources(ctx, tx, input, owner, resolved)
	if err != nil {
		return nil, err
	}
	if capacity != nil {
		return s.finishNativeAppend(ctx, tx, input, digest, &nativeAppendOutcome{Capacity: capacity}, false)
	}
	outcome, err := retainNativeAppendBatch(ctx, tx, input, &owner, raw, digest, base)
	if err != nil {
		return nil, err
	}
	return s.finishNativeAppend(ctx, tx, input, digest, outcome, false)
}

// checkNativeAppendPosition refuses a batch outside the stream's
// acknowledgement, gap, receive window and closure boundary, returning the
// receive window base.
func checkNativeAppendPosition(ctx context.Context, tx *sqlxTxWrapper, owner nativeDeliveryOwner, b model.NativeSecurityBatch) (int64, error) {
	received, gaps, err := nativeDeliveryRanges(ctx, tx, owner)
	if err != nil {
		return 0, err
	}
	progress, err := model.ResolveDeliveryProgress(owner.Allocated, received, gaps, owner.TerminalThrough)
	if err != nil {
		return 0, invalidPersistedState("native_delivery", "progress", err)
	}
	if b.PriorAcknowledgement > progress.HighestContiguous {
		return 0, nativeAppendConflict("batch_conflict")
	}
	if b.BatchSequence <= owner.TerminalThrough || slices.ContainsFunc(gaps, func(r model.SequenceRange) bool { return r.First <= b.BatchSequence && b.BatchSequence <= r.Last }) {
		return 0, nativeAppendConflict("batch_conflict")
	}
	base := max(progress.HighestContiguous, progress.SettledThrough)
	if b.BatchSequence <= base || b.BatchSequence > base+1024 {
		return 0, nativeAppendConflict("replay_window_exceeded")
	}
	if owner.Closure.ClosedAt != nil {
		boundary := *owner.Closure.KnownAtClose
		if owner.Closure.FinalSequence != nil {
			boundary = *owner.Closure.FinalSequence
		}
		if b.BatchSequence > boundary {
			return 0, nativeAppendConflict("replay_window_exceeded")
		}
	}
	return base, nil
}

// admitNativeBatchSources validates the batch's record sources under the
// native_sources savepoint and returns the metadata capacity it latched when
// the sources exceed it.
func admitNativeBatchSources(ctx context.Context, tx *sqlxTxWrapper, input *store.NativeDeliveryAppend, owner nativeDeliveryOwner, resolved model.ResolvedNativePolicy) (*model.DeliveryMetadataCapacity, error) {
	if _, err := tx.Exec(ctx, `SAVEPOINT native_sources`); err != nil {
		return nil, err
	}
	if err := validateNativeBatchSources(ctx, tx, owner, resolved, input.DesktopBuild.NativeAgreement, input.Batch.Records); err != nil {
		var capacity *model.DeliveryMetadataCapacity
		if !errors.As(err, &capacity) {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT native_sources`); err != nil {
			return nil, err
		}
		if err := latchDeliveryMetadata(ctx, tx, input.Access.Access.AttemptID); err != nil {
			return nil, err
		}
		return capacity, nil
	}
	return nil, nil
}

// nativeAppendBatch is the canonical receipt, envelope and records one native
// append retains with their counted sizes.
type nativeAppendBatch struct {
	receipt     model.NativeBatchReceipt
	receiptRaw  []byte
	envelopeRaw []byte
	records     []nativeStoredRecord
	counted     int64
}

// encodeNativeAppendBatch canonicalizes the receipt, envelope and records of a
// native batch and enforces their counted size limits.
func encodeNativeAppendBatch(b model.NativeSecurityBatch, raw []byte, digest string, now time.Time) (nativeAppendBatch, error) {
	receipt := model.NativeBatchReceipt{StreamID: b.StreamID, BatchSequence: b.BatchSequence, RequestDigest: digest, ReceivedAt: now}
	receiptRaw, err := canonicalPreflightValue(receipt)
	if err != nil {
		return nativeAppendBatch{}, err
	}
	// Retain the envelope once, with each closed record in its owning row.
	var envelope map[string]json.RawMessage
	if err = json.Unmarshal(raw, &envelope); err != nil {
		return nativeAppendBatch{}, err
	}
	delete(envelope, "records")
	envelopeRaw, err := canonicalPreflightValue(envelope)
	if err != nil || len(envelopeRaw) > 2048 {
		return nativeAppendBatch{}, model.ErrNativeDeliveryInvalid
	}
	records := make([]nativeStoredRecord, len(b.Records))
	counted := int64(len(envelopeRaw) + len(receiptRaw))
	metadata := int64(len(receiptRaw))
	for i, record := range b.Records {
		records[i], err = encodeNativeRecord(record, b.BatchSequence, i)
		if err != nil {
			return nativeAppendBatch{}, err
		}
		counted += int64(len(records[i].Raw) + len(records[i].Metadata))
		metadata += int64(len(records[i].Metadata))
	}
	if metadata > 64*1024 || counted > model.DeliveryRepairReservationBytes {
		return nativeAppendBatch{}, model.ErrNativeDeliveryInvalid
	}
	return nativeAppendBatch{receipt: receipt, receiptRaw: receiptRaw, envelopeRaw: envelopeRaw, records: records, counted: counted}, nil
}

// retainNativeAppendBatch charges and stores a native batch, advancing the
// owner's allocation, and returns its receipt or budget refusal outcome.
func retainNativeAppendBatch(ctx context.Context, tx *sqlxTxWrapper, input *store.NativeDeliveryAppend, owner *nativeDeliveryOwner, raw []byte, digest string, base int64) (*nativeAppendOutcome, error) {
	b := input.Batch
	batch, err := encodeNativeAppendBatch(b, raw, digest, owner.Now)
	if err != nil {
		return nil, err
	}
	var budget struct {
		Records   int64 `db:"native_retained_records"`
		Bytes     int64 `db:"native_retained_bytes"`
		Positions int64 `db:"native_allocated_positions"`
		Pending   int64 `db:"native_pending_bytes"`
	}
	if err = tx.Get(ctx, &budget, `SELECT native_retained_records,native_retained_bytes,native_allocated_positions,native_pending_bytes FROM exam_attempt_delivery_budgets WHERE exam_attempt_id=?`, input.Access.Access.AttemptID.String()); err != nil {
		return nil, err
	}
	var part struct {
		Records int64 `db:"retained_records"`
		Bytes   int64 `db:"retained_bytes"`
	}
	if err = tx.Get(ctx, &part, `SELECT retained_records,retained_bytes FROM exam_attempt_security_owners WHERE participation_id=?`, owner.ParticipationID); err != nil {
		return nil, err
	}
	delta := max(int64(0), b.BatchSequence-owner.Allocated)
	pq, aq := model.NewDeliveryQuotaUsage(true, false), model.NewDeliveryQuotaUsage(true, true)
	pq.RetainedRecords, pq.RetainedBytes, pq.AllocatedPositions = part.Records, part.Bytes, owner.Allocated
	aq.RetainedRecords, aq.RetainedBytes, aq.AllocatedPositions = budget.Records, budget.Bytes, budget.Positions
	pq, err = pq.Charge(int64(len(batch.records)), batch.counted, delta)
	if err != nil {
		return nil, err
	}
	aq, err = aq.Charge(int64(len(batch.records)), batch.counted, delta)
	if err != nil {
		return nil, err
	}
	if pq.SummaryOnly || aq.SummaryOnly {
		if _, err := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT native_sources`); err != nil {
			return nil, err
		}
		reason, attempt := pq.StopReason, false
		if aq.SummaryOnly {
			reason, attempt = aq.StopReason, true
		}
		if err := latchNativeDelivery(ctx, tx, input.Access.Access.AttemptID, owner.ParticipationID, *reason, attempt); err != nil {
			return nil, err
		}
		return &nativeAppendOutcome{Refusal: "detail_budget_exhausted"}, nil
	}
	repair := b.BatchSequence == base+1
	if !model.CanRetainPendingDelivery(budget.Pending, part.Bytes, model.DeliveryParticipationByteLimit, batch.counted, repair, !repair) || !model.CanRetainPendingDelivery(budget.Pending, budget.Bytes, model.DeliveryAttemptByteLimit, batch.counted, repair, !repair) {
		return nil, nativeAppendConflict("pending_capacity")
	}
	if err := storeNativeAppendBatch(ctx, tx, input, owner, batch, delta, pq, aq); err != nil {
		return nil, err
	}
	// Pending records cannot become authoritative projections ahead of a gap.
	// Validate their known neighbors now, then interpret only the settled prefix.
	if err := validateNativeOccurrenceNeighbors(ctx, tx, *owner, batch.records); err != nil {
		return nil, err
	}
	if err := advanceNativeInterpretation(ctx, tx, *owner); err != nil {
		return nil, err
	}
	return &nativeAppendOutcome{Receipt: &batch.receipt}, nil
}

// storeNativeAppendBatch inserts the batch and its records and writes the
// charged allocation and quotas, advancing the owner's allocation.
func storeNativeAppendBatch(ctx context.Context, tx *sqlxTxWrapper, input *store.NativeDeliveryAppend, owner *nativeDeliveryOwner, batch nativeAppendBatch, delta int64, pq, aq model.DeliveryQuotaUsage) error {
	b := input.Batch
	if _, err := tx.Exec(ctx, `INSERT INTO exam_native_delivery_batches(participation_id,batch_sequence,request_digest,received_at,receipt_canonical,envelope_canonical,counted_bytes) VALUES(?,?,?,?,?,?,?)`, owner.ParticipationID, b.BatchSequence, batch.receipt.RequestDigest, owner.Now, batch.receiptRaw, batch.envelopeRaw, batch.counted); err != nil {
		return err
	}
	for _, r := range batch.records {
		if _, err := tx.Exec(ctx, `INSERT INTO exam_native_delivery_records(participation_id,batch_sequence,record_index,kind,record_canonical,metadata_canonical) VALUES(?,?,?,?,?,?)`, owner.ParticipationID, b.BatchSequence, r.Index, r.Kind, r.Raw, r.Metadata); err != nil {
			return err
		}
	}
	owner.Allocated += delta
	if _, err := tx.Exec(ctx, `UPDATE exam_attempt_security_owners SET allocated_through_sequence=?,retained_records=?,retained_bytes=? WHERE participation_id=?`, owner.Allocated, pq.RetainedRecords, pq.RetainedBytes, owner.ParticipationID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE exam_attempt_delivery_budgets SET native_allocated_positions=?,native_retained_records=?,native_retained_bytes=?,native_pending_bytes=native_pending_bytes+? WHERE exam_attempt_id=?`, aq.AllocatedPositions, aq.RetainedRecords, aq.RetainedBytes, batch.counted, input.Access.Access.AttemptID.String()); err != nil {
		return err
	}
	return nil
}

func nativeAppendConflict(reason string) error {
	return store.NewErrConflict("native_delivery", reason, nil)
}
func nativeBatchReplay(ctx context.Context, tx *sqlxTxWrapper, part string, seq int64, digest string) (*model.NativeBatchReceipt, error) {
	var row struct {
		Digest string `db:"request_digest"`
		Raw    []byte `db:"receipt_canonical"`
	}
	err := tx.Get(ctx, &row, `SELECT request_digest,receipt_canonical FROM exam_native_delivery_batches WHERE participation_id=? AND batch_sequence=?`, part, seq)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if row.Digest != digest {
		return nil, nativeAppendConflict("batch_conflict")
	}
	var receipt model.NativeBatchReceipt
	if json.Unmarshal(row.Raw, &receipt) != nil || receipt.Validate() != nil || receipt.RequestDigest != digest || receipt.BatchSequence != seq {
		return nil, invalidPersistedState("native_delivery", "receipt", model.ErrNativeDeliveryInvalid)
	}
	return &receipt, nil
}

func resolveNativeDeliveryPolicy(ctx context.Context, tx *sqlxTxWrapper, owner nativeDeliveryOwner, build model.DesktopBuildTuple) (admittedSecurityBinding, model.ResolvedNativePolicy, error) {
	var binding admittedSecurityBinding
	var zero model.ResolvedNativePolicy
	if json.Unmarshal(owner.Binding, &binding) != nil || binding.Security.Validate() != nil {
		return binding, zero, invalidPersistedState("native_delivery", "binding", model.ErrNativeDeliveryInvalid)
	}
	if build.NativeAgreement == nil || build.NativeAgreement.MatrixDigest() != binding.CapabilityMatrixDigest {
		return binding, zero, model.ErrNativeDeliveryInvalid
	}
	policy := binding.Security.Policy
	var examString string
	if err := tx.Get(ctx, &examString, `SELECT exam_id FROM exam_attempts WHERE id=?`, policy.Scope.AttemptID.String()); err != nil {
		return binding, zero, err
	}
	examID, err := model.ParseExamID(examString)
	if err != nil {
		return binding, zero, err
	}
	revision, err := getExamRevisionSnapshot(ctx, tx, examID, policy.ExamRevisionID)
	if err != nil {
		return binding, zero, err
	}
	selections, err := model.DecodeExamPolicySet(revision.Policy.Bytes)
	if err != nil {
		return binding, zero, err
	}
	resolved, err := model.ResolveNativePolicy(model.NativePolicyResolution{PolicyID: policy.PolicyID, Revision: policy.Revision, Ordinal: policy.Ordinal, InstitutionID: policy.InstitutionID, ExamRevisionID: policy.ExamRevisionID, SittingID: policy.SittingID, Scope: policy.Scope, IssuedAt: policy.IssuedAt, ActiveFrom: policy.ActiveFrom, ActivationTime: owner.Now, Selections: selections, Build: build})
	if err != nil {
		return binding, zero, err
	}
	if resolved.Policy.Digest != policy.Digest || resolved.PolicyContentDigest != binding.Security.PolicyContentDigest {
		return binding, zero, invalidPersistedState("native_delivery", "policy", model.ErrNativeDeliveryInvalid)
	}
	return binding, resolved, nil
}

func latchNativeDelivery(ctx context.Context, tx *sqlxTxWrapper, attemptID model.ExamAttemptID, part string, reason model.DeliveryStopReason, attempt bool) error {
	if attempt {
		if _, err := tx.Exec(ctx, `UPDATE exam_attempt_delivery_budgets SET native_summary_only=true,native_stop_reason=COALESCE(native_stop_reason,?) WHERE exam_attempt_id=?`, string(reason), attemptID.String()); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE exam_attempt_security_owners SET terminal_missing_through_sequence=allocated_through_sequence WHERE exam_attempt_id=?`, attemptID.String())
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE exam_attempt_security_owners SET summary_only=true,stop_reason=COALESCE(stop_reason,?),terminal_missing_through_sequence=allocated_through_sequence WHERE participation_id=?`, string(reason), part)
	return err
}
