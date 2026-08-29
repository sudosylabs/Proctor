// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import "testing"

func TestBrowserActivitySubmissionClosedVariants(t *testing.T) {
	t.Parallel()

	sourceID := BrowserSourceSessionID("018f47a0-6e53-4cc4-9d0b-97c9b6d98011")
	finalSequence := int64(7)
	tests := []struct {
		name        string
		value       BrowserActivitySubmission
		clientValid bool
	}{
		{name: "not applicable", value: BrowserActivitySubmission{State: BrowserActivitySubmissionNotApplicable}, clientValid: true},
		{name: "complete", value: BrowserActivitySubmission{State: BrowserActivitySubmissionComplete,
			SourceSessionID: sourceID, FinalSequence: &finalSequence}, clientValid: true},
		{name: "client gap", value: BrowserActivitySubmission{State: BrowserActivitySubmissionGapped,
			SourceSessionID: sourceID, FinalSequence: &finalSequence, GapReason: BrowserActivityGapDeliveryIncomplete}, clientValid: true},
		{name: "automatic gap", value: BrowserActivitySubmission{State: BrowserActivitySubmissionGapped,
			SourceSessionID: sourceID, GapReason: BrowserActivityGapSourceNotFinalized}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := test.value.Validate(); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
			if err := test.value.ValidateClient(); (err == nil) != test.clientValid {
				t.Fatalf("ValidateClient() error = %v, clientValid = %t", err, test.clientValid)
			}
			clone := test.value.Clone()
			if clone.FinalSequence != nil && clone.FinalSequence == test.value.FinalSequence {
				t.Fatal("Clone() retained the caller's sequence pointer")
			}
		})
	}
}

func TestBrowserActivitySubmissionRejectsMixedOrInvalidStates(t *testing.T) {
	t.Parallel()

	sourceID := BrowserSourceSessionID("018f47a0-6e53-4cc4-9d0b-97c9b6d98011")
	zero := int64(0)
	values := []BrowserActivitySubmission{
		{},
		{State: BrowserActivitySubmissionNotApplicable, SourceSessionID: sourceID},
		{State: BrowserActivitySubmissionComplete, SourceSessionID: sourceID},
		{State: BrowserActivitySubmissionComplete, SourceSessionID: sourceID, FinalSequence: &zero},
		{State: BrowserActivitySubmissionGapped, SourceSessionID: sourceID},
		{State: BrowserActivitySubmissionGapped, SourceSessionID: sourceID, GapReason: "future_reason"},
	}
	for _, value := range values {
		if err := value.Validate(); err == nil {
			t.Fatalf("Validate(%#v) succeeded", value)
		}
	}
}
