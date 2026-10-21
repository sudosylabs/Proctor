// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
)

func TestOperationalEventProjectsOnlyBoundedOutcome(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, want string
		err        error
	}{
		{name: "success", want: "success"},
		{name: "denied", err: apperror.New("authorization.denied").Wrap(errors.New("private detail")), want: "denied"},
		{name: "credentials", err: apperror.New("authentication.credentials_invalid"), want: "rejected"},
		{name: "rate limit", err: apperror.New("authentication.rate_limited"), want: "rate_limited"},
		{name: "unknown", err: errors.New("must-not-become-a-label"), want: "error"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			event := operationalEvent("authentication", "login", test.err)
			require.Equal(t, "authentication", event.Subsystem(), "event")
			require.Equal(t, "login", event.Event(), "event")
			require.Equal(t, test.want, event.Outcome(), "event")
		})
	}
}
