// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"bytes"
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
