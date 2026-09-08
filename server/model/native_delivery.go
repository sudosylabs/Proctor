// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"encoding/json"
	"errors"
	"slices"
	"time"
)

var ErrNativeDeliveryInvalid = errors.New("native delivery is invalid")

type NativeSourceRange struct {
	SourceID         NativeSourceID `json:"source_id"`
	SourceInstanceID string         `json:"source_instance_id"`
	FirstSequence    int64          `json:"first_sequence"`
	LastSequence     int64          `json:"last_sequence"`
}

type NativeOccurrence struct {
	Kind            string                `json:"kind"`
	OccurrenceID    string                `json:"occurrence_id"`
	ConditionID     string                `json:"condition_id"`
	DetectorID      string                `json:"detector_id"`
	DetectorVersion int64                 `json:"detector_version"`
	CapabilityID    string                `json:"capability_id"`
	Mode            NativeCapabilityClaim `json:"mode"`
	Status          string                `json:"status"`
	FirstObservedAt time.Time             `json:"first_observed_at"`
	LastConfirmedAt time.Time             `json:"last_confirmed_at"`
	RepeatCount     int64                 `json:"repeat_count"`
	Certainty       string                `json:"certainty"`
	GapCount        int64                 `json:"gap_count"`
	SourceRanges    []NativeSourceRange   `json:"source_ranges"`
}

func (o NativeOccurrence) Validate() error {
	if o.Kind != "occurrence" || !IsValidAgreementID(o.OccurrenceID) || !IsValidAgreementID(o.ConditionID) || !IsValidAgreementID(o.DetectorID) || o.DetectorVersion <= 0 || !securitySafeInt(o.DetectorVersion) || !IsValidAgreementID(o.CapabilityID) || !slices.Contains([]NativeCapabilityClaim{NativeClaimObserve, NativeClaimEnforce}, o.Mode) || !slices.Contains([]string{"opened", "repeated", "recovered"}, o.Status) || !securityInstant(o.FirstObservedAt) || !securityInstant(o.LastConfirmedAt) || o.RepeatCount <= 0 || !securitySafeInt(o.RepeatCount) || !slices.Contains([]string{"complete", "partial", "unknown"}, o.Certainty) || !securitySafeInt(o.GapCount) || len(o.SourceRanges) == 0 || len(o.SourceRanges) > 11 {
		return ErrNativeDeliveryInvalid
	}
	previous := -1
	for _, r := range o.SourceRanges {
		order := slices.Index(NativeSources(), r.SourceID)
		if order <= previous || !IsValidAgreementID(r.SourceInstanceID) || !securitySafeInt(r.FirstSequence) || !securitySafeInt(r.LastSequence) || r.LastSequence < r.FirstSequence {
			return ErrNativeDeliveryInvalid
		}
		previous = order
	}
	// Client wall clocks may roll back. Source sequences and stream ordering,
	// never timestamp equality, determine occurrence continuity.
	return nil
}

type NativeCoverageTransition struct {
	Kind       string               `json:"kind"`
	Source     NativeSourceCoverage `json:"source"`
	OccurredAt time.Time            `json:"occurred_at"`
	Reason     string               `json:"reason"`
}

func (v NativeCoverageTransition) Validate() error {
	if v.Kind != "coverage_transition" || validateNativeCoverageSnapshot([]NativeSourceCoverage{v.Source}, []NativeCoverageClaim{}) != nil || !securityInstant(v.OccurredAt) || !slices.Contains([]string{"initial", "health_changed", "permission_changed", "reconciled", "stopped"}, v.Reason) {
		return ErrNativeDeliveryInvalid
	}
	return nil
}

type NativeSourceGap struct {
	Kind                 string         `json:"kind"`
	SourceID             NativeSourceID `json:"source_id"`
	SourceInstanceID     string         `json:"source_instance_id"`
	FirstMissingSequence int64          `json:"first_missing_sequence"`
	LastMissingSequence  int64          `json:"last_missing_sequence"`
	Reason               string         `json:"reason"`
	OccurredAt           time.Time      `json:"occurred_at"`
}

func (g NativeSourceGap) Validate() error {
	if g.Kind != "source_gap" || !slices.Contains(NativeSources(), g.SourceID) || !IsValidAgreementID(g.SourceInstanceID) || !securitySafeInt(g.FirstMissingSequence) || !securitySafeInt(g.LastMissingSequence) || g.LastMissingSequence < g.FirstMissingSequence || !slices.Contains([]string{"overflow", "source_loss", "corruption"}, g.Reason) || !securityInstant(g.OccurredAt) {
		return ErrNativeDeliveryInvalid
	}
	return nil
}

// NativeRecord is a closed union. It never retains an arbitrary raw detail bag.
type NativeRecord struct {
	Occurrence *NativeOccurrence
	Coverage   *NativeCoverageTransition
	Reset      *NativeSourceReset
	Gap        *NativeSourceGap
}

func (r NativeRecord) value() (any, error) {
	var value any
	count := 0
	for _, candidate := range []interface{ Validate() error }{r.Occurrence, r.Coverage, r.Reset, r.Gap} {
		// Typed nil pointers cannot be tested through the interface itself.
		switch v := candidate.(type) {
		case *NativeOccurrence:
			if v == nil {
				continue
			}
		case *NativeCoverageTransition:
			if v == nil {
				continue
			}
		case *NativeSourceReset:
			if v == nil {
				continue
			}
		case *NativeSourceGap:
			if v == nil {
				continue
			}
		}
		if candidate.Validate() != nil {
			return nil, ErrNativeDeliveryInvalid
		}
		value, count = candidate, count+1
	}
	if count != 1 {
		return nil, ErrNativeDeliveryInvalid
	}
	return value, nil
}
func (r NativeRecord) Validate() error { _, err := r.value(); return err }
func (r NativeRecord) MarshalJSON() ([]byte, error) {
	v, err := r.value()
	if err != nil {
		return nil, err
	}
	return json.Marshal(v)
}
func (r *NativeRecord) UnmarshalJSON(raw []byte) error {
	if r == nil || len(raw) > 256*1024 || validateExamDocumentJSON(raw) != nil {
		return ErrNativeDeliveryInvalid
	}
	var kind struct {
		Kind string `json:"kind"`
	}
	if json.Unmarshal(raw, &kind) != nil {
		return ErrNativeDeliveryInvalid
	}
	var value NativeRecord
	var target any
	switch kind.Kind {
	case "occurrence":
		value.Occurrence = new(NativeOccurrence)
		target = value.Occurrence
	case "coverage_transition":
		value.Coverage = new(NativeCoverageTransition)
		target = value.Coverage
	case "source_reset":
		value.Reset = new(NativeSourceReset)
		target = value.Reset
	case "source_gap":
		value.Gap = new(NativeSourceGap)
		target = value.Gap
	default:
		return ErrNativeDeliveryInvalid
	}
	if decodeClosedDeliveryDeclaration(raw, target, 256*1024) != nil || value.Validate() != nil {
		return ErrNativeDeliveryInvalid
	}
	*r = value
	return nil
}

type NativeSecurityBatch struct {
	StreamID             string                 `json:"stream_id"`
	BatchSequence        int64                  `json:"batch_sequence"`
	PriorAcknowledgement int64                  `json:"prior_acknowledgement"`
	ParticipationID      AttemptParticipationID `json:"participation_id"`
	Generation           int64                  `json:"generation"`
	SecuritySessionID    string                 `json:"security_session_id"`
	PolicyDigest         string                 `json:"policy_digest"`
	ApplicationReleaseID string                 `json:"application_release_id"`
	MatrixID             string                 `json:"matrix_id"`
	Records              []NativeRecord         `json:"records"`
}

func (b NativeSecurityBatch) Validate() error {
	if !IsValidAgreementID(b.StreamID) || b.BatchSequence <= 0 || !securitySafeInt(b.BatchSequence) || !securitySafeInt(b.PriorAcknowledgement) || !b.ParticipationID.IsValid() || b.Generation <= 0 || !securitySafeInt(b.Generation) || !IsValidAgreementID(b.SecuritySessionID) || !IsValidSHA256Fingerprint(b.PolicyDigest) || !IsValidAgreementID(b.ApplicationReleaseID) || !IsValidAgreementID(b.MatrixID) || len(b.Records) == 0 || len(b.Records) > 64 {
		return ErrNativeDeliveryInvalid
	}
	for _, r := range b.Records {
		if r.Validate() != nil {
			return ErrNativeDeliveryInvalid
		}
	}
	raw, err := encodeCanonicalExamDocument(b)
	if err != nil || len(raw) > 256*1024 {
		return ErrNativeDeliveryInvalid
	}
	return nil
}
func (b NativeSecurityBatch) Canonical() ([]byte, error) {
	if b.Validate() != nil {
		return nil, ErrNativeDeliveryInvalid
	}
	return encodeCanonicalExamDocument(b)
}
func (b *NativeSecurityBatch) UnmarshalJSON(raw []byte) error {
	if b == nil {
		return ErrNativeDeliveryInvalid
	}
	type wire NativeSecurityBatch
	var value wire
	if decodeClosedDeliveryDeclaration(raw, &value, 256*1024) != nil {
		return ErrNativeDeliveryInvalid
	}
	candidate := NativeSecurityBatch(value)
	if candidate.Validate() != nil {
		return ErrNativeDeliveryInvalid
	}
	*b = candidate
	return nil
}

type NativeBatchReceipt struct {
	StreamID      string    `json:"stream_id"`
	BatchSequence int64     `json:"batch_sequence"`
	RequestDigest string    `json:"request_digest"`
	ReceivedAt    time.Time `json:"received_at"`
}

func (r NativeBatchReceipt) Validate() error {
	if !IsValidAgreementID(r.StreamID) || r.BatchSequence < 1 || !securitySafeInt(r.BatchSequence) || !IsValidSHA256Fingerprint(r.RequestDigest) || !securityInstant(r.ReceivedAt) {
		return ErrNativeDeliveryInvalid
	}
	return nil
}

type NativeDeliveryProgress struct {
	HighestContiguousBatchSequence int64           `json:"highest_contiguous_batch_sequence"`
	SettledThroughBatchSequence    int64           `json:"settled_through_batch_sequence"`
	HighestSeenBatchSequence       int64           `json:"highest_seen_batch_sequence"`
	MissingBatchRanges             []SequenceRange `json:"missing_batch_ranges"`
	MissingRangesTruncated         bool            `json:"missing_ranges_truncated"`
	ServerTime                     time.Time       `json:"server_time"`
}
type NativeSecurityAcknowledgement struct {
	Receipt NativeBatchReceipt `json:"receipt"`
	NativeDeliveryProgress
}

type NativeSecurityStreamStatus struct {
	NativeDeliveryProgress
	StreamID                       string                     `json:"stream_id"`
	AllocatedThroughSequence       int64                      `json:"allocated_through_sequence"`
	TerminalMissingThroughSequence int64                      `json:"terminal_missing_through_sequence"`
	Closure                        DeliveryClosure            `json:"closure"`
	DeclarationRevision            int64                      `json:"declaration_revision"`
	DetailMode                     string                     `json:"detail_mode"`
	BudgetScope                    *string                    `json:"budget_scope"`
	SummaryOnlyReason              *DeliveryStopReason        `json:"summary_only_reason"`
	Summary                        *UnretainedDeliverySummary `json:"summary"`
}

func (s NativeSecurityStreamStatus) Validate() error {
	if !IsValidAgreementID(s.StreamID) || !securitySafeInt(s.AllocatedThroughSequence) || !securitySafeInt(s.TerminalMissingThroughSequence) || s.TerminalMissingThroughSequence > s.AllocatedThroughSequence || s.Closure.Validate(true) != nil || !securitySafeInt(s.DeclarationRevision) || !securityInstant(s.ServerTime) || !securitySafeInt(s.HighestContiguousBatchSequence) || !securitySafeInt(s.SettledThroughBatchSequence) || !securitySafeInt(s.HighestSeenBatchSequence) || s.HighestContiguousBatchSequence > s.SettledThroughBatchSequence || s.HighestContiguousBatchSequence > s.HighestSeenBatchSequence || s.HighestSeenBatchSequence > s.AllocatedThroughSequence || s.SettledThroughBatchSequence > s.AllocatedThroughSequence || ValidateSequenceRanges(s.MissingBatchRanges, 32) != nil {
		return ErrNativeDeliveryInvalid
	}
	if len(s.MissingBatchRanges) > 0 && (s.MissingBatchRanges[0].First <= s.SettledThroughBatchSequence || s.MissingBatchRanges[len(s.MissingBatchRanges)-1].Last > s.AllocatedThroughSequence) {
		return ErrNativeDeliveryInvalid
	}
	switch s.DetailMode {
	case "collecting":
		if s.BudgetScope != nil || s.SummaryOnlyReason != nil {
			return ErrNativeDeliveryInvalid
		}
	case "summary_only":
		if s.BudgetScope == nil || !slices.Contains([]string{"participation", "attempt"}, *s.BudgetScope) || s.SummaryOnlyReason == nil || !slices.Contains([]DeliveryStopReason{DeliveryStopRecords, DeliveryStopBytes, DeliveryStopPositions, DeliveryStopMetadata, DeliveryStopLocalLossInventory}, *s.SummaryOnlyReason) {
			return ErrNativeDeliveryInvalid
		}
	default:
		return ErrNativeDeliveryInvalid
	}
	if s.Summary != nil && s.Summary.Validate() != nil {
		return ErrNativeDeliveryInvalid
	}
	raw, err := encodeCanonicalExamDocument(s)
	if err != nil || len(raw) > 16*1024 {
		return ErrNativeDeliveryInvalid
	}
	return nil
}

// NativeSourceLifetime is reconstructed from the admitted report and immutable
// reset edges. A retired instance has a fixed final source sequence; delivery
// may fill its historical range but can never extend that lifetime.
type NativeSourceLifetime struct {
	SourceID      NativeSourceID
	InstanceID    string
	FinalSequence *int64
}

func ValidateNativeRecordBinding(record NativeRecord, resolved ResolvedNativePolicy, agreement *DesktopNativeAgreement, lifetimes []NativeSourceLifetime) error {
	if record.Validate() != nil || agreement == nil {
		return ErrNativeDeliveryInvalid
	}
	checkRange := func(source NativeSourceID, instance string, last int64) error {
		if !slices.Contains(resolved.Sources, source) {
			return ErrNativeDeliveryInvalid
		}
		for _, life := range lifetimes {
			if life.SourceID == source && life.InstanceID == instance {
				if life.FinalSequence != nil && last > *life.FinalSequence {
					return ErrNativeDeliveryInvalid
				}
				return nil
			}
		}
		return ErrNativeDeliveryInvalid
	}
	if occurrence := record.Occurrence; occurrence != nil {
		sources := make([]NativeSourceID, 0, len(occurrence.SourceRanges))
		for _, r := range occurrence.SourceRanges {
			if err := checkRange(r.SourceID, r.SourceInstanceID, r.LastSequence); err != nil {
				return err
			}
			sources = append(sources, r.SourceID)
		}
		if agreement.ValidateDetector(occurrence.DetectorID, occurrence.DetectorVersion, occurrence.CapabilityID, occurrence.ConditionID, occurrence.Mode, sources) != nil {
			return ErrNativeDeliveryInvalid
		}
		selected := false
		for _, required := range resolved.Requirements {
			if required.CapabilityID == occurrence.CapabilityID && required.RequiredClaim == occurrence.Mode {
				selected = true
			}
		}
		if !selected {
			return ErrNativeDeliveryInvalid
		}
		return nil
	}
	if transition := record.Coverage; transition != nil {
		if err := checkRange(transition.Source.SourceID, transition.Source.SourceInstanceID, transition.Source.Sequence); err != nil {
			return err
		}
		for _, required := range resolved.Requirements {
			if required.SourceID == transition.Source.SourceID && required.Entry.SourceSchemaDigest == transition.Source.SourceSchemaDigest && required.Entry.AdapterVersion == transition.Source.AdapterVersion {
				return nil
			}
		}
		return ErrNativeDeliveryInvalid
	}
	if gap := record.Gap; gap != nil {
		return checkRange(gap.SourceID, gap.SourceInstanceID, gap.LastMissingSequence)
	}
	// Reset edge continuity and semantic reset-ID replay are validated by the
	// shared reset owner. This check only proves its admitted source selector.
	if !slices.Contains(resolved.Sources, record.Reset.SourceID) {
		return ErrNativeDeliveryInvalid
	}
	return nil
}

// NativeOccurrenceProgress is an interpretation result, not a misconduct Flag.
// UnresolvedOpener survives subsequent transitions when a terminal delivery gap
// prevented the server from receiving the opening observation.
type NativeOccurrenceProgress struct {
	Latest           NativeOccurrence
	UnresolvedOpener bool
}

func AdvanceNativeOccurrence(prior *NativeOccurrenceProgress, next NativeOccurrence, precedingTerminalGap bool) (NativeOccurrenceProgress, error) {
	zero := NativeOccurrenceProgress{}
	if next.Validate() != nil {
		return zero, ErrNativeDeliveryInvalid
	}
	if prior == nil {
		if next.Status != "opened" && !precedingTerminalGap {
			return zero, ErrDeliveryConflict
		}
		return NativeOccurrenceProgress{Latest: cloneNativeOccurrence(next), UnresolvedOpener: next.Status != "opened"}, nil
	}
	old := prior.Latest
	if old.Validate() != nil || old.OccurrenceID != next.OccurrenceID || old.ConditionID != next.ConditionID || old.DetectorID != next.DetectorID || old.DetectorVersion != next.DetectorVersion || old.CapabilityID != next.CapabilityID || old.Mode != next.Mode || old.Status == "recovered" || next.Status == "opened" || !old.FirstObservedAt.Equal(next.FirstObservedAt) || next.RepeatCount < old.RepeatCount || next.GapCount < old.GapCount || len(old.SourceRanges) != len(next.SourceRanges) {
		return zero, ErrDeliveryConflict
	}
	for i, r := range old.SourceRanges {
		n := next.SourceRanges[i]
		if r.SourceID != n.SourceID || r.SourceInstanceID != n.SourceInstanceID || n.FirstSequence != r.FirstSequence || n.LastSequence < r.LastSequence {
			return zero, ErrDeliveryConflict
		}
	}
	return NativeOccurrenceProgress{Latest: cloneNativeOccurrence(next), UnresolvedOpener: prior.UnresolvedOpener}, nil
}
func cloneNativeOccurrence(o NativeOccurrence) NativeOccurrence {
	o.SourceRanges = slices.Clone(o.SourceRanges)
	return o
}
