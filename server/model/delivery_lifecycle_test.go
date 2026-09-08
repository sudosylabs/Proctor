// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"errors"
	"testing"
	"time"
)

func TestDeliverySettlementDoesNotInventReceipts(t *testing.T) {
	for _, width := range []int64{NativeReceiveWindow, BrowserReceiveWindow} {
		received := []SequenceRange{{First: 2, Last: width}}
		terminal := []SequenceRange{{First: 1, Last: 1}}
		progress, err := ResolveDeliveryProgress(width, received, terminal, 0)
		if err != nil || progress.HighestContiguous != 0 || progress.SettledThrough != width || progress.HighestSeen != width || progress.WindowBase() != width || len(progress.Missing) != 0 {
			t.Fatalf("terminal first hole: %#v %v", progress, err)
		}
		progress, err = ResolveDeliveryProgress(width+1, []SequenceRange{{First: 2, Last: width + 1}}, terminal, 0)
		if err != nil || progress.HighestContiguous != 0 || progress.SettledThrough != width+1 {
			t.Fatal("permanent hole pinned receive window")
		}
	}
	progress, err := ResolveDeliveryProgress(7, []SequenceRange{{First: 1, Last: 2}, {First: 4, Last: 6}}, []SequenceRange{{First: 3, Last: 3}}, 0)
	if err != nil || progress.HighestContiguous != 2 || progress.SettledThrough != 6 || len(progress.Missing) != 1 || progress.Missing[0] != (SequenceRange{7, 7}) {
		t.Fatalf("middle hole: %#v %v", progress, err)
	}
	expired, err := ResolveDeliveryProgress(7, []SequenceRange{{First: 1, Last: 2}, {First: 4, Last: 6}}, []SequenceRange{{First: 3, Last: 3}}, 7)
	if err != nil || expired.HighestContiguous != 2 || expired.SettledThrough != 7 || expired.HighestSeen != 6 || len(expired.Missing) != 0 {
		t.Fatal("expiry erased receipt distinction")
	}
	if _, err = ResolveDeliveryProgress(3, []SequenceRange{{1, 3}}, []SequenceRange{{2, 2}}, 0); !errors.Is(err, ErrDeliveryConflict) {
		t.Fatal("missing range overlaps actual content")
	}
}
func TestDeliveryClosureFinalBoundaryAndExpiry(t *testing.T) {
	at := time.Date(2026, 9, 8, 1, 0, 0, 0, time.UTC)
	closure, err := (DeliveryClosure{}).Close(true, DeliveryClosedParticipation, 10, at, nil)
	if err != nil || !closure.UnknownTail || closure.FinalSequence != nil || !closure.UploadExpiresAt.Equal(at.Add(24*time.Hour)) {
		t.Fatal("invalid initial closure")
	}
	repeated, err := closure.Close(true, DeliveryClosedSuspension, 100, at.Add(time.Hour), nil)
	if err != nil || *repeated.KnownAtClose != 10 || !repeated.ClosedAt.Equal(at) || !repeated.UploadExpiresAt.Equal(*closure.UploadExpiresAt) {
		t.Fatal("closure retry extended history")
	}
	declared, delta, err := closure.DeclareFinal(true, 0, 10, false, FinalDeliveryDeclaration{DeclarationID: "final", FinalSequence: 15}, at.Add(time.Second))
	if err != nil || delta != 5 || *declared.FinalSequence != 15 || declared.UnknownTail || *declared.FinalBoundaryOrigin != "client_declared" {
		t.Fatalf("final boundary: %#v %d %v", declared, delta, err)
	}
	for _, final := range []int64{9, 10 + NativeReceiveWindow + 1} {
		if _, _, err := closure.DeclareFinal(true, 0, 10, false, FinalDeliveryDeclaration{DeclarationID: "bad", FinalSequence: final}, at); err == nil {
			t.Fatal("invalid tail accepted")
		}
	}
	if _, _, err := closure.DeclareFinal(true, 0, 10, true, FinalDeliveryDeclaration{DeclarationID: "stopped", FinalSequence: 11}, at); err == nil {
		t.Fatal("summary-only boundary raised")
	}
	expired, err := closure.Expire(true, *closure.UploadExpiresAt)
	if err != nil || *expired.FinalSequence != 10 || expired.FinalDeclarationID != nil || *expired.FinalBoundaryOrigin != "server_known" || !expired.UnknownTail {
		t.Fatal("undeclared tail claimed complete")
	}
	declaredExpired, err := declared.Expire(true, *closure.UploadExpiresAt)
	if err != nil || *declaredExpired.FinalSequence != 15 || *declaredExpired.FinalBoundaryOrigin != "client_declared" {
		t.Fatal("expiry changed client boundary")
	}
	earlier := at.Add(time.Hour)
	bounded, err := (DeliveryClosure{}).Close(false, DeliveryClosedRuntimeReset, 0, at, &earlier)
	if err != nil || !bounded.UploadExpiresAt.Equal(earlier) {
		t.Fatal("retirement did not bound upload")
	}
	if _, err := (DeliveryClosure{}).Close(true, DeliveryClosedRuntimeReset, 0, at, nil); err == nil {
		t.Fatal("native accepted browser closure reason")
	}
}
func TestDeliveryGapWindowAndRetainedContent(t *testing.T) {
	at := time.Date(2026, 9, 8, 1, 0, 0, 0, time.UTC)
	received := []SequenceRange{{2, 1024}}
	progress, err := ResolveDeliveryProgress(1024, received, []SequenceRange{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	gap := DeclareDeliveryGaps{DeclarationID: "lost", AllocatedThroughSequence: 1024, Ranges: []SequenceRange{{1, 1}}, Reason: "spool_lost"}
	if err := ValidateDeliveryGapPlacement(true, progress, 1024, 0, DeliveryClosure{}, false, gap, received, nil, at); err != nil {
		t.Fatal(err)
	}
	gap.Ranges = []SequenceRange{{2, 2}}
	if err := ValidateDeliveryGapPlacement(true, progress, 1024, 0, DeliveryClosure{}, false, gap, received, nil, at); err == nil {
		t.Fatal("declaration reclaimed retained content")
	}
	gap.Ranges = []SequenceRange{{1025, 1025}}
	gap.AllocatedThroughSequence = 1025
	if err := ValidateDeliveryGapPlacement(true, progress, 1024, 0, DeliveryClosure{}, false, gap, received, nil, at); err == nil {
		t.Fatal("declaration exceeded current receive window")
	}
}
