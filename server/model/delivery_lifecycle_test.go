// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDeliverySettlementDoesNotInventReceipts(t *testing.T) {
	for _, width := range []int64{NativeReceiveWindow, BrowserReceiveWindow} {
		received := []SequenceRange{{First: 2, Last: width}}
		terminal := []SequenceRange{{First: 1, Last: 1}}
		progress, err := ResolveDeliveryProgress(width, received, terminal, 0)
		require.NoError(t, err, "terminal first hole: %#v", progress)
		require.Equal(t, int64(0), progress.HighestContiguous, "terminal first hole: %#v", progress)
		require.Equal(t, width, progress.SettledThrough, "terminal first hole: %#v", progress)
		require.Equal(t, width, progress.HighestSeen, "terminal first hole: %#v", progress)
		require.Equal(t, width, progress.WindowBase(), "terminal first hole: %#v", progress)
		require.Empty(t, progress.Missing, "terminal first hole: %#v", progress)
		progress, err = ResolveDeliveryProgress(width+1, []SequenceRange{{First: 2, Last: width + 1}}, terminal, 0)
		require.NoError(t, err, "permanent hole pinned receive window")
		require.Equal(t, int64(0), progress.HighestContiguous, "permanent hole pinned receive window")
		require.Equal(t, width+1, progress.SettledThrough, "permanent hole pinned receive window")
	}
	progress, err := ResolveDeliveryProgress(7, []SequenceRange{{First: 1, Last: 2}, {First: 4, Last: 6}}, []SequenceRange{{First: 3, Last: 3}}, 0)
	require.NoError(t, err, "middle hole: %#v", progress)
	require.Equal(t, int64(2), progress.HighestContiguous, "middle hole: %#v", progress)
	require.Equal(t, int64(6), progress.SettledThrough, "middle hole: %#v", progress)
	require.Len(t, progress.Missing, 1, "middle hole: %#v", progress)
	require.Equal(t, SequenceRange{7, 7}, progress.Missing[0], "middle hole: %#v", progress)
	expired, err := ResolveDeliveryProgress(7, []SequenceRange{{First: 1, Last: 2}, {First: 4, Last: 6}}, []SequenceRange{{First: 3, Last: 3}}, 7)
	require.NoError(t, err, "expiry erased receipt distinction")
	require.Equal(t, int64(2), expired.HighestContiguous, "expiry erased receipt distinction")
	require.Equal(t, int64(7), expired.SettledThrough, "expiry erased receipt distinction")
	require.Equal(t, int64(6), expired.HighestSeen, "expiry erased receipt distinction")
	require.Empty(t, expired.Missing, "expiry erased receipt distinction")
	_, err = ResolveDeliveryProgress(3, []SequenceRange{{1, 3}}, []SequenceRange{{2, 2}}, 0)
	require.ErrorIs(t, err, ErrDeliveryConflict, "missing range overlaps actual content")
}
func TestDeliveryClosureFinalBoundaryAndExpiry(t *testing.T) {
	at := time.Date(2026, 9, 8, 1, 0, 0, 0, time.UTC)
	closure, err := (DeliveryClosure{}).Close(true, DeliveryClosedParticipation, 10, at, nil)
	require.NoError(t, err, "invalid initial closure")
	require.True(t, closure.UnknownTail, "invalid initial closure")
	require.Nil(t, closure.FinalSequence, "invalid initial closure")
	require.True(t, closure.UploadExpiresAt.Equal(at.Add(24*time.Hour)), "invalid initial closure")
	repeated, err := closure.Close(true, DeliveryClosedSuspension, 100, at.Add(time.Hour), nil)
	require.NoError(t, err, "closure retry extended history")
	require.Equal(t, int64(10), *repeated.KnownAtClose, "closure retry extended history")
	require.True(t, repeated.ClosedAt.Equal(at), "closure retry extended history")
	require.True(t, repeated.UploadExpiresAt.Equal(*closure.UploadExpiresAt), "closure retry extended history")
	declared, delta, err := closure.DeclareFinal(true, 0, 10, false, FinalDeliveryDeclaration{DeclarationID: "final", FinalSequence: 15}, at.Add(time.Second))
	require.NoError(t, err, "final boundary: %#v", declared)
	require.Equal(t, int64(5), delta, "final boundary: %#v", declared)
	require.Equal(t, int64(15), *declared.FinalSequence, "final boundary: %#v", declared)
	require.False(t, declared.UnknownTail, "final boundary: %#v", declared)
	require.Equal(t, "client_declared", *declared.FinalBoundaryOrigin, "final boundary: %#v", declared)
	for _, final := range []int64{9, 10 + NativeReceiveWindow + 1} {
		_, _, err = closure.DeclareFinal(true, 0, 10, false, FinalDeliveryDeclaration{DeclarationID: "bad", FinalSequence: final}, at)
		require.Error(t, err, "invalid tail accepted")
	}
	_, _, err = closure.DeclareFinal(true, 0, 10, true, FinalDeliveryDeclaration{DeclarationID: "stopped", FinalSequence: 11}, at)
	require.Error(t, err, "summary-only boundary raised")
	expired, err := closure.Expire(true, *closure.UploadExpiresAt)
	require.NoError(t, err, "undeclared tail claimed complete")
	require.Equal(t, int64(10), *expired.FinalSequence, "undeclared tail claimed complete")
	require.Nil(t, expired.FinalDeclarationID, "undeclared tail claimed complete")
	require.Equal(t, "server_known", *expired.FinalBoundaryOrigin, "undeclared tail claimed complete")
	require.True(t, expired.UnknownTail, "undeclared tail claimed complete")
	declaredExpired, err := declared.Expire(true, *closure.UploadExpiresAt)
	require.NoError(t, err, "expiry changed client boundary")
	require.Equal(t, int64(15), *declaredExpired.FinalSequence, "expiry changed client boundary")
	require.Equal(t, "client_declared", *declaredExpired.FinalBoundaryOrigin, "expiry changed client boundary")
	earlier := at.Add(time.Hour)
	bounded, err := (DeliveryClosure{}).Close(false, DeliveryClosedRuntimeReset, 0, at, &earlier)
	require.NoError(t, err, "retirement did not bound upload")
	require.True(t, bounded.UploadExpiresAt.Equal(earlier), "retirement did not bound upload")
	_, err = (DeliveryClosure{}).Close(true, DeliveryClosedRuntimeReset, 0, at, nil)
	require.Error(t, err, "native accepted browser closure reason")
}
func TestDeliveryGapWindowAndRetainedContent(t *testing.T) {
	at := time.Date(2026, 9, 8, 1, 0, 0, 0, time.UTC)
	received := []SequenceRange{{2, 1024}}
	progress, err := ResolveDeliveryProgress(1024, received, []SequenceRange{}, 0)
	require.NoError(t, err)
	gap := DeclareDeliveryGaps{DeclarationID: "lost", AllocatedThroughSequence: 1024, Ranges: []SequenceRange{{1, 1}}, Reason: "spool_lost"}
	require.NoError(t, ValidateDeliveryGapPlacement(true, progress, 1024, 0, DeliveryClosure{}, false, gap, received, nil, at))
	gap.Ranges = []SequenceRange{{2, 2}}
	require.Error(t, ValidateDeliveryGapPlacement(true, progress, 1024, 0, DeliveryClosure{}, false, gap, received, nil, at), "declaration reclaimed retained content")
	gap.Ranges = []SequenceRange{{1025, 1025}}
	gap.AllocatedThroughSequence = 1025
	require.Error(t, ValidateDeliveryGapPlacement(true, progress, 1024, 0, DeliveryClosure{}, false, gap, received, nil, at), "declaration exceeded current receive window")
}
