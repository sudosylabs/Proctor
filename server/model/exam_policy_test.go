// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDefaultExamPolicySet(t *testing.T) {
	t.Parallel()
	policy := DefaultExamPolicySet()
	if err := policy.Validate(); err != nil {
		t.Fatalf("default policy: %v", err)
	}
	if policy.SchemaVersion != ExamPolicySchemaVersion || policy.ConnectionLoss.Outcome != IntegrityOutcomeFlagAndSuspend {
		t.Fatalf("unexpected connection policy: %#v", policy)
	}
	if !policy.FocusLoss.Enabled || policy.FocusLoss.MinimumDuration != 2*time.Second || policy.FocusLoss.IncidentCount != 3 || policy.FocusLoss.Window != 5*time.Minute || policy.FocusLoss.Outcome != IntegrityOutcomeFlagAndWarn {
		t.Fatalf("unexpected focus policy: %#v", policy.FocusLoss)
	}
}

func TestExamPolicySetCanonicalRoundTrip(t *testing.T) {
	t.Parallel()
	want := []byte(`{"schema_version":1,"connection_loss":{"outcome":"flag_and_suspend"},"focus_loss":{"enabled":true,"minimum_duration_milliseconds":2000,"incident_count":3,"window_milliseconds":300000,"outcome":"flag_and_warn"}}`)
	encoded, err := EncodeExamPolicySet(DefaultExamPolicySet())
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if !bytes.Equal(encoded, want) {
		t.Fatalf("encoded = %s", encoded)
	}
	decoded, err := DecodeExamPolicySet(encoded)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded != DefaultExamPolicySet() {
		t.Fatalf("round trip = %#v", decoded)
	}
}

func TestExamPolicySetRejectsUnsafeDocuments(t *testing.T) {
	t.Parallel()
	valid := `{"schema_version":1,"connection_loss":{"outcome":"flag_and_suspend"},"focus_loss":{"enabled":true,"minimum_duration_milliseconds":2000,"incident_count":3,"window_milliseconds":300000,"outcome":"flag_and_warn"}}`
	tests := map[string]string{
		"unknown version":      strings.Replace(valid, `"schema_version":1`, `"schema_version":2`, 1),
		"unknown field":        strings.Replace(valid, `"schema_version":1`, `"schema_version":1,"future":true`, 1),
		"duplicate field":      strings.Replace(valid, `"schema_version":1`, `"schema_version":1,"schema_version":1`, 1),
		"missing policy":       `{"schema_version":1,"connection_loss":{"outcome":"flag_and_suspend"}}`,
		"trailing JSON":        valid + `{}`,
		"zero focus duration":  strings.Replace(valid, `2000`, `0`, 1),
		"overflowing duration": strings.Replace(valid, `2000`, `9223372036854775807`, 1),
	}
	for name, document := range tests {
		name, document := name, document
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := DecodeExamPolicySet([]byte(document)); err == nil {
				t.Fatal("expected strict decode failure")
			}
		})
	}
	oversized := append([]byte(valid), bytes.Repeat([]byte(" "), ExamPolicySetMaxBytes-len(valid)+1)...)
	if _, err := DecodeExamPolicySet(oversized); err == nil {
		t.Fatal("expected oversized document to fail")
	}
}

func TestExamDraftApplyFocusLossPolicyHonorsExactBoundsAndPreservesConnectionLoss(t *testing.T) {
	t.Parallel()
	at := time.Now().UTC()
	draft, err := NewExamDraft(NewExamID(), "Systems", "", DefaultExamPolicySet(), at)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		policy FocusLossPolicy
	}{
		{name: "minimums", policy: FocusLossPolicy{Enabled: false, MinimumDuration: 500 * time.Millisecond, IncidentCount: 1, Window: 10 * time.Second, Outcome: IntegrityOutcomeFlag}},
		{name: "maximums", policy: FocusLossPolicy{Enabled: true, MinimumDuration: 5 * time.Minute, IncidentCount: 100, Window: 4 * time.Hour, Outcome: IntegrityOutcomeFlagAndSuspend}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := *draft
			changed, applyErr := candidate.ApplyFocusLossPolicy(test.policy, at.Add(time.Minute))
			if applyErr != nil || !changed {
				t.Fatalf("ApplyFocusLossPolicy() = %v, %v", changed, applyErr)
			}
			if candidate.Policy.FocusLoss != test.policy || candidate.Policy.ConnectionLoss != draft.Policy.ConnectionLoss {
				t.Fatalf("policy = %#v, original connection loss = %#v", candidate.Policy, draft.Policy.ConnectionLoss)
			}
			if candidate.Revision != draft.Revision+1 {
				t.Fatalf("revision = %d, want %d", candidate.Revision, draft.Revision+1)
			}
		})
	}
}

func TestExamDraftApplyFocusLossPolicyRejectsInvalidAndSkipsNoOp(t *testing.T) {
	t.Parallel()
	at := time.Now().UTC()
	draft, err := NewExamDraft(NewExamID(), "Systems", "", DefaultExamPolicySet(), at)
	if err != nil {
		t.Fatal(err)
	}
	invalid := []FocusLossPolicy{
		{Enabled: true, MinimumDuration: 499 * time.Millisecond, IncidentCount: 1, Window: 10 * time.Second, Outcome: IntegrityOutcomeFlag},
		{Enabled: true, MinimumDuration: 5*time.Minute + time.Millisecond, IncidentCount: 1, Window: 10 * time.Minute, Outcome: IntegrityOutcomeFlag},
		{Enabled: true, MinimumDuration: time.Second, IncidentCount: 0, Window: 10 * time.Second, Outcome: IntegrityOutcomeFlag},
		{Enabled: true, MinimumDuration: time.Second, IncidentCount: 101, Window: 10 * time.Second, Outcome: IntegrityOutcomeFlag},
		{Enabled: true, MinimumDuration: time.Second, IncidentCount: 1, Window: 10*time.Second - time.Millisecond, Outcome: IntegrityOutcomeFlag},
		{Enabled: true, MinimumDuration: time.Second, IncidentCount: 1, Window: 4*time.Hour + time.Millisecond, Outcome: IntegrityOutcomeFlag},
		{Enabled: true, MinimumDuration: 20 * time.Second, IncidentCount: 1, Window: 10 * time.Second, Outcome: IntegrityOutcomeFlag},
		{Enabled: true, MinimumDuration: time.Second, IncidentCount: 1, Window: time.Minute, Outcome: "warn_only"},
	}
	for _, policy := range invalid {
		candidate := *draft
		if changed, applyErr := candidate.ApplyFocusLossPolicy(policy, at.Add(time.Minute)); applyErr == nil || changed {
			t.Fatalf("invalid policy accepted: %#v", policy)
		}
		if !reflect.DeepEqual(candidate, *draft) {
			t.Fatal("invalid policy mutated Draft")
		}
	}
	changed, err := draft.ApplyFocusLossPolicy(draft.Policy.FocusLoss, at.Add(time.Minute))
	if err != nil || changed || draft.Revision != 1 || !draft.UpdatedAt.Equal(at) {
		t.Fatalf("no-op = %v, %v, draft=%#v", changed, err, draft)
	}
}
