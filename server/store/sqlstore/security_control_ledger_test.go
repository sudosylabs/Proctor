// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func controlReceipt(sequence int64, result string) securityControlReceipt {
	return securityControlReceipt{Sequence: sequence, Digest: model.SHA256Fingerprint([]byte(fmt.Sprint(sequence, result))), Result: result, ResetIDs: []string{}, Rejections: [][2]int64{}}
}
func TestNativeControlLedgerOrdersFaultsAndReplays(t *testing.T) {
	ledger := newNativeControlLedger()
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
	if receipt, stale, lookupErr := ledger.Lookup(10, controlReceipt(10, "accepted").Digest); lookupErr != nil || !stale || receipt != nil {
		t.Fatal("delayed healthy control became fresh")
	}
	receipt, processed, err := ledger.Lookup(9, accepted.Digest)
	if err != nil || !processed || receipt.Result != "accepted" || ledger.ProcessedSequence != 11 || *ledger.ProcessedDigest != fault.Digest {
		t.Fatal("replay rolled processed boundary back")
	}
	if _, _, conflictErr := ledger.Lookup(11, accepted.Digest); !errors.Is(conflictErr, model.ErrSecurityControlConflict) {
		t.Fatal("conflicting retained control accepted")
	}
	if _, olderErr := ledger.Record(controlReceipt(10, "accepted")); olderErr == nil {
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
	var restarted nativeControlLedger
	if decodeErr := json.Unmarshal(raw, &restarted); decodeErr != nil || restarted.Validate() != nil {
		t.Fatal("invalid persisted control ledger")
	}
	if len(restarted.Receipts) != 128 || restarted.ProcessedSequence != 144 {
		t.Fatal("cache growth or high-water loss")
	}
	if receipt, stale, evictedErr := restarted.Lookup(9, accepted.Digest); evictedErr != nil || !stale || receipt != nil {
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
	raw, err := canonicalSecurityControlDocument(receipt)
	if err != nil || len(raw) > model.SecurityControlReceiptMaxBytes {
		t.Fatalf("receipt exceeded reservation: %d %v", len(raw), err)
	}
	ledger := newNativeControlLedger()
	for sequence := int64(1); sequence <= 128; sequence++ {
		receipt.Sequence = sequence
		ledger, err = ledger.Record(receipt)
		if err != nil {
			t.Fatal(err)
		}
	}
	raw, err = canonicalSecurityControlDocument(ledger.Receipts)
	if err != nil || len(raw) > model.SecurityControlCacheMaxBytes {
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
