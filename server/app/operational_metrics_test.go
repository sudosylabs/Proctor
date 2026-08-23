// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"errors"
	"testing"
)

func TestOperationalEventProjectsOnlyBoundedOutcome(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, want string
		err        error
	}{
		{name: "success", want: "success"},
		{name: "denied", err: NewError("authorization.denied").Wrap(errors.New("private detail")), want: "denied"},
		{name: "credentials", err: NewError("authentication.invalid_credentials"), want: "rejected"},
		{name: "rate limit", err: NewError("authentication.rate_limited"), want: "rate_limited"},
		{name: "unknown", err: errors.New("must-not-become-a-label"), want: "error"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			event := operationalEvent("authentication", "login", test.err)
			if event.Subsystem() != "authentication" || event.Event() != "login" || event.Outcome() != test.want {
				t.Fatalf("event = (%q, %q, %q)", event.Subsystem(), event.Event(), event.Outcome())
			}
		})
	}
}
