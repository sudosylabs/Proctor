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
	"fmt"
	"strings"
	"testing"
	"time"
)

func controlReceipt(sequence int64, result string) SecurityControlReceipt {
	return SecurityControlReceipt{Sequence: sequence, Digest: SHA256Fingerprint([]byte(fmt.Sprint(sequence, result))), Result: result, ResetIDs: []string{}, Rejections: [][2]int64{}}
}
func TestNativeControlLedgerOrdersFaultsAndReplays(t *testing.T) {
	ledger := NewNativeControlLedger()
	var err error
	accepted := controlReceipt(9, "accepted")
	ledger, err = ledger.Record(accepted)
	if err != nil {
		t.Fatal(err)
	}
	fault := controlReceipt(11, "reset_conflict")
	ledger, err = ledger.Record(fault)
	if err != nil {
		t.Fatal(err)
	}
	if receipt, stale, err := ledger.Lookup(10, controlReceipt(10, "accepted").Digest); err != nil || !stale || receipt != nil {
		t.Fatal("delayed healthy control became fresh")
	}
	receipt, processed, err := ledger.Lookup(9, accepted.Digest)
	if err != nil || !processed || receipt.Result != "accepted" || ledger.ProcessedSequence != 11 || *ledger.ProcessedDigest != fault.Digest {
		t.Fatal("replay rolled processed boundary back")
	}
	if _, _, err := ledger.Lookup(11, accepted.Digest); !errors.Is(err, ErrSecurityControlConflict) {
		t.Fatal("conflicting retained control accepted")
	}
	if _, err := ledger.Record(controlReceipt(10, "accepted")); err == nil {
		t.Fatal("older control installed")
	}
	// Durable serialization and eviction preserve the highest processed fault.
	for sequence := int64(12); sequence < 145; sequence++ {
		ledger, err = ledger.Record(controlReceipt(sequence, "reset_required"))
		if err != nil {
			t.Fatal(err)
		}
	}
	raw, err := json.Marshal(ledger)
	if err != nil {
		t.Fatal(err)
	}
	var restarted NativeControlLedger
	if err := json.Unmarshal(raw, &restarted); err != nil || restarted.Validate() != nil {
		t.Fatal("invalid persisted control ledger")
	}
	if len(restarted.Receipts) != 128 || restarted.ProcessedSequence != 144 {
		t.Fatal("cache growth or high-water loss")
	}
	if receipt, stale, err := restarted.Lookup(9, accepted.Digest); err != nil || !stale || receipt != nil {
		t.Fatal("eviction made an old control fresh")
	}
	recovered, err := restarted.Record(controlReceipt(145, "accepted"))
	if err != nil || recovered.ProcessedSequence != 145 {
		t.Fatal("newer valid control cannot recover")
	}
}
func TestControlReceiptFitsReservedSlot(t *testing.T) {
	receipt := controlReceipt(9007199254740991, "reset_conflict")
	for i := 0; i < 11; i++ {
		receipt.ResetIDs = append(receipt.ResetIDs, fmt.Sprintf("%03d%s", i, strings.Repeat("a", 125)))
	}
	for i := int64(0); i < 50; i++ {
		receipt.Rejections = append(receipt.Rejections, [2]int64{i, 1})
	}
	if err := receipt.Validate(); err != nil {
		t.Fatal(err)
	}
	raw, err := encodeCanonicalExamDocument(receipt)
	if err != nil || len(raw) > SecurityControlReceiptMaxBytes {
		t.Fatalf("receipt exceeded reservation: %d %v", len(raw), err)
	}
	ledger := NewNativeControlLedger()
	for sequence := int64(1); sequence <= 128; sequence++ {
		receipt.Sequence = sequence
		ledger, err = ledger.Record(receipt)
		if err != nil {
			t.Fatal(err)
		}
	}
	raw, err = encodeCanonicalExamDocument(ledger.Receipts)
	if err != nil || len(raw) > SecurityControlCacheMaxBytes {
		t.Fatalf("cache exceeded reservation: %d %v", len(raw), err)
	}
	// No mutable caller slice or digest pointer aliases the retained ledger.
	receipt.ResetIDs[0] = "modified"
	if ledger.Receipts[127].ResetIDs[0] == "modified" {
		t.Fatal("caller mutated retained receipt")
	}
	replay, _, err := ledger.Lookup(128, receipt.Digest)
	if err != nil {
		t.Fatal(err)
	}
	replay.ResetIDs[0] = "modified"
	if ledger.Receipts[127].ResetIDs[0] == "modified" {
		t.Fatal("replay projection mutated retained receipt")
	}
}
func TestSecurityControlStrictCodec(t *testing.T) {
	control := SecurityCoverageRenewal{ControlSequence: 1, PolicyDigest: SHA256Fingerprint([]byte("policy")), SecuritySessionID: "security-session", StreamID: "stream", Posture: "checking", Sources: []NativeSourceCoverage{}, Coverage: []NativeCoverageClaim{}, SourceResets: []NativeSourceReset{}, DeliveryWatermarks: []DeliveryWatermark{}}
	raw, err := control.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	var decoded SecurityCoverageRenewal
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{strings.Replace(string(raw), "\"sources\":[]", "\"sources\":null", 1), strings.Replace(string(raw), "\"control_sequence\":1", "\"control_sequence\":1.0", 1), strings.Replace(string(raw), "\"posture\"", "\"Posture\"", 1), strings.TrimSuffix(string(raw), "}") + `,"stream_id":"other"}`, strings.TrimSuffix(string(raw), "}") + `,"unknown":false}`} {
		if json.Unmarshal([]byte(bad), &decoded) == nil {
			t.Fatalf("accepted invalid control: %s", bad)
		}
	}
	control.DeliveryWatermarks = []DeliveryWatermark{{Family: "native", SourceID: "stream", AllocatedThroughSequence: 4, AcknowledgedThroughSequence: 5}}
	if control.Validate() == nil {
		t.Fatal("acknowledgement above allocation accepted")
	}
}

func TestNativeResetContinuityAndRestartAllowance(t *testing.T) {
	head := NativeSourceCoverage{SourceID: NativeSourceCapture, SourceInstanceID: "initial", Sequence: 9}
	current := head
	current.SourceInstanceID = "restarted"
	current.Sequence = 0
	reset := NativeSourceReset{Kind: "source_reset", ResetID: "reset-1", SourceID: head.SourceID, PreviousSourceInstanceID: head.SourceInstanceID, PreviousFinalSequence: 9, NewSourceInstanceID: current.SourceInstanceID, Reason: "restart", OccurredAt: time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)}
	missing, err := ResolveNativeSourceContinuity([]NativeSourceCoverage{head}, []NativeSourceCoverage{current}, nil, nil)
	if err != nil || missing.Result != "reset_required" || missing.Heads[0].SourceInstanceID != head.SourceInstanceID {
		t.Fatal("unproven lifetime installed")
	}
	accepted, err := ResolveNativeSourceContinuity([]NativeSourceCoverage{head}, []NativeSourceCoverage{current}, nil, []NativeSourceReset{reset})
	if err != nil || accepted.Result != "accepted" || len(accepted.NewResets) != 1 {
		t.Fatalf("valid reset rejected: %#v %v", accepted, err)
	}
	replay, err := ResolveNativeSourceContinuity(accepted.Heads, accepted.Heads, accepted.NewResets, []NativeSourceReset{reset})
	if err != nil || replay.Result != "accepted" || len(replay.NewResets) != 0 || len(replay.ReceiptIDs) != 1 {
		t.Fatal("reset replay spent a second allowance")
	}
	second := reset
	second.ResetID = "reset-2"
	second.PreviousSourceInstanceID = "restarted"
	second.NewSourceInstanceID = "restarted-again"
	second.PreviousFinalSequence = 0
	secondCurrent := current
	secondCurrent.SourceInstanceID = second.NewSourceInstanceID
	refused, err := ResolveNativeSourceContinuity(accepted.Heads, []NativeSourceCoverage{secondCurrent}, accepted.NewResets, []NativeSourceReset{second})
	if err != nil || refused.Result != "reset_conflict" || len(refused.NewResets) != 0 {
		t.Fatal("automatic restart allowance exceeded")
	}
	short := reset
	short.PreviousFinalSequence = 8
	refused, err = ResolveNativeSourceContinuity([]NativeSourceCoverage{head}, []NativeSourceCoverage{current}, nil, []NativeSourceReset{short})
	if err != nil || refused.Result != "reset_conflict" {
		t.Fatal("reset truncated observed source watermark")
	}
	foreign := reset
	foreign.SourceID = NativeSourceDisplay
	if _, err = ResolveNativeSourceContinuity([]NativeSourceCoverage{head}, []NativeSourceCoverage{current}, nil, []NativeSourceReset{foreign}); err == nil {
		t.Fatal("foreign source treated as recoverable fault")
	}
	reused := second
	reused.Reason = "permission_changed"
	reused.NewSourceInstanceID = "initial"
	secondCurrent.SourceInstanceID = "initial"
	refused, err = ResolveNativeSourceContinuity(accepted.Heads, []NativeSourceCoverage{secondCurrent}, accepted.NewResets, []NativeSourceReset{reused})
	if err != nil || refused.Result != "reset_conflict" {
		t.Fatal("old source instance reused")
	}
}
