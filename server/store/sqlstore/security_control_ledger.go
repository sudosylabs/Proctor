// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"encoding/json"
	"slices"

	"github.com/sudosylabs/proctor/server/internal/canonicaljson"
	"github.com/sudosylabs/proctor/server/model"
)

// securityControlReceipt is the compact persisted outcome of one processed
// native security control, not a transport response. Reset IDs refer to
// separately charged immutable reset facts. Rejections use immutable
// owner-slot indexes (native=0, browser=1..49) and reason 0=closed,
// 1=position_limit. The owning aggregate resolves these references on replay.
type securityControlReceipt struct {
	Sequence   int64      `json:"sequence"`
	Digest     string     `json:"digest"`
	Result     string     `json:"result"`
	ResetIDs   []string   `json:"resets"`
	Rejections [][2]int64 `json:"rejections"`
}

func (r securityControlReceipt) Validate() error {
	if r.Sequence <= 0 || !securityControlSafeInt(r.Sequence) || !model.IsValidSHA256Fingerprint(r.Digest) || !slices.Contains([]string{"accepted", "reset_required", "reset_conflict"}, r.Result) || r.ResetIDs == nil || len(r.ResetIDs) > 11 || r.Rejections == nil || len(r.Rejections) > 50 {
		return model.ErrSecurityControlInvalid
	}
	for i, id := range r.ResetIDs {
		if !model.IsValidAgreementID(id) || slices.Contains(r.ResetIDs[:i], id) {
			return model.ErrSecurityControlInvalid
		}
	}
	for i, rejection := range r.Rejections {
		if rejection[0] < 0 || rejection[0] > 49 || rejection[1] < 0 || rejection[1] > 1 {
			return model.ErrSecurityControlInvalid
		}
		for _, prior := range r.Rejections[:i] {
			if prior[0] == rejection[0] {
				return model.ErrSecurityControlInvalid
			}
		}
	}
	raw, err := canonicalSecurityControlDocument(r)
	if err != nil || len(raw) > model.SecurityControlReceiptMaxBytes {
		return model.ErrSecurityControlInvalid
	}
	return nil
}

func (r securityControlReceipt) clone() securityControlReceipt {
	r.ResetIDs = slices.Clone(r.ResetIDs)
	r.Rejections = slices.Clone(r.Rejections)
	return r
}

// nativeControlLedger keeps processed ordering independently of usable coverage.
// Only authenticated, structurally valid, owner-bound controls reach this owner.
// Its receipt does not grant authority: callers always project current gates.
type nativeControlLedger struct {
	ProcessedSequence int64                    `json:"processed_sequence"`
	ProcessedDigest   *string                  `json:"processed_digest"`
	Receipts          []securityControlReceipt `json:"receipts"`
}

func newNativeControlLedger() nativeControlLedger {
	return nativeControlLedger{Receipts: []securityControlReceipt{}}
}

func (l nativeControlLedger) Validate() error {
	if !securityControlSafeInt(l.ProcessedSequence) || l.Receipts == nil || len(l.Receipts) > model.SecurityControlReceiptLimit || (l.ProcessedSequence == 0) != (l.ProcessedDigest == nil) {
		return model.ErrSecurityControlInvalid
	}
	if l.ProcessedSequence == 0 {
		if len(l.Receipts) != 0 {
			return model.ErrSecurityControlInvalid
		}
		return nil
	}
	if !model.IsValidSHA256Fingerprint(*l.ProcessedDigest) || len(l.Receipts) == 0 {
		return model.ErrSecurityControlInvalid
	}
	previous := int64(0)
	for _, receipt := range l.Receipts {
		if receipt.Validate() != nil || receipt.Sequence <= previous || receipt.Sequence > l.ProcessedSequence {
			return model.ErrSecurityControlInvalid
		}
		previous = receipt.Sequence
	}
	last := l.Receipts[len(l.Receipts)-1]
	if last.Sequence != l.ProcessedSequence || last.Digest != *l.ProcessedDigest {
		return model.ErrSecurityControlInvalid
	}
	return nil
}

// Lookup runs before interpreting reset edges or watermarks. A retained replay
// returns its original bounded outcome; an evicted older sequence is stale.
func (l nativeControlLedger) Lookup(sequence int64, digest string) (*securityControlReceipt, bool, error) {
	if l.Validate() != nil || sequence <= 0 || !securityControlSafeInt(sequence) || !model.IsValidSHA256Fingerprint(digest) {
		return nil, false, model.ErrSecurityControlInvalid
	}
	for _, receipt := range l.Receipts {
		if receipt.Sequence == sequence {
			if receipt.Digest != digest {
				return nil, false, model.ErrSecurityControlConflict
			}
			copy := receipt.clone()
			return &copy, true, nil
		}
	}
	return nil, sequence <= l.ProcessedSequence, nil
}

// Record advances every processed outcome, including reset faults. It cannot
// modify a previously processed boundary, even after cache eviction or restart.
func (l nativeControlLedger) Record(receipt securityControlReceipt) (nativeControlLedger, error) {
	if l.Validate() != nil || receipt.Validate() != nil || receipt.Sequence <= l.ProcessedSequence {
		return l, model.ErrSecurityControlInvalid
	}
	digest := receipt.Digest
	next := nativeControlLedger{ProcessedSequence: receipt.Sequence, ProcessedDigest: &digest, Receipts: make([]securityControlReceipt, 0, model.SecurityControlReceiptLimit)}
	start := 0
	if len(l.Receipts) == model.SecurityControlReceiptLimit {
		start = 1
	}
	for _, prior := range l.Receipts[start:] {
		next.Receipts = append(next.Receipts, prior.clone())
	}
	next.Receipts = append(next.Receipts, receipt.clone())
	return next, nil
}

// securityControlSafeInt bounds counters to the interoperable JSON integer
// range shared with the native security agreement.
func securityControlSafeInt(value int64) bool { return value >= 0 && value <= 9007199254740991 }

func canonicalSecurityControlDocument(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return canonicaljson.Canonicalize(encoded, len(encoded))
}
