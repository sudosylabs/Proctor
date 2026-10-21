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

	"github.com/stretchr/testify/require"
)

func TestSecurityControlStrictCodec(t *testing.T) {
	control := SecurityCoverageRenewal{ControlSequence: 1, PolicyDigest: SHA256Fingerprint([]byte("policy")), SecuritySessionID: "security-session", StreamID: "stream", Posture: "checking", Sources: []NativeSourceCoverage{}, Coverage: []NativeCoverageClaim{}, SourceResets: []NativeSourceReset{}, DeliveryWatermarks: []DeliveryWatermark{}}
	raw, err := control.Canonical()
	require.NoError(t, err)
	var decoded SecurityCoverageRenewal
	require.NoError(t, json.Unmarshal(raw, &decoded))
	for _, bad := range []string{strings.Replace(string(raw), "\"sources\":[]", "\"sources\":null", 1), strings.Replace(string(raw), "\"control_sequence\":1", "\"control_sequence\":1.0", 1), strings.Replace(string(raw), "\"posture\"", "\"Posture\"", 1), strings.TrimSuffix(string(raw), "}") + `,"stream_id":"other"}`, strings.TrimSuffix(string(raw), "}") + `,"unknown":false}`} {
		require.Error(t, json.Unmarshal([]byte(bad), &decoded), "accepted invalid control: %s", bad)
	}
	control.DeliveryWatermarks = []DeliveryWatermark{{Family: "native", SourceID: "stream", AllocatedThroughSequence: 4, AcknowledgedThroughSequence: 5}}
	require.Error(t, control.Validate(), "acknowledgement above allocation accepted")
}

func TestNativeResetContinuityAndRestartAllowance(t *testing.T) {
	head := NativeSourceCoverage{SourceID: NativeSourceCapture, SourceInstanceID: "initial", Sequence: 9}
	current := head
	current.SourceInstanceID = "restarted"
	current.Sequence = 0
	reset := NativeSourceReset{Kind: "source_reset", ResetID: "reset-1", SourceID: head.SourceID, PreviousSourceInstanceID: head.SourceInstanceID, PreviousFinalSequence: 9, NewSourceInstanceID: current.SourceInstanceID, Reason: "restart", OccurredAt: time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)}
	missing, err := ResolveNativeSourceContinuity([]NativeSourceCoverage{head}, []NativeSourceCoverage{current}, nil, nil)
	require.NoError(t, err, "unproven lifetime installed")
	require.Equal(t, "reset_required", missing.Result, "unproven lifetime installed")
	require.Equal(t, head.SourceInstanceID, missing.Heads[0].SourceInstanceID, "unproven lifetime installed")
	accepted, err := ResolveNativeSourceContinuity([]NativeSourceCoverage{head}, []NativeSourceCoverage{current}, nil, []NativeSourceReset{reset})
	require.NoError(t, err, "valid reset rejected: %#v", accepted)
	require.Equal(t, "accepted", accepted.Result, "valid reset rejected: %#v", accepted)
	require.Len(t, accepted.NewResets, 1, "valid reset rejected: %#v", accepted)
	replay, err := ResolveNativeSourceContinuity(accepted.Heads, accepted.Heads, accepted.NewResets, []NativeSourceReset{reset})
	require.NoError(t, err, "reset replay spent a second allowance")
	require.Equal(t, "accepted", replay.Result, "reset replay spent a second allowance")
	require.Empty(t, replay.NewResets, "reset replay spent a second allowance")
	require.Len(t, replay.ReceiptIDs, 1, "reset replay spent a second allowance")
	second := reset
	second.ResetID = "reset-2"
	second.PreviousSourceInstanceID = "restarted"
	second.NewSourceInstanceID = "restarted-again"
	second.PreviousFinalSequence = 0
	secondCurrent := current
	secondCurrent.SourceInstanceID = second.NewSourceInstanceID
	refused, err := ResolveNativeSourceContinuity(accepted.Heads, []NativeSourceCoverage{secondCurrent}, accepted.NewResets, []NativeSourceReset{second})
	require.NoError(t, err, "automatic restart allowance exceeded")
	require.Equal(t, "reset_conflict", refused.Result, "automatic restart allowance exceeded")
	require.Empty(t, refused.NewResets, "automatic restart allowance exceeded")
	short := reset
	short.PreviousFinalSequence = 8
	refused, err = ResolveNativeSourceContinuity([]NativeSourceCoverage{head}, []NativeSourceCoverage{current}, nil, []NativeSourceReset{short})
	require.NoError(t, err, "reset truncated observed source watermark")
	require.Equal(t, "reset_conflict", refused.Result, "reset truncated observed source watermark")
	foreign := reset
	foreign.SourceID = NativeSourceDisplay
	_, err = ResolveNativeSourceContinuity([]NativeSourceCoverage{head}, []NativeSourceCoverage{current}, nil, []NativeSourceReset{foreign})
	require.Error(t, err, "foreign source treated as recoverable fault")
	reused := second
	reused.Reason = "permission_changed"
	reused.NewSourceInstanceID = "initial"
	secondCurrent.SourceInstanceID = "initial"
	refused, err = ResolveNativeSourceContinuity(accepted.Heads, []NativeSourceCoverage{secondCurrent}, accepted.NewResets, []NativeSourceReset{reused})
	require.NoError(t, err, "old source instance reused")
	require.Equal(t, "reset_conflict", refused.Result, "old source instance reused")
}
