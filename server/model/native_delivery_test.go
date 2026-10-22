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

	"github.com/stretchr/testify/require"
)

func TestNativeDeliveryClosedRecords(t *testing.T) {
	at := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	occurrence := NativeOccurrence{Kind: "occurrence", OccurrenceID: NewID(), ConditionID: "capture.active", DetectorID: "capture", DetectorVersion: 1, CapabilityID: "capture", Mode: NativeClaimObserve, Status: "opened", FirstObservedAt: at, LastConfirmedAt: at, RepeatCount: 1, Certainty: "complete", SourceRanges: []NativeSourceRange{{SourceID: NativeSourceCapture, SourceInstanceID: NewID(), FirstSequence: 0, LastSequence: 1}}}
	batch := NativeSecurityBatch{StreamID: NewID(), BatchSequence: 1, ParticipationID: NewAttemptParticipationID(), Generation: 1, SecuritySessionID: NewID(), PolicyDigest: SHA256Fingerprint([]byte("policy")), ApplicationReleaseID: "release", MatrixID: "matrix", Records: []NativeRecord{{Occurrence: &occurrence}}}
	raw, err := batch.Canonical()
	require.NoError(t, err)
	var roundtrip NativeSecurityBatch
	require.NoError(t, json.Unmarshal(raw, &roundtrip))
	encoded, err := roundtrip.Canonical()
	require.NoError(t, err)
	require.True(t, bytes.Equal(raw, encoded))
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
		require.Error(t, json.Unmarshal([]byte(mutation(string(raw))), &invalid), "accepted non-contract batch")
	}
	gap := NativeSourceGap{Kind: "source_gap", SourceID: NativeSourceCapture, SourceInstanceID: NewID(), Reason: "overflow", OccurredAt: at}
	require.Error(t, (NativeRecord{Occurrence: &occurrence, Gap: &gap}).Validate(), "accepted ambiguous union")
	batch.Records = nil
	require.Error(t, batch.Validate(), "accepted empty batch")
}

func TestUnretainedDeliverySummaryPreservesUncertainty(t *testing.T) {
	prior := UnretainedDeliverySummary{SummarySequence: 1, UnretainedRecordCount: 7, CountComplete: false}
	replay, err := prior.Compare(prior)
	require.True(t, replay, "replay")
	require.NoError(t, err, "replay")
	for _, next := range []UnretainedDeliverySummary{
		{SummarySequence: 2, UnretainedRecordCount: 6, CountComplete: false},
		{SummarySequence: 2, UnretainedRecordCount: 8, CountComplete: true},
		{SummarySequence: 1, UnretainedRecordCount: 8, CountComplete: false},
	} {
		_, err = prior.Compare(next)
		require.Error(t, err, "lost uncertainty or accepted conflicting summary")
	}
	var decoded UnretainedDeliverySummary
	require.NoError(t, json.Unmarshal([]byte(`{"summary_sequence":1,"unretained_record_count":0,"count_complete":false}`), &decoded), "rejected omitted absent instants")
	require.Nil(t, decoded.FirstUnretainedAt)
	require.Error(t, json.Unmarshal([]byte(`{"summary_sequence":1,"unretained_record_count":0,"count_complete":false,"first_unretained_at":null}`), &decoded), "accepted an explicit null instant")
}

func TestNativeOccurrenceContinuity(t *testing.T) {
	at := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	opened := NativeOccurrence{Kind: "occurrence", OccurrenceID: NewID(), ConditionID: "capture.active", DetectorID: "capture", DetectorVersion: 1, CapabilityID: "capture", Mode: NativeClaimObserve, Status: "opened", FirstObservedAt: at, LastConfirmedAt: at, RepeatCount: 1, Certainty: "complete", SourceRanges: []NativeSourceRange{{SourceID: NativeSourceCapture, SourceInstanceID: NewID(), FirstSequence: 1, LastSequence: 2}}}
	prior, err := AdvanceNativeOccurrence(nil, opened, false)
	require.NoError(t, err)
	repeat := cloneNativeOccurrence(opened)
	repeat.Status = "repeated"
	repeat.RepeatCount = 2
	repeat.SourceRanges[0].LastSequence = 3
	// Local clock rollback is provenance, not a source-lifetime ordering.
	repeat.LastConfirmedAt = at.Add(-time.Second)
	next, err := AdvanceNativeOccurrence(&prior, repeat, false)
	require.NoError(t, err)
	for _, mutate := range []func(*NativeOccurrence){
		func(o *NativeOccurrence) { o.SourceRanges[0].SourceInstanceID = NewID() },
		func(o *NativeOccurrence) { o.SourceRanges[0].FirstSequence++ },
		func(o *NativeOccurrence) { o.SourceRanges[0].LastSequence = 1 },
		func(o *NativeOccurrence) { o.RepeatCount = 1 },
		func(o *NativeOccurrence) { o.ConditionID = "capture.other" },
		func(o *NativeOccurrence) { o.Status = "opened" },
	} {
		invalid := cloneNativeOccurrence(repeat)
		mutate(&invalid)
		_, err = AdvanceNativeOccurrence(&next, invalid, false)
		require.Error(t, err, "accepted incompatible occurrence transition")
	}
	recovered := cloneNativeOccurrence(repeat)
	recovered.Status = "recovered"
	closed, err := AdvanceNativeOccurrence(&next, recovered, false)
	require.NoError(t, err)
	_, err = AdvanceNativeOccurrence(&closed, recovered, true)
	require.Error(t, err, "recovered twice")
	_, err = AdvanceNativeOccurrence(nil, recovered, false)
	require.Error(t, err, "invented opener without a preceding hole")
	unresolved, err := AdvanceNativeOccurrence(nil, repeat, true)
	require.NoError(t, err, "lost missing opener provenance")
	require.True(t, unresolved.UnresolvedOpener, "lost missing opener provenance")
	unresolved, err = AdvanceNativeOccurrence(&unresolved, recovered, false)
	require.NoError(t, err, "recovery promoted missing opener to proven condition")
	require.True(t, unresolved.UnresolvedOpener, "recovery promoted missing opener to proven condition")
	recovered.SourceRanges[0].LastSequence = 99
	require.NotEqual(t, int64(99), unresolved.Latest.SourceRanges[0].LastSequence, "caller mutated retained range")
}
