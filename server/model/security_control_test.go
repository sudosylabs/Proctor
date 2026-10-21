// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

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
