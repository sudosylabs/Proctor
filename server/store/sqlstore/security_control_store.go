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
type nativeControlOwnerRow struct {
	Binding        []byte `db:"binding_canonical"`
	LatestCoverage []byte `db:"latest_report_canonical"`
	Ledger         []byte `db:"control_ledger_canonical"`
	Allowed        bool   `db:"security_interaction_allowed"`
	FreezeRequired bool   `db:"freeze_required"`
	Allocated      int64  `db:"allocated_through_sequence"`
	Acknowledged   int64  `db:"acknowledged_through_sequence"`
	SummaryOnly    bool   `db:"summary_only"`
}

// processSecurityCoverage runs under the existing Attempt/Participation/Session
// locks. It deliberately does not renew or expire the Participation lease.
func (s *sqlExamAttemptStore) processSecurityCoverage(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamAttemptParticipationRenewal, sittingOpen bool, now time.Time) (model.SecurityCoverageResult, error) {
	var zero model.SecurityCoverageResult
	control := input.SecurityCoverage
	raw, err := control.Canonical()
	if err != nil {
		return zero, store.NewErrInvalidInput("security_control", "body", nil)
	}
	digest := model.SHA256Fingerprint(raw)
	var budget struct {
		Metadata    int64 `db:"control_metadata_bytes"`
		Positions   int64 `db:"native_allocated_positions"`
		SummaryOnly bool  `db:"native_summary_only"`
	}
	if err := tx.Get(ctx, &budget, `SELECT control_metadata_bytes,native_allocated_positions,native_summary_only FROM exam_attempt_delivery_budgets WHERE exam_attempt_id=? FOR UPDATE`, input.AttemptID.String()); err != nil {
		return zero, err
	}
	var owner nativeControlOwnerRow
	if err := tx.Get(ctx, &owner, `SELECT binding_canonical,latest_report_canonical,control_ledger_canonical,security_interaction_allowed,freeze_required,allocated_through_sequence,acknowledged_through_sequence,summary_only FROM exam_attempt_security_owners WHERE participation_id=? AND exam_attempt_id=? AND session_id=? AND registration_id=? AND key_thumbprint=? FOR UPDATE`, input.ParticipationID.String(), input.AttemptID.String(), input.SessionID.String(), input.DesktopRegistrationID.String(), input.DPoPKeyThumbprint); err != nil {
		return zero, translateError("security_owner", input.ParticipationID.String(), err)
	}
	var binding admittedSecurityBinding
	if err := json.Unmarshal(owner.Binding, &binding); err != nil {
		return zero, invalidPersistedState("security_owner", "binding", err)
	}
	admitted := binding.Security
	if admitted.Validate() != nil || admitted.Generation != input.Generation || admitted.Policy.Scope.AttemptID != input.AttemptID {
		return zero, invalidPersistedState("security_owner", "binding", model.ErrSecurityPolicyInvalid)
	}
	if control.PolicyDigest != admitted.Policy.Digest || control.StreamID != admitted.DeliveryStreamID || control.SecuritySessionID != admitted.SecuritySessionID {
		return zero, preflightConflict("policy_changed")
	}
	if input.DesktopBuild.NativeAgreement == nil || input.DesktopBuild.NativeAgreement.MatrixDigest() != binding.CapabilityMatrixDigest {
		return zero, preflightConflict("configuration_unsupported")
	}
	// Scope and registry are immutable for this owner. Live corrections do not
	// silently rewrite its source lifetime; the correction/reentry owner fences it.
	var examIDString string
	if err := tx.Get(ctx, &examIDString, `SELECT exam_id FROM exam_attempts WHERE id=?`, input.AttemptID.String()); err != nil {
		return zero, err
	}
	examID, err := model.ParseExamID(examIDString)
	if err != nil {
		return zero, err
	}
	revision, err := getExamRevisionSnapshot(ctx, tx, examID, admitted.Policy.ExamRevisionID)
	if err != nil {
		return zero, err
	}
	selections, err := model.DecodeExamPolicySet(revision.Policy.Bytes)
	if err != nil {
		return zero, err
	}
	policy := admitted.Policy
	resolved, err := model.ResolveNativePolicy(model.NativePolicyResolution{PolicyID: policy.PolicyID, Revision: policy.Revision, Ordinal: policy.Ordinal, InstitutionID: policy.InstitutionID, ExamRevisionID: policy.ExamRevisionID, SittingID: policy.SittingID, Scope: policy.Scope, IssuedAt: policy.IssuedAt, ActiveFrom: policy.ActiveFrom, ActivationTime: now.UTC().Truncate(time.Millisecond), Selections: selections, Build: input.DesktopBuild})
	if err != nil {
		return zero, err
	}
	if resolved.Policy.Digest != policy.Digest || resolved.PolicyContentDigest != admitted.PolicyContentDigest {
		return zero, preflightConflict("policy_changed")
	}
	reasons, err := model.EvaluateNativeCoverage(resolved, control.Sources, control.Coverage, control.Posture)
	if err != nil {
		return zero, store.NewErrInvalidInput("security_control", "coverage", nil)
	}
	for _, reset := range control.SourceResets {
		owned := false
		for _, source := range resolved.Sources {
			if reset.SourceID == source {
				owned = true
			}
		}
		if !owned {
			return zero, store.NewErrNotFound("security_source", string(reset.SourceID))
		}
	}
	// Native ownership exists at admission. Browser watermarks are installed by
	// their separate source owner; no caller-supplied selector establishes one.
	for _, watermark := range control.DeliveryWatermarks {
		if watermark.Family == "browser" {
			continue
		}
		if watermark.SourceID != admitted.DeliveryStreamID {
			return zero, store.NewErrNotFound("delivery_source", watermark.SourceID)
		}
		if watermark.AcknowledgedThroughSequence > owner.Acknowledged {
			return zero, store.NewErrInvalidInput("security_control", "acknowledgement", nil)
		}
	}
	browserSources, err := lockBrowserControlSources(ctx, tx, input)
	if err != nil {
		return zero, err
	}
	var ledger model.NativeControlLedger
	if err := json.Unmarshal(owner.Ledger, &ledger); err != nil || ledger.Validate() != nil {
		return zero, invalidPersistedState("security_owner", "control_ledger", model.ErrSecurityControlInvalid)
	}
	receipt, processed, err := ledger.Lookup(control.ControlSequence, digest)
	if err != nil {
		return zero, store.NewErrConflict("security_control", "control_conflict", nil)
	}
	if processed {
		return projectSecurityControl(ctx, tx, input.AttemptID, input.ParticipationID, admitted.DeliveryStreamID, ledger, receipt, owner.Allowed, owner.FreezeRequired, sittingOpen)
	}
	var heads struct {
		Sources  []model.NativeSourceCoverage `json:"sources"`
		Coverage []model.NativeCoverageClaim  `json:"coverage"`
	}
	if err := json.Unmarshal(owner.LatestCoverage, &heads); err != nil {
		return zero, invalidPersistedState("security_owner", "latest_coverage", err)
	}
	var historyRaw [][]byte
	if err := tx.Select(ctx, &historyRaw, `SELECT reset_canonical FROM exam_native_source_resets WHERE participation_id=? ORDER BY reset_id`, input.ParticipationID.String()); err != nil {
		return zero, err
	}
	history := make([]model.NativeSourceReset, 0, len(historyRaw))
	for _, encoded := range historyRaw {
		var record nativeResetRecord
		if err := json.Unmarshal(encoded, &record); err != nil || record.Reset.Validate() != nil || record.ParticipationID != input.ParticipationID {
			return zero, invalidPersistedState("security_reset", "record", model.ErrSecurityControlInvalid)
		}
		history = append(history, record.Reset)
	}

	// Detailed evidence can arrive before its corresponding healthy control.
	// A reset must fence every sequence already retained for the predecessor.
	resetBelowEvidence := false
	for _, reset := range control.SourceResets {
		var observed int64
		if err := tx.Get(ctx, &observed, `SELECT COALESCE(MAX((r->>'last_sequence')::bigint),0) FROM exam_native_delivery_records d CROSS JOIN LATERAL jsonb_array_elements(convert_from(d.metadata_canonical,'UTF8')::jsonb->'source_ranges') r WHERE d.participation_id=? AND r->>'source_id'=? AND r->>'source_instance_id'=?`, input.ParticipationID.String(), string(reset.SourceID), reset.PreviousSourceInstanceID); err != nil {
			return zero, err
		}
		if reset.PreviousFinalSequence < observed {
			resetBelowEvidence = true
		}
	}
	continuity, err := model.ResolveNativeSourceContinuity(heads.Sources, control.Sources, history, control.SourceResets)
	if err != nil {
		return zero, store.NewErrInvalidInput("security_control", "source_ownership", nil)
	}
	if resetBelowEvidence {
		continuity = model.NativeContinuityDecision{Result: "reset_conflict", Heads: heads.Sources, NewResets: []model.NativeSourceReset{}, ReceiptIDs: []string{}}
	}
	records := make([][]byte, 0, len(continuity.NewResets))
	additional := int64(0)
	for _, reset := range continuity.NewResets {
		canonical, err := reset.Canonical()
		if err != nil {
			return zero, err
		}
		record, err := canonicalPreflightValue(nativeResetRecord{ParticipationID: input.ParticipationID, Reset: reset, Digest: model.SHA256Fingerprint(canonical)})
		if err != nil || len(record) > 2048 {
			return zero, store.NewErrInvalidInput("security_control", "reset_size", nil)
		}
		records = append(records, record)
		additional += int64(len(record))
	}
	if additional > 0 {
		next, reservationErr := model.ReserveDeliveryMetadata(budget.Metadata, additional)
		if reservationErr != nil {
			var capacity *model.DeliveryMetadataCapacity
			if !errors.As(reservationErr, &capacity) {
				return zero, reservationErr
			}
			if err := latchDeliveryMetadata(ctx, tx, input.AttemptID); err != nil {
				return zero, err
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
	outcome := model.SecurityControlReceipt{Sequence: control.ControlSequence, Digest: digest, Result: continuity.Result, ResetIDs: continuity.ReceiptIDs, Rejections: [][2]int64{}}
	for _, watermark := range control.DeliveryWatermarks {
		if watermark.Family == "browser" {
			rejection, err := applyBrowserControlWatermark(ctx, tx, input, watermark, browserSources[watermark.SourceID])
			if err != nil {
				return zero, err
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
	for i, record := range records {
		if _, err := tx.Exec(ctx, `INSERT INTO exam_native_source_resets(participation_id,reset_id,reset_canonical) VALUES(?,?,?)`, input.ParticipationID.String(), continuity.NewResets[i].ResetID, record); err != nil {
			return zero, err
		}
	}
	owner.Allowed = continuity.Result == "accepted" && len(reasons) == 0
	var grants struct {
		Exists bool `db:"exists"`
		Fenced bool `db:"fenced"`
	}
	if err := tx.Get(ctx, &grants, `SELECT EXISTS(SELECT 1 FROM execution_grants WHERE exam_attempt_id=$1 AND state IN ('reserved','ready') AND revoked_at IS NULL) AS exists,
 EXISTS(SELECT 1 FROM execution_grants WHERE exam_attempt_id=$1 AND state IN ('reserved','ready') AND revoked_at IS NULL AND environment_epoch<>'') AS fenced`, input.AttemptID.String()); err != nil {
		return zero, err
	}
	// The fenced protocol can recover the original occupancy. Native readiness
	// remains independent of the host acknowledgement; execution input additionally
	// requires its current confirmed control. Legacy guests retain protective release.
	if grants.Fenced && owner.Allowed {
		owner.FreezeRequired = false
	}
	if !owner.Allowed && grants.Exists {
		owner.FreezeRequired = true
	}
	if owner.FreezeRequired {
		owner.Allowed = false
	}
	if continuity.Result == "accepted" {
		owner.LatestCoverage, err = canonicalPreflightValue(struct {
			Sources  []model.NativeSourceCoverage `json:"sources"`
			Coverage []model.NativeCoverageClaim  `json:"coverage"`
		}{control.Sources, control.Coverage})
		if err != nil || len(owner.LatestCoverage) > model.SecurityControlMaxBytes {
			return zero, store.NewErrInvalidInput("security_control", "coverage_size", nil)
		}
	}
	ledger, err = ledger.Record(outcome)
	if err != nil {
		return zero, err
	}
	ledgerJSON, err := json.Marshal(ledger)
	if err != nil {
		return zero, err
	}
	ledgerRaw, err := canonicaljson.Canonicalize(ledgerJSON, model.SecurityControlCacheMaxBytes+512)
	if err != nil {
		return zero, err
	}
	if _, err := tx.Exec(ctx, `UPDATE exam_attempt_security_owners SET latest_report_canonical=?,control_ledger_canonical=?,control_body_canonical=?,security_interaction_allowed=?,freeze_required=?,allocated_through_sequence=?,summary_only=?,stop_reason=CASE WHEN ? THEN COALESCE(stop_reason,'positions') ELSE stop_reason END,terminal_missing_through_sequence=CASE WHEN ? THEN ? ELSE terminal_missing_through_sequence END WHERE participation_id=?`, owner.LatestCoverage, ledgerRaw, raw, owner.Allowed, owner.FreezeRequired, owner.Allocated, owner.SummaryOnly, owner.SummaryOnly, owner.SummaryOnly || budget.SummaryOnly, owner.Allocated, input.ParticipationID.String()); err != nil {
		return zero, err
	}
	if _, err := tx.Exec(ctx, `UPDATE exam_attempt_delivery_budgets SET control_metadata_bytes=?,native_allocated_positions=?,native_summary_only=?,native_stop_reason=CASE WHEN ? THEN COALESCE(native_stop_reason,'positions') ELSE native_stop_reason END WHERE exam_attempt_id=?`, budget.Metadata, budget.Positions, budget.SummaryOnly, budget.SummaryOnly, input.AttemptID.String()); err != nil {
		return zero, err
	}
	if budget.SummaryOnly {
		if _, err := tx.Exec(ctx, `UPDATE exam_attempt_security_owners SET terminal_missing_through_sequence=allocated_through_sequence WHERE exam_attempt_id=?`, input.AttemptID.String()); err != nil {
			return zero, err
		}
	}
	return projectSecurityControl(ctx, tx, input.AttemptID, input.ParticipationID, admitted.DeliveryStreamID, ledger, &outcome, owner.Allowed, owner.FreezeRequired, sittingOpen)
}
func projectSecurityControl(ctx context.Context, tx *sqlxTxWrapper, attemptID model.ExamAttemptID, participationID model.AttemptParticipationID, streamID string, ledger model.NativeControlLedger, receipt *model.SecurityControlReceipt, allowed, freezeRequired, sittingOpen bool) (model.SecurityCoverageResult, error) {
	result := model.SecurityCoverageResult{ProcessedControlSequence: ledger.ProcessedSequence, ProcessedControlDigest: ledger.ProcessedDigest, CoverageResult: "stale_control", SourceResetReceipts: []model.SourceResetReceipt{}, SecurityInteractionAllowed: allowed && sittingOpen, ExecutionState: "not_allocated", DeliveryWatermarkRejections: []model.DeliveryWatermarkRejection{}}
	var grantRow executionGrantRow
	grantErr := tx.Get(ctx, &grantRow, `SELECT `+executionGrantColumns+` FROM execution_grants
  WHERE exam_attempt_id=? AND state IN ('reserved','ready') AND revoked_at IS NULL`, attemptID.String())
	if grantErr != nil && !errors.Is(grantErr, sql.ErrNoRows) {
		return result, grantErr
	}
	if grantErr == nil {
		grant, err := executionGrantModel(grantRow)
		if err != nil {
			return result, err
		}
		result.ExecutionState = "unavailable"
		if grant.EnvironmentEpoch == "" {
			// v0.2.0 remains on the existing protective release path.
			if freezeRequired {
				result.ExecutionState = "freeze_pending"
			} else if grant.State == model.ExecutionGrantReady && !grant.LifecyclePending {
				result.ExecutionState = "ready"
			}
		} else {
			authority, err := readExecutionControlAuthority(ctx, tx, attemptID)
			if err != nil {
				return result, err
			}
			desired := authority.desired(grant)
			confirmed := grant.ControlAcknowledgedRevision == grant.ControlRevision && grant.DesiredControlState == desired && grant.ControlAuthorityDigest == authority.digest()
			switch desired {
			case model.ExecutionControlFrozen:
				result.ExecutionState = "freeze_pending"
				if confirmed {
					result.ExecutionState = "frozen"
				}
			case model.ExecutionControlRunning:
				result.ExecutionState = "thaw_pending"
				if confirmed && grant.State == model.ExecutionGrantReady && !grant.WorkspacePending && !grant.LifecyclePending {
					result.ExecutionState = "ready"
				}
			}
		}
	}
	if receipt != nil {
		result.CoverageResult = receipt.Result
		for _, id := range receipt.ResetIDs {
			var raw []byte
			if err := tx.Get(ctx, &raw, `SELECT reset_canonical FROM exam_native_source_resets WHERE participation_id=? AND reset_id=?`, participationID.String(), id); err != nil {
				return result, err
			}
			var record nativeResetRecord
			if err := json.Unmarshal(raw, &record); err != nil {
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

func (s *sqlExamAttemptStore) UpdateSecurityCoverage(ctx context.Context, input *store.ExamAttemptSecurityCoverageUpdate) (model.SecurityCoverageResult, error) {
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
	if err := tx.Get(ctx, &owner, `SELECT latest_report_canonical,control_ledger_canonical,security_interaction_allowed,freeze_required FROM exam_attempt_security_owners WHERE participation_id=? FOR SHARE`, participationID.String()); err != nil {
		return nil, err
	}
	var heads struct {
		Sources  []model.NativeSourceCoverage `json:"sources"`
		Coverage []model.NativeCoverageClaim  `json:"coverage"`
	}
	if err := json.Unmarshal(owner.LatestCoverage, &heads); err != nil {
		return nil, err
	}
	var ledger model.NativeControlLedger
	if err := json.Unmarshal(owner.Ledger, &ledger); err != nil || ledger.Validate() != nil {
		return nil, invalidPersistedState("security_owner", "control_ledger", model.ErrSecurityControlInvalid)
	}
	var latest *model.SecurityControlReceipt
	if len(ledger.Receipts) > 0 {
		value := ledger.Receipts[len(ledger.Receipts)-1]
		latest = &value
	}
	result, err := projectSecurityControl(ctx, tx, attemptID, participationID, streamID, ledger, latest, owner.Allowed, owner.FreezeRequired, sittingOpen)
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
		var record nativeResetRecord
		if err := json.Unmarshal(raw, &record); err != nil {
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

// lockSecurityInteraction is called only by mutation/execution owners, after
// their Attempt lock. Recovery, monitoring, and renewal never use this gate.
func lockSecurityInteraction(ctx context.Context, tx *sqlxTxWrapper, attemptID model.ExamAttemptID) error {
	var allowed bool
	err := tx.Get(ctx, &allowed, `SELECT o.security_interaction_allowed AND NOT o.freeze_required AND p.lease_expires_at>clock_timestamp() AS allowed FROM exam_attempt_security_owners o JOIN exam_attempt_participations p ON p.id=o.participation_id WHERE o.exam_attempt_id=? AND p.state='active' FOR SHARE OF o,p`, attemptID.String())
	if err != nil {
		return translateError("security_owner", attemptID.String(), err)
	}
	if !allowed {
		return preflightConflict("posture_blocked")
	}
	return nil
}
