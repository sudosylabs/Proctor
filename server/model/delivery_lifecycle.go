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

var (
	ErrDeliveryInvalid  = errors.New("delivery declaration is invalid")
	ErrDeliveryConflict = errors.New("delivery declaration conflicts with retained state")
	ErrDeliveryExpired  = errors.New("delivery upload window has expired")
)

const NativeReceiveWindow int64 = 1024
const BrowserReceiveWindow int64 = 4096

type SequenceRange struct {
	First int64 `json:"first"`
	Last  int64 `json:"last"`
}

func ValidateSequenceRanges(ranges []SequenceRange, maximum int) error {
	if ranges == nil || len(ranges) > maximum {
		return ErrDeliveryInvalid
	}
	previous := int64(-1)
	for _, r := range ranges {
		if r.First <= 0 || !securitySafeInt(r.Last) || r.Last < r.First || r.First <= previous+1 {
			return ErrDeliveryInvalid
		}
		previous = r.Last
	}
	return nil
}

type DeclareDeliveryGaps struct {
	DeclarationID               string          `json:"declaration_id"`
	ExpectedDeclarationRevision int64           `json:"expected_declaration_revision"`
	AllocatedThroughSequence    int64           `json:"allocated_through_sequence"`
	Ranges                      []SequenceRange `json:"ranges"`
	Reason                      string          `json:"reason"`
}

func (d DeclareDeliveryGaps) Validate() error {
	if !IsValidAgreementID(d.DeclarationID) || !securitySafeInt(d.ExpectedDeclarationRevision) || !securitySafeInt(d.AllocatedThroughSequence) || len(d.Ranges) == 0 || ValidateSequenceRanges(d.Ranges, 32) != nil || !slices.Contains([]string{"spool_corrupt", "spool_lost", "local_capacity_exhausted"}, d.Reason) {
		return ErrDeliveryInvalid
	}
	if d.Ranges[len(d.Ranges)-1].Last > d.AllocatedThroughSequence {
		return ErrDeliveryInvalid
	}
	raw, err := encodeCanonicalExamDocument(d)
	if err != nil || len(raw) > 8*1024 {
		return ErrDeliveryInvalid
	}
	return nil
}
func (d DeclareDeliveryGaps) Canonical() ([]byte, error) {
	if d.Validate() != nil {
		return nil, ErrDeliveryInvalid
	}
	return encodeCanonicalExamDocument(d)
}
func (d *DeclareDeliveryGaps) UnmarshalJSON(raw []byte) error {
	if d == nil {
		return ErrDeliveryInvalid
	}
	type wire DeclareDeliveryGaps
	var value wire
	if err := decodeClosedDeliveryDeclaration(raw, &value, 8*1024); err != nil {
		return err
	}
	candidate := DeclareDeliveryGaps(value)
	if candidate.Validate() != nil {
		return ErrDeliveryInvalid
	}
	*d = candidate
	return nil
}

type DeliveryGapReceipt struct {
	DeclarationID          string `json:"declaration_id"`
	RequestDigest          string `json:"request_digest"`
	DeclarationRevision    int64  `json:"declaration_revision"`
	SettledThroughSequence int64  `json:"settled_through_sequence"`
}

func (r DeliveryGapReceipt) Validate() error {
	if !IsValidAgreementID(r.DeclarationID) || !IsValidSHA256Fingerprint(r.RequestDigest) || r.DeclarationRevision < 1 || !securitySafeInt(r.DeclarationRevision) || !securitySafeInt(r.SettledThroughSequence) {
		return ErrDeliveryInvalid
	}
	return nil
}

type FinalDeliveryDeclaration struct {
	DeclarationID               string `json:"declaration_id"`
	ExpectedDeclarationRevision int64  `json:"expected_declaration_revision"`
	FinalSequence               int64  `json:"final_sequence"`
}

func (d FinalDeliveryDeclaration) Validate() error {
	if !IsValidAgreementID(d.DeclarationID) || !securitySafeInt(d.ExpectedDeclarationRevision) || !securitySafeInt(d.FinalSequence) {
		return ErrDeliveryInvalid
	}
	return nil
}
func (d FinalDeliveryDeclaration) Canonical() ([]byte, error) {
	if d.Validate() != nil {
		return nil, ErrDeliveryInvalid
	}
	return encodeCanonicalExamDocument(d)
}
func (d *FinalDeliveryDeclaration) UnmarshalJSON(raw []byte) error {
	if d == nil {
		return ErrDeliveryInvalid
	}
	type wire FinalDeliveryDeclaration
	var value wire
	if err := decodeClosedDeliveryDeclaration(raw, &value, 2*1024); err != nil {
		return err
	}
	candidate := FinalDeliveryDeclaration(value)
	if candidate.Validate() != nil {
		return ErrDeliveryInvalid
	}
	*d = candidate
	return nil
}
func decodeClosedDeliveryDeclaration(raw []byte, target any, maximum int) error {
	if len(raw) > maximum || validateExamDocumentJSON(raw) != nil {
		return ErrDeliveryInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil {
		return ErrDeliveryInvalid
	}
	actual, err := encodeCanonicalExamRaw(raw)
	if err != nil {
		return ErrDeliveryInvalid
	}
	expected, err := encodeCanonicalExamDocument(target)
	if err != nil || !bytes.Equal(actual, expected) {
		return ErrDeliveryInvalid
	}
	return nil
}

type DeliveryCloseReason string

const (
	DeliveryClosedSubmission       DeliveryCloseReason = "submission"
	DeliveryClosedSuspension       DeliveryCloseReason = "suspension"
	DeliveryClosedManagerEnd       DeliveryCloseReason = "manager_end"
	DeliveryClosedSitting          DeliveryCloseReason = "sitting_closed"
	DeliveryClosedParticipation    DeliveryCloseReason = "participation_ended"
	DeliveryClosedSecuritySession  DeliveryCloseReason = "security_session_lost"
	DeliveryClosedPolicyCorrection DeliveryCloseReason = "policy_correction"
	DeliveryClosedRuntimeReset     DeliveryCloseReason = "runtime_reset"
	DeliveryClosedBrowserDisabled  DeliveryCloseReason = "browser_disabled"
)

func (r DeliveryCloseReason) ValidFor(native bool) bool {
	common := slices.Contains([]DeliveryCloseReason{DeliveryClosedSubmission, DeliveryClosedSuspension, DeliveryClosedManagerEnd, DeliveryClosedSitting, DeliveryClosedParticipation, DeliveryClosedSecuritySession}, r)
	return common || !native && slices.Contains([]DeliveryCloseReason{DeliveryClosedPolicyCorrection, DeliveryClosedRuntimeReset, DeliveryClosedBrowserDisabled}, r)
}

type DeliveryClosure struct {
	ClosedAt            *time.Time           `json:"closed_at"`
	CloseReason         *DeliveryCloseReason `json:"close_reason"`
	KnownAtClose        *int64               `json:"known_at_close"`
	FinalSequence       *int64               `json:"final_sequence"`
	FinalDeclarationID  *string              `json:"final_declaration_id"`
	FinalBoundaryOrigin *string              `json:"final_boundary_origin"`
	UnknownTail         bool                 `json:"unknown_tail"`
	UploadExpiresAt     *time.Time           `json:"upload_expires_at"`
}

func (c DeliveryClosure) Validate(native bool) error {
	if c.ClosedAt == nil {
		if c.CloseReason != nil || c.KnownAtClose != nil || c.FinalSequence != nil || c.FinalDeclarationID != nil || c.FinalBoundaryOrigin != nil || c.UnknownTail || c.UploadExpiresAt != nil {
			return ErrDeliveryInvalid
		}
		return nil
	}
	if !securityInstant(*c.ClosedAt) || c.CloseReason == nil || !c.CloseReason.ValidFor(native) || c.KnownAtClose == nil || !securitySafeInt(*c.KnownAtClose) || c.UploadExpiresAt == nil || !securityInstant(*c.UploadExpiresAt) || c.UploadExpiresAt.After(c.ClosedAt.Add(24*time.Hour)) {
		return ErrDeliveryInvalid
	}
	if c.FinalSequence == nil {
		if !c.UnknownTail || c.FinalDeclarationID != nil || c.FinalBoundaryOrigin != nil {
			return ErrDeliveryInvalid
		}
		return nil
	}
	if !securitySafeInt(*c.FinalSequence) || *c.FinalSequence < *c.KnownAtClose || c.FinalBoundaryOrigin == nil {
		return ErrDeliveryInvalid
	}
	switch *c.FinalBoundaryOrigin {
	case "client_declared":
		if c.UnknownTail || c.FinalDeclarationID == nil || !IsValidAgreementID(*c.FinalDeclarationID) {
			return ErrDeliveryInvalid
		}
	case "server_known":
		if c.FinalDeclarationID != nil || !c.UnknownTail || *c.FinalSequence != *c.KnownAtClose {
			return ErrDeliveryInvalid
		}
	default:
		return ErrDeliveryInvalid
	}
	return nil
}
func (c DeliveryClosure) Clone() DeliveryClosure {
	if c.ClosedAt != nil {
		v := *c.ClosedAt
		c.ClosedAt = &v
	}
	if c.CloseReason != nil {
		v := *c.CloseReason
		c.CloseReason = &v
	}
	if c.KnownAtClose != nil {
		v := *c.KnownAtClose
		c.KnownAtClose = &v
	}
	if c.FinalSequence != nil {
		v := *c.FinalSequence
		c.FinalSequence = &v
	}
	if c.FinalDeclarationID != nil {
		v := *c.FinalDeclarationID
		c.FinalDeclarationID = &v
	}
	if c.FinalBoundaryOrigin != nil {
		v := *c.FinalBoundaryOrigin
		c.FinalBoundaryOrigin = &v
	}
	if c.UploadExpiresAt != nil {
		v := *c.UploadExpiresAt
		c.UploadExpiresAt = &v
	}
	return c
}

// Close is called by the authoritative lifecycle transaction, never by a client
// seal request. An earlier retirement deadline may already have elapsed.
func (c DeliveryClosure) Close(native bool, reason DeliveryCloseReason, known int64, at time.Time, retirement *time.Time) (DeliveryClosure, error) {
	if c.Validate(native) != nil || !reason.ValidFor(native) || !securitySafeInt(known) || !securityInstant(at) {
		return c, ErrDeliveryInvalid
	}
	if c.ClosedAt != nil {
		return c.Clone(), nil
	}
	expiry := at.Add(24 * time.Hour)
	if retirement != nil {
		if !securityInstant(*retirement) {
			return c, ErrDeliveryInvalid
		}
		if retirement.Before(expiry) {
			expiry = *retirement
		}
	}
	return DeliveryClosure{ClosedAt: &at, CloseReason: &reason, KnownAtClose: &known, UnknownTail: true, UploadExpiresAt: &expiry}, nil
}

// DeclareFinal returns the position delta which the aggregate must reserve
// atomically. Semantic declaration-ID replay is resolved by its receipt first.
func (c DeliveryClosure) DeclareFinal(native bool, revision, allocated int64, summaryOnly bool, request FinalDeliveryDeclaration, at time.Time) (DeliveryClosure, int64, error) {
	if c.Validate(native) != nil || request.Validate() != nil || !securitySafeInt(revision) || !securitySafeInt(allocated) || !securityInstant(at) {
		return c, 0, ErrDeliveryInvalid
	}
	if c.ClosedAt == nil {
		return c, 0, ErrDeliveryConflict
	}
	if !at.Before(*c.UploadExpiresAt) {
		return c, 0, ErrDeliveryExpired
	}
	if c.FinalSequence != nil || request.ExpectedDeclarationRevision != revision || request.FinalSequence < allocated || request.FinalSequence < *c.KnownAtClose {
		return c, 0, ErrDeliveryConflict
	}
	width := BrowserReceiveWindow
	if native {
		width = NativeReceiveWindow
	}
	if request.FinalSequence-*c.KnownAtClose > width || summaryOnly && request.FinalSequence > allocated {
		return c, 0, ErrDeliveryConflict
	}
	next := c.Clone()
	final := request.FinalSequence
	id := request.DeclarationID
	origin := "client_declared"
	next.FinalSequence = &final
	next.FinalDeclarationID = &id
	next.FinalBoundaryOrigin = &origin
	next.UnknownTail = false
	return next, final - allocated, nil
}

// Expire fixes an absent declaration using the frozen server-known boundary.
// Missing receipt positions then settle terminally; no content receipt is made.
func (c DeliveryClosure) Expire(native bool, at time.Time) (DeliveryClosure, error) {
	if c.Validate(native) != nil || !securityInstant(at) {
		return c, ErrDeliveryInvalid
	}
	if c.ClosedAt == nil || at.Before(*c.UploadExpiresAt) {
		return c.Clone(), nil
	}
	next := c.Clone()
	if next.FinalSequence == nil {
		final := *next.KnownAtClose
		origin := "server_known"
		next.FinalSequence = &final
		next.FinalBoundaryOrigin = &origin
		next.UnknownTail = true
	}
	return next, nil
}

// DeliveryProgress separates actual receipts from settlement. Its input ranges
// are bounded projections of the receipt and declaration owners, not a second
// persisted ledger. Expiry supplies a terminal cutoff without allocating gap rows.
type DeliveryProgress struct {
	HighestContiguous int64
	SettledThrough    int64
	HighestSeen       int64
	Missing           []SequenceRange
	MissingTruncated  bool
}

func ResolveDeliveryProgress(allocated int64, received, missing []SequenceRange, terminalThrough int64) (DeliveryProgress, error) {
	result := DeliveryProgress{Missing: []SequenceRange{}}
	if !securitySafeInt(allocated) || !securitySafeInt(terminalThrough) || terminalThrough > allocated || ValidateSequenceRanges(received, int(BrowserAttemptPositionLimit)) != nil || ValidateSequenceRanges(missing, int(DeliveryMissingIntervalLimit)) != nil {
		return result, ErrDeliveryInvalid
	}
	for _, set := range [][]SequenceRange{received, missing} {
		if len(set) > 0 && set[len(set)-1].Last > allocated {
			return result, ErrDeliveryInvalid
		}
	}
	ri, mi := 0, 0
	for ri < len(received) && mi < len(missing) {
		r, m := received[ri], missing[mi]
		if r.First <= m.Last && m.First <= r.Last {
			return result, ErrDeliveryConflict
		}
		if r.Last < m.First {
			ri++
		} else {
			mi++
		}
	}
	if len(received) > 0 {
		result.HighestSeen = received[len(received)-1].Last
		if received[0].First == 1 {
			result.HighestContiguous = received[0].Last
		}
	}
	all := make([]SequenceRange, 0, len(received)+len(missing)+1)
	all = append(all, received...)
	all = append(all, missing...)
	if terminalThrough > 0 {
		all = append(all, SequenceRange{First: 1, Last: terminalThrough})
	}
	slices.SortFunc(all, func(a, b SequenceRange) int {
		if a.First < b.First {
			return -1
		}
		if a.First > b.First {
			return 1
		}
		return 0
	})
	through := int64(0)
	for _, r := range all {
		if r.First > through+1 {
			if len(result.Missing) < 32 {
				result.Missing = append(result.Missing, SequenceRange{First: through + 1, Last: r.First - 1})
			} else {
				result.MissingTruncated = true
			}
		} else if len(result.Missing) == 0 && !result.MissingTruncated && r.Last > result.SettledThrough {
			result.SettledThrough = r.Last
		}
		if r.Last > through {
			through = r.Last
		}
	}
	if through < allocated {
		if len(result.Missing) < 32 {
			result.Missing = append(result.Missing, SequenceRange{First: through + 1, Last: allocated})
		} else {
			result.MissingTruncated = true
		}
	}
	return result, nil
}
func (p DeliveryProgress) WindowBase() int64 { return max(p.HighestContiguous, p.SettledThrough) }

// ValidateDeliveryGapPlacement checks the current window and actual receipts.
// The aggregate resolves declaration replay, increments its revision and charges
// positions/metadata in the same transaction that persists the missing intervals.
func ValidateDeliveryGapPlacement(native bool, progress DeliveryProgress, allocated, revision int64, closure DeliveryClosure, summaryOnly bool, request DeclareDeliveryGaps, received, terminal []SequenceRange, at time.Time) error {
	if request.Validate() != nil || closure.Validate(native) != nil || !securitySafeInt(allocated) || !securitySafeInt(revision) || !securityInstant(at) {
		return ErrDeliveryInvalid
	}
	if request.ExpectedDeclarationRevision != revision || summaryOnly && request.AllocatedThroughSequence > allocated {
		return ErrDeliveryConflict
	}
	if closure.ClosedAt != nil {
		if !at.Before(*closure.UploadExpiresAt) {
			return ErrDeliveryExpired
		}
		boundary := *closure.KnownAtClose
		if closure.FinalSequence != nil {
			boundary = *closure.FinalSequence
		}
		if request.AllocatedThroughSequence > boundary {
			return ErrDeliveryConflict
		}
	}
	width := BrowserReceiveWindow
	if native {
		width = NativeReceiveWindow
	}
	base := progress.WindowBase()
	for _, r := range request.Ranges {
		if r.First <= base || r.Last-base > width {
			return ErrDeliveryConflict
		}
		for _, set := range [][]SequenceRange{received, terminal} {
			for _, prior := range set {
				if r.First <= prior.Last && prior.First <= r.Last {
					return ErrDeliveryConflict
				}
			}
		}
	}
	return nil
}
