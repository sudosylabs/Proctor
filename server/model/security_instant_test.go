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

func TestSecurityDocumentsPreserveTimestampSpelling(t *testing.T) {
	_, _, _, report := preflightFixture(t)
	at := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	report.ReportedAt = at
	reset := NativeSourceReset{Kind: "source_reset", ResetID: NewID(), SourceID: NativeSourceCapture, PreviousSourceInstanceID: NewID(), NewSourceInstanceID: NewID(), Reason: "restart", OccurredAt: at}
	occurrence := NativeOccurrence{Kind: "occurrence", OccurrenceID: NewID(), ConditionID: "capture.active", DetectorID: "capture", DetectorVersion: 1, CapabilityID: "capture", Mode: NativeClaimObserve, Status: "opened", FirstObservedAt: at, LastConfirmedAt: at, RepeatCount: 1, Certainty: "complete", SourceRanges: []NativeSourceRange{{SourceID: NativeSourceCapture, SourceInstanceID: NewID(), FirstSequence: 0, LastSequence: 1}}}
	records := []NativeRecord{{Occurrence: &occurrence}, {Reset: &reset}, {Gap: &NativeSourceGap{Kind: "source_gap", SourceID: NativeSourceCapture, SourceInstanceID: NewID(), Reason: "overflow", OccurredAt: at}}, {Coverage: &NativeCoverageTransition{Kind: "coverage_transition", Source: report.Sources[0], Reason: "initial", OccurredAt: at}}}
	batch := NativeSecurityBatch{StreamID: NewID(), BatchSequence: 1, ParticipationID: NewAttemptParticipationID(), Generation: 1, SecuritySessionID: NewID(), PolicyDigest: report.PolicyDigest, ApplicationReleaseID: "release", MatrixID: "matrix", Records: records}
	control := SecurityCoverageRenewal{ControlSequence: 1, PolicyDigest: report.PolicyDigest, SecuritySessionID: NewID(), StreamID: NewID(), Posture: report.Posture, Sources: report.Sources, Coverage: report.Coverage, SourceResets: []NativeSourceReset{reset}, DeliveryWatermarks: []DeliveryWatermark{}}
	browser := BrowserActivityEvent{Sequence: 1, Kind: BrowserActivityBlockedNavigation, PolicyRevisionID: NewExamRevisionID(), ClientOccurredAt: at, Location: &BrowserLocation{Scheme: "file"}, BlockReason: func() *BrowserActivityBlockReason { v := BrowserBlockSchemeNotAllowed; return &v }()}
	cases := []struct {
		name  string
		value any
		fresh func() any
	}{
		{"preflight", report, func() any { return new(SecurityPreflightReport) }},
		{"batch", batch, func() any { return new(NativeSecurityBatch) }},
		{"control", control, func() any { return new(SecurityCoverageRenewal) }},
		{"browser", browser, func() any { return new(BrowserActivityEvent) }},
		{"summary", UnretainedDeliverySummary{SummarySequence: 1, FirstUnretainedAt: &at, LastUnretainedAt: &at}, func() any { return new(UnretainedDeliverySummary) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := encodeCanonicalExamDocument(tc.value)
			require.NoError(t, err)
			for _, suffix := range []string{"Z", ".000Z", ".100Z", ".123Z", ".100000000000Z"} {
				supplied := []byte(strings.ReplaceAll(string(raw), "2026-09-09T12:00:00Z", "2026-09-09T12:00:00"+suffix))
				decoded := tc.fresh()
				require.NoError(t, json.Unmarshal(supplied, decoded), "%s", suffix)
				actual, err := encodeCanonicalExamDocument(decoded)
				require.NoError(t, err, "%s changed hashed document: %s / %s (%v)", suffix, supplied, actual, err)
				require.True(t, bytes.Equal(supplied, actual), "%s changed hashed document: %s / %s (%v)", suffix, supplied, actual, err)
			}
		})
	}
}

func TestSecurityTimestampMutationAndExactReplay(t *testing.T) {
	raw := []byte(`{"count_complete":false,"first_unretained_at":"2026-09-09T12:00:00.100Z","last_unretained_at":null,"summary_sequence":1,"unretained_record_count":1}`)
	var original UnretainedDeliverySummary
	require.NoError(t, json.Unmarshal(raw, &original))
	var equivalent UnretainedDeliverySummary
	require.NoError(t, json.Unmarshal(bytes.Replace(raw, []byte(".100Z"), []byte(".1Z"), 1), &equivalent))
	_, err := original.Compare(equivalent)
	require.Equal(t, ErrDeliveryConflict, err)
	changed := original
	next := original.FirstUnretainedAt.Add(time.Second)
	changed.FirstUnretainedAt = &next
	updated, err := changed.Canonical()
	require.NoError(t, err)
	require.False(t, bytes.Contains(updated, []byte("12:00:00.100Z")))
	require.True(t, bytes.Contains(updated, []byte("12:00:01.1Z")))
	unchanged, _ := original.Canonical()
	require.True(t, bytes.Equal(raw, unchanged), "copy changed original timestamp")
	for _, bad := range []string{"null", `"invalid"`, `"2026-09-09T12:00:00,100Z"`, `"2026-09-09T1:00:00Z"`, `"2026-09-09T12:00:00.000001Z"`, `"2026-09-09T12:00:00.0000000001Z"`} {
		var report SecurityPreflightReport
		_, _, _, valid := preflightFixture(t)
		encoded, _ := valid.Canonical()
		var members map[string]json.RawMessage
		_ = json.Unmarshal(encoded, &members)
		members["reported_at"] = json.RawMessage(bad)
		encoded, _ = json.Marshal(members)
		require.Error(t, json.Unmarshal(encoded, &report), "accepted invalid required instant %s", bad)
	}
}

func TestEffectiveSecurityPolicyPreservesTimestampDigest(t *testing.T) {
	_, resolved, _, _ := preflightFixture(t)
	policy := resolved.Policy
	raw, err := json.Marshal(policy)
	require.NoError(t, err)
	var document map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &document))
	for _, field := range []string{"issued_at", "active_from"} {
		var instant time.Time
		require.NoError(t, json.Unmarshal(document[field], &instant))
		document[field], _ = json.Marshal(instant.Format("2006-01-02T15:04:05.000Z"))
	}
	delete(document, "digest")
	canonical, err := encodeCanonicalExamDocument(document)
	require.NoError(t, err)
	document["digest"], _ = json.Marshal(SHA256Fingerprint(canonical))
	raw, err = encodeCanonicalExamDocument(document)
	require.NoError(t, err)
	var decoded EffectiveExamSecurityPolicy
	require.NoError(t, json.Unmarshal(raw, &decoded))
	encoded, err := encodeCanonicalExamDocument(decoded)
	require.NoError(t, err)
	require.True(t, bytes.Equal(raw, encoded))
	rebound, err := decoded.RebindAttempt(NewExamAttemptID(), decoded.ActiveFrom)
	require.NoError(t, err)
	after, _ := rebound.document("digest", "scope")
	before, _ := decoded.document("digest", "scope")
	beforeRaw, _ := encodeCanonicalExamDocument(before)
	afterRaw, _ := encodeCanonicalExamDocument(after)
	require.True(t, bytes.Equal(beforeRaw, afterRaw), "scope rebinding changed timestamp spelling or failed to change digest")
	require.NotEqual(t, decoded.Digest, rebound.Digest, "scope rebinding changed timestamp spelling or failed to change digest")
}
