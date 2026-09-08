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
	"strings"
	"testing"
	"time"
)

func TestNativeDeliveryClosedRecords(t *testing.T) {
	at := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	occurrence := NativeOccurrence{Kind: "occurrence", OccurrenceID: NewId(), ConditionID: "capture.active", DetectorID: "capture", DetectorVersion: 1, CapabilityID: "capture", Mode: NativeClaimObserve, Status: "opened", FirstObservedAt: at, LastConfirmedAt: at, RepeatCount: 1, Certainty: "complete", SourceRanges: []NativeSourceRange{{SourceID: NativeSourceCapture, SourceInstanceID: NewId(), FirstSequence: 0, LastSequence: 1}}}
	batch := NativeSecurityBatch{StreamID: NewId(), BatchSequence: 1, ParticipationID: NewAttemptParticipationID(), Generation: 1, SecuritySessionID: NewId(), PolicyDigest: SHA256Fingerprint([]byte("policy")), ApplicationReleaseID: "release", MatrixID: "matrix", Records: []NativeRecord{{Occurrence: &occurrence}}}
	raw, err := batch.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	var roundtrip NativeSecurityBatch
	if err := json.Unmarshal(raw, &roundtrip); err != nil {
		t.Fatal(err)
	}
	encoded, err := roundtrip.Canonical()
	if err != nil || !bytes.Equal(raw, encoded) {
		t.Fatalf("roundtrip mismatch: %v", err)
	}
	for _, mutation := range []func(string) string{
		func(s string) string {
			return strings.Replace(s, `"occurrence_id":`, `"raw_inventory":"secret","occurrence_id":`, 1)
		},
		func(s string) string { return strings.Replace(s, `"batch_sequence":1`, `"batch_sequence":1.0`, 1) },
		func(s string) string { return strings.Replace(s, `"generation":1`, `"generation":1,"generation":1`, 1) },
		func(s string) string { return strings.Replace(s, `"kind":"occurrence"`, `"kind":"arbitrary"`, 1) },
		func(s string) string {
			return strings.Replace(s, `"repeat_count":1`, `"repeat_count":9007199254740992`, 1)
		},
		func(s string) string { return strings.Replace(s, `"prior_acknowledgement":0,`, ``, 1) },
		func(s string) string { return strings.Replace(s, `"gap_count":0`, `"Gap_count":0`, 1) },
	} {
		var invalid NativeSecurityBatch
		if json.Unmarshal([]byte(mutation(string(raw))), &invalid) == nil {
			t.Fatal("accepted non-contract batch")
		}
	}
	gap := NativeSourceGap{Kind: "source_gap", SourceID: NativeSourceCapture, SourceInstanceID: NewId(), Reason: "overflow", OccurredAt: at}
	if (NativeRecord{Occurrence: &occurrence, Gap: &gap}).Validate() == nil {
		t.Fatal("accepted ambiguous union")
	}
	batch.Records = nil
	if batch.Validate() == nil {
		t.Fatal("accepted empty batch")
	}
}

func TestUnretainedDeliverySummaryPreservesUncertainty(t *testing.T) {
	prior := UnretainedDeliverySummary{SummarySequence: 1, UnretainedRecordCount: 7, CountComplete: false}
	if replay, err := prior.Compare(prior); !replay || err != nil {
		t.Fatalf("replay: %v %v", replay, err)
	}
	for _, next := range []UnretainedDeliverySummary{
		{SummarySequence: 2, UnretainedRecordCount: 6, CountComplete: false},
		{SummarySequence: 2, UnretainedRecordCount: 8, CountComplete: true},
		{SummarySequence: 1, UnretainedRecordCount: 8, CountComplete: false},
	} {
		if _, err := prior.Compare(next); err == nil {
			t.Fatal("lost uncertainty or accepted conflicting summary")
		}
	}
	count, complete, err := SaturatingDeliveryCount(9007199254740990, 2, true)
	if err != nil || count != 9007199254740991 || complete {
		t.Fatalf("overflow: %d %v %v", count, complete, err)
	}
	var decoded UnretainedDeliverySummary
	if json.Unmarshal([]byte(`{"summary_sequence":1,"unretained_record_count":0,"count_complete":false}`), &decoded) == nil {
		t.Fatal("accepted missing explicit nullable members")
	}
}

func TestNativeOccurrenceContinuity(t *testing.T) {
	at := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	opened := NativeOccurrence{Kind: "occurrence", OccurrenceID: NewId(), ConditionID: "capture.active", DetectorID: "capture", DetectorVersion: 1, CapabilityID: "capture", Mode: NativeClaimObserve, Status: "opened", FirstObservedAt: at, LastConfirmedAt: at, RepeatCount: 1, Certainty: "complete", SourceRanges: []NativeSourceRange{{SourceID: NativeSourceCapture, SourceInstanceID: NewId(), FirstSequence: 1, LastSequence: 2}}}
	prior, err := AdvanceNativeOccurrence(nil, opened, false)
	if err != nil {
		t.Fatal(err)
	}
	repeat := cloneNativeOccurrence(opened)
	repeat.Status = "repeated"
	repeat.RepeatCount = 2
	repeat.SourceRanges[0].LastSequence = 3
	// Local clock rollback is provenance, not a source-lifetime ordering.
	repeat.LastConfirmedAt = at.Add(-time.Second)
	next, err := AdvanceNativeOccurrence(&prior, repeat, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*NativeOccurrence){
		func(o *NativeOccurrence) { o.SourceRanges[0].SourceInstanceID = NewId() },
		func(o *NativeOccurrence) { o.SourceRanges[0].FirstSequence++ },
		func(o *NativeOccurrence) { o.SourceRanges[0].LastSequence = 1 },
		func(o *NativeOccurrence) { o.RepeatCount = 1 },
		func(o *NativeOccurrence) { o.ConditionID = "capture.other" },
		func(o *NativeOccurrence) { o.Status = "opened" },
	} {
		invalid := cloneNativeOccurrence(repeat)
		mutate(&invalid)
		if _, err := AdvanceNativeOccurrence(&next, invalid, false); err == nil {
			t.Fatal("accepted incompatible occurrence transition")
		}
	}
	recovered := cloneNativeOccurrence(repeat)
	recovered.Status = "recovered"
	closed, err := AdvanceNativeOccurrence(&next, recovered, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AdvanceNativeOccurrence(&closed, recovered, true); err == nil {
		t.Fatal("recovered twice")
	}
	if _, err := AdvanceNativeOccurrence(nil, recovered, false); err == nil {
		t.Fatal("invented opener without a preceding hole")
	}
	unresolved, err := AdvanceNativeOccurrence(nil, repeat, true)
	if err != nil || !unresolved.UnresolvedOpener {
		t.Fatal("lost missing opener provenance", err)
	}
	unresolved, err = AdvanceNativeOccurrence(&unresolved, recovered, false)
	if err != nil || !unresolved.UnresolvedOpener {
		t.Fatal("recovery promoted missing opener to proven condition", err)
	}
	recovered.SourceRanges[0].LastSequence = 99
	if unresolved.Latest.SourceRanges[0].LastSequence == 99 {
		t.Fatal("caller mutated retained range")
	}
}
