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

// Native security controls are canonical, digest-bound documents: a renewal
// is accepted only when its bytes are already canonical, its digest is the
// replay identity, and reset edges are retained by digest. The adapter's compact control ledger is a persistence
// spelling and lives in sqlstore; transports present results through their own
// DTOs.

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
	ResetID     string
	ResetDigest string
}
type DeliveryWatermarkRejection struct {
	Family   string
	SourceID string
	Reason   string
}
type SecurityCoverageResult struct {
	ProcessedControlSequence    int64
	ProcessedControlDigest      *string
	CoverageResult              string
	SourceResetReceipts         []SourceResetReceipt
	SecurityInteractionAllowed  bool
	DeliveryWatermarkRejections []DeliveryWatermarkRejection
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
	if !securitySafeInt(r.ProcessedControlSequence) || (r.ProcessedControlSequence == 0) != (r.ProcessedControlDigest == nil) || r.ProcessedControlDigest != nil && !IsValidSHA256Fingerprint(*r.ProcessedControlDigest) || !slices.Contains([]string{"accepted", "stale_control", "reset_required", "reset_conflict"}, r.CoverageResult) || r.SourceResetReceipts == nil || len(r.SourceResetReceipts) > 11 || r.DeliveryWatermarkRejections == nil || len(r.DeliveryWatermarkRejections) > 50 {
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

func (r NativeSourceReset) MarshalJSON() ([]byte, error) {
	type wire NativeSourceReset
	return json.Marshal(struct {
		*wire
		OccurredAt securityJSONInstant `json:"occurred_at"`
	}{wire: (*wire)(&r), OccurredAt: securityJSONInstant{Time: r.OccurredAt, spelling: r.occurredAtSpelling}})
}
func (r *NativeSourceReset) UnmarshalJSON(raw []byte) error {
	if r == nil {
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
	*r = candidate
	return nil
}
