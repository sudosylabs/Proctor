// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDefaultExamPolicySet(t *testing.T) {
	t.Parallel()
	policy := DefaultExamPolicySet()
	require.NoError(t, policy.Validate())
	require.Equal(t, ExamPolicySchemaVersion, policy.SchemaVersion)
	require.Equal(t, IntegrityOutcomeFlagAndSuspend, policy.ConnectionLoss.Outcome)
	require.True(t, policy.FocusLoss.Enabled)
	require.Equal(t, 2*time.Second, policy.FocusLoss.MinimumDuration)
	require.Equal(t, 3, policy.FocusLoss.IncidentCount)
	require.Equal(t, 5*time.Minute, policy.FocusLoss.Window)
	require.Equal(t, IntegrityOutcomeFlagAndWarn, policy.FocusLoss.Outcome)
}

func TestExamPolicySetCanonicalRoundTrip(t *testing.T) {
	t.Parallel()
	want, err := os.ReadFile("testdata/default_exam_policy.json")
	require.NoError(t, err)
	encoded, err := EncodeExamPolicySet(DefaultExamPolicySet())
	require.NoError(t, err)
	require.True(t, bytes.Equal(encoded, want), "encoded = %s", encoded)
	decoded, err := DecodeExamPolicySet(encoded)
	require.NoError(t, err)
	require.True(t, decoded.Equal(DefaultExamPolicySet()))
}

func TestExamPolicySetRejectsUnsafeDocuments(t *testing.T) {
	t.Parallel()
	validBytes, err := os.ReadFile("testdata/default_exam_policy.json")
	require.NoError(t, err)
	valid := string(validBytes)
	tests := map[string]string{
		"unknown version":      strings.Replace(valid, `"native":`, `"schema_version":2,"native":`, 1),
		"unknown field":        strings.Replace(valid, `"native":`, `"future":true,"native":`, 1),
		"duplicate field":      strings.Replace(valid, `"connection_loss":`, `"connection_loss":{},"connection_loss":`, 1),
		"missing policy":       `{"schema_version":1,"connection_loss":{"outcome":"flag_and_suspend"}}`,
		"trailing JSON":        valid + `{}`,
		"zero focus duration":  strings.Replace(valid, `2000`, `0`, 1),
		"overflowing duration": strings.Replace(valid, `2000`, `9223372036854775807`, 1),
	}
	for name, document := range tests {
		name, document := name, document
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, decodeErr := DecodeExamPolicySet([]byte(document))
			require.Error(t, decodeErr, "expected strict decode failure")
		})
	}
	oversized := append([]byte(valid), bytes.Repeat([]byte(" "), ExamPolicySetMaxBytes-len(valid)+1)...)
	_, err = DecodeExamPolicySet(oversized)
	require.Error(t, err, "expected oversized document to fail")
}

func TestExamDraftApplyFocusLossPolicyHonorsExactBoundsAndPreservesConnectionLoss(t *testing.T) {
	t.Parallel()
	at := time.Now().UTC()
	draft, err := NewExamDraft(NewExamID(), "Systems", "", DefaultExamPolicySet(), at)
	require.NoError(t, err)

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
			require.NoError(t, applyErr, "ApplyFocusLossPolicy()")
			require.True(t, changed, "ApplyFocusLossPolicy()")
			require.Equal(t, test.policy, candidate.Policy.FocusLoss, "policy = %#v, original connection loss", candidate.Policy)
			require.Equal(t, draft.Policy.ConnectionLoss, candidate.Policy.ConnectionLoss, "policy = %#v, original connection loss", candidate.Policy)
			require.Equal(t, draft.Revision+1, candidate.Revision, "revision")
		})
	}
}

func TestExamDraftApplyFocusLossPolicyRejectsInvalidAndSkipsNoOp(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 9, 4, 12, 0, 0, 123456789, time.UTC)
	wantUpdatedAt := time.Date(2026, 9, 4, 12, 0, 0, 123456000, time.UTC)
	draft, err := NewExamDraft(NewExamID(), "Systems", "", DefaultExamPolicySet(), at)
	require.NoError(t, err)
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
		changed, applyErr := candidate.ApplyFocusLossPolicy(policy, at.Add(time.Minute))
		require.Error(t, applyErr)
		require.False(t, changed)
		require.Equal(t, *draft, candidate, "invalid policy mutated Draft")
	}
	changed, err := draft.ApplyFocusLossPolicy(draft.Policy.FocusLoss, at.Add(time.Minute))
	require.NoError(t, err, "no-op = %v, %v, draft=%#v", changed, err, draft)
	require.False(t, changed, "no-op = %v, %v, draft=%#v", changed, err, draft)
	require.Equal(t, int64(1), draft.Revision, "no-op = %v, %v, draft=%#v", changed, err, draft)
	require.True(t, draft.UpdatedAt.Equal(wantUpdatedAt), "no-op = %v, %v, draft=%#v", changed, err, draft)
}
