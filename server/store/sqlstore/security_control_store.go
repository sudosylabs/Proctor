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
	"errors"
	"time"

	"github.com/sudosylabs/proctor/server/internal/canonicaljson"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type nativeResetRecord struct {
	ParticipationID model.AttemptParticipationID `json:"participation_id"`
	Reset           model.NativeSourceReset      `json:"reset"`
	Digest          string                       `json:"digest"`
}

// Historical records use the native batch bound; live controls keep their smaller outer bound.
const nativeResetRecordMaxBytes = 256*1024 + 512

func encodeNativeResetRecord(record nativeResetRecord) ([]byte, error) {
	raw, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	return canonicaljson.Canonicalize(raw, nativeResetRecordMaxBytes)
}

func decodeNativeResetRecord(raw []byte, participationID model.AttemptParticipationID, resetID string) (nativeResetRecord, error) {
	var record nativeResetRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return record, invalidPersistedState("security_reset", "record", err)
	}
	canonical, err := record.Reset.Canonical()
	if err != nil || record.ParticipationID != participationID || !participationID.IsValid() || (resetID != "" && record.Reset.ResetID != resetID) || record.Digest != model.SHA256Fingerprint(canonical) {
		return record, invalidPersistedState("security_reset", "record", model.ErrSecurityControlInvalid)
	}
	return record, nil
}

// The initial retained value is a full preflight report; accepted renewals retain
// only these facts. Both representations use the same domain validation.
type nativeCoverageSnapshot struct {
	Sources  []model.NativeSourceCoverage `json:"sources"`
	Coverage []model.NativeCoverageClaim  `json:"coverage"`
}

func decodeNativeCoverageSnapshot(raw []byte) (nativeCoverageSnapshot, error) {
	var snapshot nativeCoverageSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return snapshot, invalidPersistedState("security_owner", "latest_coverage", err)
	}
	if err := model.ValidateNativeCoverageSnapshot(snapshot.Sources, snapshot.Coverage); err != nil {
		return snapshot, invalidPersistedState("security_owner", "latest_coverage", err)
	}
	return snapshot, nil
}

type nativeControlOwnerRow struct {
	Binding        []byte `db:"binding_canonical"`
	LatestCoverage []byte `db:"latest_report_canonical"`
	Ledger         []byte `db:"control_ledger_canonical"`
	Allowed        bool   `db:"security_interaction_allowed"`
	Allocated      int64  `db:"allocated_through_sequence"`
	Acknowledged   int64  `db:"acknowledged_through_sequence"`
	SummaryOnly    bool   `db:"summary_only"`
}

// securityControlBudget is the locked Attempt delivery budget a security
// control charges.
type securityControlBudget struct {
	Metadata    int64 `db:"control_metadata_bytes"`
	Positions   int64 `db:"native_allocated_positions"`
	SummaryOnly bool  `db:"native_summary_only"`
}

// processSecurityCoverage runs under the existing Attempt/Participation/Session
// locks. It deliberately does not renew or expire the Participation lease.
func (s *SQLExamAttemptStore) processSecurityCoverage(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamAttemptParticipationRenewal, sittingOpen bool, now time.Time) (model.SecurityCoverageResult, error) {
	var zero model.SecurityCoverageResult
	control := input.SecurityCoverage
	raw, err := control.Canonical()
	if err != nil {
		return zero, store.NewErrInvalidInput("security_control", "body", nil)
	}
	digest := model.SHA256Fingerprint(raw)
	budget, owner, binding, err := lockSecurityControlOwner(ctx, tx, input)
	if err != nil {
		return zero, err
	}
	admitted := binding.Security
	resolved, reasons, err := resolveSecurityControlPolicy(ctx, tx, input, admitted, now)
	if err != nil {
		return zero, err
	}
	if err = checkSecurityControlSources(control, resolved, admitted, owner); err != nil {
		return zero, err
	}
	browserSources, err := lockBrowserControlSources(ctx, tx, input)
	if err != nil {
		return zero, err
	}
	var ledger nativeControlLedger
	if err = json.Unmarshal(owner.Ledger, &ledger); err != nil || ledger.Validate() != nil {
		return zero, invalidPersistedState("security_owner", "control_ledger", model.ErrSecurityControlInvalid)
	}
	receipt, processed, err := ledger.Lookup(control.ControlSequence, digest)
	if err != nil {
		return zero, store.NewErrConflict("security_control", "control_conflict", nil)
	}
	if processed {
		return projectSecurityControl(ctx, tx, input.AttemptID, input.ParticipationID, admitted.DeliveryStreamID, ledger, receipt, owner.Allowed, sittingOpen)
	}
	continuity, err := resolveSecurityControlContinuity(ctx, tx, input, owner.LatestCoverage)
	if err != nil {
		return zero, err
	}
	records, err := reserveNativeResetRecords(ctx, tx, input, &budget, &continuity)
	if err != nil {
		return zero, err
	}
	outcome := securityControlReceipt{Sequence: control.ControlSequence, Digest: digest, Result: continuity.Result, ResetIDs: continuity.ReceiptIDs, Rejections: [][2]int64{}}
	if err = applySecurityControlWatermarks(ctx, tx, input, browserSources, &owner, &budget, &outcome); err != nil {
		return zero, err
	}
	ledger, err = storeSecurityControl(ctx, tx, input, raw, records, continuity, reasons, &owner, budget, ledger, outcome)
	if err != nil {
		return zero, err
	}
	return projectSecurityControl(ctx, tx, input.AttemptID, input.ParticipationID, admitted.DeliveryStreamID, ledger, &outcome, owner.Allowed, sittingOpen)
}

// lockSecurityControlOwner locks the Attempt delivery budget and the security
// owner and checks the control against the owner's admitted binding.
func lockSecurityControlOwner(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamAttemptParticipationRenewal) (securityControlBudget, nativeControlOwnerRow, admittedSecurityBinding, error) {
	control := input.SecurityCoverage
	var budget securityControlBudget
	if err := tx.Get(ctx, &budget, `SELECT control_metadata_bytes,native_allocated_positions,native_summary_only FROM exam_attempt_delivery_budgets WHERE exam_attempt_id=? FOR UPDATE`, input.AttemptID.String()); err != nil {
		return securityControlBudget{}, nativeControlOwnerRow{}, admittedSecurityBinding{}, err
	}
	var owner nativeControlOwnerRow
	if err := tx.Get(ctx, &owner, `SELECT binding_canonical,latest_report_canonical,control_ledger_canonical,security_interaction_allowed,allocated_through_sequence,acknowledged_through_sequence,summary_only FROM exam_attempt_security_owners WHERE participation_id=? AND exam_attempt_id=? AND session_id=? AND registration_id=? AND key_thumbprint=? FOR UPDATE`, input.ParticipationID.String(), input.AttemptID.String(), input.SessionID.String(), input.DesktopRegistrationID.String(), input.DPoPKeyThumbprint); err != nil {
		return securityControlBudget{}, nativeControlOwnerRow{}, admittedSecurityBinding{}, translateError("security_owner", input.ParticipationID.String(), err)
	}
	var binding admittedSecurityBinding
	if err := json.Unmarshal(owner.Binding, &binding); err != nil {
		return securityControlBudget{}, nativeControlOwnerRow{}, admittedSecurityBinding{}, invalidPersistedState("security_owner", "binding", err)
	}
	admitted := binding.Security
	if admitted.Validate() != nil || admitted.Generation != input.Generation || admitted.Policy.Scope.AttemptID != input.AttemptID {
		return securityControlBudget{}, nativeControlOwnerRow{}, admittedSecurityBinding{}, invalidPersistedState("security_owner", "binding", model.ErrSecurityPolicyInvalid)
	}
	if control.PolicyDigest != admitted.Policy.Digest || control.StreamID != admitted.DeliveryStreamID || control.SecuritySessionID != admitted.SecuritySessionID {
		return securityControlBudget{}, nativeControlOwnerRow{}, admittedSecurityBinding{}, preflightConflict("policy_changed")
	}
	if input.DesktopBuild.NativeAgreement == nil || input.DesktopBuild.NativeAgreement.MatrixDigest() != binding.CapabilityMatrixDigest {
		return securityControlBudget{}, nativeControlOwnerRow{}, admittedSecurityBinding{}, preflightConflict("configuration_unsupported")
	}
	return budget, owner, binding, nil
}

// resolveSecurityControlPolicy re-resolves the admitted native policy for the
// reporting build and evaluates the control's coverage against it.
func resolveSecurityControlPolicy(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamAttemptParticipationRenewal, admitted model.AdmittedSecurity, now time.Time) (model.ResolvedNativePolicy, []model.SecurityReasonCode, error) {
	control := input.SecurityCoverage
	// Scope and registry are immutable for this owner. Live corrections do not
	// silently rewrite its source lifetime; the correction/reentry owner fences it.
	var examIDString string
	if err := tx.Get(ctx, &examIDString, `SELECT exam_id FROM exam_attempts WHERE id=?`, input.AttemptID.String()); err != nil {
		return model.ResolvedNativePolicy{}, nil, err
	}
	examID, err := model.ParseExamID(examIDString)
	if err != nil {
		return model.ResolvedNativePolicy{}, nil, err
	}
	revision, err := getExamRevisionSnapshot(ctx, tx, examID, admitted.Policy.ExamRevisionID)
	if err != nil {
		return model.ResolvedNativePolicy{}, nil, err
	}
	selections, err := model.DecodeExamPolicySet(revision.Policy.Bytes)
	if err != nil {
		return model.ResolvedNativePolicy{}, nil, err
	}
	policy := admitted.Policy
	resolved, err := model.ResolveNativePolicy(model.NativePolicyResolution{PolicyID: policy.PolicyID, Revision: policy.Revision, Ordinal: policy.Ordinal, InstitutionID: policy.InstitutionID, ExamRevisionID: policy.ExamRevisionID, SittingID: policy.SittingID, Scope: policy.Scope, IssuedAt: policy.IssuedAt, ActiveFrom: policy.ActiveFrom, ActivationTime: now.UTC().Truncate(time.Millisecond), Selections: selections, Build: input.DesktopBuild})
	if err != nil {
		return model.ResolvedNativePolicy{}, nil, err
	}
	if resolved.Policy.Digest != policy.Digest || resolved.PolicyContentDigest != admitted.PolicyContentDigest {
		return model.ResolvedNativePolicy{}, nil, preflightConflict("policy_changed")
	}
	reasons, err := model.EvaluateNativeCoverage(resolved, control.Sources, control.Coverage, control.Posture)
	if err != nil {
		return model.ResolvedNativePolicy{}, nil, store.NewErrInvalidInput("security_control", "coverage", nil)
	}
	return resolved, reasons, nil
}

// checkSecurityControlSources requires every reset to name a resolved source
// and every native watermark to name the owner's stream within its
// acknowledgement.
func checkSecurityControlSources(control model.SecurityCoverageRenewal, resolved model.ResolvedNativePolicy, admitted model.AdmittedSecurity, owner nativeControlOwnerRow) error {
	for _, reset := range control.SourceResets {
		owned := false
		for _, source := range resolved.Sources {
			if reset.SourceID == source {
				owned = true
			}
		}
		if !owned {
			return store.NewErrNotFound("security_source", string(reset.SourceID))
		}
	}
	// Native ownership exists at admission. Browser watermarks are installed by
	// their separate source owner; no caller-supplied selector establishes one.
	for _, watermark := range control.DeliveryWatermarks {
		if watermark.Family == "browser" {
			continue
		}
		if watermark.SourceID != admitted.DeliveryStreamID {
			return store.NewErrNotFound("delivery_source", watermark.SourceID)
		}
		if watermark.AcknowledgedThroughSequence > owner.Acknowledged {
			return store.NewErrInvalidInput("security_control", "acknowledgement", nil)
		}
	}
	return nil
}

// resolveSecurityControlContinuity resolves the control's source continuity
// against the retained heads and reset history, fencing resets below retained
// evidence.
func resolveSecurityControlContinuity(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamAttemptParticipationRenewal, latestCoverage []byte) (model.NativeContinuityDecision, error) {
	control := input.SecurityCoverage
	heads, err := decodeNativeCoverageSnapshot(latestCoverage)
	if err != nil {
		return model.NativeContinuityDecision{}, err
	}
	var historyRaw [][]byte
	if err := tx.Select(ctx, &historyRaw, `SELECT reset_canonical FROM exam_native_source_resets WHERE participation_id=? ORDER BY reset_id`, input.ParticipationID.String()); err != nil {
		return model.NativeContinuityDecision{}, err
	}
	history := make([]model.NativeSourceReset, 0, len(historyRaw))
	for _, encoded := range historyRaw {
		record, err := decodeNativeResetRecord(encoded, input.ParticipationID, "")
		if err != nil {
			return model.NativeContinuityDecision{}, err
		}
		history = append(history, record.Reset)
	}

	// Detailed evidence can arrive before its corresponding healthy control.
	// A reset must fence every sequence already retained for the predecessor.
	resetBelowEvidence := false
	for _, reset := range control.SourceResets {
		var observed int64
		if err := tx.Get(ctx, &observed, `SELECT COALESCE(MAX((r->>'last_sequence')::bigint),0) FROM exam_native_delivery_records d CROSS JOIN LATERAL jsonb_array_elements(convert_from(d.metadata_canonical,'UTF8')::jsonb->'source_ranges') r WHERE d.participation_id=? AND r->>'source_id'=? AND r->>'source_instance_id'=?`, input.ParticipationID.String(), string(reset.SourceID), reset.PreviousSourceInstanceID); err != nil {
			return model.NativeContinuityDecision{}, err
		}
		if reset.PreviousFinalSequence < observed {
			resetBelowEvidence = true
		}
	}
	continuity, err := model.ResolveNativeSourceContinuity(heads.Sources, control.Sources, history, control.SourceResets)
	if err != nil {
		return model.NativeContinuityDecision{}, store.NewErrInvalidInput("security_control", "source_ownership", nil)
	}
	if resetBelowEvidence {
		continuity = model.NativeContinuityDecision{Result: "reset_conflict", Heads: heads.Sources, NewResets: []model.NativeSourceReset{}, ReceiptIDs: []string{}}
	}
	return continuity, nil
}

// reserveNativeResetRecords encodes the new reset records and reserves their
// metadata, downgrading the continuity to reset_required when capacity is
// exhausted.
func reserveNativeResetRecords(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamAttemptParticipationRenewal, budget *securityControlBudget, continuity *model.NativeContinuityDecision) ([][]byte, error) {
	records := make([][]byte, 0, len(continuity.NewResets))
	additional := int64(0)
	for _, reset := range continuity.NewResets {
		canonical, err := reset.Canonical()
		if err != nil {
			return nil, err
		}
		record, err := encodeNativeResetRecord(nativeResetRecord{ParticipationID: input.ParticipationID, Reset: reset, Digest: model.SHA256Fingerprint(canonical)})
		if err != nil || len(record) > nativeResetRecordMaxBytes {
			return nil, store.NewErrInvalidInput("security_control", "reset_size", nil)
		}
		records = append(records, record)
		additional += int64(len(record))
	}
	if additional > 0 {
		next, reservationErr := model.ReserveDeliveryMetadata(budget.Metadata, additional)
		if reservationErr != nil {
			var capacity *model.DeliveryMetadataCapacity
			if !errors.As(reservationErr, &capacity) {
				return nil, reservationErr
			}
			if err := latchDeliveryMetadata(ctx, tx, input.AttemptID); err != nil {
				return nil, err
			}
			budget.SummaryOnly = true
			continuity.Result = "reset_required"
			continuity.ReceiptIDs = []string{}
			continuity.NewResets = nil
			records = nil
		} else {
			budget.Metadata = next
		}
	}
	return records, nil
}

// applySecurityControlWatermarks applies each delivery watermark, charging
// native positions to the owner and budget and recording rejections.
func applySecurityControlWatermarks(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamAttemptParticipationRenewal, browserSources map[string]browserControlSource, owner *nativeControlOwnerRow, budget *securityControlBudget, outcome *securityControlReceipt) error {
	for _, watermark := range input.SecurityCoverage.DeliveryWatermarks {
		if watermark.Family == "browser" {
			rejection, err := applyBrowserControlWatermark(ctx, tx, input, watermark, browserSources[watermark.SourceID])
			if err != nil {
				return err
			}
			if rejection != nil {
				outcome.Rejections = append(outcome.Rejections, *rejection)
			}
			continue
		}
		delta := watermark.AllocatedThroughSequence - owner.Allocated
		if delta <= 0 {
			continue
		}
		if owner.SummaryOnly || budget.SummaryOnly || delta > model.NativeParticipationPositionLimit-owner.Allocated || delta > model.NativeAttemptPositionLimit-budget.Positions {
			outcome.Rejections = append(outcome.Rejections, [2]int64{0, 1})
			if delta > model.NativeAttemptPositionLimit-budget.Positions {
				budget.SummaryOnly = true
			} else {
				owner.SummaryOnly = true
			}
			continue
		}
		owner.Allocated = watermark.AllocatedThroughSequence
		budget.Positions += delta
	}
	return nil
}

// storeSecurityControl inserts the new resets, records the control in the
// ledger and writes the owner and budget, returning the updated ledger.
func storeSecurityControl(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamAttemptParticipationRenewal, raw []byte, records [][]byte, continuity model.NativeContinuityDecision, reasons []model.SecurityReasonCode, owner *nativeControlOwnerRow, budget securityControlBudget, ledger nativeControlLedger, outcome securityControlReceipt) (nativeControlLedger, error) {
	control := input.SecurityCoverage
	for i, record := range records {
		if _, err := tx.Exec(ctx, `INSERT INTO exam_native_source_resets(participation_id,reset_id,reset_canonical) VALUES(?,?,?)`, input.ParticipationID.String(), continuity.NewResets[i].ResetID, record); err != nil {
			return nativeControlLedger{}, err
		}
	}
	owner.Allowed = continuity.Result == "accepted" && len(reasons) == 0
	if continuity.Result == "accepted" {
		var err error
		owner.LatestCoverage, err = canonicalPreflightValue(nativeCoverageSnapshot{Sources: control.Sources, Coverage: control.Coverage})
		if err != nil || len(owner.LatestCoverage) > model.SecurityControlMaxBytes {
			return nativeControlLedger{}, store.NewErrInvalidInput("security_control", "coverage_size", nil)
		}
	}
	ledger, err := ledger.Record(outcome)
	if err != nil {
		return nativeControlLedger{}, err
	}
	ledgerJSON, err := json.Marshal(ledger)
	if err != nil {
		return nativeControlLedger{}, err
	}
	ledgerRaw, err := canonicaljson.Canonicalize(ledgerJSON, model.SecurityControlCacheMaxBytes+512)
	if err != nil {
		return nativeControlLedger{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE exam_attempt_security_owners SET latest_report_canonical=?,control_ledger_canonical=?,control_body_canonical=?,security_interaction_allowed=?,allocated_through_sequence=?,summary_only=?,stop_reason=CASE WHEN ? THEN COALESCE(stop_reason,'positions') ELSE stop_reason END,terminal_missing_through_sequence=CASE WHEN ? THEN ? ELSE terminal_missing_through_sequence END WHERE participation_id=?`, owner.LatestCoverage, ledgerRaw, raw, owner.Allowed, owner.Allocated, owner.SummaryOnly, owner.SummaryOnly, owner.SummaryOnly || budget.SummaryOnly, owner.Allocated, input.ParticipationID.String()); err != nil {
		return nativeControlLedger{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE exam_attempt_delivery_budgets SET control_metadata_bytes=?,native_allocated_positions=?,native_summary_only=?,native_stop_reason=CASE WHEN ? THEN COALESCE(native_stop_reason,'positions') ELSE native_stop_reason END WHERE exam_attempt_id=?`, budget.Metadata, budget.Positions, budget.SummaryOnly, budget.SummaryOnly, input.AttemptID.String()); err != nil {
		return nativeControlLedger{}, err
	}
	if budget.SummaryOnly {
		if _, err := tx.Exec(ctx, `UPDATE exam_attempt_security_owners SET terminal_missing_through_sequence=allocated_through_sequence WHERE exam_attempt_id=?`, input.AttemptID.String()); err != nil {
			return nativeControlLedger{}, err
		}
	}
	return ledger, nil
}
func projectSecurityControl(ctx context.Context, tx *sqlxTxWrapper, attemptID model.ExamAttemptID, participationID model.AttemptParticipationID, streamID string, ledger nativeControlLedger, receipt *securityControlReceipt, allowed, sittingOpen bool) (model.SecurityCoverageResult, error) {
	result := model.SecurityCoverageResult{ProcessedControlSequence: ledger.ProcessedSequence, ProcessedControlDigest: ledger.ProcessedDigest, CoverageResult: "stale_control", SourceResetReceipts: []model.SourceResetReceipt{}, SecurityInteractionAllowed: allowed && sittingOpen, DeliveryWatermarkRejections: []model.DeliveryWatermarkRejection{}}
	if receipt != nil {
		result.CoverageResult = receipt.Result
		for _, id := range receipt.ResetIDs {
			var raw []byte
			if err := tx.Get(ctx, &raw, `SELECT reset_canonical FROM exam_native_source_resets WHERE participation_id=? AND reset_id=?`, participationID.String(), id); err != nil {
				return result, err
			}
			record, err := decodeNativeResetRecord(raw, participationID, id)
			if err != nil {
				return result, err
			}
			result.SourceResetReceipts = append(result.SourceResetReceipts, model.SourceResetReceipt{ResetID: id, ResetDigest: record.Digest})
		}
		for _, rejection := range receipt.Rejections {
			family, sourceID := "native", streamID
			if rejection[0] != 0 {
				family = "browser"
				if err := tx.Get(ctx, &sourceID, `SELECT id::text FROM browser_activity_sources WHERE participation_id=? AND start_ordinal=? UNION ALL SELECT source_session_id::text FROM browser_delivery_retired_sources WHERE participation_id=? AND start_ordinal=?`, participationID.String(), rejection[0], participationID.String(), rejection[0]); err != nil {
					return result, err
				}
			}
			reason := "closed_source"
			if rejection[1] == 1 {
				reason = "position_limit"
			}
			result.DeliveryWatermarkRejections = append(result.DeliveryWatermarkRejections, model.DeliveryWatermarkRejection{Family: family, SourceID: sourceID, Reason: reason})
		}
	}
	return result, nil
}

// UpdateSecurityCoverage records the security coverage reported over the
// Candidate's open Connection, applying the same Participation, Connection, and
// credential checks as RenewParticipation without extending the lease.
func (s *SQLExamAttemptStore) UpdateSecurityCoverage(ctx context.Context, input *store.ExamAttemptSecurityCoverageUpdate) (model.SecurityCoverageResult, error) {
	if input == nil {
		return model.SecurityCoverageResult{}, store.NewErrInvalidInput("security_control", "update", nil)
	}
	result, err := s.changeParticipationControl(ctx, &store.ExamAttemptParticipationRenewal{AttemptID: input.Access.AttemptID, ParticipationID: input.ParticipationID, ConnectionID: input.Access.ConnectionID, CandidateUserID: input.Access.CandidateUserID, SessionID: input.Access.SessionID, DesktopRegistrationID: input.Access.DesktopRegistrationID, DPoPKeyThumbprint: input.Access.DPoPKeyThumbprint, Generation: input.Generation, ContinuityCredentialHash: input.Access.ContinuityCredentialHash, DesktopCompatibilityPolicyRevision: input.DesktopCompatibilityPolicyRevision, DesktopBuild: input.DesktopBuild, SecurityCoverage: input.Coverage}, false)
	if err != nil {
		return model.SecurityCoverageResult{}, err
	}
	return result.SecurityCoverage, nil
}

func recoverNativeControl(ctx context.Context, tx *sqlxTxWrapper, attemptID model.ExamAttemptID, participationID model.AttemptParticipationID, streamID string, sittingOpen bool) (*store.SecurityPolicyRecovery, error) {
	var owner nativeControlOwnerRow
	if err := tx.Get(ctx, &owner, `SELECT latest_report_canonical,control_ledger_canonical,security_interaction_allowed FROM exam_attempt_security_owners WHERE participation_id=? FOR SHARE`, participationID.String()); err != nil {
		return nil, err
	}
	heads, err := decodeNativeCoverageSnapshot(owner.LatestCoverage)
	if err != nil {
		return nil, err
	}
	var ledger nativeControlLedger
	if err := json.Unmarshal(owner.Ledger, &ledger); err != nil || ledger.Validate() != nil {
		return nil, invalidPersistedState("security_owner", "control_ledger", model.ErrSecurityControlInvalid)
	}
	var latest *securityControlReceipt
	if len(ledger.Receipts) > 0 {
		value := ledger.Receipts[len(ledger.Receipts)-1]
		latest = &value
	}
	result, err := projectSecurityControl(ctx, tx, attemptID, participationID, streamID, ledger, latest, owner.Allowed, sittingOpen)
	if err != nil {
		return nil, err
	}
	if ledger.ProcessedSequence == 0 {
		result.CoverageResult = "accepted"
	}
	recovery := &store.SecurityPolicyRecovery{CurrentSources: heads.Sources, CurrentCoverage: heads.Coverage, SourceResetReceipts: []model.SourceResetReceipt{}, SecurityCoverage: result}
	var rawResets [][]byte
	if err := tx.Select(ctx, &rawResets, `SELECT reset_canonical FROM exam_native_source_resets WHERE participation_id=?`, participationID.String()); err != nil {
		return nil, err
	}
	records := make([]nativeResetRecord, 0, len(rawResets))
	for _, raw := range rawResets {
		record, err := decodeNativeResetRecord(raw, participationID, "")
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	for _, source := range heads.Sources {
		for _, record := range records {
			if record.Reset.SourceID == source.SourceID && record.Reset.NewSourceInstanceID == source.SourceInstanceID {
				recovery.SourceResetReceipts = append(recovery.SourceResetReceipts, model.SourceResetReceipt{ResetID: record.Reset.ResetID, ResetDigest: record.Digest})
				break
			}
		}
	}
	return recovery, nil
}

// lockSecurityInteraction is called only by mutation owners, after
// their Attempt lock. Recovery, monitoring, and renewal never use this gate.
func lockSecurityInteraction(ctx context.Context, tx *sqlxTxWrapper, attemptID model.ExamAttemptID) error {
	var allowed bool
	err := tx.Get(ctx, &allowed, `SELECT o.security_interaction_allowed AND p.lease_expires_at>clock_timestamp() AS allowed FROM exam_attempt_security_owners o JOIN exam_attempt_participations p ON p.id=o.participation_id WHERE o.exam_attempt_id=? AND p.state='active' FOR SHARE OF o,p`, attemptID.String())
	if err != nil {
		return translateError("security_owner", attemptID.String(), err)
	}
	if !allowed {
		return preflightConflict("posture_blocked")
	}
	return nil
}
