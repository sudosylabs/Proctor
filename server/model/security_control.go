// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"time"
)

const SecurityControlMaxBytes = 64 * 1024

var (
	ErrSecurityControlInvalid  = errors.New("security control is invalid")
	ErrSecurityControlConflict = errors.New("security control sequence has conflicting content")
)

type NativeSourceReset struct {
	occurredAtSpelling string

	Kind                     string         `json:"kind"`
	ResetID                  string         `json:"reset_id"`
	SourceID                 NativeSourceID `json:"source_id"`
	PreviousSourceInstanceID string         `json:"previous_source_instance_id"`
	PreviousFinalSequence    int64          `json:"previous_final_sequence"`
	NewSourceInstanceID      string         `json:"new_source_instance_id"`
	Reason                   string         `json:"reason"`
	OccurredAt               time.Time      `json:"occurred_at"`
}

func (r NativeSourceReset) Validate() error {
	if r.Kind != "source_reset" || !IsValidAgreementID(r.ResetID) || !slices.Contains(NativeSources(), r.SourceID) || !IsValidAgreementID(r.PreviousSourceInstanceID) || !IsValidAgreementID(r.NewSourceInstanceID) || r.PreviousSourceInstanceID == r.NewSourceInstanceID || !securitySafeInt(r.PreviousFinalSequence) || !slices.Contains([]string{"restart", "resume", "permission_changed"}, r.Reason) || !securityInstant(r.OccurredAt) {
		return ErrSecurityControlInvalid
	}
	return nil
}
func (r NativeSourceReset) Canonical() ([]byte, error) {
	if r.Validate() != nil {
		return nil, ErrSecurityControlInvalid
	}
	return encodeCanonicalExamDocument(r)
}

type DeliveryWatermark struct {
	Family                      string `json:"family"`
	SourceID                    string `json:"source_id"`
	AllocatedThroughSequence    int64  `json:"allocated_through_sequence"`
	AcknowledgedThroughSequence int64  `json:"acknowledged_through_sequence"`
}
type SecurityCoverageRenewal struct {
	ControlSequence    int64                  `json:"control_sequence"`
	PolicyDigest       string                 `json:"policy_digest"`
	SecuritySessionID  string                 `json:"security_session_id"`
	StreamID           string                 `json:"stream_id"`
	Posture            string                 `json:"posture"`
	Sources            []NativeSourceCoverage `json:"sources"`
	Coverage           []NativeCoverageClaim  `json:"coverage"`
	SourceResets       []NativeSourceReset    `json:"source_resets"`
	DeliveryWatermarks []DeliveryWatermark    `json:"delivery_watermarks"`
}

func (c SecurityCoverageRenewal) Validate() error {
	if c.ControlSequence <= 0 || !securitySafeInt(c.ControlSequence) || !IsValidSHA256Fingerprint(c.PolicyDigest) || !IsValidAgreementID(c.SecuritySessionID) || !IsValidAgreementID(c.StreamID) || !slices.Contains([]string{"checking", "compliant", "degraded", "contained", "failed"}, c.Posture) || ValidateNativeCoverageSnapshot(c.Sources, c.Coverage) != nil || c.SourceResets == nil || len(c.SourceResets) > len(NativeSources()) || c.DeliveryWatermarks == nil || len(c.DeliveryWatermarks) > 50 {
		return ErrSecurityControlInvalid
	}
	previous := -1
	for i, reset := range c.SourceResets {
		order := slices.Index(NativeSources(), reset.SourceID)
		if reset.Validate() != nil || order <= previous {
			return ErrSecurityControlInvalid
		}
		previous = order
		for _, prior := range c.SourceResets[:i] {
			if prior.ResetID == reset.ResetID {
				return ErrSecurityControlInvalid
			}
		}
	}
	for i, mark := range c.DeliveryWatermarks {
		if !slices.Contains([]string{"native", "browser"}, mark.Family) || (mark.Family == "native" && !IsValidAgreementID(mark.SourceID) || mark.Family == "browser" && !BrowserSourceSessionID(mark.SourceID).IsValid()) || !securitySafeInt(mark.AllocatedThroughSequence) || !securitySafeInt(mark.AcknowledgedThroughSequence) || mark.AcknowledgedThroughSequence > mark.AllocatedThroughSequence {
			return ErrSecurityControlInvalid
		}
		for _, prior := range c.DeliveryWatermarks[:i] {
			if prior.Family == mark.Family && prior.SourceID == mark.SourceID {
				return ErrSecurityControlInvalid
			}
		}
	}
	raw, err := encodeCanonicalExamDocument(c)
	if err != nil || len(raw) > SecurityControlMaxBytes {
		return ErrSecurityControlInvalid
	}
	return nil
}
func (c SecurityCoverageRenewal) Canonical() ([]byte, error) {
	if c.Validate() != nil {
		return nil, ErrSecurityControlInvalid
	}
	return encodeCanonicalExamDocument(c)
}
func (c *SecurityCoverageRenewal) UnmarshalJSON(raw []byte) error {
	if c == nil || len(raw) > SecurityControlMaxBytes || validateExamDocumentJSON(raw) != nil {
		return ErrSecurityControlInvalid
	}
	type wire SecurityCoverageRenewal
	var value wire
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&value) != nil {
		return ErrSecurityControlInvalid
	}
	candidate := SecurityCoverageRenewal(value)
	expected, err := candidate.Canonical()
	if err != nil {
		return err
	}
	actual, err := encodeCanonicalExamRaw(raw)
	if err != nil || !bytes.Equal(actual, expected) {
		return ErrSecurityControlInvalid
	}
	*c = candidate
	return nil
}

type SourceResetReceipt struct {
	ResetID     string `json:"reset_id"`
	ResetDigest string `json:"reset_digest"`
}
type DeliveryWatermarkRejection struct {
	Family   string `json:"family"`
	SourceID string `json:"source_id"`
	Reason   string `json:"reason"`
}
type SecurityCoverageResult struct {
	ProcessedControlSequence    int64                        `json:"processed_control_sequence"`
	ProcessedControlDigest      *string                      `json:"processed_control_digest"`
	CoverageResult              string                       `json:"coverage_result"`
	SourceResetReceipts         []SourceResetReceipt         `json:"source_reset_receipts"`
	SecurityInteractionAllowed  bool                         `json:"security_interaction_allowed"`
	ExecutionState              string                       `json:"execution_state"`
	DeliveryWatermarkRejections []DeliveryWatermarkRejection `json:"delivery_watermark_rejections"`
}

// SecurityControlReceipt is compact persistence, not a transport response.
// Reset IDs refer to separately charged immutable reset facts. Rejections use
// immutable owner-slot indexes (native=0, browser=1..49) and reason 0=closed,
// 1=position_limit. The owning aggregate resolves these references on replay.
type SecurityControlReceipt struct {
	Sequence   int64      `json:"sequence"`
	Digest     string     `json:"digest"`
	Result     string     `json:"result"`
	ResetIDs   []string   `json:"resets"`
	Rejections [][2]int64 `json:"rejections"`
}

func (r SecurityControlReceipt) Validate() error {
	if r.Sequence <= 0 || !securitySafeInt(r.Sequence) || !IsValidSHA256Fingerprint(r.Digest) || !slices.Contains([]string{"accepted", "reset_required", "reset_conflict"}, r.Result) || r.ResetIDs == nil || len(r.ResetIDs) > 11 || r.Rejections == nil || len(r.Rejections) > 50 {
		return ErrSecurityControlInvalid
	}
	for i, id := range r.ResetIDs {
		if !IsValidAgreementID(id) || slices.Contains(r.ResetIDs[:i], id) {
			return ErrSecurityControlInvalid
		}
	}
	for i, rejection := range r.Rejections {
		if rejection[0] < 0 || rejection[0] > 49 || rejection[1] < 0 || rejection[1] > 1 {
			return ErrSecurityControlInvalid
		}
		for _, prior := range r.Rejections[:i] {
			if prior[0] == rejection[0] {
				return ErrSecurityControlInvalid
			}
		}
	}
	raw, err := encodeCanonicalExamDocument(r)
	if err != nil || len(raw) > SecurityControlReceiptMaxBytes {
		return ErrSecurityControlInvalid
	}
	return nil
}
func (r SecurityControlReceipt) clone() SecurityControlReceipt {
	r.ResetIDs = slices.Clone(r.ResetIDs)
	r.Rejections = slices.Clone(r.Rejections)
	return r
}

// NativeControlLedger keeps processed ordering independently of usable coverage.
// Only authenticated, structurally valid, owner-bound controls reach this owner.
// Its receipt does not grant authority: callers always project current gates.
type NativeControlLedger struct {
	ProcessedSequence int64                    `json:"processed_sequence"`
	ProcessedDigest   *string                  `json:"processed_digest"`
	Receipts          []SecurityControlReceipt `json:"receipts"`
}

func NewNativeControlLedger() NativeControlLedger {
	return NativeControlLedger{Receipts: []SecurityControlReceipt{}}
}
func (l NativeControlLedger) Validate() error {
	if !securitySafeInt(l.ProcessedSequence) || l.Receipts == nil || len(l.Receipts) > SecurityControlReceiptLimit || (l.ProcessedSequence == 0) != (l.ProcessedDigest == nil) {
		return ErrSecurityControlInvalid
	}
	if l.ProcessedSequence == 0 {
		if len(l.Receipts) != 0 {
			return ErrSecurityControlInvalid
		}
		return nil
	}
	if !IsValidSHA256Fingerprint(*l.ProcessedDigest) || len(l.Receipts) == 0 {
		return ErrSecurityControlInvalid
	}
	previous := int64(0)
	for _, receipt := range l.Receipts {
		if receipt.Validate() != nil || receipt.Sequence <= previous || receipt.Sequence > l.ProcessedSequence {
			return ErrSecurityControlInvalid
		}
		previous = receipt.Sequence
	}
	last := l.Receipts[len(l.Receipts)-1]
	if last.Sequence != l.ProcessedSequence || last.Digest != *l.ProcessedDigest {
		return ErrSecurityControlInvalid
	}
	return nil
}

// Lookup runs before interpreting reset edges or watermarks. A retained replay
// returns its original bounded outcome; an evicted older sequence is stale.
func (l NativeControlLedger) Lookup(sequence int64, digest string) (*SecurityControlReceipt, bool, error) {
	if l.Validate() != nil || sequence <= 0 || !securitySafeInt(sequence) || !IsValidSHA256Fingerprint(digest) {
		return nil, false, ErrSecurityControlInvalid
	}
	for _, receipt := range l.Receipts {
		if receipt.Sequence == sequence {
			if receipt.Digest != digest {
				return nil, false, ErrSecurityControlConflict
			}
			copy := receipt.clone()
			return &copy, true, nil
		}
	}
	return nil, sequence <= l.ProcessedSequence, nil
}

// Record advances every processed outcome, including reset faults. It cannot
// modify a previously processed boundary, even after cache eviction or restart.
func (l NativeControlLedger) Record(receipt SecurityControlReceipt) (NativeControlLedger, error) {
	if l.Validate() != nil || receipt.Validate() != nil || receipt.Sequence <= l.ProcessedSequence {
		return l, ErrSecurityControlInvalid
	}
	digest := receipt.Digest
	next := NativeControlLedger{ProcessedSequence: receipt.Sequence, ProcessedDigest: &digest, Receipts: make([]SecurityControlReceipt, 0, SecurityControlReceiptLimit)}
	start := 0
	if len(l.Receipts) == SecurityControlReceiptLimit {
		start = 1
	}
	for _, prior := range l.Receipts[start:] {
		next.Receipts = append(next.Receipts, prior.clone())
	}
	next.Receipts = append(next.Receipts, receipt.clone())
	return next, nil
}

// NativeContinuityDecision is interpreted only inside the locked security owner.
// History contains immutable, separately charged reset facts for this owner.
// A fault retains all previous heads; it cannot partially establish new lifetime.
type NativeContinuityDecision struct {
	Result     string
	Heads      []NativeSourceCoverage
	NewResets  []NativeSourceReset
	ReceiptIDs []string
}

func ResolveNativeSourceContinuity(heads, current []NativeSourceCoverage, history, resets []NativeSourceReset) (NativeContinuityDecision, error) {
	decision := NativeContinuityDecision{Result: "accepted", Heads: slices.Clone(current), NewResets: []NativeSourceReset{}, ReceiptIDs: []string{}}
	fault := func(result string) (NativeContinuityDecision, error) {
		return NativeContinuityDecision{Result: result, Heads: slices.Clone(heads), NewResets: []NativeSourceReset{}, ReceiptIDs: []string{}}, nil
	}
	if len(heads) != len(current) || len(heads) > len(NativeSources()) || len(resets) > len(heads) {
		return NativeContinuityDecision{}, ErrSecurityControlInvalid
	}
	for i, head := range heads {
		if head.SourceID != current[i].SourceID {
			return NativeContinuityDecision{}, ErrSecurityControlInvalid
		}
	}
	for _, reset := range resets {
		if reset.Validate() != nil {
			return NativeContinuityDecision{}, ErrSecurityControlInvalid
		}
		if !slices.ContainsFunc(heads, func(head NativeSourceCoverage) bool { return head.SourceID == reset.SourceID }) {
			return NativeContinuityDecision{}, ErrSecurityControlInvalid
		}
	}
	for i, head := range heads {
		next := current[i]
		var edge *NativeSourceReset
		for j := range resets {
			if resets[j].SourceID == head.SourceID {
				if edge != nil {
					return NativeContinuityDecision{}, ErrSecurityControlInvalid
				}
				edge = &resets[j]
			}
		}
		if edge == nil {
			if next.SourceInstanceID != head.SourceInstanceID {
				return fault("reset_required")
			}
			if next.Sequence < head.Sequence {
				return fault("reset_conflict")
			}
			continue
		}
		encoded, err := edge.Canonical()
		if err != nil {
			return NativeContinuityDecision{}, err
		}
		known := false
		restarts := 0
		for _, prior := range history {
			if prior.SourceID == head.SourceID && prior.Reason == "restart" {
				restarts++
			}
			if prior.ResetID == edge.ResetID {
				previous, err := prior.Canonical()
				if err != nil {
					return NativeContinuityDecision{}, err
				}
				if !bytes.Equal(previous, encoded) {
					return fault("reset_conflict")
				}
				known = true
			}
		}
		if known {
			if head.SourceInstanceID != edge.NewSourceInstanceID || next.SourceInstanceID != head.SourceInstanceID || next.Sequence < head.Sequence {
				return fault("reset_conflict")
			}
			decision.ReceiptIDs = append(decision.ReceiptIDs, edge.ResetID)
			continue
		}
		if edge.PreviousSourceInstanceID != head.SourceInstanceID || edge.PreviousFinalSequence < head.Sequence || next.SourceInstanceID != edge.NewSourceInstanceID || edge.Reason == "restart" && restarts >= 1 {
			return fault("reset_conflict")
		}
		for _, prior := range history {
			if prior.SourceID == edge.SourceID && (edge.NewSourceInstanceID == prior.PreviousSourceInstanceID || edge.NewSourceInstanceID == prior.NewSourceInstanceID) {
				return fault("reset_conflict")
			}
		}
		decision.NewResets = append(decision.NewResets, *edge)
		decision.ReceiptIDs = append(decision.ReceiptIDs, edge.ResetID)
	}
	return decision, nil
}

func (r SecurityCoverageResult) Validate() error {
	if !securitySafeInt(r.ProcessedControlSequence) || (r.ProcessedControlSequence == 0) != (r.ProcessedControlDigest == nil) || r.ProcessedControlDigest != nil && !IsValidSHA256Fingerprint(*r.ProcessedControlDigest) || !slices.Contains([]string{"accepted", "stale_control", "reset_required", "reset_conflict"}, r.CoverageResult) || r.SourceResetReceipts == nil || len(r.SourceResetReceipts) > 11 || r.DeliveryWatermarkRejections == nil || len(r.DeliveryWatermarkRejections) > 50 || !slices.Contains([]string{"not_allocated", "ready", "freeze_pending", "frozen", "thaw_pending", "unavailable"}, r.ExecutionState) {
		return ErrSecurityControlInvalid
	}
	for i, receipt := range r.SourceResetReceipts {
		if !IsValidAgreementID(receipt.ResetID) || !IsValidSHA256Fingerprint(receipt.ResetDigest) {
			return ErrSecurityControlInvalid
		}
		for _, prior := range r.SourceResetReceipts[:i] {
			if prior.ResetID == receipt.ResetID {
				return ErrSecurityControlInvalid
			}
		}
	}
	for i, rejection := range r.DeliveryWatermarkRejections {
		if !slices.Contains([]string{"native", "browser"}, rejection.Family) || !IsValidAgreementID(rejection.SourceID) || !slices.Contains([]string{"closed_source", "position_limit"}, rejection.Reason) {
			return ErrSecurityControlInvalid
		}
		for _, prior := range r.DeliveryWatermarkRejections[:i] {
			if prior.Family == rejection.Family && prior.SourceID == rejection.SourceID {
				return ErrSecurityControlInvalid
			}
		}
	}
	return nil
}

func (v NativeSourceReset) MarshalJSON() ([]byte, error) {
	type wire NativeSourceReset
	return json.Marshal(struct {
		*wire
		OccurredAt securityJSONInstant `json:"occurred_at"`
	}{wire: (*wire)(&v), OccurredAt: securityJSONInstant{Time: v.OccurredAt, spelling: v.occurredAtSpelling}})
}
func (v *NativeSourceReset) UnmarshalJSON(raw []byte) error {
	if v == nil {
		return ErrSecurityControlInvalid
	}
	type wire NativeSourceReset
	var decoded wire
	value := struct {
		*wire
		OccurredAt securityJSONInstant `json:"occurred_at"`
	}{wire: &decoded}
	if decodeClosedDeliveryDeclaration(raw, &value, 256*1024) != nil {
		return ErrSecurityControlInvalid
	}
	candidate := NativeSourceReset(decoded)
	candidate.OccurredAt = value.OccurredAt.Time
	candidate.occurredAtSpelling = value.OccurredAt.spelling
	if candidate.Validate() != nil {
		return ErrSecurityControlInvalid
	}
	*v = candidate
	return nil
}
