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

func TestSecurityDocumentsPreserveTimestampSpelling(t *testing.T) {
	_, _, _, report := preflightFixture(t)
	at := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	report.ReportedAt = at
	reset := NativeSourceReset{Kind: "source_reset", ResetID: NewId(), SourceID: NativeSourceCapture, PreviousSourceInstanceID: NewId(), NewSourceInstanceID: NewId(), Reason: "restart", OccurredAt: at}
	occurrence := NativeOccurrence{Kind: "occurrence", OccurrenceID: NewId(), ConditionID: "capture.active", DetectorID: "capture", DetectorVersion: 1, CapabilityID: "capture", Mode: NativeClaimObserve, Status: "opened", FirstObservedAt: at, LastConfirmedAt: at, RepeatCount: 1, Certainty: "complete", SourceRanges: []NativeSourceRange{{SourceID: NativeSourceCapture, SourceInstanceID: NewId(), FirstSequence: 0, LastSequence: 1}}}
	records := []NativeRecord{{Occurrence: &occurrence}, {Reset: &reset}, {Gap: &NativeSourceGap{Kind: "source_gap", SourceID: NativeSourceCapture, SourceInstanceID: NewId(), Reason: "overflow", OccurredAt: at}}, {Coverage: &NativeCoverageTransition{Kind: "coverage_transition", Source: report.Sources[0], Reason: "initial", OccurredAt: at}}}
	batch := NativeSecurityBatch{StreamID: NewId(), BatchSequence: 1, ParticipationID: NewAttemptParticipationID(), Generation: 1, SecuritySessionID: NewId(), PolicyDigest: report.PolicyDigest, ApplicationReleaseID: "release", MatrixID: "matrix", Records: records}
	control := SecurityCoverageRenewal{ControlSequence: 1, PolicyDigest: report.PolicyDigest, SecuritySessionID: NewId(), StreamID: NewId(), Posture: report.Posture, Sources: report.Sources, Coverage: report.Coverage, SourceResets: []NativeSourceReset{reset}, DeliveryWatermarks: []DeliveryWatermark{}}
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
			if err != nil {
				t.Fatal(err)
			}
			for _, suffix := range []string{"Z", ".000Z", ".100Z", ".123Z", ".100000000000Z"} {
				supplied := []byte(strings.ReplaceAll(string(raw), "2026-09-09T12:00:00Z", "2026-09-09T12:00:00"+suffix))
				decoded := tc.fresh()
				if err := json.Unmarshal(supplied, decoded); err != nil {
					t.Fatalf("%s: %v", suffix, err)
				}
				actual, err := encodeCanonicalExamDocument(decoded)
				if err != nil || !bytes.Equal(supplied, actual) {
					t.Fatalf("%s changed hashed document: %s / %s (%v)", suffix, supplied, actual, err)
				}
			}
		})
	}
}

func TestSecurityTimestampMutationAndExactReplay(t *testing.T) {
	raw := []byte(`{"count_complete":false,"first_unretained_at":"2026-09-09T12:00:00.100Z","last_unretained_at":null,"summary_sequence":1,"unretained_record_count":1}`)
	var original UnretainedDeliverySummary
	if err := json.Unmarshal(raw, &original); err != nil {
		t.Fatal(err)
	}
	var equivalent UnretainedDeliverySummary
	if err := json.Unmarshal(bytes.Replace(raw, []byte(".100Z"), []byte(".1Z"), 1), &equivalent); err != nil {
		t.Fatal(err)
	}
	if _, err := original.Compare(equivalent); err != ErrDeliveryConflict {
		t.Fatalf("different hashed timestamp strings replayed: %v", err)
	}
	changed := original
	next := original.FirstUnretainedAt.Add(time.Second)
	changed.FirstUnretainedAt = &next
	updated, err := changed.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(updated, []byte("12:00:00.100Z")) || !bytes.Contains(updated, []byte("12:00:01.1Z")) {
		t.Fatalf("mutation retained stale timestamp: %s", updated)
	}
	unchanged, _ := original.Canonical()
	if !bytes.Equal(raw, unchanged) {
		t.Fatal("copy changed original timestamp")
	}
	for _, bad := range []string{"null", `"invalid"`, `"2026-09-09T12:00:00,100Z"`, `"2026-09-09T1:00:00Z"`, `"2026-09-09T12:00:00.000001Z"`, `"2026-09-09T12:00:00.0000000001Z"`} {
		var report SecurityPreflightReport
		_, _, _, valid := preflightFixture(t)
		encoded, _ := valid.Canonical()
		var members map[string]json.RawMessage
		_ = json.Unmarshal(encoded, &members)
		members["reported_at"] = json.RawMessage(bad)
		encoded, _ = json.Marshal(members)
		if json.Unmarshal(encoded, &report) == nil {
			t.Fatalf("accepted invalid required instant %s", bad)
		}
	}
}

func TestEffectiveSecurityPolicyPreservesTimestampDigest(t *testing.T) {
	_, resolved, _, _ := preflightFixture(t)
	policy := resolved.Policy
	raw, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"issued_at", "active_from"} {
		var instant time.Time
		if err := json.Unmarshal(document[field], &instant); err != nil {
			t.Fatal(err)
		}
		document[field], _ = json.Marshal(instant.Format("2006-01-02T15:04:05.000Z"))
	}
	delete(document, "digest")
	canonical, err := encodeCanonicalExamDocument(document)
	if err != nil {
		t.Fatal(err)
	}
	document["digest"], _ = json.Marshal(SHA256Fingerprint(canonical))
	raw, err = encodeCanonicalExamDocument(document)
	if err != nil {
		t.Fatal(err)
	}
	var decoded EffectiveExamSecurityPolicy
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	encoded, err := encodeCanonicalExamDocument(decoded)
	if err != nil || !bytes.Equal(raw, encoded) {
		t.Fatalf("policy digest roundtrip changed: %v", err)
	}
	rebound, err := decoded.RebindAttempt(NewExamAttemptID(), decoded.ActiveFrom)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := rebound.document("digest", "scope")
	before, _ := decoded.document("digest", "scope")
	beforeRaw, _ := encodeCanonicalExamDocument(before)
	afterRaw, _ := encodeCanonicalExamDocument(after)
	if !bytes.Equal(beforeRaw, afterRaw) || rebound.Digest == decoded.Digest {
		t.Fatal("scope rebinding changed timestamp spelling or failed to change digest")
	}
}
