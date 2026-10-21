// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

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
	require.NoError(t, err)
	fault := controlReceipt(11, "reset_conflict")
	ledger, err = ledger.Record(fault)
	require.NoError(t, err)
	receipt, stale, lookupErr := ledger.Lookup(10, controlReceipt(10, "accepted").Digest)
	require.NoError(t, lookupErr, "delayed healthy control became fresh")
	require.True(t, stale, "delayed healthy control became fresh")
	require.Nil(t, receipt, "delayed healthy control became fresh")
	receipt, processed, err := ledger.Lookup(9, accepted.Digest)
	require.NoError(t, err, "replay rolled processed boundary back")
	require.True(t, processed, "replay rolled processed boundary back")
	require.Equal(t, "accepted", receipt.Result, "replay rolled processed boundary back")
	require.Equal(t, int64(11), ledger.ProcessedSequence, "replay rolled processed boundary back")
	require.Equal(t, fault.Digest, *ledger.ProcessedDigest, "replay rolled processed boundary back")
	_, _, conflictErr := ledger.Lookup(11, accepted.Digest)
	require.ErrorIs(t, conflictErr, model.ErrSecurityControlConflict, "conflicting retained control accepted")
	_, olderErr := ledger.Record(controlReceipt(10, "accepted"))
	require.Error(t, olderErr, "older control installed")
	// Durable serialization and eviction preserve the highest processed fault.
	for sequence := int64(12); sequence < 145; sequence++ {
		ledger, err = ledger.Record(controlReceipt(sequence, "reset_required"))
		require.NoError(t, err)
	}
	raw, err := json.Marshal(ledger)
	require.NoError(t, err)
	var restarted nativeControlLedger
	require.NoError(t, json.Unmarshal(raw, &restarted), "invalid persisted control ledger")
	require.NoError(t, restarted.Validate(), "invalid persisted control ledger")
	require.Len(t, restarted.Receipts, 128, "cache growth or high-water loss")
	require.Equal(t, int64(144), restarted.ProcessedSequence, "cache growth or high-water loss")
	receipt, stale, evictedErr := restarted.Lookup(9, accepted.Digest)
	require.NoError(t, evictedErr, "eviction made an old control fresh")
	require.True(t, stale, "eviction made an old control fresh")
	require.Nil(t, receipt, "eviction made an old control fresh")
	recovered, err := restarted.Record(controlReceipt(145, "accepted"))
	require.NoError(t, err, "newer valid control cannot recover")
	require.Equal(t, int64(145), recovered.ProcessedSequence, "newer valid control cannot recover")
}
func TestControlReceiptFitsReservedSlot(t *testing.T) {
	receipt := controlReceipt(9007199254740991, "reset_conflict")
	for i := 0; i < 11; i++ {
		receipt.ResetIDs = append(receipt.ResetIDs, fmt.Sprintf("%03d%s", i, strings.Repeat("a", 125)))
	}
	for i := int64(0); i < 50; i++ {
		receipt.Rejections = append(receipt.Rejections, [2]int64{i, 1})
	}
	require.NoError(t, receipt.Validate())
	raw, err := canonicalSecurityControlDocument(receipt)
	require.NoError(t, err, "receipt exceeded reservation")
	require.LessOrEqual(t, len(raw), model.SecurityControlReceiptMaxBytes, "receipt exceeded reservation")
	ledger := newNativeControlLedger()
	for sequence := int64(1); sequence <= 128; sequence++ {
		receipt.Sequence = sequence
		ledger, err = ledger.Record(receipt)
		require.NoError(t, err)
	}
	raw, err = canonicalSecurityControlDocument(ledger.Receipts)
	require.NoError(t, err, "cache exceeded reservation")
	require.LessOrEqual(t, len(raw), model.SecurityControlCacheMaxBytes, "cache exceeded reservation")
	// No mutable caller slice or digest pointer aliases the retained ledger.
	receipt.ResetIDs[0] = "modified"
	require.NotEqual(t, "modified", ledger.Receipts[127].ResetIDs[0], "caller mutated retained receipt")
	replay, _, err := ledger.Lookup(128, receipt.Digest)
	require.NoError(t, err)
	replay.ResetIDs[0] = "modified"
	require.NotEqual(t, "modified", ledger.Receipts[127].ResetIDs[0], "replay projection mutated retained receipt")
}
